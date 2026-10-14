package platformapply

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"homestead/internal/audit"
	"homestead/internal/operation"
	"homestead/internal/release"
	"homestead/internal/update"
)

// SelfDigest hashes the executable this process is running from
// (/proc/self/exe follows the inode even after the path was replaced).
func SelfDigest() (string, error) { return hashFile("/proc/self/exe") }

// Finalizer completes an apply from its persisted marker. The next
// daemon start runs it after the listeners are up: it health-checks the
// new binary, finalizes the operation, records the installed release
// (rollback protection engages only now), audits before and after, and
// clears the marker.
type Finalizer struct {
	Releases *release.Store
	Markers  *Store
	Ops      *operation.Store
	Audit    *audit.Service
	// Version is this process's version (version.Version).
	Version string
	Unit    string
	// StagingDir is emptied once an apply is finalized.
	StagingDir string
	// SelfDigest hashes this process's executable; nil uses SelfDigest.
	SelfDigest func() (string, error)
	// Probe proves the listeners answer (a self-probe); nil skips it.
	Probe func(ctx context.Context) error
	// OnFailure, if set, runs after a failed health check has been
	// marked failed and audited, before the marker is cleared. It is
	// the seam for S273 automatic rollback; S265 leaves it nil, which
	// keeps the previous binary in place for manual restore.
	OnFailure func(ctx context.Context, m Marker, cause error)
}

// Run finalizes a pending apply, if any. It reports whether a marker was
// processed and the health-check failure, if any.
func (f *Finalizer) Run(ctx context.Context) (bool, error) {
	m, ok, err := f.Markers.Pending()
	if err != nil || !ok {
		return false, err
	}
	if cause := f.check(ctx, m); cause != nil {
		f.failOp(m, cause)
		f.fail(ctx, m, cause)
		return true, f.failure(m, cause)
	}
	f.succeedOp(m)
	f.commit(m)
	return true, nil
}

func (f *Finalizer) digest() (string, error) {
	if f.SelfDigest != nil {
		return f.SelfDigest()
	}
	return SelfDigest()
}

// check is the post-restart health check: the process reports the new
// version and digest, the database answers, and the listeners respond.
// A component that is not the daemon (the CLI) is checked on disk.
func (f *Finalizer) check(ctx context.Context, m Marker) error {
	if m.Component != update.ComponentDaemon {
		got, err := hashFile(m.BinaryPath)
		if err != nil {
			return fmt.Errorf("installed %s unreadable: %w", m.Component, err)
		}
		if got != m.SHA256 {
			return fmt.Errorf("installed %s digest %s does not match the verified release", m.Component, got)
		}
		return nil
	}
	if f.Version != m.ToVersion {
		return fmt.Errorf("daemon reports version %q, expected %q (a release binary must be built with its version, -X homestead/internal/version.Version)", f.Version, m.ToVersion)
	}
	got, err := f.digest()
	if err != nil {
		return fmt.Errorf("cannot hash the running binary: %w", err)
	}
	if got != m.SHA256 {
		return fmt.Errorf("running binary digest %s does not match the verified release %s", got, m.SHA256)
	}
	if _, err := f.Releases.Installed(m.Component); err != nil {
		return fmt.Errorf("database check: %w", err)
	}
	if f.Probe != nil {
		if err := f.Probe(ctx); err != nil {
			return fmt.Errorf("self-probe: %w", err)
		}
	}
	return nil
}

func (f *Finalizer) failure(m Marker, cause error) error {
	return fmt.Errorf("post-restart health check failed for %s %s: %v; the previous binary (%s %s, sha256 %s) is kept at %s; restore manually with: %s",
		m.Component, m.ToVersion, cause, m.Component, m.FromVersion, m.PreviousSHA, m.PreviousPath, RestoreCommand(m, f.Unit))
}

// advance moves the operation's remaining steps to a terminal state and
// the operation itself, when the record is still open. Records already
// terminal (a late finalization after the restart wait expired) stay.
func (f *Finalizer) advance(m Marker, ok bool, result, failMsg string) {
	op, err := f.Ops.Get(m.OpID)
	if err != nil || operation.Terminal(op.Status) {
		return
	}
	for _, step := range op.Steps {
		switch {
		case step.Name == StepRestart && step.Status == operation.StatusRunning:
			_ = f.Ops.TransitionStep(op.ID, step.ID, operation.StatusSucceeded, "")
		case step.Name == StepFinalize:
			if step.Status == operation.StatusPending {
				_ = f.Ops.TransitionStep(op.ID, step.ID, operation.StatusRunning, "")
			}
			if ok {
				_ = f.Ops.TransitionStep(op.ID, step.ID, operation.StatusSucceeded, "")
			} else {
				_ = f.Ops.TransitionStep(op.ID, step.ID, operation.StatusFailed, failMsg)
			}
		}
	}
	if ok {
		_ = f.Ops.Transition(op.ID, operation.StatusSucceeded, result, "")
	} else {
		_ = f.Ops.Transition(op.ID, operation.StatusFailed, "", failMsg)
	}
}

func (f *Finalizer) succeedOp(m Marker) {
	result, _ := json.Marshal(map[string]any{
		"component": m.Component, "from_version": m.FromVersion, "to_version": m.ToVersion,
		"from_counter": m.FromCounter, "to_counter": m.ToCounter,
	})
	f.advance(m, true, string(result), "")
}

func (f *Finalizer) failOp(m Marker, cause error) {
	f.advance(m, false, "", f.failure(m, cause).Error())
}

// commit records the installed release (rollback protection), audits
// before and after versions, and clears the marker.
func (f *Finalizer) commit(m Marker) {
	if err := f.Releases.RecordInstalled(m.Component, m.ToVersion, m.ToCounter); err != nil {
		// Keep the marker: the next start retries the commit.
		_, _ = f.Audit.Record(audit.Entry{Actor: "system", Action: ActionFinalize, Outcome: audit.OutcomeError,
			Detail: fmt.Sprintf("record installed %s %s: %v", m.Component, m.ToVersion, err)})
		return
	}
	entry := audit.Entry{Actor: m.Actor, Action: ActionFinalize, Outcome: audit.OutcomeOK,
		Detail: fmt.Sprintf("applied %s: %s (counter %d) -> %s (counter %d), operation %d healthy",
			m.Component, m.FromVersion, m.FromCounter, m.ToVersion, m.ToCounter, m.OpID)}
	if m.Override {
		entry.Override, entry.OverrideReason = true, m.OverrideWhy
	}
	if _, err := f.Audit.Record(entry); err != nil {
		return
	}
	_ = f.Markers.Clear(m.OpID)
	f.cleanStaging()
}

// fail audits the failed health check, runs the rollback seam, clears
// the marker. The installed record is untouched.
func (f *Finalizer) fail(ctx context.Context, m Marker, cause error) {
	entry := audit.Entry{Actor: m.Actor, Action: ActionFinalize, Outcome: audit.OutcomeError,
		Detail: f.failure(m, cause).Error()}
	if m.Override {
		entry.Override, entry.OverrideReason = true, m.OverrideWhy
	}
	_, _ = f.Audit.Record(entry)
	if f.OnFailure != nil {
		f.OnFailure(ctx, m, cause)
	}
	_ = f.Markers.Clear(m.OpID)
	f.cleanStaging()
}

func (f *Finalizer) cleanStaging() {
	if f.StagingDir == "" {
		return
	}
	entries, _ := os.ReadDir(f.StagingDir)
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(f.StagingDir, e.Name()))
	}
}
