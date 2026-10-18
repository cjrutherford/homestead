package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"homestead/internal/craftfiles"
	"homestead/internal/docker"
	"homestead/internal/doctor"
)

const (
	cfPassword = "app-password-DO-NOT-LEAK-1234"
	cfHost     = "gridcraft.lan"
)

// cfDAV is a tiny Nextcloud: PROPFIND, MKCOL, PUT, GET under alice's files.
type cfDAV struct {
	mu    sync.Mutex
	files map[string][]byte
}

func (d *cfDAV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, p, ok := r.BasicAuth(); !ok || p != cfPassword {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	switch r.Method {
	case "PROPFIND":
		w.WriteHeader(http.StatusMultiStatus)
	case "MKCOL":
		w.WriteHeader(http.StatusCreated)
	case http.MethodPut:
		b, _ := io.ReadAll(r.Body)
		d.files[r.URL.Path] = b
		w.WriteHeader(http.StatusCreated)
	case http.MethodGet:
		b, ok := d.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

type craftApp struct {
	*enforcedApp
	token string
	dav   *cfDAV
	davU  string
}

func newCraftApp(t *testing.T) *craftApp {
	t.Helper()
	a := &craftApp{enforcedApp: newEnforcedApp(t), dav: &cfDAV{files: map[string][]byte{}}}
	a.token = a.setupToken(t)
	srv := httptest.NewServer(a.dav)
	t.Cleanup(srv.Close)
	a.davU = srv.URL
	// The edge route table as the reconcile records it.
	route := docker.Route{Host: cfHost, Target: docker.Target{Host: "gridcraft", Port: 8080},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal, CraftApp: "gridcraft"}
	if err := saveEdgeRoutes(a.st, docker.RouteSet{Routes: []docker.Route{route}}); err != nil {
		t.Fatal(err)
	}
	return a
}

func (a *craftApp) api(t *testing.T, method, path string, body any, token string) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	a.srv.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func (a *craftApp) link(t *testing.T) {
	t.Helper()
	code, body := a.api(t, http.MethodPost, "/api/v1/craft/files/plan",
		map[string]any{"action": "link", "base_url": a.davU, "account": "alice", "root": "Homestead"}, a.token)
	if code != 200 {
		t.Fatalf("plan = %d %s", code, body)
	}
	var plan struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(body), &plan)
	code, body = a.api(t, http.MethodPost, "/api/v1/craft/files/approve",
		map[string]any{"plan_id": plan.ID, "app_password": cfPassword}, a.token)
	if code != 200 {
		t.Fatalf("approve = %d %s", code, body)
	}
}

// launch mints a code through the API and returns it.
func (a *craftApp) launch(t *testing.T, token string) string {
	t.Helper()
	code, body := a.api(t, http.MethodPost, "/api/v1/craft/files/launch", map[string]string{"app": "gridcraft"}, token)
	if code != 200 {
		t.Fatalf("launch = %d %s", code, body)
	}
	var res struct{ URL string }
	_ = json.Unmarshal([]byte(body), &res)
	_, launchCode, ok := strings.Cut(res.URL, "#code=")
	if !ok || !strings.HasPrefix(res.URL, "https://"+cfHost+"/_homestead/open.html#") {
		t.Fatalf("launch url = %q", res.URL)
	}
	return launchCode
}

// edge sends a request as Caddy would deliver it on the craft listener.
func (a *craftApp) edge(method, path, body string, cookie *http.Cookie, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = cfHost
	req.Header.Set("X-Homestead-Craft-App", "gridcraft")
	req.Header.Set("X-Homestead-Craft-Host", cfHost)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Origin", "https://"+cfHost)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for _, m := range mutate {
		m(req)
	}
	rec := httptest.NewRecorder()
	a.srv.CraftHandler().ServeHTTP(rec, req)
	return rec
}

func (a *craftApp) exchange(t *testing.T, code string) *http.Cookie {
	t.Helper()
	rec := a.edge(http.MethodPost, craftfiles.SessionPath, `{"code":"`+code+`"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("exchange = %d %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == craftfiles.CookieName {
			return c
		}
	}
	t.Fatal("no craft cookie")
	return nil
}

func TestCraftFilesEndToEnd(t *testing.T) {
	a := newCraftApp(t)
	a.link(t)
	// Status is secret-free and shows the hooked apps.
	code, body := a.api(t, http.MethodGet, "/api/v1/craft/files", nil, a.token)
	if code != 200 || !strings.Contains(body, `"gridcraft"`) || !strings.Contains(body, `"credential_stored":true`) {
		t.Fatalf("status = %d %s", code, body)
	}
	cookie := a.exchange(t, a.launch(t, a.token))
	if rec := a.edge(http.MethodPut, "/_homestead/files/f/Book.xlsx", "workbook", cookie); rec.Code != 201 {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body.String())
	}
	rec := a.edge(http.MethodGet, "/_homestead/files/f/Book.xlsx", "", cookie)
	if rec.Code != 200 || rec.Body.String() != "workbook" {
		t.Fatalf("GET = %d %q", rec.Code, rec.Body.String())
	}
	if string(a.dav.files["/remote.php/dav/files/alice/Homestead/Book.xlsx"]) != "workbook" {
		t.Fatalf("not stored under the root folder: %v", a.dav.files)
	}
	// The password appears nowhere an owner or the browser can read.
	for _, path := range []string{"/api/v1/craft/files", "/api/v1/config/export", "/api/v1/audit?limit=500", "/api/v1/secrets/key", "/api/v1/config"} {
		_, b := a.api(t, http.MethodGet, path, nil, a.token)
		if strings.Contains(b, cfPassword) {
			t.Errorf("GET %s leaks the app password", path)
		}
	}
	for _, rec := range []*httptest.ResponseRecorder{a.edge(http.MethodGet, "/_homestead/files/f/Book.xlsx", "", cookie), a.edge(http.MethodGet, craftfiles.SessionPath, "", cookie)} {
		if strings.Contains(rec.Body.String()+fmt.Sprint(rec.Header()), cfPassword) {
			t.Error("a craft response carries the password")
		}
	}
	// The audit trail has the operations, without content or secrets.
	a.srv.files.Auditor.Flush(true)
	_, audit := a.api(t, http.MethodGet, "/api/v1/audit?limit=500", nil, a.token)
	for _, want := range []string{"craftfiles.file", "craft=gridcraft", "PUT /Book.xlsx status=201 bytes=8", "craftfiles.link", "craftfiles.launch"} {
		if !strings.Contains(audit, want) {
			t.Errorf("audit lacks %q", want)
		}
	}
	for _, leak := range []string{cfPassword, cookie.Value, "workbook\""} {
		if strings.Contains(audit, leak) {
			t.Errorf("audit contains %q", leak)
		}
	}
	// The secret store holds it sealed under a reserved name.
	_, b := a.api(t, http.MethodGet, "/api/v1/secrets/key", nil, a.token)
	_ = b
}

func TestCraftLaunchNeedsSessionAndCSRF(t *testing.T) {
	a := newCraftApp(t)
	a.link(t)
	if code, _ := a.api(t, http.MethodPost, "/api/v1/craft/files/launch", map[string]string{"app": "gridcraft"}, "bogus"); code != 401 {
		t.Fatalf("bogus session = %d", code)
	}
	rec := a.do(http.MethodPost, "/api/v1/craft/files/launch", `{"app":"gridcraft"}`, nil)
	if rec.Code != 401 {
		t.Fatalf("no session = %d", rec.Code)
	}
	// A cookie session without the CSRF header is refused by the boundary.
	rec = a.do(http.MethodPost, "/api/v1/craft/files/launch", `{"app":"gridcraft"}`, func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: a.token})
	})
	if rec.Code != 403 {
		t.Fatalf("cookie without CSRF = %d, want 403", rec.Code)
	}
	// Errors: unlinked-style conditions.
	if code, _ := a.api(t, http.MethodPost, "/api/v1/craft/files/launch", map[string]string{"app": "wordcraft"}, a.token); code != 422 {
		t.Fatalf("unhooked app = %d, want 422", code)
	}
	if code, _ := a.api(t, http.MethodPost, "/api/v1/craft/files/launch", map[string]string{"app": "pdfcraft"}, a.token); code != 409 {
		t.Fatalf("app without a route = %d, want 409", code)
	}
}

func TestCraftSessionEndsWithLogout(t *testing.T) {
	a := newCraftApp(t)
	a.link(t)
	cookie := a.exchange(t, a.launch(t, a.token))
	if rec := a.edge(http.MethodGet, "/_homestead/files/f/Book.xlsx", "", cookie); rec.Code == 401 {
		t.Fatal("fresh craft session refused")
	}
	if code, _ := a.api(t, http.MethodDelete, "/api/v1/sessions", nil, a.token); code != http.StatusNoContent {
		t.Fatalf("logout = %d", code)
	}
	if rec := a.edge(http.MethodGet, "/_homestead/files/f/Book.xlsx", "", cookie); rec.Code != 401 {
		t.Fatalf("craft session after logout = %d, want 401", rec.Code)
	}
}

func TestCraftLaunchCodeDiesWithLogout(t *testing.T) {
	a := newCraftApp(t)
	a.link(t)
	code := a.launch(t, a.token)
	a.api(t, http.MethodDelete, "/api/v1/sessions", nil, a.token)
	rec := a.edge(http.MethodPost, craftfiles.SessionPath, `{"code":"`+code+`"}`, nil)
	if rec.Code != 401 {
		t.Fatalf("exchange after logout = %d, want 401", rec.Code)
	}
}

func TestCraftSessionEndsWithPasswordChange(t *testing.T) {
	a := newCraftApp(t)
	a.link(t)
	cookie := a.exchange(t, a.launch(t, a.token))
	// A second login changes the password; the first session (and the craft
	// session bound to it) is revoked, the changer's survives.
	rec := a.do(http.MethodPost, "/api/v1/sessions", `{"username":"owner","password":"long-enough-password"}`, nil)
	var login struct{ Token string }
	_ = json.Unmarshal(rec.Body.Bytes(), &login)
	if login.Token == "" {
		t.Fatalf("second login: %d %s", rec.Code, rec.Body.String())
	}
	code, body := a.api(t, http.MethodPost, "/api/v1/admins/password",
		map[string]string{"current_password": "long-enough-password", "new_password": "another-long-password"}, login.Token)
	if code != 200 {
		t.Fatalf("password change = %d %s", code, body)
	}
	if rec := a.edge(http.MethodGet, "/_homestead/files/f/Book.xlsx", "", cookie); rec.Code != 401 {
		t.Fatalf("craft session after password change = %d, want 401", rec.Code)
	}
	// The surviving session can launch again.
	a.launch(t, login.Token)
}

// The changer's own operator session survives a password change, so the
// craft sessions bound to it are revoked explicitly.
func TestCraftSessionOfTheChangingSessionEndsWithPasswordChange(t *testing.T) {
	a := newCraftApp(t)
	a.link(t)
	rec := a.do(http.MethodPost, "/api/v1/sessions", `{"username":"owner","password":"long-enough-password"}`, nil)
	var login struct{ Token string }
	_ = json.Unmarshal(rec.Body.Bytes(), &login)
	if login.Token == "" {
		t.Fatalf("second login: %d %s", rec.Code, rec.Body.String())
	}
	cookie := a.exchange(t, a.launch(t, login.Token))
	pending := a.launch(t, login.Token)
	if rec := a.edge(http.MethodGet, "/_homestead/files/f/Book.xlsx", "", cookie); rec.Code == 401 {
		t.Fatal("fresh craft session refused")
	}
	if code, body := a.api(t, http.MethodPost, "/api/v1/admins/password",
		map[string]string{"current_password": "long-enough-password", "new_password": "another-long-password"}, login.Token); code != 200 {
		t.Fatalf("password change = %d %s", code, body)
	}
	if rec := a.edge(http.MethodGet, "/_homestead/files/f/Book.xlsx", "", cookie); rec.Code != 401 {
		t.Fatalf("craft session bound to the changing session = %d, want 401", rec.Code)
	}
	if rec := a.edge(http.MethodPost, craftfiles.SessionPath, `{"code":"`+pending+`"}`, nil); rec.Code != 401 {
		t.Fatalf("pending launch code after password change = %d, want 401", rec.Code)
	}
}

func TestCraftSocketPathIsPinned(t *testing.T) {
	if err := ValidateCraftSocketPath(docker.CraftSocketPath); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/tmp/x.sock", "/run/other/files.sock", "/etc/passwd", "files.sock", "/run/homestead-craft/../homestead/homestead.sock", ""} {
		if err := ValidateCraftSocketPath(p); err == nil {
			t.Errorf("craft socket path %q accepted", p)
		}
	}
}

func TestCraftCookieRequiresTheCraftHost(t *testing.T) {
	a := newCraftApp(t)
	a.link(t)
	code := a.launch(t, a.token)
	// Minted for gridcraft.lan: exchanging it through another craft host fails.
	rec := a.edge(http.MethodPost, craftfiles.SessionPath, `{"code":"`+code+`"}`, nil, func(r *http.Request) {
		r.Host = "cadcraft.lan"
		r.Header.Set("X-Homestead-Craft-Host", "cadcraft.lan")
		r.Header.Set("X-Homestead-Craft-App", "cadcraft")
		r.Header.Set("Origin", "https://cadcraft.lan")
	})
	if rec.Code != 401 && rec.Code != 409 {
		t.Fatalf("exchange via another host = %d", rec.Code)
	}
	cookie := a.exchange(t, a.launch(t, a.token))
	// A Host that does not match the edge's craft host (a request not
	// delivered for this route) is refused.
	rec = a.edge(http.MethodGet, "/_homestead/files/f/x", "", cookie, func(r *http.Request) { r.Host = "evil.example" })
	if rec.Code != http.StatusMisdirectedRequest {
		t.Fatalf("mismatched Host = %d, want 421", rec.Code)
	}
	// Without the edge headers (a direct connection to the socket).
	rec = a.edge(http.MethodGet, "/_homestead/files/f/x", "", cookie, func(r *http.Request) {
		r.Header.Del("X-Homestead-Craft-Host")
		r.Header.Del("X-Homestead-Craft-App")
	})
	if rec.Code != 400 {
		t.Fatalf("no edge identity = %d, want 400", rec.Code)
	}
	// Cross-origin.
	rec = a.edge(http.MethodPut, "/_homestead/files/f/x", "x", cookie, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") })
	if rec.Code != 403 {
		t.Fatalf("cross-origin PUT = %d, want 403", rec.Code)
	}
}

func TestCraftRoutesAreNotOnTheTCPListener(t *testing.T) {
	a := newCraftApp(t)
	a.link(t)
	cookie := a.exchange(t, a.launch(t, a.token))
	for _, p := range []string{craftfiles.SessionPath, "/_homestead/files/f/Book.xlsx"} {
		rec := a.do(http.MethodGet, p, "", func(r *http.Request) {
			r.AddCookie(cookie)
			r.Header.Set("Authorization", "Bearer "+a.token)
			r.Header.Set("X-Homestead-Craft-App", "gridcraft")
			r.Header.Set("X-Homestead-Craft-Host", cfHost)
		})
		if rec.Code == 200 || strings.Contains(rec.Body.String(), "workbook") {
			t.Errorf("TCP listener served %s: %d", p, rec.Code)
		}
	}
}

func TestCraftSocketServesOnlyCraftPaths(t *testing.T) {
	a := newCraftApp(t)
	a.link(t)
	dir, err := os.MkdirTemp("", "craft")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "run", "files.sock")
	start := func() (stop func()) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- a.srv.RunCraftSocket(ctx, sock) }()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if c, err := net.Dial("unix", sock); err != nil && time.Now().After(deadline) {
				t.Fatalf("dial: %v", err)
			} else if err == nil {
				c.Close()
				break
			}
			select {
			case err := <-done:
				t.Fatalf("craft socket exited: %v", err)
			default:
			}
			if time.Now().After(deadline) {
				t.Fatal("craft socket did not come up")
			}
			time.Sleep(10 * time.Millisecond)
		}
		return func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("RunCraftSocket: %v", err)
			}
		}
	}
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	get := func(path string, edge bool) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, "http://"+cfHost+path, nil)
		if edge {
			req.Header.Set("X-Homestead-Craft-App", "gridcraft")
			req.Header.Set("X-Homestead-Craft-Host", cfHost)
			req.Header.Set("X-Forwarded-Proto", "https")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	stop := start()
	// Permissions: directory 0750, socket 0660, never world-accessible.
	if fi, err := os.Stat(sock); err != nil || fi.Mode().Perm() != 0o660 {
		t.Fatalf("socket mode: %v %v", fi, err)
	}
	if fi, err := os.Stat(filepath.Dir(sock)); err != nil || fi.Mode().Perm() != 0o750 {
		t.Fatalf("socket dir mode: %v %v", fi, err)
	}
	// The listener refuses everything that is not a craft file path.
	for _, p := range []string{"/", "/healthz", "/version", "/api/v1/identity", "/api/v1/config", "/api/v1/admins/recover", "/api/v1/craft/files", "/_homestead/open.html"} {
		if code, _ := get(p, true); code != 404 {
			t.Errorf("GET %s on the craft socket = %d, want 404", p, code)
		}
		if code, _ := get(p, false); code != 400 && code != 404 {
			t.Errorf("GET %s without edge identity = %d", p, code)
		}
	}
	if code, _ := get("/_homestead/files/session", true); code != 401 {
		t.Fatalf("craft path without a cookie = %d, want 401", code)
	}
	// Restart: a stale socket file is replaced and the directory survives,
	// so a mount of the directory keeps working.
	stop()
	if _, err := os.Stat(filepath.Dir(sock)); err != nil {
		t.Fatalf("directory gone after stop: %v", err)
	}
	if err := os.WriteFile(sock, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop = start()
	defer stop()
	if code, _ := get("/_homestead/files/session", true); code != 401 {
		t.Fatalf("after restart = %d, want 401", code)
	}
}

func TestCraftLimitsAreConfigurable(t *testing.T) {
	a := newCraftApp(t)
	code, body := a.api(t, http.MethodGet, "/api/v1/config/schema", nil, a.token)
	if code != 200 {
		t.Fatalf("schema = %d", code)
	}
	for _, f := range []string{craftfiles.SettingMaxUploadMB, craftfiles.SettingTimeoutMin, craftfiles.SettingPerSession} {
		if !strings.Contains(body, f) {
			t.Errorf("config schema lacks %s", f)
		}
	}
	if code, body := a.api(t, http.MethodPut, "/api/v1/config", map[string]any{"settings": map[string]any{craftfiles.SettingMaxUploadMB: 1}}, a.token); code != 200 {
		t.Fatalf("set limit = %d %s", code, body)
	}
	a.link(t)
	cookie := a.exchange(t, a.launch(t, a.token))
	big := strings.Repeat("x", 2<<20)
	if rec := a.edge(http.MethodPut, "/_homestead/files/f/big.bin", big, cookie); rec.Code != 413 {
		t.Fatalf("2 MiB over a 1 MiB limit = %d, want 413", rec.Code)
	}
	if rec := a.edge(http.MethodPut, "/_homestead/files/f/ok.bin", "small", cookie); rec.Code != 201 {
		t.Fatalf("small upload = %d", rec.Code)
	}
	if code, body := a.api(t, http.MethodPut, "/api/v1/config", map[string]any{"settings": map[string]any{craftfiles.SettingMaxUploadMB: 0}}, a.token); code != 422 {
		t.Fatalf("limit 0 = %d %s, want 422", code, body)
	}
}

func TestCraftUnlinkDropsCredential(t *testing.T) {
	a := newCraftApp(t)
	a.link(t)
	cookie := a.exchange(t, a.launch(t, a.token))
	code, body := a.api(t, http.MethodPost, "/api/v1/craft/files/plan", map[string]any{"action": "unlink"}, a.token)
	if code != 200 {
		t.Fatalf("plan unlink = %d", code)
	}
	var plan struct{ ID string }
	_ = json.Unmarshal([]byte(body), &plan)
	if code, body := a.api(t, http.MethodPost, "/api/v1/craft/files/approve", map[string]any{"plan_id": plan.ID}, a.token); code != 200 {
		t.Fatalf("approve unlink = %d %s", code, body)
	}
	if rec := a.edge(http.MethodGet, "/_homestead/files/f/Book.xlsx", "", cookie); rec.Code != 409 {
		t.Fatalf("proxy after unlink = %d, want 409", rec.Code)
	}
	if code, _ := a.api(t, http.MethodPost, "/api/v1/craft/files/launch", map[string]string{"app": "gridcraft"}, a.token); code != 409 {
		t.Fatalf("launch after unlink = %d, want 409", code)
	}
}

func TestCraftLocalOnlyOptOut(t *testing.T) {
	a := newCraftApp(t)
	a.link(t)
	if code, _ := a.api(t, http.MethodPost, "/api/v1/craft/files/local-only", map[string]any{"app": "gridcraft", "local_only": true}, a.token); code != 200 {
		t.Fatal("set local-only")
	}
	if code, _ := a.api(t, http.MethodPost, "/api/v1/craft/files/launch", map[string]string{"app": "gridcraft"}, a.token); code != 409 {
		t.Fatalf("launch of a local-only app = %d, want 409", code)
	}
	if code, _ := a.api(t, http.MethodPost, "/api/v1/craft/files/local-only", map[string]any{"app": "wordcraft", "local_only": true}, a.token); code != 422 {
		t.Fatalf("unhooked app opt-out = %d, want 422", code)
	}
}

func TestCraftDoctorCheckReportsEdgeDrift(t *testing.T) {
	a := newCraftApp(t)
	dir := t.TempDir()
	sock := filepath.Join(dir, "files.sock")
	check := func(mounted bool, mErr error) []string {
		c := craftFilesDoctorCheck(a.st, func(context.Context) (bool, error) { return mounted, mErr }, sock)()
		return []string{c[0].Name, c[0].Status, c[0].Detail}
	}
	if got := check(true, nil); got[1] != "warn" || !strings.Contains(got[2], "socket") {
		t.Fatalf("missing socket: %v", got)
	}
	if err := os.WriteFile(sock, nil, 0o660); err != nil {
		t.Fatal(err)
	}
	if got := check(false, nil); got[1] != "warn" || !strings.Contains(got[2], "redeploy the edge") {
		t.Fatalf("missing mount: %v", got)
	}
	if got := check(true, nil); got[1] != "ok" {
		t.Fatalf("mounted: %v", got)
	}
	if got := check(false, fmt.Errorf("docker down")); got[1] != "warn" {
		t.Fatalf("inspect error: %v", got)
	}
	if err := saveEdgeRoutes(a.st, docker.RouteSet{}); err != nil {
		t.Fatal(err)
	}
	if got := check(false, nil); got[1] != "ok" {
		t.Fatalf("no craft routes: %v", got)
	}
}

func TestEdgeUpstreamDoctorCheck(t *testing.T) {
	a := newCraftApp(t)
	if err := saveEdgeRoutes(a.st, docker.RouteSet{Routes: []docker.Route{{
		Host: "nextcloud.lan", Target: docker.Target{Host: "nextcloud", Port: 80},
		Exposure: docker.ExposureLocal, TLS: docker.TLSInternal,
	}}}); err != nil {
		t.Fatal(err)
	}
	caddy := docker.Container{Names: []string{"/" + docker.CaddyContainerName}, Networks: []string{"homestead-edge"}}
	app := func(networks ...string) docker.Container {
		return docker.Container{Names: []string{"/nextcloud-nextcloud"}, Networks: networks,
			Labels: map[string]string{"com.docker.compose.service": "nextcloud"}}
	}
	run := func(all ...docker.Container) doctor.Check {
		return edgeUpstreamDoctorCheck(a.st, func(context.Context) ([]docker.Container, error) { return all, nil })()[0]
	}
	if c := run(caddy, app("nextcloud_default")); c.Status != doctor.StatusWarn || !strings.Contains(c.Detail, "nextcloud.lan") || !strings.Contains(c.Detail, "docker network connect") {
		t.Fatalf("pre-S249 install: %+v", c)
	}
	if c := run(caddy, app("nextcloud_default", "homestead-edge")); c.Status != doctor.StatusOK {
		t.Fatalf("attached: %+v", c)
	}
	if c := run(app("nextcloud_default")); c.Status != doctor.StatusOK {
		t.Fatalf("edge not running: %+v", c)
	}
	if c := edgeUpstreamDoctorCheck(a.st, func(context.Context) ([]docker.Container, error) { return nil, fmt.Errorf("down") })()[0]; c.Status != doctor.StatusWarn {
		t.Fatalf("list error: %+v", c)
	}
}
