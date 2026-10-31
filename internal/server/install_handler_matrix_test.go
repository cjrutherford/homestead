package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/audit"
	"homestead/internal/docker"
	"homestead/internal/router"
	"homestead/internal/store"
)

// installHandlerRig uses the production route mount and Authentik test world.
// Its small OIDC manifest keeps the planner independent of host capacity.
func installHandlerRig(t *testing.T) *ssoApp {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cat := filepath.Join(dir, "catalog")
	if err := os.Mkdir(cat, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := `name: immich
version: 1.0.0
title: Immich
blurb: Test install.
description: Test managed installation.
category: media
icon: mdi:image
services:
- name: web
  image: example/immich:1.0.0
  exposes: web
  containerport: 2283
serve: host
requirements:
  memorymb: 64
  storage: []
  ports: []
exposure:
  package: immich
  endpoints:
  - endpoint: web
    recommended: local
    allowwider: true
backuppaths: [data]
sso:
  class: optional
  protocols: [oidc]
  oidc:
    adapter: immich
    service: web
integrations:
- kind: identity
  target: authentik
  note: Sign in with the appliance identity provider.
`
	if err := os.WriteFile(filepath.Join(cat, "immich.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	provider, err := os.ReadFile(filepath.Join("..", "..", "catalog", "authentik.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cat, "authentik.yaml"), provider, 0644); err != nil {
		t.Fatal(err)
	}
	srv := New(DefaultAddr)
	app, err := MountApp(srv, AppConfig{Store: st, ConfigPath: filepath.Join(dir, "desired.yaml"), CatalogDir: cat, SecretKeyPath: filepath.Join(dir, "key"), Port: 8080})
	if err != nil {
		t.Fatal(err)
	}
	srv.MountSessionBoundary(app.Auth, app.Claim, audit.New(st))
	oldInstalled := authentikInstalled
	authentikInstalled = func(*store.Store) bool { return true }
	t.Cleanup(func() { authentikInstalled = oldInstalled })
	oldStack := stackDirFor
	stackDirFor = func(name string) string { return filepath.Join(dir, "stacks", name) }
	t.Cleanup(func() { stackDirFor = oldStack })
	a := &ssoApp{enforcedApp: &enforcedApp{srv: srv, st: st, claims: app.Claim, app: app}, w: newSSOWorld(t)}
	setup := a.setup(t)
	var token struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(setup.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	a.owner.token = token.Token
	a.configureKeeping(t, true)
	a.linkOwner(t)
	// Fail operation creation at the store boundary. If an unexpected request
	// reaches Start, no asynchronous install step can touch the host.
	if _, err := st.DB().Exec(`CREATE TRIGGER stop_install BEFORE INSERT ON operations
		WHEN NEW.type = 'catalog.install' BEGIN SELECT RAISE(ABORT, 'test install stopped'); END`); err != nil {
		t.Fatal(err)
	}
	return a
}

func installMatrixBody() map[string]any {
	return map[string]any{"domain": "lan", "container_ports": map[string]int{"web": 2283}}
}

func installOperationCount(t *testing.T, a *ssoApp) int {
	t.Helper()
	var n int
	if err := a.st.DB().QueryRow(`SELECT COUNT(*) FROM operations WHERE type = 'catalog.install'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func assertNoInstallSideEffects(t *testing.T, a *ssoApp) {
	t.Helper()
	if n := installOperationCount(t, a); n != 0 {
		t.Fatalf("catalog install operations = %d", n)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM app_setup`,
		`SELECT COUNT(*) FROM generated_fingerprints WHERE stack IN ('immich', 'demo', 'authentik')`,
	} {
		var n int
		if err := a.st.DB().QueryRow(query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("install store side effects: %s = %d", query, n)
		}
	}
}

func TestManagedInstallHandlerRejectsUnreviewedAndStaleApprovalsBeforeOperation(t *testing.T) {
	a := installHandlerRig(t)
	base := installMatrixBody()
	request := func(path string, body map[string]any) *httpResponse {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rec := a.req(http.MethodPost, path, string(raw), a.owner.token)
		return &httpResponse{code: rec.Code, body: rec.Body.String(), errorCode: rec.Header().Get(ErrorCodeHeader)}
	}
	for _, tc := range []struct {
		name     string
		body     map[string]any
		code     int
		contains string
	}{
		{"missing access", base, http.StatusBadRequest, "review app access"},
		{"stale install", map[string]any{"domain": "lan", "container_ports": map[string]int{"web": 2283}, "digest": "stale"}, http.StatusConflict, "install plan changed"},
		{"stale sign-in", map[string]any{"domain": "lan", "container_ports": map[string]int{"web": 2283}, "setup": map[string]any{"sign_in": map[string]any{"digest": "stale"}, "access": map[string]any{"digest": "stale", "choices": []any{}}}}, http.StatusConflict, "sign-in plan changed"},
		{"ordinary admin", map[string]any{"domain": "lan", "container_ports": map[string]int{"web": 2283}, "setup": map[string]any{"access": map[string]any{"digest": "stale", "choices": []map[string]any{{"person_id": 1, "role": "admin"}}}}}, http.StatusForbidden, "fresh authentication"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := installOperationCount(t, a)
			got := request("/api/v1/catalog/immich/install", tc.body)
			if got.code != tc.code || !strings.Contains(got.body, tc.contains) {
				t.Fatalf("got %d %q (%q), want %d containing %q", got.code, got.body, got.errorCode, tc.code, tc.contains)
			}
			if after := installOperationCount(t, a); after != before {
				t.Fatalf("install operations changed: %d -> %d", before, after)
			}
		})
	}
	assertNoInstallSideEffects(t, a)
}

func TestManagedInstallHandlerAccessDecisionMatrix(t *testing.T) {
	a := installHandlerRig(t)
	post := func(path string, body map[string]any) *httpResponse {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rec := a.req(http.MethodPost, path, string(raw), a.owner.token)
		return &httpResponse{code: rec.Code, body: rec.Body.String(), errorCode: rec.Header().Get(ErrorCodeHeader)}
	}
	base := installMatrixBody()
	plan := post("/api/v1/catalog/immich/plan", base)
	if plan.code != http.StatusOK {
		t.Fatalf("plan: %d %s", plan.code, plan.body)
	}
	var reviewed struct {
		Digest string `json:"digest"`
		SignIn struct {
			Plan struct {
				Digest string `json:"digest"`
			} `json:"plan"`
		} `json:"sign_in"`
		Access struct {
			Mode string `json:"mode"`
		} `json:"access"`
	}
	if err := json.Unmarshal([]byte(plan.body), &reviewed); err != nil {
		t.Fatal(err)
	}
	if reviewed.Access.Mode != "managed" || reviewed.SignIn.Plan.Digest == "" {
		t.Fatalf("managed preview: %s", plan.body)
	}
	access := post("/api/v1/catalog/immich/access-plan", base)
	if access.code != http.StatusOK {
		t.Fatalf("owner-only access: %d %s", access.code, access.body)
	}
	var accessPlan struct {
		Digest string `json:"digest"`
	}
	if err := json.Unmarshal([]byte(access.body), &accessPlan); err != nil {
		t.Fatal(err)
	}
	approved := installMatrixBody()
	approved["digest"] = reviewed.Digest
	approved["setup"] = map[string]any{"sign_in": map[string]any{"digest": reviewed.SignIn.Plan.Digest}, "access": map[string]any{"digest": accessPlan.Digest, "choices": []any{}}}
	withoutSignIn := installMatrixBody()
	withoutSignIn["digest"] = reviewed.Digest
	withoutSignIn["setup"] = map[string]any{"access": map[string]any{"digest": accessPlan.Digest, "choices": []any{}}}
	if got := post("/api/v1/catalog/immich/install", withoutSignIn); got.code != http.StatusBadRequest || !strings.Contains(got.body, "sign-in") {
		t.Fatalf("missing sign-in approval: %d %s", got.code, got.body)
	}
	missingInstallDigest := installMatrixBody()
	missingInstallDigest["setup"] = approved["setup"]
	if got := post("/api/v1/catalog/immich/install", missingInstallDigest); got.code != http.StatusBadRequest || !strings.Contains(got.body, "install plan") {
		t.Fatalf("missing install approval: %d %s", got.code, got.body)
	}
	if got := post("/api/v1/catalog/immich/install", approved); got.code != http.StatusInternalServerError || !strings.Contains(got.body, "test install stopped") {
		t.Fatalf("complete approvals did not reach operation boundary: %d %s", got.code, got.body)
	}
	if n := installOperationCount(t, a); n != 0 {
		t.Fatalf("operation created after blocked Start: %d", n)
	}
	stale := installMatrixBody()
	stale["digest"] = reviewed.Digest
	stale["setup"] = map[string]any{"sign_in": map[string]any{"digest": reviewed.SignIn.Plan.Digest}, "access": map[string]any{"digest": "stale", "choices": []any{}}}
	if got := post("/api/v1/catalog/immich/install", stale); got.code != http.StatusConflict || !strings.Contains(got.body, "decision changed") {
		t.Fatalf("stale access: %d %s", got.code, got.body)
	}
	publicReview := installMatrixBody()
	publicReview["public_name"] = "photos.example.com"
	publicPlan := post("/api/v1/catalog/immich/plan", publicReview)
	if publicPlan.code != http.StatusOK {
		t.Fatalf("public install plan: %d %s", publicPlan.code, publicPlan.body)
	}
	var pub struct {
		Digest string `json:"digest"`
		SignIn struct {
			Plan struct {
				Digest string `json:"digest"`
			} `json:"plan"`
		} `json:"sign_in"`
	}
	if err := json.Unmarshal([]byte(publicPlan.body), &pub); err != nil {
		t.Fatal(err)
	}
	publicAccess := post("/api/v1/catalog/immich/access-plan", publicReview)
	if publicAccess.code != http.StatusOK {
		t.Fatalf("public access plan: %d %s", publicAccess.code, publicAccess.body)
	}
	var pubAccess struct {
		Digest string `json:"digest"`
	}
	if err := json.Unmarshal([]byte(publicAccess.body), &pubAccess); err != nil {
		t.Fatal(err)
	}
	publicReview["digest"] = pub.Digest
	publicReview["setup"] = map[string]any{"sign_in": map[string]any{"digest": pub.SignIn.Plan.Digest}, "access": map[string]any{"digest": pubAccess.Digest, "choices": []any{}}, "public": map[string]any{"hostname": "other.example.com", "route": "immich.lan", "digest": "stale"}}
	if got := post("/api/v1/catalog/immich/install", publicReview); got.code != http.StatusConflict || !strings.Contains(got.body, "public name and sign-in review") {
		t.Fatalf("mismatched publication: %d %s", got.code, got.body)
	}
	assertNoInstallSideEffects(t, a)
}

func TestManagedInstallHandlerRosterAndAdminBoundary(t *testing.T) {
	a := installHandlerRig(t)
	post := func(path string, body map[string]any, admin bool) *httpResponse {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		var got *httpResponse
		if admin {
			r := a.req(http.MethodPost, path, string(raw), a.owner.token, reauth(ownerPassword))
			got = &httpResponse{r.Code, r.Body.String(), r.Header().Get(ErrorCodeHeader)}
		} else {
			r := a.req(http.MethodPost, path, string(raw), a.owner.token)
			got = &httpResponse{r.Code, r.Body.String(), r.Header().Get(ErrorCodeHeader)}
		}
		return got
	}
	created := a.req(http.MethodPost, "/api/v1/people", `{"confirm":true,"display_name":"Ada","username":"ada"}`, a.owner.token)
	if created.Code != http.StatusOK {
		t.Fatalf("person: %d %s", created.Code, created.Body)
	}
	var createdPerson struct {
		Person struct {
			ID int64 `json:"id"`
		} `json:"person"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdPerson); err != nil {
		t.Fatal(err)
	}
	person := createdPerson.Person
	if person.ID == 0 {
		t.Fatalf("missing person id: %s", created.Body)
	}
	base := installMatrixBody()
	if got := post("/api/v1/catalog/immich/access-plan", base, false); got.code != http.StatusBadRequest || !strings.Contains(got.body, "every person") {
		t.Fatalf("missing roster choice: %d %s", got.code, got.body)
	}
	base["choices"] = []map[string]any{{"person_id": person.ID, "role": "admin"}}
	access := post("/api/v1/catalog/immich/access-plan", base, false)
	if access.code != http.StatusOK {
		t.Fatalf("admin access plan: %d %s", access.code, access.body)
	}
	var p struct {
		Digest string `json:"digest"`
	}
	if err := json.Unmarshal([]byte(access.body), &p); err != nil {
		t.Fatal(err)
	}
	base = installMatrixBody()
	plan := post("/api/v1/catalog/immich/plan", base, false)
	if plan.code != http.StatusOK {
		t.Fatalf("install plan: %d %s", plan.code, plan.body)
	}
	var v struct {
		Digest string `json:"digest"`
		SignIn struct {
			Plan struct {
				Digest string `json:"digest"`
			} `json:"plan"`
		} `json:"sign_in"`
	}
	if err := json.Unmarshal([]byte(plan.body), &v); err != nil {
		t.Fatal(err)
	}
	base["digest"] = v.Digest
	base["setup"] = map[string]any{"sign_in": map[string]any{"digest": v.SignIn.Plan.Digest}, "access": map[string]any{"digest": p.Digest, "choices": []map[string]any{{"person_id": person.ID, "role": "admin"}}}}
	wrongRole := installMatrixBody()
	wrongRole["digest"] = v.Digest
	wrongRole["setup"] = map[string]any{"sign_in": map[string]any{"digest": v.SignIn.Plan.Digest}, "access": map[string]any{"digest": p.Digest, "choices": []map[string]any{{"person_id": person.ID, "role": "user"}}}}
	if got := post("/api/v1/catalog/immich/install", wrongRole, false); got.code != http.StatusConflict || !strings.Contains(got.body, "decision changed") {
		t.Fatalf("changed role: %d %s", got.code, got.body)
	}
	userChoice := []map[string]any{{"person_id": person.ID, "role": "user"}}
	userAccess := installMatrixBody()
	userAccess["choices"] = userChoice
	userPlan := post("/api/v1/catalog/immich/access-plan", userAccess, false)
	if userPlan.code != http.StatusOK {
		t.Fatalf("user access plan: %d %s", userPlan.code, userPlan.body)
	}
	if err := json.Unmarshal([]byte(userPlan.body), &p); err != nil {
		t.Fatal(err)
	}
	userInstall := installMatrixBody()
	userInstall["digest"] = v.Digest
	userInstall["setup"] = map[string]any{"sign_in": map[string]any{"digest": v.SignIn.Plan.Digest}, "access": map[string]any{"digest": p.Digest, "choices": userChoice}}
	if got := post("/api/v1/catalog/immich/install", userInstall, false); got.code != http.StatusInternalServerError || !strings.Contains(got.body, "test install stopped") {
		t.Fatalf("reviewed user decision did not reach operation boundary: %d %s", got.code, got.body)
	}
	if got := post("/api/v1/catalog/immich/install", base, false); got.code != http.StatusForbidden || !strings.Contains(got.body, "fresh authentication") {
		t.Fatalf("ordinary admin: %d %s", got.code, got.body)
	}
	token, csrf := a.ssoSession(t)
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	cookie := cookieAuth(token, csrf)
	if got := a.req(http.MethodPost, "/api/v1/catalog/immich/install-admin", string(raw), "", cookie); got.Code != http.StatusForbidden || got.Header().Get(ErrorCodeHeader) != ErrorCodeReauthRequired {
		t.Fatalf("unstepped admin: %d %s [%s]", got.Code, got.Body, got.Header().Get(ErrorCodeHeader))
	}
	if got := a.req(http.MethodPost, "/api/v1/catalog/immich/install-admin", string(raw), "", cookie, reauth(ownerPassword)); got.Code != http.StatusInternalServerError || !strings.Contains(got.Body.String(), "test install stopped") {
		t.Fatalf("fresh admin did not reach operation boundary: %d %s", got.Code, got.Body)
	}
	a.srv.SetDashboardNames(func() []string { return []string{dashLAN, dashLocal, dashPublic} })
	a.srv.SetDashboardPublicNames(func() []string { return []string{dashPublic} })
	publicInstall := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/immich/install-admin", strings.NewReader(string(raw)))
		req.Host = dashPublic
		req.Header.Set("X-Forwarded-For", "198.51.100.7")
		req.Header.Set("Origin", "https://"+dashPublic)
		cookie(req)
		rec := httptest.NewRecorder()
		a.srv.dashboardFront(a.srv.Handler()).ServeHTTP(rec, req)
		return rec
	}
	if got := publicInstall(); got.Code != http.StatusForbidden || got.Header().Get(ErrorCodeHeader) != ErrorCodeStepUpRequired {
		t.Fatalf("public without step-up: %d %s [%s]", got.Code, got.Body, got.Header().Get(ErrorCodeHeader))
	}
	session, err := a.app.Auth.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.app.Auth.MarkSteppedUp(session.ID, mustIdentity(t, a, session.ID)); err != nil {
		t.Fatal(err)
	}
	if got := publicInstall(); got.Code != http.StatusInternalServerError || !strings.Contains(got.Body.String(), "test install stopped") {
		t.Fatalf("fresh public admin did not reach operation boundary: %d %s", got.Code, got.Body)
	}
	other := a.req(http.MethodPost, "/api/v1/people", `{"confirm":true,"display_name":"Bob","username":"bob"}`, a.owner.token)
	if other.Code != http.StatusOK {
		t.Fatalf("second person: %d %s", other.Code, other.Body)
	}
	if got := a.req(http.MethodPost, "/api/v1/catalog/immich/install-admin", string(raw), "", cookie, reauth(ownerPassword)); got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "every person") {
		t.Fatalf("roster change: %d %s", got.Code, got.Body)
	}
	assertNoInstallSideEffects(t, a)
}

func TestOpenAndBootstrapInstallHandlersRetainOptionalSetup(t *testing.T) {
	a := installHandlerRig(t)
	cat := a.srv.appSignIn.cfg.CatalogDir
	open := `name: demo
version: 1.0.0
title: Demo
blurb: Test open install.
description: Test open installation.
category: testing
icon: mdi:test
services:
- name: web
  image: example/demo:1.0.0
  exposes: web
  containerport: 8080
requirements:
  memorymb: 64
  storage: []
  ports: []
exposure:
  package: demo
  endpoints:
  - endpoint: web
    recommended: local
    allowwider: true
backuppaths: [data]
`
	if err := os.WriteFile(filepath.Join(cat, "demo.yaml"), []byte(open), 0644); err != nil {
		t.Fatal(err)
	}
	accountlessInstallApps["demo"] = true
	t.Cleanup(func() { delete(accountlessInstallApps, "demo") })
	bootstrap := strings.ReplaceAll(strings.ReplaceAll(open, "name: demo", "name: authentik"), "package: demo", "package: authentik")
	if err := os.WriteFile(filepath.Join(cat, "authentik.yaml"), []byte(bootstrap), 0644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"demo", "authentik"} {
		body := map[string]any{"domain": "lan", "container_ports": map[string]int{"web": 8080}}
		raw, _ := json.Marshal(body)
		plan := a.req(http.MethodPost, "/api/v1/catalog/"+name+"/plan", string(raw), a.owner.token)
		if plan.Code != http.StatusOK {
			t.Fatalf("%s plan: %d %s", name, plan.Code, plan.Body)
		}
		var view struct {
			Access struct {
				Mode string `json:"mode"`
			} `json:"access"`
		}
		if err := json.Unmarshal(plan.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		want := "open"
		if name == "authentik" {
			want = "bootstrap"
		}
		if view.Access.Mode != want {
			t.Fatalf("%s mode = %q, want %q", name, view.Access.Mode, want)
		}
		got := a.req(http.MethodPost, "/api/v1/catalog/"+name+"/install", string(raw), a.owner.token)
		if got.Code != http.StatusInternalServerError || !strings.Contains(got.Body.String(), "test install stopped") {
			t.Fatalf("%s optional setup: %d %s", name, got.Code, got.Body)
		}
	}
	assertNoInstallSideEffects(t, a)
}

func TestManagedInstallHandlerChecksOriginalPublicApprovalBeforeOperation(t *testing.T) {
	a := installHandlerRig(t)
	a.srv.appPublic.setupZone = func() (string, string) { return "cloudflare", "example.com" }
	cf := newFakeCloudflare(t)
	fakeConnector(t)
	fakeEdge(t)
	seedTunnelEdge(t, a.srv, a.st, true, true)
	set, err := loadEdgeRoutes(a.st)
	if err != nil {
		t.Fatal(err)
	}
	for i := range set.Routes {
		if set.Routes[i].Host == "authentik.lan" {
			set.Routes[i].Tunnel = []docker.Hostname{"auth.example.com"}
		}
	}
	if err := saveEdgeRoutes(a.st, set); err != nil {
		t.Fatal(err)
	}
	if err := router.RecordRoute(a.st, router.TunnelRoute{Hostname: "auth.example.com", TunnelID: "tid", Service: tunnelEntryService}, "homestead"); err != nil {
		t.Fatal(err)
	}
	if err := router.SetTunnelAccount(a.st, "tid", "abcd0000000000000000000000000001", "zone1", ""); err != nil {
		t.Fatal(err)
	}
	cf.tunnels["tid"] = "homestead"
	if err := a.srv.appPublic.secrets.Set("dns_cloudflare_api_token", "sealed-token"); err != nil {
		t.Fatal(err)
	}
	post := func(path string, body map[string]any) *httpResponse {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rec := a.req(http.MethodPost, path, string(raw), a.owner.token)
		return &httpResponse{rec.Code, rec.Body.String(), rec.Header().Get(ErrorCodeHeader)}
	}
	publicBody := map[string]any{"enabled": true, "before_install": true, "anticipate_sign_in": true, "route": "immich.lan", "hostname": "photos.example.com"}
	pub := post("/api/v1/catalog/immich/public/plan", publicBody)
	if pub.code != http.StatusOK {
		t.Fatalf("anticipated public plan: %d %s", pub.code, pub.body)
	}
	var publicPlan appPublicView
	if err := json.Unmarshal([]byte(pub.body), &publicPlan); err != nil {
		t.Fatal(err)
	}
	if publicPlan.Digest == "" {
		t.Fatal("empty public digest")
	}
	base := installMatrixBody()
	base["public_name"] = "photos.example.com"
	plan := post("/api/v1/catalog/immich/plan", base)
	if plan.code != http.StatusOK {
		t.Fatalf("install plan: %d %s", plan.code, plan.body)
	}
	var reviewed struct {
		Digest string `json:"digest"`
		SignIn struct {
			Plan struct {
				Digest string `json:"digest"`
			} `json:"plan"`
		} `json:"sign_in"`
	}
	if err := json.Unmarshal([]byte(plan.body), &reviewed); err != nil {
		t.Fatal(err)
	}
	access := post("/api/v1/catalog/immich/access-plan", base)
	if access.code != http.StatusOK {
		t.Fatalf("access plan: %d %s", access.code, access.body)
	}
	var accessPlan struct {
		Digest string `json:"digest"`
	}
	if err := json.Unmarshal([]byte(access.body), &accessPlan); err != nil {
		t.Fatal(err)
	}
	base["digest"] = reviewed.Digest
	base["setup"] = map[string]any{"sign_in": map[string]any{"digest": reviewed.SignIn.Plan.Digest}, "access": map[string]any{"digest": accessPlan.Digest, "choices": []any{}}, "public": map[string]any{"hostname": publicPlan.Hostname, "route": publicPlan.Route, "digest": "stale"}}
	withoutPublic := installMatrixBody()
	withoutPublic["public_name"] = base["public_name"]
	withoutPublic["digest"] = reviewed.Digest
	withoutPublic["setup"] = map[string]any{"sign_in": map[string]any{"digest": reviewed.SignIn.Plan.Digest}, "access": map[string]any{"digest": accessPlan.Digest, "choices": []any{}}}
	if got := post("/api/v1/catalog/immich/install", withoutPublic); got.code != http.StatusBadRequest || !strings.Contains(got.body, "public address") {
		t.Fatalf("missing public approval: %d %s", got.code, got.body)
	}
	if got := post("/api/v1/catalog/immich/install", base); got.code != http.StatusConflict || !strings.Contains(got.body, "public address plan changed") {
		t.Fatalf("stale public approval: %d %s", got.code, got.body)
	}
	base["setup"].(map[string]any)["public"].(map[string]any)["digest"] = publicPlan.Digest
	cf.certHosts = append(cf.certHosts, "photos.example.com")
	if got := post("/api/v1/catalog/immich/install", base); got.code != http.StatusConflict || !strings.Contains(got.body, "public address plan changed") {
		t.Fatalf("changed certificate approval: %d %s", got.code, got.body)
	}
	cf.certHosts = cf.certHosts[:len(cf.certHosts)-1]
	cf.denyCerts = true
	if got := post("/api/v1/catalog/immich/install", base); got.code < 400 || got.code == http.StatusInternalServerError && strings.Contains(got.body, "test install stopped") {
		t.Fatalf("unreadable certificate state reached install: %d %s", got.code, got.body)
	}
	cf.denyCerts = false
	if got := post("/api/v1/catalog/immich/install", base); got.code != http.StatusInternalServerError || !strings.Contains(got.body, "test install stopped") {
		t.Fatalf("original public approval did not reach operation boundary: %d %s", got.code, got.body)
	}
	assertNoInstallSideEffects(t, a)
	set, err = loadEdgeRoutes(a.st)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range set.Routes {
		if route.Host == "immich.lan" || len(route.Tunnel) > 0 && route.Host != "authentik.lan" {
			t.Fatalf("public route appeared before sign-in and grants: %+v", route)
		}
	}
}

type httpResponse struct {
	code            int
	body, errorCode string
}
