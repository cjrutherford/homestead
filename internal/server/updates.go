package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"homestead/internal/audit"
	"homestead/internal/backup"
	"homestead/internal/catalog"
	"homestead/internal/craft"
	"homestead/internal/discover"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/scheduler"
	"homestead/internal/secret"
	"homestead/internal/storage"
	"homestead/internal/store"
	"homestead/internal/update"
)

// defaultBackupRetain keeps a week of runs when no schedule says
// otherwise: enough history to recover, bounded enough to fit.
const defaultBackupRetain = 7

// MountUpdates serves update review and apply (S193): per-package
// drift plans against running containers with best-effort registry
// maxima, and approved application through the safe-update operation.
// Long executions return operation IDs onto existing streams.
func (s *Server) MountUpdates(st *store.Store, catalogDir string, client *docker.Client, secrets catalog.SecretStore) {
	craftPipe := func() *craft.Pipeline { return s.craft }
	s.mux.HandleFunc("GET /api/v1/updates", handleUpdatesList(st, catalogDir, client, craftPipe))
	s.mux.HandleFunc("POST /api/v1/updates/{name}/apply", handleUpdatesApply(st, catalogDir, client, secrets, craftPipe))
}

// packageUpdate is the review row: the plan plus whether anything
// runs (uninstalled packages plan empty, honestly labeled).
type packageUpdate struct {
	Package   string            `json:"package"`
	Installed bool              `json:"installed"`
	Plan      update.UpdatePlan `json:"plan"`
	// Craft reports, for a plan that moves a locally built craft image
	// (S279), whether the target is already built and the app's latest
	// verification verdict. Applying verifies the manifest and rebuilds.
	Craft []craftUpdate `json:"craft,omitempty"`
}

// craftUpdate is the craft build status of one move.
type craftUpdate struct {
	Service string         `json:"service"`
	App     string         `json:"app"`
	Version string         `json:"version"`
	Tag     string         `json:"tag"`
	Built   bool           `json:"built"`
	Verdict *craft.Verdict `json:"verdict,omitempty"`
}

// craftUpdates lists the craft moves of a plan with their build state.
func craftUpdates(pipe *craft.Pipeline, plan update.UpdatePlan) []craftUpdate {
	var out []craftUpdate
	for _, move := range plan.Services {
		app, version, ok := craft.ParseRef(move.Target)
		if !ok {
			continue
		}
		cu := craftUpdate{Service: move.Service, App: app, Version: version, Tag: move.Target}
		if pipe != nil {
			if _, err := pipe.Store.Image(app, version); err == nil {
				cu.Built = true
			}
			if verdicts, err := pipe.Store.Verdicts(); err == nil {
				for i := range verdicts {
					if verdicts[i].App == app {
						cu.Verdict = &verdicts[i]
					}
				}
			}
		}
		out = append(out, cu)
	}
	return out
}

// runningImages scopes engine containers by Compose project: service
// to image tag for one package. Unknown engines fail the request —
// planning against guessed state would review fiction.
func runningImages(client *docker.Client, r *http.Request, packageName string) (map[string]string, error) {
	health, err := client.Health(r.Context())
	if err != nil {
		return nil, fmt.Errorf("engine unavailable: %w", err)
	}
	out := map[string]string{}
	for _, container := range health.Containers {
		if container.Labels[docker.LabelProject] != packageName {
			continue
		}
		service, ok := container.Labels[docker.LabelService]
		if !ok || service == "" {
			continue
		}
		out[service] = container.Image
	}
	return out, nil
}

// registryDiscoveries lists tags per service image with a bounded
// client, skipping failures service by service: registries sulk, and
// pins cover silence (S190). Docker Hub and GHCR both answer (S343);
// every other registry records why it could not be asked instead of
// querying the wrong endpoint.
func registryDiscoveries(images map[string]string) map[string]update.Discovery {
	registries := update.RegistryClient{HTTP: &http.Client{Timeout: 8 * time.Second}}
	out := map[string]update.Discovery{}
	for service, image := range images {
		if _, _, local := craft.ParseRef(image); local {
			// Locally built (S279): there is no registry to ask.
			out[service] = update.Discovery{
				Service: service, Registry: update.RegistryLocal,
				Repository: image, Local: true,
			}
			continue
		}
		out[service] = registries.Discover(service, image)
	}
	return out
}

