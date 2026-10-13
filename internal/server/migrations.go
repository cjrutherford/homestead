package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"

	"homestead/internal/audit"
	"homestead/internal/backup"
	"homestead/internal/catalog"
	"homestead/internal/discover"
	"homestead/internal/docker"
	"homestead/internal/doctor"
	"homestead/internal/operation"
	"homestead/internal/storage"
	"homestead/internal/store"
)

// migrationContainers lists engine containers with mounts, stubbed in
// tests.
var migrationContainers = func(ctx context.Context) ([]docker.Container, error) {
	return docker.NewClient("").Containers(ctx)
}

// Audit actions for app data migration.
const (
	auditMigrateApprove = "app.data.migrate.approve"
	auditMigrateResult  = "app.data.migrate"
	auditVolumesRemove  = "app.data.old-volumes.remove"
)

// MountMigrations serves S277 app data migration: placement detection
// per installed app, the migration plan, approval (an operation), and
// the separate explicit removal of old volumes.
func (s *Server) MountMigrations(st *store.Store, catalogDir string, secrets catalog.SecretStore) {
	h := &migrationHandlers{s: s, st: st, secrets: secrets, catalogDir: catalogDir, audit: audit.New(st), runner: operation.NewRunner(st)}
	s.mux.HandleFunc("GET /api/v1/data-migrations", h.list)
	s.mux.HandleFunc("POST /api/v1/data-migrations/{name}/plan", h.plan)
	s.mux.HandleFunc("POST /api/v1/data-migrations/{name}/approve", h.approve)
	s.mux.HandleFunc("POST /api/v1/data-migrations/{name}/remove-volumes", h.removeVolumes)
}

type migrationHandlers struct {
	s          *Server
	st         *store.Store
	secrets    catalog.SecretStore
	catalogDir string
	audit      *audit.Service
	runner     *operation.Runner
}

// migrationList is the detection response.
type migrationList struct {
	Apps       []catalog.AppDataStatus   `json:"apps"`
	OldVolumes []catalog.OldVolumeRecord `json:"old_volumes"`
}

