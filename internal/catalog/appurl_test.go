package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppURLTokenRendersPublicOrLocal(t *testing.T) {
	manifest, err := repoCatalog(t).Get("mealie")
	if err != nil {
		t.Fatal(err)
	}
	local, err := Generate(manifest, Generation{RouteHosts: map[string]string{"mealie": "mealie.lan"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(local.ComposeYAML, "BASE_URL: https://mealie.lan\n") {
		t.Fatalf("local render:\n%s", local.ComposeYAML)
	}
	public, err := Generate(manifest, Generation{RouteHosts: map[string]string{"mealie": "mealie.lan"},
		PublicHosts: map[string]string{"mealie": "mealie.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(public.ComposeYAML, "BASE_URL: https://mealie.example.com\n") {
		t.Fatalf("public render:\n%s", public.ComposeYAML)
	}
	none, err := Generate(manifest, Generation{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(none.ComposeYAML, "BASE_URL") {
		t.Fatalf("unrouted render keeps BASE_URL:\n%s", none.ComposeYAML)
	}
}

func TestAppURLTokenNeedsAnEndpoint(t *testing.T) {
	svc := Service{Name: "db", Image: "postgres:16", Environment: map[string]string{"X": AppURLToken}}
	if err := svc.Validate(map[string]bool{}); err == nil || !strings.Contains(err.Error(), "exposes no endpoint") {
		t.Fatalf("err = %v", err)
	}
}

func TestAppURLChangesFindsStaleValue(t *testing.T) {
	manifest, err := repoCatalog(t).Get("mealie")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"),
		[]byte("services:\n  mealie:\n    environment:\n      BASE_URL: https://mealie.lan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changes, err := AppURLChanges(manifest, dir, "mealie.lan", "mealie.example.com")
	if err != nil || len(changes) != 1 || changes[0].Old != "https://mealie.lan" || changes[0].New != "https://mealie.example.com" {
		t.Fatalf("changes = %+v, %v", changes, err)
	}
	if changes, _ = AppURLChanges(manifest, dir, "mealie.lan", ""); len(changes) != 0 {
		t.Fatalf("local changes = %+v", changes)
	}
	// An override the owner or a plan wrote wins over the compose file.
	if err := os.WriteFile(filepath.Join(dir, OverrideFileName),
		[]byte("services:\n  mealie:\n    environment:\n      BASE_URL: https://mealie.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changes, _ = AppURLChanges(manifest, dir, "mealie.lan", "mealie.example.com"); len(changes) != 0 {
		t.Fatalf("override changes = %+v", changes)
	}
}
