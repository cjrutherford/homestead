package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/store"
)

// catalogServerFrom mounts a server over an existing catalog directory.
func catalogServerFrom(t *testing.T, dir string) *Server {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	srv := New("127.0.0.1:0")
	srv.MountCatalog(s, dir, nil)
	return srv
}

// TestCatalogGetResolvesIntegrations proves the detail response carries
// declarations already resolved to levels and target titles (S341), and
// that a package with none carries an empty list rather than null.
func TestCatalogGetResolvesIntegrations(t *testing.T) {
	srv := catalogServerFrom(t, "../../catalog")

	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/jellyfin", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var detail struct {
		Name  string
		Works []struct {
			Kind   string
			Level  string
			Target string
			Title  string
			Note   string
		}
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail.Name != "jellyfin" {
		t.Fatalf("detail = %+v", detail)
	}
	if len(detail.Works) != 2 {
		t.Fatalf("works = %+v", detail.Works)
	}
	identity := detail.Works[0]
	if identity.Kind != "identity" || identity.Level != "L2" ||
		identity.Target != "authentik" || identity.Title != "Authentik" || identity.Note == "" {
		t.Fatalf("identity = %+v", identity)
	}
	library := detail.Works[1]
	if library.Kind != "library" || library.Level != "L3" || library.Title != "Nextcloud" {
		t.Fatalf("library = %+v", library)
	}

	recorder = serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/authentik", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d", recorder.Code)
	}
	var provider struct {
		Works []json.RawMessage
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &provider); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if provider.Works == nil || len(provider.Works) != 0 {
		t.Fatalf("provider works = %v, want an empty non-nil list", provider.Works)
	}
}

// TestCatalogPlanNotesMissingHandoff proves a declared handoff to an
// uninstalled package is an advisory plan note, never a refusal (S341).
func TestCatalogPlanNotesMissingHandoff(t *testing.T) {
	srv, catalogDir := catalogTestServer(t)
	entry, err := os.ReadFile(filepath.Join(catalogDir, "demo.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	patch := string(entry) + `integrations:
- kind: handoff
  target: helper
  note: Demo calls the helper at runtime; configure it after install.
`
	if err := os.WriteFile(filepath.Join(catalogDir, "demo.yaml"), []byte(patch), 0o644); err != nil {
		t.Fatal(err)
	}
	helper := strings.ReplaceAll(string(entry), "name: demo", "name: helper")
	helper = strings.ReplaceAll(helper, "package: demo", "package: helper")
	helper = strings.ReplaceAll(helper, "title: Demo", "title: Helper")
	helper = strings.ReplaceAll(helper, "ports: [18080]", "ports: [18081]")
	if err := os.WriteFile(filepath.Join(catalogDir, "helper.yaml"), []byte(helper), 0o644); err != nil {
		t.Fatal(err)
	}

	// A manifest edit alone changes the detail surface: no code change.
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/catalog/demo", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("detail code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var detail struct {
		Works []struct {
			Kind, Level, Target, Title string
		}
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(detail.Works) != 1 || detail.Works[0].Kind != "handoff" ||
		detail.Works[0].Level != "L4" || detail.Works[0].Title != "Helper" {
		t.Fatalf("works = %+v", detail.Works)
	}

	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/plan", installTestBody())
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var plan struct {
		Notes []string
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &plan); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, note := range plan.Notes {
		if strings.Contains(note, "helper") && strings.Contains(note, "not installed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("notes = %v, want the missing handoff advisory", plan.Notes)
	}
}
