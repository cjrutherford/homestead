package catalog

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/storage"
	"homestead/internal/store"
)

// composeMu serializes Compose invocations per project: concurrent
// up/down on one stack race container naming (proven live when two
// configures collided mid-recreate), so the seam holds one lock per
// project. Different projects proceed in parallel.
var composeMu sync.Map

// lockProject holds the project lock, releasing on done.
func lockProject(project string) func() {
	mu, _ := composeMu.LoadOrStore(project, &sync.Mutex{})
	m := mu.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

// composeUp runs detached up for a stack directory and project name,
// stubbed in tests. The docker package owns Compose models; install
// execution owns this seam so full install flows verify without a
// daemon. Removal shares it (remove.go) rather than growing a second.
var composeUp = func(ctx context.Context, dir, project string) error {
	defer lockProject(project)()
	// Locally built craft images start only while they match their
	// recorded digest (S279): install, configure, restore and adoption
	// all come through here.
	if err := checkCraftStack(ctx, dir, project); err != nil {
		return err
	}
	return composeRun(ctx, dir, project, "up", "-d")
}

// ensureEdgeNetwork creates the shared edge network when missing, stubbed
// in tests. It does not deploy the proxy.
var ensureEdgeNetwork = docker.EnsureEdgeNetwork

// composeDown stops and removes an installed stack's containers and
// networks without touching volumes or binds: data always survives
// down by construction.
var composeDown = func(ctx context.Context, dir, project string) error {
	defer lockProject(project)()
	return composeRun(ctx, dir, project, "down")
}

// composeRun shells one compose invocation, stubbed in tests.
var composeRun = func(ctx context.Context, dir, project string, args ...string) error {
	argv := append([]string{"compose", "--project-directory", dir, "--project-name", project}, args...)
	cmd := exec.CommandContext(ctx, "docker", argv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("catalog: compose %s %s: %w: %s", strings.Join(args, " "), project, err, string(out))
	}
	return nil
}

// InstallDefinition plans approved installation: converge storage
// directories, write the stack, and bring it up — one step per
// directory plus write plus up, failing fast with the offender named.
// dir overrides the stack directory (tests isolate here); empty means
// the planned production directory. Only approved plans execute:
// empty packages, missing routes, and unwritten compose refuse before
// anything runs. The write step re-baselines drift fingerprints, so
// edits after install read as drift (S212). Declared secrets generate
// into the secret store when absent (S266).
func InstallDefinition(plan InstallPlan, registry *storage.Registry, dir string, st *store.Store, secrets SecretStore) (operation.Definition, error) {
	if plan.Package == "" {
		return operation.Definition{}, fmt.Errorf("catalog: install needs a package")
	}
	if len(plan.Routes) == 0 {
		return operation.Definition{}, fmt.Errorf("catalog: install needs routes")
	}
	if plan.Compose.ComposeYAML == "" {
		return operation.Definition{}, fmt.Errorf("catalog: install needs compose")
	}
	if registry == nil {
		return operation.Definition{}, fmt.Errorf("catalog: install needs a registry")
	}
	if st == nil {
		return operation.Definition{}, fmt.Errorf("catalog: install needs a store")
	}
	if len(plan.Compose.SecretFiles) > 0 && secrets == nil {
		return operation.Definition{}, fmt.Errorf("catalog: install of %q needs a secret store", plan.Package)
	}
	if dir == "" {
		dir = plan.Compose.Stack.Dir
	}
	if err := RefuseInstalled(st, plan.Package, dir, nil); err != nil {
		return operation.Definition{}, err
	}
	def := operation.Definition{Type: "catalog.install"}
	if len(craftImages(plan.Compose.Images)) > 0 {
		// A craft image must be built and recorded first (S279); the
		// check also runs again inside composeUp.
		images := plan.Compose.Images
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "verify-craft-images",
			Run: func(ctx context.Context) error {
				return checkCraftImages(ctx, images)
			},
		})
	}
	if len(plan.Compose.SecretFiles) > 0 {
		// A new database password over an initialised cluster would be
		// rejected: Postgres applies POSTGRES_PASSWORD_FILE only at
		// initdb. Refuse before anything is generated or provisioned.
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "check-existing-database",
			Run: func(ctx context.Context) error {
				return refuseExistingCluster(plan, secrets)
			},
		})
	}
	if plan.EdgeNetwork {
		// Routed stacks join the external edge network, which only the
		// proxy deploy used to create. Make it exist (idempotent) so a
		// fresh appliance can install; the proxy itself is not deployed.
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "ensure-" + docker.NetworkEdge,
			Run:  ensureEdgeNetwork,
		})
	}
	for i := range plan.Directories {
		provision := plan.Directories[i]
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "provision:" + provision.Directory.Path,
			Run: func(ctx context.Context) error {
				return storage.Provision(registry, provision.Mountpoint, []storage.Directory{provision.Directory})
			},
		})
	}
	if len(plan.Seeds) > 0 {
		seeds := plan.Seeds
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "seed-first-start",
			Run: func(ctx context.Context) error {
				return writeSeeds(seeds)
			},
		})
	}
	files := plan.Compose
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "write-stack",
		Run: func(ctx context.Context) error {
			// Generate any secret the store lacks (never overwriting),
			// then materialize the 0600 files beside compose.yaml.
			if err := ensureFileSecrets(secrets, files.SecretFiles); err != nil {
				return err
			}
			if err := WriteStack(dir, files, secrets); err != nil {
				return rollbackInstall(ctx, st, plan.Package, dir, false, err)
			}
			if err := RecordFingerprints(st, plan.Package, dir); err != nil {
				return rollbackInstall(ctx, st, plan.Package, dir, false, err)
			}
			return nil
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "up",
		Run: func(ctx context.Context) error {
			if err := composeUp(ctx, dir, plan.Package); err != nil {
				return rollbackInstall(ctx, st, plan.Package, dir, true, err)
			}
			return nil
		},
	})
	return def, nil
}

