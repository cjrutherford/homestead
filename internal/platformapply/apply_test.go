package platformapply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	mu     sync.Mutex
	calls  []string
	err    error
	called chan struct{}
}

func newFakeSystemd() *fakeSystemd { return &fakeSystemd{called: make(chan struct{}, 4)} }

func (f *fakeSystemd) Restart(_ context.Context, unit string) error {
	f.mu.Lock()
	f.calls = append(f.calls, unit)
	f.mu.Unlock()
	f.called <- struct{}{}
	return f.err
}

type env struct {
	t        *testing.T
	dbPath   string
	st       *store.Store
	bin      string
	staging  string
	artifact string
	digest   string
	sd       *fakeSystemd
	a        *Applier
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	e := &env{t: t, dbPath: filepath.Join(dir, "h.db"), bin: filepath.Join(dir, "bin"), staging: filepath.Join(dir, "state", "staging"), sd: newFakeSystemd()}
	st, err := store.Open(e.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e.st = st
	for _, d := range []string{e.bin, filepath.Join(dir, "state"), filepath.Join(dir, "art")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{"homestead-server": "old daemon", "homesteadctl": "old ctl"} {
		if err := os.WriteFile(filepath.Join(e.bin, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.a = &Applier{
		Releases: release.NewStore(st), Markers: NewStore(st), Runner: operation.NewRunner(st), Ops: operation.New(st),
		Audit: audit.New(st), Systemd: e.sd, BinDir: e.bin, StagingDir: e.staging, Unit: "homestead-server.service",
		Running: "0.1.0", RestartWait: time.Hour,
	}
	e.artifact = filepath.Join(dir, "art", "homestead-server")
	return e
}

// verified records a verified release for component with artifact content.
func (e *env) verified(component, version string, counter int64, content string) {
	e.t.Helper()
	e.artifact = filepath.Join(filepath.Dir(e.artifact), component)
	if err := os.WriteFile(e.artifact, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	e.digest = sum([]byte(content))
	if err := e.a.Releases.SaveRelease(release.Release{Component: component, Version: version, Counter: counter,
		State: release.StateVerified, SHA256: e.digest, KeyID: "K", Actor: "a"}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) req(component string, allowOlder bool) Request {
	return Request{Component: component, Artifact: e.artifact, Actor: "owner", AllowOlder: allowOlder}
}

func (e *env) waitRestart() {
	e.t.Helper()
	select {
	case <-e.sd.called:
	case <-time.After(5 * time.Second):
		e.t.Fatal("restart was never requested")
	}
}

func (e *env) read(name string) string {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.bin, name))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func (e *env) auditActions() []audit.Entry {
	e.t.Helper()
	entries, err := e.a.Audit.List(0)
	if err != nil {
		e.t.Fatal(err)
	}
	return entries
}

// newProcess simulates the daemon restarting: a fresh handle on the same
// database, the recovery pass that keeps the marker's operation, and a
// finalizer for the new process.
func (e *env) newProcess(version, selfDigest string) (*Finalizer, *store.Store) {
	e.t.Helper()
	st, err := store.Open(e.dbPath)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { st.Close() })
	markers := NewStore(st)
	var keep []int64
	if m, ok, err := markers.Pending(); err != nil {
		e.t.Fatal(err)
	} else if ok {
		keep = append(keep, m.OpID)
	}
	if err := operation.NewRunner(st).RecoverExcept(keep...); err != nil {
		e.t.Fatal(err)
	}
	return &Finalizer{Releases: release.NewStore(st), Markers: markers, Ops: operation.New(st), Audit: audit.New(st),
		Version: version, Unit: "homestead-server.service", StagingDir: e.staging,
		SelfDigest: func() (string, error) { return selfDigest, nil }}, st
}

func (e *env) op(st *store.Store, id int64) operation.Operation {
	e.t.Helper()
	op, err := operation.New(st).Get(id)
	if err != nil {
		e.t.Fatal(err)
	}
	return op
}

func TestApplySuccessAcrossSimulatedRestart(t *testing.T) {
	e := newEnv(t)
	e.verified("homestead-server", "0.2.0", 7, "new daemon")
	if err := e.a.Releases.RecordInstalled("homestead-server", "0.1.0", 3); err != nil {
		t.Fatal(err)
	}
	op, err := e.a.Start(context.Background(), e.req("homestead-server", false))
	if err != nil {
		t.Fatal(err)
	}
	e.waitRestart()
	if got := e.read("homestead-server"); got != "new daemon" {
		t.Fatalf("installed binary = %q", got)
	}
	if got := e.read("homestead-server.previous"); got != "old daemon" {
		t.Fatalf("previous = %q", got)
	}
	info, _ := os.Stat(filepath.Join(e.bin, "homestead-server"))
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", info.Mode())
	}
	prev, ok, _ := e.a.Markers.Previous("homestead-server")
	if !ok || prev.Version != "0.1.0" || prev.Counter != 3 || prev.SHA256 != sum([]byte("old daemon")) {
		t.Fatalf("previous record = %+v", prev)
	}
	// The operation is mid-restart; installed baseline unchanged.
	if in, _ := e.a.Releases.Installed("homestead-server"); in.Counter != 3 {
		t.Fatalf("installed moved before finalization: %+v", in)
	}
	marker, ok, _ := e.a.Markers.Pending()
	if !ok || marker.OpID != op.ID || marker.ToVersion != "0.2.0" {
		t.Fatalf("marker = %+v ok=%v", marker, ok)
	}

	fin, st := e.newProcess("0.2.0", e.digest)
	if got := e.op(st, op.ID); got.Status != operation.StatusRunning {
		t.Fatalf("recovery touched the marked operation: %s", got.Status)
	}
	done, err := fin.Run(context.Background())
	if err != nil || !done {
		t.Fatalf("finalize = %v, %v", done, err)
	}
	got := e.op(st, op.ID)
	if got.Status != operation.StatusSucceeded || !strings.Contains(got.Result, `"to_version":"0.2.0"`) {
		t.Fatalf("op = %+v", got)
	}
	for _, step := range got.Steps {
		if step.Status != operation.StatusSucceeded {
			t.Fatalf("step %s = %s", step.Name, step.Status)
		}
	}
	if in, _ := release.NewStore(st).Installed("homestead-server"); in.Version != "0.2.0" || in.Counter != 7 {
		t.Fatalf("installed = %+v", in)
	}
	if _, ok, _ := NewStore(st).Pending(); ok {
		t.Fatal("marker not cleared")
	}
	if entries, _ := os.ReadDir(e.staging); len(entries) != 0 {
		t.Fatalf("staging not cleaned: %v", entries)
	}
	var started, finalized bool
	for _, entry := range e.auditActions() {
		if entry.Action == ActionApply && strings.Contains(entry.Detail, "0.1.0 (counter 3) -> 0.2.0 (counter 7)") {
			started = true
		}
		if entry.Action == ActionFinalize && entry.Outcome == audit.OutcomeOK && strings.Contains(entry.Detail, "0.1.0 (counter 3) -> 0.2.0 (counter 7)") {
			finalized = true
		}
	}
	if !started || !finalized {
		t.Fatalf("audit missing before/after: started=%v finalized=%v", started, finalized)
	}
	// Finalizing again is a no-op.
	if done, err := fin.Run(context.Background()); done || err != nil {
		t.Fatalf("second run = %v, %v", done, err)
	}
}

func TestPlainRecoverWouldCancelMarkedOperation(t *testing.T) {
	e := newEnv(t)
	e.verified("homestead-server", "0.2.0", 7, "new daemon")
	op, err := e.a.Start(context.Background(), e.req("homestead-server", false))
	if err != nil {
		t.Fatal(err)
	}
	e.waitRestart()
	st, _ := store.Open(e.dbPath)
	defer st.Close()
	if err := operation.NewRunner(st).Recover(); err != nil {
		t.Fatal(err)
	}
	if got := e.op(st, op.ID); got.Status != operation.StatusCancelled {
		t.Fatalf("status = %s", got.Status)
	}
	// The marker still finalizes late: the record stays terminal, the
	// installed release is still recorded and the marker cleared.
	fin := &Finalizer{Releases: release.NewStore(st), Markers: NewStore(st), Ops: operation.New(st), Audit: audit.New(st),
		Version: "0.2.0", Unit: "u", SelfDigest: func() (string, error) { return e.digest, nil }}
	if done, err := fin.Run(context.Background()); !done || err != nil {
		t.Fatalf("late finalize = %v, %v", done, err)
	}
	if in, _ := release.NewStore(st).Installed("homestead-server"); in.Version != "0.2.0" {
		t.Fatalf("installed = %+v", in)
	}
}

func TestDigestChangedBetweenVerifyAndApplyRefused(t *testing.T) {
	e := newEnv(t)
	e.verified("homestead-server", "0.2.0", 7, "new daemon")
	// Swapped before the apply starts.
	if err := os.WriteFile(e.artifact, []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.Start(context.Background(), e.req("homestead-server", false)); !errors.Is(err, ErrDigest) {
		t.Fatalf("err = %v, want ErrDigest", err)
	}
	// Swapped after staging, before replacement.
	e.verified("homestead-server", "0.2.0", 7, "new daemon")
	e.a.beforeReplace = func(staged string) {
		if err := os.WriteFile(staged, []byte("evil"), 0o755); err != nil {
			t.Error(err)
		}
	}
	op, err := e.a.Start(context.Background(), e.req("homestead-server", false))
	if err != nil {
		t.Fatal(err)
	}
	done, err := e.a.Runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != operation.StatusFailed || !strings.Contains(done.Error, "refusing to replace") {
		t.Fatalf("op = %+v", done)
	}
	if got := e.read("homestead-server"); got != "old daemon" {
		t.Fatalf("binary replaced despite digest change: %q", got)
	}
	if _, ok, _ := e.a.Markers.Pending(); ok {
		t.Fatal("marker written for a refused replacement")
	}
	if len(e.sd.calls) != 0 {
		t.Fatal("systemd restarted")
	}
}

func TestHealthCheckFailureMarksFailedKeepsPrevious(t *testing.T) {
	cases := map[string]func(e *env) (*Finalizer, *store.Store){
		"wrong digest":  func(e *env) (*Finalizer, *store.Store) { return e.newProcess("0.2.0", "deadbeef") },
		"wrong version": func(e *env) (*Finalizer, *store.Store) { return e.newProcess("0.1.0", e.digest) },
		"probe fails": func(e *env) (*Finalizer, *store.Store) {
			f, st := e.newProcess("0.2.0", e.digest)
			f.Probe = func(context.Context) error { return errors.New("listener down") }
			return f, st
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.verified("homestead-server", "0.2.0", 7, "new daemon")
			if err := e.a.Releases.RecordInstalled("homestead-server", "0.1.0", 3); err != nil {
				t.Fatal(err)
			}
			op, err := e.a.Start(context.Background(), e.req("homestead-server", false))
			if err != nil {
				t.Fatal(err)
			}
			e.waitRestart()
			fin, fst := mk(e)
			var hooked bool
			fin.OnFailure = func(context.Context, Marker, error) { hooked = true }
			done, ferr := fin.Run(context.Background())
			if !done || ferr == nil {
				t.Fatalf("finalize = %v, %v", done, ferr)
			}
			if !hooked {
				t.Fatal("rollback seam not invoked")
			}
			for _, want := range []string{"restore manually", "homestead-server.previous", "systemctl restart"} {
				if !strings.Contains(ferr.Error(), want) {
					t.Fatalf("error %q missing %q", ferr, want)
				}
			}
			got := e.op(fst, op.ID)
			if got.Status != operation.StatusFailed || !strings.Contains(got.Error, "restore manually") {
				t.Fatalf("op = %+v", got)
			}
			if e.read("homestead-server.previous") != "old daemon" {
				t.Fatal("previous binary lost")
			}
			if in, _ := fin.Releases.Installed("homestead-server"); in.Version != "0.1.0" || in.Counter != 3 {
				t.Fatalf("installed changed on failure: %+v", in)
			}
			if _, ok, _ := fin.Markers.Pending(); ok {
				t.Fatal("marker not cleared")
			}
			var audited bool
			for _, entry := range e.auditActions() {
				if entry.Action == ActionFinalize && entry.Outcome == audit.OutcomeError {
					audited = true
				}
			}
			if !audited {
				t.Fatal("failure not audited")
			}
		})
	}
}

func TestOlderCounterNeedsOverrideAndIsAudited(t *testing.T) {
	e := newEnv(t)
	e.verified("homestead-server", "0.0.9", 5, "older daemon")
	if err := e.a.Releases.RecordInstalled("homestead-server", "0.1.0", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.Start(context.Background(), e.req("homestead-server", false)); !errors.Is(err, ErrOlder) {
		t.Fatalf("err = %v, want ErrOlder", err)
	}
	if e.read("homestead-server") != "old daemon" {
		t.Fatal("binary touched")
	}
	op, err := e.a.Start(context.Background(), e.req("homestead-server", true))
	if err != nil {
		t.Fatal(err)
	}
	e.waitRestart()
	var override bool
	for _, entry := range e.auditActions() {
		if entry.Action == ActionApply && entry.Override &&
			strings.Contains(entry.OverrideReason, "0.1.0 (counter 10)") && strings.Contains(entry.OverrideReason, "0.0.9 (counter 5)") {
			override = true
		}
	}
	if !override {
		t.Fatal("override not audited with both versions")
	}
	fin, st := e.newProcess("0.0.9", e.digest)
	if done, err := fin.Run(context.Background()); !done || err != nil {
		t.Fatalf("finalize = %v, %v", done, err)
	}
	if in, _ := release.NewStore(st).Installed("homestead-server"); in.Counter != 5 {
		t.Fatalf("installed = %+v", in)
	}
	_ = op
}

func TestUnverifiedReleasesNeverApplied(t *testing.T) {
	e := newEnv(t)
	if _, err := e.a.Start(context.Background(), e.req("homestead-server", false)); !errors.Is(err, ErrNotVerified) {
		t.Fatalf("no release: %v", err)
	}
	e.verified("homestead-server", "0.2.0", 7, "new daemon")
	for _, state := range []string{release.StateFailed, release.StateUnverified} {
		if err := e.a.Releases.SaveRelease(release.Release{Component: "homestead-server", Version: "0.2.0", Counter: 7,
			State: state, SHA256: e.digest, Refusal: release.RefusalRollback}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.a.Start(context.Background(), e.req("homestead-server", true)); !errors.Is(err, ErrNotVerified) {
			t.Fatalf("%s: err = %v", state, err)
		}
	}
	if _, err := e.a.Start(context.Background(), Request{Component: "nope", Artifact: e.artifact, Actor: "o"}); !errors.Is(err, ErrInput) {
		t.Fatalf("unknown component: %v", err)
	}
	if _, err := e.a.Start(context.Background(), Request{Component: "homestead-server", Artifact: "rel/x", Actor: "o"}); !errors.Is(err, ErrInput) {
		t.Fatalf("relative path: %v", err)
	}
	if e.read("homestead-server") != "old daemon" {
		t.Fatal("binary touched")
	}
}

func TestSameVersionIsCurrent(t *testing.T) {
	e := newEnv(t)
	e.verified("homestead-server", "0.1.0", 7, "same")
	if _, err := e.a.Start(context.Background(), e.req("homestead-server", false)); !errors.Is(err, ErrCurrent) {
		t.Fatalf("err = %v", err)
	}
	// A plain version downgrade with no counter policy needs the override.
	e.verified("homestead-server", "0.0.5", 2, "lower")
	if _, err := e.a.Start(context.Background(), e.req("homestead-server", false)); !errors.Is(err, ErrOlder) {
		t.Fatalf("downgrade err = %v", err)
	}
}

func TestConcurrentApplyRefused(t *testing.T) {
	e := newEnv(t)
	e.verified("homestead-server", "0.2.0", 7, "new daemon")
	if _, err := e.a.Start(context.Background(), e.req("homestead-server", false)); err != nil {
		t.Fatal(err)
	}
	e.waitRestart()
	if _, err := e.a.Start(context.Background(), e.req("homestead-server", false)); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	// The marker alone also refuses (another process, or a restart gap).
	other := &Applier{Releases: e.a.Releases, Markers: e.a.Markers, Runner: e.a.Runner, Ops: e.a.Ops, Audit: e.a.Audit,
		Systemd: e.sd, BinDir: e.bin, StagingDir: e.staging, Unit: e.a.Unit, Running: e.a.Running}
	if _, err := other.Start(context.Background(), e.req("homestead-server", false)); !errors.Is(err, ErrBusy) {
		t.Fatalf("marker err = %v", err)
	}
	if err := e.a.Markers.Begin(Marker{OpID: 99, Component: "x"}); !errors.Is(err, ErrPending) {
		t.Fatalf("second marker = %v", err)
	}
}

func TestRestartRequestFailureUndoesReplacement(t *testing.T) {
	e := newEnv(t)
	e.verified("homestead-server", "0.2.0", 7, "new daemon")
	e.sd.err = errors.New("no systemd")
	op, err := e.a.Start(context.Background(), e.req("homestead-server", false))
	if err != nil {
		t.Fatal(err)
	}
	done, err := e.a.Runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != operation.StatusFailed || !strings.Contains(done.Error, "replacement undone") {
		t.Fatalf("op = %+v", done)
	}
	if e.read("homestead-server") != "old daemon" {
		t.Fatal("old binary not back")
	}
	if _, ok, _ := e.a.Markers.Pending(); ok {
		t.Fatal("marker left behind")
	}
}

func TestRestartWaitTimeoutKeepsMarker(t *testing.T) {
	e := newEnv(t)
	e.a.RestartWait = 50 * time.Millisecond
	e.verified("homestead-server", "0.2.0", 7, "new daemon")
	op, err := e.a.Start(context.Background(), e.req("homestead-server", false))
	if err != nil {
		t.Fatal(err)
	}
	done, err := e.a.Runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != operation.StatusFailed || !strings.Contains(done.Error, "did not stop") {
		t.Fatalf("op = %+v", done)
	}
	if _, ok, _ := e.a.Markers.Pending(); !ok {
		t.Fatal("marker must stay so a later restart finalizes")
	}
}

func TestCLIComponentAppliesWithoutRestart(t *testing.T) {
	e := newEnv(t)
	e.verified("homesteadctl", "0.2.0", 7, "new ctl")
	op, err := e.a.Start(context.Background(), e.req("homesteadctl", false))
	if err != nil {
		t.Fatal(err)
	}
	done, err := e.a.Runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("op = %+v", done)
	}
	if e.read("homesteadctl") != "new ctl" || e.read("homesteadctl.previous") != "old ctl" {
		t.Fatal("CLI not replaced with previous kept")
	}
	if len(e.sd.calls) != 0 {
		t.Fatal("CLI apply must not restart the daemon")
	}
	if in, _ := e.a.Releases.Installed("homesteadctl"); in.Version != "0.2.0" || in.Counter != 7 {
		t.Fatalf("installed = %+v", in)
	}
	if _, ok, _ := e.a.Markers.Pending(); ok {
		t.Fatal("marker left")
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
	if !strings.Contains(cmd, "mv "+bin+".restore "+bin) || !strings.HasSuffix(cmd, "systemctl restart homestead-server") {
		t.Fatalf("command = %s", cmd)
	}
}
