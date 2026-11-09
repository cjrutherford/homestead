package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// S494: the owner guide needs a signed-in session; owner and person both read it.
func TestGuideNeedsSignIn(t *testing.T) {
	a := newEnforcedApp(t)
	setup := a.setup(t)
	if c, _ := Classify("GET /guide/"); c != ClassSession {
		t.Fatalf("class = %v, want session", c)
	}
	if rec := a.do("GET", "/guide/", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous /guide/ = %d, want 401", rec.Code)
	}
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(setup.Body.Bytes(), &out)
	if out.Token == "" {
		t.Fatalf("setup returned no token: %s", setup.Body)
	}
	rec := a.do("GET", "/guide/", "", bearer(out.Token))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Guide") ||
		!strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("owner /guide/ = %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	a.addPerson(t, 7, "active")
	sess, err := a.app.Auth.MintPersonSession(7)
	if err != nil {
		t.Fatal(err)
	}
	if rec := a.do("GET", "/guide/", "", bearer(sess.Token)); rec.Code != 200 {
		t.Fatalf("person /guide/ = %d, want 200", rec.Code)
	}
	if rec := a.do("POST", "/guide/", "{}", bearer(out.Token)); rec.Code == 200 {
		t.Fatalf("POST /guide/ = 200")
	}
}
