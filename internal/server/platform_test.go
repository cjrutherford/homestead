package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/release"
	"homestead/internal/store"
)

type platformEnv struct {
	t     *testing.T
	h     *platformHandlers
	st    *store.Store
	dir   string
	pub   ed25519.PublicKey
	priv  ed25519.PrivateKey
	keyID [8]byte
}

func newPlatformEnv(t *testing.T) *platformEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := New("127.0.0.1:0")
	env := &platformEnv{t: t, st: st, dir: dir}
	env.pub, env.priv, _ = ed25519.GenerateKey(rand.Reader)
	_, _ = rand.Read(env.keyID[:])
	env.h = &platformHandlers{s: srv, rel: release.NewStore(st), audit: audit.New(st), trustYAML: filepath.Join(dir, "platform-trust.yaml")}
	return env
}

func (e *platformEnv) pubText() string {
	b := append([]byte("Ed"), e.keyID[:]...)
	b = append(b, e.pub...)
	return "untrusted comment: test\n" + base64.StdEncoding.EncodeToString(b) + "\n"
}

// release writes artifact, checksums, and signature files; returns a verify body.
func (e *platformEnv) release(version string, counter int) verifyBody {
	e.t.Helper()
	dir, err := os.MkdirTemp(e.dir, "rel")
	if err != nil {
		e.t.Fatal(err)
	}
	artifact := filepath.Join(dir, "homestead-server")
	content := []byte("daemon " + version)
	sum := sha256.Sum256(content)
	sums := []byte(fmt.Sprintf("version=%s\ncounter=%d\n%s  homestead-server\n", version, counter, hex.EncodeToString(sum[:])))
	sig := ed25519.Sign(e.priv, sums)
	raw := append(append([]byte("Ed"), e.keyID[:]...), sig...)
	global := ed25519.Sign(e.priv, append(append([]byte(nil), sig...), []byte("t")...))
	sigText := "untrusted comment: s\n" + base64.StdEncoding.EncodeToString(raw) + "\ntrusted comment: t\n" + base64.StdEncoding.EncodeToString(global) + "\n"
	for name, data := range map[string][]byte{"homestead-server": content, "SUMS": sums, "SUMS.minisig": []byte(sigText)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
	return verifyBody{Component: "homestead-server", Artifact: artifact,
		Checksums: filepath.Join(dir, "SUMS"), Signature: filepath.Join(dir, "SUMS.minisig")}
}

func opRequest(method, body string, handler http.HandlerFunc, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/x", bytes.NewReader([]byte(body)))
	if id != "" {
		req.SetPathValue("id", id)
	}
	req = req.WithContext(context.WithValue(req.Context(), operatorKey{},
		operatorSession{Token: "t", Session: auth.VerifiedSession{Username: "owner"}}))
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func (e *platformEnv) verify(body verifyBody) (release.Release, int) {
	raw, _ := json.Marshal(body)
	rec := opRequest(http.MethodPost, string(raw), e.h.verify, "")
	var out release.Release
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out, rec.Code
}

func (e *platformEnv) trust() {
	e.t.Helper()
	raw, _ := json.Marshal(keyBody{PublicKey: e.pubText(), Label: "test"})
	if rec := opRequest(http.MethodPost, string(raw), e.h.addKey, ""); rec.Code != http.StatusOK {
		e.t.Fatalf("add key: %d %s", rec.Code, rec.Body.String())
	}
}

func (e *platformEnv) planView() platformPlanView {
	e.t.Helper()
	rec := httptest.NewRecorder()
	e.h.plan(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	var view platformPlanView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		e.t.Fatalf("plan json: %v: %s", err, rec.Body.String())
	}
	return view
}

func TestPlatformVerifyUnknownKeyThenOwnerKey(t *testing.T) {
	e := newPlatformEnv(t)
	body := e.release("0.1.0", 2)
	rec, code := e.verify(body)
	if code != http.StatusOK || rec.State != release.StateFailed || !strings.Contains(rec.Detail, "not trusted") {
		t.Fatalf("unknown key: %d %+v", code, rec)
	}
	if rec.Version != "" {
		t.Fatal("unauthenticated version must not be stored")
	}
	e.trust()
	rec, _ = e.verify(body)
	if rec.State != release.StateVerified || rec.Version != "0.1.0" {
		t.Fatalf("owner key: %+v", rec)
	}
	view := e.planView()
	// 0.1.0 is not newer than the running version, so it is refused by
	// the planner and reported, not hidden.
	if len(view.Releases) != 1 || view.Releases[0].State != release.StateVerified {
		t.Fatalf("view = %+v", view)
	}
	// YAML reflects the owner key.
	data, err := os.ReadFile(filepath.Join(e.dir, "platform-trust.yaml"))
	if err != nil || !strings.Contains(string(data), "source: owner") {
		t.Fatalf("yaml: %v %s", err, data)
	}
}

func TestPlatformPlanReportsVerificationPerRelease(t *testing.T) {
	e := newPlatformEnv(t)
	e.trust()
	body := e.release("9.0.0", 10)
	if rec, _ := e.verify(body); rec.State != release.StateVerified {
		t.Fatalf("verify: %+v", rec)
	}
	view := e.planView()
	if len(view.Plan.Components) != 1 || view.Plan.Components[0].Verification != "verified" {
		t.Fatalf("plan = %+v", view.Plan)
	}
	// Tamper the artifact: the latest record is failed, still reported.
	if err := os.WriteFile(body.Artifact, []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec, _ := e.verify(body); rec.State != release.StateFailed {
		t.Fatalf("tampered: %+v", rec)
	}
	view = e.planView()
	if view.Releases[0].State != "failed" {
		t.Fatalf("view = %+v", view)
	}
}

func TestPlatformRollbackRefusedThenAuditedOverride(t *testing.T) {
	e := newPlatformEnv(t)
	e.trust()
	if err := e.h.rel.RecordInstalled("homestead-server", "2.0.0", 20); err != nil {
		t.Fatal(err)
	}
	body := e.release("1.0.0", 10)
	rec, _ := e.verify(body)
	if rec.State != release.StateFailed || rec.Refusal != release.RefusalRollback {
		t.Fatalf("rollback: %+v", rec)
	}
	body.AllowOlder = true
	rec, _ = e.verify(body)
	if rec.State != release.StateVerified || !rec.Override {
		t.Fatalf("override: %+v", rec)
	}
	entries, err := e.h.audit.List(100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Action == "platform.verify" && entry.Override {
			found = true
			for _, want := range []string{"2.0.0", "20", "1.0.0", "10"} {
				if !strings.Contains(entry.OverrideReason, want) {
					t.Errorf("override reason %q lacks %q", entry.OverrideReason, want)
				}
			}
		}
	}
	if !found {
		t.Fatal("override not audited")
	}
	// Equal counter with a different version is never overridable.
	body = e.release("2.0.1", 20)
	body.AllowOlder = true
	rec, _ = e.verify(body)
	if rec.State != release.StateFailed || rec.Refusal != release.RefusalCounter {
		t.Fatalf("conflict: %+v", rec)
	}
}

func TestPlatformVerifyInputErrors(t *testing.T) {
	e := newPlatformEnv(t)
	e.trust()
	body := e.release("1.0.0", 1)
	if err := os.Remove(body.Artifact); err != nil {
		t.Fatal(err)
	}
	if _, code := e.verify(body); code != http.StatusBadRequest {
		t.Fatalf("missing file code = %d", code)
	}
	body.Artifact = "relative"
	if _, code := e.verify(body); code != http.StatusBadRequest {
		t.Fatalf("relative path code = %d", code)
	}
	body = e.release("1.0.0", 1)
	body.Component = "dashboard"
	if _, code := e.verify(body); code != http.StatusBadRequest {
		t.Fatalf("component code = %d", code)
	}
}

func TestPlatformTrustKeyRoutes(t *testing.T) {
	e := newPlatformEnv(t)
	rec := httptest.NewRecorder()
	e.h.listKeys(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	var keys []release.TrustKey
	_ = json.Unmarshal(rec.Body.Bytes(), &keys)
	if len(keys) != 1 || keys[0].Source != "embedded" {
		t.Fatalf("keys = %+v", keys)
	}
	e.trust()
	raw, _ := json.Marshal(keyBody{PublicKey: e.pubText()})
	if r := opRequest(http.MethodPost, string(raw), e.h.addKey, ""); r.Code != http.StatusConflict {
		t.Fatalf("dup code = %d", r.Code)
	}
	if r := opRequest(http.MethodPost, `{"public_key":"junk"}`, e.h.addKey, ""); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("junk code = %d", r.Code)
	}
	if r := opRequest(http.MethodDelete, "", e.h.removeKey, keys[0].KeyID); r.Code != http.StatusNoContent {
		t.Fatalf("remove embedded code = %d", r.Code)
	}
	if r := opRequest(http.MethodDelete, "", e.h.removeKey, keys[0].KeyID); r.Code != http.StatusNotFound {
		t.Fatalf("remove again code = %d", r.Code)
	}
	entries, _ := e.h.audit.List(100)
	actions := ""
	for _, entry := range entries {
		actions += entry.Action + " "
	}
	if !strings.Contains(actions, "platform.trust.add") || !strings.Contains(actions, "platform.trust.remove") {
		t.Fatalf("audit actions = %s", actions)
	}
}
