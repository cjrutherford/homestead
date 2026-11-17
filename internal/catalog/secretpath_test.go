package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func vikunjaManifest(t *testing.T) Manifest {
	t.Helper()
	m, err := repoCatalog(t).Get("vikunja")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestSecretConsumerPathValidation: a consumer mounts its secret file at
// a path (S357); the shapes that cannot work are refused at load.
func TestSecretConsumerPathValidation(t *testing.T) {
	cases := map[string]func(c *SecretConsumer){
		"excludes env, uri and export": func(c *SecretConsumer) { c.Env = "X_FILE" },
		"clean absolute path":          func(c *SecretConsumer) { c.Path = "etc/vikunja/config.yml" },
		"outside /run/secrets":         func(c *SecretConsumer) { c.Path = "/run/secrets/x" },
		"must be a clean absolute":     func(c *SecretConsumer) { c.Path = "/etc/../config.yml" },
		"must be a clean absolute ":    func(c *SecretConsumer) { c.Path = "/etc/a b" },
	}
	for want, mutate := range cases {
		m := vikunjaManifest(t)
		m.Secrets = append([]SecretDecl(nil), m.Secrets...)
		for i := range m.Secrets {
			if m.Secrets[i].Name == "oidc_config" {
				m.Secrets[i].Consumers = append([]SecretConsumer(nil), m.Secrets[i].Consumers...)
				mutate(&m.Secrets[i].Consumers[0])
			}
		}
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), strings.TrimSpace(want)) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
}

func TestSecretConsumerPathRendersLongFormMount(t *testing.T) {
	m := vikunjaManifest(t)
	files, err := Generate(m, Generation{Services: []ServiceGeneration{{Service: "vikunja"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"source: vikunja_oidc_config", "target: /etc/vikunja/config.yml", "- vikunja_jwt_secret"} {
		if !strings.Contains(files.ComposeYAML, w) {
			t.Errorf("compose lacks %q:\n%s", w, files.ComposeYAML)
		}
	}
}

// TestSecretConsumerPathWiresOldStack patches a stack generated before the
// secret was declared with the long-form mount, and a second pass is a no-op.
func TestSecretConsumerPathWiresOldStack(t *testing.T) {
	m := vikunjaManifest(t)
	old := m
	old.Secrets = []SecretDecl{m.Secrets[0]}
	old.SSO.OIDC = nil
	files, err := Generate(old, Generation{Services: []ServiceGeneration{{Service: "vikunja"}}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(files.ComposeYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	out, changed, err := secretWiringPatch(dir, m, "oidc_config")
	if err != nil || !changed {
		t.Fatalf("changed %v, %v", changed, err)
	}
	for _, w := range []string{"source: vikunja_oidc_config", "target: /etc/vikunja/config.yml"} {
		if !strings.Contains(string(out), w) {
			t.Fatalf("patched compose lacks %q:\n%s", w, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), out, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := secretWiringPatch(dir, m, "oidc_config"); err != nil || changed {
		t.Fatalf("second pass changed %v, %v", changed, err)
	}
}
