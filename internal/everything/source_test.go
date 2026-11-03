package everything

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const srcIndex = `<!doctype html><html><head><script>window.a=1</script></head><body class="x"><div id="app"></div></body></html>`

func offerFixture(t *testing.T, offerJSON func(sum string, size int) string) (appDir, srcDir, sum string) {
	t.Helper()
	appDir, srcDir = t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(appDir, "index.html"), []byte(srcIndex), 0o600); err != nil {
		t.Fatal(err)
	}
	tar := []byte("pretend tarball bytes")
	h := sha256.Sum256(tar)
	sum = hex.EncodeToString(h[:])
	if err := os.WriteFile(filepath.Join(srcDir, "notes-source.tar.gz"), tar, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "offer.json"), []byte(offerJSON(sum, len(tar))), 0o600); err != nil {
		t.Fatal(err)
	}
	return
}

func notesOffer(sum string, size int) string {
	return `{"app":"notes","title":"Notes","license":"AGPL-3.0-only","license_url":"https://www.gnu.org/licenses/agpl-3.0.html",
"repository":"https://github.com/HeyPuter/everything","commit":"e7d07c48546ef0650a8f3ab94ae82b8b50d3646a","path":"src/notes",
"tarball":{"name":"notes-source.tar.gz","size":` + itoa(size) + `,"sha256":"` + sum + `"},
"upstream_archive":{"name":"notes-source.tar.gz","sha256":"` + sum + `"},"components":[]}`
}

func itoa(n int) string { return strconv.Itoa(n) }

func serveApp(t *testing.T, appDir, srcDir string, signedIn bool) *http.ServeMux {
	t.Helper()
	t.Setenv(SourceDirEnv, srcDir)
	mux := http.NewServeMux()
	registerApp(mux, "/apps/notes", appDir, fixedSession{ok: signedIn})
	return mux
}

func get(mux *http.ServeMux, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestSourcePageAndTarball(t *testing.T) {
	app, src, sum := offerFixture(t, notesOffer)
	mux := serveApp(t, app, src, true)

	rec := get(mux, "GET", "/apps/notes/_homestead/source")
	if rec.Code != 200 {
		t.Fatalf("page = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"AGPL-3.0-only", "https://github.com/HeyPuter/everything", "e7d07c48546ef0650a8f3ab94ae82b8b50d3646a",
		`href="/apps/notes/_homestead/source/notes-source.tar.gz"`, sum, "https://www.gnu.org/licenses/agpl-3.0.html"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(strings.ToLower(body), "<script") {
		t.Error("source page carries a script")
	}
	h := rec.Header()
	if h.Get("Content-Type") != "text/html; charset=utf-8" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers = %v", h)
	}
	csp := h.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "script-src") || strings.Contains(csp, "'unsafe-eval'") {
		t.Errorf("csp = %q", csp)
	}

	tb := get(mux, "GET", "/apps/notes/_homestead/source/notes-source.tar.gz")
	if tb.Code != 200 || tb.Body.String() != "pretend tarball bytes" {
		t.Fatalf("tarball = %d %q", tb.Code, tb.Body.String())
	}
	if tb.Header().Get("Content-Type") != "application/gzip" || tb.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(tb.Header().Get("Content-Disposition"), "attachment") || tb.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("tarball headers = %v", tb.Header())
	}
	// Unknown names under the route never fall through to a file.
	for _, p := range []string{"/apps/notes/_homestead/source/other.tar.gz", "/apps/notes/_homestead/source/../x", "/apps/notes/_homestead/source/"} {
		if c := get(mux, "GET", p).Code; c != 404 && c != 301 && c != 307 {
			t.Errorf("%s = %d", p, c)
		}
	}
	if c := get(mux, "POST", "/apps/notes/_homestead/source").Code; c != 405 {
		t.Errorf("POST = %d", c)
	}
}

func TestSourceNeedsSignIn(t *testing.T) {
	app, src, _ := offerFixture(t, notesOffer)
	mux := serveApp(t, app, src, false)
	page := get(mux, "GET", "/apps/notes/_homestead/source")
	if page.Code != 302 || !strings.HasSuffix(page.Header().Get("Location"), LoginPath) {
		t.Errorf("page = %d %v", page.Code, page.Header())
	}
	if c := get(mux, "GET", "/apps/notes/_homestead/source/notes-source.tar.gz").Code; c != 401 {
		t.Errorf("tarball = %d", c)
	}
}

