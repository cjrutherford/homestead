package catalog

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"

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
	return composeRun(ctx, dir, project, "up", "-d")
}

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
// edits after install read as drift (S212).
func InstallDefinition(plan InstallPlan, registry *storage.Registry, dir string, st *store.Store) (operation.Definition, error) {
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
	if dir == "" {
		dir = plan.Compose.Stack.Dir
	}
	if err := RefuseInstalled(st, plan.Package, dir, nil); err != nil {
		return operation.Definition{}, err
	}
	def := operation.Definition{Type: "catalog.install"}
	for i := range plan.Directories {
		provision := plan.Directories[i]
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "provision:" + provision.Directory.Path,
			Run: func(ctx context.Context) error {
				return storage.Provision(registry, provision.Mountpoint, []storage.Directory{provision.Directory})
			},
		})
	}
	files := plan.Compose
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "write-stack",
		Run: func(ctx context.Context) error {
			if err := WriteStack(dir, files); err != nil {
				return err
			}
			return RecordFingerprints(st, plan.Package, dir)
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "up",
		Run: func(ctx context.Context) error {
			return composeUp(ctx, dir, plan.Package)
		},
	})
	return def, nil
}
