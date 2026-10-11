package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"homestead/internal/audit"
	"homestead/internal/claim"
	"homestead/internal/store"
)

// enforcedApp is the production route set behind the real, enforcing
// boundary with real claim and auth services (S257).
type enforcedApp struct {
	srv    *Server
	st     *store.Store
	claims *claim.Service
	app    *AppServices
}

func newEnforcedApp(t *testing.T) *enforcedApp {
	t.Helper()
	srv, st, app := fullServerApp(t)
	srv.MountSessionBoundary(app.Auth, app.Claim, audit.New(st))
	return &enforcedApp{srv: srv, st: st, claims: app.Claim, app: app}
}

func (a *enforcedApp) claim(t *testing.T) {
	t.Helper()
	token, err := a.claims.MintToken(claim.TokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.claims.Claim(token); err != nil {
		t.Fatal(err)
	}
}

func (a *enforcedApp) do(method, path, body string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	a.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func bearer(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

// setupAndLogin claims, creates the owner through the setup route, and
// returns the setup response (which logs the owner in).
func (a *enforcedApp) setup(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	a.claim(t)
	rec := a.do(http.MethodPost, "/api/v1/admins/setup",
		`{"username":"owner","password":"long-enough-password"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup = %d: %s", rec.Code, rec.Body.String())
	}
	return rec
}

// routeRequest turns a registered pattern into a concrete method and path.
func routeRequest(pattern string) (method, path string) {
	method = http.MethodGet
	path = pattern
	if m, p, ok := strings.Cut(pattern, " "); ok {
		method, path = m, p
	}
	for _, seg := range []string{"{id}", "{name}", "{package}"} {
		path = strings.ReplaceAll(path, seg, "1")
	}
	return method, path
}

func rejected(code int) bool { return code == 401 || code == 403 || code == 503 }

func TestEverySessionRouteReturns401WithoutCredentials(t *testing.T) {
	a := newEnforcedApp(t)
	a.setup(t)
	n := 0
	for _, pattern := range a.srv.Routes() {
		class, _ := Classify(pattern)
		if class != ClassSession {
			continue
		}
		n++
		method, path := routeRequest(pattern)
		if rec := a.do(method, path, "{}", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without credentials = %d, want 401", pattern, rec.Code)
		}
		if rec := a.do(method, path, "{}", bearer("not-a-session")); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s with a bogus bearer = %d, want 401", pattern, rec.Code)
		}
	}
	if n < 45 {
		t.Fatalf("only %d session routes exercised", n)
	}
	// Unknown API paths and wrong methods fall to the catch-all, also gated.
	for _, tc := range [][2]string{{"GET", "/api/v1/nope"}, {"DELETE", "/api/v1/config"}, {"POST", "/index.html"}} {
		if rec := a.do(tc[0], tc[1], "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", tc[0], tc[1], rec.Code)
		}
	}
}

func TestSessionRoutesReachHandlersWithSession(t *testing.T) {
	a := newEnforcedApp(t)
	token := a.setupToken(t)
	for _, path := range []string{
		"/api/v1/config", "/api/v1/config/schema", "/api/v1/audit", "/api/v1/operations",
		"/api/v1/catalog", "/api/v1/backups/schedules", "/api/v1/storage/mounts",
		"/api/v1/exposure/approvals", "/api/v1/admins/lockout",
	} {
		if rec := a.do(http.MethodGet, path, "", bearer(token)); rejected(rec.Code) {
			t.Errorf("GET %s with a session = %d (%s)", path, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}

func (a *enforcedApp) setupToken(t *testing.T) string {
	t.Helper()
	rec := a.setup(t)
	token := jsonField(t, rec, "token")
	if token == "" {
		t.Fatal("setup returned no token")
	}
	return token
}

func TestPreClaimRejectsEverythingButAllowlistAndWizards(t *testing.T) {
	a := newEnforcedApp(t)
	for _, pattern := range a.srv.Routes() {
		class, _ := Classify(pattern)
		method, path := routeRequest(pattern)
		rec := a.do(method, path, "{}", nil)
		switch class {
		case ClassSession:
			if rec.Code != http.StatusForbidden {
				t.Errorf("pre-claim %s = %d, want 403", pattern, rec.Code)
			}
			// A session cannot exist pre-claim, but a presented one does
			// not widen the surface either.
			if rec := a.do(method, path, "{}", bearer("x")); rec.Code != http.StatusForbidden {
				t.Errorf("pre-claim %s with bearer = %d, want 403", pattern, rec.Code)
			}
		default:
			if rejected(rec.Code) && pattern != "POST /api/v1/admins/setup" && pattern != "POST /api/v1/sessions" {
				t.Errorf("pre-claim %s = %d, should be reachable", pattern, rec.Code)
			}
		}
	}
	// handleConfigApply specifically cannot apply configuration unclaimed.
	if rec := a.do(http.MethodPut, "/api/v1/config", `{"settings":{}}`, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("unclaimed config apply = %d, want 403", rec.Code)
	}
	if rec := a.do(http.MethodPost, "/api/v1/config/import", `{}`, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("unclaimed config import = %d, want 403", rec.Code)
	}
}

func TestClaimWindowClosesWhenClaimCompletes(t *testing.T) {
	a := newEnforcedApp(t)
	put := func(mut func(*http.Request)) int {
		return a.do(http.MethodPut, "/api/v1/wizards/claim", `{"step":"welcome","payload":{}}`, mut).Code
	}
	if code := put(nil); code != http.StatusOK {
		t.Fatalf("pre-claim wizard save = %d, want 200", code)
	}
	token := a.setupToken(t) // claims, then creates the owner and logs in
	if code := put(nil); code != http.StatusUnauthorized {
		t.Fatalf("post-claim anonymous wizard save = %d, want 401", code)
	}
	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		if rec := a.do(m, "/api/v1/wizards/claim", "", nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("post-claim anonymous wizard %s = %d, want 401", m, rec.Code)
		}
	}
	if code := put(bearer(token)); code != http.StatusOK {
		t.Fatalf("post-claim wizard save with session = %d, want 200", code)
	}
}

func TestSetupLogsTheOwnerIn(t *testing.T) {
	a := newEnforcedApp(t)
	rec := a.setup(t)
	token := jsonField(t, rec, "token")
	csrf := jsonField(t, rec, "csrf_token")
	if token == "" || csrf == "" || jsonField(t, rec, "expires_at") == "" {
		t.Fatalf("setup response lacks session fields: %s", rec.Body.String())
	}
	cookie := cookieByName(rec, SessionCookieName)
	if cookie == nil || !cookie.HttpOnly || cookie.Value != token {
		t.Fatalf("setup did not set the session cookie: %+v", cookie)
	}
	// Bearer works...
	if r := a.do(http.MethodGet, "/api/v1/config", "", bearer(token)); rejected(r.Code) {
		t.Fatalf("bearer after setup = %d", r.Code)
	}
	// ...and so does the cookie, with CSRF on a mutation (the wizard save
	// the claim page performs right after setup).
	save := func(csrfHeader string) int {
		return a.do(http.MethodPut, "/api/v1/wizards/claim", `{"step":"done","payload":{}}`, func(r *http.Request) {
			r.AddCookie(cookie)
			if csrfHeader != "" {
				r.Header.Set(CSRFHeader, csrfHeader)
			}
		}).Code
	}
	if code := save(""); code != http.StatusForbidden {
		t.Fatalf("cookie wizard save without CSRF = %d, want 403", code)
	}
	if code := save(csrf); code != http.StatusOK {
		t.Fatalf("cookie wizard save with CSRF = %d, want 200", code)
	}
	// A second setup is still refused and issues nothing.
	rec2 := a.do(http.MethodPost, "/api/v1/admins/setup", `{"username":"x","password":"another-long-password"}`, nil)
	if rec2.Code != http.StatusConflict || strings.Contains(rec2.Body.String(), "token") || len(rec2.Result().Cookies()) != 0 {
		t.Fatalf("second setup = %d %s", rec2.Code, rec2.Body.String())
	}
}

func TestRevokedSessionIsRejected(t *testing.T) {
	a := newEnforcedApp(t)
	token := a.setupToken(t)
	if rec := a.do(http.MethodDelete, "/api/v1/sessions", "", bearer(token)); rec.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", rec.Code)
	}
	if rec := a.do(http.MethodGet, "/api/v1/config", "", bearer(token)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session = %d, want 401", rec.Code)
	}
	if rec := a.do(http.MethodDelete, "/api/v1/sessions", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous logout = %d, want 401", rec.Code)
	}
}

func TestCSRFMatrixOnRealRoutes(t *testing.T) {
	a := newEnforcedApp(t)
	rec := a.setup(t)
	cookie := cookieByName(rec, SessionCookieName)
	csrf := jsonField(t, rec, "csrf_token")
	token := cookie.Value
	cases := []struct {
		name, method, path, csrf string
		viaBearer                bool
		want                     func(int) bool
	}{
		{"cookie PUT no csrf", "PUT", "/api/v1/config", "", false, func(c int) bool { return c == 403 }},
		{"cookie PUT wrong csrf", "PUT", "/api/v1/config", "nope", false, func(c int) bool { return c == 403 }},
		{"cookie POST no csrf", "POST", "/api/v1/config/plan", "", false, func(c int) bool { return c == 403 }},
		{"cookie DELETE no csrf", "DELETE", "/api/v1/sessions", "", false, func(c int) bool { return c == 403 }},
		{"cookie PUT csrf ok", "PUT", "/api/v1/config", csrf, false, func(c int) bool { return !rejected(c) }},
		{"cookie GET no csrf ok", "GET", "/api/v1/config", "", false, func(c int) bool { return !rejected(c) }},
		{"bearer PUT no csrf ok", "PUT", "/api/v1/config", "", true, func(c int) bool { return !rejected(c) }},
		{"bearer POST no csrf ok", "POST", "/api/v1/config/plan", "", true, func(c int) bool { return !rejected(c) }},
	}
	for _, tc := range cases {
		code := a.do(tc.method, tc.path, `{"settings":{}}`, func(r *http.Request) {
			if tc.viaBearer {
				r.Header.Set("Authorization", "Bearer "+token)
			} else {
				r.AddCookie(cookie)
			}
			if tc.csrf != "" {
				r.Header.Set(CSRFHeader, tc.csrf)
			}
		}).Code
		if !tc.want(code) {
			t.Errorf("%s: status %d", tc.name, code)
		}
	}
}

func TestRecoverySocketUnaffectedByEnforcement(t *testing.T) {
	a := newEnforcedApp(t)
	a.setup(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admins/recover",
		strings.NewReader(`{"username":"owner","password":"a-brand-new-password"}`))
	a.srv.SocketHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("socket recovery without a session = %d: %s", rec.Code, rec.Body.String())
	}
	// And the same route is not reachable over TCP at all.
	if r := a.do(http.MethodPost, "/api/v1/admins/recover", "{}", nil); r.Code == http.StatusOK {
		t.Fatalf("recover served on TCP: %d", r.Code)
	}
}

func TestRejectionsAreAuditedWithoutCredentials(t *testing.T) {
	a := newEnforcedApp(t)
	a.setup(t)
	for i := 0; i < 3; i++ {
		a.do(http.MethodGet, "/api/v1/config", "", bearer("super-secret-token"))
	}
	a.srv.boundary.flush()
	entries, err := audit.New(a.st).List(100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Action != auditActionRejected {
			continue
		}
		found = true
		if strings.Contains(e.Detail, "super-secret") || !strings.Contains(e.Detail, "x3") {
			t.Fatalf("rejection audit entry %q", e.Detail)
		}
	}
	if !found {
		t.Fatal("no rejection audit entry")
	}
}

func jsonField(t *testing.T, rec *httptest.ResponseRecorder, key string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	v, _ := m[key].(string)
	return v
}
