package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/docker"
	"homestead/internal/doctor"
	"homestead/internal/mdns"
	"homestead/internal/store"
)

const (
	dashLAN   = "homestead.lan"
	dashLocal = "homestead.local"
)

func (a *enforcedApp) socketDo(method, path, body string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	a.srv.SetDashboardNames(func() []string { return []string{dashLAN, dashLocal} })
	h := a.srv.dashboardFront(a.srv.Handler())
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = dashLocal
	req.Header.Set("X-Forwarded-For", "192.168.1.50")
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestDashboardSocketForwardedAddress(t *testing.T) {
	a := newEnforcedApp(t)
	for name, xff := range map[string][]string{
		"missing":   nil,
		"two":       {"192.168.1.5", "192.168.1.6"},
		"list":      {"192.168.1.5, 10.0.0.1"},
		"invalid":   {"not-an-ip"},
		"loopback":  {"127.0.0.1"},
		"loopback6": {"::1"},
		"mapped":    {"::ffff:127.0.0.1"},
		"any":       {"0.0.0.0"},
	} {
		rec := a.socketDo("GET", "/api/v1/claim/status", "", func(r *http.Request) {
			r.Header.Del("X-Forwarded-For")
			for _, v := range xff {
				r.Header.Add("X-Forwarded-For", v)
			}
		})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d, want 400", name, rec.Code)
		}
	}
	if rec := a.socketDo("GET", "/api/v1/claim/status", "", nil); rec.Code == http.StatusBadRequest || rec.Code == http.StatusMisdirectedRequest {
		t.Errorf("a valid request was refused: %d %s", rec.Code, rec.Body.String())
	}
}

func TestForgedForwardedForIsIgnoredOnTCP(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/claim/status", nil)
	req.RemoteAddr = "203.0.113.9:4000"
	req.Header.Set("X-Forwarded-For", "192.168.1.50")
	if _, ok := edgeClientFrom(req.Context()); ok {
		t.Fatal("tcp request carries an edge client")
	}
	if got := requestConnection(req, 8080).SourceIP; got != "203.0.113.9" {
		t.Fatalf("tcp source = %q, want the connection address", got)
	}
	if got := viaEdge(req, "d"); got != "d" {
		t.Fatalf("tcp viaEdge = %q", got)
	}
}

func TestDashboardSocketUsesForwardedAddress(t *testing.T) {
	var got *http.Request
	srv := New("127.0.0.1:0")
	srv.SetDashboardNames(func() []string { return []string{dashLAN, dashLocal} })
	h := srv.dashboardFront(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r }))
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = dashLAN
	req.RemoteAddr = "@"
	req.Header.Set("X-Forwarded-For", "192.168.1.77")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got == nil {
		t.Fatal("handler not reached")
	}
	if c, ok := edgeClientFrom(got.Context()); !ok || c != "192.168.1.77" {
		t.Fatalf("edge client = %q %v", c, ok)
	}
	if !strings.HasPrefix(got.RemoteAddr, "192.168.1.77:") {
		t.Fatalf("RemoteAddr = %q", got.RemoteAddr)
	}
	if !strings.Contains(viaEdge(got, "x"), "via edge from 192.168.1.77") {
		t.Fatalf("viaEdge = %q", viaEdge(got, "x"))
	}
	if got := requestConnection(got, 8080).SourceIP; got != "192.168.1.77" {
		t.Fatalf("connection source = %q", got)
	}
	if got.Header.Get("X-Forwarded-Proto") != "https" {
		t.Fatal("proto not https")
	}
}

func TestDashboardSocketHost(t *testing.T) {
	a := newEnforcedApp(t)
	for _, host := range []string{dashLAN, dashLocal, "HOMESTEAD.LOCAL", dashLAN + ":443"} {
		rec := a.socketDo("GET", "/api/v1/claim/status", "", func(r *http.Request) { r.Host = host })
		if rec.Code == http.StatusMisdirectedRequest {
			t.Errorf("host %q refused", host)
		}
	}
	for _, host := range []string{"evil.example", "192.168.1.2", dashLAN + ":8080", ""} {
		rec := a.socketDo("GET", "/api/v1/claim/status", "", func(r *http.Request) { r.Host = host })
		if rec.Code != http.StatusMisdirectedRequest {
			t.Errorf("host %q = %d, want 421", host, rec.Code)
		}
	}
}