func TestNoOfferMeansNotFoundAndNoLink(t *testing.T) {
	app := t.TempDir()
	if err := os.WriteFile(filepath.Join(app, "index.html"), []byte(srcIndex), 0o600); err != nil {
		t.Fatal(err)
	}
	mux := serveApp(t, app, t.TempDir(), true)
	for _, p := range []string{"/apps/notes/_homestead/source", "/apps/notes/_homestead/source/notes-source.tar.gz"} {
		if c := get(mux, "GET", p).Code; c != 404 {
			t.Errorf("%s = %d", p, c)
		}
	}
	if idx := get(mux, "GET", "/apps/notes/").Body.String(); strings.Contains(idx, "homestead-source") || !strings.Contains(idx, "homestead-csrf") {
		t.Errorf("index = %s", idx)
	}
}

func TestIndexGetsSourceLinkWithoutBreakingPolicy(t *testing.T) {
	app, src, _ := offerFixture(t, notesOffer)
	mux := serveApp(t, app, src, true)
	rec := get(mux, "GET", "/apps/notes/")
	body := rec.Body.String()
	link := `<a id="homestead-source" href="/apps/notes/_homestead/source" aria-label="Source code offer for Notes (AGPL-3.0-only)"`
	if !strings.Contains(body, `<body class="x">`+link) || !strings.Contains(body, ">Source</a>") {
		t.Fatalf("link not injected after <body>: %s", body)
	}
	if !strings.Contains(body, `name="homestead-csrf"`) {
		t.Error("csrf meta lost")
	}
	// No new script, no inline handler; the policy is the one computed for the
	// original page (the single inline script hash only).
	csp := rec.Header().Get("Content-Security-Policy")
	if strings.Count(body, "<script") != 1 || strings.Contains(body, " onclick") || strings.Count(csp, "sha256-") != 1 {
		t.Errorf("script surface changed: csp=%s", csp)
	}
}

func TestSourceLinkOffsetIsApplied(t *testing.T) {
	app, src, _ := offerFixture(t, func(sum string, size int) string {
		return strings.Replace(notesOffer(sum, size), `"components":[]`, `"components":[],"link_offset":{"left":"40%","bottom":"4px"}`, 1)
	})
	body := get(serveApp(t, app, src, true), "GET", "/apps/notes/").Body.String()
	if !strings.Contains(body, "position:fixed;bottom:4px;left:40%;") && !strings.Contains(body, "position:fixed;left:40%;bottom:4px;") {
		t.Errorf("offset not applied: %s", body)
	}
	if strings.Contains(body, "right:4px;bottom:4px;") {
		t.Error("default corner used despite an offset")
	}
}

func TestComponentOnlyOfferForLGPL(t *testing.T) {
	app, src, _ := offerFixture(t, func(string, int) string {
		return `{"app":"parametric","title":"Parametric","license":"MIT","repository":"https://github.com/HeyPuter/everything",
"commit":"e7d07c48546ef0650a8f3ab94ae82b8b50d3646a","path":"src/parametric","tarball":null,"upstream_archive":null,
"components":[{"name":"replicad-opencascadejs","license":"LGPL-2.1-only","version":"1.1.0","source_url":"https://github.com/sgenoud/replicad",
"license_file":"licenses/replicad-opencascadejs-LICENSE.txt","note":"kernel"}]}`
	})
	mux := serveApp(t, app, src, true)
	body := get(mux, "GET", "/apps/notes/_homestead/source").Body.String()
	for _, want := range []string{"replicad-opencascadejs 1.1.0 (LGPL-2.1-only)", "https://github.com/sgenoud/replicad", `href="/apps/notes/licenses/replicad-opencascadejs-LICENSE.txt"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if c := get(mux, "GET", "/apps/notes/_homestead/source/notes-source.tar.gz").Code; c != 404 {
		t.Errorf("tarball without declaration = %d", c)
	}
}

func TestMalformedOfferIsIgnored(t *testing.T) {
	for name, mutate := range map[string]func(string) string{
		"javascript url": func(s string) string {
			return strings.Replace(s, "https://github.com/HeyPuter/everything", "javascript:alert(1)", 1)
		},
		"bad commit": func(s string) string {
			return strings.Replace(s, "e7d07c48546ef0650a8f3ab94ae82b8b50d3646a", "main", 1)
		},
		"wrong tarball": func(s string) string {
			return strings.Replace(s, `"name":"notes-source.tar.gz","size"`, `"name":"../x.tar.gz","size"`, 1)
		},
		"wrong size": func(s string) string { return strings.Replace(s, `"size":21`, `"size":22`, 1) },
		"bad offset": func(s string) string {
			return strings.Replace(s, `"components":[]`, `"components":[],"link_offset":{"left":"0;x:y"}`, 1)
		},
		"unknown field": func(s string) string { return strings.Replace(s, `"components":[]`, `"components":[],"x":1`, 1) },
	} {
		app, src, _ := offerFixture(t, func(sum string, size int) string { return mutate(notesOffer(sum, size)) })
		mux := serveApp(t, app, src, true)
		if c := get(mux, "GET", "/apps/notes/_homestead/source").Code; c != 404 {
			t.Errorf("%s: page = %d", name, c)
		}
	}
}
