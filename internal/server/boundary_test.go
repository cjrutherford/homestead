package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/claim"
	"homestead/internal/config"
	"homestead/internal/docker"
	"homestead/internal/firewall"
	"homestead/internal/operation"
	"homestead/internal/secret"
	"homestead/internal/store"
	"homestead/internal/wizard"
)

// fullServer mounts every production TCP route, mirroring
// cmd/homestead-server, so the classification test sees the real set.
func fullServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, store.DBFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	claimSvc := claim.New(st)
	authSvc := auth.New(st, claimSvc)
	srv := New(DefaultAddr)
	cfgPath := filepath.Join(dir, "desired.yaml")
	cfg := config.New(firewall.Schema(), cfgPath)
	srv.MountConfig(cfg)
	secrets, err := secret.New(st, filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	srv.MountConfigIO(cfgPath, secrets)
	srv.MountClaim(claimSvc)
	srv.MountAuth(authSvc, audit.New(st))
	srv.MountAudit(audit.New(st))
	srv.MountRecovery(authSvc)
	srv.MountWizards(wizard.New(st))
	srv.MountDoctor()
	srv.MountOperations(operation.New(st))
	srv.MountCatalog(st, "catalog")
	srv.MountStorage(st)
	srv.MountBackups(st)
	srv.MountDrift(st, "catalog")
	dc := docker.NewClient("")
	srv.MountDocker(dc)
	srv.MountDockge(dc)
	srv.MountEdge(st)
	srv.MountUpdates(st, "catalog", dc)
	srv.MountFirewall(8080, cfg)
	srv.MountExposure(st, 8080)
	srv.MountTunnel(st, secrets)
	srv.MountDashboard(http.NotFoundHandler())
	return srv, st
}

func TestEveryRegisteredRouteIsClassified(t *testing.T) {
	srv, _ := fullServer(t)
	routes := srv.Routes()
	registered := map[string]bool{}
	for _, p := range routes {
		if registered[p] {
			t.Errorf("route %q registered twice", p)
		}
		registered[p] = true
		if _, ok := Classify(p); !ok {
			t.Errorf("route %q is unclassified: add it to routeClasses (ADR-020)", p)
		}
	}
	for p := range routeClasses {
		if !registered[p] {
			t.Errorf("routeClasses has stale entry %q (not registered)", p)
		}
	}
	counts := map[RouteClass]int{}
	for _, p := range routes {
		c, _ := Classify(p)
		counts[c]++
	}
	t.Logf("TCP routes: %d (public %d, claim-window %d, session %d)",
		len(routes), counts[ClassPublic], counts[ClassClaimWindow], counts[ClassSession])
	if counts[ClassPublic] != 7 {
		t.Errorf("public allowlist size = %d, want 7 (ADR-020)", counts[ClassPublic])
	}
	if counts[ClassClaimWindow] != 3 {
		t.Errorf("claim-window routes = %d, want 3", counts[ClassClaimWindow])
	}
	var public []string
	for _, p := range routes {
		if c, _ := Classify(p); c == ClassPublic {
			public = append(public, p)
		}
	}
	sort.Strings(public)
	want := []string{"/", "GET /api/v1/identity", "GET /healthz", "GET /version",
		"POST /api/v1/admins/setup", "POST /api/v1/claim", "POST /api/v1/sessions"}
	sort.Strings(want)
	if strings.Join(public, "|") != strings.Join(want, "|") {
		t.Errorf("public allowlist = %v, want %v", public, want)
	}
}

func TestRecoverIsSocketOnly(t *testing.T) {
	srv, _ := fullServer(t)
	for _, p := range srv.Routes() {
		if strings.Contains(p, "admins/recover") {
			t.Fatalf("recover route %q registered on TCP mux", p)
		}
	}
	rec := httptest.NewRecorder()
	srv.SocketHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/admins/recover", strings.NewReader("{}")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("socket recover status = %d, want 400 (route present)", rec.Code)
	}
}

type fakeClaims struct {
	state claim.State
	err   error
}

func (f *fakeClaims) CurrentState() (claim.State, error) { return f.state, f.err }

type fakeSessions struct{ valid string }

func (f fakeSessions) Verify(token string) (auth.VerifiedSession, error) {
	if token == f.valid {
		return auth.VerifiedSession{Username: "owner"}, nil
	}
	return auth.VerifiedSession{}, auth.ErrInvalidSession
}

type fakeAudit struct{ entries []audit.Entry }

func (f *fakeAudit) Record(e audit.Entry) (audit.Entry, error) {
	f.entries = append(f.entries, e)
	return e, nil
}

type boundaryRig struct {
	srv    *Server
	claims *fakeClaims
	audit  *fakeAudit
	now    time.Time
}

