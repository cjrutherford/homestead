package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"homestead/internal/audit"
	"homestead/internal/operation"
	"homestead/internal/store"
)

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "homestead.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func quickJob(id string, enabled bool, runs *atomic.Int32) Job {
	return Job{
		ID: id, Interval: time.Hour, Enabled: enabled, Since: t0,
		Build: func() (operation.Definition, error) {
			return operation.Definition{Type: "test.job", Steps: []operation.StepDef{{
				Name: "run", Run: func(ctx context.Context) error { runs.Add(1); return nil },
			}}}, nil
		},
	}
}

func newSched(st *store.Store, clock *fakeClock, jobs ...Job) *Scheduler {
	s := New(st, WithClock(clock.Now))
	s.Register("test", SourceFunc(func() ([]Job, error) { return jobs, nil }))
	return s
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func auditActions(t *testing.T, st *store.Store, action string) []audit.Entry {
	t.Helper()
	all, err := audit.New(st).List(100)
	if err != nil {
		t.Fatal(err)
	}
	var out []audit.Entry
	for _, e := range all {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

func TestDueFiresThroughOperationRunner(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0}
	var runs atomic.Int32
	s := newSched(st, clock, quickJob("a", true, &runs))

	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runs.Load() != 0 {
		t.Fatal("job ran before its window")
	}
	clock.Advance(time.Hour)
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return runs.Load() == 1 })

	ops, err := operation.New(st).List(0)
	if err != nil || len(ops) != 1 || ops[0].Type != "test.job" || !strings.Contains(ops[0].Payload, `"job":"a"`) {
		t.Fatalf("ops = %+v, err %v", ops, err)
	}
	if got := auditActions(t, st, "scheduler.run"); len(got) != 1 || got[0].Actor != Actor || got[0].Outcome != audit.OutcomeOK {
		t.Fatalf("audit = %+v", got)
	}
	// Same instant again: window consumed, nothing more.
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(auditActions(t, st, "scheduler.run")); n != 1 {
		t.Fatalf("run audits = %d", n)
	}
	status, err := s.Status()
	if err != nil || len(status) != 1 {
		t.Fatalf("status = %+v, %v", status, err)
	}
	if !status[0].LastRunAt.Equal(t0.Add(time.Hour)) || !status[0].NextDue.Equal(t0.Add(2*time.Hour)) || status[0].LastOperationID != ops[0].ID {
		t.Fatalf("status = %+v", status[0])
	}
}

func TestMissedWindowRunsOnceAtStartupNoBurst(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0}
	var runs atomic.Int32
	job := quickJob("a", true, &runs)
	first := newSched(st, clock, job)
	clock.Advance(time.Hour)
	if err := first.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		ops, _ := operation.New(st).List(0)
		return len(ops) == 1 && operation.Terminal(ops[0].Status)
	})

	// Daemon down for ten intervals; a new scheduler over the same store.
	clock.Advance(10 * time.Hour)
	second := newSched(st, clock, job)
	for i := 0; i < 3; i++ {
		if err := second.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, func() bool { return runs.Load() == 2 })
	time.Sleep(50 * time.Millisecond)
	if runs.Load() != 2 {
		t.Fatalf("runs = %d, want exactly one catch-up run", runs.Load())
	}
}

