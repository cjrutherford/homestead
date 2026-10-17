package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/storage"
	"homestead/internal/store"
)

// Seams, stubbed in tests so the whole migration verifies without a
// Docker daemon.
var (
	// composeStop stops containers without removing them: stopped
	// containers keep their anonymous volumes attached, which `down`
	// would orphan for any later recreate.
	composeStop = func(ctx context.Context, dir, project string) error {
		defer lockProject(project)()
		return composeRun(ctx, dir, project, "stop")
	}
	// composeStart restarts the original stopped containers.
	composeStart = func(ctx context.Context, dir, project string) error {
		defer lockProject(project)()
		if err := checkCraftStack(ctx, dir, project); err != nil {
			return err
		}
		return composeRun(ctx, dir, project, "start")
	}
	// composeUpServices brings up only the named services. It takes the
	// same craft gate as composeUp (S249): the database-only start used by
	// migrations is still a Compose up, and `up <service>` also creates
	// the services the named ones depend on, so a craft image must match
	// its recorded digest before any of them starts.
	composeUpServices = func(ctx context.Context, dir, project string, services ...string) error {
		defer lockProject(project)()
		if err := checkCraftStack(ctx, dir, project); err != nil {
			return err
		}
		return composeRun(ctx, dir, project, append([]string{"up", "-d"}, services...)...)
	}
	// projectContainers reads the project's containers with mounts.
	projectContainers = func(ctx context.Context, project string) ([]docker.Container, error) {
		all, err := docker.NewClient("").Containers(ctx)
		if err != nil {
			return nil, err
		}
		return ProjectContainers(all, project), nil
	}
	// execIn runs a command inside a container.
	execIn = func(ctx context.Context, container string, args ...string) error {
		cmd := exec.CommandContext(ctx, "docker", append([]string{"exec", container}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	// volumeRemove deletes one Docker volume.
	volumeRemove = func(ctx context.Context, name string) error {
		out, err := exec.CommandContext(ctx, "docker", "volume", "rm", name).CombinedOutput()
		if err != nil {
			return fmt.Errorf("catalog: docker volume rm %s: %w: %s", name, err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	// migrateCopy copies src onto dst preserving ownership, modes,
	// timestamps, ACLs, and extended attributes. GNU tar on the host
	// does this faithfully: the daemon runs as root, volume data
	// directories are plain host paths (the engine reports them), and
	// no helper image has to be present or pulled. busybox cp/tar, the
	// usual helper-container tools, drop xattrs.
	migrateCopy = tarCopy
	// healthTimeout bounds waiting for the restarted stack; healthStable
	// is how long every container must stay up; healthPoll is the
	// polling interval.
	// writeStackFile writes compose.yaml; a seam so tests can fail it.
	writeStackFile = os.WriteFile
	// recordOldVolumes persists the left-behind volumes; a seam so
	// tests can fail bookkeeping after a committed migration.
	recordOldVolumes = RecordOldVolumes
	// recordRetryDelay spaces bookkeeping retries.
	recordRetryDelay = 2 * time.Second
	// stopSettle bounds waiting for stopped containers to read as stopped.
	stopSettle    = 20 * time.Second
	healthTimeout = 3 * time.Minute
	healthStable  = 10 * time.Second
	healthPoll    = 2 * time.Second
)

// tarCopy is the production migrateCopy.
func tarCopy(ctx context.Context, src, dst string, excludes []string) error {
	if _, err := os.Stat(src); err != nil {
		return err
	}
	common := []string{"--numeric-owner", "--xattrs", "--xattrs-include=*", "--acls"}
	create := append([]string{}, common...)
	create = append(create, "--sparse", "-C", src)
	// Excludes are literal, root-relative paths: no glob expansion and
	// no matching of a same-named entry deeper in the tree.
	for _, exclude := range excludes {
		create = append(create, "--anchored", "--no-wildcards", "--exclude=./"+exclude)
	}
	create = append(create, "-cf", "-", ".")
	extract := append(append([]string{}, common...), "-C", dst, "-xpf", "-")
	producer := exec.CommandContext(ctx, "tar", create...)
	consumer := exec.CommandContext(ctx, "tar", extract...)
	pipe, err := producer.StdoutPipe()
	if err != nil {
		return err
	}
	consumer.Stdin = pipe
	var produced, consumed strings.Builder
	producer.Stderr = &produced
	consumer.Stderr = &consumed
	if err := consumer.Start(); err != nil {
		return fmt.Errorf("tar extract: %w", err)
	}
	if err := producer.Start(); err != nil {
		_ = consumer.Process.Kill()
		_ = consumer.Wait()
		return fmt.Errorf("tar create: %w", err)
	}
	producerErr := producer.Wait()
	consumerErr := consumer.Wait()
	if producerErr != nil {
		return fmt.Errorf("tar create %s: %w: %s", src, producerErr, strings.TrimSpace(produced.String()))
	}
	if consumerErr != nil {
		return fmt.Errorf("tar extract %s: %w: %s", dst, consumerErr, strings.TrimSpace(consumed.String()))
	}
	return nil
}

// MigrationOptions carries what executing an approved plan needs.
type MigrationOptions struct {
	Manifest Manifest
	Plan     MigrationPlan
	// Dir is the stack directory.
	Dir      string
	Registry *storage.Registry
	Store    *store.Store
	// Replan re-detects and re-plans at execution time; its digest must
	// match the approved plan's, so a stack that changed since approval
	// is refused instead of migrated blindly.
	Replan func(ctx context.Context) (MigrationPlan, error)
	// Backup takes the pre-migration backup. Nil means the owner
	// acknowledged that none is possible (AckNoBackup must then be set).
	Backup      func(ctx context.Context) error
	AckNoBackup bool
	Actor       string
	// Audit records the final outcome (ok or error) with the step
	// where it ended.
	Audit func(outcome, detail string)
	Now   func() time.Time
}

// phase tracks how far a migration got, so rollback undoes exactly
// that much.
type migrationRun struct {
	opts        MigrationOptions
	original    string
	pins        []PinVolume
	stopped     bool
	written     bool
	recreated   bool
	copied      bool
	projectName string
}

// MigrateDefinition builds the approved migration operation: preflight,
// provision, stop, backup, copy, verify, rewrite the stack, verify
// databases, start, health-check, record old volumes. A failing step
// rolls everything back to the original volumes and names itself in
// the error, so the operation record says exactly where it stopped.
func MigrateDefinition(opts MigrationOptions) (operation.Definition, error) {
	plan := opts.Plan
	if plan.Package == "" || len(plan.Items) == 0 && len(plan.OldVolumes) == 0 {
		return operation.Definition{}, fmt.Errorf("catalog: migration needs an approved plan")
	}
	if opts.Dir == "" || opts.Store == nil || opts.Registry == nil || opts.Replan == nil {
		return operation.Definition{}, fmt.Errorf("catalog: migration needs a stack directory, store, registry, and replanner")
	}
	if opts.Backup == nil && !opts.AckNoBackup {
		return operation.Definition{}, fmt.Errorf("catalog: migration needs a pre-migration backup or the owner's explicit acknowledgement that none is possible")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	release, ok := acquireMigration(plan.Package)
	if !ok {
		return operation.Definition{}, ErrMigrationActive
	}
	var releaseOnce sync.Once
	run := &migrationRun{opts: opts, projectName: plan.Package}
	def := operation.Definition{Type: "catalog.migrate", Cleanup: func() { releaseOnce.Do(release) }}
	add := func(name string, fn func(ctx context.Context) error) {
		def.Steps = append(def.Steps, operation.StepDef{
			Name: name,
			Run: func(ctx context.Context) error {
				if err := fn(ctx); err != nil {
					return run.fail(ctx, name, err)
				}
				return nil
			},
		})
	}
	add("preflight", run.preflight)
	for _, dir := range plan.Directories {
		provision := dir
		add("provision:"+provision.Directory.Path, func(ctx context.Context) error {
			return storage.Provision(opts.Registry, provision.Mountpoint, []storage.Directory{provision.Directory})
		})
	}
	add("stop", run.stop)
	add("verify-stopped", run.verifyStopped)
	if opts.Backup != nil {
		add("backup", opts.Backup)
	} else {
		add("backup-acknowledged-none", func(ctx context.Context) error { return nil })
	}
	// The backup can take a long time; confirm nothing restarted the
	// stack (a restart policy, another tool) right before copying.
	add("verify-stopped:before-copy", run.verifyStopped)
	for _, item := range plan.Items {
		copying := item
		add("copy:"+copying.Service+":"+copying.Target, func(ctx context.Context) error {
			run.copied = true
			if _, err := os.Stat(copying.Source); err != nil && !(copying.Nested && os.IsNotExist(err)) {
				return fmt.Errorf("source %s: %w", copying.Source, err)
			} else if err != nil {
				return nil // a nested path absent from its volume is empty
			}
			return migrateCopy(ctx, copying.Source, copying.Dest, copying.Excludes)
		})
	}
	add("verify-stopped:before-verify", run.verifyStopped)
	add("verify-copy", func(ctx context.Context) error {
		for _, item := range plan.Items {
			if err := verifyCopy(item); err != nil {
				return err
			}
		}
		return nil
	})
	add("write-stack", run.writeStack)
	if len(plan.DatabaseServices) > 0 {
		add("verify-database", run.verifyDatabase)
	}
	add("up", func(ctx context.Context) error {
		run.recreated = true
		return composeUp(ctx, opts.Dir, plan.Package)
	})
	add("health-check", run.healthCheck)
	// Bookkeeping is not part of the data move: once the health check
	// passed the migration is committed. A failure here must never roll
	// back a healthy stack; it is retried and then reported as a
	// warning, and the old volumes stay ineligible for removal until
	// they are recorded.
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "record-old-volumes",
		Run: func(ctx context.Context) error {
			var err error
			for attempt := 0; attempt < 3; attempt++ {
				if attempt > 0 {
					select {
					case <-ctx.Done():
					case <-time.After(recordRetryDelay):
					}
				}
				if err = recordOldVolumes(opts.Store, plan.Package, plan.OldVolumes, opts.Actor, opts.Now()); err == nil {
					break
				}
			}
			if opts.Audit == nil {
				return nil
			}
			if err != nil {
				var names []string
				for _, volume := range plan.OldVolumes {
					names = append(names, volume.Name)
				}
				opts.Audit("warning", fmt.Sprintf("migrated %s and it is healthy, but recording its old volumes failed (%v); volumes %v are NOT eligible for removal until recorded",
					plan.Package, err, names))
				return nil
			}
			opts.Audit("ok", fmt.Sprintf("migrated %s; %d old volume(s) pending removal", plan.Package, len(plan.OldVolumes)))
			return nil
		},
	})
	return def, nil
}

func (r *migrationRun) preflight(ctx context.Context) error {
	fresh, err := r.opts.Replan(ctx)
	if err != nil {
		return fmt.Errorf("re-checking the plan: %w", err)
	}
	if fresh.Digest != r.opts.Plan.Digest {
		return fmt.Errorf("the stack changed since the plan was approved; review a new plan")
	}
	raw, err := os.ReadFile(filepath.Join(r.opts.Dir, "compose.yaml"))
	if err != nil {
		return fmt.Errorf("read compose.yaml: %w", err)
	}
	r.original = string(raw)
	if _, err := ApplyPlacement(r.original, r.opts.Plan.Mounts); err != nil {
		return fmt.Errorf("compose.yaml cannot take the new mounts: %w", err)
	}
	containers, err := projectContainers(ctx, r.projectName)
	if err != nil {
		return fmt.Errorf("inspect containers: %w", err)
	}
	for _, container := range containers {
		for _, mount := range container.Mounts {
			if mount.Type == "volume" {
				r.pins = append(r.pins, PinVolume{
					Service: container.Labels[docker.LabelService], Target: mount.Destination, Volume: mount.Name,
				})
			}
		}
	}
	return nil
}

func (r *migrationRun) stop(ctx context.Context) error {
	r.stopped = true
	return composeStop(ctx, r.opts.Dir, r.projectName)
}

// verifyStopped refuses to copy while any container still runs:
// database files and live writers cannot be copied consistently.
func (r *migrationRun) verifyStopped(ctx context.Context) error {
	deadline := time.Now().Add(stopSettle)
	for {
		err := r.runningContainer(ctx)
		if err == nil {
			return nil
		}
		// The engine reports a container's final state a moment after
		// compose stop returns (seen live on a crash-looping stack), so
		// a still-running container is re-read for a short, bounded
		// time; one that keeps running fails the step.
		var running errStillRunning
		if !errors.As(err, &running) || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(healthPoll):
		}
	}
}

// errStillRunning marks a container that is running or restarting.
type errStillRunning struct{ message string }

func (e errStillRunning) Error() string { return e.message }

func (r *migrationRun) runningContainer(ctx context.Context) error {
	containers, err := projectContainers(ctx, r.projectName)
	if err != nil {
		return err
	}
	for _, container := range containers {
		if container.State == "running" || container.State == "restarting" {
			return errStillRunning{fmt.Sprintf("container %s is still %s; data is not copied while it runs", strings.Join(container.Names, ","), container.State)}
		}
	}
	return nil
}

func (r *migrationRun) writeStack(ctx context.Context) error {
	rewritten, err := ApplyPlacement(r.original, r.opts.Plan.Mounts)
	if err != nil {
		return err
	}
	r.written = true
	if err := writeStackFile(filepath.Join(r.opts.Dir, "compose.yaml"), []byte(rewritten), 0o644); err != nil {
		return fmt.Errorf("write compose.yaml: %w", err)
	}
	if !hasDeclaredBinds(r.opts.Dir, r.opts.Manifest) {
		return fmt.Errorf("rewritten compose.yaml does not mount every declared path")
	}
	return RecordFingerprints(r.opts.Store, r.opts.Plan.Package, r.opts.Dir)
}

// verifyDatabase starts only the database services on their new bind
// mounts and waits until the engine answers: a bad copy shows here,
// before the rest of the stack depends on it.
func (r *migrationRun) verifyDatabase(ctx context.Context) error {
	r.recreated = true
	if err := composeUpServices(ctx, r.opts.Dir, r.projectName, r.opts.Plan.DatabaseServices...); err != nil {
		return err
	}
	deadline := time.Now().Add(healthTimeout)
	for _, service := range r.opts.Plan.DatabaseServices {
		container := r.projectName + "-" + service
		for {
			err := r.databaseReady(ctx, service, container)
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("database %s did not come up on the migrated data: %w", service, err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(healthPoll):
			}
		}
	}
	return nil
}

// databaseReady asks postgres images pg_isready. Only postgres is
// verified: no catalog entry declares another server engine today
// (Nextcloud's SQLite is a file inside the copied tree, covered by
// verify-copy). A non-postgres database service merely has to be
// running, and a new engine needs its own check added here.
func (r *migrationRun) databaseReady(ctx context.Context, service, container string) error {
	containers, err := projectContainers(ctx, r.projectName)
	if err != nil {
		return err
	}
	var current *docker.Container
	for i := range containers {
		if containers[i].Labels[docker.LabelService] == service {
			current = &containers[i]
		}
	}
	if current == nil || current.State != "running" {
		return fmt.Errorf("container %s is not running", container)
	}
	if strings.Contains(current.Image, "postgres") {
		return execIn(ctx, container, "pg_isready")
	}
	return nil
}

// healthCheck waits until every project container has been running,
// not unhealthy, for healthStable.
func (r *migrationRun) healthCheck(ctx context.Context) error {
	deadline := time.Now().Add(healthTimeout)
	var stableSince time.Time
	var last string
	for {
		containers, err := projectContainers(ctx, r.projectName)
		if err != nil {
			return err
		}
		problem := ""
		if len(containers) == 0 {
			problem = "no containers are running"
		}
		for _, container := range containers {
			name := strings.Join(container.Names, ",")
			switch {
			case container.State != "running":
				problem = fmt.Sprintf("%s is %s", name, container.State)
			case strings.Contains(container.Status, "unhealthy"):
				problem = name + " is unhealthy"
			case strings.Contains(container.Status, "health: starting"):
				problem = name + " is still starting"
			}
		}
		if problem == "" {
			if stableSince.IsZero() {
				stableSince = time.Now()
			}
			if time.Since(stableSince) >= healthStable {
				return nil
			}
		} else {
			stableSince = time.Time{}
			last = problem
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("stack did not stay healthy: %s", last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(healthPoll):
		}
	}
}

// fail rolls back to the original volumes and wraps the step error.
func (r *migrationRun) fail(ctx context.Context, step string, cause error) error {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	summary, rollbackErr := r.rollback(rollbackCtx)
	message := fmt.Sprintf("migration stopped at step %q: %v; %s", step, cause, summary)
	if rollbackErr != nil {
		message += fmt.Sprintf("; ROLLBACK FAILED: %v — the app may be down; see docs/architecture/APP_DATA_MIGRATION.md", rollbackErr)
	}
	if r.opts.Audit != nil {
		r.opts.Audit("error", message)
	}
	return fmt.Errorf("%s", message)
}

// rollback returns the app to its original volumes. The new stack is
// stopped first when it was started: nothing may write to the target
// while it is set aside. Data at the new location is NEVER deleted —
// each copied directory is renamed to <dest>.rollback-<stamp> (same
// mount) so the owner can inspect it or merge writes made after
// cutover. Before containers were recreated, the original file and
// stopped containers are simply restored and restarted. After, the
// recreated containers sit on bind mounts, so compose.yaml is rewritten
// to the original plus explicit pins on the original volumes (a bare
// original file would let Compose create fresh empty volumes).
func (r *migrationRun) rollback(ctx context.Context) (string, error) {
	if r.original == "" {
		return "nothing had changed", nil
	}
	var problems []string
	composePath := filepath.Join(r.opts.Dir, "compose.yaml")
	if r.recreated {
		if err := composeStop(ctx, r.opts.Dir, r.projectName); err != nil {
			// Moving data from under a running stack would be worse
			// than leaving it: stop here and let the owner decide.
			return "", fmt.Errorf("could not stop the migrated stack before rollback: %w", err)
		}
	}
	var aside []string
	if r.copied {
		stamp := r.opts.Now().UTC().Format("20060102T150405Z")
		for _, item := range r.opts.Plan.Items {
			moved, err := moveAside(item.Dest, stamp)
			if err != nil {
				problems = append(problems, err.Error())
			} else if moved != "" {
				aside = append(aside, moved)
			}
		}
	}
	summary := "original stack restored"
	switch {
	case r.recreated:
		pinned, err := PinOriginalVolumes(r.original, r.pins)
		if err != nil {
			return "", err
		}
		if err := writeStackFile(composePath, []byte(pinned), 0o644); err != nil {
			return "", fmt.Errorf("write pinned compose.yaml: %w", err)
		}
		if err := RecordFingerprints(r.opts.Store, r.opts.Plan.Package, r.opts.Dir); err != nil {
			problems = append(problems, err.Error())
		}
		if err := composeUp(ctx, r.opts.Dir, r.projectName); err != nil {
			problems = append(problems, err.Error())
		}
		summary = "restarted on the original volumes (compose.yaml now pins them explicitly)"
	default:
		if r.written {
			if err := writeStackFile(composePath, []byte(r.original), 0o644); err != nil {
				return "", fmt.Errorf("restore compose.yaml: %w", err)
			}
			if err := RecordFingerprints(r.opts.Store, r.opts.Plan.Package, r.opts.Dir); err != nil {
				problems = append(problems, err.Error())
			}
		}
		if r.stopped {
			if err := composeStart(ctx, r.opts.Dir, r.projectName); err != nil {
				problems = append(problems, err.Error())
			}
		}
		summary = "original compose.yaml and containers restored and restarted on the original volumes"
	}
	if len(aside) > 0 {
		summary += "; data at the new location was moved aside, not deleted: " + strings.Join(aside, ", ")
	}
	if len(problems) > 0 {
		return summary, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return summary, nil
}

// moveAside renames a directory holding files to a timestamped sibling
// and returns the new path ("" when it held nothing worth keeping and
// was left in place).
func moveAside(dir, stamp string) (string, error) {
	holds, err := dirHasEntries(dir)
	if err != nil {
		return "", err
	}
	if !holds {
		return "", nil
	}
	target := dir + ".rollback-" + stamp
	for i := 2; ; i++ {
		if _, err := os.Lstat(target); os.IsNotExist(err) {
			break
		}
		target = fmt.Sprintf("%s.rollback-%s-%d", dir, stamp, i)
	}
	if err := os.Rename(dir, target); err != nil {
		return "", fmt.Errorf("move %s aside: %w", dir, err)
	}
	return target, nil
}
