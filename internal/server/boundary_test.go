package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/claim"
	"homestead/internal/store"
)

// fullServer mounts every production TCP route through the same
// MountApp cmd/homestead-server uses, so wiring drift cannot hide.
func fullServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	srv, st, _ := fullServerApp(t)
	return srv, st
}

func fullServerApp(t *testing.T) (*Server, *store.Store, *AppServices) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, store.DBFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := New(DefaultAddr)
	svcs, err := MountApp(srv, AppConfig{
		Store: st, ConfigPath: filepath.Join(dir, "desired.yaml"), CatalogDir: "catalog",
		SecretKeyPath: filepath.Join(dir, "key"), Port: 8080,
		Dashboard: http.NotFoundHandler(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv, st, svcs
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
	// The craft-file socket mux (ADR-032) is enumerated too: each of its
	// routes must be classified craft-session, and no craft-session route
	// may be registered on the TCP mux.
	craftRoutes := srv.CraftRoutes()
	for _, p := range routes {
		if c, _ := Classify(p); c == ClassCraftSession {
			t.Errorf("craft-session route %q registered on the TCP mux", p)
		}
	}
	for _, p := range craftRoutes {
		if registered[p] {
			t.Errorf("route %q registered on both the TCP and craft muxes", p)
		}
		registered[p] = true
		c, ok := Classify(p)
		if !ok {
			t.Errorf("craft route %q is unclassified: add it to routeClasses (ADR-020)", p)
		} else if c != ClassCraftSession {
			t.Errorf("craft route %q is class %s, want craft-session", p, c)
		}
	}
	if len(craftRoutes) != 14 {
		t.Errorf("craft route count = %d, want 14 (update deliberately with routeClasses)", len(craftRoutes))
	}
	for p := range routeClasses {
		if !registered[p] {
			t.Errorf("routeClasses has stale entry %q (not registered)", p)
		}
	}
	if len(routes) != 180 {
		t.Errorf("TCP route count = %d, want 180 (update deliberately with routeClasses)", len(routes))
	}
	counts := map[RouteClass]int{}
	for _, p := range routes {
		c, _ := Classify(p)
		counts[c]++
	}
	t.Logf("TCP routes: %d (public %d, claim-window %d, session %d)",
		len(routes), counts[ClassPublic], counts[ClassClaimWindow], counts[ClassSession])
	if counts[ClassPublic] != 12 {
		t.Errorf("public allowlist size = %d, want 12 (ADR-020, ADR-030)", counts[ClassPublic])
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
		"GET /api/v1/tls/local-ca/root.der", "GET /api/v1/tls/local-ca/root.pem",
		"POST /api/v1/admins/setup", "POST /api/v1/claim", "POST /api/v1/sessions",
		"GET /api/v1/sso/options", "GET /api/v1/sso/login", "GET /api/v1/sso/callback"}
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

type fakeSessions struct {
	valid string
	err   error // non-invalid-session store failure
	// expires, when set with now, makes the valid token die at that time.
	expires time.Time
	now     func() time.Time
}

func (f fakeSessions) Verify(token string) (auth.VerifiedSession, error) {
	if f.err != nil {
		return auth.VerifiedSession{}, f.err
	}
	if !f.expires.IsZero() && !f.now().Before(f.expires) {
		return auth.VerifiedSession{}, auth.ErrInvalidSession
	}
	if token == f.valid {
		sum := sha256.Sum256([]byte("csrf-good"))
		return auth.VerifiedSession{Username: "owner", CSRFHash: sum[:]}, nil
	}
	return auth.VerifiedSession{}, auth.ErrInvalidSession
}

type fakeAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
	err     error
}

func (f *fakeAudit) Record(e audit.Entry) (audit.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return audit.Entry{}, f.err
	}
	f.entries = append(f.entries, e)
	return e, nil
}

func (f *fakeAudit) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.entries)
}

type fakeLog struct {
	mu   sync.Mutex
	msgs []string
}

