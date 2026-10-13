package catalog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"homestead/internal/operation"
	"homestead/internal/secret"
	"homestead/internal/store"
)

// Adopting secrets (S266 review): stacks installed before S266 carry
// database credentials as plaintext environment in the override file and
// no secret wiring in compose.yaml. Adoption regenerates ONLY the wiring
// (secret files, compose `secrets:` and the `*_FILE` variables) around
// the existing stack — never data, never a new password, never a
// re-initialised cluster.

// containerEnv reads one variable from a running container's own
// environment, stubbed in tests.
var containerEnv = func(ctx context.Context, container, name string) (string, bool) {
	out, err := dbExec(ctx, container, "", "printenv", name)
	if err != nil {
		return "", false
	}
	value := strings.TrimRight(out, "\r\n")
	return value, value != ""
}

// plainEnvNames lists the plaintext variables a secret consumer
// replaces: its own name and, for `*_FILE` forms, the plain variable.
func plainEnvNames(c SecretConsumer) []string {
	names := []string{c.Env}
	if plain := strings.TrimSuffix(c.Env, "_FILE"); plain != c.Env {
		names = append(names, plain)
	}
	return names
}

// AdoptPreflight checks adoption can proceed: stack installed, not yet
// wired, and the database password already registered (it cannot be
// guessed or regenerated over an initialised data directory).
func AdoptPreflight(m Manifest, dir string, src SecretStore) error {
	if len(m.Secrets) == 0 {
		return fmt.Errorf("catalog: %q declares no secrets to adopt", m.Name)
	}
	if _, err := os.Stat(filepath.Join(dir, "compose.yaml")); err != nil {
		return fmt.Errorf("catalog: %q is not installed: %w", m.Name, err)
	}
	if composeSecretsWired(dir, m) {
		return fmt.Errorf("catalog: %q already has secret wiring; nothing to adopt", m.Name)
	}
	if src == nil {
		return fmt.Errorf("catalog: adopting secrets needs a secret store")
	}
	if m.Database != nil && m.Database.PasswordSecret != "" {
		name := SecretStoreName(m.Name, m.Database.PasswordSecret)
		if v, err := src.Get(name); err != nil || v == "" {
			return fmt.Errorf("catalog: register the existing database password first (homesteadctl apps secrets register %s %s --value-file -): %q is not in the secret store", m.Name, m.Database.PasswordSecret, name)
		}
	}
	return nil
}

// captureSecrets fills declared secrets the store lacks from the running
// containers' current environment; one that cannot be found must be
// registered by the owner.
func captureSecrets(ctx context.Context, m Manifest, src SecretStore) error {
	for _, decl := range m.Secrets {
		store := SecretStoreName(m.Name, decl.Name)
		if v, err := src.Get(store); err == nil && v != "" {
			continue
		} else if err != nil && !errors.Is(err, secret.ErrNotFound) {
			return fmt.Errorf("catalog: read secret %q: %w", store, err)
		}
		found := ""
		for _, c := range decl.Consumers {
			for _, env := range plainEnvNames(c) {
				if v, ok := containerEnv(ctx, m.Name+"-"+c.Service, env); ok {
					found = v
					break
				}
			}
			if found != "" {
				break
			}
		}
		if found == "" {
			return fmt.Errorf("catalog: secret %q is not in the store and could not be read from the running containers; register it (homesteadctl apps secrets register %s %s --value-file -)", decl.Name, m.Name, decl.Name)
		}
		if err := src.SetApp(store, found); err != nil {
			return fmt.Errorf("catalog: store secret %q: %w", store, err)
		}
	}
	return nil
}