func newRig(t *testing.T, state claim.State) *boundaryRig {
	t.Helper()
	r := &boundaryRig{claims: &fakeClaims{state: state}, audit: &fakeAudit{}, now: time.Unix(1_700_000_000, 0)}
	r.srv = New(DefaultAddr)
	r.srv.mux.HandleFunc("PUT /api/v1/config", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	r.srv.mux.HandleFunc("PUT /api/v1/wizards/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	r.srv.mountBoundary(fakeSessions{valid: "good"}, r.claims, r.audit, func() time.Time { return r.now })
	return r
}

func (r *boundaryRig) do(method, path, bearer string) int {
	req := httptest.NewRequest(method, path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	r.srv.Handler().ServeHTTP(rec, req)
	return rec.Code
}

func (r *boundaryRig) reasons() []string {
	r.srv.boundary.flush()
	var out []string
	for _, e := range r.audit.entries {
		out = append(out, e.Detail)
	}
	return out
}

func TestBoundaryReportOnlyNeverRejects(t *testing.T) {
	r := newRig(t, claim.StateClaimed)
	if got := r.do(http.MethodPut, "/api/v1/config", ""); got != http.StatusTeapot {
		t.Fatalf("report-only must serve: status %d", got)
	}
	got := r.reasons()
	if len(got) != 1 || !strings.Contains(got[0], "no-session") {
		t.Fatalf("audit = %v, want one no-session entry", got)
	}
	if r.audit.entries[0].Action != auditActionWouldReject || r.audit.entries[0].Outcome != audit.OutcomeError {
		t.Fatalf("bad entry %+v", r.audit.entries[0])
	}
}

func TestBoundaryReasons(t *testing.T) {
	cases := []struct {
		name, method, path, bearer string
		state                      claim.State
		want                       string // "" = nothing reported
	}{
		{"public claimed", "GET", "/healthz", "", claim.StateClaimed, ""},
		{"public unclaimed", "GET", "/healthz", "", claim.StateUnclaimed, ""},
		{"valid session", "PUT", "/api/v1/config", "good", claim.StateClaimed, ""},
		{"bad session", "PUT", "/api/v1/config", "bad", claim.StateClaimed, "invalid-session"},
		{"no session", "PUT", "/api/v1/config", "", claim.StateClaimed, "no-session"},
		{"pre-claim config", "PUT", "/api/v1/config", "", claim.StateUnclaimed, "pre-claim"},
		{"pre-claim even with session", "PUT", "/api/v1/config", "good", claim.StateUnclaimed, "pre-claim"},
		{"wizard pre-claim", "PUT", "/api/v1/wizards/claim", "", claim.StateUnclaimed, ""},
		{"wizard after claim, no session", "PUT", "/api/v1/wizards/claim", "", claim.StateClaimed, "claim-window-closed"},
		{"wizard after claim, session", "PUT", "/api/v1/wizards/claim", "good", claim.StateClaimed, ""},
		{"unrouted path", "GET", "/api/v1/nope", "", claim.StateClaimed, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, tc.state)
			r.do(tc.method, tc.path, tc.bearer)
			got := r.reasons()
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("unexpected report %v", got)
				}
				return
			}
			if len(got) != 1 || !strings.Contains(got[0], "("+tc.want+")") {
				t.Fatalf("report = %v, want reason %s", got, tc.want)
			}
		})
	}
}

func TestBoundaryClaimStateErrorFailsClosed(t *testing.T) {
	r := newRig(t, claim.StateUnclaimed)
	r.claims.err = errors.New("db down")
	r.do(http.MethodPut, "/api/v1/wizards/claim", "")
	got := r.reasons()
	if len(got) != 1 || !strings.Contains(got[0], "claim-window-closed") {
		t.Fatalf("report = %v", got)
	}
}

func TestBoundaryAggregatesPerWindow(t *testing.T) {
	r := newRig(t, claim.StateClaimed)
	for i := 0; i < 50; i++ {
		r.do(http.MethodPut, "/api/v1/config", "")
	}
	r.do(http.MethodPut, "/api/v1/wizards/x", "")
	if n := len(r.audit.entries); n != 0 {
		t.Fatalf("entries inside window = %d, want 0 until flush", n)
	}
	r.now = r.now.Add(reportWindow + time.Second)
	r.do(http.MethodPut, "/api/v1/config", "") // crosses the window
	if n := len(r.audit.entries); n != 2 {
		t.Fatalf("entries after window = %d, want 2 (one per route+reason)", n)
	}
	var cfg string
	for _, e := range r.audit.entries {
		if strings.Contains(e.Detail, "PUT /api/v1/config") {
			cfg = e.Detail
		}
	}
	if !strings.Contains(cfg, "x50") {
		t.Fatalf("config entry %q lacks count x50", cfg)
	}
}

func TestBoundaryNeverRecordsCredentials(t *testing.T) {
	r := newRig(t, claim.StateClaimed)
	r.do(http.MethodPut, "/api/v1/config", "supersecret-token")
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", nil)
	req.AddCookie(&http.Cookie{Name: "homestead_session", Value: "cookie-secret"})
	r.srv.Handler().ServeHTTP(httptest.NewRecorder(), req)
	for _, d := range r.reasons() {
		if strings.Contains(d, "supersecret") || strings.Contains(d, "cookie-secret") {
			t.Fatalf("credential leaked into audit: %q", d)
		}
	}
}

func TestBoundaryUnclassifiedRouteIsReported(t *testing.T) {
	r := newRig(t, claim.StateClaimed)
	r.srv.mux.HandleFunc("GET /api/v1/brand-new", func(http.ResponseWriter, *http.Request) {})
	if got := r.do(http.MethodGet, "/api/v1/brand-new", "good"); got != http.StatusOK {
		t.Fatalf("status %d", got)
	}
	got := r.reasons()
	if len(got) != 1 || !strings.Contains(got[0], "unclassified-route") {
		t.Fatalf("report = %v", got)
	}
}
