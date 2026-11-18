package platformapply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"homestead/internal/audit"
	"homestead/internal/operation"
	"homestead/internal/release"
	"homestead/internal/update"
)

// Rollback (S273, ADR-027, ADR-025). Both the automatic rollback after a
// failed post-restart health check and the owner's manual rollback run in
// the privileged apply unit: it restores the previous binary recorded by
// the last apply, verifying its digest against the record, restarts the
// daemon, and waits for it to be healthy.

// rolledBackSuffix prefixes the timestamped copies kept of the binary a
// rollback replaced (`<bin>.rolledback-<stamp>`); the last keptCopies are
// retained, older ones removed.
const (
	rolledBackSuffix = ".rolledback"
	keptCopies       = 2
)

// Test seams: the clock for copy stamps and the free space of a directory.
var (
	nowFunc   = time.Now
	freeSpace = func(dir string) (uint64, error) {
		var st syscall.Statfs_t
		if err := syscall.Statfs(dir, &st); err != nil {
			return 0, err
		}
		return st.Bavail * uint64(st.Bsize), nil
	}
)

// Rollback refusals, mapped to HTTP statuses by the route.
var (
	ErrNoPrevious = errors.New("platformapply: no previous binary is recorded for this component")
	ErrPrevious   = errors.New("platformapply: the recorded previous binary is unusable")
	// ErrCounterUnknown refuses a rollback whose previous release has no
	// recorded counter: restoring it would drop anti-rollback protection.
	ErrCounterUnknown = errors.New("platformapply: the previous release has no recorded counter")
	// ErrCounterConflict refuses a rollback to the installed counter under
	// a different version (ADR-025: one counter, one version).
	ErrCounterConflict = errors.New("platformapply: the previous release reuses the installed counter under a different version")
)

// checkRollbackCounter applies the ADR-025 counter policy to a rollback
// from the installed baseline to the previous release. The anti-rollback
// baseline must never drop to an unknown counter, so even the owner
// override cannot roll back to a release without one.
func checkRollbackCounter(component string, prev Previous, inst release.Installed) error {
	if prev.Counter <= 0 {
		return fmt.Errorf("%w: rolling back %s to %s would erase the anti-rollback baseline, so it is refused even with --allow-older; "+
			"to return to that release, reinstall it through a verified apply (`homesteadctl updates verify` then `apply-platform` with its signed checksums file; --allow-older if its counter is older)",
			ErrCounterUnknown, component, prev.Version)
	}
	if inst.Counter > 0 && prev.Counter == inst.Counter && prev.Version != inst.Version {
		return fmt.Errorf("%w: counter %d is installed as %s but the previous binary is %s",
			ErrCounterConflict, inst.Counter, inst.Version, prev.Version)
	}
	return nil
}

// RollbackRequest is a manual rollback. AllowOlder is the explicit,
// audited owner override for moving to an older counter (ADR-025).
type RollbackRequest struct {
	Component  string
	Actor      string
	AllowOlder bool
}

// checkPrevious verifies that the recorded previous binary may be
// restored: it is the fixed sibling of the installed binary, a regular
// file, and its bytes match the recorded digest.
func checkPrevious(binPath, previousPath, wantSHA string) error {
	if previousPath != binPath+".previous" {
		return fmt.Errorf("%w: %s is not %s.previous", ErrPrevious, previousPath, binPath)
	}
	if _, _, err := openSource(previousPath); err != nil {
		return fmt.Errorf("%w: %v", ErrPrevious, err)
	}
	got, err := hashFile(previousPath)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPrevious, err)
	}
	if wantSHA == "" || got != wantSHA {
		return fmt.Errorf("%w: %s does not match its recorded digest", ErrDigest, previousPath)
	}
	return nil
}

