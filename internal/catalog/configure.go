package catalog

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"homestead/internal/operation"
	"homestead/internal/store"
)

// OverrideFileName is the layered configuration document: base
// compose files stay generated-only (S153), and all Homestead-managed
// environment rides here where owners can read it. Removal of the
// file resets every managed variable at once.
const OverrideFileName = "compose.override.yaml"

var validEnvKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ConfigChange sets and unsets variables for one service: Set writes
// values (empty strings write empty, distinctly from unsetting),
// Unset drops keys from managed state. Overlapping keys refuse —
// intent must pick one verb.
type ConfigChange struct {
	Service string
	Set     map[string]string
	Unset   []string
}

// Validate checks change shape: known service, well-formed keys,
// bounded values, no key in both verbs.
func (c ConfigChange) Validate(known map[string]bool) error {
	if !known[c.Service] {
		return fmt.Errorf("catalog: unknown service %q", c.Service)
	}
	for key, value := range c.Set {
		if !validEnvKey.MatchString(key) {
			return fmt.Errorf("catalog: invalid variable %q", key)
		}
		if len(value) > 4096 {
			return fmt.Errorf("catalog: variable %q exceeds 4096 characters", key)
		}
	}
	for _, key := range c.Unset {
		if !validEnvKey.MatchString(key) {
			return fmt.Errorf("catalog: invalid variable %q", key)
		}
		if _, ok := c.Set[key]; ok {
			return fmt.Errorf("catalog: variable %q both set and unset", key)
		}
	}
	return nil
}

// ConfigPlan is the reviewed configuration for one package: manifest
// context plus per-service changes. Values here are operational, not
// secret — secret-backed variables arrive in a later slice, and this
// layer never pretends otherwise.
type ConfigPlan struct {
	Manifest Manifest
	Changes  []ConfigChange
	// Rotate names declared secrets (SecretDecl.Name) to replace with
	// fresh generated values (S266). Values never appear in plans.
	Rotate []string
	// Adopt regenerates only the secret wiring of a stack installed
	// before S266 (see adopt.go); it takes no other change.
	Adopt bool
}

// Validate checks the plan: manifest shape, known unique services,
// well-formed changes.
func (p ConfigPlan) Validate() error {
	if err := p.Manifest.Validate(); err != nil {
		return err
	}
	if p.Adopt && (len(p.Changes) > 0 || len(p.Rotate) > 0) {
		return fmt.Errorf("catalog: adopting secrets takes no other changes")
	}
	if len(p.Rotate) > 1 {
		return fmt.Errorf("catalog: rotate one secret per operation")
	}
	if len(p.Changes) == 0 && len(p.Rotate) == 0 && !p.Adopt {
		return fmt.Errorf("catalog: configuration needs at least one change")
	}
	declared := map[string]SecretDecl{}
	for _, d := range p.Manifest.Secrets {
		declared[d.Name] = d
	}
	seenRotate := map[string]bool{}
	for _, name := range p.Rotate {
		d, ok := declared[name]
		switch {
		case !ok:
			return fmt.Errorf("catalog: %q declares no secret %q", p.Manifest.Name, name)
		case d.Immutable:
			return fmt.Errorf("catalog: secret %q cannot be rotated: replacing it would break existing data", name)
		case seenRotate[name]:
			return fmt.Errorf("catalog: secret %q rotated twice", name)
		}
		seenRotate[name] = true
	}
	for _, change := range p.Changes {
		if err := refuseSecretKeys(p.Manifest, change); err != nil {
			return err
		}
	}
	known := map[string]bool{}
	for _, service := range p.Manifest.Services {
		known[service.Name] = true
	}
	seen := map[string]bool{}
	for i := range p.Changes {
		if err := p.Changes[i].Validate(known); err != nil {
			return fmt.Errorf("catalog: change #%d: %w", i, err)
		}
		if seen[p.Changes[i].Service] {
			return fmt.Errorf("catalog: duplicate changes for service %q", p.Changes[i].Service)
		}
		seen[p.Changes[i].Service] = true
	}
	return nil
}

// refuseSecretKeys keeps secret-backed variables (and the plain
// variables their `*_FILE` forms replace) out of the plaintext override:
// they change only through rotation or registration.
func refuseSecretKeys(m Manifest, change ConfigChange) error {
	for _, decl := range m.Secrets {
		for _, c := range decl.Consumers {
			if c.Service != change.Service {
				continue
			}
			plain := strings.TrimSuffix(c.Env, "_FILE")
			keys := make([]string, 0, len(change.Set)+len(change.Unset))
			for k := range change.Set {
				keys = append(keys, k)
			}
			keys = append(keys, change.Unset...)
			for _, k := range keys {
				if k == c.Env || k == plain {
					return fmt.Errorf("catalog: variable %q on %q is secret-backed; rotate secret %q instead of setting it", k, change.Service, decl.Name)
				}
			}
		}
	}
	return nil
}