// planPackage reviews one package: manifest, running images, registry
// maxima, then the update plan.
func planPackage(catalogDir string, client *docker.Client, r *http.Request, name string) (catalog.Manifest, update.UpdatePlan, bool, error) {
	entries, err := catalog.Load(catalogDir)
	if err != nil {
		return catalog.Manifest{}, update.UpdatePlan{}, false, fmt.Errorf("catalog unavailable: %w", err)
	}
	manifest, err := entries.Get(name)
	if err != nil {
		return catalog.Manifest{}, update.UpdatePlan{}, false, fmt.Errorf("unknown package")
	}
	running, err := runningImages(client, r, name)
	if err != nil {
		return catalog.Manifest{}, update.UpdatePlan{}, false, err
	}
	images := map[string]string{}
	for _, service := range manifest.Services {
		images[service.Name] = service.Image
	}
	plan, err := update.PlanUpdateFromDiscoveries(manifest, running, registryDiscoveries(images))
	if err != nil {
		return catalog.Manifest{}, update.UpdatePlan{}, false, err
	}
	return manifest, plan, len(running) > 0, nil
}

func handleUpdatesList(st *store.Store, catalogDir string, client *docker.Client, craftPipe func() *craft.Pipeline) http.HandlerFunc {
	_ = st
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := catalog.Load(catalogDir)
		if err != nil {
			http.Error(w, fmt.Sprintf("catalog unavailable: %v", err), http.StatusInternalServerError)
			return
		}
		var out []packageUpdate
		for _, name := range entries.Names() {
			_, plan, installed, err := planPackage(catalogDir, client, r, name)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			out = append(out, packageUpdate{Package: name, Installed: installed, Plan: plan, Craft: craftUpdates(craftPipe(), plan)})
		}
		if out == nil {
			out = []packageUpdate{}
		}
		writeJSON(w, out)
	}
}

// applyBody carries approval plus backup inputs: opt-in backup with
// an explicit database password that flows through once, never stored.
type applyBody struct {
	Backup     bool   `json:"backup"`
	DBPassword string `json:"db_password"`
	// Craft carries the inputs for a craft image move (S279); every field
	// is optional (the catalog's shipped manifest and the signed URL are
	// the defaults).
	Craft *craftApplyInput `json:"craft,omitempty"`
}

// craftApplyInput is the manifest, signature and optional owner-supplied
// artifact for a craft update, plus the audited counter override.
type craftApplyInput struct {
	Manifest   string `json:"manifest"`
	Signature  string `json:"signature"`
	Artifact   string `json:"artifact"`
	AllowOlder bool   `json:"allow_older"`
}

// backupSources finds existing package directories under assigned
// mounts: the file half of pre-update backups, discovered rather
// than assumed.
func backupSources(st *store.Store, packageName string) ([]backup.Source, error) {
	assignments, err := storage.NewAssignments(st).List()
	if err != nil {
		return nil, fmt.Errorf("assignments: %w", err)
	}
	discovered, err := discover.DiscoverStorage()
	if err != nil {
		return nil, fmt.Errorf("storage discovery: %w", err)
	}
	byPoint := map[string]discover.Mount{}
	for _, mount := range discovered.Mounts {
		byPoint[mount.Mountpoint] = mount
	}
	return selectBackupSources(assignments, byPoint, packageName), nil
}

// selectBackupSources picks writable assigned mounts holding the
// package directory. The backup class is the destination, never a
// source (its package directory holds earlier runs, so copying it
// would back the destination up into itself), and a mount assigned
// several classes counts once, so step names stay unique.
func selectBackupSources(assignments []storage.Assignment, byPoint map[string]discover.Mount, packageName string) []backup.Source {
	var sources []backup.Source
	seen := map[string]bool{}
	for _, assignment := range assignments {
		if assignment.Class == storage.ClassBackup || seen[assignment.Mountpoint] {
			continue
		}
		mount, ok := byPoint[assignment.Mountpoint]
		if !ok || !mount.Accessible || mount.ReadOnly {
			continue
		}
		candidate := filepath.Join(assignment.Mountpoint, packageName)
		info, err := os.Stat(candidate)
		if err != nil || !info.IsDir() {
			continue
		}
		seen[assignment.Mountpoint] = true
		sources = append(sources, backup.Source{Mountpoint: assignment.Mountpoint, Path: packageName, Class: assignment.Class})
	}
	return sources
}

// classifyDestinationError marks only destination faults that can clear
// by themselves as retryable; configuration faults (no backup class
// assignment) fail the run and spend the window.
func classifyDestinationError(err error) error {
	if errors.Is(err, backup.ErrDestinationUnavailable) {
		return fmt.Errorf("%w: %v", scheduler.ErrUnavailable, err)
	}
	return err
}

