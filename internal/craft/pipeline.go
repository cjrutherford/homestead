package craft

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"homestead/internal/audit"
	"homestead/internal/operation"
	"homestead/internal/release"
)

// Failing check names. They appear in errors, verdicts (Refusal), audit
// entries and the API.
const (
	CheckInput           = "input"
	CheckSignature       = "signature"
	CheckManifest        = "manifest"
	CheckCounter         = "counter"
	CheckCounterConflict = "counter-conflict"
	CheckArtifact        = "artifact-digest"
	CheckContext         = "build-context-digest"
	CheckDisk            = "disk-space"
	CheckFetch           = "fetch"
	CheckBuild           = "build"
	CheckCancelled       = "cancelled"
	CheckRecord          = "record"
)

// OperationType is the operation type of a verify-then-build run.
const OperationType = "craft.build"

// DiskHeadroom is the layer headroom added to three times the artifact
// size when checking free space (artifact copy, extracted tree, image
// layer), on both the staging directory and the Docker data root.
const DiskHeadroom int64 = 512 << 20

// MaxOutput bounds captured build output (the tail is kept).
const MaxOutput = 32 << 10

// CheckError names the check that refused a verify or build.
type CheckError struct {
	Check string
	Err   error
}

func (e *CheckError) Error() string {
	return fmt.Sprintf("craft: %s check failed: %v", e.Check, e.Err)
}

func (e *CheckError) Unwrap() error { return e.Err }

// Overridable reports whether an explicit owner override can lift the
// refusal: only the ADR-025 counter rollback.
func (e *CheckError) Overridable() bool { return e.Check == CheckCounter }

// Retryable reports a transient failure: nothing was recorded and the
// same request can simply run again.
func (e *CheckError) Retryable() bool {
	switch e.Check {
	case CheckFetch, CheckBuild, CheckCancelled, CheckDisk, CheckRecord:
		return true
	}
	return false
}

func failed(check string, err error) error { return &CheckError{Check: check, Err: err} }

// ErrInput marks unusable request input (HTTP 400); it is also a CheckError
// with Check "input".
var ErrInput = errors.New("craft: invalid input")

// ErrImageMissing is returned by a Builder for an unknown image ref.
var ErrImageMissing = errors.New("craft: image not present")

// BuildSpec is one docker build.
type BuildSpec struct {
	Dir string
	// File is the Dockerfile name inside Dir ("" is Dockerfile).
	File string
	// Network allows the build network. Only Everything bridge builds set
	// it (npm ci and go mod download, both lockfile-verified).
	Network bool
	Tag     string
	Args    map[string]string
	Output  io.Writer
}

// Builder is the Docker surface the pipeline needs; internal/docker
// implements it and tests fake it.
type Builder interface {
	Build(ctx context.Context, spec BuildSpec) error
	// ImageID returns the local image ID behind ref, or ErrImageMissing.
	ImageID(ctx context.Context, ref string) (string, error)
	Tag(ctx context.Context, src, dst string) error
	Remove(ctx context.Context, ref string) error
	// RootDir is the Docker data root, for the disk-space check; an
	// error skips that half of the check.
	RootDir(ctx context.Context) (string, error)
}

// Pipeline is the verify-then-build engine.
type Pipeline struct {
	// Keys returns the trusted craft signing keys (never platform keys).
	Keys  func() ([]release.PublicKey, error)
	Store *Store
	// Builder talks to Docker.
	Builder Builder
	// ContextDir is the Dockerfile and build context shipped with the
	// catalog; StagingDir is the private scratch area.
	ContextDir string
	StagingDir string
	// BridgeContextDir and BridgeSourcesDir are the Everything bridge
	// build context and the pinned source archives shipped with the
	// appliance (ADR-057). They are not part of the signed craft manifest.
	BridgeContextDir string
	BridgeSourcesDir string
	// Fetch downloads rawURL into dst, writing at most limit bytes. Nil
	// uses HTTPS with a bounded client.
	Fetch func(ctx context.Context, rawURL string, dst io.Writer, limit int64) error
	// FreeBytes reports free bytes on path's filesystem; nil uses statfs.
	FreeBytes func(path string) (uint64, error)
	// MaxExpanded caps the total expanded size of an artifact archive
	// (default DefaultMaxExpanded, 1 GiB); the disk preflight is sized
	// from it.
	MaxExpanded int64

	lockOnce sync.Once
	lock     chan struct{}
}

