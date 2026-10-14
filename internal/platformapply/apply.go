// Package platformapply applies a verified platform update (S265,
// ADR-001, ADR-025): stage the artifact, keep the previous binary,
// replace atomically, and restart homestead-server through systemd.
//
// The restart ends the process that runs the apply operation, so the
// operation cannot finish itself. Instead a persisted marker (SQLite)
// is written before the binary is replaced; the next daemon start reads
// it, health-checks the new binary, and finalizes the operation (see
// Finalizer). Automatic rollback is S273: this package only marks a
// failed health check failed, keeps the previous binary, and tells the
// owner how to restore it. Finalizer.OnFailure and RestorePrevious are
// the seams S273 plugs into.
package platformapply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"homestead/internal/audit"
	"homestead/internal/operation"
	"homestead/internal/release"
	"homestead/internal/update"
)

// OperationType is the operation type of an apply.
const OperationType = "platform.update"

// Step names, in execution order (the CLI component skips restart).
const (
	StepStage    = "stage"
	StepPreserve = "preserve-previous"
	StepReplace  = "replace"
	StepRestart  = "restart"
	StepFinalize = "finalize"
)

// Audit actions.
const (
	ActionApply    = "platform.apply"
	ActionFinalize = "platform.apply.finalize"
)

// Refusals, mapped to HTTP statuses by the route.
var (
	ErrInput       = errors.New("platformapply: invalid request")
	ErrNotVerified = errors.New("platformapply: release is not verified")
	ErrOlder       = errors.New("platformapply: release is older than the installed release")
	ErrDigest      = errors.New("platformapply: artifact does not match the verified release")
	ErrCurrent     = errors.New("platformapply: release is already installed")
	ErrBusy        = errors.New("platformapply: a platform update is already running or pending")
)

// Systemd restarts a unit. The production implementation queues the job
// and returns; tests substitute a fake.
type Systemd interface {
	Restart(ctx context.Context, unit string) error
}

// ExecSystemd restarts through systemctl. It uses `systemctl
// --no-block restart`: the restart job is queued inside systemd (PID 1)
// and the command returns at once, so the job survives the daemon (and
// the systemctl child with it) being stopped. A plain blocking
// `systemctl restart` would be killed with the unit's cgroup mid-job.
// systemd-run is avoided: a transient unit adds a second unit to track
// and needs nothing this does not already have (the daemon runs as root
// and already drives systemctl for the tunnel connector).
type ExecSystemd struct{}

// Restart queues a restart of unit.
func (ExecSystemd) Restart(ctx context.Context, unit string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "--no-block", "restart", unit).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl restart %s: %w: %s", unit, err, out)
	}
	return nil
}

// Applier starts apply operations.
type Applier struct {
	Releases *release.Store
	Markers  *Store
	Runner   *operation.Runner
	Ops      *operation.Store
	Audit    *audit.Service
	Systemd  Systemd
	// BinDir holds the installed binaries (/usr/local/bin); StagingDir is
	// the root-owned staging directory under the layout's state dir.
	BinDir     string
	StagingDir string
	Unit       string
	// Running is the running daemon version.
	Running string
	// RestartWait bounds how long the restart step waits for the daemon
	// to be stopped after the restart was queued.
	RestartWait time.Duration

	mu sync.Mutex
	// beforeReplace, set by tests, runs between the staged-digest
	// verification of stage and the final check of replace.
	beforeReplace func(staged string)
}

// Request is one apply. Artifact is the owner-supplied file the release
// was verified from; its bytes must still match the digest verification
// recorded.
type Request struct {
	Component  string
	Artifact   string
	Actor      string
	AllowOlder bool
}

type plan struct {
	rel         release.Release
	inst        release.Installed
	from        string
	override    bool
	overrideWhy string
}

func (a *Applier) binPath(component string) string { return filepath.Join(a.BinDir, component) }