// AdoptWiring edits an existing compose document in place: top-level
// `secrets:`, per-service `secrets:` and environment (secret paths plus
// the manifest's plain variables where absent). Everything else —
// volumes, ports, networks, header — is untouched.
func AdoptWiring(raw string, m Manifest) (string, error) {
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
	for _, svc := range m.Services {
		node := mapValue(services, svc.Name)
		if node == nil || node.Kind != yaml.MappingNode {
			continue
		}
		env, secrets := serviceSecretWiring(m, svc.Name)
		if len(env) == 0 && len(secrets) == 0 {
			continue
		}
		envNode := mapValue(node, "environment")
		if envNode == nil {
			envNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			setMapValue(node, "environment", envNode)
		}
		if envNode.Kind != yaml.MappingNode {
			return "", fmt.Errorf("catalog: service %q environment is not a mapping; adopt it by hand", svc.Name)
		}
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			setMapValue(envNode, k, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: env[k]})
		}
		if len(secrets) > 0 {
			seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			if old := mapValue(node, "secrets"); old != nil && old.Kind == yaml.SequenceNode {
				seq.Content = append(seq.Content, old.Content...)
			}
			for _, key := range secrets {
				present := false
				for _, e := range seq.Content {
					present = present || e.Value == key
				}
				if !present {
					seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key})
				}
			}
			setMapValue(node, "secrets", seq)
		}
	}
	top := mapValue(root, "secrets")
	if top == nil || top.Kind != yaml.MappingNode {
		top = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMapValue(root, "secrets", top)
	}
	for _, f := range SecretFilesFor(m) {
		entry := &yaml.Node{}
		if err := entry.Encode(composeSecret{File: "./" + SecretsDirName + "/" + f.Key}); err != nil {
			return "", err
		}
		setMapValue(top, f.Key, entry)
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

// scrubOverride removes the plaintext variables secrets replace from the
// override file; a document left empty removes the file.
func scrubOverride(dir string, m Manifest) error {
	path := filepath.Join(dir, OverrideFileName)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("catalog: read %s: %w", OverrideFileName, err)
	}
	var doc overrideDocument
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&doc); err != nil {
		return fmt.Errorf("catalog: parse %s: %w", OverrideFileName, err)
	}
	for _, decl := range m.Secrets {
		for _, c := range decl.Consumers {
			entry, ok := doc.Services[c.Service]
			if !ok {
				continue
			}
			for _, name := range plainEnvNames(c) {
				delete(entry.Environment, name)
			}
			if len(entry.Environment) == 0 {
				delete(doc.Services, c.Service)
			} else {
				doc.Services[c.Service] = entry
			}
		}
	}
	if len(doc.Services) == 0 {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("catalog: remove %s: %w", OverrideFileName, err)
		}
		return nil
	}
	var body strings.Builder
	body.WriteString("# Generated by Homestead (S157). Homestead-managed environment lives here;\n# base compose files stay generated-only. Remove this file to reset all managed variables.\n")
	encoder := yaml.NewEncoder(&body)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return fmt.Errorf("catalog: render %s: %w", OverrideFileName, err)
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body.String()), 0o600)
}

// adoptSteps builds the adopt operation steps: capture, rewire, up,
// verify. A failed verification restores the previous compose and
// override bytes and brings the old stack back up.
func adoptSteps(m Manifest, dir, project string, st *store.Store, src SecretStore) []operation.StepDef {
	var prevCompose, prevOverride []byte
	var hadOverride bool
	restoreFiles := func() {
		_ = os.WriteFile(filepath.Join(dir, "compose.yaml"), prevCompose, 0o644)
		if hadOverride {
			_ = os.WriteFile(filepath.Join(dir, OverrideFileName), prevOverride, 0o600)
		}
		_ = os.RemoveAll(filepath.Join(dir, SecretsDirName))
		_ = RecordFingerprints(st, m.Name, dir)
	}
	restore := func(ctx context.Context) {
		restoreFiles()
		_ = composeUp(ctx, dir, project)
	}
	return []operation.StepDef{
		{Name: "capture-secrets", Run: func(ctx context.Context) error {
			return captureSecrets(ctx, m, src)
		}},
		{Name: "adopt-wiring", Run: func(ctx context.Context) error {
			defer lockProject(project)()
			var err error
			if prevCompose, err = os.ReadFile(filepath.Join(dir, "compose.yaml")); err != nil {
				return fmt.Errorf("catalog: read compose.yaml: %w", err)
			}
			if prevOverride, err = os.ReadFile(filepath.Join(dir, OverrideFileName)); err == nil {
				hadOverride = true
			}
			next, err := AdoptWiring(string(prevCompose), m)
			if err != nil {
				return err
			}
			if err := WriteSecretFiles(dir, SecretFilesFor(m), src); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(next), 0o644); err != nil {
				return fmt.Errorf("catalog: write compose.yaml: %w", err)
			}
			if err := scrubOverride(dir, m); err != nil {
				restoreFiles()
				return err
			}
			return RecordFingerprints(st, m.Name, dir)
		}},
		{Name: "up", Run: func(ctx context.Context) error {
			if err := composeUp(ctx, dir, project); err != nil {
				restore(ctx)
				return fmt.Errorf("catalog: adopted stack failed to start; previous configuration restored: %w", err)
			}
			return nil
		}},
		{Name: "verify-secrets", Run: func(ctx context.Context) error {
			if m.Database == nil || m.Database.PasswordSecret == "" {
				return nil
			}
			pw, err := src.Get(SecretStoreName(m.Name, m.Database.PasswordSecret))
			if err == nil {
				err = VerifyDatabasePassword(ctx, m, pw)
			}
			if err != nil {
				restore(ctx)
				return fmt.Errorf("catalog: adopted stack failed verification; previous configuration restored: %w", err)
			}
			return nil
		}},
	}
}