// backupAvailability tells the owner whether the pre-migration backup
// can run; when it cannot, approval requires an explicit
// acknowledgement.
type backupAvailability struct {
	Possible    bool   `json:"possible"`
	Destination string `json:"destination,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// migrationPlanView wraps the plan with the backup verdict.
type migrationPlanView struct {
	catalog.MigrationPlan
	Backup backupAvailability `json:"backup"`
}

func (h *migrationHandlers) mountpoints() (map[storage.Class]string, error) {
	assignments, err := storage.NewAssignments(h.st).List()
	if err != nil {
		return nil, fmt.Errorf("assignments: %w", err)
	}
	return assignedMountpoints(assignments), nil
}

// installedStatuses detects every installed catalog app.
func (h *migrationHandlers) installedStatuses(ctx context.Context) ([]catalog.AppDataStatus, error) {
	entries, err := catalog.Load(h.catalogDir)
	if err != nil {
		return nil, fmt.Errorf("catalog unavailable: %w", err)
	}
	mountpoints, err := h.mountpoints()
	if err != nil {
		return nil, err
	}
	var containers []docker.Container
	loaded := false
	out := []catalog.AppDataStatus{}
	for _, name := range entries.Names() {
		installed, _ := catalog.Installed(h.st, name, stackDirFor(name))
		if !installed {
			continue
		}
		if !loaded {
			containers, err = migrationContainers(ctx)
			if err != nil {
				return nil, fmt.Errorf("engine unavailable: %w", err)
			}
			loaded = true
		}
		manifest, err := entries.Get(name)
		if err != nil {
			continue
		}
		out = append(out, catalog.DetectData(manifest, catalog.ProjectContainers(containers, name), mountpoints))
	}
	return out, nil
}

func (h *migrationHandlers) list(w http.ResponseWriter, r *http.Request) {
	apps, err := h.installedStatuses(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	old, err := catalog.ListOldVolumes(h.st, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, migrationList{Apps: apps, OldVolumes: old})
}

// buildPlan assembles live inputs and plans one package's migration.
func (h *migrationHandlers) buildPlan(ctx context.Context, manifest catalog.Manifest) (catalog.MigrationPlan, int, error) {
	installed, _ := catalog.Installed(h.st, manifest.Name, stackDirFor(manifest.Name))
	if !installed {
		return catalog.MigrationPlan{}, http.StatusConflict, fmt.Errorf("%q is not installed", manifest.Name)
	}
	mountpoints, err := h.mountpoints()
	if err != nil {
		return catalog.MigrationPlan{}, http.StatusInternalServerError, err
	}
	containers, err := migrationContainers(ctx)
	if err != nil {
		return catalog.MigrationPlan{}, http.StatusBadGateway, fmt.Errorf("engine unavailable: %w", err)
	}
	discovered, err := discoverStorage()
	if err != nil {
		return catalog.MigrationPlan{}, http.StatusInternalServerError, fmt.Errorf("storage discovery: %w", err)
	}
	free := map[string]uint64{}
	for _, mount := range discovered.Mounts {
		if mount.Accessible && !mount.ReadOnly {
			free[mount.Mountpoint] = mount.FreeBytes
		}
	}
	plan, err := catalog.PlanMigration(catalog.MigrationRequest{
		Manifest: manifest, Containers: catalog.ProjectContainers(containers, manifest.Name),
		Mountpoints: mountpoints, FreeBytes: free,
	})
	if err != nil {
		status := http.StatusUnprocessableEntity
		return plan, status, err
	}
	if err := verifyMounts(catalog.InstallPlan{Directories: plan.Directories}); err != nil {
		return catalog.MigrationPlan{}, http.StatusUnprocessableEntity, err
	}
	return plan, 0, nil
}

// volumeSources turns the old volumes into backup sources: each
// volume's data directory is its own classed entry, so the run holds
// <run>/<vol-id>/_data per volume.
func volumeSources(volumes []catalog.OldVolume) []backup.Source {
	var sources []backup.Source
	for i, volume := range volumes {
		id := volume.Name
		if len(id) > 12 {
			id = id[:12]
		}
		sources = append(sources, backup.Source{
			Mountpoint: filepath.Dir(volume.Source), Path: filepath.Base(volume.Source),
			Class: storage.Class(fmt.Sprintf("vol%d-%s", i, id)),
		})
	}
	return sources
}

// preMigrationBackup assembles the volume backup job or says why it
// cannot run (no destination, or not enough room on it).
func (h *migrationHandlers) preMigrationBackup(manifest catalog.Manifest, plan catalog.MigrationPlan) (backup.Job, backupAvailability) {
	discovered, err := discoverStorage()
	if err != nil {
		return backup.Job{}, backupAvailability{Reason: err.Error()}
	}
	dest, err := backup.ResolveDestination(storage.NewAssignments(h.st), discovered)
	if err != nil {
		return backup.Job{}, backupAvailability{Reason: err.Error()}
	}
	var need int64
	for _, volume := range plan.OldVolumes {
		need += volume.SizeBytes
	}
	required := need + need*5/100
	if mount, ok := containingMount(discovered.Mounts, dest); ok && int64(mount.FreeBytes) < required {
		return backup.Job{}, backupAvailability{Destination: dest,
			Reason: fmt.Sprintf("backup destination %s has %d bytes free; the volumes hold %d (5%% margin: %d)", dest, mount.FreeBytes, need, required)}
	}
	sources := volumeSources(plan.OldVolumes)
	if len(sources) == 0 {
		return backup.Job{}, backupAvailability{Destination: dest, Reason: "no volume data to back up"}
	}
	// A file copy of the stopped volumes: databases are consistent
	// because the stack is down, so no export (and no password) is
	// needed.
	manifest.Database = nil
	manifest.Hooks = nil
	// The volume copy is encrypted like every backup (S259).
	keys, err := backupKeyStore(h.secrets)
	var recipients []age.Recipient
	if err == nil {
		recipients, err = backup.RecipientsWithOwner(keys, backup.NewRecipientStore(h.st))
	}
	if err != nil {
		return backup.Job{}, backupAvailability{Destination: dest,
			Reason: "backup encryption unavailable: " + err.Error()}
	}
	return backup.Job{Manifest: manifest, Package: manifest.Name, Sources: sources, DestRoot: dest, Recipients: recipients},
		backupAvailability{Possible: true, Destination: dest}
}

// containingMount finds the mount a path lives on: the longest
// mountpoint that is the path or one of its ancestors.
func containingMount(mounts []discover.Mount, path string) (discover.Mount, bool) {
	var best discover.Mount
	found := false
	path = filepath.Clean(path)
	for _, mount := range mounts {
		point := filepath.Clean(mount.Mountpoint)
		if path == point || point == "/" || strings.HasPrefix(path, point+"/") {
			if !found || len(point) > len(filepath.Clean(best.Mountpoint)) {
				best, found = mount, true
			}
		}
	}
	return best, found
}

func (h *migrationHandlers) plan(w http.ResponseWriter, r *http.Request) {
	manifest, ok := loadPackage(h.catalogDir, r.PathValue("name"), w)
	if !ok {
		return
	}
	plan, status, err := h.buildPlan(r.Context(), manifest)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	_, availability := h.preMigrationBackup(manifest, plan)
	writeJSON(w, migrationPlanView{MigrationPlan: plan, Backup: availability})
}

type approveMigrationBody struct {
	Digest              string `json:"digest"`
	AcknowledgeNoBackup bool   `json:"acknowledge_no_backup"`
}

func (h *migrationHandlers) record(entry audit.Entry) {
	if _, err := h.audit.Record(entry); err != nil {
		h.s.log.Error("migration audit record failed", "action", entry.Action, "error", err)
	}
}

func (h *migrationHandlers) approve(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	manifest, ok := loadPackage(h.catalogDir, r.PathValue("name"), w)
	if !ok {
		return
	}
	var body approveMigrationBody
	if err := decodeBody(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	plan, status, err := h.buildPlan(r.Context(), manifest)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if body.Digest == "" || body.Digest != plan.Digest {
		http.Error(w, "the plan changed or was not quoted; review a fresh plan and approve its digest", http.StatusConflict)
		return
	}
	job, availability := h.preMigrationBackup(manifest, plan)
	options := catalog.MigrationOptions{
		Manifest: manifest, Plan: plan, Dir: stackDirFor(manifest.Name),
		Registry: storage.NewRegistry(h.st), Store: h.st, Actor: op.Session.Username,
		Replan: func(ctx context.Context) (catalog.MigrationPlan, error) {
			fresh, _, err := h.buildPlan(ctx, manifest)
			return fresh, err
		},
		Audit: func(outcome, detail string) {
			h.record(audit.Entry{Actor: op.Session.Username, Action: auditMigrateResult, Outcome: outcome, Detail: detail})
		},
	}
	switch {
	case availability.Possible:
		options.Backup = h.backupStep(job)
	case body.AcknowledgeNoBackup:
		options.AckNoBackup = true
	default:
		http.Error(w, "pre-migration backup is not possible ("+availability.Reason+"); approve again with acknowledge_no_backup to proceed without one", http.StatusUnprocessableEntity)
		return
	}
	def, err := catalog.MigrateDefinition(options)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, catalog.ErrMigrationActive) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	planJSON, _ := json.Marshal(plan)
	detail := "approved migration of " + manifest.Name
	if options.AckNoBackup {
		detail += " WITHOUT a pre-migration backup (owner acknowledged: " + availability.Reason + ")"
	}
	started, err := h.runner.Start(r.Context(), def, "")
	if err != nil {
		// The package claim was taken building the definition; Start
		// failed before the runner could release it.
		def.Cleanup()
		h.record(audit.Entry{Actor: op.Session.Username, Action: auditMigrateApprove, Plan: string(planJSON),
			Outcome: audit.OutcomeError, Detail: detail + ": operation failed to start: " + err.Error()})
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.record(audit.Entry{Actor: op.Session.Username, Action: auditMigrateApprove, Plan: string(planJSON), Outcome: audit.OutcomeOK,
		Detail: fmt.Sprintf("%s (operation %d)", detail, started.ID)})
	writeJSON(w, map[string]any{"id": started.ID})
}

// backupStep runs the volume backup as its own operation and waits.
func (h *migrationHandlers) backupStep(job backup.Job) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		def, err := backup.JobDefinition(job, nil)
		if err != nil {
			return err
		}
		started, err := h.runner.Start(ctx, def, "")
		if err != nil {
			return err
		}
		done, err := h.runner.Wait(ctx, started.ID)
		if err != nil {
			return err
		}
		if done.Status != operation.StatusSucceeded {
			return fmt.Errorf("pre-migration backup failed: %s", done.Error)
		}
		return nil
	}
}

type removeVolumesBody struct {
	Volumes []string `json:"volumes"`
}

func (h *migrationHandlers) removeVolumes(w http.ResponseWriter, r *http.Request) {
	op, ok := requireOperator(w, r)
	if !ok {
		return
	}
	manifest, ok := loadPackage(h.catalogDir, r.PathValue("name"), w)
	if !ok {
		return
	}
	var body removeVolumesBody
	if err := decodeBody(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req := catalog.RemoveOldVolumesRequest{Package: manifest.Name, Volumes: body.Volumes, Actor: op.Session.Username,
		Audit: func(outcome, detail string) {
			h.record(audit.Entry{Actor: op.Session.Username, Action: auditVolumesRemove + ".result", Outcome: outcome, Detail: detail})
		}}
	containers, err := migrationContainers(r.Context())
	if err != nil {
		http.Error(w, "engine unavailable: "+err.Error(), http.StatusBadGateway)
		return
	}
	if err := catalog.CheckOldVolumeRemoval(h.st, req, containers); err != nil {
		h.record(audit.Entry{Actor: op.Session.Username, Action: auditVolumesRemove, Outcome: audit.OutcomeError, Detail: err.Error()})
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	def, err := catalog.RemoveOldVolumesDefinition(h.st, req, time.Now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	h.record(audit.Entry{Actor: op.Session.Username, Action: auditVolumesRemove, Outcome: audit.OutcomeOK,
		Detail: fmt.Sprintf("removing %d old volume(s) of %s: %v", len(req.Volumes), manifest.Name, req.Volumes)})
	started, err := h.runner.Start(r.Context(), def, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"id": started.ID})
}

// migrationDoctorChecks reports app data placement as doctor checks:
// placed is ok; needs-migration and unknown warn with what to do.
// Without installed apps (or a reachable engine to inspect) it says
// nothing.
func migrationDoctorChecks(st *store.Store, catalogDir string) func() []doctor.Check {
	h := &migrationHandlers{st: st, catalogDir: catalogDir}
	return func() []doctor.Check {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		statuses, err := h.installedStatuses(ctx)
		if err != nil {
			return []doctor.Check{{Name: "app-data", Status: doctor.StatusWarn, Detail: "placement unavailable: " + err.Error()}}
		}
		var checks []doctor.Check
		for _, status := range statuses {
			check := doctor.Check{Name: "app-data:" + status.Package, Status: doctor.StatusOK, Detail: "data is on mounted storage"}
			switch status.State {
			case catalog.DataNeedsMigration:
				check.Status = doctor.StatusWarn
				check.Detail = "data lives in Docker volumes outside mounted storage and backups; plan the migration (homesteadctl data plan " + status.Package + ")"
			case catalog.DataUnknown:
				check.Status = doctor.StatusWarn
				check.Detail = "data placement unknown: " + status.Reason
			}
			checks = append(checks, check)
		}
		return checks
	}
}
