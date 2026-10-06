package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testFiles() fstest.MapFS {
	return fstest.MapFS{
		"index.html":  {Data: []byte("<title>Homestead</title>")},
		"main-ABC.js": {Data: []byte("console.log(1)")},
	}
}

func TestServesIndexAtRoot(t *testing.T) {
	h := New(testFiles())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", cc)
	}
	if !strings.Contains(rec.Body.String(), "Homestead") {
		t.Fatalf("body = %q, want shell", rec.Body.String())
	}
}

func TestServesHashedAssets(t *testing.T) {
	h := New(testFiles())
	req := httptest.NewRequest(http.MethodGet, "/main-ABC.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("Content-Type = %q, want javascript", ct)
	}
	if rec.Body.String() != "console.log(1)" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestClientRoutesFallBackToIndex(t *testing.T) {
	h := New(testFiles())
	for _, target := range []string{"/status", "/api/v1/config", "/favicon.ico"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Homestead") {
			t.Fatalf("GET %s = %d %q, want index fallback", target, rec.Code, rec.Body.String())
		}
	}
}

func TestRejectsWrites(t *testing.T) {
	h := New(testFiles())
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", rec.Code)
	}
}

func TestMissingIndexIsUnavailable(t *testing.T) {
	h := New(fstest.MapFS{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("GET status = %d, want 500", rec.Code)
	}
}

func TestEmbeddedAbsentWithoutTag(t *testing.T) {
	if _, ok := Embedded(); ok {
		t.Fatal("Embedded() = true without embeddash tag, want false")
	}
}
