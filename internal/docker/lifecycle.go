package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"homestead/internal/operation"
)

// Operation types for stack lifecycle, one per verb for queryability.
//
// Verbs that start containers (up, restart, redeploy) are deliberately
// absent: Compose starts go through the catalog package's craft-gated
// paths (S249, ADR-028), and an ungated definition here would bypass
// the recorded-image check.
const (
	OpPull = "stack.pull"
	OpStop = "stack.stop"
)

// Lifecycle runs through the operation engine (S061): definitions below
// execute via Runner.Start, record per-step results, and stream through
// the S062 endpoints. No down/remove lives here — stopping keeps
// containers (restartable); removal belongs to S158. Surfacing (which
// stacks, when) belongs to the catalog operations (S156+); this file
// owns the verbs.
func composeArgs(stack Stack, action []string, services []string) []string {
	args := []string{
		"compose",
		"--project-directory", stack.Dir,
		"--project-name", stack.Name,
	}
	args = append(args, action...)
	return append(args, services...)
}

// verifyService proves one Compose service runs via compose ps. Deploy
// and reconcile operations close with it: running is the automatable
// proof, deeper serving checks belong to the owning feature slice.
func verifyService(ctx context.Context, stack Stack, service string) error {
	out, err := execRun(ctx, "docker",
		"compose", "--project-directory", stack.Dir, "--project-name", stack.Name,
		"ps", "--format", "json")
	if err != nil {
		return fmt.Errorf("verify %s: %w", service, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var observed struct {
			Service string `json:"Service"`
			State   string `json:"State"`
		}
		if err := json.Unmarshal([]byte(line), &observed); err != nil {
			return fmt.Errorf("verify %s: %w", service, err)
		}
		if observed.Service == service && observed.State == "running" {
			return nil
		}
	}
	return fmt.Errorf("verify %s: not running", service)
}

// runCompose executes one compose invocation in the stack directory.
// Project name rides along explicitly so the model stays authoritative
// regardless of directory naming or file-level `name:` keys.
func runCompose(ctx context.Context, stack Stack, action []string, services []string) error {
	if _, err := execRun(ctx, "docker", composeArgs(stack, action, services)...); err != nil {
		return fmt.Errorf("%s %s: %w", action[0], stack.Name, err)
	}
	return nil
}

type stepSpec struct {
	name string
	args []string
}

func lifecycleDefinition(opType string, steps []stepSpec, stack Stack, services []string) (operation.Definition, error) {
	if err := stack.Validate(); err != nil {
		return operation.Definition{}, err
	}
	def := operation.Definition{Type: opType}
	for _, step := range steps {
		step := step
		def.Steps = append(def.Steps, operation.StepDef{
			Name: step.name,
			Run: func(ctx context.Context) error {
				return runCompose(ctx, stack, step.args, services)
			},
		})
	}
	return def, nil
}

// PullDefinition fetches images without starting anything.
func PullDefinition(stack Stack, services ...string) (operation.Definition, error) {
	return lifecycleDefinition(OpPull, []stepSpec{{"pull", []string{"pull"}}}, stack, services)
}

// StopDefinition stops containers without removing them.
func StopDefinition(stack Stack, services ...string) (operation.Definition, error) {
	return lifecycleDefinition(OpStop, []stepSpec{{"stop", []string{"stop"}}}, stack, services)
}