// preflight decides whether req may start: only verified releases,
// older counters only with the explicit override.
func (a *Applier) preflight(req Request) (plan, error) {
	var p plan
	if !update.IsPlatformComponent(req.Component) {
		return p, fmt.Errorf("%w: unknown platform component %q", ErrInput, req.Component)
	}
	if !filepath.IsAbs(req.Artifact) {
		return p, fmt.Errorf("%w: artifact must be an absolute path on the appliance", ErrInput)
	}
	if req.Actor == "" {
		return p, fmt.Errorf("%w: actor is required", ErrInput)
	}
	releases, err := a.Releases.Releases()
	if err != nil {
		return p, err
	}
	found := false
	for _, r := range releases {
		if r.Component == req.Component {
			p.rel, found = r, true
		}
	}
	if !found || p.rel.State != release.StateVerified || p.rel.Version == "" || len(p.rel.SHA256) != 64 {
		return p, fmt.Errorf("%w: verify %s first (homesteadctl updates verify)", ErrNotVerified, req.Component)
	}
	if err := (update.PlatformRelease{Component: req.Component, Version: p.rel.Version}).Validate(); err != nil {
		return p, fmt.Errorf("%w: %v", ErrNotVerified, err)
	}
	if p.inst, err = a.Releases.Installed(req.Component); err != nil {
		return p, err
	}
	p.from = a.Running
	if req.Component != update.ComponentDaemon && p.inst.Version != "" {
		p.from = p.inst.Version
	}
	switch {
	case p.inst.Counter > 0 && p.rel.Counter < p.inst.Counter:
		p.override = true
		p.overrideWhy = fmt.Sprintf("owner allowed older release: installed %s (counter %d), applying %s (counter %d)",
			p.inst.Version, p.inst.Counter, p.rel.Version, p.rel.Counter)
	case p.inst.Counter > 0 && p.rel.Counter == p.inst.Counter && p.rel.Version == p.inst.Version:
		return p, fmt.Errorf("%w: %s %s", ErrCurrent, req.Component, p.rel.Version)
	case p.inst.Counter > 0 && p.rel.Counter == p.inst.Counter:
		return p, fmt.Errorf("%w: same counter %d but a different version", ErrNotVerified, p.rel.Counter)
	case p.rel.Version == p.from:
		return p, fmt.Errorf("%w: %s %s", ErrCurrent, req.Component, p.rel.Version)
	default:
		// No counter policy yet (or newer counter): the planner still
		// refuses version downgrades.
		running := map[string]string{req.Component: p.from}
		if _, err := update.PlanPlatform(running, []update.PlatformRelease{{
			Component: req.Component, Version: p.rel.Version, Verification: update.VerificationVerified}}); err != nil {
			p.override = true
			p.overrideWhy = fmt.Sprintf("owner allowed older release: running %s, applying %s", p.from, p.rel.Version)
		}
	}
	if p.rel.Override && !p.override {
		p.override = true
		p.overrideWhy = "release was verified with the older-release override: " + p.rel.Detail
	}
	if p.override && !req.AllowOlder {
		return p, fmt.Errorf("%w: %s; re-run with the explicit older-release override (audited)", ErrOlder, p.overrideWhy)
	}
	if err := release.VerifyDigestUnchanged(req.Artifact, p.rel.SHA256); err != nil {
		return p, fmt.Errorf("%w: %v", ErrDigest, err)
	}
	return p, nil
}

type run struct {
	a      *Applier
	req    Request
	p      plan
	opID   int64
	ready  chan struct{}
	staged string
	prev   Previous
}