func (l *fakeLog) Warn(msg string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = append(l.msgs, msg)
}

func (l *fakeLog) count(msg string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, m := range l.msgs {
		if m == msg {
			n++
		}
	}
	return n
}

type boundaryRig struct {
	srv    *Server
	log    *fakeLog
	claims *fakeClaims
	audit  *fakeAudit
	now    time.Time
}

func newRig(t *testing.T, state claim.State) *boundaryRig {
	t.Helper()
	r := &boundaryRig{claims: &fakeClaims{state: state}, audit: &fakeAudit{}, now: time.Unix(1_700_000_000, 0)}
	r.srv = New(DefaultAddr)
	r.log = &fakeLog{}
	r.srv.mux.HandleFunc("PUT /api/v1/config", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	r.srv.mux.HandleFunc("PUT /api/v1/wizards/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	r.srv.mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	r.srv.mountBoundary(fakeSessions{valid: "good"}, r.claims, r.audit, func() time.Time { return r.now })
	r.srv.boundary.log = r.log
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

func TestBoundaryEnforcesAndAudits(t *testing.T) {
	r := newRig(t, claim.StateClaimed)
	if got := r.do(http.MethodPut, "/api/v1/config", ""); got != http.StatusUnauthorized {
		t.Fatalf("enforcing: no session must be 401, got %d", got)
	}
	got := r.reasons()
	if len(got) != 1 || !strings.Contains(got[0], "no-session") {
		t.Fatalf("audit = %v, want one no-session entry", got)
	}
	if r.audit.entries[0].Action != auditActionRejected || r.audit.entries[0].Outcome != audit.OutcomeError {
		t.Fatalf("bad entry %+v", r.audit.entries[0])
	}
	if got := r.do(http.MethodPut, "/api/v1/config", "good"); got != http.StatusTeapot {
		t.Fatalf("valid session must reach the handler, got %d", got)
	}
}

// reasonStatus is the documented status per rejection reason.
var reasonStatus = map[string]int{
	reasonPreClaim: 403, reasonNoSession: 401, reasonInvalidSession: 401,
	reasonWizardClaimed: 401, reasonUnclassified: 403, reasonClaimStateErr: 503,
	reasonCSRFMissing: 403, reasonCSRFMismatch: 403, reasonVerifyError: 503,
}

var rejectionStatuses = map[int]bool{401: true, 403: true, 503: true}

func TestBoundaryReasons(t *testing.T) {
	cases := []struct {
		name, method, path, bearer string
		state                      claim.State
		want                       string // "" = nothing rejected
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
		{"unrouted api path falls to catch-all", "GET", "/api/v1/nope", "", claim.StateClaimed, "no-session"},
		{"unregistered method on api path", "DELETE", "/api/v1/config", "", claim.StateClaimed, "no-session"},
		{"unregistered method, valid session", "DELETE", "/api/v1/config", "good", claim.StateClaimed, ""},
		{"dashboard GET public", "GET", "/index.html", "", claim.StateClaimed, ""},
		{"dashboard HEAD public", "HEAD", "/", "", claim.StateClaimed, ""},
		{"dashboard POST not public", "POST", "/index.html", "", claim.StateClaimed, "no-session"},
		{"api path pre-claim", "PUT", "/api/v1/other", "", claim.StateUnclaimed, "pre-claim"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, tc.state)
			status := r.do(tc.method, tc.path, tc.bearer)
			got := r.reasons()
			if tc.want == "" {
				if len(got) != 0 || rejectionStatuses[status] {
					t.Fatalf("unexpected rejection %v (status %d)", got, status)
				}
				return
			}
			if len(got) != 1 || !strings.Contains(got[0], "("+tc.want+")") {
				t.Fatalf("report = %v, want reason %s", got, tc.want)
			}
			if status != reasonStatus[tc.want] {
				t.Fatalf("status = %d, want %d", status, reasonStatus[tc.want])
			}
		})
	}
}

func TestBoundaryClaimStateErrorFailsClosed(t *testing.T) {
	r := newRig(t, claim.StateUnclaimed)
	r.claims.err = errors.New("db down")
	for i := 0; i < 5; i++ {
		if got := r.do(http.MethodPut, "/api/v1/wizards/claim", ""); got != http.StatusServiceUnavailable {
			t.Fatalf("claim state unreadable must fail closed with 503, got %d", got)
		}
	}
	got := r.reasons()
	if len(got) != 1 || !strings.Contains(got[0], "(claim-state-error)") || !strings.Contains(got[0], "x5") {
		t.Fatalf("report = %v", got)
	}
	if n := r.log.count("session boundary: claim state unreadable; failing closed"); n != 1 {
		t.Fatalf("state error logged %d times in one window, want 1 (rate limited)", n)
	}
	r.now = r.now.Add(reportWindow + time.Second)
	r.do(http.MethodPut, "/api/v1/wizards/claim", "")
	if n := r.log.count("session boundary: claim state unreadable; failing closed"); n != 2 {
		t.Fatalf("state error logged %d times after window, want 2", n)
	}
}

func TestBoundaryRequestsNeverWriteAudit(t *testing.T) {
	r := newRig(t, claim.StateClaimed)
	for i := 0; i < 20; i++ {
		r.do(http.MethodPut, "/api/v1/config", "")
		r.now = r.now.Add(reportWindow) // every request crosses a window
	}
	if n := r.audit.count(); n != 0 {
		t.Fatalf("requests wrote %d audit entries; only the ticker/flush may", n)
	}
	r.srv.boundary.flush()
	if n := r.audit.count(); n != 1 {
		t.Fatalf("entries after flush = %d, want 1", n)
	}
}

func TestBoundaryAggregatesPerWindow(t *testing.T) {
	r := newRig(t, claim.StateClaimed)
	for i := 0; i < 50; i++ {
		r.do(http.MethodPut, "/api/v1/config", "")
	}
	r.do(http.MethodPut, "/api/v1/wizards/x", "")
	if n := r.audit.count(); n != 0 {
		t.Fatalf("entries inside window = %d, want 0 until flush", n)
	}
	r.srv.boundary.flush()
	if n := r.audit.count(); n != 2 {
		t.Fatalf("entries after flush = %d, want 2 (one per route+reason)", n)
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

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBoundaryRunFlushesOnTickAndShutdown(t *testing.T) {
	r := newRig(t, claim.StateClaimed)
	r.do(http.MethodPut, "/api/v1/config", "")
	tick := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.srv.boundary.run(ctx, tick) }()
	tick <- time.Now()
	waitFor(t, func() bool { return r.audit.count() == 1 })
	r.do(http.MethodPut, "/api/v1/wizards/x", "")
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("run did not return after cancel")
	}
	if n := r.audit.count(); n != 2 {
		t.Fatalf("entries after shutdown = %d, want 2 (final flush completed before run returned)", n)
	}
}

func TestBoundaryConcurrentReportAndFlush(t *testing.T) {
	r := newRig(t, claim.StateClaimed)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				r.do(http.MethodPut, "/api/v1/config", "")
				if j%25 == 0 {
					r.srv.boundary.flush()
				}
			}
		}()
	}
	wg.Wait()
	r.srv.boundary.flush()
	total := 0
	for _, e := range r.audit.entries {
		var n int
		idx := strings.Index(e.Detail, ") x")
		if idx < 0 {
			t.Fatalf("bad detail %q", e.Detail)
		}
		if _, err := fmt.Sscanf(e.Detail[idx+3:], "%d", &n); err != nil {
			t.Fatal(err)
		}
		total += n
	}
	if total != 1600 {
		t.Fatalf("counted %d reports, want 1600 (none lost or duplicated)", total)
	}
}

