package craftfiles

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---- finding 1: https required --------------------------------------

func TestBaseURLRequiresHTTPSUnlessLoopbackOrOptedIn(t *testing.T) {
	e := newMgr(t)
	for _, u := range []string{"http://nextcloud.lan", "http://192.168.1.20:8080", "http://[fe80::1]/"} {
		_, err := e.m.Plan(PlanRequest{Action: ActionLink, BaseURL: u, Account: "alice", Root: "Homestead"}, "owner")
		if !errors.Is(err, ErrInput) || !strings.Contains(err.Error(), "https") {
			t.Fatalf("%s without opt-in: %v", u, err)
		}
	}
	for _, u := range []string{"https://nextcloud.lan", "http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080"} {
		p, err := e.m.Plan(PlanRequest{Action: ActionLink, BaseURL: u, Account: "alice", Root: "Homestead"}, "owner")
		if err != nil || p.Insecure {
			t.Fatalf("%s should plan without an insecure flag: %+v %v", u, p, err)
		}
		for _, w := range p.Warnings {
			if strings.Contains(w, "INSECURE") {
				t.Fatalf("%s carries the insecure warning", u)
			}
		}
	}
	p, err := e.m.Plan(PlanRequest{Action: ActionLink, BaseURL: "http://nextcloud.lan", Account: "alice", Root: "Homestead", AllowInsecureHTTP: true}, "owner")
	if err != nil || !p.Insecure || len(p.Warnings) == 0 || !strings.HasPrefix(p.Warnings[0], "INSECURE") {
		t.Fatalf("opted-in plain http plan: %+v %v", p, err)
	}
}