func TestOverlapSkippedAndAudited(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0}
	release := make(chan struct{})
	var runs atomic.Int32
	job := Job{
		ID: "slow", Interval: time.Hour, Enabled: true, Since: t0,
		Build: func() (operation.Definition, error) {
			return operation.Definition{Type: "test.slow", Steps: []operation.StepDef{{
				Name: "block", Run: func(ctx context.Context) error {
					runs.Add(1)
					<-release
					return nil
				},
			}}}, nil
		},
	}
	s := newSched(st, clock, job)
	clock.Advance(time.Hour)
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return runs.Load() == 1 })
	blocking := func() int64 {
		ops, _ := operation.New(st).List(0)
		return ops[0].ID
	}()

	clock.Advance(time.Hour)
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	skips := auditActions(t, st, "scheduler.skip")
	if len(skips) != 1 {
		t.Fatalf("skip audits = %+v", skips)
	}
	d := skips[0].Detail
	if !strings.Contains(d, "slow") || !strings.Contains(d, t0.Add(2*time.Hour).Format(time.RFC3339Nano)) ||
		!strings.Contains(d, "operation "+itoa(blocking)) {
		t.Fatalf("skip detail = %q", d)
	}
	status, _ := s.Status()
	if !status[0].Running {
		t.Fatal("status should report running")
	}
	close(release)
	ops, _ := operation.New(st).List(0)
	if len(ops) != 1 || runs.Load() != 1 {
		t.Fatalf("skipped run must not enqueue: ops=%d runs=%d", len(ops), runs.Load())
	}
	// Skipped window is spent, not queued: finishing the blocker does not
	// trigger a run until the next window.
	waitFor(t, func() bool {
		op, _ := operation.New(st).Get(blocking)
		return operation.Terminal(op.Status)
	})
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(auditActions(t, st, "scheduler.run")); got != 1 {
		t.Fatalf("run audits after skip = %d", got)
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

func TestDisabledNeverFires(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0}
	var runs atomic.Int32
	s := newSched(st, clock, quickJob("off", false, &runs))
	clock.Advance(100 * time.Hour)
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if runs.Load() != 0 {
		t.Fatal("disabled job ran")
	}
	status, _ := s.Status()
	if len(status) != 1 || status[0].Enabled || !status[0].NextDue.IsZero() {
		t.Fatalf("status = %+v", status)
	}
}

func TestBuildFailureRecordedAndAudited(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0}
	job := Job{ID: "bad", Interval: time.Hour, Enabled: true, Since: t0,
		Build: func() (operation.Definition, error) { return operation.Definition{}, errors.New("no sources") }}
	s := newSched(st, clock, job)
	clock.Advance(time.Hour)
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := auditActions(t, st, "scheduler.run")
	if len(got) != 1 || got[0].Outcome != audit.OutcomeError || !strings.Contains(got[0].Detail, "no sources") {
		t.Fatalf("audit = %+v", got)
	}
	status, _ := s.Status()
	if !strings.Contains(status[0].LastError, "no sources") {
		t.Fatalf("status = %+v", status[0])
	}
	// Window spent: no retry storm on the next tick.
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(auditActions(t, st, "scheduler.run")); n != 1 {
		t.Fatalf("audits = %d", n)
	}
}