func TestBoundaryAuditFailureIsLoggedNotFatal(t *testing.T) {
	r := newRig(t, claim.StateClaimed)
	r.audit.err = errors.New("disk full")
	if got := r.do(http.MethodPut, "/api/v1/config", ""); got != http.StatusUnauthorized {
		t.Fatalf("status %d", got)
	}
	r.srv.boundary.flush()
	if n := r.log.count("audit record failed"); n != 1 {
		t.Fatalf("audit failure logged %d times, want 1", n)
	}
	if n := r.audit.count(); n != 0 {
		t.Fatalf("entries = %d", n)
	}
}

func TestBoundaryDoesNotWrapUnixSocket(t *testing.T) {
	srv, st := fullServer(t)
	aud := &fakeAudit{}
	cl := &fakeClaims{state: claim.StateClaimed}
	srv.mountBoundary(fakeSessions{}, cl, aud, time.Now)
	_ = st
	r := &boundaryRig{srv: srv, audit: aud}
	rec := httptest.NewRecorder()
	r.srv.SocketHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/admins/recover", strings.NewReader("{}")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("socket status = %d, want 400 (served without a session)", rec.Code)
	}
	if got := r.reasons(); len(got) != 0 {
		t.Fatalf("socket request was classified by the boundary: %v", got)
	}
	if r.srv.unix.Handler != http.Handler(r.srv.socketMux) {
		t.Fatal("unix server handler was replaced")
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

func TestBoundaryUnclassifiedRouteIsRejected(t *testing.T) {
	r := newRig(t, claim.StateClaimed)
	r.srv.mux.HandleFunc("GET /api/v1/brand-new", func(http.ResponseWriter, *http.Request) {})
	// Even a valid session cannot reach a route nobody classified.
	if got := r.do(http.MethodGet, "/api/v1/brand-new", "good"); got != http.StatusForbidden {
		t.Fatalf("status %d, want 403", got)
	}
	got := r.reasons()
	if len(got) != 1 || !strings.Contains(got[0], "unclassified-route") {
		t.Fatalf("report = %v", got)
	}
}

func TestRunWaitsForBoundaryFinalFlush(t *testing.T) {
	r := newRig(t, claim.StateClaimed)
	r.srv.addr = "127.0.0.1:0"
	r.do(http.MethodPut, "/api/v1/config", "")
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- r.srv.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	if n := r.audit.count(); n != 1 {
		t.Fatalf("entries when Run returned = %d, want 1 (final flush must finish before stores close)", n)
	}
}

func (r *boundaryRig) doCookie(method, path, cookie, csrf, bearer string) int {
	req := httptest.NewRequest(method, path, nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: cookie})
	}
	if csrf != "" {
		req.Header.Set(CSRFHeader, csrf)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	r.srv.Handler().ServeHTTP(rec, req)
	return rec.Code
}