// backupJob assembles the backup (scheduled and pre-update): file
// sources plus the declared database export against its conventional
// container. The Postgres password resolves from the secret store
// (S266) through the manifest's PasswordSecret; an explicit password
// argument overrides it. A missing secret fails naming it. Retention follows the package schedule,
// defaulting to a week unscheduled.
func backupJob(st *store.Store, manifest catalog.Manifest, secrets catalog.SecretStore, password string) (backup.Job, error) {
	// A stateless package has nothing to copy: a defined result, never
	// "no sources" and never wrapped in scheduler.ErrUnavailable (ADR-028).
	if manifest.Stateless() {
		return backup.Job{}, fmt.Errorf("%w: %q declares no persistent data", backup.ErrStateless, manifest.Name)
	}
	sources, err := backupSources(st, manifest.Name)
	if err != nil {
		return backup.Job{}, err
	}
	if len(sources) == 0 {
		return backup.Job{}, fmt.Errorf("%w: no backup sources for %q%s", scheduler.ErrUnavailable, manifest.Name, orphanedDataHint(st, manifest.Name))
	}
	assignments, err := storage.NewAssignments(st).List()
	if err != nil {
		return backup.Job{}, fmt.Errorf("assignments: %w", err)
	}
	containers, err := backupInspect(manifest.Name)
	if err != nil {
		return backup.Job{}, fmt.Errorf("backup source guard: cannot inspect running mounts: %w", err)
	}
	warnings, err := guardBackupSources(manifest, assignedMountpoints(assignments), sources, containers)
	if err != nil {
		return backup.Job{}, err
	}
	sources, dbNotes := applyDatabaseHandling(manifest, assignedMountpoints(assignments), sources)
	warnings = append(warnings, dbNotes...)
	discovered, err := discover.DiscoverStorage()
	if err != nil {
		return backup.Job{}, err
	}
	dest, err := backup.ResolveDestination(storage.NewAssignments(st), discovered)
	if err != nil {
		// A destination that is not mounted right now may be back by the
		// next tick; the scheduler retries instead of spending the window.
		return backup.Job{}, classifyDestinationError(err)
	}
	retain := backupRetain(st, manifest.Name)
	keys, err := backupKeyStore(secrets)
	if err != nil {
		return backup.Job{}, err
	}
	recipients, err := backup.RecipientsWithOwner(keys, backup.NewRecipientStore(st))
	if err != nil {
		return backup.Job{}, err
	}
	job := backup.Job{
		Manifest: manifest, Package: manifest.Name,
		Sources: sources, DestRoot: dest, Retain: retain, Warnings: warnings,
		Recipients: recipients,
	}
	if manifest.Database != nil {
		job.Container = manifest.Name + "-" + manifest.Database.Service
		if password == "" && manifest.Database.PasswordSecret != "" {
			resolved, err := databasePassword(secrets, manifest)
			if err != nil {
				return backup.Job{}, err
			}
			password = resolved
		}
		job.Password = password
		for _, source := range sources {
			job.SrcRoot = filepath.Join(source.Mountpoint, source.Path)
			break
		}
		if password == "" && manifest.Database.Kind.Server() {
			return backup.Job{}, fmt.Errorf("%s backup needs a database password", manifest.Database.Kind)
		}
	}
	// S266 secret files live in the stack directory, outside the data
	// mounts. They join the run as a sensitive source: archived only
	// encrypted, restored 0600 with their recorded ownership.
	// The stack definition joins first: restoring it replaces the stack
	// directory, so the secrets entry must follow it, never precede it.
	if source, ok := stackDefinitionSource(manifest.Name); ok {
		job.Sources = append(job.Sources, source)
	}
	if source, ok := stackSecretsSource(manifest.Name); ok {
		job.Sources = append(job.Sources, source)
	}
	return job, nil
}

// stackDefinitionSource returns the source for a stack's compose.yaml
// and override files: the stack directory minus secrets/ (its own
// sensitive entry), when the directory holds a compose.yaml.
func stackDefinitionSource(packageName string) (backup.Source, bool) {
	dir := stackDirFor(packageName)
	if info, err := os.Stat(filepath.Join(dir, "compose.yaml")); err != nil || !info.Mode().IsRegular() {
		return backup.Source{}, false
	}
	return backup.Source{
		Mountpoint: filepath.Dir(dir), Path: packageName,
		Class: backup.StackClass, Exclude: []string{catalog.SecretsDirName},
	}, true
}

