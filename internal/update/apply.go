package update

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
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

// rewriteImages swaps pinned image scalars in the stack compose file.
// It resolves moves against the original YAML document so services
// sharing an image can each have a move without the first replacement
// consuming the second one's match. Scalar edits are spliced into the
// original bytes to preserve comments, formatting and file mode.
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
	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("update: parse compose: %w", err)
	}
	if len(document.Content) != 1 {
		return fmt.Errorf("update: compose has no document mapping")
	}
	services := mappingValue(document.Content[0], "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return fmt.Errorf("update: compose has no services mapping")
	}
	type scalarEdit struct {
		start, end  int
		replacement string
	}
	var edits []scalarEdit
	lines := strings.Split(string(raw), "\n")
	seen := make(map[string]bool, len(moves))
	for _, move := range moves {
		if move.Service == "" || seen[move.Service] {
			return fmt.Errorf("update: missing or duplicate service %q", move.Service)
		}
		seen[move.Service] = true
		var service *yaml.Node
		for i := 0; i+1 < len(services.Content); i += 2 {
			if services.Content[i].Value == move.Service {
				if service != nil {
					return fmt.Errorf("update: duplicate service %q", move.Service)
				}
				service = services.Content[i+1]
			}
		}
		if service == nil || service.Kind != yaml.MappingNode {
			return fmt.Errorf("update: service %q not found as a mapping in compose", move.Service)
		}
		var image *yaml.Node
		for i := 0; i+1 < len(service.Content); i += 2 {
			if service.Content[i].Value == "image" {
				if image != nil {
					return fmt.Errorf("update: duplicate image in service %q", move.Service)
				}
				image = service.Content[i+1]
			}
		}
		if image == nil || image.Kind != yaml.ScalarNode || image.Value != from(move) {
			return fmt.Errorf("update: service %q image does not match %q", move.Service, from(move))
		}
		lineIndex := image.Line - 1
		if lineIndex < 0 || lineIndex >= len(lines) {
			return fmt.Errorf("update: invalid image location in compose")
		}
		// yaml columns count Unicode characters, while string offsets count bytes.
		runes := []rune(lines[lineIndex])
		if image.Column < 1 || image.Column-1 >= len(runes) {
			return fmt.Errorf("update: invalid image column")
		}
		column := len(string(runes[:image.Column-1]))
		start, end, err := imageScalarBounds(lines[lineIndex], column, image.Style, service.Style&yaml.FlowStyle != 0)
		if err != nil {
			return fmt.Errorf("update: service %q image: %w", move.Service, err)
		}
		var parsed string
		if err := yaml.Unmarshal([]byte(lines[lineIndex][start:end]), &parsed); err != nil || parsed != image.Value {
			return fmt.Errorf("update: service %q has unsupported image scalar syntax", move.Service)
		}
		lineStart := 0
		for n := 0; n < lineIndex; n++ {
			lineStart += len(lines[n]) + 1
		}
		replacement := to(move)
		switch {
		case image.Style&yaml.DoubleQuotedStyle != 0:
			replacement = yamlQuoteDouble(replacement)
		case image.Style&yaml.SingleQuotedStyle != 0:
			replacement = "'" + strings.ReplaceAll(replacement, "'", "''") + "'"
		default:
			// Plain replacements must remain a single plain YAML scalar.
			var check yaml.Node
			if err := yaml.Unmarshal([]byte(replacement), &check); err != nil || len(check.Content) != 1 || check.Content[0].Kind != yaml.ScalarNode || check.Content[0].Value != replacement || check.Content[0].Style != 0 || strings.ContainsAny(replacement, "\r\n{}[],") {
				return fmt.Errorf("update: service %q has unsupported replacement image", move.Service)
			}
		}
		edits = append(edits, scalarEdit{start: lineStart + start, end: lineStart + end, replacement: replacement})
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	for i := 1; i < len(edits); i++ {
		if edits[i-1].end > edits[i].start {
			return fmt.Errorf("update: overlapping image scalars")
		}
	}
	text := string(raw)
	for i := len(edits) - 1; i >= 0; i-- {
		edit := edits[i]
		text = text[:edit.start] + edit.replacement + text[edit.end:]
	}
	var rewritten yaml.Node
	if err := yaml.Unmarshal([]byte(text), &rewritten); err != nil {
		return fmt.Errorf("update: rewritten compose is invalid: %w", err)
	}
	if err := os.WriteFile(path, []byte(text), info.Mode().Perm()); err != nil {
		return fmt.Errorf("update: write compose: %w", err)
	}
	return nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func imageScalarBounds(line string, start int, style yaml.Style, flow bool) (int, int, error) {
	if start < 0 || start >= len(line) {
		return 0, 0, fmt.Errorf("invalid scalar column")
	}
	switch {
	case style&yaml.DoubleQuotedStyle != 0:
		for i := start + 1; i < len(line); i++ {
			if line[i] == '\\' {
				i++
				continue
			}
			if line[i] == '"' {
				return start, i + 1, nil
			}
		}
	case style&yaml.SingleQuotedStyle != 0:
		for i := start + 1; i < len(line); i++ {
			if line[i] == '\'' {
				if i+1 < len(line) && line[i+1] == '\'' {
					i++
					continue
				}
				return start, i + 1, nil
			}
		}
	default:
		end := start
		for end < len(line) && line[end] != ' ' && line[end] != '\t' && line[end] != '\r' && !(flow && strings.ContainsRune("{},[]", rune(line[end]))) {
			end++
		}
		if end > start {
			return start, end, nil
		}
	}
	return 0, 0, fmt.Errorf("could not locate scalar text")
}

func yamlQuoteDouble(value string) string {
	return strconv.Quote(value)
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
				rollback, rollbackErr := rollbackImages(req)
				if rollbackErr != nil {
					return fmt.Errorf("%v (rollback rewrite failed: %w)", err, rollbackErr)
				}
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
func rollbackImages(req UpdateRequest) ([]string, error) {
	err := rewriteImages(req.StackDir, req.Moves,
		func(move ServiceUpdate) string { return move.Target },
		func(move ServiceUpdate) string { return move.Current })
	if err != nil {
		return nil, err
	}
	var images []string
	for _, move := range req.Moves {
		images = append(images, move.Current)
	}
	return images, nil
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
