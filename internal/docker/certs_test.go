package docker

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func autoRoute(host string) Route {
	return Route{Host: Hostname(host), Target: Target{Host: "app", Port: 80},
		Exposure: ExposureLocal, TLS: TLSAuto}
}

// testPair mints a self-signed leaf covering names plus its key, a valid
// chain/key pair for placement.
func testPair(t *testing.T, names ...string) (chain, key []byte) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalECPrivateKey(k)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd})
}

func place(t *testing.T, dir, name string, names ...string) {
	t.Helper()
	chain, key := testPair(t, names...)
	if err := PlaceCert(dir, PlacedCert{Name: name, Names: names}, chain, key); err != nil {
		t.Fatalf("PlaceCert(%s): %v", name, err)
	}
}

func TestPlaceCertModesAndLookup(t *testing.T) {
	dir := t.TempDir()
	place(t, dir, "apps", "*.example.com", "example.com")
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
	// Replacing keeps 0600 and leaves no staging or old directories.
	chain, key := testPair(t, "*.example.com")
	if err := PlaceCert(dir, PlacedCert{Name: "apps", Names: []string{"*.example.com"}}, chain, key); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if info, _ := os.Stat(keyPath); info.Mode().Perm() != 0o600 {
		t.Fatalf("replaced key mode = %v", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(CertDir(dir, "apps")))
	if len(entries) != 1 {
		t.Fatalf("leftover entries: %v", entries)
	}
	for host, want := range map[Hostname]bool{
		"app.example.com":       true,
		"example.com":           false, // wildcard never implies the apex
		"a.b.example.com":       false, // one label only
		"app.other.example.org": false,
	} {
		if _, ok := ServingCert(dir, host); ok != want {
			t.Errorf("ServingCert(%q) = %v, want %v", host, ok, want)
		}
	}
	good, goodKey := testPair(t, "a.b")
	for _, bad := range []PlacedCert{{Name: "../x", Names: []string{"a.b"}}, {Name: "ok"}} {
		if err := PlaceCert(dir, bad, good, goodKey); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestPlaceCertValidatesBeforeTouchingDisk(t *testing.T) {
	dir := t.TempDir()
	place(t, dir, "apps", "app.example.com")
	_, keyPath := CertPaths(dir, "apps")
	before, _ := os.ReadFile(keyPath)
	chain, _ := testPair(t, "app.example.com")
	_, otherKey := testPair(t, "app.example.com")
	leafOnly, leafKey := testPair(t, "other.example.com")
	for name, args := range map[string]struct {
		names      []string
		chain, key []byte
	}{
		"key of a different certificate": {[]string{"app.example.com"}, chain, otherKey},
		"leaf misses an approved name":   {[]string{"app.example.com"}, leafOnly, leafKey},
		"garbage":                        {[]string{"app.example.com"}, []byte("x"), []byte("y")},
	} {
		if err := PlaceCert(dir, PlacedCert{Name: "apps", Names: args.names}, args.chain, args.key); err == nil {
			t.Errorf("%s: placed", name)
		}
	}
	// A refused placement leaves the previous pair and no staging files.
	if after, _ := os.ReadFile(keyPath); string(after) != string(before) {
		t.Fatal("previous key changed by a refused placement")
	}
	if entries, _ := os.ReadDir(filepath.Dir(CertDir(dir, "apps"))); len(entries) != 1 {
		t.Fatalf("staging left behind: %v", entries)
	}
	// An expired certificate is refused.
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := x509.Certificate{SerialNumber: big.NewInt(9), DNSNames: []string{"old.example.com"},
		NotBefore: time.Now().Add(-48 * time.Hour), NotAfter: time.Now().Add(-time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &k.PublicKey, k)
	kd, _ := x509.MarshalECPrivateKey(k)
	err := PlaceCert(dir, PlacedCert{Name: "old", Names: []string{"old.example.com"}},
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}))
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired placement = %v", err)
	}
}

func TestPlacementRefusesSymlinks(t *testing.T) {
	dir := t.TempDir()
	elsewhere := t.TempDir()
	routes := filepath.Join(dir, "routes")
	_ = os.MkdirAll(routes, 0o755)
	// The certs directory itself is a symlink.
	if err := os.Symlink(elsewhere, filepath.Join(routes, "certs")); err != nil {
		t.Fatal(err)
	}
	chain, key := testPair(t, "app.example.com")
	if err := PlaceCert(dir, PlacedCert{Name: "apps", Names: []string{"app.example.com"}}, chain, key); err == nil {
		t.Fatal("placed through a symlinked certs directory")
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("wrote through the symlink: %v", entries)
	}
	_ = os.Remove(filepath.Join(routes, "certs"))
	// The certificate directory is a symlink.
	_ = os.MkdirAll(filepath.Join(routes, "certs"), 0o700)
	if err := os.Symlink(elsewhere, CertDir(dir, "apps")); err != nil {
		t.Fatal(err)
	}
	if err := PlaceCert(dir, PlacedCert{Name: "apps", Names: []string{"app.example.com"}}, chain, key); err == nil {
		t.Fatal("placed over a symlinked certificate directory")
	}
	if _, ok := ServingCert(dir, "app.example.com"); ok {
		t.Fatal("served from a symlinked directory")
	}
	_ = os.Remove(CertDir(dir, "apps"))
	// A key file replaced by a symlink stops serving.
	place(t, dir, "apps", "app.example.com")
	_, keyPath := CertPaths(dir, "apps")
	real, _ := os.ReadFile(keyPath)
	target := filepath.Join(elsewhere, "k.pem")
	_ = os.WriteFile(target, real, 0o600)
	_ = os.Remove(keyPath)
	_ = os.Symlink(target, keyPath)
	if _, ok := ServingCert(dir, "app.example.com"); ok {
		t.Fatal("served with a symlinked key")
	}
}

func TestServingCertPrefersExactAndIgnoresInvalidPairs(t *testing.T) {
	dir := t.TempDir()
	place(t, dir, "a-wild", "*.example.com")
	place(t, dir, "z-exact", "app.example.com")
	got, ok := ServingCert(dir, "app.example.com")
	if !ok || got.Name != "z-exact" {
		t.Fatalf("got %+v ok %v, want the exact match", got, ok)
	}
	// A placement whose key no longer matches its chain is ignored, so
	// it can never break route applies; the wildcard still serves.
	_, otherKey := testPair(t, "app.example.com")
	_ = os.WriteFile(filepath.Join(CertDir(dir, "z-exact"), certKeyFile), otherKey, 0o600)
	got, _ = ServingCert(dir, "app.example.com")
	if got.Name != "a-wild" {
		t.Fatalf("got %+v, want wildcard after the exact pair broke", got)
	}
	// Missing key, garbage metadata: skipped too.
	_ = os.Remove(filepath.Join(CertDir(dir, "a-wild"), certKeyFile))
	if _, ok := ServingCert(dir, "app.example.com"); ok {
		t.Fatal("served from broken placements")
	}
	if _, ok := ServingCert(t.TempDir(), "app.example.com"); ok {
		t.Fatal("served with nothing placed")
	}
	// Routes still render and reconcile with a broken placement present.
	set := RouteSet{Routes: []Route{autoRoute("app.example.com")}}
	if _, err := ReconcileRoutes(dir, set, RouteSet{}); err != nil {
		t.Fatalf("reconcile with broken placement: %v", err)
	}
}

func TestRenderRouteInUsesPlacedCertOnlyForAuto(t *testing.T) {
	dir := t.TempDir()
	place(t, dir, "apps", "app.example.com")
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
	plain, _ := RenderRoute(autoRoute("other.example.com"))
	got, _ = RenderRouteIn(dir, autoRoute("other.example.com"))
	if got != plain {
		t.Fatalf("uncovered host changed:\n%s", got)
	}
}

func TestRefreshCertRoutesApprovedHostsAndModes(t *testing.T) {
	dir := t.TempDir()
	routes := filepath.Join(dir, "routes")
	_ = os.MkdirAll(routes, 0o755)
	applied, other, missing := autoRoute("app.example.com"), autoRoute("two.example.com"), autoRoute("new.example.com")
	content, _ := RenderRoute(applied)
	snippet := filepath.Join(routes, RouteFilename(applied.Host))
	_ = os.WriteFile(snippet, []byte(content), 0o640)
	_ = os.Chmod(snippet, 0o640)
	otherContent, _ := RenderRoute(other)
	otherPath := filepath.Join(routes, RouteFilename(other.Host))
	_ = os.WriteFile(otherPath, []byte(otherContent), 0o644)
	place(t, dir, "apps", "*.example.com")
	// Only approved hosts with an existing snippet are rewritten; the
	// live route set may contain more.
	undo, err := RefreshCertRoutes(dir, []Route{applied, other, missing, applied},
		[]string{"app.example.com", "new.example.com"})
	if err != nil {
		t.Fatalf("RefreshCertRoutes: %v", err)
	}
	got, _ := os.ReadFile(snippet)
	if !strings.Contains(string(got), "privkey.pem") {
		t.Fatalf("approved snippet not refreshed:\n%s", got)
	}
	if info, _ := os.Stat(snippet); info.Mode().Perm() != 0o640 {
		t.Fatalf("mode after refresh = %v", info.Mode().Perm())
	}
	if got, _ := os.ReadFile(otherPath); string(got) != otherContent {
		t.Fatal("rewrote a host that was not approved")
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
	if info, _ := os.Stat(snippet); info.Mode().Perm() != 0o640 {
		t.Fatalf("mode after undo = %v", info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(routes); len(entries) != 3 { // two snippets + certs
		t.Fatalf("temp files left in routes: %v", entries)
	}
}

func TestReconcileRoutesKeepsPlacedCerts(t *testing.T) {
	dir := t.TempDir()
	place(t, dir, "apps", "app.example.com")
	set := RouteSet{Routes: []Route{autoRoute("app.example.com")}}
	if _, err := ReconcileRoutes(dir, set, RouteSet{}); err != nil {
		t.Fatalf("ReconcileRoutes: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "routes", "app.example.com.caddy"))
	if !strings.Contains(string(got), "privkey.pem") {
		t.Fatalf("reconcile did not serve the placed cert:\n%s", got)
	}
	if _, err := ReconcileRoutes(dir, RouteSet{}, set); err != nil {
		t.Fatalf("reconcile empty: %v", err)
	}
	if len(PlacedCerts(dir)) != 1 {
		t.Fatal("reconcile removed a placed certificate")
	}
}

func TestCleanCertTemps(t *testing.T) {
	dir := t.TempDir()
	place(t, dir, "apps", "app.example.com")
	parent := filepath.Dir(CertDir(dir, "apps"))
	_ = os.MkdirAll(filepath.Join(parent, ".tmp-apps~123"), 0o700)
	_ = os.WriteFile(filepath.Join(parent, ".tmp-apps~123", "privkey.pem"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(CertDir(dir, "apps"), ".tmp-9"), []byte("x"), 0o600)
	// An interrupted swap: old placement set aside, new one never landed.
	place(t, dir, "lost", "lost.example.com")
	if err := os.Rename(CertDir(dir, "lost"), filepath.Join(parent, ".old-lost~7")); err != nil {
		t.Fatal(err)
	}
	// A stale old directory whose replacement did land is just removed.
	_ = os.MkdirAll(filepath.Join(parent, ".old-apps~5"), 0o700)
	if err := CleanCertTemps(dir); err != nil {
		t.Fatalf("CleanCertTemps: %v", err)
	}
	entries, _ := os.ReadDir(parent)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "apps,lost" {
		t.Fatalf("entries = %v", names)
	}
	if _, err := os.Stat(filepath.Join(CertDir(dir, "apps"), ".tmp-9")); !os.IsNotExist(err) {
		t.Fatal("stale temp file survived")
	}
	if len(PlacedCerts(dir)) != 2 {
		t.Fatalf("placements after cleanup = %v", PlacedCerts(dir))
	}
	if err := CleanCertTemps(t.TempDir()); err != nil {
		t.Fatalf("clean with nothing placed: %v", err)
	}
}