func TestBoundaryRecognizesCookieSessionsAndCSRF(t *testing.T) {
	cases := []struct {
		name                       string
		method, cookie, csrf, bear string
		want                       string // "" = nothing reported
	}{
		{"cookie GET needs no csrf", "GET", "good", "", "", ""},
		{"cookie PUT with csrf", "PUT", "good", "csrf-good", "", ""},
		{"cookie PUT missing csrf", "PUT", "good", "", "", "csrf-missing"},
		{"cookie POST missing csrf", "POST", "good", "", "", "csrf-missing"},
		{"cookie PATCH missing csrf", "PATCH", "good", "", "", "csrf-missing"},
		{"cookie DELETE missing csrf", "DELETE", "good", "", "", "csrf-missing"},
		{"cookie POST wrong csrf", "POST", "good", "csrf-bad", "", "csrf-mismatch"},
		{"cookie DELETE with csrf", "DELETE", "good", "csrf-good", "", ""},
		{"bearer POST exempt", "POST", "", "", "good", ""},
		{"bearer DELETE exempt", "DELETE", "", "", "good", ""},
		{"cookie PUT wrong csrf", "PUT", "good", "csrf-bad", "", "csrf-mismatch"},
		{"cookie PUT csrf equal to session token", "PUT", "good", "good", "", "csrf-mismatch"},
		{"cookie invalid session", "PUT", "bad", "csrf-good", "", "invalid-session"},
		{"bearer PUT exempt from csrf", "PUT", "", "", "good", ""},
		{"bearer wins over cookie, exempt", "PUT", "good", "", "good", ""},
		{"bad bearer is not rescued by good cookie", "PUT", "good", "csrf-good", "bad", "invalid-session"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, claim.StateClaimed)
			path := "/api/v1/config"
			if tc.method == "GET" {
				path = "/api/v1/config"
				r.srv.mux.HandleFunc("GET /api/v1/config", func(http.ResponseWriter, *http.Request) {})
			}
			status := r.doCookie(tc.method, path, tc.cookie, tc.csrf, tc.bear)
			got := r.reasons()
			if tc.want == "" {
				if len(got) != 0 || rejectionStatuses[status] {
					t.Fatalf("unexpected rejection %v (status %d)", got, status)
				}
				return
			}
			if len(got) != 1 || !strings.Contains(got[0], "("+tc.want+")") {
				t.Fatalf("report = %v, want %s", got, tc.want)
			}
			if status != reasonStatus[tc.want] {
				t.Fatalf("status = %d, want %d", status, reasonStatus[tc.want])
			}
		})
	}
}