func TestDashboardSocketRouteClasses(t *testing.T) {
	a := newEnforcedApp(t)
	// Before claim: public and claim-window routes behave as on TCP.
	for _, p := range []string{"/api/v1/claim/status", "/api/v1/health"} {
		tcp := a.do("GET", p, "", nil).Code
		if sock := a.socketDo("GET", p, "", nil).Code; sock != tcp {
			t.Errorf("%s: socket %d != tcp %d", p, sock, tcp)
		}
	}
	for _, p := range []string{"/api/v1/config", "/api/v1/audit"} {
		if sock, tcp := a.socketDo("GET", p, "", nil).Code, a.do("GET", p, "", nil).Code; sock != tcp || !rejected(sock) {
			t.Errorf("%s before claim: socket %d, tcp %d", p, sock, tcp)
		}
	}
	rec := a.setup(t)
	token := jsonField(t, rec, "token")
	cookie := cookieByName(rec, SessionCookieName)

	if code := a.socketDo("GET", "/api/v1/config", "", nil).Code; code != http.StatusUnauthorized {
		t.Errorf("session route without credentials = %d", code)
	}
	if code := a.socketDo("GET", "/api/v1/config", "", bearer(token)).Code; code != http.StatusOK {
		t.Errorf("session route with bearer = %d", code)
	}
	withCookie := func(origin string) func(*http.Request) {
		return func(r *http.Request) {
			if cookie != nil {
				r.AddCookie(cookie)
			}
			if origin != "" {
				r.Header.Set("Origin", origin)
			}
		}
	}
	if code := a.socketDo("GET", "/api/v1/config", "", withCookie("")).Code; code != http.StatusOK {
		t.Errorf("cookie GET = %d", code)
	}
	// Cookie mutation: no Origin refused, wrong Origin refused, right Origin passes the front.
	mut := "/api/v1/dashboard/address"
	if code := a.socketDo("PUT", mut, `{"name":"x.lan"}`, withCookie("")).Code; code != http.StatusForbidden {
		t.Errorf("cookie mutation without Origin = %d", code)
	}
	if code := a.socketDo("PUT", mut, `{"name":"x.lan"}`, withCookie("https://evil.example")).Code; code != http.StatusForbidden {
		t.Errorf("cookie mutation wrong Origin = %d", code)
	}
	if code := a.socketDo("PUT", mut, `{"name":"x.lan"}`, withCookie("http://"+dashLocal)).Code; code != http.StatusForbidden {
		t.Errorf("cookie mutation http Origin = %d", code)
	}
	for _, o := range []string{"https://" + dashLAN, "https://" + dashLocal} {
		body := a.socketDo("PUT", mut, `{"name":"x.lan"}`, withCookie(o)).Body.String()
		if strings.Contains(body, "origin not allowed") || strings.Contains(body, "Origin header required") {
			t.Errorf("origin %s refused by the front: %s", o, body)
		}
	}
	// Bearer without cookie needs no Origin, and a mutation reaches the handler.
	if code := a.socketDo("PUT", mut, `{"name":"x.example.com"}`, bearer(token)).Code; code != http.StatusUnprocessableEntity {
		t.Errorf("bearer mutation without Origin = %d, want handler 422", code)
	}
	// Cookie and bearer together are refused.
	both := func(r *http.Request) {
		bearer(token)(r)
		if cookie != nil {
			r.AddCookie(cookie)
		}
	}
	if code := a.socketDo("GET", "/api/v1/config", "", both).Code; code != http.StatusBadRequest {
		t.Errorf("cookie plus bearer = %d", code)
	}
}

func TestDashboardSocketDoesNotServeRecoveryOrCraft(t *testing.T) {
	a := newEnforcedApp(t)
	token := jsonField(t, a.setup(t), "token")
	for _, p := range []string{"/api/v1/recovery/reset", "/_homestead/craft/files/x"} {
		if code := a.socketDo("POST", p, "{}", bearer(token)).Code; code != http.StatusNotFound {
			t.Errorf("%s over the socket = %d, want 404", p, code)
		}
		if code := a.do("POST", p, "{}", bearer(token)).Code; code != http.StatusNotFound {
			t.Errorf("%s over TCP = %d, want 404", p, code)
		}
	}
}

