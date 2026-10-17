package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"homestead/internal/craft"
)

// Craft images (S279, ADR-028). A catalog service whose image is the
// local tag `homestead/craft-<app>:<version>` is built on the appliance
// by the verify-then-build operation, never pulled. This file is the
// extension point S249 uses: the reference convention (craft.ParseRef)
// plus two seams the daemon sets at startup. S249 owns catalog schema
// validation of craft packages (for example refusing registry-qualified
// references and requiring the tag to be listed in the craft manifest).

// CraftChecker gates every Compose up of a stack that runs craft
// images: it returns nil only when the local image behind the tag
// matches the digest recorded at build time. The daemon sets it
// (craft.Pipeline.CheckImage). While unset, craft images are refused
// (fail closed).
var CraftChecker func(ctx context.Context, image string) error

// CraftRecordedID returns the recorded local image ID for a craft tag,
// the reference live drift compares running containers with. The daemon
// sets it (craft.Pipeline.RecordedID).
var CraftRecordedID func(image string) (string, bool)

// craftImages returns the craft references among images, sorted, unique.
func craftImages(images []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, image := range images {
		if _, _, ok := craft.ParseRef(image); ok && !seen[image] {
			seen[image] = true
			out = append(out, image)
		}
	}
	sort.Strings(out)
	return out
}

// checkCraftImages refuses unless every craft image in images matches
// its recorded local build.
func checkCraftImages(ctx context.Context, images []string) error {
	for _, image := range craftImages(images) {
		if CraftChecker == nil {
			return fmt.Errorf("catalog: craft image %s cannot be verified: craft builds are not available", image)
		}
		if err := CraftChecker(ctx, image); err != nil {
			return fmt.Errorf("catalog: refusing to start: %w", err)
		}
	}
	return nil
}

// CheckCraftStack is the exported craft gate for start paths outside this
// package (update apply): nil only when every craft image in the stack's
// compose files matches its recorded local build.
func CheckCraftStack(ctx context.Context, dir, project string) error {
	return checkCraftStack(ctx, dir, project)
}

// craftMarker is how every craft image reference starts.
const craftMarker = "homestead/craft-"

