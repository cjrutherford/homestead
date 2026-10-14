package platformapply

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"homestead/internal/audit"
	"homestead/internal/operation"
	"homestead/internal/release"
	"homestead/internal/store"
)

type fakeSystemd struct {
	mu        sync.Mutex
	started   []string
	restarts  []string
	startErr  error
	restartFn func() error
	pid       int
	active    bool
	startedCh chan struct{}
}

func newFakeSystemd() *fakeSystemd { return &fakeSystemd{pid: 4242, startedCh: make(chan struct{}, 4)} }

func (f *fakeSystemd) Start(_ context.Context, unit string) error {
	f.mu.Lock()
	f.started = append(f.started, unit)
	err := f.startErr
	f.mu.Unlock()
	if err == nil {
		f.startedCh <- struct{}{}
	}
	return err
}

func (f *fakeSystemd) Restart(_ context.Context, unit string) error {
	f.mu.Lock()
	f.restarts = append(f.restarts, unit)
	fn := f.restartFn
	f.mu.Unlock()
	if fn != nil {
		return fn()
	}
	return nil
}

func (f *fakeSystemd) MainPID(context.Context, string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pid, nil
}

func (f *fakeSystemd) Active(context.Context, string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active, nil
}

type env struct {
	t       *testing.T
	dir     string
	st      *store.Store
	bin     string
	staging string
	art     string
	sd      *fakeSystemd
	a       *Applier
	r       *Runner
	pub     ed25519.PublicKey
	priv    ed25519.PrivateKey
	keyID   [8]byte
	files   release.Inputs
	execSHA func(pid int) (string, error)
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &env{t: t, dir: dir, st: st, bin: filepath.Join(dir, "bin"), staging: filepath.Join(dir, "state", "staging"),
		art: filepath.Join(dir, "art"), sd: newFakeSystemd()}
	for _, d := range []string{e.bin, filepath.Join(dir, "state"), e.art} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{"homestead-server": "old daemon", "homesteadctl": "old ctl"} {
		if err := os.WriteFile(filepath.Join(e.bin, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.pub, e.priv, _ = ed25519.GenerateKey(rand.Reader)
	_, _ = rand.Read(e.keyID[:])
	rel := release.NewStore(st)
	if _, err := rel.AddKey(e.pubText(), "test", "tester"); err != nil {
		t.Fatal(err)
	}
	e.a = &Applier{
		Releases: rel, Markers: NewStore(st), Runner: operation.NewRunner(st), Ops: operation.New(st),
		Audit: audit.New(st), Systemd: e.sd, BinDir: e.bin, StagingDir: e.staging,
		Unit: "homestead-server.service", ApplyUnit: "homestead-apply.service", Running: "0.1.0",
		Listen: "127.0.0.1:8080", SocketPath: "/run/homestead/homestead.sock", AwaitPoll: 5 * time.Millisecond,
	}
	e.execSHA = func(int) (string, error) { return hashFile(filepath.Join(e.bin, "homestead-server")) }
	e.r = &Runner{
		Releases: rel, Markers: e.a.Markers, Ops: e.a.Ops, Audit: e.a.Audit, Systemd: e.sd,
		Unit: "homestead-server.service", StagingDir: e.staging,
		HealthTimeout: 150 * time.Millisecond, HealthInterval: 5 * time.Millisecond,
		ExecDigest: func(pid int) (string, error) { return e.execSHA(pid) },
	}
	return e
}

func (e *env) pubText() string {
	b := append([]byte("Ed"), e.keyID[:]...)
	b = append(b, e.pub...)
	return "untrusted comment: test\n" + base64.StdEncoding.EncodeToString(b) + "\n"
}

func (e *env) sign(sums []byte) string {
	sig := ed25519.Sign(e.priv, sums)
	raw := append(append([]byte("Ed"), e.keyID[:]...), sig...)
	global := ed25519.Sign(e.priv, append(append([]byte(nil), sig...), []byte("t")...))
	return "untrusted comment: s\n" + base64.StdEncoding.EncodeToString(raw) + "\ntrusted comment: t\n" + base64.StdEncoding.EncodeToString(global) + "\n"
}

// release writes a signed release for component and records it verified,
// as `updates verify` would. It returns the request for it.
func (e *env) release(component, version string, counter int64, content string) Request {
	e.t.Helper()
	dir, _ := os.MkdirTemp(e.art, "rel")
	artifact := filepath.Join(dir, component)
	sums := []byte(fmt.Sprintf("version=%s\ncounter=%d\n%s  %s\n", version, counter, sum([]byte(content)), component))
	for path, data := range map[string][]byte{artifact: []byte(content), filepath.Join(dir, "SUMS"): sums, filepath.Join(dir, "SUMS.minisig"): []byte(e.sign(sums))} {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
	inst, _ := e.a.Releases.Installed(component)
	keys, _ := e.a.Releases.PublicKeys()
	res, err := release.Verify(release.Inputs{ArtifactPath: artifact, ChecksumsPath: filepath.Join(dir, "SUMS"),
		SignaturePath: filepath.Join(dir, "SUMS.minisig"), Trusted: keys, Installed: inst, AllowOlder: true})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.a.Releases.SaveRelease(release.Release{Component: component, Version: version, Counter: counter,
		State: release.StateVerified, SHA256: res.SHA256, KeyID: res.KeyID, Override: res.Override, Actor: "a"}); err != nil {
		e.t.Fatal(err)
	}
	return Request{Component: component, Artifact: artifact, Checksums: filepath.Join(dir, "SUMS"),
		Signature: filepath.Join(dir, "SUMS.minisig"), Actor: "owner"}
}

func (e *env) read(name string) string {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.bin, name))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func (e *env) entries() []audit.Entry {
	e.t.Helper()
	entries, err := e.a.Audit.List(0)
	if err != nil {
		e.t.Fatal(err)
	}
	return entries
}

// start begins an apply and waits for the handoff (marker written, apply
// unit started).
func (e *env) start(req Request) operation.Operation {
	e.t.Helper()
	op, err := e.a.Start(context.Background(), req)
	if err != nil {
		e.t.Fatal(err)
	}
	select {
	case <-e.sd.startedCh:
	case <-time.After(5 * time.Second):
		e.t.Fatal("apply unit was never started")
	}
	return op
}

func (e *env) wait(id int64) operation.Operation {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	op, err := e.a.Runner.Wait(ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return op
}

func (e *env) staged() []string {
	entries, _ := os.ReadDir(e.staging)
	var names []string
	for _, en := range entries {
		names = append(names, en.Name())
	}
	return names
}

func TestApplyViaApplyUnit(t *testing.T) {
	e := newEnv(t)
	req := e.release("homestead-server", "0.2.0", 7, "new daemon")
	if err := e.a.Releases.RecordInstalled("homestead-server", "0.1.0", 3); err != nil {
		t.Fatal(err)
	}
	op := e.start(req)
	m, ok, _ := e.a.Markers.Pending()
	if !ok || m.OpID != op.ID || m.ToVersion != "0.2.0" || m.AllowOlder || m.Listen != "127.0.0.1:8080" {
		t.Fatalf("marker = %+v ok=%v", m, ok)
	}
	if got := e.read("homestead-server"); got != "old daemon" {
		t.Fatalf("the daemon side must not touch binaries, got %q", got)
	}
	if len(e.staged()) != 3 {
		t.Fatalf("staged = %v", e.staged())
	}
	// A restarted daemon's recovery pass keeps the marker's operation.
	if err := operation.NewRunner(e.st).RecoverExcept(m.OpID); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.a.Ops.Get(op.ID); got.Status != operation.StatusRunning {
		t.Fatalf("recovery touched the marked operation: %s", got.Status)
	}
	if err := e.r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := e.wait(op.ID)
	if got.Status != operation.StatusSucceeded || !strings.Contains(got.Result, `"to_version":"0.2.0"`) {
		t.Fatalf("op = %+v", got)
	}
	names := []string{}
	for _, s := range got.Steps {
		names = append(names, s.Name)
		if s.Status != operation.StatusSucceeded {
			t.Fatalf("step %s = %s", s.Name, s.Status)
		}
	}
	if strings.Join(names, ",") != "stage,handoff,apply,verify-staged,preserve-previous,replace,restart,health-check" {
		t.Fatalf("steps = %v", names)
	}
	if e.read("homestead-server") != "new daemon" || e.read("homestead-server.previous") != "old daemon" {
		t.Fatal("binary swap or previous wrong")
	}
	if info, _ := os.Stat(filepath.Join(e.bin, "homestead-server")); info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", info.Mode())
	}
	if len(e.sd.restarts) != 1 || e.sd.restarts[0] != "homestead-server.service" {
		t.Fatalf("restarts = %v", e.sd.restarts)
	}
	prev, ok, _ := e.a.Markers.Previous("homestead-server")
	if !ok || prev.Version != "0.1.0" || prev.Counter != 3 || prev.SHA256 != sum([]byte("old daemon")) {
		t.Fatalf("previous = %+v", prev)
	}
	if in, _ := e.a.Releases.Installed("homestead-server"); in.Version != "0.2.0" || in.Counter != 7 {
		t.Fatalf("installed = %+v", in)
	}
	if _, ok, _ := e.a.Markers.Pending(); ok {
		t.Fatal("marker not cleared")
	}
	if len(e.staged()) != 0 {
		t.Fatalf("staging not cleaned: %v", e.staged())
	}
	var started, finalized bool
	for _, en := range e.entries() {
		if en.Action == ActionApply && strings.Contains(en.Detail, "0.1.0 (counter 3) -> 0.2.0 (counter 7)") {
			started = true
		}
		if en.Action == ActionFinalize && en.Outcome == audit.OutcomeOK && strings.Contains(en.Detail, "0.1.0 (counter 3) -> 0.2.0 (counter 7)") {
			finalized = true
		}
	}
	if !started || !finalized {
		t.Fatalf("audit before/after: started=%v finalized=%v", started, finalized)
	}
	if err := e.r.Run(context.Background()); !errors.Is(err, ErrNoMarker) {
		t.Fatalf("second run = %v", err)
	}
}

func TestStagedFilesReverifiedFromFiles(t *testing.T) {
	cases := map[string]func(e *env, m Marker){
		"tampered artifact": func(e *env, m Marker) { _ = os.WriteFile(m.StagedArtifact, []byte("evil"), 0o755) },
		"unsigned swap of artifact and checksums": func(e *env, m Marker) {
			_ = os.WriteFile(m.StagedArtifact, []byte("evil"), 0o755)
			sums := fmt.Sprintf("version=0.2.0\ncounter=7\n%s  homestead-server\n", sum([]byte("evil")))
			_ = os.WriteFile(m.StagedChecksums, []byte(sums), 0o600)
		},
		"bad signature": func(e *env, m Marker) { _ = os.WriteFile(m.StagedSignature, []byte("junk"), 0o600) },
		"database row alone is not enough": func(e *env, m Marker) {
			// A different but validly signed release swapped in: the
			// database still says the original is verified.
			other := e.release("homestead-server", "0.3.0", 8, "other daemon")
			for src, dst := range map[string]string{other.Artifact: m.StagedArtifact, other.Checksums: m.StagedChecksums, other.Signature: m.StagedSignature} {
				b, _ := os.ReadFile(src)
				_ = os.WriteFile(dst, b, 0o755)
			}
		},
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			req := e.release("homestead-server", "0.2.0", 7, "new daemon")
			op := e.start(req)
			m, _, _ := e.a.Markers.Pending()
			tamper(e, m)
			err := e.r.Run(context.Background())
			if err == nil || !strings.Contains(err.Error(), "not changed") {
				t.Fatalf("err = %v", err)
			}
			if e.read("homestead-server") != "old daemon" {
				t.Fatal("binary replaced from tampered files")
			}
			if got := e.wait(op.ID); got.Status != operation.StatusFailed {
				t.Fatalf("op = %+v", got)
			}
			if _, ok, _ := e.a.Markers.Pending(); ok {
				t.Fatal("marker left")
			}
			if len(e.sd.restarts) != 0 {
				t.Fatal("daemon restarted")
			}
		})
	}
}

// A failed post-restart health check (S273) restores the previous binary
// inside the apply unit, restarts, and finalizes the operation as
// failed (rolled back) with both versions audited. The baseline never
// records the failed release.
func TestHealthFailureRollsBackAutomatically(t *testing.T) {
	cases := map[string]func(e *env){
		"wrong digest": func(e *env) {
			e.execSHA = func(int) (string, error) {
				h, _ := hashFile(filepath.Join(e.bin, "homestead-server"))
				if h == sum([]byte("new daemon")) {
					return "deadbeef", nil
				}
				return h, nil
			}
		},
		"never running": func(e *env) {
			e.sd.restartFn = func() error {
				e.sd.mu.Lock()
				defer e.sd.mu.Unlock()
				if len(e.sd.restarts) == 1 {
					e.sd.pid = 0
				} else {
					e.sd.pid = 4242
				}
				return nil
			}
		},
		"probe fails": func(e *env) {
			e.r.Probe = func(_ context.Context, m Marker) error {
				if m.ToVersion == "0.2.0" {
					return errors.New("listener down")
				}
				return nil
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			req := e.release("homestead-server", "0.2.0", 7, "new daemon")
			if err := e.a.Releases.RecordInstalled("homestead-server", "0.1.0", 3); err != nil {
				t.Fatal(err)
			}
			op := e.start(req)
			setup(e)
			err := e.r.Run(context.Background())
			if err == nil {
				t.Fatal("expected the operation to end failed")
			}
			for _, want := range []string{"failed (rolled back)", "was not healthy within", "rolled back to 0.1.0", "0.2.0"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q missing %q", err, want)
				}
			}
			got := e.wait(op.ID)
			if got.Status != operation.StatusFailed || !strings.HasPrefix(got.Error, "failed (rolled back)") {
				t.Fatalf("op = %+v", got)
			}
			if e.read("homestead-server") != "old daemon" || e.read("homestead-server.previous") != "old daemon" ||
				e.read("homestead-server.rolledback") != "new daemon" {
				t.Fatal("binaries: previous must be installed again, with the failed one kept")
			}
			if len(e.sd.restarts) != 2 {
				t.Fatalf("restarts = %v, want the apply restart and the rollback restart", e.sd.restarts)
			}
			if in, _ := e.a.Releases.Installed("homestead-server"); in.Version != "0.1.0" || in.Counter != 3 {
				t.Fatalf("baseline recorded the failed release: %+v", in)
			}
			if _, ok, _ := e.a.Markers.Pending(); ok {
				t.Fatal("marker left to block future applies")
			}
			var audited bool
			for _, en := range e.entries() {
				if en.Action == ActionFinalize && en.Outcome == audit.OutcomeError && strings.Contains(en.Detail, "failed (rolled back)") &&
					strings.Contains(en.Detail, "0.2.0 (counter 7)") && strings.Contains(en.Detail, "0.1.0 (counter 3") {
					audited = true
				}
			}
			if !audited {
				t.Fatalf("rollback not audited with both versions: %+v", e.entries())
			}
		})
	}
}