// Rollback starts a manual rollback of component to its recorded previous
// binary. Moving to an older counter than the installed baseline needs
// AllowOlder; it is audited with both versions.
func (a *Applier) Rollback(ctx context.Context, req RollbackRequest) (operation.Operation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !update.IsPlatformComponent(req.Component) {
		return operation.Operation{}, fmt.Errorf("%w: unknown platform component %q", ErrInput, req.Component)
	}
	if req.Component == update.ComponentDeb {
		return operation.Operation{}, errors.New("platformapply: Debian package updates are forward-only; no automatic or blind package downgrade is supported")
	}
	if req.Actor == "" {
		return operation.Operation{}, fmt.Errorf("%w: actor is required", ErrInput)
	}
	if _, pending, err := a.Markers.Pending(); err != nil {
		return operation.Operation{}, err
	} else if pending {
		return operation.Operation{}, fmt.Errorf("%w: a pending apply is awaiting its apply unit", ErrBusy)
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
	prev, ok, err := a.Markers.Previous(req.Component)
	if err != nil {
		return operation.Operation{}, err
	}
	if !ok {
		return operation.Operation{}, fmt.Errorf("%w: apply a platform update first", ErrNoPrevious)
	}
	bin := a.binPath(req.Component)
	if err := checkPrevious(bin, prev.Path, prev.SHA256); err != nil {
		return operation.Operation{}, err
	}
	installedSHA, err := hashFile(bin)
	if err != nil {
		return operation.Operation{}, fmt.Errorf("%w: no installed %s at %s", ErrInput, req.Component, bin)
	}
	if installedSHA == prev.SHA256 {
		return operation.Operation{}, fmt.Errorf("%w: %s already runs the previous binary (%s)", ErrCurrent, req.Component, prev.Version)
	}
	inst, err := a.Releases.Installed(req.Component)
	if err != nil {
		return operation.Operation{}, err
	}
	if err := checkRollbackCounter(req.Component, prev, inst); err != nil {
		return operation.Operation{}, err
	}
	from := inst.Version
	switch {
	case req.Component == update.ComponentDaemon:
		from = a.Running
	case from == "":
		from = fmt.Sprintf("unrecorded (sha256 %.12s)", installedSHA)
	}
	older := inst.Counter > 0 && prev.Counter < inst.Counter
	var why string
	if older {
		why = fmt.Sprintf("owner allowed rollback to an older release: installed %s (counter %d), rolling back to %s (counter %d)",
			from, inst.Counter, prev.Version, prev.Counter)
		if !req.AllowOlder {
			return operation.Operation{}, fmt.Errorf("%w: %s (counter %d) is older than the installed %s (counter %d); re-run with the explicit older-release override (audited)",
				ErrOlder, prev.Version, prev.Counter, from, inst.Counter)
		}
	}
	r := &run{a: a, req: Request{Component: req.Component, Actor: req.Actor, AllowOlder: req.AllowOlder}, ready: make(chan struct{})}
	r.p.from = from
	r.p.res.Version, r.p.res.Counter = prev.Version, prev.Counter
	r.p.override, r.p.overrideWhy = older, why
	steps := []operation.StepDef{
		{Name: StepHandoff, Run: r.step(StepHandoff, func(ctx context.Context) error { return r.handoffRollback(ctx, prev, inst.Counter) })},
		{Name: StepApply, Run: r.step(StepApply, r.await)},
	}
	payload, _ := json.Marshal(map[string]any{
		"component": req.Component, "rollback": true, "from_version": from, "from_counter": inst.Counter,
		"to_version": prev.Version, "to_counter": prev.Counter, "sha256": prev.SHA256,
		"allow_older": req.AllowOlder, "override": older,
	})
	op, err := a.Runner.Start(ctx, operation.Definition{Type: OperationType, Steps: steps}, string(payload))
	if err != nil {
		return operation.Operation{}, err
	}
	r.opID = op.ID
	close(r.ready)
	entry := audit.Entry{Actor: req.Actor, Action: ActionRollback, Plan: string(payload), Outcome: audit.OutcomeOK,
		Detail: fmt.Sprintf("rollback %s started: %s (counter %d) -> %s (counter %d), operation %d",
			req.Component, from, inst.Counter, prev.Version, prev.Counter, op.ID)}
	if older {
		entry.Override, entry.OverrideReason = true, why
		entry.Detail += "; " + why
	}
	a.record(entry)
	return op, nil
}

func (r *run) handoffRollback(ctx context.Context, prev Previous, fromCounter int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	marker := Marker{
		Kind: KindRollback, OpID: r.opID, Component: r.req.Component, FromVersion: r.p.from, FromCounter: fromCounter,
		ToVersion: prev.Version, ToCounter: prev.Counter, SHA256: prev.SHA256, BinaryPath: r.a.binPath(r.req.Component),
		PreviousPath: prev.Path, PreviousSHA: prev.SHA256, Override: r.p.override, OverrideWhy: r.p.overrideWhy,
		Actor: r.req.Actor, AllowOlder: r.req.AllowOlder, Listen: r.a.Listen, SocketPath: r.a.SocketPath,
	}
	if err := r.a.Markers.Begin(marker); err != nil {
		return err
	}
	if err := r.a.Systemd.Start(ctx, r.a.ApplyUnit); err != nil {
		_ = r.a.Markers.Clear(r.opID)
		return fmt.Errorf("cannot start %s (a pre-ADR-027 install needs `make install` re-run): %w", r.a.ApplyUnit, err)
	}
	return nil
}

// keepReplaced copies the binary about to be replaced beside it as
// `<bin>.rolledback-<stamp>` so a rollback does not destroy the binary it
// moves away from, retaining the last keptCopies. It checks free space
// first and reports the copy's path or why none was kept; a failure does
// not stop the restore.
func keepReplaced(binPath string) (string, error) {
	info, err := os.Stat(binPath)
	if err != nil {
		return "", err
	}
	free, err := freeSpace(filepath.Dir(binPath))
	if err != nil {
		return "", fmt.Errorf("cannot check free space: %w", err)
	}
	if free < uint64(info.Size())+1<<20 {
		return "", fmt.Errorf("not enough free space beside %s (%d bytes free, %d needed)", binPath, free, info.Size()+1<<20)
	}
	dst := binPath + rolledBackSuffix + "-" + nowFunc().UTC().Format("20060102T150405.000000Z")
	if _, _, err := copyFile(binPath, dst, 0o755); err != nil {
		return "", err
	}
	pruneKept(binPath)
	return dst, nil
}

// pruneKept removes all but the newest keptCopies rolled-back copies.
func pruneKept(binPath string) {
	matches, _ := filepath.Glob(binPath + rolledBackSuffix + "-*")
	sort.Strings(matches)
	for len(matches) > keptCopies {
		_ = os.Remove(matches[0])
		matches = matches[1:]
	}
}

// keepReplaced records the copy on the run so failure messages can say
// truthfully whether the replaced binary was kept.
func (u *unitRun) keepReplaced() {
	u.keptPath, u.keepErr = keepReplaced(u.m.BinaryPath)
}

// replacedNote says where the replaced binary is, or that it was not kept.
func (u *unitRun) replacedNote() string {
	switch {
	case u.keptPath != "":
		return "a copy of the replaced binary is kept at " + u.keptPath
	case u.keepErr != nil:
		return fmt.Sprintf("no copy of the replaced binary was kept (%v)", u.keepErr)
	}
	return "the replaced binary was not copied"
}

// restoreAndRestart puts the previous binary back, restarts the daemon
// (the CLI needs no restart) and waits for the restored binary to be
// healthy. target names the restored binary (SHA256, ToVersion).
func (u *unitRun) restoreAndRestart(ctx context.Context, target Marker) error {
	m := u.m
	if err := checkPrevious(m.BinaryPath, m.PreviousPath, m.PreviousSHA); err != nil {
		return err
	}
	u.keepReplaced()
	if err := RestorePrevious(m.BinaryPath, m.PreviousPath, m.PreviousSHA); err != nil {
		return err
	}
	u.restored = true
	if m.Component == update.ComponentDaemon {
		if err := u.r.Systemd.Restart(ctx, u.r.Unit); err != nil {
			return fmt.Errorf("restart on the previous binary failed: %w", err)
		}
	}
	return u.healthy(ctx, target)
}

// rollbackAfter is the automatic rollback: the new binary failed its
// health check, so the previous one is restored inside the same apply
// unit run. The operation ends failed either way; the installed release
// record was never moved, so the baseline stays the previous release.
func (u *unitRun) rollbackAfter(ctx context.Context, cause error) error {
	m := u.m
	target := m
	target.SHA256, target.ToVersion, target.ToCounter = m.PreviousSHA, m.FromVersion, m.FromCounter
	// A stop signal must not abandon a half-finished rollback.
	rctx := context.WithoutCancel(ctx)
	rerr := u.step(rctx, "rollback", func(ctx context.Context) error { return u.restoreAndRestart(ctx, target) })
	if rerr != nil {
		if !u.restored {
			return u.finish(fmt.Sprintf("rollback failed: apply of %s %s failed (%v) and the previous binary could not be restored: %v; "+
				"the daemon is still on the failed binary at %s and nothing was changed; %s. Manual recovery: put a known-good %s %s binary (sha256 %s) "+
				"in place of the missing or corrupt %s with: sudo cp -p <good binary> %s.restore && sudo mv %s.restore %s && sudo systemctl reset-failed %s; sudo systemctl restart %s",
				m.Component, m.ToVersion, cause, rerr, m.BinaryPath, u.replacedNote(), m.Component, m.FromVersion, m.PreviousSHA, m.PreviousPath,
				m.BinaryPath, m.BinaryPath, m.BinaryPath, u.r.Unit, u.r.Unit))
		}
		return u.finish(fmt.Sprintf("rollback failed: apply of %s %s failed (%v) and the rollback to %s did not complete: %v; "+
			"the previous binary is restored at %s (sha256 %s) but the daemon is not confirmed healthy; %s; restart or restore manually with: %s",
			m.Component, m.ToVersion, cause, m.FromVersion, rerr, m.BinaryPath, m.PreviousSHA, u.replacedNote(), RestoreCommand(m, u.r.Unit)))
	}
	restarted := ""
	if m.Component == update.ComponentDaemon {
		restarted = "; the daemon restarted on it and is healthy"
	}
	return u.finish(fmt.Sprintf("failed (rolled back): apply of %s %s (counter %d) failed: %v; rolled back to %s (counter %d, sha256 %.12s)%s; "+
		"the installed release record still names %s; %s",
		m.Component, m.ToVersion, m.ToCounter, cause, m.FromVersion, m.FromCounter, m.PreviousSHA, restarted,
		m.FromVersion, u.replacedNote()))
}

// runRollback is the apply unit's manual rollback: verify the recorded
// previous binary and the counter policy, restore, restart, health check,
// and record the previous release as the installed baseline.
func (u *unitRun) runRollback(ctx context.Context) error {
	m := u.m
	if err := u.step(ctx, "verify-previous", u.verifyPrevious); err != nil {
		return u.fail(ctx, err)
	}
	if err := u.step(ctx, "replace", func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		u.keepReplaced()
		if err := RestorePrevious(m.BinaryPath, m.PreviousPath, m.PreviousSHA); err != nil {
			return err
		}
		u.swapped = true
		return nil
	}); err != nil {
		return u.fail(ctx, err)
	}
	if m.Component == update.ComponentDaemon {
		if err := u.step(ctx, "restart", u.restart); err != nil {
			return u.fail(ctx, err)
		}
	}
	if err := u.step(ctx, "health-check", func(ctx context.Context) error { return u.healthy(ctx, m) }); err != nil {
		return u.fail(ctx, err)
	}
	return u.succeed(ctx)
}