// Start validates and begins an apply operation. It returns while the
// operation runs; for the daemon component the operation ends with a
// restart and finishes on the next start (Finalizer).
func (a *Applier) Start(ctx context.Context, req Request) (operation.Operation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, pending, err := a.Markers.Pending(); err != nil {
		return operation.Operation{}, err
	} else if pending {
		return operation.Operation{}, fmt.Errorf("%w: a pending apply is awaiting restart finalization", ErrBusy)
	}
	ops, err := a.Ops.List(0)
	if err != nil {
		return operation.Operation{}, err
	}
	for _, op := range ops {
		if op.Type == OperationType && !operation.Terminal(op.Status) {
			return operation.Operation{}, fmt.Errorf("%w: operation %d", ErrBusy, op.ID)
		}
	}
	p, err := a.preflight(req)
	if err != nil {
		return operation.Operation{}, err
	}
	r := &run{a: a, req: req, p: p, ready: make(chan struct{}), staged: filepath.Join(a.StagingDir, req.Component)}
	steps := []operation.StepDef{
		{Name: StepStage, Run: r.step(StepStage, r.stage)},
		{Name: StepPreserve, Run: r.step(StepPreserve, r.preserve)},
		{Name: StepReplace, Run: r.step(StepReplace, r.replace)},
	}
	if req.Component == update.ComponentDaemon {
		steps = append(steps, operation.StepDef{Name: StepRestart, Run: r.step(StepRestart, r.restart)})
	} else {
		steps = append(steps, operation.StepDef{Name: StepFinalize, Run: r.step(StepFinalize, r.finalizeLocal)})
	}
	if req.Component == update.ComponentDaemon {
		// The next start owns this step: it runs in the new process.
		steps = append(steps, operation.StepDef{Name: StepFinalize, Run: func(context.Context) error {
			return errors.New("finalize runs on the next daemon start")
		}})
	}
	payload, _ := json.Marshal(map[string]any{
		"component": req.Component, "from_version": p.from, "from_counter": p.inst.Counter,
		"to_version": p.rel.Version, "to_counter": p.rel.Counter, "sha256": p.rel.SHA256,
		"allow_older": req.AllowOlder, "override": p.override,
	})
	op, err := a.Runner.Start(ctx, operation.Definition{Type: OperationType, Steps: steps, Cleanup: r.cleanup}, string(payload))
	if err != nil {
		return operation.Operation{}, err
	}
	r.opID = op.ID
	close(r.ready)
	entry := audit.Entry{Actor: req.Actor, Action: ActionApply, Plan: string(payload), Outcome: audit.OutcomeOK,
		Detail: fmt.Sprintf("apply %s started: %s (counter %d) -> %s (counter %d), operation %d",
			req.Component, p.from, p.inst.Counter, p.rel.Version, p.rel.Counter, op.ID)}
	if p.override {
		entry.Override, entry.OverrideReason = true, p.overrideWhy
		entry.Detail += "; " + p.overrideWhy
	}
	a.record(entry)
	return op, nil
}

func (a *Applier) record(entry audit.Entry) {
	// An audit write failure must not abort an apply already underway.
	_, _ = a.Audit.Record(entry)
}

// step wraps a step: waits for the operation id, audits a failure.
func (r *run) step(name string, fn func(context.Context) error) operation.StepFunc {
	return func(ctx context.Context) error {
		<-r.ready
		if err := fn(ctx); err != nil {
			r.a.record(audit.Entry{Actor: r.req.Actor, Action: ActionApply, Outcome: audit.OutcomeError,
				Detail: fmt.Sprintf("apply %s %s -> %s failed at %s: %v", r.req.Component, r.p.from, r.p.rel.Version, name, err)})
			return err
		}
		return nil
	}
}

func (r *run) cleanup() {
	_ = os.Remove(r.staged)
	_ = os.Remove(r.a.binPath(r.req.Component) + ".new")
}

func (r *run) stage(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(r.a.StagingDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(r.a.StagingDir, 0o700); err != nil {
		return err
	}
	entries, _ := os.ReadDir(r.a.StagingDir)
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(r.a.StagingDir, e.Name()))
	}
	_, info, err := openSource(r.req.Artifact)
	if err != nil {
		return err
	}
	sum, n, err := copyFile(r.req.Artifact, r.staged, 0o755)
	if err != nil {
		return err
	}
	if sum != r.p.rel.SHA256 {
		_ = os.Remove(r.staged)
		return fmt.Errorf("%w: copied bytes differ from the verified digest", ErrDigest)
	}
	if st, err := os.Stat(r.staged); err != nil || st.Size() != n || n != info.Size() {
		_ = os.Remove(r.staged)
		return fmt.Errorf("staged copy size mismatch: copied %d bytes of %d", n, info.Size())
	}
	if err := release.VerifyDigestUnchanged(r.staged, r.p.rel.SHA256); err != nil {
		_ = os.Remove(r.staged)
		return fmt.Errorf("%w: staged copy: %v", ErrDigest, err)
	}
	return nil
}

func (r *run) preserve(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	bin := r.a.binPath(r.req.Component)
	prev := bin + ".previous"
	tmp := prev + ".tmp"
	sum, _, err := copyFile(bin, tmp, 0o755)
	if err != nil {
		return fmt.Errorf("preserve previous %s: %w", bin, err)
	}
	if err := os.Rename(tmp, prev); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := syncDir(r.a.BinDir); err != nil {
		return err
	}
	r.prev = Previous{Component: r.req.Component, Version: r.p.from, Counter: r.p.inst.Counter, SHA256: sum, Path: prev}
	return r.a.Markers.SavePrevious(r.prev)
}