// The rollback itself failing is surfaced loudly and nothing is deleted.
func TestRollbackRestartFailureIsLoudAndKeepsBothBinaries(t *testing.T) {
	e := newEnv(t)
	req := e.release("homestead-server", "0.2.0", 7, "new daemon")
	_ = e.a.Releases.RecordInstalled("homestead-server", "0.1.0", 3)
	op := e.start(req)
	e.execSHA = func(int) (string, error) { return "deadbeef", nil }
	e.sd.restartFn = func() error {
		if len(e.sd.restarts) >= 2 {
			return errors.New("systemd refused")
		}
		return nil
	}
	err := e.r.Run(context.Background())
	if err == nil {
		t.Fatal("expected failure")
	}
	for _, want := range []string{"rollback failed", "systemd refused", "both binaries are kept", "homestead-server.previous", "homestead-server.rolledback", "systemctl restart"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
	got := e.wait(op.ID)
	if got.Status != operation.StatusFailed || !strings.HasPrefix(got.Error, "rollback failed") {
		t.Fatalf("op = %+v", got)
	}
	if e.read("homestead-server.previous") != "old daemon" || e.read("homestead-server.rolledback") != "new daemon" {
		t.Fatal("a binary was lost")
	}
	if in, _ := e.a.Releases.Installed("homestead-server"); in.Version != "0.1.0" {
		t.Fatalf("baseline = %+v", in)
	}
	var audited bool
	for _, en := range e.entries() {
		if en.Action == ActionFinalize && en.Outcome == audit.OutcomeError && strings.Contains(en.Detail, "rollback failed") {
			audited = true
		}
	}
	if !audited {
		t.Fatal("rollback failure not audited")
	}
	if _, ok, _ := e.a.Markers.Pending(); ok {
		t.Fatal("marker left behind")
	}
}

// applied applies release content as `from` then returns, leaving a
// recorded previous binary behind.
func (e *env) applied(component, content string, counter int64, version string) {
	e.t.Helper()
	req := e.release(component, version, counter, content)
	e.start(req)
	if err := e.r.Run(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) rollbackStart(req RollbackRequest) (operation.Operation, error) {
	e.t.Helper()
	op, err := e.a.Rollback(context.Background(), req)
	if err != nil {
		return op, err
	}
	select {
	case <-e.sd.startedCh:
	case <-time.After(5 * time.Second):
		e.t.Fatal("apply unit was never started")
	}
	return op, nil
}

func TestManualRollbackDaemon(t *testing.T) {
	e := newEnv(t)
	_ = e.a.Releases.RecordInstalled("homestead-server", "0.1.0", 9)
	// The apply moves the baseline to counter 10; the previous is counter 9.
	e.applied("homestead-server", "new daemon", 10, "0.2.0")
	e.a.Running = "0.2.0"
	// Counter 9 < installed 10: refused without the override, and audited.
	if _, err := e.a.Rollback(context.Background(), RollbackRequest{Component: "homestead-server", Actor: "owner"}); !errors.Is(err, ErrOlder) {
		t.Fatalf("err = %v, want ErrOlder", err)
	}
	if e.read("homestead-server") != "new daemon" {
		t.Fatal("refusal changed the binary")
	}
	op, err := e.rollbackStart(RollbackRequest{Component: "homestead-server", Actor: "owner", AllowOlder: true})
	if err != nil {
		t.Fatal(err)
	}
	m, ok, _ := e.a.Markers.Pending()
	if !ok || m.Kind != KindRollback || m.ToVersion != "0.1.0" || m.FromVersion != "0.2.0" || !m.Override {
		t.Fatalf("marker = %+v", m)
	}
	// The apply unit runs with a daemon reporting the restored binary.
	if err := e.r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := e.wait(op.ID)
	if got.Status != operation.StatusSucceeded || !strings.Contains(got.Result, `"rollback":true`) {
		t.Fatalf("op = %+v", got)
	}
	if e.read("homestead-server") != "old daemon" || e.read("homestead-server.rolledback") != "new daemon" || e.read("homestead-server.previous") != "old daemon" {
		t.Fatal("binary state wrong after rollback")
	}
	if in, _ := e.a.Releases.Installed("homestead-server"); in.Version != "0.1.0" || in.Counter != 9 {
		t.Fatalf("installed = %+v", in)
	}
	if len(e.sd.restarts) != 2 {
		t.Fatalf("restarts = %v", e.sd.restarts)
	}
	var started, finalized bool
	for _, en := range e.entries() {
		if en.Action == ActionRollback && en.Override && strings.Contains(en.Detail, "0.2.0 (counter 10) -> 0.1.0 (counter 9)") &&
			strings.Contains(en.OverrideReason, "0.2.0") && strings.Contains(en.OverrideReason, "0.1.0") {
			started = true
		}
		if en.Action == ActionFinalize && en.Outcome == audit.OutcomeOK && en.Override && strings.Contains(en.Detail, "rolled back homestead-server") {
			finalized = true
		}
	}
	if !started || !finalized {
		t.Fatalf("audit: started=%v finalized=%v: %+v", started, finalized, e.entries())
	}
	// Already on the previous binary: nothing to roll back.
	if _, err := e.a.Rollback(context.Background(), RollbackRequest{Component: "homestead-server", Actor: "owner", AllowOlder: true}); !errors.Is(err, ErrCurrent) {
		t.Fatalf("err = %v, want ErrCurrent", err)
	}
}

func TestManualRollbackRefusals(t *testing.T) {
	e := newEnv(t)
	// No previous recorded.
	if _, err := e.a.Rollback(context.Background(), RollbackRequest{Component: "homestead-server", Actor: "owner"}); !errors.Is(err, ErrNoPrevious) {
		t.Fatalf("err = %v, want ErrNoPrevious", err)
	}
	if _, err := e.a.Rollback(context.Background(), RollbackRequest{Component: "nope", Actor: "owner"}); !errors.Is(err, ErrInput) {
		t.Fatalf("err = %v", err)
	}
	// A previous binary that no longer matches its record is refused.
	if err := e.a.Markers.SavePrevious(Previous{Component: "homestead-server", Version: "0.0.9", Counter: 0, SHA256: sum([]byte("old daemon")), Path: filepath.Join(e.bin, "homestead-server.previous")}); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(e.bin, "homestead-server.previous"), []byte("older daemon"), 0o755)
	if _, err := e.a.Rollback(context.Background(), RollbackRequest{Component: "homestead-server", Actor: "owner"}); !errors.Is(err, ErrDigest) {
		t.Fatalf("a previous binary that does not match its record must be refused: %v", err)
	}
}

func TestManualRollbackReverifiesPreviousDigestInTheUnit(t *testing.T) {
	e := newEnv(t)
	_ = e.a.Releases.RecordInstalled("homestead-server", "0.1.0", 3)
	e.applied("homestead-server", "new daemon", 7, "0.2.0")
	e.a.Running = "0.2.0"
	op, err := e.rollbackStart(RollbackRequest{Component: "homestead-server", Actor: "owner", AllowOlder: true})
	if err != nil {
		t.Fatal(err)
	}
	// Swapped after the daemon checked it: the unit must notice.
	_ = os.WriteFile(filepath.Join(e.bin, "homestead-server.previous"), []byte("evil"), 0o755)
	if err := e.r.Run(context.Background()); err == nil {
		t.Fatal("expected refusal")
	}
	if got := e.wait(op.ID); got.Status != operation.StatusFailed {
		t.Fatalf("op = %+v", got)
	}
	if e.read("homestead-server") != "new daemon" {
		t.Fatal("binary changed despite a bad previous")
	}
	if _, ok, _ := e.a.Markers.Pending(); ok {
		t.Fatal("marker left")
	}
}

func TestManualRollbackCLIComponentWithoutRestart(t *testing.T) {
	e := newEnv(t)
	_ = e.a.Releases.RecordInstalled("homesteadctl", "0.1.0", 3)
	e.applied("homesteadctl", "new ctl", 7, "0.2.0")
	restartsBefore := len(e.sd.restarts)
	op, err := e.rollbackStart(RollbackRequest{Component: "homesteadctl", Actor: "owner", AllowOlder: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.wait(op.ID); got.Status != operation.StatusSucceeded {
		t.Fatalf("op = %+v", got)
	}
	if e.read("homesteadctl") != "old ctl" {
		t.Fatal("CLI not restored")
	}
	if len(e.sd.restarts) != restartsBefore {
		t.Fatal("CLI rollback must not restart the daemon")
	}
	if in, _ := e.a.Releases.Installed("homesteadctl"); in.Version != "0.1.0" || in.Counter != 3 {
		t.Fatalf("installed = %+v", in)
	}
}

func TestAutomaticRollbackCLIComponent(t *testing.T) {
	e := newEnv(t)
	_ = e.a.Releases.RecordInstalled("homesteadctl", "0.1.0", 3)
	req := e.release("homesteadctl", "0.2.0", 7, "new ctl")
	op := e.start(req)
	// The installed CLI is corrupted right after the swap (the directory
	// sync hook runs then), so the on-disk digest check fails; the
	// restored previous passes.
	orig := syncDir
	t.Cleanup(func() { syncDir = orig })
	syncDir = func(dir string) error {
		path := filepath.Join(e.bin, "homesteadctl")
		if b, _ := os.ReadFile(path); string(b) == "new ctl" {
			_ = os.WriteFile(path, []byte("corrupt"), 0o755)
		}
		return orig(dir)
	}
	err := e.r.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed (rolled back)") {
		t.Fatalf("err = %v", err)
	}
	if e.read("homesteadctl") != "old ctl" || len(e.sd.restarts) != 0 {
		t.Fatalf("CLI rollback state wrong (restarts %v)", e.sd.restarts)
	}
	if got := e.wait(op.ID); got.Status != operation.StatusFailed {
		t.Fatalf("op = %+v", got)
	}
}

func TestOlderCounterNeedsOverrideAndIsAudited(t *testing.T) {
	e := newEnv(t)
	req := e.release("homestead-server", "0.0.9", 5, "older daemon")
	if err := e.a.Releases.RecordInstalled("homestead-server", "0.1.0", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.Start(context.Background(), req); !errors.Is(err, ErrOlder) {
		t.Fatalf("err = %v, want ErrOlder", err)
	}
	req.AllowOlder = true
	op := e.start(req)
	var override bool
	for _, en := range e.entries() {
		if en.Action == ActionApply && en.Override && strings.Contains(en.OverrideReason, "0.1.0 (counter 10)") && strings.Contains(en.OverrideReason, "0.0.9 (counter 5)") {
			override = true
		}
	}
	if !override {
		t.Fatal("override not audited with both versions")
	}
	if err := e.r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.wait(op.ID); got.Status != operation.StatusSucceeded {
		t.Fatalf("op = %+v", got)
	}
	if in, _ := e.a.Releases.Installed("homestead-server"); in.Counter != 5 {
		t.Fatalf("installed = %+v", in)
	}
}

func TestNoCounterBaselineNeverDemandsOverride(t *testing.T) {
	// A dev build with an unparseable running version and no installed
	// record is not an "older counter": it applies without the override.
	e := newEnv(t)
	e.a.Running = "dev"
	req := e.release("homestead-server", "0.0.1", 1, "first daemon")
	op := e.start(req)
	for _, en := range e.entries() {
		if en.Override {
			t.Fatalf("override audited without an older counter: %+v", en)
		}
	}
	if err := e.r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.wait(op.ID); got.Status != operation.StatusSucceeded {
		t.Fatalf("op = %+v", got)
	}
}

func TestUnverifiedAndChangedReleasesNeverApplied(t *testing.T) {
	e := newEnv(t)
	if _, err := e.a.Start(context.Background(), Request{Component: "homestead-server", Artifact: "/a", Checksums: "/b", Signature: "/c", Actor: "o"}); !errors.Is(err, ErrNotVerified) {
		t.Fatalf("no release: %v", err)
	}
	req := e.release("homestead-server", "0.2.0", 7, "new daemon")
	for _, state := range []string{release.StateFailed, release.StateUnverified} {
		if err := e.a.Releases.SaveRelease(release.Release{Component: "homestead-server", Version: "0.2.0", Counter: 7,
			State: state, SHA256: sum([]byte("new daemon")), Refusal: release.RefusalRollback}); err != nil {
			t.Fatal(err)
		}
		req.AllowOlder = true
		if _, err := e.a.Start(context.Background(), req); !errors.Is(err, ErrNotVerified) {
			t.Fatalf("%s: err = %v", state, err)
		}
	}
	req = e.release("homestead-server", "0.2.0", 7, "new daemon")
	if err := os.WriteFile(req.Artifact, []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.Start(context.Background(), req); !errors.Is(err, ErrNotVerified) {
		t.Fatalf("tampered artifact: %v", err)
	}
	// A different validly signed release than the one verified.
	other := e.release("homestead-server", "0.3.0", 9, "other")
	rec := e.release("homestead-server", "0.2.0", 7, "new daemon")
	_ = rec
	if _, err := e.a.Start(context.Background(), other); !errors.Is(err, ErrDigest) {
		t.Fatalf("files differ from the verified record: %v", err)
	}
	for _, bad := range []Request{
		{Component: "nope", Artifact: "/a", Checksums: "/b", Signature: "/c", Actor: "o"},
		{Component: "homestead-server", Artifact: "rel/x", Checksums: "/b", Signature: "/c", Actor: "o"},
	} {
		if _, err := e.a.Start(context.Background(), bad); !errors.Is(err, ErrInput) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
	if e.read("homestead-server") != "old daemon" {
		t.Fatal("binary touched")
	}
}

func TestSameVersionIsCurrent(t *testing.T) {
	e := newEnv(t)
	req := e.release("homestead-server", "0.1.0", 7, "same")
	if _, err := e.a.Start(context.Background(), req); !errors.Is(err, ErrCurrent) {
		t.Fatalf("err = %v", err)
	}
}

func TestConcurrentApplyRefused(t *testing.T) {
	e := newEnv(t)
	req := e.release("homestead-server", "0.2.0", 7, "new daemon")
	e.start(req)
	if _, err := e.a.Start(context.Background(), req); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	other := &Applier{Releases: e.a.Releases, Markers: e.a.Markers, Runner: e.a.Runner, Ops: e.a.Ops, Audit: e.a.Audit,
		Systemd: e.sd, BinDir: e.bin, StagingDir: e.staging, Unit: e.a.Unit, ApplyUnit: e.a.ApplyUnit, Running: e.a.Running}
	if _, err := other.Start(context.Background(), req); !errors.Is(err, ErrBusy) {
		t.Fatalf("marker err = %v", err)
	}
	if err := e.a.Markers.Begin(Marker{OpID: 99, Component: "x"}); !errors.Is(err, ErrPending) {
		t.Fatalf("second marker = %v", err)
	}
}

func TestHandoffFailureClearsEverything(t *testing.T) {
	e := newEnv(t)
	e.sd.startErr = errors.New("Unit homestead-apply.service not found")
	req := e.release("homestead-server", "0.2.0", 7, "new daemon")
	op, err := e.a.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	got := e.wait(op.ID)
	if got.Status != operation.StatusFailed || !strings.Contains(got.Error, "make install") {
		t.Fatalf("op = %+v", got)
	}
	if _, ok, _ := e.a.Markers.Pending(); ok {
		t.Fatal("marker left")
	}
	if len(e.staged()) != 0 {
		t.Fatalf("staging left: %v", e.staged())
	}
}

func TestRestartRequestFailureUndoesReplacement(t *testing.T) {
	e := newEnv(t)
	e.sd.restartFn = func() error { return errors.New("no systemd") }
	req := e.release("homestead-server", "0.2.0", 7, "new daemon")
	op := e.start(req)
	err := e.r.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "replacement undone") || !strings.Contains(err.Error(), "was not changed") {
		t.Fatalf("err = %v", err)
	}
	if e.read("homestead-server") != "old daemon" {
		t.Fatal("old binary not back")
	}
	if len(e.sd.restarts) != 2 {
		t.Fatalf("expected a second restart attempt on the old binary, got %d", len(e.sd.restarts))
	}
	if got := e.wait(op.ID); got.Status != operation.StatusFailed {
		t.Fatalf("op = %+v", got)
	}
}

func TestDaemonSideNeverTimesOutOrDoubleFinalizes(t *testing.T) {
	e := newEnv(t)
	req := e.release("homestead-server", "0.2.0", 7, "new daemon")
	op := e.start(req)
	// The apply unit is slow: the daemon-side wait must not fail the
	// operation on its own.
	time.Sleep(150 * time.Millisecond)
	if got, _ := e.a.Ops.Get(op.ID); got.Status != operation.StatusRunning {
		t.Fatalf("op = %s while the apply unit still owns it", got.Status)
	}
	if err := e.r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := e.wait(op.ID)
	time.Sleep(50 * time.Millisecond) // let the daemon-side step observe it
	again, _ := e.a.Ops.Get(op.ID)
	if got.Status != operation.StatusSucceeded || again.Status != operation.StatusSucceeded || again.Error != "" {
		t.Fatalf("op = %+v / %+v", got, again)
	}
	for _, en := range e.entries() {
		if en.Outcome == audit.OutcomeError {
			t.Fatalf("error audit on a successful apply: %+v", en)
		}
	}
}

func TestCancelledOperationAbortsUnitWithoutChange(t *testing.T) {
	e := newEnv(t)
	req := e.release("homestead-server", "0.2.0", 7, "new daemon")
	op := e.start(req)
	if err := e.a.Runner.Cancel(op.ID); err != nil {
		t.Fatal(err)
	}
	err := e.r.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no longer open") {
		t.Fatalf("err = %v", err)
	}
	if e.read("homestead-server") != "old daemon" {
		t.Fatal("binary changed for a cancelled operation")
	}
	if _, ok, _ := e.a.Markers.Pending(); ok {
		t.Fatal("marker left")
	}
}

func TestClearApply(t *testing.T) {
	e := newEnv(t)
	if _, err := e.a.ClearApply(context.Background(), "owner"); !errors.Is(err, ErrNoPending) {
		t.Fatalf("nothing pending: %v", err)
	}
	req := e.release("homestead-server", "0.2.0", 7, "new daemon")
	op := e.start(req)
	e.sd.active = true
	if _, err := e.a.ClearApply(context.Background(), "owner"); !errors.Is(err, ErrBusy) {
		t.Fatalf("active unit: %v", err)
	}
	e.sd.active = false
	m, err := e.a.ClearApply(context.Background(), "owner")
	if err != nil || m.OpID != op.ID {
		t.Fatalf("clear = %+v, %v", m, err)
	}
	got := e.wait(op.ID)
	if got.Status != operation.StatusFailed || !strings.Contains(got.Error, "cleared by owner") {
		t.Fatalf("op = %+v", got)
	}
	if _, ok, _ := e.a.Markers.Pending(); ok {
		t.Fatal("marker left")
	}
	var audited bool
	for _, en := range e.entries() {
		if en.Action == ActionClear && en.Actor == "owner" {
			audited = true
		}
	}
	if !audited {
		t.Fatal("clear not audited")
	}
	// New applies are possible again.
	e.start(e.release("homestead-server", "0.2.0", 7, "new daemon"))
}

func TestFirstCLIApplyUsesCLIOwnFrom(t *testing.T) {
	e := newEnv(t)
	e.a.Running = "0.2.0" // the daemon already runs the release version
	req := e.release("homesteadctl", "0.2.0", 7, "new ctl")
	op := e.start(req)
	m, _, _ := e.a.Markers.Pending()
	if strings.Contains(m.FromVersion, "0.2.0") || !strings.HasPrefix(m.FromVersion, "unrecorded (sha256 ") {
		t.Fatalf("from = %q, must come from the CLI's own state", m.FromVersion)
	}
	if err := e.r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.wait(op.ID); got.Status != operation.StatusSucceeded {
		t.Fatalf("op = %+v", got)
	}
	if e.read("homesteadctl") != "new ctl" || e.read("homesteadctl.previous") != "old ctl" {
		t.Fatal("CLI not replaced with previous kept")
	}
	if len(e.sd.restarts) != 0 {
		t.Fatal("CLI apply must not restart the daemon")
	}
	if in, _ := e.a.Releases.Installed("homesteadctl"); in.Version != "0.2.0" || in.Counter != 7 {
		t.Fatalf("installed = %+v", in)
	}
	// The same release again is current (identical binary).
	if _, err := e.a.Start(context.Background(), req); !errors.Is(err, ErrCurrent) {
		t.Fatalf("err = %v", err)
	}
}

func TestSyncDirFailureAfterRenameKeepsTheSwap(t *testing.T) {
	e := newEnv(t)
	real := syncDir
	t.Cleanup(func() { syncDir = real })
	syncDir = func(dir string) error {
		if b, _ := os.ReadFile(filepath.Join(e.bin, "homestead-server")); string(b) == "new daemon" {
			return errors.New("fsync: input/output error")
		}
		return real(dir)
	}
	req := e.release("homestead-server", "0.2.0", 7, "new daemon")
	op := e.start(req)
	if err := e.r.Run(context.Background()); err != nil {
		t.Fatalf("a durable-only failure must not fail the apply: %v", err)
	}
	if got := e.wait(op.ID); got.Status != operation.StatusSucceeded || e.read("homestead-server") != "new daemon" {
		t.Fatalf("op = %+v", got)
	}
	var noted bool
	for _, en := range e.entries() {
		if strings.Contains(en.Detail, "fsync of its directory failed") {
			noted = true
		}
	}
	if !noted {
		t.Fatal("fsync failure not recorded")
	}
}

func TestRecordInstalledFailureFailsOperationAndClearsMarker(t *testing.T) {
	for _, component := range []string{"homesteadctl", "homestead-server"} {
		t.Run(component, func(t *testing.T) {
			e := newEnv(t)
			e.r.RecordInstalled = func(string, string, int64) error { return errors.New("database is locked") }
			req := e.release(component, "0.2.0", 7, "new "+component)
			op := e.start(req)
			err := e.r.Run(context.Background())
			if err == nil || !strings.Contains(err.Error(), "could not be recorded") {
				t.Fatalf("err = %v", err)
			}
			if got := e.wait(op.ID); got.Status != operation.StatusFailed {
				t.Fatalf("op = %+v", got)
			}
			if _, ok, _ := e.a.Markers.Pending(); ok {
				t.Fatal("marker left to block future applies")
			}
			// A later apply can start.
			e.r.RecordInstalled = nil
			if _, err := e.a.Start(context.Background(), e.release(component, "0.3.0", 8, "newer "+component)); err != nil {
				t.Fatalf("later apply refused: %v", err)
			}
		})
	}
}

func TestRestoreCommandAndRestorePrevious(t *testing.T) {
	dir := t.TempDir()
	bin, prev := filepath.Join(dir, "bin"), filepath.Join(dir, "bin.previous")
	_ = os.WriteFile(bin, []byte("new"), 0o755)
	_ = os.WriteFile(prev, []byte("old"), 0o755)
	if err := RestorePrevious(bin, prev, sum([]byte("nope"))); err == nil {
		t.Fatal("digest mismatch must refuse")
	}
	if b, _ := os.ReadFile(bin); string(b) != "new" {
		t.Fatal("binary touched on refusal")
	}
	if err := RestorePrevious(bin, prev, sum([]byte("old"))); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bin); string(b) != "old" {
		t.Fatalf("restored = %q", b)
	}
	cmd := RestoreCommand(Marker{BinaryPath: bin, PreviousPath: prev}, "homestead-server")
	if !strings.Contains(cmd, "mv "+bin+".restore "+bin) || !strings.HasSuffix(cmd, "systemctl restart homestead-server") || !strings.Contains(cmd, "reset-failed") {
		t.Fatalf("command = %s", cmd)
	}
}
