// Package platformapply applies a verified platform update (S265,
// ADR-001, ADR-025, ADR-027).
//
// The daemon never writes executables. Applier (daemon side) checks the
// release, stages the artifact, checksums and signature into a
// root-owned staging directory, writes the apply marker, and starts the
// privileged oneshot unit homestead-apply.service. Runner (apply unit
// side, `homestead-server apply-platform`) independently re-verifies the
// staged files, keeps the previous binary, swaps atomically, restarts
// the daemon, waits for it to be healthy, and finalizes the same
// operation record from outside the daemon. Automatic rollback is S273:
// Runner.OnFailure and RestorePrevious are its seams.
package platformapply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"homestead/internal/audit"
	"homestead/internal/operation"
	"homestead/internal/release"
	"homestead/internal/update"
)

// OperationType is the operation type of an apply.
const OperationType = "platform.update"

// Daemon-side step names; the apply unit appends its own sub-steps
// (verify-staged, preserve-previous, replace, restart, health-check).
const (
	StepStage   = "stage"
	StepHandoff = "handoff"
	StepApply   = "apply"
)

// Audit actions.
const (
	ActionApply    = "platform.apply"
	ActionFinalize = "platform.apply.finalize"
	ActionClear    = "platform.apply.clear"
)

// Refusals, mapped to HTTP statuses by the route.
var (
	ErrInput       = errors.New("platformapply: invalid request")
	ErrNotVerified = errors.New("platformapply: release is not verified")
	ErrOlder       = errors.New("platformapply: release is older than the installed release")
	ErrDigest      = errors.New("platformapply: files do not match the verified release")
	ErrCurrent     = errors.New("platformapply: release is already installed")
	ErrBusy        = errors.New("platformapply: a platform update is already running or pending")
	ErrNoPending   = errors.New("platformapply: no pending apply to clear")
)

// Systemd is the slice of systemd the apply path uses; tests substitute
// a fake.
type Systemd interface {
	// Start queues a start of unit and returns without waiting.
	Start(ctx context.Context, unit string) error
	// Restart restarts unit and waits for the job.
	Restart(ctx context.Context, unit string) error
	// MainPID returns the unit's main process id (0 when not running).
	MainPID(ctx context.Context, unit string) (int, error)
	// Active reports whether the unit is active or activating.
	Active(ctx context.Context, unit string) (bool, error)
}

// ExecSystemd drives systemctl. Start uses `--no-block`: the daemon
// queues the apply unit and returns, and the unit (its own cgroup)
// survives the daemon being restarted underneath it.
type ExecSystemd struct{}

