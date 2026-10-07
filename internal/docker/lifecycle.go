package docker

import (
	"context"
	"fmt"

	"homestead/internal/operation"
)

// Operation types for stack lifecycle, one per verb for queryability.
const (
	OpPull     = "stack.pull"
	OpUp       = "stack.up"
	OpStop     = "stack.stop"
	OpRestart  = "stack.restart"
	OpRedeploy = "stack.redeploy"
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

// UpDefinition ensures the stack runs (creates or starts, detached).
func UpDefinition(stack Stack, services ...string) (operation.Definition, error) {
	return lifecycleDefinition(OpUp, []stepSpec{{"up", []string{"up", "-d"}}}, stack, services)
}

// StopDefinition stops containers without removing them.
func StopDefinition(stack Stack, services ...string) (operation.Definition, error) {
	return lifecycleDefinition(OpStop, []stepSpec{{"stop", []string{"stop"}}}, stack, services)
}

// RestartDefinition restarts running containers in place.
func RestartDefinition(stack Stack, services ...string) (operation.Definition, error) {
	return lifecycleDefinition(OpRestart, []stepSpec{{"restart", []string{"restart"}}}, stack, services)
}

// RedeployDefinition pulls fresh images then recreates containers.
func RedeployDefinition(stack Stack, services ...string) (operation.Definition, error) {
	return lifecycleDefinition(OpRedeploy, []stepSpec{
		{"pull", []string{"pull"}},
		{"up", []string{"up", "-d"}},
	}, stack, services)
}
