package everything

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// headerIdentity takes the scope from test headers so one handler (one
// limiter) can serve several people and apps.
type headerIdentity struct{}

func (headerIdentity) Authenticate(r *http.Request) (Scope, error) {
	return Scope{App: r.Header.Get("X-T-App"), Subject: r.Header.Get("X-T-Sub")}, nil
}
func (headerIdentity) CheckCSRF(r *http.Request) bool { return r.Header.Get("X-T-CSRF") == "ok" }

type netEnv struct {
	t     *testing.T
	store *Store
	h     http.Handler
	srv   *httptest.Server
	host  string // fake public name that dials srv
	port  string
	dials atomic.Int32
}

func newNetEnv(t *testing.T, on bool, upstream http.Handler) *netEnv {
	t.Helper()
	st, err := OpenStore(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	env := &netEnv{t: t, store: st, srv: srv, host: "api.example.test", port: u.Port()}
	res := &fakeResolver{answers: map[string][][]string{
		"api.example.test":     {{"93.184.216.34"}},
		"private.example.test": {{"10.0.0.5"}},
		"mixed.example.test":   {{"93.184.216.34", "192.168.1.9"}},
	}}
	st.SetNet(on, &NetFence{Resolver: res, Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
		env.dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, "tcp", srv.Listener.Addr().String())
	}})
	h, err := NewHandler(st, headerIdentity{}, []string{"https://home.example"})
	if err != nil {
		t.Fatal(err)
	}
	env.h = h
	return env
}

type netReply struct {
	Status     int               `json:"status"`
	StatusText string            `json:"statusText"`
	Headers    map[string]string `json:"headers"`
	Body       []byte            `json:"body"`
	Error      string            `json:"error"`
}

func (e *netEnv) call(app, sub string, body map[string]any) (int, netReply) {
	e.t.Helper()
	body["operation"] = "net.fetch"
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "https://home.example/bridge", bytes.NewReader(raw))
	req.Header.Set("Origin", "https://home.example")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-T-App", app)
	req.Header.Set("X-T-Sub", sub)
	req.Header.Set("X-T-CSRF", "ok")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var rep netReply
	_ = json.Unmarshal(rec.Body.Bytes(), &rep)
	return rec.Code, rep
}

func (e *netEnv) fetch(sub string, u string, extra map[string]any) (int, netReply) {
	b := map[string]any{"url": u}
	for k, v := range extra {
		b[k] = v
	}
	return e.call("api-client", sub, b)
}

func (e *netEnv) url(path string) string { return "http://" + e.host + ":" + e.port + path }

func echoUpstream() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redir-private":
			http.Redirect(w, r, "http://127.0.0.1:80/", http.StatusFound)
			return
		case "/redir-ok":
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		case "/big":
			w.Write(bytes.Repeat([]byte("a"), NetMaxResponseBody+1))
			return
		case "/exact":
			w.Write(bytes.Repeat([]byte("a"), NetMaxResponseBody))
			return
		}
		b := new(bytes.Buffer)
		b.ReadFrom(r.Body)
		w.Header().Set("Set-Cookie", "sid=upstream-secret")
		w.Header().Set("X-Secret-Header", "nope")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Remaining", "9")
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"method": r.Method, "path": r.URL.RequestURI(), "headers": r.Header, "host": r.Host, "body": b.String()})
	})
}

func TestNetFetchDisabledByDefault(t *testing.T) {
	e := newNetEnv(t, false, echoUpstream())
	code, rep := e.fetch("alice", e.url("/"), nil)
	if code != 403 || rep.Error != "net_disabled" || e.dials.Load() != 0 {
		t.Fatalf("%d %+v dials=%d", code, rep, e.dials.Load())
	}
	if rows, _ := e.store.NetLog(context.Background(), "api-client", 10); len(rows) != 0 {
		t.Fatal("disabled request logged")
	}
}

