package platformapply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"homestead/internal/audit"
	"homestead/internal/operation"
	"homestead/internal/release"
	"homestead/internal/update"
)

// ErrNoMarker is returned when the apply unit starts with nothing to do.
var ErrNoMarker = errors.New("platformapply: no pending apply marker")

// Runner is the apply unit side (ADR-027): it runs as root in
// homestead-apply.service, takes no network input, and trusts neither
// the daemon's verdict nor the staging directory's contents without
// verifying them again.
type Runner struct {
	Releases *release.Store
	Markers  *Store
	Ops      *operation.Store
	Audit    *audit.Service
	Systemd  Systemd
	// Unit is the daemon's unit.
	Unit       string
	StagingDir string
	// HealthTimeout bounds the wait for the restarted daemon; a new
	// binary that never becomes healthy fails the operation instead of
	// leaving it running.
	HealthTimeout  time.Duration
	HealthInterval time.Duration
	// Probe makes one attempt to prove the listeners answer and report the
	// target version; nil skips it.
	Probe func(ctx context.Context, m Marker) error
	// ExecDigest hashes a process's executable; nil reads /proc/<pid>/exe.
	ExecDigest func(pid int) (string, error)
	// RecordInstalled records the installed release; nil uses the store.
	RecordInstalled func(component, version string, counter int64) error
	// RetryWait paces RecordInstalled retries.
	RetryWait time.Duration
}

type unitRun struct {
	r       *Runner
	m       Marker
	swapped bool
}

// Run applies the pending marker: verify, preserve, replace, restart,
// health check, finalize. The returned error is the failure message the
// operation record carries; nil means the apply succeeded.
func (r *Runner) Run(ctx context.Context) error {
	m, ok, err := r.Markers.Pending()
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoMarker
	}
	u := &unitRun{r: r, m: m}
	op, err := r.Ops.Get(m.OpID)
	if err != nil || operation.Terminal(op.Status) {
		// The owner cancelled or cleared the operation: change nothing.
		return u.fail(ctx, errors.New("the operation is no longer open; nothing was changed"))
	}
	if m.Kind == KindRollback {
		return u.runRollback(ctx)
	}
	if err := u.step(ctx, "verify-staged", u.verify); err != nil {
		return u.fail(ctx, err)
	}
	if err := u.step(ctx, "preserve-previous", u.preserve); err != nil {
		return u.fail(ctx, err)
	}
	if err := u.step(ctx, "replace", u.replace); err != nil {
		return u.fail(ctx, err)
	}
	if m.Component == update.ComponentDaemon {
		if err := u.step(ctx, "restart", u.restart); err != nil {
			return u.fail(ctx, err)
		}
	}
	if err := u.step(ctx, "health-check", func(ctx context.Context) error { return u.healthy(ctx, u.m) }); err != nil {
		return u.rollbackAfter(ctx, err)
	}
	return u.succeed(ctx)
}

// step runs fn as a recorded sub-step of the operation.
func (u *unitRun) step(ctx context.Context, name string, fn func(context.Context) error) error {
	st, err := u.r.Ops.AddStep(u.m.OpID, name)
	if err == nil {
		_ = u.r.Ops.TransitionStep(u.m.OpID, st.ID, operation.StatusRunning, "")
	}
	ferr := fn(ctx)
	if err == nil {
		if ferr != nil {
			_ = u.r.Ops.TransitionStep(u.m.OpID, st.ID, operation.StatusFailed, ferr.Error())
		} else {
			_ = u.r.Ops.TransitionStep(u.m.OpID, st.ID, operation.StatusSucceeded, "")
		}
	}
	return ferr
}

// verify independently re-verifies the staged files from scratch: the
// minisign signature and checksums against the trusted keys, the digest
// of the staged artifact, and the counter against the installed baseline.
// The database "verified" record is not consulted.
func (u *unitRun) verify(context.Context) error {
	m := u.m
	keys, err := u.r.Releases.PublicKeys()
	if err != nil {
		return err
	}
	inst, err := u.r.Releases.Installed(m.Component)
	if err != nil {
		return err
	}
	res, err := release.Verify(release.Inputs{
		ArtifactPath: m.StagedArtifact, ChecksumsPath: m.StagedChecksums, SignaturePath: m.StagedSignature,
		Trusted: keys, Installed: inst, AllowOlder: m.AllowOlder,
	})
	if err != nil {
		return fmt.Errorf("staged release does not verify: %w", err)
	}
	if res.SHA256 != m.SHA256 || res.Version != m.ToVersion || res.Counter != m.ToCounter {
		return fmt.Errorf("staged release is %s counter %d (sha256 %.12s), not the %s counter %d the daemon staged",
			res.Version, res.Counter, res.SHA256, m.ToVersion, m.ToCounter)
	}
	return nil
}

