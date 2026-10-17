package update

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"homestead/internal/catalog"
	"homestead/internal/craft"
	"homestead/internal/operation"
	"homestead/internal/store"
)

// composeYAMLName is the only file updates rewrite: generated stacks
// carry their pins there, and only there.
const composeYAMLName = "compose.yaml"

// execCompose runs detached Compose verbs for update flows, stubbed
// in tests. Models live in docker; execution seams live with their
// operations (see catalog).
var execCompose = func(ctx context.Context, dir, project string, args ...string) (string, error) {
	argv := append([]string{"compose", "--project-directory", dir, "--project-name", project}, args...)
	cmd := exec.CommandContext(ctx, "docker", argv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("update: compose %s %s: %w: %s", strings.Join(args, " "), project, err, string(out))
	}
	return string(out), nil
}

// checkCraftStart gates every Compose up an update runs: a stack that
// runs locally built craft images starts only while each matches its
// recorded digest (S249, ADR-028). Stubbed in tests.
var checkCraftStart = catalog.CheckCraftStack

// UpdateRequest scopes one safe update: the package and stack
// directory, service image moves, and an optional pre-update backup
// hook (nil skips with the plan's backup flag left for callers to
// enforce — this unit never decides backup policy, only runs it).
type UpdateRequest struct {
	Package  string
	StackDir string
	Moves    []ServiceUpdate
	Backup   func(ctx context.Context) error
	// Store, when set, receives the generated-file fingerprints after the
	// pins on disk change and settle (a successful up, or a rollback), so
	// the drift scan does not report Homestead's own rewrite as an edit.
	Store *store.Store
}

// Validate checks update shape: named package and directory, at
// least one move, well-formed images on both ends.
func (r UpdateRequest) Validate() error {
	if r.Package == "" {
		return fmt.Errorf("update: package must not be empty")
	}
	if r.StackDir == "" {
		return fmt.Errorf("update: stack directory must not be empty")
	}
	if len(r.Moves) == 0 {
		return fmt.Errorf("update: nothing to move")
	}
	for i := range r.Moves {
		if r.Moves[i].Service == "" || r.Moves[i].Current == "" || r.Moves[i].Target == "" {
			return fmt.Errorf("update: move #%d needs service, current, and target", i)
		}
		if r.Moves[i].Current == r.Moves[i].Target {
			return fmt.Errorf("update: move #%d goes nowhere", i)
		}
	}
	return nil
}

// rewriteImages swaps pinned images in the stack compose file,
// counting replacements per move: every move must land at least
// once, or the file does not describe the plan and rewriting stops
// before anything runs. Modes preserve whatever the file carries.
func rewriteImages(stackDir string, moves []ServiceUpdate, from, to func(ServiceUpdate) string) error {
	path := filepath.Join(stackDir, composeYAMLName)
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("update: read compose: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("update: stat compose: %w", err)
	}
	text := string(raw)
	for _, move := range moves {
		needle := "image: " + from(move)
		if !strings.Contains(text, needle) {
			return fmt.Errorf("update: image %q not found in compose", from(move))
		}
		text = strings.ReplaceAll(text, needle, "image: "+to(move))
	}
	if err := os.WriteFile(path, []byte(text), info.Mode().Perm()); err != nil {
		return fmt.Errorf("update: write compose: %w", err)
	}
	return nil
}

// psService is the compose-ps running-state subset this unit reads.
type psService struct {
	Service string `json:"Service"`
	State   string `json:"State"`
}

// verifyRunning proves every moved service runs after up: compose ps
// in JSON lines, all named services present and running. Anything
// else — missing, exited, restarting — fails with the observed
// states attached.
func verifyRunning(ctx context.Context, stackDir, project string, moves []ServiceUpdate) error {
	out, err := execCompose(ctx, stackDir, project, "ps", "--format", "json")
	if err != nil {
		return fmt.Errorf("update: verify: %w", err)
	}
	running := map[string]bool{}
	var states []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var service psService
		if err := json.Unmarshal([]byte(line), &service); err != nil {
			return fmt.Errorf("update: verify decode: %w", err)
		}
		states = append(states, service.Service+"="+service.State)
		if service.State == "running" {
			running[service.Service] = true
		}
	}
	for _, move := range moves {
		if !running[move.Service] {
			return fmt.Errorf("update: %s not running (%s)", move.Service, strings.Join(states, ", "))
		}
	}
	return nil
}