func TestNetFetchRoundTripHeadersAndLog(t *testing.T) {
	e := newNetEnv(t, true, echoUpstream())
	code, rep := e.fetch("alice-sub", e.url("/v1/items?key=SECRETKEY&x=1"), map[string]any{
		"method": "post",
		"headers": map[string]string{
			"Cookie": "bridge=SESSIONSECRET", "Host": "evil.test", "X-Forwarded-For": "1.2.3.4", "Forwarded": "for=1",
			"Proxy-Authorization": "p", "Proxy-Foo": "p", "Connection": "close", "Keep-Alive": "1", "Upgrade": "h2c",
			"Authorization": "Bearer APPTOKEN", "X-Custom": "yes", "User-Agent": "mine", "Accept-Encoding": "br",
			"Content-Type": "text/plain",
		},
		"body": []byte("REQBODY"),
	})
	if code != 200 || rep.Status != 201 || rep.StatusText != "Created" {
		t.Fatalf("%d %+v", code, rep)
	}
	var echo struct {
		Method, Path, Host, Body string
		Headers                  map[string][]string
	}
	if err := json.Unmarshal(rep.Body, &echo); err != nil {
		t.Fatal(err)
	}
	if echo.Method != "POST" || echo.Body != "REQBODY" || echo.Path != "/v1/items?key=SECRETKEY&x=1" {
		t.Fatalf("%+v", echo)
	}
	if strings.HasPrefix(echo.Host, "evil") || !strings.HasPrefix(echo.Host, e.host) {
		t.Fatalf("host %q", echo.Host)
	}
	for _, h := range []string{"Cookie", "X-Forwarded-For", "Forwarded", "Proxy-Authorization", "Proxy-Foo", "Keep-Alive", "Upgrade"} {
		if _, ok := echo.Headers[h]; ok {
			t.Fatalf("forwarded %s", h)
		}
	}
	if echo.Headers["Authorization"][0] != "Bearer APPTOKEN" || echo.Headers["X-Custom"][0] != "yes" {
		t.Fatalf("app headers lost: %v", echo.Headers)
	}
	if ua := echo.Headers["User-Agent"][0]; ua != netUserAgent {
		t.Fatalf("ua %q", ua)
	}
	if ae := echo.Headers["Accept-Encoding"]; len(ae) > 0 && ae[0] == "br" {
		t.Fatal("app Accept-Encoding forwarded")
	}
	if rep.Headers["content-type"] != "application/json" || rep.Headers["x-ratelimit-remaining"] != "9" {
		t.Fatalf("resp headers %v", rep.Headers)
	}
	for k := range rep.Headers {
		if k == "set-cookie" || k == "x-secret-header" {
			t.Fatalf("leaked response header %s", k)
		}
	}
	rows, err := e.store.NetLog(context.Background(), "api-client", 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("%v %v", rows, err)
	}
	r := rows[0]
	if r.Method != "POST" || r.Scheme != "http" || r.Host != e.host || fmt.Sprint(r.Port) != e.port || r.Path != "/v1/items" ||
		r.Status != 201 || r.BytesOut != 7 || r.BytesIn != int64(len(rep.Body)) || r.Class != "ok" || r.Addr != "93.184.216.34" || r.Time == 0 {
		t.Fatalf("%+v", r)
	}
	p, _ := e.store.personUUID(context.Background(), "api-client", "alice-sub")
	if r.Person != p || strings.Contains(r.Person, "alice") {
		t.Fatalf("person %q", r.Person)
	}
	dumpNoSecrets(t, e.store)
}

// dumpNoSecrets scans the whole database file text for things that must
// never be stored in the net log.
func dumpNoSecrets(t *testing.T, st *Store) {
	t.Helper()
	rows, err := st.db.Query(`SELECT app||'|'||person||'|'||method||'|'||scheme||'|'||host||'|'||port||'|'||path||'|'||addr||'|'||class FROM net_log`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		rows.Scan(&s)
		for _, bad := range []string{"SECRETKEY", "key=", "?", "SESSIONSECRET", "APPTOKEN", "REQBODY", "alice-sub", "Bearer", "upstream-secret", "pw@", "USERINFO"} {
			if strings.Contains(s, bad) {
				t.Fatalf("net_log row %q contains %q", s, bad)
			}
		}
	}
}