func TestRotateOfPlainHTTPLinkNeedsOptInAgain(t *testing.T) {
	e := newMgr(t)
	if _, _, err := link(t, e, PlanRequest{Action: ActionLink, BaseURL: e.baseURL, Account: "alice", Root: "Homestead"}, ApproveRequest{AppPassword: testPassword}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.DB.Exec(`UPDATE craft_file_link SET base_url = 'http://nextcloud.lan' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Plan(PlanRequest{Action: ActionRotate}, "owner"); !errors.Is(err, ErrInput) {
		t.Fatalf("rotate without opt-in: %v", err)
	}
	p, err := e.m.Plan(PlanRequest{Action: ActionRotate, AllowInsecureHTTP: true}, "owner")
	if err != nil || !p.Insecure || !strings.HasPrefix(p.Warnings[0], "INSECURE") {
		t.Fatalf("rotate with opt-in: %+v %v", p, err)
	}
}

// The appliance's own Nextcloud is reached through its https .lan route
// signed by the internal CA: the upstream client trusts that root.
func TestUpstreamTrustsInternalCARoot(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(207) }))
	defer srv.Close()
	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})

	plain := (&Upstream{}).Client()
	if _, err := plain.Get(srv.URL); err == nil {
		t.Fatal("an unknown CA must not be trusted by default")
	}
	var uerr x509.UnknownAuthorityError
	if _, err := plain.Get(srv.URL); !errors.As(err, &uerr) {
		t.Fatalf("want an unknown authority error, got %v", err)
	}
	up := &Upstream{Roots: func() []byte { return rootPEM }}
	resp, err := up.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("internal CA root not trusted: %v", err)
	}
	resp.Body.Close()
	if up.Client() != up.Client() {
		t.Fatal("client rebuilt without a CA change")
	}
}

// ---- finding 2: mid-stream revocation -------------------------------

func TestRevokingOperatorSessionCancelsInFlightRequest(t *testing.T) {
	e := newEnv(t, Limits{})
	ticks := make(chan time.Time)
	e.proxy.Tick = func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} }
	cookie := e.login()
	e.dav.mu.Lock()
	e.dav.hold = make(chan struct{})
	e.dav.entered = make(chan struct{}, 1)
	e.dav.mu.Unlock()
	defer close(e.dav.hold)

	done := make(chan int, 1)
	go func() {
		r := e.do("PUT", "/_homestead/files/f/long.txt", strings.NewReader("x"), withCookie(cookie))
		r.Body.Close()
		done <- r.StatusCode
	}()
	<-e.dav.entered
	ticks <- e.clock.Now() // still valid: the request keeps running
	select {
	case code := <-done:
		t.Fatalf("request ended early with %d", code)
	case <-time.After(100 * time.Millisecond):
	}
	e.op.revoke("op-token")
	ticks <- e.clock.Now()
	select {
	case code := <-done:
		if code != 401 {
			t.Fatalf("revoked in flight = %d, want 401", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight request survived revocation")
	}
}

// ---- finding 3: non-2xx bodies are never relayed --------------------

func TestUpstreamErrorBodiesAreReplacedAndNosniff(t *testing.T) {
	e := newEnv(t, Limits{})
	cookie := e.login()
	e.dav.mu.Lock()
	e.dav.status = 507
	e.dav.mu.Unlock()
	for _, method := range []string{"PUT", "GET", "DELETE", "PROPFIND"} {
		r := e.do(method, "/_homestead/files/f/a.txt", strings.NewReader("x"), withCookie(cookie))
		body := readAll(t, r)
		if r.StatusCode != 507 || strings.Contains(body, "forced upstream") {
			t.Fatalf("%s: %d %q relays the upstream body", method, r.StatusCode, body)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Fatalf("%s error Content-Type = %q", method, ct)
		}
		if r.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s lacks nosniff", method)
		}
		if !strings.Contains(body, "507") {
			t.Fatalf("%s body %q lacks the status", method, body)
		}
	}
	// Success responses carry nosniff too, also when Serve is used directly.
	e.dav.mu.Lock()
	e.dav.status = 0
	e.dav.files["a.txt"] = []byte("hi")
	e.dav.mu.Unlock()
	r := e.do("GET", "/_homestead/files/f/a.txt", nil, withCookie(cookie))
	r.Body.Close()
	if r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("GET lacks nosniff")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	e.proxy.Serve(rec, req, &Session{ID: "s", App: testApp, Operator: "owner"}, "a.txt")
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("Serve lacks nosniff")
	}
}

// ---- finding 4: account names ---------------------------------------

func TestAccountNameValidation(t *testing.T) {
	for _, a := range []string{".", "..", "a/b", "a\\b", "a:b", "a\nb", "a\x00b", "a\x7f", ""} {
		if err := validateAccount(a); !errors.Is(err, ErrInput) {
			t.Errorf("account %q accepted", a)
		}
	}
	for _, a := range []string{"alice", "alice@example.com", "a.b", "A B"} {
		if err := validateAccount(a); err != nil {
			t.Errorf("account %q refused: %v", a, err)
		}
	}
}

// ---- finding 5: scheme comes from the edge --------------------------

func TestSchemeMustBeSetByTheEdge(t *testing.T) {
	e := newEnv(t, Limits{})
	for _, v := range []string{"", "ftp", "https, http"} {
		r := e.do("GET", SessionPath, nil, withHeader("X-Forwarded-Proto", v))
		r.Body.Close()
		if r.StatusCode != 400 {
			t.Fatalf("X-Forwarded-Proto %q = %d, want 400", v, r.StatusCode)
		}
	}
	// A plain-http route: the Origin must be the http origin, and the
	// cookie is not Secure.
	code := mint(t, e, testApp, testHost)
	r := e.do("POST", SessionPath, strings.NewReader(`{"code":"`+code+`"}`),
		withHeader("X-Forwarded-Proto", "http"), withHeader("Origin", "http://"+testHost))
	if r.StatusCode != 200 {
		t.Fatalf("http route exchange = %d", r.StatusCode)
	}
	for _, c := range r.Cookies() {
		if c.Name == CookieName && c.Secure {
			t.Fatal("cookie Secure on a plain-http route")
		}
	}
	// An https origin claim on an http route is cross-origin.
	r = e.do("GET", SessionPath, nil, withHeader("X-Forwarded-Proto", "http"))
	r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatalf("https origin on an http route = %d, want 403", r.StatusCode)
	}
}

// ---- finding 6: audit ------------------------------------------------

func TestAuditRecordsMoveDestinationAndDeniedRequests(t *testing.T) {
	e := newEnv(t, Limits{})
	cookie := e.login()
	e.dav.mu.Lock()
	e.dav.files["a.txt"] = []byte("hi")
	e.dav.mu.Unlock()
	r := e.do("MOVE", "/_homestead/files/f/a.txt", nil, withCookie(cookie),
		withHeader("Destination", "https://"+testHost+"/_homestead/files/f//sub/./b.txt"))
	r.Body.Close()
	r = e.do("MOVE", "/_homestead/files/f/a.txt", nil, withCookie(cookie),
		withHeader("Destination", "https://"+testHost+"/_homestead/files/f/sub/b.txt"))
	r.Body.Close()
	if r.StatusCode >= 300 && r.StatusCode != 400 {
		t.Fatalf("move = %d", r.StatusCode)
	}
	var dests []string
	for _, ev := range e.eventList() {
		if ev.Method == "MOVE" {
			dests = append(dests, ev.Dest)
		}
	}
	if len(dests) == 0 || dests[len(dests)-1] != "/sub/b.txt" {
		t.Fatalf("MOVE destinations audited: %v", dests)
	}

	// Denied before Serve: no cookie, forged cookie, bad launch code.
	r = e.do("PUT", "/_homestead/files/f/x.txt", strings.NewReader("x"))
	r.Body.Close()
	r = e.do("GET", "/_homestead/files/f/y.txt", nil, withCookie("forged-cookie-value"))
	r.Body.Close()
	r = e.do("POST", SessionPath, strings.NewReader(`{"code":"guessed-code-value"}`))
	r.Body.Close()
	e.auditor.Flush(true)
	denied := map[string]int{}
	for _, ev := range e.eventList() {
		if ev.Operator == "unauthenticated" {
			denied[ev.Method+" "+ev.Path] = ev.Status
		}
	}
	for _, k := range []string{"PUT /x.txt", "GET /y.txt", "POST " + SessionPath} {
		if denied[k] != 401 {
			t.Errorf("denied %q not audited with 401: %v", k, denied)
		}
	}
	all := fmt.Sprintf("%+v", e.eventList())
	for _, secret := range []string{"forged-cookie-value", "guessed-code-value", cookie} {
		if strings.Contains(all, secret) {
			t.Fatalf("audit holds %q", secret)
		}
	}
}

func TestAggregatedReadsKeepFirstFailure(t *testing.T) {
	clock := newClock()
	var out []Event
	a := &Auditor{Now: clock.Now, Sink: func(ev Event) { out = append(out, ev) }}
	for _, st := range []int{200, 404, 200, 500, 200} {
		a.Record(Event{Session: "s", Method: "GET", Path: "/f", Status: st})
	}
	a.Flush(true)
	if len(out) != 1 || out[0].Count != 5 || out[0].Status != 404 {
		t.Fatalf("aggregate = %+v, want first failure 404", out)
	}
	out = nil
	for _, st := range []int{200, 200} {
		a.Record(Event{Session: "s", Method: "GET", Path: "/g", Status: st})
	}
	a.Flush(true)
	if out[0].Status != 200 {
		t.Fatalf("all-success aggregate = %d", out[0].Status)
	}
}

// ---- finding 7: unlink ordering --------------------------------------

type failingSecrets struct {
	*memSecrets
	failDelete bool
}

func (f *failingSecrets) Delete(n string) error {
	if f.failDelete {
		return errors.New("disk on fire")
	}
	return f.memSecrets.Delete(n)
}

func TestUnlinkFailureKeepsLinkSoItCanBeRetried(t *testing.T) {
	e := newMgr(t)
	fs := &failingSecrets{memSecrets: e.secrets}
	e.m.Secrets = fs
	if _, _, err := link(t, e, PlanRequest{Action: ActionLink, BaseURL: e.baseURL, Account: "alice", Root: "Homestead", CreateAppPassword: true},
		ApproveRequest{AccountPassword: "account-login-password"}); err != nil {
		t.Fatal(err)
	}
	fs.failDelete = true
	p, _ := e.m.Plan(PlanRequest{Action: ActionUnlink}, "owner")
	if _, err := e.m.Approve(context.Background(), ApproveRequest{PlanID: p.ID}, "owner"); err == nil {
		t.Fatal("unlink succeeded though the secret could not be deleted")
	}
	st, err := e.m.Status()
	if err != nil || !st.Linked {
		t.Fatalf("the link row must survive a failed unlink: %+v %v", st, err)
	}
	fs.failDelete = false
	p, _ = e.m.Plan(PlanRequest{Action: ActionUnlink}, "owner")
	st, err = e.m.Approve(context.Background(), ApproveRequest{PlanID: p.ID}, "owner")
	if err != nil || st.Linked {
		t.Fatalf("retry: %+v %v", st, err)
	}
	if _, ok := e.secrets.m[SecretName]; ok {
		t.Fatal("credential orphaned after retry")
	}
}

// ---- finding 9: password change --------------------------------------

func TestRevokeOperatorEndsCraftSessionsAndCodes(t *testing.T) {
	e := newEnv(t, Limits{})
	cookie := e.login()
	mint(t, e, testApp, testHost)
	if n := e.ses.RevokeOperator("someone-else"); n != 0 {
		t.Fatalf("revoked %d sessions of another operator", n)
	}
	if n := e.ses.RevokeOperator("owner"); n != 2 {
		t.Fatalf("revoked %d, want the session and the pending code", n)
	}
	// The operator session itself still verifies (password change keeps it).
	r := e.do("GET", "/_homestead/files/f/", nil, withCookie(cookie))
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatalf("craft session after password change = %d, want 401", r.StatusCode)
	}
	if codes, sessions := e.ses.Counts(); codes != 0 || sessions != 0 {
		t.Fatalf("left over: %d codes, %d sessions", codes, sessions)
	}
}

// A linked address that is one of this appliance's edge routes is dialled
// on the local edge (127.0.0.1) at the URL's port, with the route name kept
// for TLS: the host need not resolve its own LAN names. Unlisted names
// still resolve normally.
func TestUpstreamDialsOwnEdgeRoutesOnLoopback(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Host", r.Host)
		w.WriteHeader(207)
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	hosts := map[string]bool{}
	up := &Upstream{Roots: func() []byte { return rootPEM }, EdgeHosts: func() map[string]bool { return hosts }}

	// httptest's certificate is valid for example.com, 127.0.0.1 and ::1,
	// so the edge-route name verifies only when TLS uses the name: use the
	// certificate's own DNS name as the route host.
	routed := "example.com"
	hosts[routed] = true
	resp, err := up.Client().Get("https://" + routed + ":" + port + "/")
	if err != nil {
		t.Fatalf("route host not dialled on loopback: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 207 || resp.Header.Get("X-Host") != routed+":"+port {
		t.Fatalf("status %d host %q", resp.StatusCode, resp.Header.Get("X-Host"))
	}

	delete(hosts, routed)
	if _, err := up.Client().Get("https://unlisted.invalid:" + port + "/"); err == nil {
		t.Fatal("a name outside the route table must resolve normally and fail here")
	}
}

func TestStatusErrHintsForEdgeAndTrustedDomains(t *testing.T) {
	n := &nextcloud{link: Link{BaseURL: "https://nextcloud.lan"}}
	if err := n.statusErr("the account's file space", 400); !strings.Contains(err.Error(), "trusted_domains") || !strings.Contains(err.Error(), "--value=nextcloud.lan") {
		t.Fatalf("400: %v", err)
	}
	if err := n.statusErr("x", 502); !strings.Contains(err.Error(), "homestead-edge") || !strings.Contains(err.Error(), "doctor") {
		t.Fatalf("502: %v", err)
	}
	if err := n.statusErr("x", 401); !strings.Contains(err.Error(), "rejected the credential") {
		t.Fatalf("401: %v", err)
	}
}
