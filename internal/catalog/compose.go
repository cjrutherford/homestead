package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"homestead/internal/docker"
)

// PortMapping publishes one container port on a host port. Host ports
// stay unique across the stack; container ports repeat freely across
// services.
type PortMapping struct {
	Host      int
	Container int
}

// VolumeMapping binds one host path into a container path: absolute
// both ends, read-only where the app must not write. Sources resolve
// from class assignments at the caller's layer (S142/S155) — the
// generator never guesses host layout.
type VolumeMapping struct {
	Source   string
	Target   string
	ReadOnly bool
}

// NetworkAttachment joins one network: external Homestead networks
// (edge, platform) attach by name, while an empty set leaves services
// on the Compose default. Which networks serve an app is a planning
// decision (S154); generation only renders it.
type NetworkAttachment struct {
	Name     string
	External bool
}

// ServiceGeneration carries per-service wiring the manifest cannot
// know: published ports and volume bindings. Environment and secrets
// belong to configuration (S157), never here.
type ServiceGeneration struct {
	Service string
	Ports   []PortMapping
	Volumes []VolumeMapping
}

// Generation scopes one render: service wiring plus network
// attachments. Manifest identity, images, and requirements come from
// the manifest itself.
type Generation struct {
	Networks []NetworkAttachment
	Services []ServiceGeneration
}

// validate checks generation against the manifest: known services
// only, sane ports and volumes, known networks, no duplicates.
func (g Generation) validate(manifest Manifest) error {
	known := map[string]bool{}
	for _, service := range manifest.Services {
		known[service.Name] = true
	}
	seenServices := map[string]bool{}
	hostPorts := map[int]bool{}
	for i := range g.Services {
		spec := &g.Services[i]
		if !known[spec.Service] {
			return fmt.Errorf("catalog: unknown service %q", spec.Service)
		}
		if seenServices[spec.Service] {
			return fmt.Errorf("catalog: duplicate service %q", spec.Service)
		}
		seenServices[spec.Service] = true
		for _, port := range spec.Ports {
			if port.Host < 1 || port.Host > 65535 || port.Container < 1 || port.Container > 65535 {
				return fmt.Errorf("catalog: service %q ports must be 1-65535", spec.Service)
			}
			if hostPorts[port.Host] {
				return fmt.Errorf("catalog: duplicate host port %d", port.Host)
			}
			hostPorts[port.Host] = true
		}
		seenTargets := map[string]bool{}
		for _, volume := range spec.Volumes {
			if volume.Source == "" || volume.Source[0] != '/' {
				return fmt.Errorf("catalog: service %q volume source must be absolute", spec.Service)
			}
			if volume.Target == "" || volume.Target[0] != '/' {
				return fmt.Errorf("catalog: service %q volume target must be absolute", spec.Service)
			}
			if seenTargets[volume.Target] {
				return fmt.Errorf("catalog: service %q duplicate target %q", spec.Service, volume.Target)
			}
			seenTargets[volume.Target] = true
		}
	}
	seenNetworks := map[string]bool{}
	for _, network := range g.Networks {
		if network.Name == "" {
			return fmt.Errorf("catalog: network names must not be empty")
		}
		if seenNetworks[network.Name] {
			return fmt.Errorf("catalog: duplicate network %q", network.Name)
		}
		seenNetworks[network.Name] = true
	}
	return nil
}

// composeVolume renders one bind in long syntax: self-documenting for
// owners reading generated files.
type composeVolume struct {
	Type     string `yaml:"type"`
	Source   string `yaml:"source"`
	Target   string `yaml:"target"`
	ReadOnly bool   `yaml:"read_only,omitempty"`
}

// composeService renders one service with Homestead conventions:
// deterministic container names, restart unless stopped, and only the
// sections generation was told about.
type composeService struct {
	Image     string          `yaml:"image"`
	Container string          `yaml:"container_name"`
	Restart   string          `yaml:"restart"`
	Command   []string        `yaml:"command,omitempty"`
	Ports     []string        `yaml:"ports,omitempty"`
	Volumes   []composeVolume `yaml:"volumes,omitempty"`
	Networks  []string        `yaml:"networks,omitempty"`
}

