package craftfiles

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testPassword = "s3cret-app-password-XYZ"
	testHost     = "gridcraft.lan"
	testApp      = "gridcraft"
)

// fakeDAV is a minimal Nextcloud WebDAV and OCS server.
type fakeDAV struct {
	mu       sync.Mutex
	files    map[string][]byte // key: path under /remote.php/dav/files/alice/Homestead
	dirs     map[string]bool
	reqs     []seenReq
	revoked  bool // answer 401 to everything
	status   int  // when non-zero, answer every DAV request with it
	redirect bool
	delay    time.Duration
	hold     chan struct{} // PUT blocks until closed when non-nil
	entered  chan struct{} // signalled when a held PUT arrives
	password string
	revokes  int
}

type seenReq struct {
	Method, Path string
	Header       http.Header
	Body         []byte
}

const davRoot = "/remote.php/dav/files/alice/Homestead"

func newFakeDAV() *fakeDAV {
	return &fakeDAV{files: map[string][]byte{}, dirs: map[string]bool{}, password: testPassword}
}

func (f *fakeDAV) seen() []seenReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seenReq(nil), f.reqs...)
}

func (f *fakeDAV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, pass, ok := r.BasicAuth()
	if strings.HasPrefix(r.URL.Path, "/ocs/") {
		f.ocs(w, r, user, pass, ok)
		return
	}
	f.mu.Lock()
	if f.delay > 0 {
		d := f.delay
		f.mu.Unlock()
		time.Sleep(d)
		f.mu.Lock()
	}
	var body []byte
	hold := f.hold
	entered := f.entered
	f.mu.Unlock()
	if r.Method == http.MethodPut && hold != nil {
		if entered != nil {
			select {
			case entered <- struct{}{}:
			default:
			}
		}
		<-hold
	}
	if r.Method == http.MethodPut && !strings.HasSuffix(r.URL.Path, "/big") {
		var err error
		if body, err = io.ReadAll(r.Body); err != nil {
			return // aborted upload: a real server stores nothing
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, seenReq{r.Method, r.URL.EscapedPath(), r.Header.Clone(), body})
	if !ok || user != "alice" || pass != f.password || f.revoked {
		w.Header().Set("WWW-Authenticate", `Basic realm="nextcloud"`)
		w.Header().Set("Set-Cookie", "oc_sessionPassphrase=upstream-secret")
		http.Error(w, "upstream says: bad credentials "+pass, http.StatusUnauthorized)
		return
	}
	if f.redirect {
		http.Redirect(w, r, "https://elsewhere.example/", http.StatusFound)
		return
	}
	if f.status != 0 {
		http.Error(w, "forced upstream failure with body text", f.status)
		return
	}
	rel, found := strings.CutPrefix(r.URL.Path, davRoot)
	if !found && r.URL.Path != "/remote.php/dav/files/alice/" && r.URL.Path != "/remote.php/dav/files/alice" {
		http.NotFound(w, r)
		return
	}
	rel = strings.Trim(rel, "/")
	switch r.Method {
	case "PROPFIND":
		if !found {
			w.WriteHeader(http.StatusMultiStatus)
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		fmt.Fprintf(w, `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:"><d:response><d:href>%s/%s</d:href></d:response>`, davRoot, rel)
		for name := range f.files {
			fmt.Fprintf(w, `<d:response><d:href>%s/%s</d:href></d:response>`, davRoot, name)
		}
		io.WriteString(w, `</d:multistatus>`)
	case http.MethodGet, http.MethodHead:
		if strings.HasSuffix(rel, "big") {
			n := int64(bigSize.Load())
			w.Header().Set("Content-Length", fmt.Sprint(n))
			w.Header().Set("Content-Type", "text/html")
			if r.Method == http.MethodGet {
				io.CopyN(w, zeroReader{}, n)
			}
			return
		}
		data, has := f.files[rel]
		if !has {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Etag", `"abc123"`)
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		if r.Method == http.MethodGet {
			w.Write(data)
		}
	case http.MethodPut:
		if strings.HasSuffix(rel, "big") {
			n, _ := io.Copy(io.Discard, r.Body)
			bigSeen.Store(n)
			w.WriteHeader(http.StatusCreated)
			return
		}
		if m := r.Header.Get("If-Match"); m != "" && m != `"abc123"` {
			http.Error(w, "precondition", http.StatusPreconditionFailed)
			return
		}
		f.files[rel] = body
		w.WriteHeader(http.StatusCreated)
	case "MKCOL":
		if rel == "" {
			rel = "Homestead"
		}
		if f.dirs[rel] {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		f.dirs[rel] = true
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		delete(f.files, rel)
		w.WriteHeader(http.StatusNoContent)
	case "MOVE", "COPY":
		dest := r.Header.Get("Destination")
		u, _ := url.Parse(dest)
		drel := strings.Trim(strings.TrimPrefix(u.Path, davRoot), "/")
		if !strings.HasPrefix(u.Path, davRoot+"/") {
			http.Error(w, "bad destination", http.StatusBadRequest)
			return
		}
		data := f.files[rel]
		f.files[drel] = data
		if r.Method == "MOVE" {
			delete(f.files, rel)
		}
		w.WriteHeader(http.StatusCreated)
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

func (f *fakeDAV) ocs(w http.ResponseWriter, r *http.Request, user, pass string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("OCS-APIRequest") != "true" {
		http.Error(w, "missing OCS header", http.StatusPreconditionFailed)
		return
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/getapppassword") && r.Method == http.MethodGet:
		if !ok || user != "alice" || pass != "account-login-password" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ocs": map[string]any{"data": map[string]string{"apppassword": f.password}}})
	case strings.HasSuffix(r.URL.Path, "/apppassword") && r.Method == http.MethodDelete:
		if !ok || pass != f.password {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		f.revokes++
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

// testClock is an injectable clock.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *testClock { return &testClock{t: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)} }
func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fakeOperator stands in for the operator session store.
type fakeOperator struct {
	mu      sync.Mutex
	expires map[string]time.Time
	down    bool
}

func (o *fakeOperator) Check(token string) (time.Time, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.down {
		return time.Time{}, ErrOperatorUnavailable
	}
	exp, ok := o.expires[token]
	if !ok {
		return time.Time{}, ErrOperatorInvalid
	}
	return exp, nil
}

func (o *fakeOperator) revoke(token string) {
	o.mu.Lock()
	delete(o.expires, token)
	o.mu.Unlock()
}

// env wires a craft listener (guard + mux) to a fake Nextcloud.
type env struct {
	t       *testing.T
	dav     *fakeDAV
	davSrv  *httptest.Server
	clock   *testClock
	op      *fakeOperator
	ses     *Sessions
	proxy   *Proxy
	handler *Handler
	srv     *httptest.Server
	events  []Event
	evMu    sync.Mutex
	auditor *Auditor
	linked  bool
	local   bool
}

func newEnv(t *testing.T, lim Limits) *env {
	t.Helper()
	e := &env{t: t, dav: newFakeDAV(), clock: newClock(), linked: true}
	e.davSrv = httptest.NewServer(e.dav)
	t.Cleanup(e.davSrv.Close)
	e.op = &fakeOperator{expires: map[string]time.Time{"op-token": e.clock.Now().Add(24 * time.Hour)}}
	e.ses = NewSessions(e.op)
	e.ses.Now = e.clock.Now
	e.auditor = &Auditor{Now: e.clock.Now, Sink: func(ev Event) {
		e.evMu.Lock()
		e.events = append(e.events, ev)
		e.evMu.Unlock()
	}}
	link := Link{BaseURL: e.davSrv.URL, Account: "alice", Root: "Homestead"}
	e.proxy = &Proxy{
		Link:     func() (Link, bool, error) { return link, e.linked, nil },
		Password: func() (string, error) { return testPassword, nil },
		Client:   func() *http.Client { return NewClient(nil) },
		Limits:   lim, Audit: e.auditor, Sessions: e.ses,
	}
	e.handler = &Handler{Sessions: e.ses, Proxy: e.proxy, Info: func(string) (SessionInfo, error) {
		return SessionInfo{Linked: e.linked, LocalOnly: e.local, Root: "Homestead", MaxUpload: lim.MaxUpload}, nil
	}, Audit: e.auditor}
	mux := http.NewServeMux()
	for _, r := range e.handler.Routes() {
		mux.HandleFunc(r.Pattern, r.Handler)
	}
	e.srv = httptest.NewServer(e.handler.Guard(mux))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) eventList() []Event {
	e.evMu.Lock()
	defer e.evMu.Unlock()
	return append([]Event(nil), e.events...)
}

// reqOpt customises a request.
type reqOpt func(*http.Request)

func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }
func withoutHeader(k string) reqOpt { return func(r *http.Request) { r.Header.Del(k) } }
func withCookie(v string) reqOpt {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: CookieName, Value: v}) }
}

// do sends a request as the browser behind the edge would: Host set, edge
// headers set, same-origin Origin.
func (e *env) do(method, path string, body io.Reader, opts ...reqOpt) *http.Response {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, body)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Host = testHost
	req.Header.Set("X-Homestead-Craft-App", testApp)
	req.Header.Set("X-Homestead-Craft-Host", testHost)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Origin", "https://"+testHost)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, o := range opts {
		o(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	return resp
}

func readAll(t *testing.T, r *http.Response) string {
	t.Helper()
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// login mints a code for the fake operator session, exchanges it and
// returns the cookie value.
func (e *env) login() string {
	e.t.Helper()
	code, _, err := e.ses.Mint("op-token", "owner", e.clock.Now().Add(24*time.Hour), testApp, testHost)
	if err != nil {
		e.t.Fatal(err)
	}
	resp := e.do("POST", SessionPath, strings.NewReader(`{"code":"`+code+`"}`))
	if resp.StatusCode != 200 {
		e.t.Fatalf("exchange = %d: %s", resp.StatusCode, readAll(e.t, resp))
	}
	for _, c := range resp.Cookies() {
		if c.Name == CookieName {
			resp.Body.Close()
			return c.Value
		}
	}
	e.t.Fatal("exchange set no cookie")
	return ""
}

func newDAVServer(t *testing.T, dav *fakeDAV) string {
	t.Helper()
	srv := httptest.NewServer(dav)
	t.Cleanup(srv.Close)
	return srv.URL
}