// overrideDocument is the layered file shape: environment per service.
type overrideDocument struct {
	Services map[string]overrideService `yaml:"services"`
}

type overrideService struct {
	Environment map[string]string `yaml:"environment,omitempty"`
	// ExtraHosts and Volumes carry app sign-in's wiring (S298): the
	// Authentik name pointed at the appliance edge, and the appliance root.
	ExtraHosts []string `yaml:"extra_hosts,omitempty"`
	Volumes    []string `yaml:"volumes,omitempty"`
}

func (o overrideService) empty() bool {
	return len(o.Environment) == 0 && len(o.ExtraHosts) == 0 && len(o.Volumes) == 0
}

// ApplyConfig merges changes into the stack override file, creating it
// when absent and tolerating foreign keys already there: Homestead
// manages named variables, never whole-file ownership. Removal of all
// variables drops the service section; an empty document removes the
// file, resetting managed state in one gesture. The file writes 0600:
// values are operational, but neighboring secrets deserve no leaks.
func ApplyConfig(dir string, plan ConfigPlan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	path := filepath.Join(dir, OverrideFileName)
	document, err := readOverride(path)
	if err != nil {
		return err
	}
	for _, change := range plan.Changes {
		entry := document.Services[change.Service]
		if entry.Environment == nil {
			entry.Environment = map[string]string{}
		}
		for key, value := range change.Set {
			entry.Environment[key] = value
		}
		for _, key := range change.Unset {
			delete(entry.Environment, key)
		}
		if entry.empty() {
			delete(document.Services, change.Service)
		} else {
			document.Services[change.Service] = entry
		}
	}
	return writeOverride(path, document)
}

func readOverride(path string) (overrideDocument, error) {
	document := overrideDocument{Services: map[string]overrideService{}}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return document, nil
	}
	if err != nil {
		return document, fmt.Errorf("catalog: read %s: %w", OverrideFileName, err)
	}
	var existing overrideDocument
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&existing); err != nil {
		return document, fmt.Errorf("catalog: parse %s: %w", OverrideFileName, err)
	}
	if existing.Services != nil {
		document.Services = existing.Services
	}
	return document, nil
}

// SignInWiring is what app sign-in (S298, ADR-040) layers onto the
// service that talks to Authentik: host entries, read-only mounts and
// plain environment. None of it is secret.
type SignInWiring struct {
	Service    string
	ExtraHosts []string
	Volumes    []string
	Env        map[string]string
}

// WiringNotes reports what ApplySignInWiring did to variables the owner
// may have set: Kept were left in place on off because their value is
// not the one sign-in set; Replaced held another value that sign-in's
// own value now overrides on on.
type WiringNotes struct {
	Kept     []string
	Replaced []string
}

// OverrideEnv returns the override file's environment for one service,
// so a plan can see the owner's values before sign-in touches them.
func OverrideEnv(dir, service string) (map[string]string, error) {
	document, err := readOverride(filepath.Join(dir, OverrideFileName))
	if err != nil {
		return nil, err
	}
	return document.Services[service].Environment, nil
}

// ApplySignInWiring adds the wiring to the override file (on) or takes
// exactly those entries out again (off), leaving every other entry,
// including the owner's environment, as it was. A variable is removed on
// off only while it still holds the value sign-in set; on on, sign-in's
// value wins over a different one, and both cases are reported.
func ApplySignInWiring(dir string, w SignInWiring, on bool) (WiringNotes, error) {
	var notes WiringNotes
	if !validServiceName.MatchString(w.Service) {
		return notes, fmt.Errorf("catalog: invalid service %q", w.Service)
	}
	path := filepath.Join(dir, OverrideFileName)
	document, err := readOverride(path)
	if err != nil {
		return notes, err
	}
	entry := document.Services[w.Service]
	without := func(list, drop []string) []string {
		out := []string{}
		for _, item := range list {
			keep := true
			for _, d := range drop {
				keep = keep && item != d
			}
			if keep {
				out = append(out, item)
			}
		}
		return out
	}
	entry.ExtraHosts = without(entry.ExtraHosts, w.ExtraHosts)
	entry.Volumes = without(entry.Volumes, w.Volumes)
	if entry.Environment == nil {
		entry.Environment = map[string]string{}
	}
	keys := make([]string, 0, len(w.Env))
	for key := range w.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := w.Env[key]
		if !validEnvKey.MatchString(key) {
			return notes, fmt.Errorf("catalog: invalid variable %q", key)
		}
		current, present := entry.Environment[key]
		switch {
		case on:
			if present && current != value {
				notes.Replaced = append(notes.Replaced, key)
			}
			entry.Environment[key] = value
		case present && current == value:
			delete(entry.Environment, key)
		case present:
			notes.Kept = append(notes.Kept, key)
		}
	}
	if on {
		entry.ExtraHosts = append(entry.ExtraHosts, w.ExtraHosts...)
		entry.Volumes = append(entry.Volumes, w.Volumes...)
	}
	if entry.empty() {
		delete(document.Services, w.Service)
	} else {
		document.Services[w.Service] = entry
	}
	return notes, writeOverride(path, document)
}

