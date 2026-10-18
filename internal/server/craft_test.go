package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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
	"sync"
	"testing"
	"time"

	"homestead/internal/audit"
	"homestead/internal/craft"
	"homestead/internal/operation"
	"homestead/internal/release"
	"homestead/internal/store"
	"homestead/internal/update"
)

type memBuilder struct {
	mu   sync.Mutex
	tags map[string]string
	n    int
}

func (m *memBuilder) set(tag, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tags[tag] = id
}

func (m *memBuilder) Build(_ context.Context, spec craft.BuildSpec) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.n++
	m.tags[spec.Tag] = fmt.Sprintf("sha256:%064d", m.n)
	return nil
}
func (m *memBuilder) ImageID(_ context.Context, ref string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok := m.tags[ref]; ok {
		return id, nil
	}
	return "", craft.ErrImageMissing
}
func (m *memBuilder) Tag(_ context.Context, src, dst string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok := m.tags[src]; ok {
		src = id
	}
	m.tags[dst] = src
	return nil
}
func (m *memBuilder) Remove(_ context.Context, ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tags, ref)
	return nil
}
func (m *memBuilder) RootDir(context.Context) (string, error) { return "", fmt.Errorf("none") }

func siteTarGz(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := "<html></html>"
	_ = tw.WriteHeader(&tar.Header{Name: "index.html", Mode: 0o644, Size: int64(len(body))})
	_, _ = tw.Write([]byte(body))
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

type craftEnv struct {
	t          *testing.T
	h          *craftHandlers
	st         *store.Store
	dir        string
	catalogDir string
	pub        ed25519.PublicKey
	priv       ed25519.PrivateKey
	keyID      [8]byte
	builder    *memBuilder
	artifact   []byte
}

func newCraftEnv(t *testing.T) *craftEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &craftEnv{t: t, st: st, dir: dir, catalogDir: filepath.Join(dir, "catalog"), builder: &memBuilder{tags: map[string]string{}}, artifact: siteTarGz(t)}
	e.pub, e.priv, _ = ed25519.GenerateKey(rand.Reader)
	_, _ = rand.Read(e.keyID[:])
	ctxDir := filepath.Join(e.catalogDir, "craft", "context")
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ctxDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	keys := release.NewCraftStore(st)
	srv := New("127.0.0.1:0")
	srv.craft = &craft.Pipeline{Keys: keys.PublicKeys, Store: craft.NewStore(st), Builder: e.builder, ContextDir: ctxDir,
		StagingDir: filepath.Join(dir, "staging"),
		FreeBytes:  func(string) (uint64, error) { return 1 << 40, nil },
		Fetch:      nil,
	}
	e.h = &craftHandlers{s: srv, pipe: srv.craft, keys: keys, audit: audit.New(st), runner: operation.NewRunner(st), st: st,
		catalogDir: e.catalogDir, trustYAML: filepath.Join(dir, "craft-trust.yaml")}
	return e
}

func (e *craftEnv) pubText() string {
	b := append([]byte("Ed"), e.keyID[:]...)
	b = append(b, e.pub...)
	return "untrusted comment: test\n" + base64.StdEncoding.EncodeToString(b) + "\n"
}

func (e *craftEnv) trust() {
	e.t.Helper()
	raw, _ := json.Marshal(keyBody{PublicKey: e.pubText(), Label: "test"})
	if rec := opRequest(http.MethodPost, string(raw), e.h.addKey, ""); rec.Code != http.StatusOK {
		e.t.Fatalf("add craft key: %d %s", rec.Code, rec.Body.String())
	}
}