// composeNetwork renders one top-level network: external Homestead
// networks attach by name, project-scoped ones declare empty for
// Compose to create.
type composeNetwork struct {
	External bool `yaml:"external,omitempty"`
}

// composeFile is the rendered document root.
type composeFile struct {
	Name     string                    `yaml:"name"`
	Services map[string]composeService `yaml:"services"`
	Networks map[string]composeNetwork `yaml:"networks,omitempty"`
}

// StackFiles is the generation outcome: the Compose document plus the
// S074 stack value addressing it.
type StackFiles struct {
	ComposeYAML string
	Stack       docker.Stack
}

// Generate renders inspectable Compose artifacts from a manifest plus
// generation inputs: images from the manifest, everything positional
// from the caller. Output is deterministic — sorted services — with a
// provenance header naming package, version, and generator.
func Generate(manifest Manifest, generation Generation) (StackFiles, error) {
	if err := manifest.Validate(); err != nil {
		return StackFiles{}, err
	}
	if err := generation.validate(manifest); err != nil {
		return StackFiles{}, err
	}
	images := map[string]string{}
	for _, service := range manifest.Services {
		images[service.Name] = service.Image
	}
	byService := map[string]ServiceGeneration{}
	for _, spec := range generation.Services {
		byService[spec.Service] = spec
	}
	var names []string
	commands := map[string][]string{}
	for _, service := range manifest.Services {
		names = append(names, service.Name)
		commands[service.Name] = service.Command
	}
	sort.Strings(names)
	document := composeFile{Name: manifest.Name, Services: map[string]composeService{}}
	var serviceNetworks []string
	for _, network := range generation.Networks {
		serviceNetworks = append(serviceNetworks, network.Name)
	}
	for _, name := range names {
		rendered := composeService{
			Image:     images[name],
			Container: manifest.Name + "-" + name,
			Restart:   "unless-stopped",
			Command:   commands[name],
		}
		if spec, ok := byService[name]; ok {
			for _, port := range spec.Ports {
				rendered.Ports = append(rendered.Ports, fmt.Sprintf("%d:%d", port.Host, port.Container))
			}
			for _, volume := range spec.Volumes {
				rendered.Volumes = append(rendered.Volumes, composeVolume{
					Type: "bind", Source: volume.Source, Target: volume.Target, ReadOnly: volume.ReadOnly,
				})
			}
		}
		if len(serviceNetworks) > 0 {
			rendered.Networks = append([]string(nil), serviceNetworks...)
		}
		document.Services[name] = rendered
	}
	if len(generation.Networks) > 0 {
		document.Networks = map[string]composeNetwork{}
		for _, network := range generation.Networks {
			entry := composeNetwork{}
			if network.External {
				entry.External = true
			}
			document.Networks[network.Name] = entry
		}
	}
	var body strings.Builder
	fmt.Fprintf(&body, "# Generated by Homestead (S153) from %s %s. Inspect freely;\n# regeneration overwrites, so hand edits belong in later slices, not here.\n", manifest.Name, manifest.Version)
	encoder := yaml.NewEncoder(&body)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return StackFiles{}, fmt.Errorf("catalog: render compose: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return StackFiles{}, fmt.Errorf("catalog: render compose: %w", err)
	}
	return StackFiles{
		ComposeYAML: body.String(),
		Stack: docker.Stack{
			Name: manifest.Name, Dir: docker.DirFor(manifest.Name),
			Services: names, Enabled: true,
		},
	}, nil
}

// WriteStack writes generated artifacts under dir: compose.yaml only,
// since application stacks own no other files at generation time.
// Configuration and secrets arrive in S157 without touching this file.
func WriteStack(dir string, files StackFiles) error {
	if err := files.Stack.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("catalog: stack dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(files.ComposeYAML), 0o644); err != nil {
		return fmt.Errorf("catalog: write compose.yaml: %w", err)
	}
	return nil
}