// checkCraftStack fails closed: it reads the stack's compose files raw
// (not through the drift reader, which blanks interpolated images),
// expands ${VAR} references from the stack .env and the daemon
// environment, and checks every craft image. A compose file that cannot
// be read or parsed, or that names a craft image anywhere but a plain
// service image field (include, extends, labels, anchors), is refused.
// Stacks with no craft image and no compose file pass untouched (Compose
// reports a missing file itself).
func checkCraftStack(ctx context.Context, dir, project string) error {
	var images []string
	mentioned := false
	env := dotEnv(dir)
	for _, name := range composeFileCandidates(dir, env) {
		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, name)
		}
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("catalog: refusing to start %s: cannot read %s to check for craft images: %w", project, name, err)
		}
		if strings.Contains(string(raw), craftMarker) {
			mentioned = true
		}
		var doc struct {
			Services map[string]struct {
				Image string `yaml:"image"`
			} `yaml:"services"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("catalog: refusing to start %s: %s does not parse, so its images cannot be checked: %w", project, name, err)
		}
		for _, svc := range doc.Services {
			images = append(images, expandCompose(svc.Image, env))
		}
	}
	found := craftImages(images)
	if mentioned && len(found) == 0 {
		return fmt.Errorf("catalog: refusing to start %s: its compose files name a craft image outside a plain service image field", project)
	}
	return checkCraftImages(ctx, images)
}

// composeFileCandidates lists every file name Compose could load for the
// stack, so the craft gate cannot be sidestepped by a file it does not
// read: the files Homestead writes, the alternative names Compose
// discovers on its own, and any COMPOSE_FILE the stack .env or the
// daemon environment sets (honoured, split on COMPOSE_PATH_SEPARATOR,
// default ":"). Absolute entries are kept as given.
func composeFileCandidates(dir string, env map[string]string) []string {
	names := []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml",
		OverrideFileName, "compose.override.yml", "docker-compose.override.yaml", "docker-compose.override.yml"}
	sep := ":"
	if v, ok := env["COMPOSE_PATH_SEPARATOR"]; ok && v != "" {
		sep = v
	} else if v := os.Getenv("COMPOSE_PATH_SEPARATOR"); v != "" {
		sep = v
	}
	for _, list := range []string{env["COMPOSE_FILE"], os.Getenv("COMPOSE_FILE")} {
		for _, name := range strings.Split(list, sep) {
			if name = strings.TrimSpace(name); name != "" {
				names = append(names, name)
			}
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, name := range names {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// dotEnv reads the stack's .env (KEY=VALUE lines) for interpolation.
func dotEnv(dir string) map[string]string {
	out := map[string]string{}
	raw, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if ok {
			out[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return out
}

// expandCompose interpolates ${VAR}, ${VAR:-default}, ${VAR-default}
// and $VAR the way Compose does for an image field; $$ is a literal $.
func expandCompose(value string, env map[string]string) string {
	lookup := func(name string) (string, bool) {
		if v, ok := env[name]; ok {
			return v, true
		}
		return os.LookupEnv(name)
	}
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '$' || i+1 >= len(value) {
			out.WriteByte(value[i])
			continue
		}
		switch next := value[i+1]; {
		case next == '$':
			out.WriteByte('$')
			i++
		case next == '{':
			end := strings.IndexByte(value[i:], '}')
			if end < 0 {
				out.WriteString(value[i:])
				return out.String()
			}
			expr := value[i+2 : i+end]
			i += end
			name, def, op := expr, "", ""
			for _, sep := range []string{":-", "-", ":?", "?", ":+", "+"} {
				if k := strings.Index(expr, sep); k > 0 {
					name, def, op = expr[:k], expr[k+len(sep):], sep
					break
				}
			}
			v, ok := lookup(name)
			switch op {
			case ":-":
				if v == "" {
					v = def
				}
			case "-":
				if !ok {
					v = def
				}
			}
			out.WriteString(v)
		case next == '_' || (next >= 'A' && next <= 'Z') || (next >= 'a' && next <= 'z'):
			j := i + 1
			for j < len(value) && (value[j] == '_' || (value[j] >= 'A' && value[j] <= 'Z') || (value[j] >= 'a' && value[j] <= 'z') || (value[j] >= '0' && value[j] <= '9')) {
				j++
			}
			v, _ := lookup(value[i+1 : j])
			out.WriteString(v)
			i = j - 1
		default:
			out.WriteByte('$')
		}
	}
	return out.String()
}

// craftImageDrift compares a running container with the recorded local
// digest of a craft image. It returns "" when they match.
func craftImageDrift(desired string, c craftContainer) string {
	if CraftRecordedID == nil {
		return fmt.Sprintf("craft image %s has no recorded local build to compare with", desired)
	}
	recorded, ok := CraftRecordedID(desired)
	if !ok {
		return fmt.Sprintf("craft image %s has no recorded local build to compare with", desired)
	}
	got := c.imageID
	if got == "" {
		return "" // the Engine reported no image ID: cannot judge
	}
	if sameImageID(got, recorded) {
		return ""
	}
	return fmt.Sprintf("running image %s is not the recorded local build %s of %s (the image was replaced or rebuilt since the container was created)",
		shortID(got), shortID(recorded), desired)
}

type craftContainer struct{ imageID string }

// sameImageID compares image IDs after sha256: normalization: equal, or
// one a prefix of the other with at least 12 hex digits (the Engine's
// short form).
func sameImageID(a, b string) bool {
	a, b = strings.TrimPrefix(a, "sha256:"), strings.TrimPrefix(b, "sha256:")
	if a == b {
		return a != ""
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	return len(a) >= 12 && strings.HasPrefix(b, a)
}
