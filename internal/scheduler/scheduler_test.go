package scheduler

import (
	"context"
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
