package catalog

import (
	"context"
	"fmt"
	"os/exec"

	"homestead/internal/operation"
	"homestead/internal/storage"
)

// composeUp runs detached up for a stack directory and project name,
// stubbed in tests. The docker package owns Compose models; install
// execution owns this seam so full install flows verify without a
// daemon.
var composeUp = func(ctx context.Context, dir, project string) error {
	cmd := exec.CommandContext(ctx, "docker", "compose",
		"--project-directory", dir, "--project-name", project, "up", "-d")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("catalog: compose up %s: %w: %s", project, err, string(out))
	}
	return nil
}

// InstallDefinition plans approved installation: converge storage
// directories, write the stack, and bring it up — one step per
// directory plus write plus up, failing fast with the offender named.
// dir overrides the stack directory (tests isolate here); empty means
// the planned production directory. Only approved plans execute:
// empty packages, missing routes, and unwritten compose refuse before
// anything runs.
func InstallDefinition(plan InstallPlan, registry *storage.Registry, dir string) (operation.Definition, error) {
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
	if dir == "" {
		dir = plan.Compose.Stack.Dir
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
			return WriteStack(dir, files)
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