func writeOverride(path string, document overrideDocument) error {
	if len(document.Services) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("catalog: remove %s: %w", OverrideFileName, err)
		}
		return nil
	}
	var body strings.Builder
	body.WriteString("# Generated by Homestead (S157). Homestead-managed environment lives here;\n# base compose files stay generated-only. Remove this file to reset all managed variables.\n")
	encoder := yaml.NewEncoder(&body)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return fmt.Errorf("catalog: render %s: %w", OverrideFileName, err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("catalog: render %s: %w", OverrideFileName, err)
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o600); err != nil {
		return fmt.Errorf("catalog: write %s: %w", OverrideFileName, err)
	}
	return nil
}

// ConfigureDefinition plans configuration: apply the reviewed plan to
// the override file, then bring the stack up so containers adopt it.
// Only reviewed plans execute: empty and invalid plans refuse before
// anything writes.
func ConfigureDefinition(plan ConfigPlan, dir, project string, st *store.Store, secrets SecretStore) (operation.Definition, error) {
	if err := plan.Validate(); err != nil {
		return operation.Definition{}, err
	}
	if dir == "" || project == "" {
		return operation.Definition{}, fmt.Errorf("catalog: configure needs directory and project")
	}
	if err := RefuseDuringMigration(project); err != nil {
		return operation.Definition{}, err
	}
	if st == nil {
		return operation.Definition{}, fmt.Errorf("catalog: configure needs a store")
	}
	def := operation.Definition{Type: "catalog.configure"}
	if len(plan.Rotate) > 0 || len(plan.Manifest.Secrets) > 0 {
		if secrets == nil {
			return operation.Definition{}, fmt.Errorf("catalog: configure of %q needs a secret store", plan.Manifest.Name)
		}
	}
	if plan.Adopt {
		if err := AdoptPreflight(plan.Manifest, dir, secrets); err != nil {
			return operation.Definition{}, err
		}
		def.Steps = append(def.Steps, adoptSteps(plan.Manifest, dir, project, st, secrets)...)
		return def, nil
	}
	wired := composeSecretsWired(dir, plan.Manifest)
	if len(plan.Rotate) > 0 && !wired {
		return operation.Definition{}, fmt.Errorf("catalog: %q has no secret wiring (installed before S266); register its existing secrets or reinstall before rotating", plan.Manifest.Name)
	}
	if wired {
		// Repair path: regenerate missing store values and missing or
		// mis-moded files on every configure, never overwriting values.
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "ensure-secrets",
			Run: func(ctx context.Context) error {
				if err := EnsureSecrets(secrets, plan.Manifest); err != nil {
					return err
				}
				return WriteSecretFiles(dir, SecretFilesFor(plan.Manifest), secrets)
			},
		})
	}
	if len(plan.Rotate) > 0 {
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "rotate-secrets",
			Run: func(ctx context.Context) error {
				defer lockProject(project)()
				return rotateSecret(ctx, plan.Manifest, dir, project, plan.Rotate[0], secrets)
			},
		})
	}
	if len(plan.Changes) > 0 {
		def.Steps = append(def.Steps, operation.StepDef{
			Name: "write-config",
			Run: func(ctx context.Context) error {
				if err := ApplyConfig(dir, plan); err != nil {
					return err
				}
				return RecordFingerprints(st, plan.Manifest.Name, dir)
			},
		})
	}
	def.Steps = append(def.Steps, operation.StepDef{
		Name: "up",
		Run: func(ctx context.Context) error {
			return composeUp(ctx, dir, project)
		},
	})
	return def, nil
}