func TestBoundaryNeverRecordsCSRFValues(t *testing.T) {
	r := newRig(t, claim.StateClaimed)
	r.doCookie(http.MethodPut, "/api/v1/config", "good", "csrf-wrong-secret", "")
	for _, d := range r.reasons() {
		if strings.Contains(d, "csrf-wrong-secret") || strings.Contains(d, "csrf-good") {
			t.Fatalf("csrf value leaked into audit: %q", d)
		}
	}
}

func TestBoundaryStoreErrorFailsClosedWith503(t *testing.T) {
	r := newRig(t, claim.StateClaimed)
	r.srv.mountBoundary(fakeSessions{valid: "good", err: errors.New("db down")}, r.claims, r.audit, func() time.Time { return r.now })
	r.srv.boundary.log = r.log
	status := r.do(http.MethodPut, "/api/v1/config", "good")
	if status != reasonStatus[reasonVerifyError] {
		t.Fatalf("status = %d, want %d", status, reasonStatus[reasonVerifyError])
	}
	got := r.reasons()
	if len(got) != 1 || !strings.Contains(got[0], "("+reasonVerifyError+")") {
		t.Fatalf("report = %v", got)
	}
}

func TestBoundaryRejectsExpiredSession(t *testing.T) {
	r := newRig(t, claim.StateClaimed)
	clock := func() time.Time { return r.now }
	r.srv.mountBoundary(fakeSessions{valid: "good", expires: r.now.Add(time.Hour), now: clock}, r.claims, r.audit, clock)
	r.srv.boundary.log = r.log
	if got := r.do(http.MethodPut, "/api/v1/config", "good"); got != http.StatusTeapot {
		t.Fatalf("live session status = %d", got)
	}
	r.now = r.now.Add(2 * time.Hour)
	if got := r.do(http.MethodPut, "/api/v1/config", "good"); got != http.StatusUnauthorized {
		t.Fatalf("expired session status = %d, want 401", got)
	}
}

func TestBoundaryInvalidBearerDoesNotFallBackToCookie(t *testing.T) {
	r := newRig(t, claim.StateClaimed)
	if got := r.doCookie(http.MethodGet, "/api/v1/operations", "good", "", "bad"); got != http.StatusUnauthorized {
		t.Fatalf("invalid bearer + valid cookie = %d, want 401", got)
	}
}

func TestSessionStillValidFailsClosedWithoutSession(t *testing.T) {
	if sessionStillValid(httptest.NewRequest(http.MethodGet, "/", nil)) {
		t.Fatal("no boundary session in context must not count as valid")
	}
}
