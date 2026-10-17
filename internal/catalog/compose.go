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
	// Tmpfs renders a memory-backed mount for an ephemeral path:
	// no source, no persistence, and no anonymous Docker volume.
	Tmpfs bool
	// TmpfsBytes caps the tmpfs size (default defaultTmpfsBytes).
	TmpfsBytes int64
}

// defaultTmpfsBytes bounds ephemeral mounts at 256 MiB so a runaway
// writer cannot exhaust host memory.
const defaultTmpfsBytes int64 = 256 << 20

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
	// EdgeServices, when non-nil, limits the homestead-edge network in
	// Networks to the named services (the routed ones, S249): data
	// services such as databases keep the package network only. Nil
	// attaches every network to every service.
	EdgeServices map[string]bool
	Services     []ServiceGeneration
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
			if !volume.Tmpfs && (volume.Source == "" || volume.Source[0] != '/') {
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
	Type     string        `yaml:"type"`
	Source   string        `yaml:"source,omitempty"`
	Target   string        `yaml:"target"`
	ReadOnly bool          `yaml:"read_only,omitempty"`
	Bind     *composeBind  `yaml:"bind,omitempty"`
	Tmpfs    *composeTmpfs `yaml:"tmpfs,omitempty"`
}

// composeBind refuses implicit host directory creation: a missing
// source (unmounted storage) must fail the start instead of silently
// writing app data onto the root disk (ADR-013).
type composeBind struct {
	CreateHostPath bool `yaml:"create_host_path"`
}

// composeTmpfs sizes an ephemeral mount in bytes.
type composeTmpfs struct {
	Size int64 `yaml:"size"`
}

// composeService renders one service with Homestead conventions:
// deterministic container names, restart unless stopped, and only the
// sections generation was told about.
type composeService struct {
	Image     string   `yaml:"image"`
	Container string   `yaml:"container_name"`
	Restart   string   `yaml:"restart"`
	Command   []string `yaml:"command,omitempty"`
	// Environment carries plain manifest variables and the in-container
	// PATHS of secret files; never a secret value (ADR-021).
	Environment map[string]string `yaml:"environment,omitempty"`
	Secrets     []string          `yaml:"secrets,omitempty"`
	Ports       []string          `yaml:"ports,omitempty"`
	Volumes     []composeVolume   `yaml:"volumes,omitempty"`
	Networks    []string          `yaml:"networks,omitempty"`
}

// composeNetwork renders one top-level network: external Homestead
// networks attach by name, project-scoped ones declare empty for
// Compose to create.
type composeNetwork struct {
	External bool `yaml:"external,omitempty"`
}

// composeSecret is one top-level secret: a relative file source, so the
// stack directory stays relocatable and valid without Homestead.
type composeSecret struct {
	File string `yaml:"file"`
}

// composeFile is the rendered document root.
type composeFile struct {
	Name     string                    `yaml:"name"`
	Services map[string]composeService `yaml:"services"`
	Networks map[string]composeNetwork `yaml:"networks,omitempty"`
	Secrets  map[string]composeSecret  `yaml:"secrets,omitempty"`
}

// StackFiles is the generation outcome: the Compose document plus the
// S074 stack value addressing it.
type StackFiles struct {
	ComposeYAML string
	Stack       docker.Stack
	// SecretFiles names the secret files the document references
	// (metadata only); WriteStack materializes them from the store.
	SecretFiles []SecretFile
	// Images lists the service images the stack runs, in service order
	// (S279 uses it to find locally built craft images).
	Images []string
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
				entry := composeVolume{
					Type: "bind", Source: volume.Source, Target: volume.Target, ReadOnly: volume.ReadOnly,
					Bind: &composeBind{CreateHostPath: false},
				}
				if volume.Tmpfs {
					size := volume.TmpfsBytes
					if size <= 0 {
						size = defaultTmpfsBytes
					}
					entry = composeVolume{Type: "tmpfs", Target: volume.Target, Tmpfs: &composeTmpfs{Size: size}}
				}
				rendered.Volumes = append(rendered.Volumes, entry)
			}
		}
		rendered.Environment, rendered.Secrets = serviceSecretWiring(manifest, name)
		for _, network := range serviceNetworks {
			if network == docker.NetworkEdge && generation.EdgeServices != nil && !generation.EdgeServices[name] {
				continue
			}
			rendered.Networks = append(rendered.Networks, network)
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
	secretFiles := SecretFilesFor(manifest)
	if len(secretFiles) > 0 {
		document.Secrets = map[string]composeSecret{}
		for _, f := range secretFiles {
			document.Secrets[f.Key] = composeSecret{File: "./" + SecretsDirName + "/" + f.Key}
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
		SecretFiles: secretFiles,
		Images:      stackImages(manifest),
	}, nil
}

// serviceSecretWiring renders one service's environment (plain manifest
// variables plus secret file paths) and its secret references.
func serviceSecretWiring(manifest Manifest, service string) (map[string]string, []string) {
	env := map[string]string{}
	for _, svc := range manifest.Services {
		if svc.Name == service {
			for k, v := range svc.Environment {
				env[k] = v
			}
		}
	}
	var secrets []string
	seen := map[string]bool{}
	for _, decl := range manifest.Secrets {
		for _, c := range decl.Consumers {
			if c.Service != service {
				continue
			}
			key := secretKey(service, decl.Name)
			env[c.Env] = c.value(key)
			if !seen[key] {
				seen[key] = true
				secrets = append(secrets, key)
			}
		}
	}
	sort.Strings(secrets)
	if len(env) == 0 {
		env = nil
	}
	return env, secrets
}

// WriteStack writes generated artifacts under dir: compose.yaml, plus
// the 0600 secret files the document references, resolved from the
// secret store (S266, ADR-021). A nil source is only valid for stacks
// without secrets. Secret files land before compose.yaml, so a missing
// secret fails before any reference to it exists.
func WriteStack(dir string, files StackFiles, secrets SecretStore) error {
	if err := files.Stack.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("catalog: stack dir: %w", err)
	}
	if err := WriteSecretFiles(dir, files.SecretFiles, secrets); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(files.ComposeYAML), 0o644); err != nil {
		return fmt.Errorf("catalog: write compose.yaml: %w", err)
	}
	return nil
}

// stackImages lists the manifest's service images in manifest order.
func stackImages(manifest Manifest) []string {
	images := make([]string, 0, len(manifest.Services))
	for _, service := range manifest.Services {
		images = append(images, service.Image)
	}
	return images
}
