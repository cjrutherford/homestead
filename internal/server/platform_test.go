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
	"syscall"
	"testing"

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/debianupdate"
	"homestead/internal/doctor"
	"homestead/internal/platformapply"
	"homestead/internal/release"
	"homestead/internal/store"
	"homestead/internal/update"
	"homestead/internal/version"
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

type platformPackageRunner struct {
	installed bool
	current   string
	candidate string
}

func (f *platformPackageRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	switch name {
	case "/usr/bin/dpkg-query":
		if !f.installed {
			return []byte("dpkg-query: no packages found matching homestead"), fmt.Errorf("exit status 1")
		}
		return []byte("installed\n" + f.current + "\namd64\n"), nil
	case "/usr/bin/dpkg-deb":
		return []byte("Package: homestead\nVersion: " + f.candidate + "\nArchitecture: amd64\n"), nil
	case "/usr/bin/dpkg":
		return []byte("amd64\n"), nil
	case "/usr/bin/apt-get":
		return []byte("Inst homestead [" + f.current + "] (" + f.candidate + " local-deb [amd64])\nConf homestead (" + f.candidate + " local-deb [amd64])\n"), nil
	default:
		return nil, fmt.Errorf("unexpected executable %s", name)
	}
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
	env.h = &platformHandlers{s: srv, rel: release.NewStore(st), audit: audit.New(st), trustYAML: filepath.Join(dir, "platform-trust.yaml"), marks: platformapply.NewStore(st), debian: debianupdate.Manager{Runner: &platformPackageRunner{}}}
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
	return e.releaseFor("homestead-server", version, counter)
}