func TestNetFetchRedirectFollowedAndPrivateRedirectRefused(t *testing.T) {
	e := newNetEnv(t, true, echoUpstream())
	if code, rep := e.fetch("a", e.url("/redir-ok"), nil); code != 200 || rep.Status != 201 {
		t.Fatalf("%d %+v", code, rep)
	}
	code, rep := e.fetch("a", e.url("/redir-private"), nil)
	if code != 502 || rep.Error != "net_failed" {
		t.Fatalf("%d %+v", code, rep)
	}
	rows, _ := e.store.NetLog(context.Background(), "api-client", 1)
	if rows[0].Class != FenceBlocked || rows[0].Host != e.host {
		t.Fatalf("%+v", rows[0])
	}
}

func TestNetFetchRefusalsAreUniformAndLoggedWithClass(t *testing.T) {
	e := newNetEnv(t, true, echoUpstream())
	cases := []struct {
		name, url string
		code      int
		err, cls  string
	}{
		{"private", "http://private.example.test/", 502, "net_failed", FenceBlocked},
		{"mixed answers", "http://mixed.example.test/", 502, "net_failed", FenceBlocked},
		{"loopback literal", "http://127.0.0.1:" + e.port + "/", 502, "net_failed", FenceBlocked},
		{"decimal loopback", "http://2130706433/", 502, "net_failed", FenceBlocked},
		{"metadata", "http://169.254.169.254/latest/meta-data/", 502, "net_failed", FenceBlocked},
		{"ipv6 loopback", "http://[::1]/", 502, "net_failed", FenceBlocked},
		{"nxdomain", "http://nope.example.test/", 502, "net_failed", FenceResolve},
		{"mail port", "http://api.example.test:25/", 502, "net_failed", FencePort},
		{"ftp", "ftp://api.example.test/", 400, "net_invalid", FenceScheme},
		{"userinfo", "http://USERINFO:pw@api.example.test/", 400, "net_invalid", FenceUserinfo},
		{"no host", "http:///x", 400, "net_invalid", FenceHost},
		{"junk", "::::", 400, "net_invalid", "invalid"},
		{"ws", "ws://api.example.test/", 400, "net_invalid", FenceScheme},
	}
	for _, c := range cases {
		code, rep := e.fetch("a", c.url, nil)
		if code != c.code || rep.Error != c.err || rep.Status != 0 || len(rep.Body) != 0 {
			t.Fatalf("%s: %d %+v", c.name, code, rep)
		}
		rows, _ := e.store.NetLog(context.Background(), "api-client", 1)
		if len(rows) != 1 || rows[0].Class != c.cls || rows[0].Status != 0 {
			t.Fatalf("%s: %+v", c.name, rows)
		}
		e.store.db.Exec(`DELETE FROM net_log`)
	}
	if e.dials.Load() != 0 {
		t.Fatalf("a refused request reached the network (%d dials)", e.dials.Load())
	}
	// The log never holds user information.
	e.fetch("a", "http://USERINFO:pw@api.example.test/", nil)
	dumpNoSecrets(t, e.store)
}

func TestNetFetchInputValidation(t *testing.T) {
	e := newNetEnv(t, true, echoUpstream())
	big := bytes.Repeat([]byte("a"), NetMaxRequestBody+1)
	many := map[string]string{}
	for i := 0; i < netMaxHeaders+1; i++ {
		many[fmt.Sprintf("X-H%d", i)] = "v"
	}
	for name, b := range map[string]map[string]any{
		"trace":          {"method": "TRACE"},
		"connect":        {"method": "CONNECT"},
		"get with body":  {"method": "GET", "body": []byte("x")},
		"body too large": {"method": "POST", "body": big},
		"header crlf":    {"headers": map[string]string{"X-A": "a\r\nX-B: b"}},
		"header name":    {"headers": map[string]string{"X A": "a"}},
		"too many hdrs":  {"headers": many},
		"no url":         {"url": ""},
	} {
		if _, ok := b["url"]; !ok {
			b["url"] = e.url("/")
		}
		code, rep := e.call("api-client", "a", b)
		if code != 400 || rep.Error != "net_invalid" {
			t.Fatalf("%s: %d %+v", name, code, rep)
		}
	}
	if e.dials.Load() != 0 {
		t.Fatal("invalid request dialed")
	}
	for _, m := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		if code, _ := e.fetch("a", e.url("/"), map[string]any{"method": m}); code != 200 {
			t.Fatalf("%s refused: %d", m, code)
		}
	}
	// A request body at exactly the cap is accepted.
	if code, rep := e.fetch("a", e.url("/"), map[string]any{"method": "POST", "body": big[:NetMaxRequestBody]}); code != 200 {
		t.Fatalf("%d %+v", code, rep)
	}
}

