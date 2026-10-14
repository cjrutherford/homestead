package docker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func autoRoute(host string) Route {
	return Route{Host: Hostname(host), Target: Target{Host: "app", Port: 80},
		Exposure: ExposureLocal, TLS: TLSAuto}
}

func TestPlaceCertModesAndLookup(t *testing.T) {
	dir := t.TempDir()
	chain, key := []byte("CHAIN"), []byte("KEY")
	if err := PlaceCert(dir, PlacedCert{Name: "apps", Names: []string{"*.example.com", "example.com"}}, chain, key); err != nil {
		t.Fatalf("PlaceCert: %v", err)
	}
	chainPath, keyPath := CertPaths(dir, "apps")
	info, err := os.Stat(keyPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key = %v err %v, want 0600", info, err)
	}
	if info, err := os.Stat(chainPath); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("chain = %v err %v", info, err)
	}
	if info, _ := os.Stat(filepath.Dir(CertDir(dir, "apps"))); info.Mode().Perm() != 0o700 {
		t.Fatalf("certs dir mode = %v", info.Mode().Perm())
	}
	// Replacing keeps 0600 and leaves no temp files behind.
	if err := PlaceCert(dir, PlacedCert{Name: "apps", Names: []string{"*.example.com"}}, chain, []byte("KEY2")); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if info, _ := os.Stat(keyPath); info.Mode().Perm() != 0o600 {
		t.Fatalf("replaced key mode = %v", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(CertDir(dir, "apps"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp") {
			t.Fatalf("temp file left: %s", e.Name())
		}
	}
	for host, want := range map[Hostname]bool{
		"app.example.com":       true,
		"example.com":           false, // wildcard never implies the apex
		"a.b.example.com":       false, // one label only
		"app.other.example.org": false,
	} {
		_, ok := ServingCert(dir, host)
		if ok != want {
			t.Errorf("ServingCert(%q) = %v, want %v", host, ok, want)
		}
	}
	for _, bad := range []PlacedCert{{Name: "../x", Names: []string{"a.b"}}, {Name: "ok"}} {
		if err := PlaceCert(dir, bad, chain, key); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestServingCertPrefersExactAndSkipsBroken(t *testing.T) {
	dir := t.TempDir()
	_ = PlaceCert(dir, PlacedCert{Name: "a-wild", Names: []string{"*.example.com"}}, []byte("c"), []byte("k"))
	_ = PlaceCert(dir, PlacedCert{Name: "z-exact", Names: []string{"app.example.com"}}, []byte("c"), []byte("k"))
	got, ok := ServingCert(dir, "app.example.com")
	if !ok || got.Name != "z-exact" {
		t.Fatalf("got %+v ok %v, want the exact match", got, ok)
	}
	// A placement missing its key never serves.
	_ = os.Remove(filepath.Join(CertDir(dir, "z-exact"), certKeyFile))
	got, _ = ServingCert(dir, "app.example.com")
	if got.Name != "a-wild" {
		t.Fatalf("got %+v, want wildcard after the exact lost its key", got)
	}
	if _, ok := ServingCert(t.TempDir(), "app.example.com"); ok {
		t.Fatal("served with nothing placed")
	}
}

func TestRenderRouteInUsesPlacedCertOnlyForAuto(t *testing.T) {
	dir := t.TempDir()
	_ = PlaceCert(dir, PlacedCert{Name: "apps", Names: []string{"app.example.com"}}, []byte("c"), []byte("k"))
	auto, err := RenderRouteIn(dir, autoRoute("app.example.com"))
	if err != nil || !strings.Contains(auto, "\ttls /etc/caddy/routes/certs/apps/fullchain.pem /etc/caddy/routes/certs/apps/privkey.pem\n") {
		t.Fatalf("auto = %q err %v", auto, err)
	}
	internal := autoRoute("app.example.com")
	internal.TLS = TLSInternal
	got, _ := RenderRouteIn(dir, internal)
	if strings.Contains(got, "privkey.pem") || !strings.Contains(got, "tls internal") {
		t.Fatalf("internal route changed: %q", got)
	}
	off := autoRoute("app.example.com")
	off.TLS = TLSOff
	if got, _ := RenderRouteIn(dir, off); strings.Contains(got, "privkey.pem") {
		t.Fatalf("off route changed: %q", got)
	}
	// Uncovered hosts and the plain renderer are unchanged.
	plain, _ := RenderRoute(autoRoute("other.example.com"))
	got, _ = RenderRouteIn(dir, autoRoute("other.example.com"))
	if got != plain {
		t.Fatalf("uncovered host changed:\n%s", got)
	}
	if strings.Contains(auto, "tls internal") {
		t.Fatal("placed cert route also asks for the internal CA")
	}
}

func TestRefreshCertRoutesOnlyTouchesExistingSnippets(t *testing.T) {
	dir := t.TempDir()
	routes := filepath.Join(dir, "routes")
	_ = os.MkdirAll(routes, 0o755)
	applied, missing := autoRoute("app.example.com"), autoRoute("new.example.com")
	content, _ := RenderRoute(applied)
	snippet := filepath.Join(routes, RouteFilename(applied.Host))
	_ = os.WriteFile(snippet, []byte(content), 0o644)
	_ = PlaceCert(dir, PlacedCert{Name: "apps", Names: []string{"*.example.com"}}, []byte("c"), []byte("k"))
	undo, err := RefreshCertRoutes(dir, []Route{applied, missing, applied})
	if err != nil {
		t.Fatalf("RefreshCertRoutes: %v", err)
	}
	got, _ := os.ReadFile(snippet)
	if !strings.Contains(string(got), "privkey.pem") {
		t.Fatalf("applied snippet not refreshed:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(routes, RouteFilename(missing.Host))); !os.IsNotExist(err) {
		t.Fatal("created a snippet for a route nobody applied")
	}
	if err := undo(); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if got, _ := os.ReadFile(snippet); string(got) != content {
		t.Fatalf("undo did not restore:\n%s", got)
	}
}

func TestReconcileRoutesKeepsPlacedCerts(t *testing.T) {
	dir := t.TempDir()
	_ = PlaceCert(dir, PlacedCert{Name: "apps", Names: []string{"app.example.com"}}, []byte("c"), []byte("k"))
	set := RouteSet{Routes: []Route{autoRoute("app.example.com")}}
	if _, err := ReconcileRoutes(dir, set, RouteSet{}); err != nil {
		t.Fatalf("ReconcileRoutes: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "routes", "app.example.com.caddy"))
	if !strings.Contains(string(got), "privkey.pem") {
		t.Fatalf("reconcile did not serve the placed cert:\n%s", got)
	}
	// Dropping every route removes snippets, never the placed material.
	if _, err := ReconcileRoutes(dir, RouteSet{}, set); err != nil {
		t.Fatalf("reconcile empty: %v", err)
	}
	if len(PlacedCerts(dir)) != 1 {
		t.Fatal("reconcile removed a placed certificate")
	}
}