// verifyPrevious independently re-checks the rollback in the apply unit:
// the store's previous record matches the marker, the file digest matches
// the record, and an older counter carries the owner's override.
func (u *unitRun) verifyPrevious(context.Context) error {
	m := u.m
	rec, ok, err := u.r.Markers.Previous(m.Component)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoPrevious
	}
	if rec.SHA256 != m.PreviousSHA || rec.Version != m.ToVersion || rec.Counter != m.ToCounter {
		return fmt.Errorf("%w: the previous record changed after the rollback was requested", ErrPrevious)
	}
	if err := checkPrevious(m.BinaryPath, rec.Path, rec.SHA256); err != nil {
		return err
	}
	if rec.Path != m.PreviousPath {
		return fmt.Errorf("%w: marker and record disagree about the previous binary", ErrPrevious)
	}
	inst, err := u.r.Releases.Installed(m.Component)
	if err != nil {
		return err
	}
	if err := checkRollbackCounter(m.Component, rec, inst); err != nil {
		return err
	}
	if inst.Counter > 0 && m.ToCounter < inst.Counter && !m.AllowOlder {
		return fmt.Errorf("%w: counter %d is older than the installed %d and the owner did not allow it", ErrOlder, m.ToCounter, inst.Counter)
	}
	if _, err := os.Stat(filepath.Dir(m.BinaryPath)); err != nil {
		return err
	}
	return nil
}