// rollbackInstall un-records a failed install so the package reads as not
// installed and a retry is allowed (the install route answers 409 for any
// recorded stack). It removes only what the install wrote: containers and
// networks the failed up created (compose down, never with volumes), the
// generated stack files and their fingerprint records. Provisioned data
// directories, bind-mounted data, the edge network and generated secrets
// stay: they may hold owner data (a partly started database may have
// initialised on them) and a retry reuses them. Cleanup problems are
// appended to the original error, which always wins.
func rollbackInstall(ctx context.Context, st *store.Store, pkg, dir string, started bool, cause error) error {
	var problems []string
	if started {
		// A fresh context: the failure may be a cancelled one.
		downCtx := ctx
		if ctx.Err() != nil {
			downCtx = context.WithoutCancel(ctx)
		}
		if err := composeDown(downCtx, dir, pkg); err != nil {
			problems = append(problems, fmt.Sprintf("stop containers: %v", err))
		}
	}
	if err := removeStackFiles(st, pkg, dir); err != nil {
		problems = append(problems, fmt.Sprintf("remove generated files: %v", err))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w (install was not rolled back cleanly: %s; run homesteadctl apps uninstall %s)", cause, strings.Join(problems, "; "), pkg)
	}
	return fmt.Errorf("%w (rolled back: %s is not installed, your data and settings were kept; fix the problem and install again)", cause, pkg)
}

// refuseExistingCluster fails when a database password would be newly
// generated while a database data directory already holds an
// initialised cluster (PG_VERSION present).
func refuseExistingCluster(plan InstallPlan, secrets SecretStore) error {
	for _, f := range plan.Compose.SecretFiles {
		if !f.DatabasePassword {
			continue
		}
		if v, err := secrets.Get(f.StoreName); err == nil && v != "" {
			continue
		}
		for _, mount := range plan.Mounts {
			if !mount.Database || mount.Ephemeral {
				continue
			}
			if _, err := os.Stat(filepath.Join(mount.Source, "PG_VERSION")); err == nil {
				return fmt.Errorf("catalog: %s already holds an initialised database and %q has no stored password; a new password would be rejected. Register the existing password first (homesteadctl apps secrets register %s %s --value-file -)",
					mount.Source, f.StoreName, plan.Package, f.Name)
			}
		}
	}
	return nil
}

// UpStack brings an installed stack up so its containers adopt a changed
// override file (app sign-in, S298). Same lock and craft checks as install.
func UpStack(ctx context.Context, dir, project string) error {
	return composeUp(ctx, dir, project)
}

// writeSeeds writes each first-start file where nothing is (ADR-044):
// an app that already has its configuration keeps it untouched.
func writeSeeds(seeds []PlannedSeed) error {
	for _, seed := range seeds {
		if _, err := os.Lstat(seed.Source); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("seed %s: %w", seed.Target, err)
		}
		if err := os.MkdirAll(filepath.Dir(seed.Source), 0o755); err != nil {
			return fmt.Errorf("seed %s: %w", seed.Target, err)
		}
		f, err := os.OpenFile(seed.Source, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return fmt.Errorf("seed %s: %w", seed.Target, err)
		}
		_, werr := f.WriteString(seed.Content)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return fmt.Errorf("seed %s: %w", seed.Target, werr)
		}
	}
	return nil
}