func (u *unitRun) preserve(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m := u.m
	prev := m.BinaryPath + ".previous"
	tmp := prev + ".tmp"
	sum, _, err := copyFile(m.BinaryPath, tmp, 0o755)
	if err != nil {
		return fmt.Errorf("preserve previous %s: %w", m.BinaryPath, err)
	}
	if err := os.Rename(tmp, prev); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := syncDir(filepath.Dir(m.BinaryPath)); err != nil {
		return err
	}
	inst, _ := u.r.Releases.Installed(m.Component)
	if err := u.r.Markers.SavePrevious(Previous{Component: m.Component, Version: m.FromVersion, Counter: inst.Counter, SHA256: sum, Path: prev}); err != nil {
		return err
	}
	u.m.PreviousPath, u.m.PreviousSHA = prev, sum
	return u.r.Markers.SetPrevious(m.OpID, prev, sum)
}

func (u *unitRun) replace(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m := u.m
	// Immediately before replacement: the staged bytes are still the
	// verified bytes.
	if err := release.VerifyDigestUnchanged(m.StagedArtifact, m.SHA256); err != nil {
		return fmt.Errorf("%w: refusing to replace: %v", ErrDigest, err)
	}
	tmp := m.BinaryPath + ".new"
	sum, _, err := copyFile(m.StagedArtifact, tmp, 0o755)
	if err != nil {
		return fmt.Errorf("install %s: %w", m.BinaryPath, err)
	}
	if sum != m.SHA256 {
		_ = os.Remove(tmp)
		return fmt.Errorf("%w: installed copy differs from the verified digest", ErrDigest)
	}
	if err := os.Rename(tmp, m.BinaryPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", m.BinaryPath, err)
	}
	u.swapped = true
	// The rename is done and visible; a failed directory fsync only
	// weakens durability, so the swap proceeds and is reported.
	if err := syncDir(filepath.Dir(m.BinaryPath)); err != nil {
		u.r.record(audit.Entry{Actor: m.Actor, Action: ActionFinalize, Outcome: audit.OutcomeError,
			Detail: fmt.Sprintf("replaced %s but fsync of its directory failed (swap kept): %v", m.BinaryPath, err)})
	}
	return nil
}

func (u *unitRun) restart(ctx context.Context) error {
	err := u.r.Systemd.Restart(ctx, u.r.Unit)
	if err == nil {
		return nil
	}
	if u.m.Kind == KindRollback {
		return fmt.Errorf("restart after restoring the previous binary failed: %w", err)
	}
	// A restart that could not even run never started the new binary;
	// put the old one back so the file matches the running process.
	if rerr := RestorePrevious(u.m.BinaryPath, u.m.PreviousPath, u.m.PreviousSHA); rerr != nil {
		return fmt.Errorf("restart failed (%v) and the replacement could not be undone (%v)", err, rerr)
	}
	u.swapped = false
	// Do not leave the daemon down with the old binary back on disk: try
	// to start it again (best effort; the operation is failed either way).
	if rerr := u.r.Systemd.Restart(ctx, u.r.Unit); rerr != nil {
		return fmt.Errorf("restart failed, replacement undone, daemon not restarted (%v): %w", rerr, err)
	}
	return fmt.Errorf("restart failed, replacement undone, daemon restarted on the previous binary: %w", err)
}

func (u *unitRun) execDigest(pid int) (string, error) {
	if u.r.ExecDigest != nil {
		return u.r.ExecDigest(pid)
	}
	return hashFile(fmt.Sprintf("/proc/%d/exe", pid))
}

// check is one health attempt. For the daemon: the main process runs the
// verified digest, the database opens, and the listeners answer with the
// target version (m names the target: SHA256 and ToVersion). The CLI is
// checked on disk by digest.
func (u *unitRun) check(ctx context.Context, m Marker) error {
	if m.Component != update.ComponentDaemon {
		got, err := hashFile(m.BinaryPath)
		if err != nil {
			return fmt.Errorf("installed %s unreadable: %w", m.Component, err)
		}
		if got != m.SHA256 {
			return fmt.Errorf("installed %s digest %.12s does not match the verified release", m.Component, got)
		}
		return nil
	}
	pid, err := u.r.Systemd.MainPID(ctx, u.r.Unit)
	if err != nil {
		return fmt.Errorf("daemon main pid: %w", err)
	}
	if pid <= 0 {
		return errors.New("daemon is not running")
	}
	got, err := u.execDigest(pid)
	if err != nil {
		return fmt.Errorf("cannot hash the daemon binary: %w", err)
	}
	if got != m.SHA256 {
		return fmt.Errorf("daemon runs digest %.12s, not the verified release", got)
	}
	if _, err := u.r.Releases.Installed(m.Component); err != nil {
		return fmt.Errorf("database check: %w", err)
	}
	if u.r.Probe != nil {
		if err := u.r.Probe(ctx, m); err != nil {
			return fmt.Errorf("listener probe: %w", err)
		}
	}
	return nil
}