// releaseFor is release for any component; the file carries the component's name.
func (e *platformEnv) releaseFor(component, version string, counter int) verifyBody {
	e.t.Helper()
	dir, err := os.MkdirTemp(e.dir, "rel")
	if err != nil {
		e.t.Fatal(err)
	}
	artifact := filepath.Join(dir, component)
	content := []byte("daemon " + version)
	sum := sha256.Sum256(content)
	sums := []byte(fmt.Sprintf("version=%s\ncounter=%d\n%s  %s\n", version, counter, hex.EncodeToString(sum[:]), component))
	sig := ed25519.Sign(e.priv, sums)
	raw := append(append([]byte("Ed"), e.keyID[:]...), sig...)
	global := ed25519.Sign(e.priv, append(append([]byte(nil), sig...), []byte("t")...))
	sigText := "untrusted comment: s\n" + base64.StdEncoding.EncodeToString(raw) + "\ntrusted comment: t\n" + base64.StdEncoding.EncodeToString(global) + "\n"
	for name, data := range map[string][]byte{component: content, "SUMS": sums, "SUMS.minisig": []byte(sigText)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
	return verifyBody{Component: component, Artifact: artifact,
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

// The first successful verify of a release equal to the running version
// seeds the installed baseline; a later older counter is then refused.
func TestPlatformSeedsInstalledBaselineThenRefusesOlder(t *testing.T) {
	e := newPlatformEnv(t)
	e.trust()
	old := version.Version
	version.Version = "3.0.0"
	t.Cleanup(func() { version.Version = old })

	if in, _ := e.h.rel.Installed("homestead-server"); in.Counter != 0 {
		t.Fatalf("installed before = %+v", in)
	}
	rec, _ := e.verify(e.release("3.0.0", 30))
	if rec.State != release.StateVerified {
		t.Fatalf("running-version verify: %+v", rec)
	}
	if in, _ := e.h.rel.Installed("homestead-server"); in.Version != "3.0.0" || in.Counter != 30 {
		t.Fatalf("baseline not seeded: %+v", in)
	}
	rec, _ = e.verify(e.release("2.0.0", 20))
	if rec.State != release.StateFailed || rec.Refusal != release.RefusalRollback {
		t.Fatalf("older counter must be refused after seeding: %+v", rec)
	}
	// A release that is not the running version never seeds or moves it.
	if err := e.h.rel.RecordInstalled("homestead-server", "3.0.0", 30); err != nil {
		t.Fatal(err)
	}
	if rec, _ = e.verify(e.release("4.0.0", 40)); rec.State != release.StateVerified {
		t.Fatalf("newer: %+v", rec)
	}
	if in, _ := e.h.rel.Installed("homestead-server"); in.Counter != 30 {
		t.Fatalf("a non-running release changed the baseline: %+v", in)
	}
}

// The running version is the daemon's: a CLI release carrying it must not
// seed the CLI's baseline (found in KVM: the CLI then read as installed).
func TestPlatformRunningVersionSeedsOnlyTheDaemon(t *testing.T) {
	e := newPlatformEnv(t)
	e.trust()
	old := version.Version
	version.Version = "3.0.0"
	t.Cleanup(func() { version.Version = old })
	if rec, _ := e.verify(e.releaseFor("homesteadctl", "3.0.0", 30)); rec.State != release.StateVerified {
		t.Fatalf("%+v", rec)
	}
	if in, _ := e.h.rel.Installed("homesteadctl"); in.Counter != 0 {
		t.Fatalf("CLI baseline seeded from the daemon's version: %+v", in)
	}
}

func TestPackageVerifyReturnsArchitectureBoundAptPlanAndModeGuards(t *testing.T) {
	e := newPlatformEnv(t)
	e.trust()
	runner := &platformPackageRunner{installed: true, current: "0.1.0-1", candidate: "0.2.0-1"}
	e.h.debian = debianupdate.Manager{Runner: runner}
	body := e.releaseFor(update.ComponentDeb, "0.2.0", 2)
	raw, _ := json.Marshal(body)
	rec := opRequest(http.MethodPost, string(raw), e.h.verify, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("package verify = %d: %s", rec.Code, rec.Body.String())
	}
	var result struct {
		Component   string                `json:"component"`
		State       string                `json:"state"`
		PackagePlan *debianupdate.Preview `json:"package_plan"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Component != update.ComponentDeb || result.State != release.StateVerified || result.PackagePlan == nil || len(result.PackagePlan.Digest) != 64 || result.PackagePlan.Architecture != "amd64" || len(result.PackagePlan.Actions) != 2 {
		t.Fatalf("verify response = %+v", result)
	}
	view := e.planView()
	if view.Running[update.ComponentDeb] != "0.1.0" || len(view.Plan.Components) != 1 || view.Plan.Components[0].Component != update.ComponentDeb {
		t.Fatalf("package-mode plan = %+v", view)
	}
	if _, code := e.verify(e.release("0.3.0", 3)); code != http.StatusConflict {
		t.Fatalf("source binary verification in package mode = %d, want conflict", code)
	}
}

// A verified release for another version does not seed the baseline.
func TestPlatformNonRunningVersionDoesNotSeed(t *testing.T) {
	e := newPlatformEnv(t)
	e.trust()
	if rec, _ := e.verify(e.release("9.9.9", 90)); rec.State != release.StateVerified {
		t.Fatalf("%+v", rec)
	}
	if in, _ := e.h.rel.Installed("homestead-server"); in.Counter != 0 {
		t.Fatalf("seeded from a non-running version: %+v", in)
	}
}

// Failed and rollback-refused releases are listed as refused with a
// reason and never reach the planner.
func TestPlatformPlanExcludesUnverifiedReleases(t *testing.T) {
	e := newPlatformEnv(t)
	e.trust()
	if err := e.h.rel.RecordInstalled("homestead-server", "5.0.0", 50); err != nil {
		t.Fatal(err)
	}
	// Rollback-refused: signed, counter below installed, version newer
	// than running so the planner would otherwise take it.
	body := e.release("6.0.0", 10)
	if rec, _ := e.verify(body); rec.Refusal != release.RefusalRollback {
		t.Fatalf("setup: %+v", rec)
	}
	view := e.planView()
	if len(view.Plan.Components) != 0 {
		t.Fatalf("a refused release is plannable: %+v", view.Plan)
	}
	if len(view.Refused) != 1 || !strings.Contains(view.Refused[0].PlanError, "refused: failed") ||
		!strings.Contains(view.Refused[0].PlanError, "older") {
		t.Fatalf("refused = %+v", view.Refused)
	}
	// Tampered artifact (authenticity failure): also refused, no plan.
	body = e.release("7.0.0", 70)
	if err := os.WriteFile(body.Artifact, []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.verify(body)
	view = e.planView()
	if len(view.Plan.Components) != 0 || len(view.Refused) != 1 {
		t.Fatalf("view = %+v", view)
	}
}

func TestPlatformVerifyRefusesSpecialFilesGenerically(t *testing.T) {
	e := newPlatformEnv(t)
	e.trust()
	body := e.release("1.0.0", 1)
	fifo := filepath.Join(e.dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	link := filepath.Join(e.dir, "link")
	if err := os.Symlink(body.Artifact, link); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"fifo": fifo, "symlink": link, "missing": filepath.Join(e.dir, "secret-missing-name")} {
		b := body
		b.Signature = path
		raw, _ := json.Marshal(b)
		rec := opRequest(http.MethodPost, string(raw), e.h.verify, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d", name, rec.Code)
		}
		if msg := rec.Body.String(); strings.Contains(msg, e.dir) || strings.Contains(msg, "no such file") || strings.Contains(msg, "permission") {
			t.Errorf("%s: response leaks filesystem detail: %q", name, msg)
		}
	}
	if releases, _ := e.h.rel.Releases(); len(releases) != 0 {
		t.Fatalf("input refusals must not be recorded as verdicts: %+v", releases)
	}
}

func TestPlatformPlaceholderKeyMarkedAndDoctorWarns(t *testing.T) {
	e := newPlatformEnv(t)
	rec := httptest.NewRecorder()
	e.h.listKeys(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	var keys []release.TrustKey
	_ = json.Unmarshal(rec.Body.Bytes(), &keys)
	if len(keys) != 1 || !keys[0].Placeholder {
		t.Fatalf("keys = %+v", keys)
	}
	checks := platformDoctorCheck(e.st)()
	if len(checks) != 1 || checks[0].Status != doctor.StatusWarn || !strings.Contains(checks[0].Detail, "placeholder") {
		t.Fatalf("doctor with only placeholder = %+v", checks)
	}
	e.trust()
	if checks := platformDoctorCheck(e.st)(); checks[0].Status != doctor.StatusOK {
		t.Fatalf("doctor with owner key = %+v", checks)
	}
	// Removing every key warns again.
	for _, k := range func() []release.TrustKey { ks, _ := e.h.rel.Keys(); return ks }() {
		if err := e.h.rel.RemoveKey(k.KeyID); err != nil {
			t.Fatal(err)
		}
	}
	if checks := platformDoctorCheck(e.st)(); checks[0].Status != doctor.StatusWarn {
		t.Fatalf("doctor with no keys = %+v", checks)
	}
}

// A YAML render failure is audited as an error, not as ok.
func TestPlatformKeyChangeRenderFailureAuditedAsError(t *testing.T) {
	e := newPlatformEnv(t)
	e.h.trustYAML = filepath.Join(e.dir, "missing-dir", "platform-trust.yaml")
	raw, _ := json.Marshal(keyBody{PublicKey: e.pubText(), Label: "x"})
	if rec := opRequest(http.MethodPost, string(raw), e.h.addKey, ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d", rec.Code)
	}
	entries, _ := e.h.audit.List(20)
	for _, entry := range entries {
		if entry.Action == "platform.trust.add" {
			if entry.Outcome != audit.OutcomeError || !strings.Contains(entry.Detail, "render failed") {
				t.Fatalf("audit = %+v", entry)
			}
			return
		}
	}
	t.Fatal("no audit entry")
}