func TestNetFetchResponseSizeCap(t *testing.T) {
	e := newNetEnv(t, true, echoUpstream())
	if code, rep := e.fetch("a", e.url("/exact"), nil); code != 200 || len(rep.Body) != NetMaxResponseBody {
		t.Fatalf("%d len=%d", code, len(rep.Body))
	}
	code, rep := e.fetch("a", e.url("/big"), nil)
	if code != 413 || rep.Error != "net_too_large" || len(rep.Body) != 0 {
		t.Fatalf("%d %+v", code, rep)
	}
	rows, _ := e.store.NetLog(context.Background(), "api-client", 1)
	if rows[0].Class != "too_large" || rows[0].Status != 200 {
		t.Fatalf("%+v", rows[0])
	}
}

func TestNetFetchRequiresSessionCSRFAndOrigin(t *testing.T) {
	e := newNetEnv(t, true, echoUpstream())
	for name, mod := range map[string]func(*http.Request){
		"no csrf":      func(r *http.Request) { r.Header.Set("X-T-CSRF", "bad") },
		"other origin": func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") },
		"no origin":    func(r *http.Request) { r.Header.Del("Origin") },
	} {
		req := httptest.NewRequest("POST", "https://home.example/bridge", strings.NewReader(`{"operation":"net.fetch","url":"`+e.url("/")+`"}`))
		req.Header.Set("Origin", "https://home.example")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-T-App", "api-client")
		req.Header.Set("X-T-Sub", "a")
		req.Header.Set("X-T-CSRF", "ok")
		mod(req)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != 403 || e.dials.Load() != 0 {
			t.Fatalf("%s: %d", name, rec.Code)
		}
	}
	if rows, _ := e.store.NetLog(context.Background(), "api-client", 10); len(rows) != 0 {
		t.Fatal("refused-at-the-door request logged")
	}
}

func TestNetFetchPerMinuteLimitIsolatedPerPersonAndApp(t *testing.T) {
	e := newNetEnv(t, true, echoUpstream())
	clock := time.Unix(1_800_000_000, 0)
	e.h.(*handler).now = func() time.Time { return clock }
	for i := 0; i < NetPerMinute; i++ {
		if code, rep := e.fetch("alice", e.url("/"), nil); code != 200 {
			t.Fatalf("%d: %d %+v", i, code, rep)
		}
	}
	code, rep := e.fetch("alice", e.url("/"), nil)
	if code != 429 || rep.Error != "net_rate_limited" {
		t.Fatalf("%d %+v", code, rep)
	}
	dials := e.dials.Load()
	// Another person of the same app, and the same subject in another app,
	// are unaffected.
	if code, _ := e.fetch("bob", e.url("/"), nil); code != 200 {
		t.Fatalf("bob: %d", code)
	}
	if code, _ := e.call("other-app", "alice", map[string]any{"url": e.url("/")}); code != 200 {
		t.Fatalf("other app: %d", code)
	}
	if e.dials.Load() != dials+2 {
		t.Fatal("limited request dialed")
	}
	// The refusal is logged with its class; counts and logs are per app.
	rows, _ := e.store.NetLog(context.Background(), "api-client", 1000)
	limited, alice := 0, 0
	p, _ := e.store.personUUID(context.Background(), "api-client", "alice")
	for _, r := range rows {
		if r.Class == "rate_limited" {
			limited++
		}
		if r.Person == p {
			alice++
		}
	}
	if limited != 1 || alice != NetPerMinute+1 || len(rows) != NetPerMinute+2 {
		t.Fatalf("limited=%d alice=%d rows=%d", limited, alice, len(rows))
	}
	if other, _ := e.store.NetLog(context.Background(), "other-app", 100); len(other) != 1 || other[0].Person == p {
		t.Fatalf("other app log %+v", other)
	}
	// The window slides: a minute later alice may fetch again.
	clock = clock.Add(61 * time.Second)
	if code, _ := e.fetch("alice", e.url("/"), nil); code != 200 {
		t.Fatalf("after a minute: %d", code)
	}
}