func (r *run) replace(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.a.beforeReplace != nil {
		r.a.beforeReplace(r.staged)
	}
	// Immediately before replacement: the staged bytes must still be the
	// verified bytes (time of check to time of use, RELEASE_VERIFICATION).
	if err := release.VerifyDigestUnchanged(r.staged, r.p.rel.SHA256); err != nil {
		return fmt.Errorf("%w: refusing to replace: %v", ErrDigest, err)
	}
	bin := r.a.binPath(r.req.Component)
	tmp := bin + ".new"
	sum, _, err := copyFile(r.staged, tmp, 0o755)
	if err != nil {
		return fmt.Errorf("install %s: %w", bin, err)
	}
	if sum != r.p.rel.SHA256 {
		_ = os.Remove(tmp)
		return fmt.Errorf("%w: installed copy differs from the verified digest", ErrDigest)
	}
	// Marker first: from here a restart (ours or a crash) is finalized
	// by the next start instead of being lost.
	marker := Marker{
		OpID: r.opID, Component: r.req.Component, FromVersion: r.p.from, FromCounter: r.p.inst.Counter,
		ToVersion: r.p.rel.Version, ToCounter: r.p.rel.Counter, SHA256: r.p.rel.SHA256, BinaryPath: bin,
		PreviousPath: r.prev.Path, PreviousSHA: r.prev.SHA256, Override: r.p.override, OverrideWhy: r.p.overrideWhy,
		Actor: r.req.Actor,
	}
	if err := r.a.Markers.Begin(marker); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, bin); err != nil {
		_ = os.Remove(tmp)
		_ = r.a.Markers.Clear(r.opID)
		return fmt.Errorf("replace %s: %w", bin, err)
	}
	if err := syncDir(r.a.BinDir); err != nil {
		return fmt.Errorf("replace %s: fsync directory: %w", bin, err)
	}
	return nil
}

// restart queues the daemon restart and waits to be stopped. It never
// returns success: the process ends, and the next start finalizes. A
// restart that cannot even be queued never ran the new binary, so the
// replacement is undone and the marker cleared; a restart that was
// queued but did not happen leaves the marker, so the owner restarting
// the unit finalizes the apply.
func (r *run) restart(ctx context.Context) error {
	if err := r.a.Systemd.Restart(ctx, r.a.Unit); err != nil {
		m := Marker{BinaryPath: r.a.binPath(r.req.Component), PreviousPath: r.prev.Path, PreviousSHA: r.prev.SHA256}
		if rerr := RestorePrevious(m.BinaryPath, m.PreviousPath, m.PreviousSHA); rerr != nil {
			return fmt.Errorf("restart request failed (%v) and the replacement could not be undone (%v); the pending marker is kept: %s",
				err, rerr, RestoreCommand(m, r.a.Unit))
		}
		_ = r.a.Markers.Clear(r.opID)
		return fmt.Errorf("restart request failed, replacement undone: %w", err)
	}
	wait := r.a.RestartWait
	if wait <= 0 {
		wait = 5 * time.Minute
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return fmt.Errorf("restart was requested but the daemon did not stop within %s; the pending marker is kept, so restarting %s finalizes this apply", wait, r.a.Unit)
	}
}

// finalizeLocal finishes a component that needs no restart (the CLI):
// same health-check and commit path as the daemon's next-start
// finalization, run in this process.
func (r *run) finalizeLocal(ctx context.Context) error {
	marker, ok, err := r.a.Markers.Pending()
	if err != nil || !ok || marker.OpID != r.opID {
		return fmt.Errorf("apply marker missing for operation %d", r.opID)
	}
	f := r.a.Finalizer()
	if err := f.check(ctx, marker); err != nil {
		f.fail(ctx, marker, err)
		return f.failure(marker, err)
	}
	f.commit(marker)
	return nil
}

// Finalizer returns a finalizer sharing this applier's dependencies.
func (a *Applier) Finalizer() *Finalizer {
	return &Finalizer{Releases: a.Releases, Markers: a.Markers, Ops: a.Ops, Audit: a.Audit,
		Version: a.Running, Unit: a.Unit, StagingDir: a.StagingDir}
}

// RestoreCommand is the documented manual restore for a marker: copy
// the preserved previous binary beside the installed one and rename it
// into place (atomic, safe while the binary runs), then restart.
func RestoreCommand(m Marker, unit string) string {
	return fmt.Sprintf("sudo cp -p %s %s.restore && sudo mv %s.restore %s && sudo systemctl restart %s",
		m.PreviousPath, m.BinaryPath, m.BinaryPath, m.BinaryPath, unit)
}
