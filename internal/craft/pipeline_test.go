package craft

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/operation"
	"homestead/internal/release"
	"homestead/internal/store"
)

// signer is a test minisign signer producing the real file layouts.
type signer struct {
	id   [8]byte
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newSigner(t *testing.T) *signer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s := &signer{pub: pub, priv: priv}
	_, _ = rand.Read(s.id[:])
	return s
}

func (s *signer) pubText() string {
	b := append([]byte("Ed"), s.id[:]...)
	b = append(b, s.pub...)
	return "untrusted comment: minisign public key\n" + base64.StdEncoding.EncodeToString(b) + "\n"
}

func (s *signer) parsed(t *testing.T) release.PublicKey {
	t.Helper()
	k, err := release.ParsePublicKey(s.pubText())
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func (s *signer) sign(msg []byte, algo string) string {
	sig := ed25519.Sign(s.priv, msg)
	raw := append([]byte(algo), s.id[:]...)
	raw = append(raw, sig...)
	comment := "timestamp:1"
	global := ed25519.Sign(s.priv, append(append([]byte(nil), sig...), []byte(comment)...))
	return "untrusted comment: signature\n" + base64.StdEncoding.EncodeToString(raw) +
		"\ntrusted comment: " + comment + "\n" + base64.StdEncoding.EncodeToString(global) + "\n"
}

// fakeBuilder is an in-memory Docker.
type fakeBuilder struct {
	tags      map[string]string
	builds    []BuildSpec
	removed   []string
	failBuild error
	blockOn   chan struct{}
	started   chan struct{}
	nextID    int
	buildArgs map[string]string
	rootDir   string
}

func newFakeBuilder() *fakeBuilder { return &fakeBuilder{tags: map[string]string{}} }

func (f *fakeBuilder) Build(ctx context.Context, spec BuildSpec) error {
	f.builds = append(f.builds, spec)
	f.buildArgs = spec.Args
	// The context must carry the verified artifact and the Dockerfile.
	if _, err := os.Stat(filepath.Join(spec.Dir, "Dockerfile")); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(spec.Dir, "artifact", spec.Args["ARTIFACT_FILE"])); err != nil {
		return err
	}
	fmt.Fprint(spec.Output, "step 1/3 password=hunter2 building\n")
	if f.started != nil {
		close(f.started)
	}
	if f.blockOn != nil {
		select {
		case <-f.blockOn:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.failBuild != nil {
		return f.failBuild
	}
	f.nextID++
	f.tags[spec.Tag] = fmt.Sprintf("sha256:%064d", f.nextID)
	return nil
}

func (f *fakeBuilder) ImageID(_ context.Context, ref string) (string, error) {
	if id, ok := f.tags[ref]; ok {
		return id, nil
	}
	return "", ErrImageMissing
}

func (f *fakeBuilder) Tag(_ context.Context, src, dst string) error {
	id := src
	if v, ok := f.tags[src]; ok {
		id = v
	}
	f.tags[dst] = id
	return nil
}

func (f *fakeBuilder) Remove(_ context.Context, ref string) error {
	f.removed = append(f.removed, ref)
	if _, ok := f.tags[ref]; !ok {
		return ErrImageMissing
	}
	delete(f.tags, ref)
	return nil
}

func (f *fakeBuilder) RootDir(context.Context) (string, error) {
	if f.rootDir == "" {
		return "", errors.New("no root")
	}
	return f.rootDir, nil
}

type env struct {
	t        *testing.T
	signer   *signer
	pipe     *Pipeline
	fb       *fakeBuilder
	dir      string
	ctxDir   string
	artifact []byte
	fetched  int
	// fetchBody is what the fake network serves.
	fetchBody []byte
	fetchErr  error
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &env{t: t, signer: newSigner(t), fb: newFakeBuilder(), dir: t.TempDir(), artifact: []byte("static-site-tarball")}
	e.ctxDir = filepath.Join(e.dir, "context")
	if err := os.MkdirAll(filepath.Join(e.ctxDir, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"Dockerfile": "FROM scratch\n", "nginx.conf": "events {}\n", "conf/extra.conf": "x\n"} {
		if err := os.WriteFile(filepath.Join(e.ctxDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.fetchBody = e.artifact
	key := e.signer.parsed(t)
	e.pipe = &Pipeline{
		Keys:       func() ([]release.PublicKey, error) { return []release.PublicKey{key}, nil },
		Store:      NewStore(st),
		Builder:    e.fb,
		ContextDir: e.ctxDir,
		StagingDir: filepath.Join(e.dir, "staging"),
		Fetch: func(_ context.Context, _ string, dst io.Writer, limit int64) error {
			e.fetched++
			if e.fetchErr != nil {
				return e.fetchErr
			}
			_, err := io.Copy(dst, io.LimitReader(strings.NewReader(string(e.fetchBody)), limit))
			return err
		},
		FreeBytes: func(string) (uint64, error) { return 1 << 40, nil },
	}
	return e
}

// manifestFor writes a signed manifest for wordcraft and returns a request.
func (e *env) manifestFor(version string, counter int64, mutate func(*string)) Request {
	e.t.Helper()
	digest, err := TreeDigest(e.ctxDir)
	if err != nil {
		e.t.Fatal(err)
	}
	sum := sha256.Sum256(e.artifact)
	text := fmt.Sprintf(`format: %s
counter: %d
context_digest: %s
apps:
  - name: wordcraft
    version: %s
    artifact:
      name: wordcraft-%s.tar.gz
      urls:
        - https://example.invalid/wordcraft-%s.tar.gz
      sha256: %s
      size: %d
`, ManifestFormat, counter, digest, version, version, version, hex.EncodeToString(sum[:]), len(e.artifact))
	sig := e.signer.sign([]byte(text), "Ed")
	if mutate != nil {
		mutate(&text) // tamper after signing
	}
	return e.write(text, sig)
}

func (e *env) write(text, sig string) Request {
	e.t.Helper()
	dir := e.t.TempDir()
	m, s := filepath.Join(dir, "craft-manifest.yaml"), filepath.Join(dir, "craft-manifest.yaml.minisig")
	if err := os.WriteFile(m, []byte(text), 0o644); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(s, []byte(sig), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return Request{App: "wordcraft", ManifestPath: m, SignaturePath: s, Actor: "tester"}
}

// build runs the operation definition as the runner would.
func (e *env) build(req Request) error { return e.buildCtx(context.Background(), req) }

func (e *env) buildCtx(ctx context.Context, req Request) error {
	e.t.Helper()
	def, err := e.pipe.Definition(req)
	if err != nil {
		return err
	}
	return runDefinition(ctx, def)
}

func runDefinition(ctx context.Context, def operation.Definition) error {
	if def.Cleanup != nil {
		defer def.Cleanup()
	}
	for _, step := range def.Steps {
		if err := step.Run(ctx); err != nil {
			return err
		}
	}
	return nil
}

func wantCheck(t *testing.T, err error, check string) {
	t.Helper()
	var ce *CheckError
	if !errors.As(err, &ce) || ce.Check != check {
		t.Fatalf("err = %v, want failing check %q", err, check)
	}
}

func (e *env) assertNothingBuilt() {
	e.t.Helper()
	if len(e.fb.builds) != 0 {
		e.t.Fatalf("docker build ran %d times, want 0", len(e.fb.builds))
	}
	e.assertNoRecord()
}

func (e *env) assertNoRecord() {
	e.t.Helper()
	if imgs, _ := e.pipe.Store.Images(); len(imgs) != 0 {
		e.t.Fatalf("recorded images %v, want none", imgs)
	}
	for tag := range e.fb.tags {
		if strings.HasPrefix(tag, "homestead/craft-") {
			e.t.Fatalf("tag %s left behind", tag)
		}
	}
}

func TestBuildAcceptsValidManifest(t *testing.T) {
	e := newEnv(t)
	req := e.manifestFor("1.2.0", 5, nil)
	if err := e.build(req); err != nil {
		t.Fatal(err)
	}
	img, err := e.pipe.Store.Image("wordcraft", "1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if img.Tag != "homestead/craft-wordcraft:1.2.0" || img.Counter != 5 || img.ImageID != e.fb.tags[img.Tag] || img.ImageID == "" {
		t.Fatalf("record = %+v tags=%v", img, e.fb.tags)
	}
	if want := hex.EncodeToString(func() []byte { s := sha256.Sum256(e.artifact); return s[:] }()); img.ArtifactSHA256 != want {
		t.Fatalf("artifact digest %s", img.ArtifactSHA256)
	}
	if e.fb.buildArgs["ARTIFACT_SHA256"] != img.ArtifactSHA256 || e.fb.buildArgs["ARTIFACT_FILE"] != "wordcraft-1.2.0.tar.gz" {
		t.Fatalf("build args = %v", e.fb.buildArgs)
	}
	// Only the final tag remains; the staging tag is gone.
	if len(e.fb.tags) != 1 {
		t.Fatalf("tags = %v", e.fb.tags)
	}
	base, _ := e.pipe.Store.Baseline("wordcraft")
	if base.Counter != 5 || base.Version != "1.2.0" {
		t.Fatalf("baseline = %+v", base)
	}
	verdicts, _ := e.pipe.Store.Verdicts()
	if len(verdicts) != 1 || verdicts[0].State != StateBuilt {
		t.Fatalf("verdicts = %+v", verdicts)
	}
	if err := e.pipe.CheckImage(context.Background(), img.Tag); err != nil {
		t.Fatalf("CheckImage: %v", err)
	}
	if entries, _ := os.ReadDir(e.pipe.StagingDir); len(entries) != 0 {
		t.Fatalf("staging not cleaned: %v", entries)
	}
}

func TestBuildOutputIsRedacted(t *testing.T) {
	e := newEnv(t)
	e.fb.failBuild = errors.New("exit status 1")
	err := e.build(e.manifestFor("1.0.0", 1, nil))
	wantCheck(t, err, CheckBuild)
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("build output leaked a secret: %v", err)
	}
	if !strings.Contains(err.Error(), "step 1/3") {
		t.Fatalf("build output tail missing: %v", err)
	}
}

func TestTamperedManifestRefused(t *testing.T) {
	e := newEnv(t)
	req := e.manifestFor("1.0.0", 1, func(s *string) { *s = strings.Replace(*s, "counter: 1", "counter: 9", 1) })
	wantCheck(t, e.build(req), CheckSignature)
	e.assertNothingBuilt()
	if e.fetched != 0 {
		t.Fatal("artifact fetched before the manifest verified")
	}
}

func TestUnknownKeyAndPlatformKeyRefused(t *testing.T) {
	e := newEnv(t)
	other := newSigner(t) // e.g. the platform release key
	digest, _ := TreeDigest(e.ctxDir)
	sum := sha256.Sum256(e.artifact)
	text := fmt.Sprintf("format: %s\ncounter: 1\ncontext_digest: %s\napps:\n  - name: wordcraft\n    version: 1.0.0\n    artifact:\n      name: w.tar.gz\n      sha256: %s\n      size: 3\n", ManifestFormat, digest, hex.EncodeToString(sum[:]))
	wantCheck(t, e.build(e.write(text, other.sign([]byte(text), "Ed"))), CheckSignature)
	e.assertNothingBuilt()
}

func TestPrehashedSignatureRefused(t *testing.T) {
	e := newEnv(t)
	good := e.manifestFor("1.0.0", 1, nil)
	data, _ := os.ReadFile(good.ManifestPath)
	err := e.build(e.write(string(data), e.signer.sign(data, "ED")))
	wantCheck(t, err, CheckSignature)
	if !errors.Is(err, release.ErrPrehashed) {
		t.Fatalf("err = %v, want prehashed rejection", err)
	}
}

func TestMissingManifestIsInputError(t *testing.T) {
	e := newEnv(t)
	req := Request{App: "wordcraft", ManifestPath: filepath.Join(e.dir, "nope"), SignaturePath: filepath.Join(e.dir, "nope.minisig")}
	err := e.build(req)
	wantCheck(t, err, CheckInput)
	if !errors.Is(err, ErrInput) {
		t.Fatalf("err = %v", err)
	}
	e.assertNothingBuilt()
}

func TestCounterRollbackAndOverride(t *testing.T) {
	e := newEnv(t)
	if err := e.build(e.manifestFor("1.1.0", 5, nil)); err != nil {
		t.Fatal(err)
	}
	older := e.manifestFor("1.0.0", 4, nil)
	err := e.build(older)
	wantCheck(t, err, CheckCounter)
	var ce *CheckError
	if !errors.As(err, &ce) || !ce.Overridable() {
		t.Fatalf("counter refusal must be overridable: %v", err)
	}
	if !errors.Is(err, release.ErrRollback) {
		t.Fatalf("err = %v", err)
	}
	// Preview with the override reports both counters.
	older.AllowOlder = true
	plan, _, err := e.pipe.Preview(context.Background(), older)
	if err != nil || !plan.Override || !strings.Contains(plan.Baseline, "counter 5") || plan.Counter != 4 {
		t.Fatalf("plan = %+v err = %v", plan, err)
	}
	if err := e.build(older); err != nil {
		t.Fatalf("override build: %v", err)
	}
	if base, _ := e.pipe.Store.Baseline("wordcraft"); base.Counter != 4 || base.Version != "1.0.0" {
		t.Fatalf("baseline after override = %+v", base)
	}
	verdicts, _ := e.pipe.Store.Verdicts()
	if !verdicts[0].Override {
		t.Fatalf("override not recorded in verdict: %+v", verdicts[0])
	}
}

func TestEqualCounterDifferentVersionNoOverride(t *testing.T) {
	e := newEnv(t)
	if err := e.build(e.manifestFor("1.1.0", 5, nil)); err != nil {
		t.Fatal(err)
	}
	req := e.manifestFor("1.2.0", 5, nil)
	req.AllowOlder = true
	err := e.build(req)
	wantCheck(t, err, CheckCounterConflict)
	var ce *CheckError
	if errors.As(err, &ce) && ce.Overridable() {
		t.Fatal("equal counter with a different version must have no override")
	}
}

func TestArtifactDigestMismatch(t *testing.T) {
	e := newEnv(t)
	e.fetchBody = append([]byte(nil), e.artifact...)
	e.fetchBody[0] ^= 0xff // one flipped byte
	err := e.build(e.manifestFor("1.0.0", 1, nil))
	wantCheck(t, err, CheckArtifact)
	e.assertNothingBuilt()
	// A longer body than signed is also refused (no unbounded download).
	e.fetchBody = append(append([]byte(nil), e.artifact...), "extra"...)
	wantCheck(t, e.build(e.manifestFor("1.0.0", 1, nil)), CheckArtifact)
	e.assertNothingBuilt()
}

func TestContextDigestMismatch(t *testing.T) {
	e := newEnv(t)
	req := e.manifestFor("1.0.0", 1, nil)
	if err := os.WriteFile(filepath.Join(e.ctxDir, "nginx.conf"), []byte("events { tampered }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := e.build(req)
	wantCheck(t, err, CheckContext)
	e.assertNothingBuilt()
	if _, _, perr := e.pipe.Preview(context.Background(), req); perr == nil {
		t.Fatal("preview must also refuse")
	} else {
		wantCheck(t, perr, CheckContext)
	}
}

func TestContextRefusesSymlinkAndReservedName(t *testing.T) {
	e := newEnv(t)
	req := e.manifestFor("1.0.0", 1, nil)
	if err := os.Symlink("/etc/passwd", filepath.Join(e.ctxDir, "link")); err != nil {
		t.Fatal(err)
	}
	wantCheck(t, e.build(req), CheckContext)
	e.assertNothingBuilt()
	_ = os.Remove(filepath.Join(e.ctxDir, "link"))
	if err := os.Mkdir(filepath.Join(e.ctxDir, "artifact"), 0o755); err != nil {
		t.Fatal(err)
	}
	wantCheck(t, e.build(req), CheckContext)
}

func TestLocalArtifactVerifiedIdentically(t *testing.T) {
	e := newEnv(t)
	local := filepath.Join(e.dir, "supplied.tar.gz")
	if err := os.WriteFile(local, e.artifact, 0o644); err != nil {
		t.Fatal(err)
	}
	e.fetchErr = errors.New("network blocked")
	req := e.manifestFor("1.0.0", 1, nil)
	req.ArtifactPath = local
	if err := e.build(req); err != nil {
		t.Fatalf("offline build: %v", err)
	}
	if e.fetched != 0 {
		t.Fatal("a local artifact must not be fetched")
	}
	// Tampered local file is refused with the same check and builds nothing more.
	before := len(e.fb.builds)
	bad := append([]byte(nil), e.artifact...)
	bad[1] ^= 1
	if err := os.WriteFile(local, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	wantCheck(t, e.build(req), CheckArtifact)
	wantCheck(t, func() error { _, _, err := e.pipe.Preview(context.Background(), req); return err }(), CheckArtifact)
	if len(e.fb.builds) != before {
		t.Fatal("tampered local artifact reached docker build")
	}
	// Path safety: symlinks and relative paths are inputs errors.
	link := filepath.Join(e.dir, "link.tar.gz")
	if err := os.Symlink(local, link); err != nil {
		t.Fatal(err)
	}
	req.ArtifactPath = link
	wantCheck(t, e.build(req), CheckInput)
	req.ArtifactPath = "relative.tar.gz"
	wantCheck(t, e.build(req), CheckInput)
}

func TestNetworkFailureIsRetryableAndLeavesNothing(t *testing.T) {
	e := newEnv(t)
	e.fetchErr = errors.New("connection refused")
	err := e.build(e.manifestFor("1.0.0", 1, nil))
	wantCheck(t, err, CheckFetch)
	var ce *CheckError
	if !errors.As(err, &ce) || !ce.Retryable() {
		t.Fatal("fetch failure must be retryable")
	}
	e.assertNothingBuilt()
	// Retry succeeds.
	e.fetchErr = nil
	if err := e.build(e.manifestFor("1.0.0", 1, nil)); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestDiskSpaceRefusal(t *testing.T) {
	e := newEnv(t)
	e.pipe.FreeBytes = func(string) (uint64, error) { return 1 << 20, nil }
	err := e.build(e.manifestFor("1.0.0", 1, nil))
	wantCheck(t, err, CheckDisk)
	if !strings.Contains(err.Error(), "MiB free") {
		t.Fatalf("message not actionable: %v", err)
	}
	if e.fetched != 0 {
		t.Fatal("must refuse before fetching")
	}
	e.assertNothingBuilt()
	// The Docker data root is checked too.
	e.fb.rootDir = "/var/lib/docker"
	e.pipe.FreeBytes = func(p string) (uint64, error) {
		if p == "/var/lib/docker" {
			return 1 << 20, nil
		}
		return 1 << 40, nil
	}
	err = e.build(e.manifestFor("1.0.0", 1, nil))
	wantCheck(t, err, CheckDisk)
	if !strings.Contains(err.Error(), "Docker data root") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildFailureLeavesNoTagNoRecord(t *testing.T) {
	e := newEnv(t)
	e.fb.failBuild = errors.New("exit status 1")
	err := e.build(e.manifestFor("1.0.0", 1, nil))
	wantCheck(t, err, CheckBuild)
	var ce *CheckError
	if !errors.As(err, &ce) || !ce.Retryable() {
		t.Fatal("build failure must be retryable")
	}
	e.assertNoRecord()
	if base, _ := e.pipe.Store.Baseline("wordcraft"); base.Counter != 0 {
		t.Fatalf("a failed build moved the baseline: %+v", base)
	}
	v, _ := e.pipe.Store.Verdicts()
	if len(v) != 1 || v[0].State != StateFailed || v[0].Refusal != CheckBuild {
		t.Fatalf("verdict = %+v", v)
	}
	if entries, _ := os.ReadDir(e.pipe.StagingDir); len(entries) != 0 {
		t.Fatalf("staging not cleaned: %v", entries)
	}
}

func TestFailedRebuildKeepsPreviousImage(t *testing.T) {
	e := newEnv(t)
	if err := e.build(e.manifestFor("1.0.0", 1, nil)); err != nil {
		t.Fatal(err)
	}
	prev := e.fb.tags["homestead/craft-wordcraft:1.0.0"]
	e.fb.failBuild = errors.New("boom")
	wantCheck(t, e.build(e.manifestFor("1.1.0", 2, nil)), CheckBuild)
	if e.fb.tags["homestead/craft-wordcraft:1.0.0"] != prev {
		t.Fatal("failed update changed the running version's image")
	}
	if _, err := e.pipe.Store.Image("wordcraft", "1.1.0"); !errors.Is(err, ErrNoImage) {
		t.Fatal("failed build recorded an image")
	}
	if base, _ := e.pipe.Store.Baseline("wordcraft"); base.Version != "1.0.0" {
		t.Fatalf("baseline moved: %+v", base)
	}
}

func TestCancelledBuildLeavesNothing(t *testing.T) {
	e := newEnv(t)
	e.fb.blockOn = make(chan struct{})
	e.fb.started = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.buildCtx(ctx, e.manifestFor("1.0.0", 1, nil)) }()
	select {
	case <-e.fb.started:
	case err := <-done:
		t.Fatalf("finished early: %v", err)
	}
	cancel()
	err := <-done
	wantCheck(t, err, CheckCancelled)
	e.assertNoRecord()
}

func TestRecordedDigestMismatchRefuses(t *testing.T) {
	e := newEnv(t)
	if err := e.build(e.manifestFor("1.0.0", 1, nil)); err != nil {
		t.Fatal(err)
	}
	tag := "homestead/craft-wordcraft:1.0.0"
	if err := e.pipe.CheckImage(context.Background(), tag); err != nil {
		t.Fatal(err)
	}
	e.fb.tags[tag] = "sha256:" + strings.Repeat("f", 64) // replaced behind the daemon's back
	if err := e.pipe.CheckImage(context.Background(), tag); !errors.Is(err, ErrImageMismatch) {
		t.Fatalf("err = %v, want mismatch", err)
	}
	delete(e.fb.tags, tag)
	if err := e.pipe.CheckImage(context.Background(), tag); !errors.Is(err, ErrImageMismatch) {
		t.Fatalf("missing image err = %v", err)
	}
	if err := e.pipe.CheckImage(context.Background(), "homestead/craft-gridcraft:1.0.0"); !errors.Is(err, ErrNotBuilt) {
		t.Fatalf("unbuilt err = %v", err)
	}
	if err := e.pipe.CheckImage(context.Background(), "nginx:1.27"); !errors.Is(err, ErrNotCraftImage) {
		t.Fatalf("non-craft err = %v", err)
	}
	if id, ok := e.pipe.RecordedID(tag); !ok || id == "" {
		t.Fatal("RecordedID missing")
	}
}

func TestPruneKeepsNewVersionOnly(t *testing.T) {
	e := newEnv(t)
	if err := e.build(e.manifestFor("1.0.0", 1, nil)); err != nil {
		t.Fatal(err)
	}
	if err := e.build(e.manifestFor("1.1.0", 2, nil)); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.fb.tags["homestead/craft-wordcraft:1.0.0"]; !ok {
		t.Fatal("previous image must be kept until the update applies")
	}
	removed, err := e.pipe.Prune(context.Background(), "wordcraft", "1.1.0")
	if err != nil || len(removed) != 1 || removed[0] != "homestead/craft-wordcraft:1.0.0" {
		t.Fatalf("removed = %v err = %v", removed, err)
	}
	if _, err := e.pipe.Store.Image("wordcraft", "1.0.0"); !errors.Is(err, ErrNoImage) {
		t.Fatal("old record kept")
	}
	if _, err := e.pipe.Store.Image("wordcraft", "1.1.0"); err != nil {
		t.Fatal("new record lost")
	}
}

func TestCheckRecordsVerdictAndPlan(t *testing.T) {
	e := newEnv(t)
	plan, v, err := e.pipe.Check(context.Background(), e.manifestFor("1.0.0", 3, nil))
	if err != nil || v.State != StateVerified || plan.Tag != "homestead/craft-wordcraft:1.0.0" || plan.Source != "download" {
		t.Fatalf("plan=%+v v=%+v err=%v", plan, v, err)
	}
	bad := e.manifestFor("1.0.0", 3, func(s *string) { *s += "# x\n" })
	_, v, err = e.pipe.Check(context.Background(), bad)
	wantCheck(t, err, CheckSignature)
	if v.State != StateFailed || v.Refusal != CheckSignature {
		t.Fatalf("v = %+v", v)
	}
	if saved, _ := e.pipe.Store.Verdicts(); saved[0].State != StateFailed {
		t.Fatal("failed verdict not saved")
	}
	if len(e.fb.builds) != 0 {
		t.Fatal("preview must not build")
	}
}

func TestCraftAndPlatformKeyClassesAreSeparate(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	craftSigner, platformSigner := newSigner(t), newSigner(t)
	craftKeys, platformKeys := release.NewCraftStore(st), release.NewStore(st)
	if _, err := craftKeys.AddKey(craftSigner.pubText(), "craft", "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := platformKeys.AddKey(platformSigner.pubText(), "platform", "t"); err != nil {
		t.Fatal(err)
	}
	craftTrusted, _ := craftKeys.PublicKeys()
	platformTrusted, _ := platformKeys.PublicKeys()
	msg := []byte("version=1.0.0\ncounter=1\n")
	// Craft-signed content does not verify against platform keys, nor the reverse.
	if _, err := release.VerifyMessage(craftSigner.sign(msg, "Ed"), msg, platformTrusted); !errors.Is(err, release.ErrUnknownKey) {
		t.Fatalf("craft-signed verified as platform: %v", err)
	}
	if _, err := release.VerifyMessage(platformSigner.sign(msg, "Ed"), msg, craftTrusted); !errors.Is(err, release.ErrUnknownKey) {
		t.Fatalf("platform-signed verified as craft: %v", err)
	}
	if _, err := release.VerifyMessage(craftSigner.sign(msg, "Ed"), msg, craftTrusted); err != nil {
		t.Fatalf("craft key must verify craft: %v", err)
	}
	// The embedded keys are distinct placeholders.
	ck, _ := craftKeys.Keys()
	pk, _ := platformKeys.Keys()
	if ck[0].KeyID == pk[0].KeyID && ck[0].Source == release.SourceEmbedded {
		t.Fatal("embedded craft and platform keys must differ")
	}
	var embeddedCraft string
	for _, k := range ck {
		if k.Source == release.SourceEmbedded {
			embeddedCraft = k.KeyID
		}
	}
	for _, k := range pk {
		if k.KeyID == embeddedCraft {
			t.Fatal("craft embedded key listed in the platform class")
		}
	}
	// A craft manifest is also not a platform checksums file.
	if _, err := release.ParseChecksums([]byte("format: " + ManifestFormat + "\ncounter: 1\n")); err == nil {
		t.Fatal("craft manifest parsed as checksums")
	}
}