func TestNetLimiterDayAndConcurrency(t *testing.T) {
	var l netLimiter
	now := time.Unix(1_800_000_000, 0)
	// Daily: 5000 spread out (never 60 in a minute), the next is refused,
	// and it is allowed again 24h after the first.
	sc := Scope{App: "a", Subject: "p"}
	for i := 0; i < NetPerDay; i++ {
		rel, ok := l.acquire(sc, now.Add(time.Duration(i)*17*time.Second))
		if !ok {
			t.Fatalf("attempt %d refused", i)
		}
		rel()
	}
	last := now.Add(time.Duration(NetPerDay-1) * 17 * time.Second)
	if _, ok := l.acquire(sc, last.Add(time.Second)); ok {
		t.Fatal("5001st in a day allowed")
	}
	if _, ok := l.acquire(Scope{App: "b", Subject: "p"}, last); !ok {
		t.Fatal("other app shares the daily budget")
	}
	if _, ok := l.acquire(Scope{App: "a", Subject: "q"}, last); !ok {
		t.Fatal("other person shares the daily budget")
	}
	// Concurrency: 5 per person, 20 per app.
	var l2 netLimiter
	var rels []func()
	for i := 0; i < NetPersonConc; i++ {
		r, ok := l2.acquire(Scope{App: "a", Subject: "p"}, now)
		if !ok {
			t.Fatal("refused early")
		}
		rels = append(rels, r)
	}
	if _, ok := l2.acquire(Scope{App: "a", Subject: "p"}, now); ok {
		t.Fatal("6th concurrent allowed")
	}
	rels[0]()
	rels[0]() // release is idempotent
	if r, ok := l2.acquire(Scope{App: "a", Subject: "p"}, now.Add(time.Minute)); !ok {
		t.Fatal("slot not freed")
	} else {
		r()
	}
	for i := 0; i < 4; i++ { // other people fill the app to 20
		for j := 0; j < NetPersonConc; j++ {
			l2.acquire(Scope{App: "a", Subject: fmt.Sprint("x", i)}, now)
		}
	}
	if _, ok := l2.acquire(Scope{App: "a", Subject: "fresh"}, now); ok {
		t.Fatal("21st concurrent in the app allowed")
	}
	if _, ok := l2.acquire(Scope{App: "other", Subject: "fresh"}, now); !ok {
		t.Fatal("other app blocked by app concurrency")
	}
}

func TestNetLogRetention(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Now()
	st.LogNet(ctx, NetLogEntry{Time: now.Add(-31 * 24 * time.Hour).Unix(), App: "a", Person: "p", Method: "GET", Scheme: "http", Host: "old", Class: "ok"})
	for i := 0; i < NetLogMaxRows+25; i++ {
		if err = st.LogNet(ctx, NetLogEntry{Time: now.Unix(), App: "a", Person: "p", Method: "GET", Scheme: "http", Host: fmt.Sprint("h", i), Class: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	st.db.QueryRow(`SELECT COUNT(*) FROM net_log`).Scan(&n)
	if n != NetLogMaxRows {
		t.Fatalf("rows %d", n)
	}
	rows, _ := st.NetLog(ctx, "a", 1)
	if rows[0].Host != fmt.Sprint("h", NetLogMaxRows+24) {
		t.Fatalf("newest %+v", rows[0])
	}
	var old int
	st.db.QueryRow(`SELECT COUNT(*) FROM net_log WHERE host = 'old'`).Scan(&old)
	if old != 0 {
		t.Fatal("30-day-old row kept")
	}
}

func TestNetClassifyTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	code, status, class := netClassify(ctx.Err(), ctx)
	if code != "net_timeout" || status != 504 || class != "timeout" {
		t.Fatal(code, status, class)
	}
}
