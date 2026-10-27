package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type peopleStub struct {
	mu       sync.Mutex
	calls    []string
	bodies   []map[string]any
	tokenKep bool
	url      string
}

func newPeopleStub(t *testing.T, tokenKept bool) *peopleStub {
	t.Helper()
	s := &peopleStub{tokenKep: tokenKept}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.calls = append(s.calls, r.Method+" "+r.URL.Path)
		s.bodies = append(s.bodies, body)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		person := `{"id":4,"display_name":"Sam Lee","username":"sam","state":"active","grants":[{"app":"immich","role":"user"}],"status":"ok"}`
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/people":
			if !s.tokenKep {
				_, _ = w.Write([]byte(`{"token_kept":false,"people":[],"unmanaged":[],"apps":[],"owner":"chris","reason":"no-token","token_words":"It can create people."}`))
				return
			}
			_, _ = w.Write([]byte(`{"token_kept":true,"owner":"chris","apps":["immich"],"people":[` + person + `,{"id":5,"display_name":"Pat","username":"pat","state":"active","grants":[],"status":"drift","drift":[{"kind":"grant","app":"immich","homestead":"user","authentik":"none"}]}],"unmanaged":[{"username":"old","name":"Old One","active":true}]}`))
		case r.URL.Path == "/api/v1/people" && r.Method == "POST":
			_, _ = w.Write([]byte(`{"person":` + person + `,"revealed":{"kind":"password","value":"s3cretGenerated"}}`))
		case strings.HasSuffix(r.URL.Path, "/password-reset") && body["password"] != nil && body["password"] != "":
			_, _ = w.Write([]byte(`{"revealed":null}`))
		case strings.HasSuffix(r.URL.Path, "/password-reset"):
			_, _ = w.Write([]byte(`{"revealed":{"kind":"link","value":"https://x/recover/abc"}}`))
		case strings.HasSuffix(r.URL.Path, "/mfa-reset"):
			_, _ = w.Write([]byte(`{"removed":["totp"],"skipped":["webauthn"]}`))
		case strings.HasSuffix(r.URL.Path, "/resolve"):
			_, _ = w.Write([]byte(`{"revealed":null}`))
		case strings.Contains(r.URL.Path, "/apps/") || r.Method == "PATCH" || strings.HasSuffix(r.URL.Path, "/adopt"):
			_, _ = w.Write([]byte(`{"person":` + person + `}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	t.Cleanup(ts.Close)
	s.url = ts.URL
	return s
}

func (s *peopleStub) has(call string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.calls {
		if c == call {
			return true
		}
	}
	return false
}

func (s *peopleStub) last() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bodies[len(s.bodies)-1]
}

func TestPeopleListShowsOwnerGrantsDriftAndUnmanaged(t *testing.T) {
	s := newPeopleStub(t, true)
	code, out, _ := invoke(t, []string{"--server", s.url, "people", "list"})
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"chris (owner): every app", "Sam Lee (sam)", "immich", "drift: app immich", "not managed: old", "people adopt old"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestPeopleListNoTokenExplainsAndOffersOptIn(t *testing.T) {
	s := newPeopleStub(t, false)
	code, out, _ := invoke(t, []string{"--server", s.url, "people", "list"})
	if code != exitOK || !strings.Contains(out, "no kept Authentik API token") || !strings.Contains(out, "--keep-token") || !strings.Contains(out, "It can create people.") {
		t.Fatalf("exit %d out %q", code, out)
	}
	code, _, errOut := invoke(t, []string{"--server", s.url, "people", "disable", "sam"})
	if code != exitError || !strings.Contains(errOut, "kept Authentik API token") || !strings.Contains(errOut, "--keep-token") {
		t.Fatalf("exit %d err %q", code, errOut)
	}
}

func TestPeopleAddPrintsSecretToStdoutOnlyAndSendsConfirm(t *testing.T) {
	s := newPeopleStub(t, true)
	code, out, errOut := invoke(t, []string{"--server", s.url, "people", "add", "--name", "Sam Lee", "--username", "sam",
		"--email", "sam@example.com", "--app", "immich", "--app", "jellyfin:admin", "--generate"})
	if code != exitOK {
		t.Fatalf("exit %d err %q", code, errOut)
	}
	if strings.TrimSpace(out) != "s3cretGenerated" || strings.Contains(errOut, "s3cretGenerated") {
		t.Fatalf("out %q err %q", out, errOut)
	}
	b := s.last()
	if b["confirm"] != true || b["method"] != "generated" || b["username"] != "sam" {
		t.Fatalf("body %v", b)
	}
	grants, _ := b["grants"].([]any)
	if len(grants) != 2 || grants[1].(map[string]any)["role"] != "admin" {
		t.Fatalf("grants %v", b["grants"])
	}
}

func TestPeopleAddNeedsNameAndUsername(t *testing.T) {
	s := newPeopleStub(t, true)
	if code, _, _ := invoke(t, []string{"--server", s.url, "people", "add", "--name", "x"}); code != exitUsage {
		t.Fatalf("exit %d", code)
	}
}

func TestPeoplePasswordFileMustBePrivateAndNeverInArgv(t *testing.T) {
	s := newPeopleStub(t, true)
	dir := t.TempDir()
	open := filepath.Join(dir, "open")
	if err := os.WriteFile(open, []byte("a-long-password-123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := invoke(t, []string{"--server", s.url, "people", "reset-password", "sam", "--password-file", open})
	if code != exitError || !strings.Contains(errOut, "chmod 600") {
		t.Fatalf("exit %d err %q", code, errOut)
	}
	private := filepath.Join(dir, "private")
	if err := os.WriteFile(private, []byte("a-long-password-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := invoke(t, []string{"--server", s.url, "people", "reset-password", "sam", "--password-file", private})
	if code != exitOK || strings.TrimSpace(out) != "" {
		t.Fatalf("exit %d out %q", code, out)
	}
	if b := s.last(); b["password"] != "a-long-password-123" || b["confirm"] != true {
		t.Fatalf("body %v", b)
	}
	if code, _, _ := invoke(t, []string{"--server", s.url, "people", "reset-password", "sam", "--link", "--generate"}); code != exitError {
		t.Fatalf("both methods: exit %d", code)
	}
}

func TestPeopleResetPasswordLink(t *testing.T) {
	s := newPeopleStub(t, true)
	code, out, _ := invoke(t, []string{"--server", s.url, "people", "reset-password", "sam", "--link"})
	if code != exitOK || strings.TrimSpace(out) != "https://x/recover/abc" || !s.has("POST /api/v1/people/4/password-reset") {
		t.Fatalf("exit %d out %q calls %v", code, out, s.calls)
	}
}

func TestPeopleVerbsReachTheirRoutes(t *testing.T) {
	s := newPeopleStub(t, true)
	for _, c := range []struct {
		args []string
		call string
	}{
		{[]string{"edit", "sam", "--email", "n@example.com"}, "PATCH /api/v1/people/4"},
		{[]string{"reset-mfa", "4"}, "POST /api/v1/people/4/mfa-reset"},
		{[]string{"disable", "sam"}, "POST /api/v1/people/4/disable"},
		{[]string{"enable", "sam"}, "POST /api/v1/people/4/enable"},
		{[]string{"remove", "sam"}, "POST /api/v1/people/4/remove"},
		{[]string{"adopt", "old"}, "POST /api/v1/people/adopt"},
		{[]string{"grant", "sam", "immich"}, "PUT /api/v1/people/4/apps/immich"},
		{[]string{"grant", "sam", "immich", "--admin"}, "PUT /api/v1/people/4/apps/immich/admin"},
		{[]string{"revoke", "sam", "immich"}, "POST /api/v1/people/4/apps/immich/revoke"},
		{[]string{"resolve", "pat", "put-back"}, "POST /api/v1/people/5/resolve"},
		{[]string{"resolve", "pat", "recreate", "--generate"}, "POST /api/v1/people/5/resolve"},
	} {
		code, _, errOut := invoke(t, append([]string{"--server", s.url, "people"}, c.args...))
		if code != exitOK || !s.has(c.call) {
			t.Errorf("%v: exit %d err %q calls %v", c.args, code, errOut, s.calls)
		}
		if b := s.last(); b["confirm"] != true {
			t.Errorf("%v: no confirm in %v", c.args, b)
		}
	}
	if b := s.last(); b["action"] != "recreate" || b["method"] != "generated" {
		t.Errorf("resolve body %v", b)
	}
}

func TestPeopleUsageErrors(t *testing.T) {
	s := newPeopleStub(t, true)
	for _, args := range [][]string{{}, {"nope"}, {"grant", "sam"}, {"revoke", "sam", "immich", "--admin"}, {"resolve", "pat", "bad"}, {"edit", "sam"}, {"remove"}} {
		if code, _, _ := invoke(t, append([]string{"--server", s.url, "people"}, args...)); code != exitUsage {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	if code, _, _ := invoke(t, []string{"--server", s.url, "people", "remove", "ghost"}); code != exitError {
		t.Errorf("unknown person: exit %d", code)
	}
}