func systemctl(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Start queues a start of unit.
func (ExecSystemd) Start(ctx context.Context, unit string) error {
	_, err := systemctl(ctx, "--no-block", "start", unit)
	return err
}

// Restart restarts unit and waits.
func (ExecSystemd) Restart(ctx context.Context, unit string) error {
	_, err := systemctl(ctx, "restart", unit)
	return err
}

// MainPID reads the unit's main pid.
func (ExecSystemd) MainPID(ctx context.Context, unit string) (int, error) {
	out, err := systemctl(ctx, "show", "-p", "MainPID", "--value", unit)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// Active reports whether unit is active or activating.
func (ExecSystemd) Active(ctx context.Context, unit string) (bool, error) {
	out, err := exec.CommandContext(ctx, "systemctl", "is-active", unit).CombinedOutput()
	state := strings.TrimSpace(string(out))
	if state == "active" || state == "activating" || state == "reloading" {
		return true, nil
	}
	if err != nil && state == "" {
		return false, err
	}
	return false, nil
}

// Applier is the daemon side: it prepares an apply and hands it to the
// apply unit.
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
	// Unit is the daemon's unit; ApplyUnit is the privileged apply unit.
	Unit      string
	ApplyUnit string
	// Running is the running daemon version.
	Running string
	// Listen and SocketPath are the daemon's listeners; the apply unit
	// health-checks them.
	Listen     string
	SocketPath string
	// AwaitPoll paces the daemon-side wait for the apply unit.
	AwaitPoll time.Duration

	mu sync.Mutex
}

// Request is one apply. The three paths are the files the release was
// verified from; the daemon verifies them again, stages copies, and the
// apply unit verifies the copies once more.
type Request struct {
	Component  string
	Artifact   string
	Checksums  string
	Signature  string
	Actor      string
	AllowOlder bool
}

type plan struct {
	res         release.Result
	inst        release.Installed
	from        string
	override    bool
	overrideWhy string
}

func (a *Applier) binPath(component string) string { return filepath.Join(a.BinDir, component) }

// preflight decides whether req may start: only releases that verify
// from the supplied files and carry a verified record, older counters
// only with the explicit override.
func (a *Applier) preflight(req Request) (plan, error) {
	var p plan
	if !update.IsPlatformComponent(req.Component) {
		return p, fmt.Errorf("%w: unknown platform component %q", ErrInput, req.Component)
	}
	for name, path := range map[string]string{"artifact": req.Artifact, "checksums": req.Checksums, "signature": req.Signature} {
		if !filepath.IsAbs(path) {
			return p, fmt.Errorf("%w: %s must be an absolute path on the appliance", ErrInput, name)
		}
	}
	if req.Actor == "" {
		return p, fmt.Errorf("%w: actor is required", ErrInput)
	}
	records, err := a.Releases.Releases()
	if err != nil {
		return p, err
	}
	var rec release.Release
	found := false
	for _, r := range records {
		if r.Component == req.Component {
			rec, found = r, true
		}
	}
	if !found || rec.State != release.StateVerified || rec.Version == "" || len(rec.SHA256) != 64 {
		return p, fmt.Errorf("%w: verify %s first (homesteadctl updates verify)", ErrNotVerified, req.Component)
	}
	if err := (update.PlatformRelease{Component: req.Component, Version: rec.Version}).Validate(); err != nil {
		return p, fmt.Errorf("%w: %v", ErrNotVerified, err)
	}
	if p.inst, err = a.Releases.Installed(req.Component); err != nil {
		return p, err
	}
	keys, err := a.Releases.PublicKeys()
	if err != nil {
		return p, err
	}
	res, verr := release.Verify(release.Inputs{
		ArtifactPath: req.Artifact, ChecksumsPath: req.Checksums, SignaturePath: req.Signature,
		Trusted: keys, Installed: p.inst, AllowOlder: req.AllowOlder,
	})
	switch {
	case verr == nil:
	case errors.Is(verr, release.ErrNotRegularFile), errors.Is(verr, release.ErrTooLarge):
		return p, fmt.Errorf("%w: artifact, checksums and signature must each be a readable regular file", ErrInput)
	case errors.Is(verr, release.ErrRollback):
		return p, fmt.Errorf("%w: %v; re-run with the explicit older-release override (audited)", ErrOlder, verr)
	default:
		return p, fmt.Errorf("%w: %v", ErrNotVerified, verr)
	}
	if res.SHA256 != rec.SHA256 || res.Version != rec.Version || res.Counter != rec.Counter {
		return p, fmt.Errorf("%w: they differ from the verified record (%s counter %d); verify again", ErrDigest, rec.Version, rec.Counter)
	}
	p.res = res
	installedSHA, err := hashFile(a.binPath(req.Component))
	if err != nil {
		return p, fmt.Errorf("%w: no installed %s at %s", ErrInput, req.Component, a.binPath(req.Component))
	}
	// "from" comes from the component's own record, never another
	// component's: the CLI is not the daemon.
	switch {
	case req.Component == update.ComponentDaemon:
		p.from = a.Running
	case p.inst.Version != "":
		p.from = p.inst.Version
	default:
		p.from = fmt.Sprintf("unrecorded (sha256 %.12s)", installedSHA)
	}
	switch {
	case installedSHA == res.SHA256:
		return p, fmt.Errorf("%w: %s %s (identical binary)", ErrCurrent, req.Component, res.Version)
	case req.Component == update.ComponentDaemon && res.Version == a.Running:
		return p, fmt.Errorf("%w: %s %s", ErrCurrent, req.Component, res.Version)
	case p.inst.Counter > 0 && res.Counter == p.inst.Counter && res.Version == p.inst.Version:
		return p, fmt.Errorf("%w: %s %s", ErrCurrent, req.Component, res.Version)
	}
	if res.Override {
		p.override = true
		p.overrideWhy = fmt.Sprintf("owner allowed older release: installed %s (counter %d), applying %s (counter %d)",
			p.inst.Version, p.inst.Counter, res.Version, res.Counter)
	}
	return p, nil
}

type run struct {
	a      *Applier
	req    Request
	p      plan
	opID   int64
	ready  chan struct{}
	staged stagedFiles
}

type stagedFiles struct{ artifact, checksums, signature string }

// Start validates and begins an apply operation. It returns while the
// operation runs; the operation ends when the apply unit finalizes it.
func (a *Applier) Start(ctx context.Context, req Request) (operation.Operation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, pending, err := a.Markers.Pending(); err != nil {
		return operation.Operation{}, err
	} else if pending {
		return operation.Operation{}, fmt.Errorf("%w: a pending apply is awaiting its apply unit (clear it with `homesteadctl updates platform clear-apply` if the unit died)", ErrBusy)
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
	base := filepath.Base(req.Artifact)
	if base == "checksums.staged" || base == "signature.staged" {
		return operation.Operation{}, fmt.Errorf("%w: reserved artifact file name", ErrInput)
	}
	r := &run{a: a, req: req, p: p, ready: make(chan struct{}), staged: stagedFiles{
		artifact:  filepath.Join(a.StagingDir, base),
		checksums: filepath.Join(a.StagingDir, "checksums.staged"),
		signature: filepath.Join(a.StagingDir, "signature.staged"),
	}}
	steps := []operation.StepDef{
		{Name: StepStage, Run: r.step(StepStage, r.stage)},
		{Name: StepHandoff, Run: r.step(StepHandoff, r.handoff)},
		{Name: StepApply, Run: r.step(StepApply, r.await)},
	}
	payload, _ := json.Marshal(map[string]any{
		"component": req.Component, "from_version": p.from, "from_counter": p.inst.Counter,
		"to_version": p.res.Version, "to_counter": p.res.Counter, "sha256": p.res.SHA256,
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
			req.Component, p.from, p.inst.Counter, p.res.Version, p.res.Counter, op.ID)}
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

// step waits for the operation id and audits a failure.
func (r *run) step(name string, fn func(context.Context) error) operation.StepFunc {
	return func(ctx context.Context) error {
		<-r.ready
		if err := fn(ctx); err != nil {
			r.a.record(audit.Entry{Actor: r.req.Actor, Action: ActionApply, Outcome: audit.OutcomeError,
				Detail: fmt.Sprintf("apply %s %s -> %s failed at %s: %v", r.req.Component, r.p.from, r.p.res.Version, name, err)})
			return err
		}
		return nil
	}
}

// cleanup removes staged files unless the apply unit now owns them.
func (r *run) cleanup() {
	if m, ok, err := r.a.Markers.Pending(); err != nil || (ok && m.OpID == r.opID) {
		return
	}
	cleanDir(r.a.StagingDir)
}

func cleanDir(dir string) {
	if dir == "" {
		return
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
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
	cleanDir(r.a.StagingDir)
	_, info, err := openSource(r.req.Artifact)
	if err != nil {
		return err
	}
	sum, n, err := copyFile(r.req.Artifact, r.staged.artifact, 0o755)
	if err != nil {
		return err
	}
	if sum != r.p.res.SHA256 {
		return fmt.Errorf("%w: copied bytes differ from the verified digest", ErrDigest)
	}
	if st, err := os.Stat(r.staged.artifact); err != nil || st.Size() != n || n != info.Size() {
		return fmt.Errorf("staged copy size mismatch: copied %d bytes of %d", n, info.Size())
	}
	for src, dst := range map[string]string{r.req.Checksums: r.staged.checksums, r.req.Signature: r.staged.signature} {
		if _, _, err := copyFileLimit(src, dst, 0o600, release.MaxSmallFileBytes); err != nil {
			return err
		}
	}
	if err := release.VerifyDigestUnchanged(r.staged.artifact, r.p.res.SHA256); err != nil {
		return fmt.Errorf("%w: staged copy: %v", ErrDigest, err)
	}
	// The staged triple must verify exactly as the originals did.
	keys, err := r.a.Releases.PublicKeys()
	if err != nil {
		return err
	}
	res, verr := release.Verify(release.Inputs{
		ArtifactPath: r.staged.artifact, ChecksumsPath: r.staged.checksums, SignaturePath: r.staged.signature,
		Trusted: keys, Installed: r.p.inst, AllowOlder: r.req.AllowOlder,
	})
	if verr != nil || res.SHA256 != r.p.res.SHA256 || res.Counter != r.p.res.Counter || res.Version != r.p.res.Version {
		return fmt.Errorf("%w: staged files do not verify: %v", ErrDigest, verr)
	}
	return nil
}

// handoff writes the marker (the apply lock and the unit's input), then
// starts the privileged apply unit. A unit that cannot be started
// changed nothing, so the marker is cleared and the operation fails.
func (r *run) handoff(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	marker := Marker{
		OpID: r.opID, Component: r.req.Component, FromVersion: r.p.from, FromCounter: r.p.inst.Counter,
		ToVersion: r.p.res.Version, ToCounter: r.p.res.Counter, SHA256: r.p.res.SHA256, BinaryPath: r.a.binPath(r.req.Component),
		Override: r.p.override, OverrideWhy: r.p.overrideWhy, Actor: r.req.Actor,
		StagedArtifact: r.staged.artifact, StagedChecksums: r.staged.checksums, StagedSignature: r.staged.signature,
		AllowOlder: r.req.AllowOlder, Listen: r.a.Listen, SocketPath: r.a.SocketPath,
	}
	if err := r.a.Markers.Begin(marker); err != nil {
		return err
	}
	if err := r.a.Systemd.Start(ctx, r.a.ApplyUnit); err != nil {
		_ = r.a.Markers.Clear(r.opID)
		cleanDir(r.a.StagingDir)
		return fmt.Errorf("cannot start %s (a pre-ADR-027 install needs `make install` re-run): %w", r.a.ApplyUnit, err)
	}
	return nil
}

// await keeps the operation open while the apply unit owns it. It has
// no timeout of its own: the apply unit's health timeout bounds the
// work, and a daemon-side timeout would mark the operation failed while
// the unit could still finish it. It returns when the unit has finalized
// the record, or when the marker vanished without a verdict.
func (r *run) await(ctx context.Context) error {
	poll := r.a.AwaitPoll
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	for {
		if op, err := r.a.Ops.Get(r.opID); err == nil && operation.Terminal(op.Status) {
			return nil
		}
		if m, ok, err := r.a.Markers.Pending(); err == nil && (!ok || m.OpID != r.opID) {
			// Grace: the unit clears the marker after finalizing the record.
			time.Sleep(poll)
			if op, err := r.a.Ops.Get(r.opID); err == nil && operation.Terminal(op.Status) {
				return nil
			}
			return errors.New("the apply unit finished without finalizing the operation")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// ClearApply is the owner's way out of a stale marker (the apply unit
// died): it marks the operation failed, clears the marker and audits.
// It refuses while the apply unit is running.
func (a *Applier) ClearApply(ctx context.Context, actor string) (Marker, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	m, ok, err := a.Markers.Pending()
	if err != nil {
		return Marker{}, err
	}
	if !ok {
		return Marker{}, ErrNoPending
	}
	active, err := a.Systemd.Active(ctx, a.ApplyUnit)
	if err != nil {
		return Marker{}, err
	}
	if active {
		return Marker{}, fmt.Errorf("%w: %s is still running", ErrBusy, a.ApplyUnit)
	}
	msg := fmt.Sprintf("apply of %s %s cleared by %s: its apply unit did not finish. If the binary was changed, check it against %s",
		m.Component, m.ToVersion, actor, m.PreviousPath)
	advanceOp(a.Ops, m, false, "", msg)
	a.record(audit.Entry{Actor: actor, Action: ActionClear, Outcome: audit.OutcomeOK,
		Detail: fmt.Sprintf("cleared stale apply marker for %s %s -> %s (operation %d)", m.Component, m.FromVersion, m.ToVersion, m.OpID)})
	if err := a.Markers.Clear(m.OpID); err != nil {
		return Marker{}, err
	}
	cleanDir(a.StagingDir)
	return m, nil
}

// advanceOp moves an operation's running steps and the operation itself
// to a terminal state, when the record is still open.
func advanceOp(ops *operation.Store, m Marker, ok bool, result, failMsg string) {
	op, err := ops.Get(m.OpID)
	if err != nil || operation.Terminal(op.Status) {
		return
	}
	for _, step := range op.Steps {
		if step.Status != operation.StatusRunning {
			continue
		}
		if ok {
			_ = ops.TransitionStep(op.ID, step.ID, operation.StatusSucceeded, "")
		} else {
			_ = ops.TransitionStep(op.ID, step.ID, operation.StatusFailed, failMsg)
		}
	}
	if ok {
		_ = ops.Transition(op.ID, operation.StatusSucceeded, result, "")
	} else {
		_ = ops.Transition(op.ID, operation.StatusFailed, "", failMsg)
	}
}

// RestoreCommand is the documented manual restore for a marker: copy
// the preserved previous binary beside the installed one and rename it
// into place (atomic, safe while the binary runs), clear the unit's
// failed state, and restart.
func RestoreCommand(m Marker, unit string) string {
	return fmt.Sprintf("sudo cp -p %s %s.restore && sudo mv %s.restore %s && sudo systemctl reset-failed %s; sudo systemctl restart %s",
		m.PreviousPath, m.BinaryPath, m.BinaryPath, m.BinaryPath, unit, unit)
}
