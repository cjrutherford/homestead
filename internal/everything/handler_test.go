package everything

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type fakeIdentity struct {
	scope Scope
	err   error
	csrf  bool
}

func (f fakeIdentity) Authenticate(*http.Request) (Scope, error) { return f.scope, f.err }
func (f fakeIdentity) CheckCSRF(*http.Request) bool              { return f.csrf }

func TestBridgeBoundaryRequiresAuthenticationOriginAndCSRF(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scope := Scope{App: "tasks", Subject: "alice"}
	for _, tc := range []struct {
		name, origin, fetch string
		auth                fakeIdentity
		want                int
	}{
		{"no session", "https://home.example", "same-origin", fakeIdentity{err: errors.New("no")}, http.StatusUnauthorized},
		{"untrusted origin", "https://evil.example", "cross-site", fakeIdentity{scope: scope, csrf: true}, http.StatusForbidden},
		{"missing origin", "", "same-origin", fakeIdentity{scope: scope, csrf: true}, http.StatusForbidden},
		{"cross-site metadata", "https://home.example", "cross-site", fakeIdentity{scope: scope, csrf: true}, http.StatusForbidden},
		{"csrf failure", "https://home.example", "same-origin", fakeIdentity{scope: scope}, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := NewHandler(store, tc.auth, []string{"https://home.example"})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("POST", "https://home.example/bridge", strings.NewReader(`{"operation":"get","kind":"kv","key":"x"}`))
			req.Header.Set("Content-Type", "application/json")
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			req.Header.Set("Sec-Fetch-Site", tc.fetch)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("unsafe headers")
			}
		})
	}
}

func TestBridgeUsesVerifiedScopeAndRejectsClientIdentity(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	alice := fakeIdentity{scope: Scope{App: "tasks", Subject: "alice"}, csrf: true}
	bob := fakeIdentity{scope: Scope{App: "tasks", Subject: "bob"}, csrf: true}
	call := func(auth fakeIdentity, body string) *httptest.ResponseRecorder {
		h, e := NewHandler(store, auth, []string{"https://home.example"})
		if e != nil {
			t.Fatal(e)
		}
		req := httptest.NewRequest("POST", "https://home.example/bridge", bytes.NewBufferString(body))
		req.Header.Set("Origin", "https://home.example")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := call(alice, `{"operation":"put","kind":"kv","key":"x","value":"c2VjcmV0"}`); rec.Code != 200 {
		t.Fatalf("put %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(bob, `{"operation":"get","kind":"kv","key":"x"}`); rec.Code != 404 {
		t.Fatalf("cross-user %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(bob, `{"operation":"get","kind":"kv","key":"x","subject":"alice"}`); rec.Code != 400 {
		t.Fatalf("forged subject %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(alice, `{"operation":"get","kind":"kv","key":"x"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), "c2VjcmV0") {
		t.Fatalf("get %d %s", rec.Code, rec.Body.String())
	}
}

func TestBridgeRejectsInvalidConfigurationAndRequests(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	auth := fakeIdentity{scope: Scope{App: "tasks", Subject: "alice"}, csrf: true}
	for _, origin := range []string{"*", "https://home.example/path", "https://user@home.example", "https://*.example", "https://home.example?x=1"} {
		if _, err := NewHandler(store, auth, []string{origin}); err == nil {
			t.Fatalf("accepted origin %q", origin)
		}
	}
	if _, err := NewHandler(store, nil, []string{"https://home.example"}); err == nil {
		t.Fatal("accepted missing identity")
	}
	var nilIdentity *fakeIdentity
	if _, err := NewHandler(store, nilIdentity, []string{"https://home.example"}); err == nil {
		t.Fatal("accepted typed nil identity")
	}
	h, err := NewHandler(store, auth, []string{"https://home.example"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, content, body string
		want                  int
	}{
		{"GET", "application/json", `{"operation":"get","kind":"kv","key":"x"}`, 405},
		{"POST", "text/plain", `{"operation":"get","kind":"kv","key":"x"}`, 400},
		{"POST", "application/json", `{"operation":"get","kind":"kv","key":"x"} {}`, 400},
		{"POST", "application/json", `{"operation":"get","kind":"file","key":"../x"}`, 400},
	} {
		req := httptest.NewRequest(tc.method, "https://home.example/bridge", strings.NewReader(tc.body))
		req.Header.Set("Origin", "https://home.example")
		req.Header.Set("Content-Type", tc.content)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s %q: got %d, want %d", tc.method, tc.body, rec.Code, tc.want)
		}
	}
}
