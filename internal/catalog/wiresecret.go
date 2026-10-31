package catalog

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SecretConsumerWired reports whether the installed stack's base Compose
// file already carries one declared secret for every consumer of it: the
// top-level secret, the service reference and the file-path variable.
func SecretConsumerWired(dir string, m Manifest, name string) bool {
	_, missing, err := secretWiringPatch(dir, m, name)
	return err == nil && !missing && len(staleEntrypoints(dir, m, name)) == 0
}

// staleEntrypoints lists the supervisor scripts of services that export
// this secret and whose file in the stack differs from what the manifest
// now generates (S409): a stack from before the export was declared has a
// supervisor that does not read it.
func staleEntrypoints(dir string, m Manifest, name string) []EntrypointScript {
	exporting := map[string]bool{}
	for _, d := range m.Secrets {
		if d.Name != name {
			continue
		}
		for _, c := range d.Consumers {
			if c.Export {
				exporting[c.Service] = true
			}
		}
	}
	var out []EntrypointScript
	for _, sc := range EntrypointScriptsFor(m) {
		if !exporting[sc.Service] {
			continue
		}
		if cur, err := os.ReadFile(filepath.Join(dir, sc.File)); err != nil || string(cur) != sc.Body {
			out = append(out, sc)
		}
	}
	return out
}

// WireDeclaredSecret adds one declared secret's wiring to an installed
// stack's compose.yaml (S403): the top-level secret entry, each
// consuming service's reference and its file-path variable. It changes
// nothing else (volumes, networks, ports, labels and the provenance
// header stay as they are), so a stack installed before the manifest
// declared the secret takes the secret without being regenerated. It
// reports whether it wrote. The secret file must already exist.
func WireDeclaredSecret(dir string, m Manifest, name string) (bool, error) {
	body, missing, err := secretWiringPatch(dir, m, name)
	if err != nil {
		return false, err
	}
	stale := staleEntrypoints(dir, m, name)
	if !missing && len(stale) == 0 {
		return false, nil
	}
	defer lockStackDir(dir)()
	// A secret the service exports reaches it through the supervisor
	// script the stack already mounts; the script is regenerated so it
	// reads the new file too (S409). Scripts hold paths, never values.
	if err := writeEntrypointScripts(dir, stale); err != nil {
		return false, err
	}
	if !missing {
		return true, nil
	}
	path := filepath.Join(dir, "compose.yaml")
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(path, body, info.Mode().Perm())
}

// superviseService puts an installed service under the secret supervisor
// when its stack predates the first exported secret (S411): the
// entrypoint becomes the supervisor, the command the image's own start
// followed by the service's existing command, and the script is mounted.
// A service that already has the supervisor is left alone. It reports
// whether it changed anything.
func superviseService(svc *yaml.Node, m Manifest, service string) bool {
	image := serviceEntrypoint(m, service)
	if len(image) == 0 {
		return false
	}
	if ep := mapKey(svc, "entrypoint"); ep != nil && len(ep.Content) == 1 && ep.Content[0].Value == EntrypointName {
		return false
	}
	var existing []*yaml.Node
	if cmd := mapKey(svc, "command"); cmd != nil && cmd.Kind == yaml.SequenceNode {
		existing = cmd.Content
	}
	seq := func(items ...*yaml.Node) *yaml.Node {
		return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: items}
	}
	args := make([]*yaml.Node, 0, len(image)+len(existing))
	for _, a := range image {
		args = append(args, scalar(a))
	}
	args = append(args, existing...)
	setSeq := func(key string, v *yaml.Node) {
		if cur := mapKey(svc, key); cur != nil {
			*cur = *v
			return
		}
		svc.Content = append(svc.Content, scalar(key), v)
	}
	setSeq("entrypoint", seq(scalar(EntrypointName)))
	setSeq("command", seq(args...))
	var vol yaml.Node
	_ = vol.Encode(composeVolume{Type: "bind", Source: "./" + EntrypointFile(service), Target: EntrypointName,
		ReadOnly: true, Bind: &composeBind{CreateHostPath: false}})
	vols := mapKey(svc, "volumes")
	if vols == nil {
		vols = seq()
		svc.Content = append(svc.Content, scalar("volumes"), vols)
	}
	vols.Content = append(vols.Content, &vol)
	return true
}

func mapKey(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func ensureMap(n *yaml.Node, key string) *yaml.Node {
	if v := mapKey(n, key); v != nil {
		return v
	}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
	return v
}

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func setScalar(m *yaml.Node, key, value string) bool {
	if v := mapKey(m, key); v != nil {
		if v.Value == value {
			return false
		}
		v.Kind, v.Tag, v.Value, v.Content = yaml.ScalarNode, "!!str", value, nil
		return true
	}
	m.Content = append(m.Content, scalar(key), scalar(value))
	return true
}

// secretWiringPatch renders the compose file with the secret wired and
// reports whether anything had to change.
func secretWiringPatch(dir string, m Manifest, name string) ([]byte, bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		return nil, false, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, false, fmt.Errorf("catalog: compose.yaml is not a Compose document")
	}
	root := doc.Content[0]
	services := mapKey(root, "services")
	var decl *SecretDecl
	for i := range m.Secrets {
		if m.Secrets[i].Name == name {
			decl = &m.Secrets[i]
		}
	}
	if services == nil || decl == nil {
		return nil, false, fmt.Errorf("catalog: %s has no secret %q to wire", m.Name, name)
	}
	changed := false
	for _, c := range decl.Consumers {
		svc := mapKey(services, c.Service)
		if svc == nil {
			return nil, false, fmt.Errorf("catalog: compose.yaml has no service %q", c.Service)
		}
		key := secretKey(c.Service, name)
		top := ensureMap(root, "secrets")
		if mapKey(top, key) == nil {
			entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			setScalar(entry, "file", "./"+SecretsDirName+"/"+key)
			top.Content = append(top.Content, scalar(key), entry)
			changed = true
		}
		refs := mapKey(svc, "secrets")
		if refs == nil {
			refs = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			svc.Content = append(svc.Content, scalar("secrets"), refs)
		}
		found := false
		for _, r := range refs.Content {
			found = found || r.Value == key
		}
		if !found {
			refs.Content = append(refs.Content, scalar(key))
			changed = true
		}
		if setScalar(ensureMap(svc, "environment"), c.Env, c.value(key)) {
			changed = true
		}
		if c.Export && superviseService(svc, m, c.Service) {
			changed = true
		}
	}
	if !changed {
		return raw, false, nil
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, false, err
	}
	if err := enc.Close(); err != nil {
		return nil, false, err
	}
	return out.Bytes(), true, nil
}
