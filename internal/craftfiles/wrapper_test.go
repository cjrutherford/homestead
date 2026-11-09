package craftfiles

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// The daemon's wrapper differs from the signed image's copy only in its
// paths (ADR-045): any other change must land in both.
func TestWrapperMatchesImageCopyBarPaths(t *testing.T) {
	edits := map[string][][2]string{
		"open.html": {
			{`href="/_homestead/open.css"`, `href="open.css"`},
			{`src="/_homestead/open.js"`, `src="open.js"`},
			{`id="local" href="/"`, `id="local" href="../"`},
		},
		"open.js": {
			{"'/?host=parent' : '/'", "'../?host=parent' : '../'"},
			{"var FILES = '/_homestead/files';", "var FILES = new URL('files', location.href).pathname;"},
		},
		"open.css": nil,
	}
	for name, subs := range edits {
		image, err := os.ReadFile("../../images/craft/homestead/" + name)
		if err != nil {
			t.Fatal(err)
		}
		ours, err := wrapperFiles.ReadFile("wrapper/" + name)
		if err != nil {
			t.Fatal(err)
		}
		want := string(image)
		for _, s := range subs {
			if !strings.Contains(want, s[0]) {
				t.Fatalf("%s: image copy no longer has %q", name, s[0])
			}
			want = strings.Replace(want, s[0], s[1], 1)
		}
		if code := codeLines(string(ours)); code != codeLines(want) {
			t.Errorf("%s: the daemon's wrapper drifted from images/craft/homestead/%s beyond its paths", name, name)
		}
		if strings.Contains(codeLines(string(ours)), "'/_homestead") || strings.Contains(codeLines(string(ours)), `"/_homestead`) {
			t.Errorf("%s still has an absolute path", name)
		}
	}
}

// codeLines drops comments, which may differ between the two copies.
func codeLines(s string) string {
	var out []string
	inHTMLComment := false
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if inHTMLComment {
			if strings.Contains(t, "-->") {
				inHTMLComment = false
			}
			continue
		}
		if strings.HasPrefix(t, "<!--") {
			inHTMLComment = !strings.Contains(t, "-->")
			continue
		}
		if t == "" || strings.HasPrefix(t, "//") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func TestWrapperServedOnPathRoute(t *testing.T) {
	e := newEnv(t, Limits{})
	direct := func(r *http.Request) {
		r.Header.Del("Origin")
		r.Header.Set("Sec-Fetch-Site", "none")
		r.Header.Set(prefixHeader, "/apps/"+testApp)
	}
	r := e.do("GET", "/_homestead/open.html", nil, direct)
	body, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 || !strings.Contains(string(body), `src="open.js"`) {
		t.Fatalf("wrapper = %d %s", r.StatusCode, body)
	}
	if csp := r.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP %q", csp)
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("type %q", ct)
	}
	if r := e.do("GET", "/_homestead/open.js", nil); r.StatusCode != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/javascript") {
		t.Fatalf("open.js = %d %s", r.StatusCode, r.Header.Get("Content-Type"))
	}
	// Only the wrapper's files may be opened directly; the session may not.
	if r := e.do("GET", SessionPath, nil, direct); r.StatusCode != 403 {
		t.Fatalf("direct session read = %d, want 403", r.StatusCode)
	}
	// A cross-site fetch of the wrapper is still refused.
	if r := e.do("GET", "/_homestead/open.js", nil, withHeader("Origin", "https://evil.example")); r.StatusCode != 403 {
		t.Fatalf("cross-origin wrapper fetch = %d, want 403", r.StatusCode)
	}
	if r := e.do("POST", "/_homestead/open.html", nil); r.StatusCode != 405 && r.StatusCode != 404 {
		t.Fatalf("POST wrapper = %d", r.StatusCode)
	}
}

func TestPathRouteCookieAndPrefix(t *testing.T) {
	e := newEnv(t, Limits{})
	for _, bad := range []string{"/apps/", "/apps/x/", "/other/x", "apps/x", "/apps/x/../y"} {
		if r := e.do("GET", SessionPath, nil, withHeader(prefixHeader, bad)); r.StatusCode != 400 {
			t.Errorf("prefix %q = %d, want 400", bad, r.StatusCode)
		}
	}
	code := mint(t, e, testApp, testHost)
	body, _ := json.Marshal(map[string]string{"code": code})
	r := e.do("POST", SessionPath, bytes.NewReader(body), withHeader(prefixHeader, "/apps/"+testApp))
	if r.StatusCode != 200 {
		t.Fatalf("exchange = %d", r.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range r.Cookies() {
		if c.Name == CookieName {
			cookie = c
		}
	}
	if cookie == nil || cookie.Path != "/apps/"+testApp+CookiePath || !cookie.HttpOnly || !cookie.Secure {
		t.Fatalf("cookie %+v", cookie)
	}
	r = e.do("DELETE", SessionPath, nil, withCookie(cookie.Value), withHeader(prefixHeader, "/apps/"+testApp))
	for _, c := range r.Cookies() {
		if c.Name == CookieName && c.Path != "/apps/"+testApp+CookiePath {
			t.Fatalf("cleared cookie path %q", c.Path)
		}
	}
}

func TestLaunchOnPathRoute(t *testing.T) {
	e := newMgr(t)
	sessions := NewSessions(&fakeOperator{expires: map[string]time.Time{"op": e.clock.Now().Add(time.Hour)}})
	sessions.Now = e.clock.Now
	if _, _, err := link(t, e, PlanRequest{Action: ActionLink, BaseURL: e.baseURL, Account: "alice", Root: "Homestead"}, ApproveRequest{AppPassword: testPassword}); err != nil {
		t.Fatal(err)
	}
	e.routes = []CraftRoute{{App: "gridcraft", Host: "homestead.lan", TLS: true, Path: "/apps/gridcraft/",
		Names: []string{"homestead.lan", "homestead.local", "homestead.example.com"}}}
	launch := func(host string) LaunchResult {
		t.Helper()
		res, err := e.m.Launch(sessions, "op", "owner", e.clock.Now().Add(time.Hour), "gridcraft", host)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := launch("Homestead.Example.com")
	want := "https://homestead.example.com/apps/gridcraft/_homestead/open.html#code="
	if !strings.HasPrefix(res.URL, want) {
		t.Fatalf("launch url %s", res.URL)
	}
	code := strings.TrimPrefix(res.URL, want)
	if _, _, err := sessions.Exchange(code, "gridcraft", "homestead.lan"); err == nil {
		t.Fatal("code exchanged on another name")
	}
	res = launch("homestead.local")
	if _, _, err := sessions.Exchange(strings.TrimPrefix(res.URL, "https://homestead.local/apps/gridcraft/_homestead/open.html#code="), "gridcraft", "homestead.local"); err != nil {
		t.Fatalf("exchange on the launch name: %v", err)
	}
	// A name that is not the route's binds to the route's first name.
	if res = launch("evil.example"); !strings.HasPrefix(res.URL, "https://homestead.lan/apps/gridcraft/") {
		t.Fatalf("foreign host url %s", res.URL)
	}
}
