package everything

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
)

const pubOrigin = "https://homestead.example.org"

func (e *linkEnv) post(origin, body string) (int, string) {
	req := httptest.NewRequest("POST", "https://x/bridge", bytes.NewBufferString(body))
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// S433: a request from the public name is refused with read_links_not_exposed
// until the owner approved exposure; then it may mint, and nothing else.
func TestPublicOriginMintsOnlyWhenExposed(t *testing.T) {
	e := newLinkEnv(t)
	lnkPut(t, e.store, gAlice, "f.txt", "x")
	mint := `{"operation":"fs.getReadURL","key":"f.txt"}`

	// Not approved: read_links_not_exposed, and no link is created.
	if st, out := e.post(pubOrigin, mint); st != 403 || !strings.Contains(out, "read_links_not_exposed") {
		t.Fatalf("unapproved: %d %s", st, out)
	}
	if ls, _ := e.store.ReadLinks(t.Context(), "tasks", true, e.now); len(ls) != 0 {
		t.Fatalf("unapproved request minted %d", len(ls))
	}
	// Unknown, private, plain-http and malformed origins are plain 403.
	for _, o := range []string{"https://evil.lan", "http://homestead.example.org", "https://10.0.0.5", "https://host", "", "https://a.example.org/x", "null"} {
		if st, out := e.post(o, mint); st != 403 || strings.Contains(out, "read_links_not_exposed") {
			t.Errorf("origin %q: %d %s", o, st, out)
		}
	}

	// Approved by the environment value.
	if err := e.store.SetPublicReadOrigin(pubOrigin); err != nil {
		t.Fatal(err)
	}
	st, out := e.post(pubOrigin, mint)
	if st != 200 || !strings.Contains(out, pubOrigin+"/apps/tasks"+ReadPrefix) {
		t.Fatalf("approved: %d %s", st, out)
	}
	// Another public name is still not exposed.
	if st, out := e.post("https://other.example.org", mint); st != 403 || !strings.Contains(out, "read_links_not_exposed") {
		t.Fatalf("other public origin: %d %s", st, out)
	}
	// The public origin can do nothing else, whatever the operation.
	for _, body := range []string{
		`{"operation":"whoami"}`,
		`{"operation":"kv.get","kind":"kv","key":"a"}`,
		`{"kind":"file","operation":"put","key":"x","value":"eA=="}`,
		`{"operation":"fs.share","key":"f.txt","recipient":"bob"}`,
		`{"operation":"events.subscribe"}`,
	} {
		if st, out := e.post(pubOrigin, body); st != 403 || strings.Contains(out, "url") {
			t.Errorf("%s from the public origin: %d %s", body, st, out)
		}
	}
	// The local origin still works, and since S465 the link it mints is on the
	// approved public origin so it opens for outsiders.
	if st, out := e.post(lnkOrigin, mint); st != 200 || !strings.Contains(out, pubOrigin+"/apps/tasks"+ReadPrefix) {
		t.Fatalf("local: %d %s", st, out)
	}
	// Removal (empty value on restart) closes it again.
	if err := e.store.SetPublicReadOrigin(""); err != nil {
		t.Fatal(err)
	}
	if st, out := e.post(pubOrigin, mint); st != 403 || !strings.Contains(out, "read_links_not_exposed") {
		t.Fatalf("after removal: %d %s", st, out)
	}
}

func TestPublicOriginStillNeedsASession(t *testing.T) {
	e := newLinkEnv(t)
	_ = e.store.SetPublicReadOrigin(pubOrigin)
	e.h.identity = noCSRF{e.id}
	if st, out := e.post(pubOrigin, `{"operation":"fs.getReadURL","key":"f.txt"}`); st != 403 || strings.Contains(out, "url") {
		t.Fatalf("no csrf: %d %s", st, out)
	}
}

func TestSetPublicReadOriginValidation(t *testing.T) {
	s, _ := grantStore(t)
	for _, bad := range []string{"http://a.example.org", "https://a.example.org/", "https://a.example.org/x", "https://user@a.example.org",
		"https://*.example.org", "https://homestead.lan", "https://10.1.1.1", "https://localhost", "a.example.org", "https://a.example.org:443?x"} {
		if err := s.SetPublicReadOrigin(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	for _, good := range []string{"https://homestead.mydomain.net", "https://home.mydomain.net:8443"} {
		if err := s.SetPublicReadOrigin(good); err != nil {
			t.Errorf("refused %q: %v", good, err)
		}
	}
}

// What the tunnel entry forwards: exactly one address, the one Cloudflare
// saw. With the edge as the trusted peer it is the limiter's client; a
// missing value falls back to the edge itself rather than a client string.
func TestReadRouteTunnelForwardedAddress(t *testing.T) {
	s, _ := grantStore(t)
	trusted := s.trustedNets()
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "172.18.0.3:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.77")
	if got := clientAddr(req, trusted); got != "203.0.113.77" {
		t.Fatalf("edge-set address = %q", got)
	}
	req.Header.Set("X-Forwarded-For", "")
	if got := clientAddr(req, trusted); got != "172.18.0.3" {
		t.Fatalf("empty forwarded value = %q", got)
	}
	// Anything a client put before the edge's value is ignored.
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.78")
	if got := clientAddr(req, trusted); got != "203.0.113.78" {
		t.Fatalf("client-prepended value won: %q", got)
	}
}