func TestRunStopsOnCancelAndFiresAtStartup(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0.Add(5 * time.Hour)}
	var runs atomic.Int32
	s := newSched(st, clock, quickJob("a", true, &runs))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx, nil); close(done) }()
	waitFor(t, func() bool { return runs.Load() == 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func recordedOp(t *testing.T, st *store.Store, status string) int64 {
	t.Helper()
	op, err := operation.New(st).Create("test.job", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if status != "" {
		if err := operation.New(st).Transition(op.ID, status, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	return op.ID
}

func TestGetErrorFailsClosedButMissingRowIsIdle(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0}
	var runs atomic.Int32
	s := newSched(st, clock, quickJob("a", true, &runs))
	if err := s.save("a", "last_run_at = ?, last_operation_id = 999", format(t0)); err != nil {
		t.Fatal(err)
	}
	// Definitively missing row: counts as idle, job runs.
	clock.Advance(time.Hour)
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return runs.Load() == 1 })
	waitFor(t, func() bool {
		ops, _ := operation.New(st).List(0)
		return len(ops) == 1 && operation.Terminal(ops[0].Status)
	})

	// Unreadable operation store: idle cannot be proven, so skip.
	if _, err := st.DB().Exec(`ALTER TABLE operations RENAME TO operations_gone`); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Hour)
	err := s.Tick(context.Background())
	if _, rerr := st.DB().Exec(`ALTER TABLE operations_gone RENAME TO operations`); rerr != nil {
		t.Fatal(rerr)
	}
	if err == nil || !strings.Contains(err.Error(), "cannot verify") {
		t.Fatalf("Tick err = %v", err)
	}
	if runs.Load() != 1 {
		t.Fatalf("runs = %d, must not run when idle is unproven", runs.Load())
	}
	skips := auditActions(t, st, "scheduler.skip")
	if len(skips) != 1 || skips[0].Outcome != audit.OutcomeError || !strings.Contains(skips[0].Detail, "cannot verify") {
		t.Fatalf("skips = %+v", skips)
	}
}

func TestSaveFailureAfterStartCannotDuplicateRun(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0}
	release := make(chan struct{})
	var runs atomic.Int32
	job := Job{ID: "slow", Interval: time.Hour, Enabled: true, Since: t0,
		Build: func() (operation.Definition, error) {
			return operation.Definition{Type: "test.slow", Steps: []operation.StepDef{{
				Name: "block", Run: func(ctx context.Context) error { runs.Add(1); <-release; return nil },
			}}}, nil
		}}
	s := newSched(st, clock, job)
	if _, err := st.DB().Exec(`CREATE TRIGGER fail_opid BEFORE UPDATE ON scheduler_jobs
		WHEN NEW.last_operation_id != 0 BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Hour)
	if err := s.Tick(context.Background()); err == nil {
		t.Fatal("expected the injected save failure to surface")
	}
	waitFor(t, func() bool { return runs.Load() == 1 })
	// Same window: spent by the pending marker, nothing re-enqueued.
	for i := 0; i < 3; i++ {
		_ = s.Tick(context.Background())
	}
	// Next window while the first run is live: reconcile finds it by payload.
	clock.Advance(time.Hour)
	_ = s.Tick(context.Background())
	ops, _ := operation.New(st).List(0)
	if len(ops) != 1 || runs.Load() != 1 {
		t.Fatalf("duplicate run: ops=%d runs=%d", len(ops), runs.Load())
	}
	skips := auditActions(t, st, "scheduler.skip")
	if len(skips) != 1 || skips[0].Outcome != audit.OutcomeSkipped ||
		!strings.Contains(skips[0].Detail, "operation "+itoa(ops[0].ID)) || !strings.Contains(skips[0].Detail, "cancel") {
		t.Fatalf("skips = %+v", skips)
	}
	close(release)
}

func TestStatusStaysResponsiveDuringBuild(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0.Add(2 * time.Hour)}
	entered, release := make(chan struct{}), make(chan struct{})
	job := Job{ID: "a", Interval: time.Hour, Enabled: true, Since: t0,
		Build: func() (operation.Definition, error) {
			close(entered)
			<-release
			return operation.Definition{}, errors.New("stop")
		}}
	s := newSched(st, clock, job)
	done := make(chan error)
	go func() { done <- s.Tick(context.Background()) }()
	<-entered
	statusDone := make(chan struct{})
	go func() { _, _ = s.Status(); close(statusDone) }()
	select {
	case <-statusDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Status blocked behind Build")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentTickStatusRun(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0}
	var runs atomic.Int32
	s := New(st, WithClock(clock.Now), WithTickEvery(time.Millisecond))
	s.Register("test", SourceFunc(func() ([]Job, error) {
		return []Job{quickJob("a", true, &runs), quickJob("b", true, &runs)}, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); s.Run(ctx, nil) }()
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				clock.Advance(time.Hour)
				_ = s.Tick(ctx)
				_, _ = s.Status()
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()
	waitFor(t, func() bool {
		ops, _ := operation.New(st).List(0)
		for _, op := range ops {
			if !operation.Terminal(op.Status) {
				return false
			}
		}
		return true
	})
}

func TestTickHonoursCancelBetweenJobs(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0.Add(2 * time.Hour)}
	ctx, cancel := context.WithCancel(context.Background())
	var builtB atomic.Bool
	a := Job{ID: "a", Interval: time.Hour, Enabled: true, Since: t0,
		Build: func() (operation.Definition, error) { cancel(); return operation.Definition{}, errors.New("x") }}
	b := Job{ID: "b", Interval: time.Hour, Enabled: true, Since: t0,
		Build: func() (operation.Definition, error) {
			builtB.Store(true)
			return operation.Definition{}, errors.New("x")
		}}
	s := newSched(st, clock, a, b)
	err := s.Tick(ctx)
	if !errors.Is(err, context.Canceled) || builtB.Load() {
		t.Fatalf("err = %v, builtB = %v", err, builtB.Load())
	}
}

func TestBackwardsClockClamped(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0}
	var runs atomic.Int32
	s := newSched(st, clock, quickJob("a", true, &runs))
	future := t0.Add(24 * time.Hour)
	if err := s.save("a", "last_run_at = ?, last_skip_at = ?", format(future), format(future)); err != nil {
		t.Fatal(err)
	}
	status, _ := s.Status()
	if !status[0].NextDue.Equal(t0.Add(time.Hour)) {
		t.Fatalf("next due = %v", status[0].NextDue)
	}
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	st1, _ := s.load("a")
	if !st1.lastRun.Equal(t0) || !st1.lastSkip.Equal(t0) {
		t.Fatalf("clamp not persisted: %+v", st1)
	}
	clock.Advance(time.Hour)
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return runs.Load() == 1 })
}

func TestDuplicateJobIDsReportedFirstWins(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0.Add(time.Hour)}
	var runsA, runsB atomic.Int32
	s := New(st, WithClock(clock.Now))
	s.Register("one", SourceFunc(func() ([]Job, error) { return []Job{quickJob("dup", true, &runsA)}, nil }))
	s.Register("two", SourceFunc(func() ([]Job, error) {
		return []Job{quickJob("dup", true, &runsB), quickJob("x", true, &runsB)}, nil
	}))
	err := s.Tick(context.Background())
	if err == nil || !strings.Contains(err.Error(), `duplicate job id "dup"`) {
		t.Fatalf("err = %v", err)
	}
	waitFor(t, func() bool { return runsA.Load() == 1 && runsB.Load() == 1 })
	status, _ := s.Status()
	if len(status) != 2 {
		t.Fatalf("status = %+v", status)
	}
}

func TestPayloadIsValidJSONForAwkwardIDs(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0.Add(time.Hour)}
	var runs atomic.Int32
	id := "we\"ird\n id"
	s := newSched(st, clock, quickJob(id, true, &runs))
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	ops, _ := operation.New(st).List(0)
	var p struct{ Job string }
	if len(ops) != 1 || json.Unmarshal([]byte(ops[0].Payload), &p) != nil || p.Job != id {
		t.Fatalf("payload = %+v", ops)
	}
}

func TestSkipStateReadBack(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0}
	blocker := recordedOp(t, st, operation.StatusRunning)
	var runs atomic.Int32
	s := newSched(st, clock, quickJob("a", true, &runs))
	if err := s.save("a", "last_run_at = ?, last_operation_id = ?", format(t0), blocker); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Hour)
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, _ := s.Status()
	if status[0].LastSkipBlocking != blocker || !status[0].LastSkipDue.Equal(t0.Add(time.Hour)) || !status[0].Running {
		t.Fatalf("status = %+v", status[0])
	}
	if got := auditActions(t, st, "scheduler.skip"); len(got) != 1 || got[0].Outcome != audit.OutcomeSkipped {
		t.Fatalf("skip audit = %+v", got)
	}
}

// A source that fails is reported (audited, rate-limited) and skipped;
// other sources' jobs still run, and Status still lists them.
func TestFailingSourceDoesNotStopOtherSources(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0}
	var runs atomic.Int32
	s := New(st, WithClock(clock.Now))
	s.Register("drift", SourceFunc(func() ([]Job, error) { return nil, errors.New("schedule unreadable") }))
	s.Register("backup", SourceFunc(func() ([]Job, error) { return []Job{quickJob("backup:x", true, &runs)}, nil }))

	clock.Advance(time.Hour)
	err := s.Tick(context.Background())
	if err == nil || !strings.Contains(err.Error(), `source "drift"`) {
		t.Fatalf("tick err = %v, want drift source error", err)
	}
	waitFor(t, func() bool { return runs.Load() == 1 })
	if got := auditActions(t, st, "scheduler.source"); len(got) != 1 || got[0].Outcome != audit.OutcomeError {
		t.Fatalf("source audit = %+v", got)
	}
	if status, err := s.Status(); err != nil || len(status) != 1 || status[0].ID != "backup:x" {
		t.Fatalf("status = %+v %v", status, err)
	}

	// Rate-limited: the next ticks within the hour neither audit nor
	// return the error; after it, reported again.
	clock.Advance(time.Minute)
	if err := s.Tick(context.Background()); err != nil {
		t.Fatalf("rate-limited tick err = %v", err)
	}
	clock.Advance(SourceErrorEvery)
	if err := s.Tick(context.Background()); err == nil {
		t.Fatal("source error not reported again after the rate limit")
	}
	if got := auditActions(t, st, "scheduler.source"); len(got) != 2 {
		t.Fatalf("source audit = %d, want 2", len(got))
	}
}

func TestUnavailableBuildKeepsWindowAndBacksOff(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0}
	var runs, builds atomic.Int32
	ready := false
	job := Job{ID: "late", Interval: time.Hour, Enabled: true, Since: t0,
		Build: func() (operation.Definition, error) {
			builds.Add(1)
			if !ready {
				return operation.Definition{}, errors.Join(ErrUnavailable, errors.New("mount missing"))
			}
			return quickJob("late", true, &runs).Build()
		}}
	s := newSched(st, clock, job)
	tick := func() {
		t.Helper()
		if err := s.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(time.Hour)
	tick()
	if builds.Load() != 1 {
		t.Fatalf("builds = %d", builds.Load())
	}
	clock.Advance(RetryBase - time.Second)
	tick()
	if builds.Load() != 1 {
		t.Fatalf("retried inside backoff: %d", builds.Load())
	}
	clock.Advance(2 * time.Second)
	tick()
	if builds.Load() != 2 {
		t.Fatalf("no retry after backoff: %d", builds.Load())
	}
	// The second delay doubles.
	clock.Advance(RetryBase + time.Second)
	tick()
	if builds.Load() != 2 {
		t.Fatalf("backoff did not grow: %d", builds.Load())
	}
	if n := len(auditActions(t, st, "scheduler.run")); n != 1 {
		t.Fatalf("audits = %d, want one per window", n)
	}
	status, _ := s.Status()
	if !strings.Contains(status[0].LastError, "mount missing") || !status[0].LastRunAt.IsZero() {
		t.Fatalf("window spent or error missing: %+v", status[0])
	}
	// The mount appears: the same window runs, long before the next interval.
	ready = true
	clock.Advance(10 * time.Minute)
	tick()
	waitFor(t, func() bool { return runs.Load() == 1 })
	clock.Advance(time.Minute)
	tick()
	if runs.Load() != 1 {
		t.Fatalf("runs = %d", runs.Load())
	}
}

func TestUnavailableBackoffCappedAtInterval(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0}
	var builds atomic.Int32
	job := Job{ID: "capped", Interval: 6 * time.Minute, Enabled: true, Since: t0,
		Build: func() (operation.Definition, error) {
			builds.Add(1)
			return operation.Definition{}, errors.Join(ErrUnavailable, errors.New("gone"))
		}}
	s := newSched(st, clock, job)
	clock.Advance(6 * time.Minute)
	for i := 0; i < 5; i++ {
		if err := s.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		clock.Advance(6 * time.Minute)
	}
	// Uncapped, the delays 5, 10, 20 minutes would allow only 3 attempts.
	if builds.Load() != 5 {
		t.Fatalf("builds = %d, want a retry every capped interval", builds.Load())
	}
}

func TestUnavailableAuditsEachWindowOnce(t *testing.T) {
	st := openStore(t)
	clock := &fakeClock{now: t0}
	job := Job{ID: "w", Interval: time.Hour, Enabled: true, Since: t0,
		Build: func() (operation.Definition, error) {
			return operation.Definition{}, errors.Join(ErrUnavailable, errors.New("gone"))
		}}
	s := newSched(st, clock, job)
	clock.Advance(time.Hour)
	for i := 0; i < 5; i++ {
		_ = s.Tick(context.Background())
		clock.Advance(RetryBase * 2)
	}
	if n := len(auditActions(t, st, "scheduler.run")); n != 1 {
		t.Fatalf("audits = %d, want 1", n)
	}
}
