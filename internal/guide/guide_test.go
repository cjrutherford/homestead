package guide

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func files() fstest.MapFS {
	return fstest.MapFS{
		"index.html":      {Data: []byte("<h1>Guide</h1>")},
		"apps.html":       {Data: []byte("<h1>Apps</h1>")},
		"img/a.png":       {Data: []byte("png")},
		"sub/index.html":  {Data: []byte("<h1>Sub</h1>")},
		"sub/.hidden.txt": {Data: []byte("x")},
	}
}

func get(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestServesPagesWithHeaders(t *testing.T) {
	h := New(files())
	for target, want := range map[string]string{"/guide/": "Guide", "/guide/apps.html": "Apps", "/guide/sub/": "Sub"} {
		rec := get(h, http.MethodGet, target)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("GET %s = %d %q", target, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Fatalf("GET %s content type %q", target, ct)
		}
		if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") ||
			rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("GET %s headers = %v", target, rec.Header())
		}
	}
	if rec := get(h, http.MethodHead, "/guide/"); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Fatalf("HEAD = %d body %d", rec.Code, rec.Body.Len())
	}
}

func TestUnknownAndTraversalAre404(t *testing.T) {
	h := New(files())
	for _, target := range []string{"/guide/missing.html", "/guide/sub", "/guide/%2e%2e/etc/passwd", "/guide/img/"} {
		if rec := get(h, http.MethodGet, target); rec.Code != 404 {
			t.Fatalf("GET %s = %d, want 404 (no listings, no SPA fallback)", target, rec.Code)
		}
	}
}

func TestRejectsWrites(t *testing.T) {
	if rec := get(New(files()), http.MethodPost, "/guide/"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", rec.Code)
	}
}

func TestAvailableAndNoEmbed(t *testing.T) {
	if !Available(files()) || Available(fstest.MapFS{}) {
		t.Fatal("Available wrong")
	}
	if _, ok := Embedded(); ok {
		t.Fatal("Embedded() = true without the embeddash tag")
	}
}
