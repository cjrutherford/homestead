package docker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR-047 §2-3: the dashboard's public name answers on 443 for
// Authentik's path only, and only once a public certificate is placed.
func TestSignInSite(t *testing.T) {
	dir := t.TempDir()
	set := pathSet()
	reconcile := func() string {
		t.Helper()
		if _, err := ReconcileRoutes(dir, set, RouteSet{}); err != nil {
			t.Fatalf("ReconcileRoutes: %v", err)
		}
		got, _ := os.ReadFile(filepath.Join(dir, "routes", "homestead.lan.caddy"))
		return string(got)
	}
	if got := reconcile(); strings.Contains(got, "Sign-in back channel") {
		t.Fatalf("site rendered without a certificate:\n%s", got)
	}
	if _, ok := SignInSiteServed(dir, set); ok {
		t.Fatal("served without a certificate")
	}
	// A local leaf never serves it, even one that (wrongly) covers it.
	place(t, dir, LocalCertName("homestead.example.com"), "homestead.example.com")
	if got := reconcile(); strings.Contains(got, "Sign-in back channel") {
		t.Fatalf("site rendered with a local leaf:\n%s", got)
	}
	place(t, dir, "public", "*.example.com")
	got := reconcile()
	i := strings.Index(got, "# Sign-in back channel")
	if i < 0 {
		t.Fatalf("no sign-in site:\n%s", got)
	}
	site := got[i:]
	for _, want := range []string{
		"\nhomestead.example.com {\n",
		"tls /etc/caddy/routes/certs/public/fullchain.pem /etc/caddy/routes/certs/public/privkey.pem",
		"handle /apps/auth/* {", "reverse_proxy server:9000", "respond 404",
	} {
		if !strings.Contains(site, want) {
			t.Fatalf("site lacks %q:\n%s", want, site)
		}
	}
	if strings.Contains(site, "nextcloud") || strings.Contains(site, "unix/") {
		t.Fatalf("site carries more than Authentik:\n%s", site)
	}
	if name, ok := SignInSiteServed(dir, set); !ok || name != "homestead.example.com" {
		t.Fatalf("served = %q %t", name, ok)
	}
	// Unpublishing Authentik's path drops the site.
	set.Routes[2].Published = false
	if got := reconcile(); strings.Contains(got, "Sign-in back channel") {
		t.Fatalf("site rendered for an unpublished path:\n%s", got)
	}
	if _, ok := SignInSiteServed(dir, set); ok {
		t.Fatal("served for an unpublished path")
	}
}

// ADR-047 §3: a certificate placed for the public name is planned as
// served by the dashboard route and refreshes its snippet.
func TestSignInSiteRefresh(t *testing.T) {
	dir := t.TempDir()
	set := pathSet()
	if _, err := ReconcileRoutes(dir, set, RouteSet{}); err != nil {
		t.Fatal(err)
	}
	if !WouldServe([]string{"homestead.example.com"}, set.Routes[0]) || WouldServe([]string{"other.example.com"}, set.Routes[0]) {
		t.Fatal("WouldServe for the dashboard's public name")
	}
	place(t, dir, "public", "homestead.example.com")
	if _, err := RefreshCertRoutes(dir, set.Routes, []string{"homestead.lan"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "routes", "homestead.lan.caddy"))
	if !strings.Contains(string(got), "certs/public/fullchain.pem") || !RouteServesCert(dir, "homestead.lan", "public") {
		t.Fatalf("not refreshed:\n%s", got)
	}
}