// stackSecretsSource returns the sensitive source for a stack's
// materialized secret files when the directory exists.
func stackSecretsSource(packageName string) (backup.Source, bool) {
	dir := filepath.Join(stackDirFor(packageName), catalog.SecretsDirName)
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return backup.Source{}, false
	}
	return backup.Source{
		Mountpoint: filepath.Dir(stackDirFor(packageName)),
		Path:       packageName + "/" + catalog.SecretsDirName,
		Class:      backup.SecretsClass, Sensitive: true,
	}, true
}

// databasePassword reads the declared database password secret. A
// missing secret names the store entry and the remedy; an empty value is
// never returned.
func databasePassword(secrets catalog.SecretStore, manifest catalog.Manifest) (string, error) {
	name := catalog.SecretStoreName(manifest.Name, manifest.Database.PasswordSecret)
	if secrets == nil {
		return "", fmt.Errorf("database backup needs secret %q but no secret store is available", name)
	}
	value, err := secrets.Get(name)
	if errors.Is(err, secret.ErrNotFound) || (err == nil && value == "") {
		return "", fmt.Errorf("database backup needs secret %q, which is not in the secret store: register the existing database password (homesteadctl apps secrets register %s %s) or reinstall", name, manifest.Name, manifest.Database.PasswordSecret)
	}
	if err != nil {
		return "", fmt.Errorf("database backup cannot read secret %q: %w", name, err)
	}
	return value, nil
}

func handleUpdatesApply(st *store.Store, catalogDir string, client *docker.Client, secrets catalog.SecretStore, craftPipe func() *craft.Pipeline) http.HandlerFunc {
	runner := operation.NewRunner(st)
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		// Platform components update separately (S194): the
		// compose-based application applier cannot replace the
		// daemon out from under itself, so platform names refuse
		// here even if a catalog entry ever collides with them.
		if update.IsPlatformComponent(name) {
			http.Error(w, fmt.Sprintf("platform component %q updates separately from applications", name), http.StatusBadRequest)
			return
		}
		if err := catalog.RefuseDuringMigration(name); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		manifest, plan, _, err := planPackage(catalogDir, client, r, name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(plan.Services) == 0 {
			writeJSON(w, map[string]any{"id": 0, "detail": "already current"})
			return
		}
		var body applyBody
		if err := decodeBody(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		req := update.UpdateRequest{
			Package: name, StackDir: docker.DirFor(name), Store: st,
		}
		for _, move := range plan.Services {
			req.Moves = append(req.Moves, move)
		}
		var backupNote string
		if body.Backup && manifest.Stateless() {
			// A pre-update backup of a stateless package is skipped, with a
			// recorded note; the update proceeds (ADR-028).
			backupNote = fmt.Sprintf("backup skipped: %s is stateless and keeps nothing on the appliance", name)
			_, _ = audit.New(st).Record(audit.Entry{Actor: operatorActor(r), Action: "update.backup.skipped", Outcome: audit.OutcomeOK,
				Detail: backupNote})
		} else if body.Backup {
			job, err := backupJob(st, manifest, secrets, body.DBPassword)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
			jobRunner := operation.NewRunner(st)
			req.Backup = func(ctx context.Context) error {
				def, err := backup.JobDefinition(job, nil)
				if err != nil {
					return err
				}
				op, err := jobRunner.Start(ctx, def, "")
				if err != nil {
					return err
				}
				done, err := jobRunner.Wait(ctx, op.ID)
				if err != nil {
					return err
				}
				if done.Status != operation.StatusSucceeded {
					return fmt.Errorf("backup failed: %s", done.Error)
				}
				return nil
			}
		} else if plan.Backup && !manifest.Stateless() {
			http.Error(w, "backup required: re-apply with backup requested", http.StatusUnprocessableEntity)
			return
		}
		def, err := update.UpdateDefinition(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		craftPlan, status, err := withCraftBuild(r, &def, craftPipe(), plan, body.Craft)
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}
		op, err := runner.Start(r.Context(), def, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if craftPlan != nil && craftPlan.Override {
			_, _ = audit.New(st).Record(audit.Entry{Actor: operatorActor(r), Action: "craft.build.override", Outcome: audit.OutcomeOK, Override: true,
				Detail: fmt.Sprintf("%s %s (update of %s)", craftPlan.App, craftPlan.Version, name),
				OverrideReason: fmt.Sprintf("owner allowed older craft manifest: recorded %s, manifest %s counter %d",
					craftPlan.Baseline, craftPlan.Version, craftPlan.Counter)})
		}
		reply := map[string]any{"id": op.ID}
		if backupNote != "" {
			reply["detail"] = backupNote
		}
		writeJSON(w, reply)
	}
}

// withCraftBuild adds the verify-then-build steps in front of an update
// that moves a locally built craft image (S279), and prunes the previous
// build after the update applied. The manifest is verified here first so
// a refusal answers immediately (HTTP 422, counter rollbacks with the
// override code); the operation verifies again before building. A plan
// with no craft move is left untouched.
func withCraftBuild(r *http.Request, def *operation.Definition, pipe *craft.Pipeline, plan update.UpdatePlan, in *craftApplyInput) (*craft.Plan, int, error) {
	var app, version string
	for _, move := range plan.Services {
		a, v, ok := craft.ParseRef(move.Target)
		if !ok {
			continue
		}
		if app != "" {
			return nil, http.StatusUnprocessableEntity, fmt.Errorf("an update may move only one craft image at a time")
		}
		app, version = a, v
	}
	if app == "" {
		return nil, 0, nil
	}
	if pipe == nil {
		return nil, http.StatusUnprocessableEntity, fmt.Errorf("craft builds are not available")
	}
	manifest := filepath.Join(pipe.ContextDir, "..", craftManifestFile)
	req := craft.Request{App: app, Version: version, ManifestPath: filepath.Clean(manifest), SignaturePath: filepath.Clean(manifest) + craftSigSuffix, Actor: operatorActor(r)}
	if in != nil {
		if in.Manifest != "" {
			req.ManifestPath, req.SignaturePath = in.Manifest, in.Signature
			if req.SignaturePath == "" {
				req.SignaturePath = in.Manifest + craftSigSuffix
			}
		}
		req.ArtifactPath, req.AllowOlder = in.Artifact, in.AllowOlder
	}
	checked, _, err := pipe.Check(r.Context(), req)
	if err != nil {
		var ce *craft.CheckError
		if errors.As(err, &ce) && ce.Check == craft.CheckCounter {
			return nil, http.StatusUnprocessableEntity, fmt.Errorf("%w (override with allow_older)", err)
		}
		return nil, refusalStatus(err), err
	}
	build, err := pipe.Definition(req)
	if err != nil {
		return nil, refusalStatus(err), err
	}
	def.Steps = append(append([]operation.StepDef{}, build.Steps...), def.Steps...)
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "prune-craft-images",
		Run: func(ctx context.Context) error {
			// The update applied: the previous build can go. A failure to
			// prune is not an update failure; the image stays recorded and
			// the next update retries.
			_, _ = pipe.Prune(ctx, app, version)
			return nil
		},
	})
	prevCleanup := def.Cleanup
	def.Cleanup = func() {
		build.Cleanup()
		if prevCleanup != nil {
			prevCleanup()
		}
	}
	return &checked, 0, nil
}

