package craftfiles

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func exchange(e *env, code string, opts ...reqOpt) *http.Response {
	return e.do("POST", SessionPath, strings.NewReader(`{"code":"`+code+`"}`), opts...)
}

func mint(t *testing.T, e *env, app, host string) string {
	t.Helper()
	code, _, err := e.ses.Mint("op-token", "owner", e.clock.Now().Add(24*time.Hour), app, host)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestExchangeValidSetsHardenedCookie(t *testing.T) {
	e := newEnv(t, Limits{})
	code := mint(t, e, testApp, testHost)
	r := exchange(e, code)
	body := readAll(t, r)
	if r.StatusCode != 200 {
		t.Fatalf("exchange = %d %s", r.StatusCode, body)
	}
	var c *http.Cookie
	for _, k := range r.Cookies() {
		if k.Name == CookieName {
			c = k
		}
	}
	if c == nil {
		t.Fatal("no craft cookie")
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != CookiePath || c.Domain != "" {
		t.Fatalf("cookie attributes wrong: %+v", c)
	}
	if strings.Contains(body, c.Value) || strings.Contains(body, code) {
		t.Fatalf("response body carries the cookie or the launch code: %s", body)
	}
	var info map[string]any
	_ = json.Unmarshal([]byte(body), &info)
	if info["app"] != testApp || info["root"] != "Homestead" {
		t.Fatalf("status body: %v", info)
	}
	// Over plain HTTP the cookie is not Secure (a local-only http route).
	code = mint(t, e, testApp, testHost)
	r = exchange(e, code, withHeader("X-Forwarded-Proto", "http"), withHeader("Origin", "http://"+testHost))
	for _, k := range r.Cookies() {
		if k.Name == CookieName && k.Secure {
			t.Fatal("cookie Secure over plain http")
		}
	}
}

func TestLaunchCodeIsOneTime(t *testing.T) {
	e := newEnv(t, Limits{})
	code := mint(t, e, testApp, testHost)
	if r := exchange(e, code); r.StatusCode != 200 {
		t.Fatalf("first use = %d", r.StatusCode)
	}
	r := exchange(e, code)
	if r.StatusCode != 401 {
		t.Fatalf("reused code = %d, want 401", r.StatusCode)
	}
	if len(r.Cookies()) != 0 {
		t.Fatal("reused code set a cookie")
	}
}

func TestLaunchCodeExpires(t *testing.T) {
	e := newEnv(t, Limits{})
	code := mint(t, e, testApp, testHost)
	e.clock.Advance(DefaultCodeTTL + time.Second)
	if r := exchange(e, code); r.StatusCode != 401 {
		t.Fatalf("expired code = %d, want 401", r.StatusCode)
	}
	if codes, _ := e.ses.Counts(); codes != 0 {
		t.Fatalf("expired code still held: %d", codes)
	}
}

func TestLaunchCodeBoundToAppAndHost(t *testing.T) {
	e := newEnv(t, Limits{})
	code := mint(t, e, testApp, "other.lan")
	if r := exchange(e, code); r.StatusCode != 401 {
		t.Fatalf("code for another host = %d, want 401", r.StatusCode)
	}
	code = mint(t, e, "cadcraft", testHost)
	if r := exchange(e, code); r.StatusCode != 401 {
		t.Fatalf("code for another app = %d, want 401", r.StatusCode)
	}
	// A bogus code and a missing one.
	if r := exchange(e, "nope"); r.StatusCode != 401 {
		t.Fatalf("bogus code = %d", r.StatusCode)
	}
	if r := e.do("POST", SessionPath, strings.NewReader(`{}`)); r.StatusCode != 400 {
		t.Fatalf("empty body = %d", r.StatusCode)
	}
}

func TestCookieBoundToCraftHostAndApp(t *testing.T) {
	e := newEnv(t, Limits{})
	cookie := e.login()
	// The same cookie replayed through another craft host or app is refused.
	r := e.do("GET", "/_homestead/files/f/", nil, withCookie(cookie),
		withHeader("X-Homestead-Craft-Host", "cadcraft.lan"), func(r *http.Request) { r.Host = "cadcraft.lan" },
		withHeader("Origin", "https://cadcraft.lan"))
	if r.StatusCode != 401 {
		t.Fatalf("cookie on another host = %d, want 401", r.StatusCode)
	}
	r = e.do("GET", "/_homestead/files/f/", nil, withCookie(cookie), withHeader("X-Homestead-Craft-App", "cadcraft"))
	if r.StatusCode != 401 {
		t.Fatalf("cookie for another app = %d, want 401", r.StatusCode)
	}
}

func TestUnknownOrMissingCookieIs401(t *testing.T) {
	e := newEnv(t, Limits{})
	for _, opts := range [][]reqOpt{nil, {withCookie("forged")}, {withHeader("Authorization", "Bearer op-token")}, {withHeader("Cookie", "homestead_session=op-token")}} {
		for _, p := range []string{"/_homestead/files/f/a", "/_homestead/files/session"} {
			if r := e.do("GET", p, nil, opts...); r.StatusCode != 401 {
				t.Errorf("GET %s with %d opts = %d, want 401", p, len(opts), r.StatusCode)
			}
		}
	}
	if n := len(e.dav.seen()); n != 0 {
		t.Fatalf("unauthenticated requests reached Nextcloud: %d", n)
	}
}

func TestSessionRevokedWithOperatorSession(t *testing.T) {
	e := newEnv(t, Limits{})
	cookie := e.login()
	if r := e.do("GET", "/_homestead/files/f/", nil, withCookie(cookie)); r.StatusCode == 401 {
		t.Fatal("fresh session refused")
	}
	e.op.revoke("op-token") // logout or password change
	r := e.do("GET", "/_homestead/files/f/", nil, withCookie(cookie))
	if r.StatusCode != 401 {
		t.Fatalf("after operator session ended = %d, want 401", r.StatusCode)
	}
	if _, sessions := e.ses.Counts(); sessions != 0 {
		t.Fatal("revoked craft session still held")
	}
	// A code minted before logout cannot be exchanged after it either.
	e.op.mu.Lock()
	e.op.expires["op-token"] = e.clock.Now().Add(time.Hour)
	e.op.mu.Unlock()
	code := mint(t, e, testApp, testHost)
	e.op.revoke("op-token")
	if r := exchange(e, code); r.StatusCode != 401 {
		t.Fatalf("exchange after logout = %d, want 401", r.StatusCode)
	}
}

func TestSessionNeverOutlivesOperatorSession(t *testing.T) {
	e := newEnv(t, Limits{})
	e.op.mu.Lock()
	e.op.expires["op-token"] = e.clock.Now().Add(30 * time.Minute)
	e.op.mu.Unlock()
	code, _, _ := e.ses.Mint("op-token", "owner", e.clock.Now().Add(30*time.Minute), testApp, testHost)
	r := exchange(e, code)
	var c *http.Cookie
	for _, k := range r.Cookies() {
		if k.Name == CookieName {
			c = k
		}
	}
	if c == nil {
		t.Fatal("no cookie")
	}
	if want := e.clock.Now().Add(30 * time.Minute); c.Expires.After(want.Add(time.Second)) {
		t.Fatalf("cookie expires %v, after the operator session %v", c.Expires, want)
	}
	e.clock.Advance(31 * time.Minute)
	e.op.revoke("op-token") // the operator session expired too
	if r := e.do("GET", "/_homestead/files/f/", nil, withCookie(c.Value)); r.StatusCode != 401 {
		t.Fatalf("after expiry = %d, want 401", r.StatusCode)
	}
}

func TestSessionTTLExpiry(t *testing.T) {
	e := newEnv(t, Limits{})
	e.op.mu.Lock()
	e.op.expires["op-token"] = e.clock.Now().Add(72 * time.Hour)
	e.op.mu.Unlock()
	cookie := e.login()
	e.clock.Advance(DefaultSessionTTL - time.Minute)
	if r := e.do("GET", "/_homestead/files/f/", nil, withCookie(cookie)); r.StatusCode == 401 {
		t.Fatal("session expired early")
	}
	e.clock.Advance(2 * time.Minute)
	if r := e.do("GET", "/_homestead/files/f/", nil, withCookie(cookie)); r.StatusCode != 401 {
		t.Fatalf("expired session = %d, want 401", r.StatusCode)
	}
}

func TestOperatorStoreFailureFailsClosed(t *testing.T) {
	e := newEnv(t, Limits{})
	cookie := e.login()
	e.op.mu.Lock()
	e.op.down = true
	e.op.mu.Unlock()
	if r := e.do("GET", "/_homestead/files/f/", nil, withCookie(cookie)); r.StatusCode != 503 {
		t.Fatalf("verifier down = %d, want 503", r.StatusCode)
	}
	if n := len(e.dav.seen()); n != 0 {
		t.Fatal("request served while the operator session could not be checked")
	}
}

func TestEndSession(t *testing.T) {
	e := newEnv(t, Limits{})
	cookie := e.login()
	if r := e.do("DELETE", SessionPath, nil, withCookie(cookie)); r.StatusCode != 204 {
		t.Fatalf("end = %d", r.StatusCode)
	}
	if r := e.do("GET", "/_homestead/files/f/", nil, withCookie(cookie)); r.StatusCode != 401 {
		t.Fatalf("ended session = %d", r.StatusCode)
	}
}

func TestUnlinkedOrLocalOnlyAppsRefuse(t *testing.T) {
	e := newEnv(t, Limits{})
	cookie := e.login()
	e.local = true
	if r := e.do("GET", "/_homestead/files/f/", nil, withCookie(cookie)); r.StatusCode != 409 {
		t.Fatalf("local-only app = %d, want 409", r.StatusCode)
	}
	code := mint(t, e, testApp, testHost)
	if r := exchange(e, code); r.StatusCode != 409 {
		t.Fatalf("exchange for local-only app = %d, want 409", r.StatusCode)
	}
}

func TestCrossOriginIsRefusedAndNoCORS(t *testing.T) {
	e := newEnv(t, Limits{})
	cookie := e.login()
	check := func(name string, r *http.Response, want int) {
		t.Helper()
		for k := range r.Header {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				t.Errorf("%s: CORS header %s set", name, k)
			}
		}
		if r.StatusCode != want {
			t.Errorf("%s = %d, want %d", name, r.StatusCode, want)
		}
	}
	check("preflight", e.do("OPTIONS", "/_homestead/files/f/a", nil, withCookie(cookie),
		withHeader("Origin", "https://evil.example"), withHeader("Access-Control-Request-Method", "PUT")), 403)
	check("preflight same origin", e.do("OPTIONS", "/_homestead/files/f/a", nil, withCookie(cookie)), 403)
	check("cross-origin GET", e.do("GET", "/_homestead/files/f/a", nil, withCookie(cookie), withHeader("Origin", "https://evil.example")), 403)
	check("cross-origin PUT", e.do("PUT", "/_homestead/files/f/a", strings.NewReader("x"), withCookie(cookie), withHeader("Origin", "https://evil.example")), 403)
	check("scheme downgrade origin", e.do("GET", "/_homestead/files/f/a", nil, withCookie(cookie), withHeader("Origin", "http://"+testHost)), 403)
	check("subdomain origin", e.do("GET", "/_homestead/files/f/a", nil, withCookie(cookie), withHeader("Origin", "https://x."+testHost)), 403)
	check("cross-site fetch", e.do("GET", "/_homestead/files/f/a", nil, withCookie(cookie), withHeader("Sec-Fetch-Site", "cross-site")), 403)
	check("same-site fetch", e.do("GET", "/_homestead/files/f/a", nil, withCookie(cookie), withHeader("Sec-Fetch-Site", "same-site")), 403)
	check("write without origin", e.do("PUT", "/_homestead/files/f/a", strings.NewReader("x"), withCookie(cookie), withoutHeader("Origin"), withoutHeader("Sec-Fetch-Site")), 403)
	check("exchange cross-origin", e.do("POST", SessionPath, strings.NewReader(`{"code":"x"}`), withHeader("Origin", "https://evil.example")), 403)
	check("same-origin works", e.do("PROPFIND", "/_homestead/files/f/", nil, withCookie(cookie)), 207)
	if n := len(e.dav.seen()); n != 1 {
		t.Fatalf("cross-origin requests reached Nextcloud: %d upstream requests", n)
	}
}

func TestHostAndEdgeIdentity(t *testing.T) {
	e := newEnv(t, Limits{})
	cookie := e.login()
	if r := e.do("GET", "/_homestead/files/f/", nil, withCookie(cookie), withoutHeader("X-Homestead-Craft-Host")); r.StatusCode != 400 {
		t.Fatalf("no edge host header = %d, want 400", r.StatusCode)
	}
	if r := e.do("GET", "/_homestead/files/f/", nil, withCookie(cookie), withoutHeader("X-Homestead-Craft-App")); r.StatusCode != 400 {
		t.Fatalf("no edge app header = %d, want 400", r.StatusCode)
	}
	if r := e.do("GET", "/_homestead/files/f/", nil, withCookie(cookie), func(r *http.Request) { r.Host = "evil.example" }); r.StatusCode != 421 {
		t.Fatalf("Host not the craft host = %d, want 421", r.StatusCode)
	}
	if r := e.do("PROPFIND", "/_homestead/files/f/", nil, withCookie(cookie), func(r *http.Request) { r.Host = testHost + ":8443" }); r.StatusCode != 207 {
		t.Fatalf("host with port = %d, want 207", r.StatusCode)
	}
	// Only the craft paths exist on the listener.
	for _, p := range []string{"/", "/api/v1/status", "/_homestead/open.html", "/_homestead/files", "/_homestead/files/other", "/healthz"} {
		if r := e.do("GET", p, nil, withCookie(cookie)); r.StatusCode != 404 && r.StatusCode != 301 {
			t.Errorf("GET %s = %d, want 404", p, r.StatusCode)
		}
	}
}

func TestNoSecretsInAuditOrErrorBodies(t *testing.T) {
	e := newEnv(t, Limits{MaxUpload: 64})
	code := mint(t, e, testApp, testHost)
	r := exchange(e, code)
	var cookie string
	for _, k := range r.Cookies() {
		cookie = k.Value
	}
	e.dav.mu.Lock()
	e.dav.files["a.txt"] = []byte("TOP-SECRET-DOCUMENT-CONTENT")
	e.dav.mu.Unlock()
	var bodies []string
	collect := func(r *http.Response) { bodies = append(bodies, fmt.Sprint(r.Header), readAll(t, r)) }
	collect(e.do("GET", "/_homestead/files/f/a.txt", nil, withCookie(cookie)))
	collect(e.do("PUT", "/_homestead/files/f/b.txt", strings.NewReader("UPLOADED-SECRET-CONTENT"), withCookie(cookie)))
	collect(e.do("PUT", "/_homestead/files/f/c.txt", strings.NewReader(strings.Repeat("Z", 200)), withCookie(cookie)))
	collect(e.do("GET", "/_homestead/files/f/missing", nil, withCookie(cookie)))
	collect(e.do("GET", "/_homestead/files/f/a.txt", nil, withCookie("forged")))
	collect(exchange(e, code)) // reused
	e.dav.mu.Lock()
	e.dav.revoked = true
	e.dav.mu.Unlock()
	collect(e.do("GET", "/_homestead/files/f/a.txt", nil, withCookie(cookie)))
	e.auditor.Flush(true)
	events := e.eventList()
	if len(events) == 0 {
		t.Fatal("no audit events")
	}
	all := fmt.Sprintf("%+v", events)
	for _, secret := range []string{testPassword, cookie, code, "TOP-SECRET-DOCUMENT-CONTENT", "UPLOADED-SECRET-CONTENT", "op-token"} {
		if strings.Contains(all, secret) {
			t.Fatalf("audit events contain %q: %s", secret, all)
		}
		for _, b := range bodies {
			if strings.Contains(b, secret) && secret != "TOP-SECRET-DOCUMENT-CONTENT" {
				t.Fatalf("a response contains %q: %s", secret, b)
			}
		}
	}
	for _, ev := range events {
		if ev.Operator == "unauthenticated" && ev.Session == "" && (ev.Status == 401 || ev.Status == 409) && ev.Path != "" {
			continue // refused before a session resolved
		}
		if ev.Session == "" || ev.Operator != "owner" || ev.App != testApp || ev.Method == "" || ev.Path == "" || ev.Status == 0 {
			t.Errorf("incomplete audit event: %+v", ev)
		}
	}
}

func TestAuditRecordsWritesAndAggregatesReads(t *testing.T) {
	e := newEnv(t, Limits{})
	cookie := e.login()
	e.dav.mu.Lock()
	e.dav.files["doc.txt"] = []byte("12345")
	e.dav.mu.Unlock()
	for i := 0; i < 5; i++ {
		e.do("GET", "/_homestead/files/f/doc.txt", nil, withCookie(cookie)).Body.Close()
	}
	e.do("GET", "/_homestead/files/f/other.txt", nil, withCookie(cookie)).Body.Close()
	e.do("PUT", "/_homestead/files/f/new.txt", strings.NewReader("abcdefgh"), withCookie(cookie)).Body.Close()
	e.do("DELETE", "/_homestead/files/f/new.txt", nil, withCookie(cookie)).Body.Close()
	// Writes are audited immediately; reads wait for the minute to end.
	var methods []string
	for _, ev := range e.eventList() {
		methods = append(methods, ev.Method)
	}
	if strings.Join(methods, ",") != "PUT,DELETE" {
		t.Fatalf("before flush: %v", methods)
	}
	e.auditor.Flush(false)
	if len(e.eventList()) != 2 {
		t.Fatal("reads flushed before their minute ended")
	}
	e.clock.Advance(time.Minute)
	e.auditor.Flush(false)
	var doc *Event
	reads := 0
	for _, ev := range e.eventList() {
		if ev.Method == "GET" {
			reads++
			if ev.Path == "/doc.txt" {
				c := ev
				doc = &c
			}
		}
	}
	if reads != 2 || doc == nil || doc.Count != 5 || doc.Bytes != 25 {
		t.Fatalf("read aggregation: %d GET events, doc=%+v", reads, doc)
	}
	for _, ev := range e.eventList() {
		if ev.Method == "PUT" && (ev.Bytes != 8 || ev.Status != 201 || ev.Path != "/new.txt") {
			t.Fatalf("PUT event: %+v", ev)
		}
	}
}