// healthy polls check until it passes or HealthTimeout elapses.
func (u *unitRun) healthy(ctx context.Context, m Marker) error {
	timeout, interval := u.r.HealthTimeout, u.r.HealthInterval
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	if interval <= 0 {
		interval = time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		err := u.check(ctx, m)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("%s was not healthy within %s: %w", m.Component, timeout, err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(interval):
		}
	}
}

func (r *Runner) record(entry audit.Entry) { _, _ = r.Audit.Record(entry) }

func (u *unitRun) failureMessage(cause error) string {
	m := u.m
	if m.Kind == KindRollback {
		if !u.swapped {
			return fmt.Sprintf("rollback of %s to %s failed: %v; the installed binary was not changed", m.Component, m.ToVersion, cause)
		}
		return fmt.Sprintf("rollback failed: %s to %s: %v; the previous binary is restored at %s and the replaced binary is kept at %s; "+
			"if the daemon is down, restart it with: %s",
			m.Component, m.ToVersion, cause, m.BinaryPath, m.BinaryPath+rolledBackSuffix, RestoreCommand(m, u.r.Unit))
	}
	if !u.swapped {
		return fmt.Sprintf("apply of %s %s failed: %v; the installed binary was not changed", m.Component, m.ToVersion, cause)
	}
	return fmt.Sprintf("apply of %s %s failed: %v; the previous binary (%s %s, sha256 %s) is kept at %s; restore manually with: %s",
		m.Component, m.ToVersion, cause, m.Component, m.FromVersion, m.PreviousSHA, m.PreviousPath, RestoreCommand(m, u.r.Unit))
}

// fail finalizes a failed apply from outside the daemon: the operation
// failed, audited loudly, the marker always cleared so it
// can never block future applies, and the previous binary left alone.
func (u *unitRun) fail(ctx context.Context, cause error) error {
	return u.finish(u.failureMessage(cause))
}

// finish marks the operation failed with msg, audits it loudly, and
// always clears the marker so it can never block future applies.
func (u *unitRun) finish(msg string) error {
	advanceOp(u.r.Ops, u.m, false, "", msg)
	entry := audit.Entry{Actor: u.m.Actor, Action: ActionFinalize, Outcome: audit.OutcomeError, Detail: msg}
	if u.m.Override {
		entry.Override, entry.OverrideReason = true, u.m.OverrideWhy
	}
	u.r.record(entry)
	_ = u.r.Markers.Clear(u.m.OpID)
	cleanDir(u.r.StagingDir)
	return errors.New(msg)
}

func (u *unitRun) record() error {
	if u.r.RecordInstalled != nil {
		return u.r.RecordInstalled(u.m.Component, u.m.ToVersion, u.m.ToCounter)
	}
	return u.r.Releases.RecordInstalled(u.m.Component, u.m.ToVersion, u.m.ToCounter)
}

// succeed records the installed release (rollback protection engages
// only now), finalizes the operation, audits before and after, and
// clears the marker. A baseline that cannot be recorded fails the
// operation: the apply did not complete truthfully.
func (u *unitRun) succeed(ctx context.Context) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = u.record(); err == nil {
			break
		}
		time.Sleep(u.r.RetryWait)
	}
	if err != nil {
		return u.fail(ctx, fmt.Errorf("the binary is installed and healthy but the installed release could not be recorded: %w", err))
	}
	m := u.m
	result, _ := json.Marshal(map[string]any{
		"component": m.Component, "from_version": m.FromVersion, "to_version": m.ToVersion,
		"from_counter": m.FromCounter, "to_counter": m.ToCounter, "rollback": m.Kind == KindRollback,
	})
	advanceOp(u.r.Ops, m, true, string(result), "")
	verb := "applied"
	if m.Kind == KindRollback {
		verb = "rolled back"
	}
	entry := audit.Entry{Actor: m.Actor, Action: ActionFinalize, Outcome: audit.OutcomeOK,
		Detail: fmt.Sprintf("%s %s: %s (counter %d) -> %s (counter %d), operation %d healthy",
			verb, m.Component, m.FromVersion, m.FromCounter, m.ToVersion, m.ToCounter, m.OpID)}
	if m.Override {
		entry.Override, entry.OverrideReason = true, m.OverrideWhy
	}
	u.r.record(entry)
	_ = u.r.Markers.Clear(m.OpID)
	cleanDir(u.r.StagingDir)
	return nil
}
