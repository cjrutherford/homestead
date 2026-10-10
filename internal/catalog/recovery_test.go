package catalog

import (
	"os/exec"
	"strings"
	"testing"
)

// composeAvailable reports a working Compose plugin, skipping
// otherwise: recovery validation needs the real binary, and its
// absence skips instead of failing.
func composeAvailable() bool {
	err := exec.Command("docker", "compose", "version").Run()
	return err == nil
}

// TestGeneratedStacksValidateWithCompose proves the ownership
// receipt (S233): every curated package renders artifacts that
// vanilla `docker compose` accepts with Homestead nowhere in the
// loop — no daemon, no store, no custom extensions. Missing
// engines skip; present ones must agree.
func TestGeneratedStacksValidateWithCompose(t *testing.T) {
	if !composeAvailable() {
		t.Skip("docker compose unavailable")
	}
	catalog := repoCatalog(t)
	for _, name := range catalog.Names() {
		manifest, err := catalog.Get(name)
		if err != nil {
			t.Fatalf("Get(%s): %v", name, err)
		}
		files, err := Generate(manifest, Generation{
			Networks: []NetworkAttachment{{Name: "homestead-edge", External: true}},
		})
		if err != nil {
			t.Fatalf("Generate(%s): %v", name, err)
		}
		if strings.Contains(files.ComposeYAML, "\nx-") {
			t.Fatalf("%s compose carries proprietary extensions", name)
		}
		dir := t.TempDir()
		if err := WriteStack(dir, files); err != nil {
			t.Fatalf("WriteStack(%s): %v", name, err)
		}
		//nolint:gosec // test-only compose invocation on generated files.
		out, err := exec.Command("docker", "compose", "--project-directory", dir, "config").CombinedOutput()
		if err != nil {
			t.Fatalf("%s compose config: %v\n%s", name, err, out)
		}
		// The canonical rendering still names every service: config
		// did not silently drop what generation promised.
		for _, service := range manifest.Services {
			if !strings.Contains(string(out), service.Name+":") {
				t.Fatalf("%s config missing service %q", name, service.Name)
			}
		}
	}
}

// TestOverrideMergesWithCompose proves layered configuration stays
// vanilla too: base plus override validate as one document, so
// owners editing either file get Compose errors, not Homestead ones.
func TestOverrideMergesWithCompose(t *testing.T) {
	if !composeAvailable() {
		t.Skip("docker compose unavailable")
	}
	catalog := repoCatalog(t)
	manifest, err := catalog.Get("jellyfin")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	files, err := Generate(manifest, Generation{
		Networks: []NetworkAttachment{{Name: "homestead-edge", External: true}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	dir := t.TempDir()
	if err := WriteStack(dir, files); err != nil {
		t.Fatalf("WriteStack: %v", err)
	}
	plan := ConfigPlan{
		Manifest: manifest,
		Changes:  []ConfigChange{{Service: "jellyfin", Set: map[string]string{"TZ": "UTC"}}},
	}
	if err := ApplyConfig(dir, plan); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	//nolint:gosec // test-only compose invocation on generated files.
	out, err := exec.Command("docker", "compose", "--project-directory", dir, "config").CombinedOutput()
	if err != nil {
		t.Fatalf("compose config: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "TZ") {
		t.Fatalf("merged config missing override:\n%s", out)
	}
}
