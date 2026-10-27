package catalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Moving an app installed under a name of its own to its path (ADR-044
// §5). The package's own serving settings are what the move changes:
// the environment variables that carry the route (into the override
// file, never the generated compose) and the first-start files whose
// elements name the path. Nothing here runs without an approved plan.

// MoveEnv is the environment a package served by path needs on an
// existing install: every variable whose value names the route, filled
// for the appliance names, and every variable set to the path itself.
// names[0] is the name the app builds its links on.
func MoveEnv(m Manifest, names []string) []ConfigChange {
	if !m.ServedByPath() || len(names) == 0 {
		return nil
	}
	path := m.AppPath()
	bare := strings.TrimSuffix(path, "/")
	var out []ConfigChange
	for _, svc := range m.Services {
		set := map[string]string{}
		for key, value := range svc.Environment {
			switch {
			case strings.Contains(value, RoutePathToken) || strings.Contains(value, RouteURLToken) ||
				strings.Contains(value, RouteHostToken) || strings.Contains(value, RouteNamesToken):
				value = strings.ReplaceAll(value, RouteURLToken, "https://"+names[0]+bare)
				value = strings.ReplaceAll(value, RoutePathToken, bare)
				value = strings.ReplaceAll(value, RouteHostToken, names[0])
				value = strings.ReplaceAll(value, RouteNamesToken, strings.Join(names, " "))
				set[key] = value
			case value == path || value == bare:
				set[key] = value
			}
		}
		if len(set) > 0 {
			out = append(out, ConfigChange{Service: svc.Name, Set: set})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out
}

// MoveSeed is one first-start file an existing install gets its path
// settings in.
type MoveSeed struct {
	Service string
	Target  string
	Source  string
	// Elements are the XML elements set in the existing file, by name.
	Elements map[string]string
	// Content is written whole when no file is there yet.
	Content string
}

var leafElement = regexp.MustCompile(`<([A-Za-z][A-Za-z0-9]*)>([^<]*)</([A-Za-z][A-Za-z0-9]*)>`)

// MoveSeeds resolves the package's first-start files on an installed
// stack: the host path from the stack's compose file, and the elements
// that name the path.
func MoveSeeds(m Manifest, dir string) ([]MoveSeed, error) {
	if len(m.Seeds) == 0 {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		return nil, fmt.Errorf("catalog: read %s's compose file: %w", m.Name, err)
	}
	var doc struct {
		Services map[string]struct {
			Volumes []composeVolume `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("catalog: parse %s's compose file: %w", m.Name, err)
	}
	bare := strings.TrimSuffix(m.AppPath(), "/")
	var out []MoveSeed
	for _, seed := range m.Seeds {
		mount, ok := seedMount(m, seed)
		if !ok {
			return nil, fmt.Errorf("catalog: seed %s has no data path", seed.Target)
		}
		source := ""
		for _, v := range doc.Services[seed.Service].Volumes {
			if v.Type == "bind" && v.Target == mount.Target && filepath.IsAbs(v.Source) {
				source = filepath.Join(v.Source, strings.TrimPrefix(seed.Target, strings.TrimSuffix(mount.Target, "/")))
			}
		}
		if source == "" {
			return nil, fmt.Errorf("catalog: %s's compose file mounts no data path for %s", m.Name, seed.Target)
		}
		content := strings.ReplaceAll(seed.Content, RoutePathToken, bare)
		elements := map[string]string{}
		for _, match := range leafElement.FindAllStringSubmatch(content, -1) {
			if match[1] == match[3] && strings.Contains(seed.Content, "<"+match[1]+">"+RoutePathToken) {
				elements[match[1]] = match[2]
			}
		}
		out = append(out, MoveSeed{Service: seed.Service, Target: seed.Target, Source: source, Elements: elements, Content: content})
	}
	return out, nil
}

// ApplyMoveSeed sets the seed's elements in the existing file, or writes
// the whole seed when there is none. An element present is replaced
// (including the empty <X /> form); one missing goes before the root's
// closing tag. Nothing else in the file changes.
func ApplyMoveSeed(seed MoveSeed) error {
	data, err := os.ReadFile(seed.Source)
	if os.IsNotExist(err) {
		return writeSeeds([]PlannedSeed{{Service: seed.Service, Target: seed.Target, Source: seed.Source, Content: seed.Content}})
	}
	if err != nil {
		return fmt.Errorf("seed %s: %w", seed.Target, err)
	}
	text := string(data)
	names := make([]string, 0, len(seed.Elements))
	for name := range seed.Elements {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := seed.Elements[name]
		element := "<" + name + ">" + value + "</" + name + ">"
		full := regexp.MustCompile(`<` + name + `>[^<]*</` + name + `>`)
		empty := regexp.MustCompile(`<` + name + `\s*/>`)
		switch {
		case full.MatchString(text):
			text = full.ReplaceAllLiteralString(text, element)
		case empty.MatchString(text):
			text = empty.ReplaceAllLiteralString(text, element)
		default:
			end := strings.LastIndex(text, "</")
			if end < 0 {
				return fmt.Errorf("seed %s: the existing file has no closing root element", seed.Target)
			}
			text = text[:end] + "  " + element + "\n" + text[end:]
		}
	}
	info, err := os.Stat(seed.Source)
	if err != nil {
		return err
	}
	return os.WriteFile(seed.Source, []byte(text), info.Mode().Perm())
}

// StopStack stops an installed stack's containers without removing
// them, so files they rewrite on exit are not raced (the move, ADR-044).
var StopStack = func(ctx context.Context, dir, project string) error {
	defer lockProject(project)()
	return composeRun(ctx, dir, project, "stop")
}