// backupKeyStore narrows the secret store to what the backup identity
// needs; stores that cannot hold system secrets refuse encryption.
func backupKeyStore(secrets catalog.SecretStore) (backup.KeyStore, error) {
	keys, ok := secrets.(backup.KeyStore)
	if !ok || secrets == nil {
		return nil, errors.New("backup encryption needs a secret store that holds system secrets")
	}
	return keys, nil
}

// orphanedDataHint diagnoses a package with no backup sources: data on a
// registered mount that no class is bound to any more usually means a
// storage class was re-assigned. Empty when nothing matches.
func orphanedDataHint(st *store.Store, packageName string) string {
	registrations, err := storage.NewRegistry(st).List()
	if err != nil {
		return ""
	}
	assignments, err := storage.NewAssignments(st).List()
	if err != nil {
		return ""
	}
	assigned := map[string]bool{}
	for _, assignment := range assignments {
		assigned[assignment.Mountpoint] = true
	}
	var orphaned []string
	for _, reg := range registrations {
		if assigned[reg.Mountpoint] {
			continue
		}
		if info, err := os.Stat(filepath.Join(reg.Mountpoint, packageName)); err == nil && info.IsDir() {
			orphaned = append(orphaned, reg.Mountpoint)
		}
	}
	if len(orphaned) == 0 {
		return ""
	}
	return fmt.Sprintf(": its data exists on %s, which no storage class is assigned to any more (was a class re-assigned?); assign a class back to that mount or migrate the data", strings.Join(orphaned, ", "))
}

// backupRetain is how many runs of an app to keep: its own schedule's
// count, else the nightly policy's (S297) while it is on, else the default.
func backupRetain(st *store.Store, name string) int {
	if schedule, err := backup.NewSchedules(st).Get(name); err == nil {
		return schedule.Retain
	}
	if policy, ok, err := backup.NewPolicies(st).Get(); err == nil && ok && policy.Enabled {
		return policy.Retain
	}
	return defaultBackupRetain
}