// The SSO rate limiter keys on the forwarded client address for socket
// requests: one noisy LAN device cannot lock out another, and the same
// device is still limited.
func TestSSOCallbackRateLimitKeysOnForwardedAddress(t *testing.T) {
	a := newSSOApp(t)
	a.configure(t)
	a.linkOwner(t)
	a.srv.SetDashboardNames(func() []string { return []string{dashLAN, dashLocal} })
	h := a.srv.dashboardFront(a.srv.Handler())
	call := func(ip string) string {
		req := httptest.NewRequest("GET", "/api/v1/sso/callback?state=x&code=y", nil)
		req.Host = dashLocal
		req.Header.Set("X-Forwarded-For", ip)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Header().Get("Location")
	}
	limited := func(loc string) bool { return strings.Contains(loc, "sso_error=rate-limited") }
	for i := 0; i < 31; i++ {
		call("192.168.1.10")
	}
	if !limited(call("192.168.1.10")) {
		t.Fatal("the same forwarded address was not rate limited")
	}
	if loc := call("192.168.1.11"); limited(loc) {
		t.Fatalf("a different forwarded address shares the bucket: %s", loc)
	}
}

func TestDashboardAddressAPI(t *testing.T) {
	a := newEnforcedApp(t)
	token := jsonField(t, a.setup(t), "token")
	rec := a.do("GET", "/api/v1/dashboard/address", "", bearer(token))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body.String())
	}
	for body, want := range map[string]int{
		`{"name":"homestead.example.com"}`:     http.StatusUnprocessableEntity,
		`{"name":"1.2.3.4"}`:                   http.StatusUnprocessableEntity,
		`{"name":"x.lan","exposure":"public"}`: http.StatusUnprocessableEntity,
		`{"name":"Bad Name.lan"}`:              http.StatusUnprocessableEntity,
		`not json`:                             http.StatusBadRequest,
	} {
		if code := a.do("PUT", "/api/v1/dashboard/address", body, bearer(token)).Code; code != want {
			t.Errorf("PUT %s = %d, want %d", body, code, want)
		}
	}
	rec = a.do("PUT", "/api/v1/dashboard/address", `{"name":"x.lan","exposure":"public"}`, bearer(token))
	if !strings.Contains(rec.Body.String(), "tunnel") {
		t.Errorf("public refusal does not name the tunnel: %s", rec.Body.String())
	}
}

