package operation

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"homestead/internal/store"
)

func openRunner(t *testing.T) *Runner {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return NewRunner(s)
}

func TestStartReturnsWhileRunning(t *testing.T) {
	runner := openRunner(t)
	release := make(chan struct{})
	def := Definition{Type: "test.long", Steps: []StepDef{
		{Name: "wait", Run: func(ctx context.Context) error {
			<-release
			return nil
		}},
	}}
	started := make(chan Operation, 1)
	startErr := make(chan error, 1)
	go func() {
		op, err := runner.Start(context.Background(), def, "")
		if err != nil {
			startErr <- err
			return
		}
		started <- op
	}()
	var op Operation
	select {
	case op = <-started:
	case err := <-startErr:
		t.Fatalf("Start: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start blocked on step execution")
	}
	if Terminal(op.Status) {
		t.Fatalf("Start returned terminal op %+v", op)
	}
	close(release)
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", done.Status)
	}
}

func TestFullLifecycle(t *testing.T) {
	runner := openRunner(t)
	var order []string
	var mu sync.Mutex
	step := func(name string) StepDef {
		return StepDef{Name: name, Run: func(ctx context.Context) error {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			return nil
		}}
	}
	op, err := runner.Start(context.Background(), Definition{
		Type:  "test.seq",
		Steps: []StepDef{step("one"), step("two")},
	}, `{"x": 1}`)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != StatusSucceeded {
		t.Fatalf("status = %q", done.Status)
	}
	if len(done.Steps) != 2 || done.Steps[0].Status != StatusSucceeded {
		t.Fatalf("steps = %+v", done.Steps)
	}
	if done.Steps[0].StartedAt.IsZero() || done.Steps[0].FinishedAt.IsZero() {
		t.Fatal("timestamps missing")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "one" || order[1] != "two" {
		t.Fatalf("order = %v", order)
	}
}

func TestFailureStopsAndMarks(t *testing.T) {
	runner := openRunner(t)
	ran := false
	op, err := runner.Start(context.Background(), Definition{
		Type: "test.fail",
		Steps: []StepDef{
			{Name: "bad", Run: func(ctx context.Context) error {
				return errors.New("boom")
			}},
			{Name: "never", Run: func(ctx context.Context) error {
				ran = true
				return nil
			}},
		},
	}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != StatusFailed || done.Error != "boom" {
		t.Fatalf("op = %+v", done)
	}
	if done.Steps[0].Status != StatusFailed || done.Steps[1].Status != StatusPending {
		t.Fatalf("steps = %+v (second stays pending, never started)", done.Steps)
	}
	if ran {
		t.Fatal("second step ran after failure")
	}
}

func TestCancel(t *testing.T) {
	runner := openRunner(t)
	release := make(chan struct{})
	op, err := runner.Start(context.Background(), Definition{
		Type: "test.cancel",
		Steps: []StepDef{
			{Name: "wait", Run: func(ctx context.Context) error {
				<-release
				return nil
			}},
			{Name: "later", Run: func(ctx context.Context) error { return nil }},
		},
	}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := runner.store.Get(op.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status == StatusRunning && len(got.Steps) > 0 && got.Steps[0].Status == StatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("step never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := runner.Cancel(op.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	close(release)
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != StatusCancelled {
		t.Fatalf("status = %q, want cancelled", done.Status)
	}
	if done.Steps[0].Status != StatusCancelled || done.Steps[1].Status != StatusPending {
		t.Fatalf("steps = %+v", done.Steps)
	}
	if err := runner.Cancel(op.ID); err != nil {
		t.Fatalf("second Cancel: %v (idempotent)", err)
	}
}

func TestStepPanicFails(t *testing.T) {
	runner := openRunner(t)
	op, err := runner.Start(context.Background(), Definition{
		Type: "test.panic",
		Steps: []StepDef{{Name: "bang", Run: func(ctx context.Context) error {
			panic("kaput")
		}}},
	}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", done.Status)
	}
}

func TestDefinitionValidation(t *testing.T) {
	runner := openRunner(t)
	if _, err := runner.Start(context.Background(), Definition{Type: "x.y"}, ""); err == nil {
		t.Fatal("empty steps accepted")
	}
	if _, err := runner.Start(context.Background(), Definition{Type: "Bad!"}, ""); err == nil {
		t.Fatal("bad type accepted")
	}
	dup := Definition{Type: "x.y", Steps: []StepDef{
		{Name: "a", Run: func(ctx context.Context) error { return nil }},
		{Name: "a", Run: func(ctx context.Context) error { return nil }},
	}}
	if _, err := runner.Start(context.Background(), dup, ""); err == nil {
		t.Fatal("duplicate steps accepted")
	}
	if _, err := runner.Start(context.Background(), Definition{
		Type:  "x.y",
		Steps: []StepDef{{Name: "a"}},
	}, ""); err == nil {
		t.Fatal("nil step func accepted")
	}
}

func TestRecover(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	runner := NewRunner(s)
	op, err := runner.Start(context.Background(), Definition{
		Type: "test.crash",
		Steps: []StepDef{{Name: "wait", Run: func(ctx context.Context) error {
			time.Sleep(10 * time.Second)
			return nil
		}}},
	}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	fresh := NewRunner(reopened)
	stale, err := fresh.store.Get(op.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stale.Status != StatusRunning {
		t.Fatalf("status = %q, want still-running record", stale.Status)
	}
	if err := fresh.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	after, err := fresh.store.Get(op.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.Status != StatusCancelled {
		t.Fatalf("status = %q, want cancelled", after.Status)
	}
}

func TestRecoverExceptKeepsNamedOperations(t *testing.T) {
	runner := openRunner(t)
	mk := func() int64 {
		op, err := runner.store.Create("test.keep", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := runner.store.Transition(op.ID, StatusRunning, "", ""); err != nil {
			t.Fatal(err)
		}
		return op.ID
	}
	kept, failed := mk(), mk()
	if err := runner.RecoverExcept(kept); err != nil {
		t.Fatal(err)
	}
	if op, _ := runner.store.Get(kept); op.Status != StatusRunning {
		t.Fatalf("kept op = %s, want running", op.Status)
	}
	if op, _ := runner.store.Get(failed); op.Status != StatusCancelled {
		t.Fatalf("other op = %s, want cancelled", op.Status)
	}
}
