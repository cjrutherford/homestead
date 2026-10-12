package catalog

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// composeVolumeTarget extracts the container path of one volumes entry
// in either syntax: long form (mapping with target) or short form
// (source:target[:options], or a lone target).
func composeVolumeTarget(node *yaml.Node) string {
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == "target" {
				return node.Content[i+1].Value
			}
		}
	case yaml.ScalarNode:
		parts := strings.Split(node.Value, ":")
		if len(parts) == 1 {
			return parts[0]
		}
		return parts[1]
	}
	return ""
}

// mapValue returns the value node for key in a mapping node.
func mapValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// setMapValue replaces or appends key in a mapping node.
func setMapValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

// rewriteComposeVolumes edits a generated Compose document in place:
// for each named service, entries whose target is being replaced are
// dropped and the new entries appended; external named volumes are
// declared at the top level for pinned entries. Everything else —
// header comment, ports, networks, environment — is untouched, so the
// migration changes only where data lives.
func rewriteComposeVolumes(raw string, perService map[string][]composeVolume, external []string) (string, error) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &document); err != nil {
		return "", fmt.Errorf("catalog: parse compose: %w", err)
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return "", fmt.Errorf("catalog: compose document is not a mapping")
	}
	root := document.Content[0]
	services := mapValue(root, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return "", fmt.Errorf("catalog: compose has no services")
	}
	names := make([]string, 0, len(perService))
	for name := range perService {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		service := mapValue(services, name)
		if service == nil || service.Kind != yaml.MappingNode {
			return "", fmt.Errorf("catalog: compose has no service %q", name)
		}
		replaced := map[string]bool{}
		for _, volume := range perService[name] {
			replaced[volume.Target] = true
		}
		volumes := mapValue(service, "volumes")
		if volumes == nil || volumes.Kind != yaml.SequenceNode {
			volumes = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			setMapValue(service, "volumes", volumes)
		}
		kept := volumes.Content[:0:0]
		for _, entry := range volumes.Content {
			if !replaced[composeVolumeTarget(entry)] {
				kept = append(kept, entry)
			}
		}
		volumes.Content = kept
		for _, volume := range perService[name] {
			entry := &yaml.Node{}
			if err := entry.Encode(volume); err != nil {
				return "", fmt.Errorf("catalog: render volume: %w", err)
			}
			volumes.Content = append(volumes.Content, entry)
		}
	}
	if len(external) > 0 {
		top := mapValue(root, "volumes")
		if top == nil || top.Kind != yaml.MappingNode {
			top = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			setMapValue(root, "volumes", top)
		}
		sort.Strings(external)
		for _, volume := range external {
			entry := &yaml.Node{}
			if err := entry.Encode(map[string]bool{"external": true}); err != nil {
				return "", err
			}
			setMapValue(top, volume, entry)
		}
	}
	var out strings.Builder
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return "", fmt.Errorf("catalog: render compose: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return "", fmt.Errorf("catalog: render compose: %w", err)
	}
	return out.String(), nil
}

// ApplyPlacement rewrites a stack's Compose document so every mount
// the plan resolved (bind for persistent paths, tmpfs for ephemeral
// ones) replaces whatever the file had at that target.
func ApplyPlacement(raw string, mounts []PlannedMount) (string, error) {
	perService := map[string][]composeVolume{}
	for _, mount := range mounts {
		entry := composeVolume{
			Type: "bind", Source: mount.Source, Target: mount.Target, ReadOnly: mount.ReadOnly,
			Bind: &composeBind{CreateHostPath: false},
		}
		if mount.Ephemeral {
			size := int64(mount.TmpfsMB) << 20
			if size <= 0 {
				size = defaultTmpfsBytes
			}
			entry = composeVolume{Type: "tmpfs", Target: mount.Target, Tmpfs: &composeTmpfs{Size: size}}
		}
		perService[mount.Service] = append(perService[mount.Service], entry)
	}
	return rewriteComposeVolumes(raw, perService, nil)
}

// PinVolume names one original volume mount to keep a rolled-back
// stack on its original data.
type PinVolume struct {
	Service string
	Target  string
	Volume  string
}

// PinOriginalVolumes rewrites a Compose document so each service mounts
// its original named volumes explicitly (declared external). A plain
// restart of the original file would let Compose create fresh empty
// anonymous volumes once containers were recreated; pinning keeps the
// original data attached through any later recreate.
func PinOriginalVolumes(raw string, pins []PinVolume) (string, error) {
	perService := map[string][]composeVolume{}
	var external []string
	seen := map[string]bool{}
	for _, pin := range pins {
		perService[pin.Service] = append(perService[pin.Service],
			composeVolume{Type: "volume", Source: pin.Volume, Target: pin.Target})
		if !seen[pin.Volume] {
			seen[pin.Volume] = true
			external = append(external, pin.Volume)
		}
	}
	return rewriteComposeVolumes(raw, perService, external)
}