// manifest writes a signed manifest plus a local artifact; returns the request body.
func (e *craftEnv) manifest(version string, counter int64) craftBody {
	e.t.Helper()
	digest, err := craft.TreeDigest(e.h.pipe.ContextDir)
	if err != nil {
		e.t.Fatal(err)
	}
	sum := sha256.Sum256(e.artifact)
	text := fmt.Sprintf("format: %s\ncounter: %d\ncontext_digest: %s\napps:\n  - name: wordcraft\n    version: %s\n    artifact:\n      name: wordcraft-%s.tar.gz\n      sha256: %s\n      size: %d\n",
		craft.ManifestFormat, counter, digest, version, version, hex.EncodeToString(sum[:]), len(e.artifact))
	sig := ed25519.Sign(e.priv, []byte(text))
	raw := append(append([]byte("Ed"), e.keyID[:]...), sig...)
	comment := "t"
	global := ed25519.Sign(e.priv, append(append([]byte(nil), sig...), []byte(comment)...))
	sigText := "untrusted comment: s\n" + base64.StdEncoding.EncodeToString(raw) + "\ntrusted comment: " + comment + "\n" + base64.StdEncoding.EncodeToString(global) + "\n"
	d := e.t.TempDir()
	m := filepath.Join(d, "m.yaml")
	art := filepath.Join(d, "wordcraft.tar.gz")
	for p, b := range map[string]string{m: text, m + ".minisig": sigText, art: string(e.artifact)} {
		if err := os.WriteFile(p, []byte(b), 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
	return craftBody{App: "wordcraft", Manifest: m, Artifact: art}
}

func post(h http.HandlerFunc, v any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(v)
	return opRequest(http.MethodPost, string(raw), h, "")
}

func TestCraftTrustKeyRoutesAreSeparateFromPlatform(t *testing.T) {
	e := newCraftEnv(t)
	rec := httptest.NewRecorder()
	e.h.listKeys(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	var keys []release.TrustKey
	_ = json.Unmarshal(rec.Body.Bytes(), &keys)
	if len(keys) != 1 || keys[0].Source != "embedded" || keys[0].Placeholder || keys[0].KeyID != "B03DB3F5AA66DB3E" {
		t.Fatalf("keys = %+v", keys)
	}
	e.trust()
	if r := post(e.h.addKey, keyBody{PublicKey: e.pubText()}); r.Code != http.StatusConflict {
		t.Fatalf("dup = %d", r.Code)
	}
	if r := opRequest(http.MethodPost, `{"public_key":"junk"}`, e.h.addKey, ""); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("junk = %d", r.Code)
	}
	// The platform class does not trust the craft key.
	platformKeys, _ := release.NewStore(e.st).Keys()
	for _, k := range platformKeys {
		if strings.Contains(e.pubText(), k.PublicKey) {
			t.Fatal("craft key leaked into the platform class")
		}
	}
	if data, err := os.ReadFile(e.h.trustYAML); err != nil || !strings.Contains(string(data), "ADR-028") {
		t.Fatalf("craft trust yaml: %v %s", err, data)
	}
	if r := opRequest(http.MethodDelete, "", e.h.removeKey, keys[0].KeyID); r.Code != http.StatusNoContent {
		t.Fatalf("remove embedded = %d", r.Code)
	}
	if r := opRequest(http.MethodDelete, "", e.h.removeKey, keys[0].KeyID); r.Code != http.StatusNotFound {
		t.Fatalf("remove again = %d", r.Code)
	}
	entries, _ := e.h.audit.List(100)
	var actions string
	for _, en := range entries {
		actions += en.Action + " "
	}
	if !strings.Contains(actions, "craft.trust.add") || !strings.Contains(actions, "craft.trust.remove") {
		t.Fatalf("audit = %s", actions)
	}
}

func TestCraftPlanVerdicts(t *testing.T) {
	e := newCraftEnv(t)
	body := e.manifest("1.0.0", 3)
	// Untrusted key: a verdict (200), failing the signature check.
	var resp craftPlanResponse
	rec := post(e.h.plan, body)
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if rec.Code != http.StatusOK || resp.Verdict.State != craft.StateFailed || resp.Verdict.Refusal != craft.CheckSignature {
		t.Fatalf("untrusted: %d %+v", rec.Code, resp)
	}
	e.trust()
	rec = post(e.h.plan, body)
	resp = craftPlanResponse{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Verdict.State != craft.StateVerified || resp.Plan.Source != "local" || resp.Plan.Tag != "homestead/craft-wordcraft:1.0.0" {
		t.Fatalf("trusted: %+v", resp)
	}
	// Missing files are a 400, not a verdict.
	if r := post(e.h.plan, craftBody{App: "wordcraft", Manifest: filepath.Join(e.dir, "nope")}); r.Code != http.StatusBadRequest {
		t.Fatalf("missing = %d", r.Code)
	}
}

func (e *craftEnv) waitOp(id int64) operation.Operation {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		op, err := operation.New(e.st).Get(id)
		if err == nil && operation.Terminal(op.Status) {
			return op
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatal("operation did not finish")
	return operation.Operation{}
}

func TestCraftBuildStatusRollbackAndOverride(t *testing.T) {
	e := newCraftEnv(t)
	e.trust()
	rec := post(e.h.build, e.manifest("1.1.0", 5))
	if rec.Code != http.StatusOK {
		t.Fatalf("build = %d %s", rec.Code, rec.Body.String())
	}
	var started struct{ ID int64 }
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	if op := e.waitOp(started.ID); op.Status != operation.StatusSucceeded {
		t.Fatalf("op = %+v", op)
	}
	statusView := func() craftStatusView {
		r := httptest.NewRecorder()
		e.h.status(r, httptest.NewRequest(http.MethodGet, "/x", nil))
		var v craftStatusView
		_ = json.Unmarshal(r.Body.Bytes(), &v)
		return v
	}
	v := statusView()
	if len(v.Images) != 1 || !v.Images[0].Match || v.Baselines["wordcraft"].Counter != 5 {
		t.Fatalf("status = %+v", v)
	}
	// Replace the image behind the daemon's back: status shows the mismatch.
	e.builder.set("homestead/craft-wordcraft:1.1.0", "sha256:"+strings.Repeat("e", 64))
	if v := statusView(); v.Images[0].Match {
		t.Fatal("replaced image still matches")
	}
	// Older counter: 422 with the override code; the override is audited.
	older := e.manifest("1.0.0", 4)
	rec = post(e.h.build, older)
	if rec.Code != http.StatusUnprocessableEntity || rec.Header().Get(ErrorCodeHeader) != ErrorCodeOlderRelease {
		t.Fatalf("older = %d %q %s", rec.Code, rec.Header().Get(ErrorCodeHeader), rec.Body.String())
	}
	older.AllowOlder = true
	rec = post(e.h.build, older)
	if rec.Code != http.StatusOK {
		t.Fatalf("override = %d %s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	if op := e.waitOp(started.ID); op.Status != operation.StatusSucceeded {
		t.Fatalf("override op = %+v", op)
	}
	entries, _ := e.h.audit.List(100)
	found := false
	for _, en := range entries {
		if en.Action == "craft.build.override" && en.Override && strings.Contains(en.OverrideReason, "counter 5") && strings.Contains(en.OverrideReason, "counter 4") {
			found = true
		}
	}
	if !found {
		t.Fatalf("override not audited with both counters: %+v", entries)
	}
}

func TestCraftUpdateAddsBuildStepsAndPrunes(t *testing.T) {
	e := newCraftEnv(t)
	e.trust()
	body := e.manifest("1.2.0", 2)
	// Ship the manifest in the catalog the way packaging does.
	for _, suffix := range []string{"", ".minisig"} {
		data, _ := os.ReadFile(body.Manifest + suffix)
		if err := os.WriteFile(filepath.Join(e.catalogDir, "craft", craftManifestFile+suffix), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plan := update.UpdatePlan{Package: "wordcraft", Services: []update.ServiceUpdate{
		{Service: "web", Current: "homestead/craft-wordcraft:1.1.0", Target: "homestead/craft-wordcraft:1.2.0"}}}
	def := operation.Definition{Type: "app.update", Steps: []operation.StepDef{{Name: "up", Run: func(context.Context) error { return nil }}}}
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	cp, status, err := withCraftBuild(r, &def, e.h.pipe, plan, &craftApplyInput{Artifact: body.Artifact})
	if err != nil || status != 0 || cp == nil {
		t.Fatalf("withCraftBuild = %v %d %v", cp, status, err)
	}
	names := []string{}
	for _, s := range def.Steps {
		names = append(names, s.Name)
	}
	got := strings.Join(names, ",")
	if !strings.HasPrefix(got, "verify-manifest,") || !strings.HasSuffix(got, ",up,prune-craft-images") {
		t.Fatalf("steps = %s", got)
	}
	// A non-craft plan is untouched.
	plain := operation.Definition{Type: "app.update", Steps: []operation.StepDef{{Name: "up", Run: func(context.Context) error { return nil }}}}
	if cp, _, err := withCraftBuild(r, &plain, e.h.pipe, update.UpdatePlan{Services: []update.ServiceUpdate{{Service: "a", Current: "x:1.0", Target: "x:1.1"}}}, nil); err != nil || cp != nil || len(plain.Steps) != 1 {
		t.Fatalf("non-craft plan changed: %v %v", cp, err)
	}
	// A refused manifest blocks the update before anything runs.
	tampered := e.manifest("1.2.0", 2)
	data, _ := os.ReadFile(tampered.Manifest)
	_ = os.WriteFile(tampered.Manifest, []byte(strings.Replace(string(data), "counter: 2", "counter: 9", 1)), 0o644)
	_, status, err = withCraftBuild(r, &plain, e.h.pipe, plan, &craftApplyInput{Manifest: tampered.Manifest, Artifact: tampered.Artifact})
	if err == nil || status != http.StatusUnprocessableEntity {
		t.Fatalf("tampered manifest: %d %v", status, err)
	}
}

func TestCraftRoutesClassified(t *testing.T) {
	for _, route := range []string{"GET /api/v1/craft", "POST /api/v1/craft/plan", "POST /api/v1/craft/build",
		"GET /api/v1/craft/trust-keys", "POST /api/v1/craft/trust-keys", "PUT /api/v1/craft/trust-keys/{id}", "DELETE /api/v1/craft/trust-keys/{id}"} {
		if c, ok := routeClasses[route]; !ok || c != ClassSession {
			t.Errorf("%s class = %v ok=%v", route, c, ok)
		}
	}
}

func TestWithCraftBuildChainsCleanup(t *testing.T) {
	e := newCraftEnv(t)
	e.trust()
	body := e.manifest("1.2.0", 2)
	for _, suffix := range []string{"", ".minisig"} {
		data, _ := os.ReadFile(body.Manifest + suffix)
		_ = os.WriteFile(filepath.Join(e.catalogDir, "craft", craftManifestFile+suffix), data, 0o644)
	}
	plan := update.UpdatePlan{Package: "wordcraft", Services: []update.ServiceUpdate{
		{Service: "web", Current: "homestead/craft-wordcraft:1.1.0", Target: "homestead/craft-wordcraft:1.2.0"}}}
	prevRan := false
	def := operation.Definition{Type: "app.update", Cleanup: func() { prevRan = true },
		Steps: []operation.StepDef{{Name: "up", Run: func(context.Context) error { return nil }}}}
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	if _, _, err := withCraftBuild(r, &def, e.h.pipe, plan, &craftApplyInput{Artifact: body.Artifact}); err != nil {
		t.Fatal(err)
	}
	def.Cleanup()
	if !prevRan {
		t.Fatal("the update's own Cleanup was overwritten")
	}
}