func TestBuildDashboardRoute(t *testing.T) {
	r, err := BuildDashboardRoute("Homestead.LAN", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Host != dashLAN || !r.Dashboard || r.TLS != docker.TLSInternal || r.Exposure != docker.ExposureLocal {
		t.Fatalf("route = %+v", r)
	}
	if len(r.Aliases) != 1 || r.Aliases[0] != dashLocal {
		t.Fatalf("aliases = %v", r.Aliases)
	}
	r, err = BuildDashboardRoute("homestead.local", docker.ExposureOverlay)
	if err != nil || len(r.Aliases) != 0 {
		t.Fatalf("local name: %+v %v", r, err)
	}
	for _, bad := range []string{"", "10.0.0.1", "box.example.com", "a..lan"} {
		if _, err := BuildDashboardRoute(bad, ""); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := BuildDashboardRoute("x.lan", docker.ExposurePublic); err == nil {
		t.Error("public accepted")
	}
}

func TestDashboardRoutePublicRefusedInEdgeRoutes(t *testing.T) {
	srv := edgeTestServer(t)
	route := map[string]any{"Host": "homestead.lan", "Dashboard": true, "Exposure": "public", "TLS": "internal"}
	rec := serveRequest(t, srv, http.MethodPost, "/api/v1/edge/routes", map[string]any{"routes": []any{route}})
	if rec.Code < 400 {
		t.Fatalf("public dashboard accepted: %d", rec.Code)
	}
}

func TestPreserveDashboardRoute(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), store.DBFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dash, _ := BuildDashboardRoute(dashLAN, "")
	app := docker.Route{Host: "app.lan", Target: docker.Target{Host: "app", Port: 80}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}
	if err := saveEdgeRoutes(st, docker.RouteSet{Routes: []docker.Route{dash, app}}); err != nil {
		t.Fatal(err)
	}
	got := preserveDashboardRoute(st, []docker.Route{app})
	if len(got) != 2 || !got[1].Dashboard {
		t.Fatalf("dashboard route dropped: %+v", got)
	}
	other, _ := BuildDashboardRoute("other.lan", "")
	got = preserveDashboardRoute(st, []docker.Route{app, other})
	if len(got) != 2 || got[1].Host != "other.lan" {
		t.Fatalf("replacement not honoured: %+v", got)
	}
}

func TestSSODefaultsIncludeBothDashboardNames(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), store.DBFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dash, _ := BuildDashboardRoute(dashLAN, "")
	if err := saveEdgeRoutes(st, docker.RouteSet{Routes: []docker.Route{dash}}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(ssoDefaults(st, 8080).Origins, " ")
	for _, want := range []string{"https://" + dashLAN, "https://" + dashLocal} {
		if !strings.Contains(joined, want) {
			t.Errorf("origins %q lack %s", joined, want)
		}
	}
}

func TestValidateDashboardSocketPath(t *testing.T) {
	if err := ValidateDashboardSocketPath(docker.DashboardSocketPath); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"/tmp/x.sock", "/run/homestead-dashboard", "/etc/passwd"} {
		if ValidateDashboardSocketPath(bad) == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestDashboardSocketDoctorCheck(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), store.DBFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mounted := func(v bool) func(context.Context) (bool, error) {
		return func(context.Context) (bool, error) { return v, nil }
	}
	sock := filepath.Join(t.TempDir(), "d.sock")
	if c := dashboardSocketDoctorCheck(st, mounted(true), sock)()[0]; c.Status != doctor.StatusWarn {
		t.Errorf("no route: %+v", c)
	}
	other := docker.Route{Host: docker.Hostname(dashLAN), Target: docker.Target{Host: "app", Port: 80}, Exposure: docker.ExposureLocal, TLS: docker.TLSInternal}
	if err := saveEdgeRoutes(st, docker.RouteSet{Routes: []docker.Route{other}}); err != nil {
		t.Fatal(err)
	}
	if c := dashboardSocketDoctorCheck(st, mounted(true), sock)()[0]; c.Status != doctor.StatusWarn ||
		!strings.Contains(c.Detail, dashLAN) || !strings.Contains(c.Detail, "dashboard address set") {
		t.Errorf("name conflict: %+v", c)
	}
	dash, _ := BuildDashboardRoute(dashLAN, "")
	if err := saveEdgeRoutes(st, docker.RouteSet{Routes: []docker.Route{dash}}); err != nil {
		t.Fatal(err)
	}
	if c := dashboardSocketDoctorCheck(st, mounted(true), sock)()[0]; c.Status != doctor.StatusWarn || !strings.Contains(c.Detail, "missing") {
		t.Errorf("no socket: %+v", c)
	}
	if err := writeFile(sock); err != nil {
		t.Fatal(err)
	}
	if c := dashboardSocketDoctorCheck(st, mounted(false), sock)()[0]; c.Status != doctor.StatusWarn || !strings.Contains(c.Detail, "redeploy") {
		t.Errorf("no mount: %+v", c)
	}
	if c := dashboardSocketDoctorCheck(st, mounted(true), sock)()[0]; c.Status != doctor.StatusOK {
		t.Errorf("healthy: %+v", c)
	}
}

func TestMDNSDoctorCheck(t *testing.T) {
	check := func(s mdns.State) doctor.Check {
		return mdnsDoctorCheck(func() mdns.State { return s })()[0]
	}
	if c := check(mdns.State{Known: true, Running: true}); c.Status != doctor.StatusOK {
		t.Errorf("running: %+v", c)
	}
	if c := check(mdns.State{Known: true, Err: "address already in use"}); c.Status != doctor.StatusWarn || !strings.Contains(c.Detail, "5353") {
		t.Errorf("port taken: %+v", c)
	}
	if c := check(mdns.State{}); c.Status != doctor.StatusWarn {
		t.Errorf("unknown: %+v", c)
	}
	if c := check(mdns.State{Known: true}); c.Status != doctor.StatusWarn {
		t.Errorf("stopped: %+v", c)
	}
}

func writeFile(p string) error { return os.WriteFile(p, nil, 0o600) }
