package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
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

func TestAppSignInPlanCarriesGroupsAndAccessReadBack(t *testing.T) {
	a := appSignInRig(t)
	a.linkOwner(t)
	rec := a.req("POST", "/api/v1/catalog/immich/sign-in/plan", `{"enabled":true}`, a.owner.token)
	if rec.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body)
	}
	plan := decodeAs[appsso.Plan](t, rec.Body.Bytes())
	if plan.Access == nil || plan.Access.OwnerSub != "owner-sub" ||
		strings.Join(plan.Access.Groups, ",") != "homestead-immich-users,homestead-immich-admins,homestead-owner" {
		t.Fatalf("access: %+v", plan.Access)
	}
	// An old sign-in (no access record) is open to every Authentik user.
	if err := (appsso.Records{DB: a.st.DB()}).Save(appsso.Record{Package: "immich", Adapter: catalog.AdapterImmich, ClientID: "homestead-immich", Issuer: plan.Issuer, RedirectURIs: plan.RedirectURIs}); err != nil {
		t.Fatal(err)
	}
	st := decodeAs[appsso.Status](t, a.req("GET", "/api/v1/catalog/immich/sign-in", "", a.owner.token).Body.Bytes())
	if !st.Enabled || !st.OpenToAll {
		t.Fatalf("status: %+v", st)
	}
	view := decodeAs[ssoView](t, a.req("GET", "/api/v1/sso", "", a.owner.token).Body.Bytes())
	if len(view.OpenApps) != 1 || view.OpenApps[0] != "immich" {
		t.Fatalf("open apps: %v", view.OpenApps)
	}
	// Read-back needs a token when none is kept; with one it reports, unchanged.
	if rec := a.req("POST", "/api/v1/catalog/immich/sign-in/access", `{}`, a.owner.token); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no token: %d %s", rec.Code, rec.Body)
	}
	// The fake shares one application pk; drop the dashboard binding that
	// linking now creates (S338) so this app reads as open.
	a.w.bindings = nil
	groups, bindings := len(a.w.groups), len(a.w.bindings)
	rec = a.req("POST", "/api/v1/catalog/immich/sign-in/access", `{"token":"`+ssoToken+`"}`, a.owner.token)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"open":true`) {
		t.Fatalf("access: %d %s", rec.Code, rec.Body)
	}
	if len(a.w.groups) != groups || len(a.w.bindings) != bindings {
		t.Fatal("a read-back changed Authentik")
	}
}

func TestSetupAnticipatedSignInMatchesPreviewAndRejectsChanges(t *testing.T) {
	a := appSignInRig(t)
	entries, err := catalog.Load(filepath.Join("..", "..", "catalog"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := entries.Get("immich")
	if err != nil {
		t.Fatal(err)
	}
	set, err := loadEdgeRoutes(a.st)
	if err != nil {
		t.Fatal(err)
	}
	route := set.Routes[0]
	install := catalog.InstallPlan{Package: "immich", Routes: []docker.Route{route}}
	preview := a.srv.appSignIn.preview(manifest, install, "immich.example.com")
	if preview == nil || !preview.Offered {
		t.Fatalf("preview: %+v", preview)
	}
	rec := setupRecord{App: "immich", Routes: []docker.Route{route}, Public: &setupPublicApproval{Route: string(route.Host), Hostname: "immich.example.com"}}
	in, err := a.srv.appSignIn.setupInputs(rec)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := appsso.BuildPlan(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Digest != preview.Plan.Digest {
		t.Fatalf("digest %s != preview %s", plan.Digest, preview.Plan.Digest)
	}
	after, err := loadEdgeRoutes(a.st)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, set) {
		t.Fatalf("route published: %+v", after)
	}
	changed := route
	changed.Target.Port++
	if err := saveEdgeRoutes(a.st, docker.RouteSet{Routes: []docker.Route{changed}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.srv.appSignIn.startSetup(context.Background(), rec, preview.Plan.Digest, "token", "owner"); !errors.Is(err, errPlanChanged) {
		t.Fatalf("stale route: %v", err)
	}
	if err := saveEdgeRoutes(a.st, set); err != nil {
		t.Fatal(err)
	}
	spec, ok, err := a.srv.appSignIn.m.Config()
	if err != nil || !ok {
		t.Fatalf("SSO config: %v", err)
	}
	spec.AuthentikURL = "https://changed.example.com"
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.DB().Exec(`UPDATE sso_config SET spec = ? WHERE id = 1`, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.srv.appSignIn.startSetup(context.Background(), rec, preview.Plan.Digest, "token", "owner"); !errors.Is(err, errPlanChanged) {
		t.Fatalf("stale issuer: %v", err)
	}
	spec.AuthentikURL = a.w.srv.URL
	raw, _ = json.Marshal(spec)
	if _, err := a.st.DB().Exec(`UPDATE sso_config SET spec = ? WHERE id = 1`, string(raw)); err != nil {
		t.Fatal(err)
	}
	rec.Public.Hostname = "other.example.com"
	if _, err := a.srv.appSignIn.startSetup(context.Background(), rec, preview.Plan.Digest, "token", "owner"); !errors.Is(err, errPlanChanged) {
		t.Fatalf("stale public name: %v", err)
	}
	rec.Public.Hostname = "immich.example.com"
	id, err := a.srv.appSignIn.startSetup(context.Background(), rec, preview.Plan.Digest, "token", "owner")
	if err != nil {
		t.Fatalf("start approved sign-in: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.srv.appSignIn.runner.Wait(ctx, id); err != nil {
		t.Fatalf("wait for sign-in: %v", err)
	}
	after, err = loadEdgeRoutes(a.st)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, set) {
		t.Fatalf("operation published route: %+v", after)
	}
}
