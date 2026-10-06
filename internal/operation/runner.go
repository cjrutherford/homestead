package operation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"homestead/internal/store"
)

// StepFunc is one executable step. It must respect ctx: Cancel and
// shutdown propagate through it. Steps report outcome by error only;
// richer output rides the operation payload/result in later slices.
type StepFunc func(ctx context.Context) error

// StepDef names one step of a Definition.
type StepDef struct {
	Name string
	Run  StepFunc
}

// Definition is an executable operation: its type plus ordered steps.
type Definition struct {
	Type  string
	Steps []StepDef
}

// Runner executes definitions in the background, independently of the
// HTTP connection that started them: Start returns while steps run on,
// and client disconnects never cancel work. Cancellation is explicit,
// and unfinished records from a dead daemon fail at the next startup.
type Runner struct {
	store *Store
	mu    sync.Mutex
	live  map[int64]context.CancelFunc
}

// NewRunner returns a Runner over an open store.
func NewRunner(s *store.Store) *Runner {
	return &Runner{store: New(s), live: map[int64]context.CancelFunc{}}
}

// Start records a pending operation with its steps, marks it running,
// and executes asynchronously, returning immediately. The steps run on
// a context detached from the caller: dropping the HTTP connection does
// not stop the work. Cancel stops it; Recover cleans up after crashes.
func (r *Runner) Start(ctx context.Context, def Definition, payload string) (Operation, error) {
	if err := ValidateType(def.Type); err != nil {
		return Operation{}, err
	}
	if len(def.Steps) == 0 {
		return Operation{}, fmt.Errorf("operation: definition %q has no steps", def.Type)
	}
	for _, step := range def.Steps {
		if step.Name == "" {
			return Operation{}, fmt.Errorf("operation: step name must not be empty")
		}
		if step.Run == nil {
			return Operation{}, fmt.Errorf("operation: step %q has no function", step.Name)
		}
	}
	seen := map[string]bool{}
	for _, step := range def.Steps {
		if seen[step.Name] {
			return Operation{}, fmt.Errorf("operation: duplicate step name %q", step.Name)
		}
		seen[step.Name] = true
	}
	op, err := r.store.Create(def.Type, payload)
	if err != nil {
		return Operation{}, err
	}
	for _, step := range def.Steps {
		if _, err := r.store.AddStep(op.ID, step.Name); err != nil {
			return Operation{}, err
		}
	}
	if err := r.store.Transition(op.ID, StatusRunning, "", ""); err != nil {
		return Operation{}, err
	}
	stepCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r.mu.Lock()
	r.live[op.ID] = cancel
	r.mu.Unlock()
	go r.execute(stepCtx, op.ID, def)
	return r.store.Get(op.ID)
}

// execute runs steps in order, failing fast. A panicking step fails the
// operation instead of stranding it. Races lost to Cancel surface as
// bad-transition errors and end quietly: the state is already correct.
func (r *Runner) execute(ctx context.Context, id int64, def Definition) {
	defer func() {
		r.mu.Lock()
		delete(r.live, id)
		r.mu.Unlock()
	}()
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = r.store.Transition(id, StatusFailed, "", fmt.Sprintf("step panicked: %v", recovered))
		}
	}()
	op, err := r.store.Get(id)
	if err != nil {
		return
	}
	for _, step := range op.Steps {
		fn := findStep(def, step.Name)
		if fn == nil {
			_ = r.store.TransitionStep(id, step.ID, StatusFailed, "step function missing")
			_ = r.store.Transition(id, StatusFailed, "", "step function missing")
			return
		}
		if err := r.store.TransitionStep(id, step.ID, StatusRunning, ""); err != nil {
			return
		}
		if err := fn(ctx); err != nil {
			_ = r.store.TransitionStep(id, step.ID, StatusFailed, err.Error())
			_ = r.store.Transition(id, StatusFailed, "", err.Error())
			return
		}
		if err := r.store.TransitionStep(id, step.ID, StatusSucceeded, ""); err != nil {
			return
		}
	}
	_ = r.store.Transition(id, StatusSucceeded, "", "")
}

// findStep resolves the function for a recorded step by name.
func findStep(def Definition, name string) StepFunc {
	for _, step := range def.Steps {
		if step.Name == name {
			return step.Run
		}
	}
	return nil
}

// Cancel stops a running operation: pending work after the current step
// never starts (stays pending), the running step sees cancellation, and
// the operation lands cancelled. Terminal operations are already stopped
// and report success.
func (r *Runner) Cancel(id int64) error {
	op, err := r.store.Get(id)
	if err != nil {
		return err
	}
	if Terminal(op.Status) {
		return nil
	}
	r.mu.Lock()
	cancel, ok := r.live[id]
	r.mu.Unlock()
	if ok {
		cancel()
	}
	for _, step := range op.Steps {
		if step.Status == StatusRunning {
			_ = r.store.TransitionStep(id, step.ID, StatusCancelled, "cancelled")
		}
	}
	_ = r.store.Transition(id, StatusCancelled, "", "cancelled")
	return nil
}

// Wait blocks until the operation reaches a terminal state, then returns
// it. Client disconnects should cancel via ctx, not via Wait: the work
// continues regardless.
func (r *Runner) Wait(ctx context.Context, id int64) (Operation, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		op, err := r.store.Get(id)
		if err != nil {
			return Operation{}, err
		}
		if Terminal(op.Status) {
			return op, nil
		}
		select {
		case <-ctx.Done():
			return Operation{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Recover fails every non-terminal operation left by a dead daemon, so
// no record claims to run forever. Call once at startup.
func (r *Runner) Recover() error {
	ops, err := r.store.List(0)
	if err != nil {
		return err
	}
	for _, op := range ops {
		if Terminal(op.Status) {
			continue
		}
		if err := r.store.Transition(op.ID, StatusCancelled, "", "interrupted by restart"); err != nil {
			return err
		}
		for _, step := range op.Steps {
			if step.Status == StatusRunning {
				_ = r.store.TransitionStep(op.ID, step.ID, StatusCancelled, "interrupted by restart")
			}
		}
	}
	return nil
}