func (p *Pipeline) maxExpanded() int64 {
	if p.MaxExpanded > 0 {
		return p.MaxExpanded
	}
	return DefaultMaxExpanded
}

// acquire takes the pipeline-wide build lock: one verify-through-record
// at a time, so the baseline cannot be raced or moved out of order. It
// returns false when ctx ends first.
func (p *Pipeline) acquire(ctx context.Context) bool {
	p.lockOnce.Do(func() { p.lock = make(chan struct{}, 1) })
	select {
	case p.lock <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (p *Pipeline) release() { <-p.lock }

// Request is one verify or build request.
type Request struct {
	App string
	// Version, when set, is the version the caller expects the manifest
	// to carry for App (an update names the catalog's target version).
	Version       string
	ManifestPath  string
	SignaturePath string
	// ArtifactPath is an owner-supplied local artifact (offline use);
	// empty fetches from the signed URLs.
	ArtifactPath string
	AllowOlder   bool
	Actor        string
}

// Verified is a manifest that passed signature and counter checks.
type Verified struct {
	Manifest       Manifest
	App            App
	KeyID          string
	ManifestSHA256 string
	Baseline       release.Installed
	Override       bool
}

func (p *Pipeline) verifyManifest(req Request) (Verified, error) {
	if !appNamePattern.MatchString(req.App) {
		return Verified{}, failed(CheckInput, fmt.Errorf("%w: invalid app name", ErrInput))
	}
	for name, path := range map[string]string{"manifest": req.ManifestPath, "signature": req.SignaturePath} {
		if !filepath.IsAbs(path) {
			return Verified{}, failed(CheckInput, fmt.Errorf("%w: %s must be an absolute path on the appliance", ErrInput, name))
		}
	}
	if req.ArtifactPath != "" && !filepath.IsAbs(req.ArtifactPath) {
		return Verified{}, failed(CheckInput, fmt.Errorf("%w: artifact must be an absolute path on the appliance", ErrInput))
	}
	data, err := release.ReadSmallFile(req.ManifestPath)
	if err != nil {
		return Verified{}, failed(CheckInput, fmt.Errorf("%w: manifest must be a readable regular file under 1 MiB", ErrInput))
	}
	sigData, err := release.ReadSmallFile(req.SignaturePath)
	if err != nil {
		return Verified{}, failed(CheckInput, fmt.Errorf("%w: signature must be a readable regular file under 1 MiB", ErrInput))
	}
	keys, err := p.Keys()
	if err != nil {
		return Verified{}, err
	}
	keyID, err := release.VerifyMessage(string(sigData), data, keys)
	if err != nil {
		return Verified{}, failed(CheckSignature, err)
	}
	m, err := ParseManifest(data)
	if err != nil {
		return Verified{}, failed(CheckManifest, err)
	}
	app, ok := m.App(req.App)
	if !ok {
		return Verified{}, failed(CheckManifest, fmt.Errorf("app %q is not listed in the manifest", req.App))
	}
	if req.Version != "" && app.Version != req.Version {
		return Verified{}, failed(CheckManifest, fmt.Errorf("the manifest carries %s %s but %s was requested", app.Name, app.Version, req.Version))
	}
	sum := sha256.Sum256(data)
	v := Verified{Manifest: m, App: app, KeyID: keyID, ManifestSHA256: hex.EncodeToString(sum[:])}
	if v.Baseline, err = p.Store.Baseline(req.App); err != nil {
		return Verified{}, err
	}
	base := v.Baseline
	high, err := p.Store.HighWater()
	if err != nil {
		return Verified{}, err
	}
	// One counter sequence per signing train: a manifest below the
	// highest counter any craft app was built from is a replay, even for
	// an app with no baseline of its own (ADR-025 rollback rule).
	floor := base
	if high > floor.Counter {
		floor = release.Installed{Version: "(another craft app)", Counter: high}
	}
	if floor.Counter > 0 {
		switch {
		case m.Counter < floor.Counter:
			err := fmt.Errorf("%w: counter %d (%s) < recorded %d (%s)", release.ErrRollback, m.Counter, app.Version, floor.Counter, floor.Version)
			if !req.AllowOlder {
				return v, failed(CheckCounter, err)
			}
			v.Override = true
		case m.Counter == base.Counter && app.Version != base.Version:
			return v, failed(CheckCounterConflict, fmt.Errorf("%w: counter %d is %s, recorded is %s",
				release.ErrCounterConflict, m.Counter, app.Version, base.Version))
		}
	}
	if base.Counter > 0 && m.Counter == base.Counter && app.Version == base.Version {
		art, ctxDigest, derr := p.Store.BaselineDigests(req.App)
		if derr != nil {
			return v, derr
		}
		if (art != "" && art != app.Artifact.SHA256) || (ctxDigest != "" && ctxDigest != m.ContextDigest) {
			return v, failed(CheckCounterConflict, fmt.Errorf("%w: counter %d and version %s were already built from different digests",
				release.ErrCounterConflict, m.Counter, app.Version))
		}
	}
	return v, nil
}

// Plan is the read-only preview: signature, counter, local artifact (if
// supplied) and build-context digest, and the disk-space check. It
// fetches nothing; the apply re-verifies everything.
type Plan struct {
	App            string `json:"app"`
	Version        string `json:"version"`
	Counter        int64  `json:"counter"`
	Tag            string `json:"tag"`
	KeyID          string `json:"key_id"`
	ArtifactName   string `json:"artifact_name"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	ArtifactSize   int64  `json:"artifact_size"`
	// Source is "local" (owner-supplied file, verified now) or "download"
	// (fetched and verified at build time).
	Source        string `json:"source"`
	ContextDigest string `json:"context_digest"`
	Override      bool   `json:"override"`
	DiskRequired  int64  `json:"disk_required"`
	Baseline      string `json:"baseline"`
}

// Preview runs the checks that need no network and no build.
func (p *Pipeline) Preview(ctx context.Context, req Request) (Plan, Verified, error) {
	v, err := p.verifyManifest(req)
	if err != nil {
		return Plan{}, v, err
	}
	plan := Plan{App: v.App.Name, Version: v.App.Version, Counter: v.Manifest.Counter, Tag: Tag(v.App.Name, v.App.Version),
		KeyID: v.KeyID, ArtifactName: v.App.Artifact.Name, ArtifactSHA256: v.App.Artifact.SHA256, ArtifactSize: v.App.Artifact.Size,
		Source: "download", ContextDigest: v.Manifest.ContextDigest, Override: v.Override, DiskRequired: p.required(v.App.Artifact.Size)}
	if v.Baseline.Counter > 0 {
		plan.Baseline = fmt.Sprintf("%s (counter %d)", v.Baseline.Version, v.Baseline.Counter)
	}
	if req.ArtifactPath != "" {
		plan.Source = "local"
		if err := checkLocalArtifact(req.ArtifactPath, v.App.Artifact); err != nil {
			return plan, v, err
		}
		if err := scanArchive(req.ArtifactPath, v.App.Artifact.Name, p.maxExpanded()); err != nil {
			return plan, v, failed(CheckArtifact, err)
		}
	}
	got, err := TreeDigest(p.ContextDir)
	if err != nil {
		return plan, v, failed(CheckContext, err)
	}
	if got != v.Manifest.ContextDigest {
		return plan, v, failed(CheckContext, fmt.Errorf("build context digest %s does not match the signed %s", got, v.Manifest.ContextDigest))
	}
	if err := p.preflight(ctx, v.App.Artifact.Size); err != nil {
		return plan, v, err
	}
	return plan, v, nil
}

// Check previews a request and records the outcome as the app's
// verdict (input refusals are not verdicts). The Plan is meaningful on
// refusals too, so callers can show what was checked.
func (p *Pipeline) Check(ctx context.Context, req Request) (Plan, Verdict, error) {
	plan, v, err := p.Preview(ctx, req)
	verdict := Verdict{App: req.App, Actor: req.Actor, KeyID: v.KeyID, Override: v.Override}
	if v.App.Name != "" {
		verdict.Version, verdict.Counter = v.App.Version, v.Manifest.Counter
	}
	if err != nil {
		var ce *CheckError
		verdict.State, verdict.Refusal, verdict.Detail = StateFailed, CheckBuild, err.Error()
		if errors.As(err, &ce) {
			verdict.Refusal = ce.Check
			if ce.Check == CheckInput {
				return plan, verdict, err
			}
		}
	} else {
		verdict.State = StateVerified
		verdict.Detail = fmt.Sprintf("verified %s counter %d with key %s; build context digest matches (%s)", plan.Version, plan.Counter, plan.KeyID, plan.Source)
	}
	if serr := p.Store.SaveVerdict(verdict); serr != nil && err == nil {
		return plan, verdict, serr
	}
	return plan, verdict, err
}

// required is the free space a build needs: the artifact and its staged
// copy, the extracted tree and the image layer (each bounded by the
// expansion cap), plus headroom.
func (p *Pipeline) required(size int64) int64 { return 2*size + 2*p.maxExpanded() + DiskHeadroom }

func (p *Pipeline) free(path string) (uint64, error) {
	if p.FreeBytes != nil {
		return p.FreeBytes(path)
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// preflight refuses before fetching or building when the staging
// directory or the Docker data root lacks space for the artifact plus
// layer headroom.
func (p *Pipeline) preflight(ctx context.Context, size int64) error {
	need := p.required(size)
	if err := ensureStaging(p.StagingDir); err != nil {
		return failed(CheckDisk, fmt.Errorf("staging directory: %v", err))
	}
	targets := []struct{ what, path string }{{"staging directory", p.StagingDir}}
	if root, err := p.Builder.RootDir(ctx); err == nil && root != "" {
		targets = append(targets, struct{ what, path string }{"Docker data root", root})
	}
	for _, t := range targets {
		free, err := p.free(t.path)
		if err != nil {
			if t.what == "staging directory" {
				return failed(CheckDisk, fmt.Errorf("cannot read free space of the %s: %v", t.what, err))
			}
			continue // the Docker root may be remote or unreadable
		}
		if free < uint64(need) {
			return failed(CheckDisk, fmt.Errorf("the %s has %d MiB free but this build needs %d MiB (twice the artifact, twice the %d MiB expansion cap, plus %d MiB headroom); free space and retry",
				t.what, free>>20, need>>20, p.maxExpanded()>>20, DiskHeadroom>>20))
		}
	}
	return nil
}

// checkLocalArtifact verifies an owner-supplied file in place (preview).
func checkLocalArtifact(path string, want Artifact) error {
	f, err := release.OpenRegularFile(path, want.Size)
	if err != nil {
		if errors.Is(err, release.ErrTooLarge) {
			return failed(CheckArtifact, fmt.Errorf("local artifact is larger than the signed size %d", want.Size))
		}
		return failed(CheckInput, fmt.Errorf("%w: artifact must be a readable regular file", ErrInput))
	}
	defer f.Close()
	sum, n, err := hashCopy(io.Discard, f, want.Size)
	if err != nil {
		return failed(CheckInput, fmt.Errorf("%w: artifact unreadable", ErrInput))
	}
	return compareArtifact(sum, n, want)
}

func compareArtifact(sum string, n int64, want Artifact) error {
	if n != want.Size {
		return failed(CheckArtifact, fmt.Errorf("artifact is %d bytes, the signed manifest says %d", n, want.Size))
	}
	if sum != want.SHA256 {
		return failed(CheckArtifact, fmt.Errorf("artifact SHA-256 %s does not match the signed %s", sum, want.SHA256))
	}
	return nil
}

// hashCopy copies at most limit+1 bytes of src to dst, returning the
// SHA-256 and count; a count above limit means the source was longer.
func hashCopy(dst io.Writer, src io.Reader, limit int64) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), io.LimitReader(src, limit+1))
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// tailBuffer keeps the last max bytes written.
type tailBuffer struct {
	max int
	buf []byte
}

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.buf = append(t.buf, b...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(b), nil
}

// String returns the captured tail with credential shapes redacted.
func (t *tailBuffer) String() string { return audit.RedactText(string(t.buf)) }

type run struct {
	p      *Pipeline
	req    Request
	v      Verified
	tmp    string
	artTmp string
	ctxDir string
	stage  string
	out    *tailBuffer
	built  bool
	locked bool
	// bridge marks an Everything bridge build (ADR-057): the pin replaces
	// the signed manifest, the bridge context replaces the craft context.
	bridge *BridgeApp
}

func (p *Pipeline) newRun(req Request) *run {
	return &run{p: p, req: req, out: &tailBuffer{max: MaxOutput}}
}

// Definition returns the verify-then-build operation. Steps re-verify
// from the files, never trusting an earlier plan. Any failure leaves no
// partial tag and no recorded image, and is saved as the app's verdict.
func (p *Pipeline) Definition(req Request) (operation.Definition, error) {
	if req.App == "" || req.ManifestPath == "" || req.SignaturePath == "" {
		return operation.Definition{}, failed(CheckInput, fmt.Errorf("%w: app, manifest and signature are required", ErrInput))
	}
	r := p.newRun(req)
	step := func(name string, fn func(ctx context.Context) error) operation.StepDef {
		return operation.StepDef{Name: name, Run: func(ctx context.Context) error {
			err := fn(ctx)
			if err != nil {
				r.recordFailure(ctx, err)
			}
			return err
		}}
	}
	return operation.Definition{
		Type: OperationType,
		Steps: []operation.StepDef{
			step("verify-manifest", r.stepVerify),
			step("preflight-disk", r.stepPreflight),
			step("obtain-artifact", r.stepArtifact),
			step("scan-archive", r.stepScan),
			step("verify-context", r.stepContext),
			step("build", r.stepBuild),
			step("record", r.stepRecord),
		},
		Cleanup: r.cleanup,
	}, nil
}

func (r *run) stepVerify(ctx context.Context) error {
	if !r.p.acquire(ctx) {
		return failed(CheckCancelled, ctx.Err())
	}
	r.locked = true
	v, err := r.p.verifyManifest(r.req)
	r.v = v
	return err
}

func (r *run) stepPreflight(ctx context.Context) error {
	return r.p.preflight(ctx, r.v.App.Artifact.Size)
}

func (r *run) stepArtifact(ctx context.Context) error {
	if err := ensureStaging(r.p.StagingDir); err != nil {
		return failed(CheckDisk, fmt.Errorf("staging directory: %v", err))
	}
	tmp, err := os.MkdirTemp(r.p.StagingDir, "craft-"+r.v.App.Name+"-")
	if err != nil {
		return failed(CheckDisk, fmt.Errorf("staging directory: %v", err))
	}
	r.tmp = tmp
	art := r.v.App.Artifact
	r.artTmp = filepath.Join(tmp, "artifact-"+art.Name)
	dst, err := os.OpenFile(r.artTmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return failed(CheckDisk, fmt.Errorf("staging artifact: %v", err))
	}
	defer dst.Close()
	if r.req.ArtifactPath != "" {
		src, err := release.OpenRegularFile(r.req.ArtifactPath, art.Size)
		if err != nil {
			if errors.Is(err, release.ErrTooLarge) {
				return failed(CheckArtifact, fmt.Errorf("local artifact is larger than the signed size %d", art.Size))
			}
			return failed(CheckInput, fmt.Errorf("%w: artifact must be a readable regular file", ErrInput))
		}
		defer src.Close()
		// The bytes hashed are the bytes staged: the owner's file can
		// change afterwards without effect.
		sum, n, err := hashCopy(dst, src, art.Size)
		if err != nil {
			return failed(CheckInput, fmt.Errorf("%w: artifact unreadable", ErrInput))
		}
		return compareArtifact(sum, n, art)
	}
	if len(art.URLs) == 0 {
		return failed(CheckInput, fmt.Errorf("%w: the manifest lists no artifact URL; supply the artifact file locally", ErrInput))
	}
	var failures []string
	mismatch := false
	for _, raw := range art.URLs {
		if err := dst.Truncate(0); err != nil {
			return failed(CheckDisk, err)
		}
		if _, err := dst.Seek(0, io.SeekStart); err != nil {
			return failed(CheckDisk, err)
		}
		h := sha256.New()
		cw := &countWriter{w: io.MultiWriter(dst, h)}
		err := r.p.fetch(ctx, raw, cw, art.Size+1)
		if ctx.Err() != nil {
			return failed(CheckCancelled, ctx.Err())
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", raw, err))
			continue
		}
		if cerr := compareArtifact(hex.EncodeToString(h.Sum(nil)), cw.n, art); cerr != nil {
			// A mirror serving other bytes is skipped, not trusted; the
			// next mirror may serve the signed file.
			mismatch = true
			failures = append(failures, fmt.Sprintf("%s: %v", raw, errors.Unwrap(cerr)))
			continue
		}
		return nil
	}
	if mismatch {
		return failed(CheckArtifact, fmt.Errorf("no signed URL served the signed artifact: %s", strings.Join(failures, "; ")))
	}
	return failed(CheckFetch, fmt.Errorf("could not fetch the artifact from any signed URL: %s", strings.Join(failures, "; ")))
}

func (r *run) stepScan(ctx context.Context) error {
	if err := scanArchive(r.artTmp, r.v.App.Artifact.Name, r.p.maxExpanded()); err != nil {
		return failed(CheckArtifact, err)
	}
	return nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.n += int64(n)
	return n, err
}

func (p *Pipeline) fetch(ctx context.Context, raw string, dst io.Writer, limit int64) error {
	if p.Fetch != nil {
		return p.Fetch(ctx, raw, dst, limit)
	}
	return httpFetch(ctx, raw, dst, limit)
}

func httpFetch(ctx context.Context, raw string, dst io.Writer, limit int64) error {
	return fetchWith(ctx, nil, raw, dst, limit)
}

// hardenClient copies base (nil: a default client) with the fetch
// policy: https only with at most 5 https redirects, a bounded time, and
// no transparent decompression (the signed digest is over the bytes as
// published, never over a decoded body).
func hardenClient(base *http.Client) *http.Client {
	client := &http.Client{Timeout: 30 * time.Minute}
	var transport *http.Transport
	if base != nil {
		client.Transport = base.Transport
		if t, ok := base.Transport.(*http.Transport); ok {
			transport = t.Clone()
		}
	} else {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	if transport != nil {
		transport.DisableCompression = true
		client.Transport = transport
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) >= 5 {
			return errors.New("redirect refused (https only, at most 5)")
		}
		return nil
	}
	return client
}

func fetchWith(ctx context.Context, base *http.Client, raw string, dst io.Writer, limit int64) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return fmt.Errorf("refusing non-https URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := hardenClient(base).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if enc := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding"))); enc != "" && enc != "identity" {
		return fmt.Errorf("server applied Content-Encoding %q; the signed digest covers the published bytes", enc)
	}
	_, err = io.Copy(dst, io.LimitReader(resp.Body, limit))
	return err
}

func (r *run) stepContext(ctx context.Context) error {
	r.ctxDir = filepath.Join(r.tmp, "ctx")
	src := r.p.ContextDir
	if r.bridge != nil {
		src = r.p.BridgeContextDir
	}
	if err := copyContext(src, r.ctxDir); err != nil {
		return failed(CheckContext, err)
	}
	// The digest is taken of the private copy the build will use.
	got, err := TreeDigest(r.ctxDir)
	if err != nil {
		return failed(CheckContext, err)
	}
	if got != r.v.Manifest.ContextDigest {
		return failed(CheckContext, fmt.Errorf("build context digest %s does not match the signed %s", got, r.v.Manifest.ContextDigest))
	}
	if err := checkDockerfilePinned(filepath.Join(r.ctxDir, "Dockerfile")); err != nil {
		return failed(CheckContext, err)
	}
	artDir := filepath.Join(r.ctxDir, ReservedContextEntry)
	if err := os.MkdirAll(artDir, 0o700); err != nil {
		return failed(CheckDisk, err)
	}
	if err := os.Rename(r.artTmp, filepath.Join(artDir, r.v.App.Artifact.Name)); err != nil {
		return failed(CheckDisk, err)
	}
	return nil
}

func randomSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (r *run) stepBuild(ctx context.Context) error {
	app := r.v.App
	r.stage = fmt.Sprintf("homestead/craft-%s:%s-build-%s", app.Name, app.Version, randomSuffix())
	args := map[string]string{"ARTIFACT_FILE": app.Artifact.Name, "ARTIFACT_SHA256": app.Artifact.SHA256}
	if r.bridge != nil {
		r.stage = fmt.Sprintf("homestead/everything-%s:%s-build-%s", app.Name, app.Version, randomSuffix())
		args["APP"] = app.Name
	}
	err := r.p.Builder.Build(ctx, BuildSpec{
		Dir: r.ctxDir, Network: r.bridge != nil, Tag: r.stage, Output: r.out,
		Args: args,
	})
	if ctx.Err() != nil {
		return failed(CheckCancelled, fmt.Errorf("build cancelled: %w", ctx.Err()))
	}
	if err != nil {
		return failed(CheckBuild, fmt.Errorf("docker build failed: %v\n%s", err, lastLines(r.out.String(), 2048)))
	}
	r.built = true
	return nil
}

func lastLines(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = "..." + s[len(s)-max:]
	}
	return s
}

func (r *run) stepRecord(ctx context.Context) error {
	app := r.v.App
	id, err := r.p.Builder.ImageID(ctx, r.stage)
	if err != nil || id == "" {
		return failed(CheckBuild, fmt.Errorf("built image not found: %v", err))
	}
	final := Tag(app.Name, app.Version)
	if r.bridge != nil {
		final = BridgeTag(app.Name, app.Version)
	}
	prev, prevErr := r.p.Store.Image(app.Name, app.Version)
	prevID, _ := r.p.Builder.ImageID(ctx, final)
	if err := r.p.Builder.Tag(ctx, id, final); err != nil {
		return failed(CheckRecord, fmt.Errorf("tag %s: %v", final, err))
	}
	img := Image{App: app.Name, Version: app.Version, Tag: final, ImageID: id, Counter: r.v.Manifest.Counter, KeyID: r.v.KeyID,
		ArtifactSHA256: app.Artifact.SHA256, ContextDigest: r.v.Manifest.ContextDigest, ManifestSHA256: r.v.ManifestSHA256, BuiltAt: time.Now().UTC()}
	put := func() error { return r.p.Store.putImage(img, r.v.Override) }
	if r.bridge != nil {
		// A bridge build has no manifest counter: it records the image
		// only, never a craft baseline.
		put = func() error { return r.p.Store.putBridgeImage(img) }
	}
	if err := put(); err != nil {
		// Restore the previous tag (or drop the new one): no current tag
		// may point at an unrecorded image.
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if prevErr == nil && prevID != "" && prevID == prev.ImageID {
			_ = r.p.Builder.Tag(bg, prevID, final)
		} else {
			_ = r.p.Builder.Remove(bg, final)
		}
		if errors.Is(err, ErrBaselineLowered) {
			return failed(CheckCounter, err)
		}
		return failed(CheckRecord, err)
	}
	return r.p.Store.SaveVerdict(Verdict{App: app.Name, Version: app.Version, Counter: r.v.Manifest.Counter, State: StateBuilt,
		Detail: fmt.Sprintf("built %s as %s from manifest counter %d (key %s)", final, id, r.v.Manifest.Counter, r.v.KeyID),
		KeyID:  r.v.KeyID, Override: r.v.Override, Actor: r.req.Actor})
}

// recordFailure saves the failing check as the app's verdict.
func (r *run) recordFailure(ctx context.Context, err error) {
	check := CheckBuild
	var ce *CheckError
	if errors.As(err, &ce) {
		check = ce.Check
	}
	v := Verdict{App: r.req.App, State: StateFailed, Refusal: check, Detail: err.Error(), Actor: r.req.Actor, KeyID: r.v.KeyID}
	if r.v.App.Name != "" {
		v.Version, v.Counter = r.v.App.Version, r.v.Manifest.Counter
	}
	_ = r.p.Store.SaveVerdict(v)
}

// cleanup removes the staging tree and the staging tag on every path.
func (r *run) cleanup() {
	if r.locked {
		r.locked = false
		defer r.p.release()
	}
	if r.tmp != "" {
		_ = os.RemoveAll(r.tmp)
	}
	if r.stage != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = r.p.Builder.Remove(ctx, r.stage)
	}
}

// Prune removes recorded builds of app other than keepVersion (and their
// tags), returning what it removed. The update flow calls it after the
// new version is applied, so the previous image stays available until
// then. A tag still in use by a container is left and reported.
func (p *Pipeline) Prune(ctx context.Context, app, keepVersion string) (removed []string, err error) {
	images, err := p.Store.Images()
	if err != nil {
		return nil, err
	}
	var problems []string
	for _, img := range images {
		if img.App != app || img.Version == keepVersion {
			continue
		}
		if err := p.Builder.Remove(ctx, img.Tag); err != nil && !errors.Is(err, ErrImageMissing) {
			problems = append(problems, fmt.Sprintf("%s: %v", img.Tag, err))
			continue
		}
		if err := p.Store.deleteImage(img.App, img.Version); err != nil {
			problems = append(problems, err.Error())
			continue
		}
		removed = append(removed, img.Tag)
	}
	if len(problems) > 0 {
		return removed, fmt.Errorf("craft: prune: %s", strings.Join(problems, "; "))
	}
	return removed, nil
}

// Errors for CheckImage.
var (
	ErrNotBuilt      = errors.New("craft: image has no recorded local build")
	ErrImageMismatch = errors.New("craft: local image does not match the recorded digest")
	ErrNotCraftImage = errors.New("craft: not a local craft image reference")
)

// CheckImage is the install and start gate: image must be a craft tag,
// have a recorded build, and the local image behind the tag must have the
// recorded ID. Nothing here builds or repairs.
func (p *Pipeline) CheckImage(ctx context.Context, image string) error {
	app, version, ok := ParseRef(image)
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotCraftImage, image)
	}
	rec, err := p.Store.Image(app, version)
	if errors.Is(err, ErrNoImage) {
		return fmt.Errorf("%w: %s; build it with homesteadctl craft build %s", ErrNotBuilt, image, app)
	}
	if err != nil {
		return err
	}
	id, err := p.Builder.ImageID(ctx, image)
	if errors.Is(err, ErrImageMissing) {
		return fmt.Errorf("%w: %s is recorded but missing locally; rebuild with homesteadctl craft build %s", ErrImageMismatch, image, app)
	}
	if err != nil {
		return err
	}
	if id != rec.ImageID {
		return fmt.Errorf("%w: %s is %s, recorded %s; rebuild with homesteadctl craft build %s", ErrImageMismatch, image, id, rec.ImageID, app)
	}
	return nil
}

// RecordedID returns the recorded local image ID for a craft tag, the
// reference drift detection compares running containers against.
func (p *Pipeline) RecordedID(image string) (string, bool) {
	app, version, ok := ParseRef(image)
	if !ok {
		return "", false
	}
	rec, err := p.Store.Image(app, version)
	if err != nil {
		return "", false
	}
	return rec.ImageID, true
}
