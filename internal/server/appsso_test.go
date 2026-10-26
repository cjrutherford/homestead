package server

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/appsso"
	"homestead/internal/catalog"
	"homestead/internal/docker"
)

// appSignInRig is the SSO rig with dashboard sign-in configured, the real
// catalog, and Immich installed with a route on the edge.
func appSignInRig(t *testing.T) *ssoApp {
	t.Helper()
	a := newSSOApp(t)
	a.configure(t)
	a.srv.appSignIn.cfg.CatalogDir = filepath.Join("..", "..", "catalog")
	dir := t.TempDir()
	old := stackDirFor
	stackDirFor = func(name string) string { return filepath.Join(dir, name) }
	t.Cleanup(func() { stackDirFor = old })
	if _, err := a.st.DB().Exec(`INSERT INTO generated_fingerprints (stack, path, sha256, updated_at) VALUES ('immich', 'compose.yaml', 'x', ?)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := saveEdgeRoutes(a.st, docker.RouteSet{Routes: []docker.Route{
		{Host: "immich.lan", Target: docker.Target{Host: "server", Port: 2283}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal},
	}}); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAppSignInStatusPlanAndApplyRefusals(t *testing.T) {
	a := appSignInRig(t)
	rec := a.req("GET", "/api/v1/catalog/immich/sign-in", "", a.owner.token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}
	status := decodeAs[appsso.Status](t, rec.Body.Bytes())
	if !status.Offered || status.Enabled || status.Adapter != catalog.AdapterImmich {
		t.Fatalf("status: %+v", status)
	}
	rec = a.req("POST", "/api/v1/catalog/immich/sign-in/plan", `{"enabled":true}`, a.owner.token)
	if rec.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body)
	}
	plan := decodeAs[appsso.Plan](t, rec.Body.Bytes())
	if plan.ClientID != "homestead-immich" || !strings.HasPrefix(plan.Issuer, a.w.srv.URL+"/application/o/homestead-immich/") ||
		len(plan.RedirectURIs) != 5 || plan.Digest == "" {
		t.Fatalf("plan: %+v", plan)
	}
	if _, ok, _ := (appsso.Records{DB: a.st.DB()}).Get("immich"); ok {
		t.Fatal("planning stored a record")
	}
	stale := a.req("POST", "/api/v1/catalog/immich/sign-in/apply", `{"enabled":true,"digest":"stale","token":"t"}`, a.owner.token)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale: %d %s", stale.Code, stale.Body)
	}
	// The dashboard sign-in did not keep its token, so apply needs one.
	noToken := a.req("POST", "/api/v1/catalog/immich/sign-in/apply", fmt.Sprintf(`{"enabled":true,"digest":%q}`, plan.Digest), a.owner.token)
	if noToken.Code != http.StatusUnprocessableEntity || !strings.Contains(noToken.Body.String(), "API token") {
		t.Fatalf("no token: %d %s", noToken.Code, noToken.Body)
	}
	for path, want := range map[string]int{
		"/api/v1/catalog/ghost/sign-in":     http.StatusNotFound,
		"/api/v1/catalog/wordcraft/sign-in": http.StatusOK,
	} {
		if rec := a.req("GET", path, "", a.owner.token); rec.Code != want {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	if rec := a.req("POST", "/api/v1/catalog/wordcraft/sign-in/plan", `{"enabled":true}`, a.owner.token); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no adapter: %d %s", rec.Code, rec.Body)
	}
	if rec := a.req("POST", "/api/v1/catalog/immich/sign-in/plan", `{}`, a.owner.token); rec.Code != http.StatusBadRequest {
		t.Fatalf("no enabled: %d", rec.Code)
	}
}

func TestInstallPlanPreviewsSignIn(t *testing.T) {
	a := appSignInRig(t)
	entries, err := catalog.Load(filepath.Join("..", "..", "catalog"))
	if err != nil {
		t.Fatal(err)
	}
	jellyfin, _ := entries.Get("jellyfin")
	install := catalog.InstallPlan{Package: "jellyfin", Routes: []docker.Route{
		{Host: "jellyfin.lan", Target: docker.Target{Host: "jellyfin", Port: 8096}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal},
	}}
	p := a.srv.appSignIn.preview(jellyfin, install, "")
	if p == nil || !p.Offered || p.Plan == nil || p.Plan.Plugin == nil ||
		strings.Join(p.Plan.RedirectURIs, ",") != "https://jellyfin.lan/sso/OID/redirect/authentik,https://jellyfin.local/sso/OID/redirect/authentik" {
		t.Fatalf("preview: %+v", p)
	}
	// S313: a public name reviewed with the install is one of the sign-in
	// plan's origins, as it will be once the wizard has published it.
	withPublic := a.srv.appSignIn.preview(jellyfin, install, "jellyfin.homestead.example.com")
	if withPublic == nil || !withPublic.Offered ||
		!strings.Contains(strings.Join(withPublic.Plan.RedirectURIs, ","), "https://jellyfin.homestead.example.com/sso/OID/redirect/authentik") ||
		withPublic.Plan.Digest == p.Plan.Digest {
		t.Fatalf("preview with public: %+v", withPublic)
	}
	wordcraft, _ := entries.Get("wordcraft")
	if p := a.srv.appSignIn.preview(wordcraft, catalog.InstallPlan{}, ""); p != nil {
		t.Fatalf("no adapter preview: %+v", p)
	}
	// No route planned: not offered, with the reason.
	if p := a.srv.appSignIn.preview(jellyfin, catalog.InstallPlan{}, ""); p == nil || p.Offered || !strings.Contains(p.Reason, "no address") {
		t.Fatalf("no route: %+v", p)
	}
}
