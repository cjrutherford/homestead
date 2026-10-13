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

	"homestead/internal/backup"
	"homestead/internal/catalog"
	"homestead/internal/discover"
	"homestead/internal/docker"
	"homestead/internal/operation"
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
	s.mux.HandleFunc("GET /api/v1/updates", handleUpdatesList(st, catalogDir, client))
	s.mux.HandleFunc("POST /api/v1/updates/{name}/apply", handleUpdatesApply(st, catalogDir, client, secrets))
}

// packageUpdate is the review row: the plan plus whether anything
// runs (uninstalled packages plan empty, honestly labeled).
type packageUpdate struct {
	Package   string            `json:"package"`
	Installed bool              `json:"installed"`
	Plan      update.UpdatePlan `json:"plan"`
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

// registryMaxima fetches newer tags per service image with a bounded
// client, skipping failures service by service: registries sulk,
// and pins cover silence (S190).
func registryMaxima(images map[string]string) map[string][]string {
	client := &http.Client{Timeout: 8 * time.Second}
	out := map[string][]string{}
	for service, image := range images {
		tag := image
		if index := strings.LastIndex(image, ":"); index >= 0 {
			tag = image[index+1:]
		}
		if tags, err := update.RegistryUpdates(client, update.DockerHubAPI, image, tag); err == nil {
			out[service] = tags
		}
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
	plan, err := update.PlanUpdate(manifest, running, registryMaxima(images))
	if err != nil {
		return catalog.Manifest{}, update.UpdatePlan{}, false, err
	}
	return manifest, plan, len(running) > 0, nil
}

func handleUpdatesList(st *store.Store, catalogDir string, client *docker.Client) http.HandlerFunc {
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
			out = append(out, packageUpdate{Package: name, Installed: installed, Plan: plan})
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

// backupJob assembles the backup (scheduled and pre-update): file
// sources plus the declared database export against its conventional
// container. The Postgres password resolves from the secret store
// (S266) through the manifest's PasswordSecret; an explicit password
// argument overrides it. A missing secret fails naming it. Retention follows the package schedule,
// defaulting to a week unscheduled.
func backupJob(st *store.Store, manifest catalog.Manifest, secrets catalog.SecretStore, password string) (backup.Job, error) {
	sources, err := backupSources(st, manifest.Name)
	if err != nil {
		return backup.Job{}, err
	}
	if len(sources) == 0 {
		return backup.Job{}, fmt.Errorf("no backup sources for %q", manifest.Name)
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
	discovered, err := discover.DiscoverStorage()
	if err != nil {
		return backup.Job{}, err
	}
	dest, err := backup.ResolveDestination(storage.NewAssignments(st), discovered)
	if err != nil {
		return backup.Job{}, err
	}
	retain := defaultBackupRetain
	schedules := backup.NewSchedules(st)
	if schedule, err := schedules.Get(manifest.Name); err == nil {
		retain = schedule.Retain
	}
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
		if password == "" && manifest.Database.Kind == catalog.DatabasePostgres {
			return backup.Job{}, fmt.Errorf("postgres backup needs a database password")
		}
	}
	// S266 secret files live in the stack directory, outside the data
	// mounts. They join the run as a sensitive source: archived only
	// encrypted, restored 0600 with their recorded ownership.
	if source, ok := stackSecretsSource(manifest.Name); ok {
		job.Sources = append(job.Sources, source)
	}
	return job, nil
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
		return "", fmt.Errorf("postgres backup needs secret %q but no secret store is available", name)
	}
	value, err := secrets.Get(name)
	if errors.Is(err, secret.ErrNotFound) || (err == nil && value == "") {
		return "", fmt.Errorf("postgres backup needs secret %q, which is not in the secret store: register the existing database password (homesteadctl apps secrets register %s %s) or reinstall", name, manifest.Name, manifest.Database.PasswordSecret)
	}
	if err != nil {
		return "", fmt.Errorf("postgres backup cannot read secret %q: %w", name, err)
	}
	return value, nil
}

func handleUpdatesApply(st *store.Store, catalogDir string, client *docker.Client, secrets catalog.SecretStore) http.HandlerFunc {
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
			Package: name, StackDir: docker.DirFor(name),
		}
		for _, move := range plan.Services {
			req.Moves = append(req.Moves, move)
		}
		if body.Backup {
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
		} else if plan.Backup {
			http.Error(w, "backup required: re-apply with backup requested", http.StatusUnprocessableEntity)
			return
		}
		def, err := update.UpdateDefinition(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		op, err := runner.Start(r.Context(), def, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"id": op.ID})
	}
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