// UpdateDefinition plans safe application updates: optional backup,
// image pull, pin rewrite, up with verification, and rollback on
// verify failure — previous images stay local (pulls never delete),
// so rolling pins back and up again restores service, reporting both
// outcomes instead of masking the original failure. Rollback itself
// never recurses: a failed rollback reports alongside, and owners
// intervene with full evidence.
func UpdateDefinition(req UpdateRequest) (operation.Definition, error) {
	if err := req.Validate(); err != nil {
		return operation.Definition{}, err
	}
	def := operation.Definition{Type: "app.update"}
	if req.Backup != nil {
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "backup",
			Run: func(ctx context.Context) error {
				return req.Backup(ctx)
			},
		})
	}
	// Locally built craft images (S279) are never pulled: only the other
	// moved services are, and the step disappears when all are craft.
	if pull := pullServices(req.Moves); pull == nil || len(pull) > 0 {
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "pull",
			Run: func(ctx context.Context) error {
				args := append([]string{"pull"}, pull...)
				_, err := execCompose(ctx, req.StackDir, req.Package, args...)
				return err
			},
		})
	}
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "rewrite",
		Run: func(ctx context.Context) error {
			return rewriteImages(req.StackDir, req.Moves,
				func(move ServiceUpdate) string { return move.Current },
				func(move ServiceUpdate) string { return move.Target })
		},
	})
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "up",
		Run: func(ctx context.Context) error {
			if err := checkCraftStart(ctx, req.StackDir, req.Package); err != nil {
				return err
			}
			if _, err := execCompose(ctx, req.StackDir, req.Package, "up", "-d"); err != nil {
				return err
			}
			if err := verifyRunning(ctx, req.StackDir, req.Package, req.Moves); err != nil {
				rollback := rollbackImages(req)
				recordErr := recordFingerprints(req)
				if recordErr != nil {
					err = fmt.Errorf("%v (%v)", err, recordErr)
				}
				if gateErr := checkCraftStart(ctx, req.StackDir, req.Package); gateErr != nil {
					return fmt.Errorf("%v (rolled back to %v, but %v)", err, rollback, gateErr)
				}
				if _, rollbackErr := execCompose(ctx, req.StackDir, req.Package, "up", "-d"); rollbackErr != nil {
					return fmt.Errorf("%v (rollback %v: %v)", err, rollback, rollbackErr)
				}
				if verifyErr := verifyRunning(ctx, req.StackDir, req.Package, req.Moves); verifyErr != nil {
					return fmt.Errorf("%v (rolled back to %v but still failing: %v)", err, rollback, verifyErr)
				}
				return fmt.Errorf("%v (rolled back to previous images)", err)
			}
			return recordFingerprints(req)
		},
	})
	return def, nil
}

// pullServices names the moved services that need a registry pull. It
// returns nil (pull the whole stack, the historic behavior) when no move
// is a craft image, and an empty non-nil slice when every move is.
func pullServices(moves []ServiceUpdate) []string {
	var registry []string
	craftSeen := false
	for _, move := range moves {
		if _, _, ok := craft.ParseRef(move.Target); ok {
			craftSeen = true
			continue
		}
		registry = append(registry, move.Service)
	}
	if !craftSeen {
		return nil
	}
	if registry == nil {
		return []string{}
	}
	return registry
}

// rollbackImages restores previous pins after a failed update.
func rollbackImages(req UpdateRequest) []string {
	_ = rewriteImages(req.StackDir, req.Moves,
		func(move ServiceUpdate) string { return move.Target },
		func(move ServiceUpdate) string { return move.Current })
	var images []string
	for _, move := range req.Moves {
		images = append(images, move.Current)
	}
	return images
}

// recordFingerprints re-records the stack's managed-file fingerprints
// from what is on disk now. Update and rollback both rewrite
// compose.yaml; without this the next drift scan calls that an edit.
func recordFingerprints(req UpdateRequest) error {
	if req.Store == nil {
		return nil
	}
	if err := catalog.RecordFingerprints(req.Store, req.Package, req.StackDir); err != nil {
		return fmt.Errorf("update: record fingerprints: %w", err)
	}
	return nil
}
