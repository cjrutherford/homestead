// Package scheduler is the in-process daemon scheduler (S260, ADR-023).
//
// It never executes work itself: a due job is enqueued through the
// operation runner, so scheduled runs are visible and audited like
// owner-initiated ones. Last-run state lives in SQLite. A window missed
// while the daemon was down runs once at startup (the first Tick), with
// no burst catch-up; a job whose previous run is still queued or running
// is skipped and audited, never run concurrently.
//
// Later periodic work (drift scans, certificate checks) registers a
// Source with Register; this package knows nothing about those domains.
package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"sync"
	"time"

	"homestead/internal/audit"
	"homestead/internal/operation"
	"homestead/internal/store"
)

// Actor is the audit identity of scheduler-initiated work.
const Actor = "scheduler"

// DefaultTickEvery is how often Run evaluates schedules.
const DefaultTickEvery = 30 * time.Second

// Job is one periodic unit of work as a Source reports it right now.
type Job struct {
	// ID is stable and unique across sources (for example "backup:immich").
	ID       string
	Interval time.Duration
	Enabled  bool
	// Since anchors a job that has never run: its first window ends
	// Interval after Since (for backups, when the schedule was saved).
	Since time.Time
	// Build produces the operation to enqueue. Called only when due.
	Build func() (operation.Definition, error)
}

// Source lists the jobs currently declared by one domain. It is read on
// every tick, so edits to declared schedules take effect without restart.
type Source interface {
	ScheduledJobs() ([]Job, error)
}

// SourceFunc adapts a function to Source.
type SourceFunc func() ([]Job, error)

// ScheduledJobs implements Source.
func (f SourceFunc) ScheduledJobs() ([]Job, error) { return f() }

// Status is the owner-visible state of one job.
type Status struct {
	ID              string    `json:"id"`
	IntervalSeconds int64     `json:"interval_seconds"`
	Enabled         bool      `json:"enabled"`
	LastRunAt       time.Time `json:"last_run_at"`
	LastOperationID int64     `json:"last_operation_id"`
	LastError       string    `json:"last_error"`
	LastSkipAt      time.Time `json:"last_skip_at"`
	NextDue         time.Time `json:"next_due"`
	Running         bool      `json:"running"`
}

// Scheduler evaluates registered sources and enqueues due jobs.
type Scheduler struct {
	db     *sql.DB
	runner *operation.Runner
	ops    *operation.Store
	audit  *audit.Service
	clock  func() time.Time
	every  time.Duration

	mu      sync.Mutex // serializes Tick and Status; guards sources
	sources []namedSource
}

type namedSource struct {
	name string
	src  Source
}

// Option configures New.
type Option func(*Scheduler)

// WithClock injects the time source (tests).
func WithClock(now func() time.Time) Option {
	return func(s *Scheduler) { s.clock = now }
}

// WithTickEvery overrides the evaluation cadence of Run.
func WithTickEvery(d time.Duration) Option {
	return func(s *Scheduler) { s.every = d }
}

// New returns a Scheduler over an open store.
func New(st *store.Store, opts ...Option) *Scheduler {
	s := &Scheduler{
		db: st.DB(), runner: operation.NewRunner(st), ops: operation.New(st),
		audit: audit.New(st), clock: time.Now, every: DefaultTickEvery,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Register adds a job source. Names label errors only; job IDs identify jobs.
func (s *Scheduler) Register(name string, src Source) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sources = append(s.sources, namedSource{name, src})
}

type state struct {
	lastRun      time.Time
	lastOp       int64
	lastErr      string
	lastSkip     time.Time
	skipBlocking int64
}

func parse(v string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}

func format(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func (s *Scheduler) load(id string) (state, error) {
	var st state
	var run, skip string
	err := s.db.QueryRow(
		`SELECT last_run_at, last_operation_id, last_error, last_skip_at, last_skip_blocking
		 FROM scheduler_jobs WHERE job_id = ?`, id,
	).Scan(&run, &st.lastOp, &st.lastErr, &skip, &st.skipBlocking)
	if err == sql.ErrNoRows {
		return state{}, nil
	}
	if err != nil {
		return state{}, fmt.Errorf("scheduler: load %q: %w", id, err)
	}
	st.lastRun, st.lastSkip = parse(run), parse(skip)
	return st, nil
}

// nextDue is one interval after the latest of the anchor, the last run,
// and the last skip: a skipped window is spent, not queued.
func nextDue(job Job, st state) time.Time {
	base := job.Since
	if st.lastRun.After(base) {
		base = st.lastRun
	}
	if st.lastSkip.After(base) {
		base = st.lastSkip
	}
	return base.Add(job.Interval)
}

func (s *Scheduler) jobs() (map[string]Job, []string, error) {
	out := map[string]Job{}
	var ids []string
	for _, ns := range s.sources {
		jobs, err := ns.src.ScheduledJobs()
		if err != nil {
			return nil, nil, fmt.Errorf("scheduler: source %q: %w", ns.name, err)
		}
		for _, j := range jobs {
			if j.ID == "" || j.Interval <= 0 {
				continue
			}
			if _, dup := out[j.ID]; dup {
				continue
			}
			out[j.ID] = j
			ids = append(ids, j.ID)
		}
	}
	sort.Strings(ids)
	return out, ids, nil
}

// Tick evaluates every enabled job once at the injected clock's time.
// The first Tick after a restart is the missed-run check: a job whose
// window elapsed while the daemon was down is due and runs exactly once.
func (s *Scheduler) Tick(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs, ids, err := s.jobs()
	if err != nil {
		return err
	}
	now := s.clock()
	var firstErr error
	for _, id := range ids {
		job := jobs[id]
		if !job.Enabled {
			continue
		}
		st, err := s.load(id)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		due := nextDue(job, st)
		if now.Before(due) {
			continue
		}
		if err := s.fire(ctx, job, st, due, now); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Scheduler) fire(ctx context.Context, job Job, st state, due, now time.Time) error {
	if st.lastOp != 0 {
		op, err := s.ops.Get(st.lastOp)
		if err == nil && !operation.Terminal(op.Status) {
			return s.skip(job, due, now, op.ID)
		}
	}
	def, err := job.Build()
	if err != nil {
		return s.failed(job, now, fmt.Errorf("build: %w", err))
	}
	payload := fmt.Sprintf(`{"scheduled":true,"job":%q,"due":%q}`, job.ID, format(due))
	op, err := s.runner.Start(ctx, def, payload)
	if err != nil {
		return s.failed(job, now, fmt.Errorf("enqueue: %w", err))
	}
	if err := s.save(job.ID, "last_run_at = ?, last_operation_id = ?, last_error = ''", format(now), op.ID); err != nil {
		return err
	}
	_, err = s.audit.Record(audit.Entry{
		Actor: Actor, Action: "scheduler.run", Outcome: audit.OutcomeOK,
		Detail: fmt.Sprintf("job %s due %s enqueued operation %d", job.ID, format(due), op.ID),
	})
	return err
}

// failed records an attempt that could not start. The window is spent
// (last run advances) so a persistent fault retries per interval rather
// than every tick.
func (s *Scheduler) failed(job Job, now time.Time, cause error) error {
	if err := s.save(job.ID, "last_run_at = ?, last_operation_id = 0, last_error = ?", format(now), cause.Error()); err != nil {
		return err
	}
	_, err := s.audit.Record(audit.Entry{
		Actor: Actor, Action: "scheduler.run", Outcome: audit.OutcomeError,
		Detail: fmt.Sprintf("job %s could not start: %v", job.ID, cause),
	})
	return err
}

func (s *Scheduler) skip(job Job, due, now time.Time, blocking int64) error {
	if err := s.save(job.ID, "last_skip_at = ?, last_skip_due = ?, last_skip_blocking = ?", format(now), format(due), blocking); err != nil {
		return err
	}
	_, err := s.audit.Record(audit.Entry{
		Actor: Actor, Action: "scheduler.skip", Outcome: audit.OutcomeError,
		Detail: fmt.Sprintf("job %s due %s skipped: operation %d still active", job.ID, format(due), blocking),
	})
	return err
}

// save upserts the named columns. set is a trusted internal fragment.
func (s *Scheduler) save(id, set string, args ...any) error {
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO scheduler_jobs (job_id) VALUES (?)`, id); err != nil {
		return fmt.Errorf("scheduler: save %q: %w", id, err)
	}
	if _, err := s.db.Exec(`UPDATE scheduler_jobs SET `+set+` WHERE job_id = ?`, append(args, id)...); err != nil {
		return fmt.Errorf("scheduler: save %q: %w", id, err)
	}
	return nil
}

// Status reports every declared job with persisted run state.
func (s *Scheduler) Status() ([]Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs, ids, err := s.jobs()
	if err != nil {
		return nil, err
	}
	out := []Status{}
	for _, id := range ids {
		job := jobs[id]
		st, err := s.load(id)
		if err != nil {
			return nil, err
		}
		status := Status{
			ID: id, IntervalSeconds: int64(job.Interval / time.Second), Enabled: job.Enabled,
			LastRunAt: st.lastRun, LastOperationID: st.lastOp, LastError: st.lastErr,
			LastSkipAt: st.lastSkip,
		}
		if job.Enabled {
			status.NextDue = nextDue(job, st)
		}
		if st.lastOp != 0 {
			if op, err := s.ops.Get(st.lastOp); err == nil && !operation.Terminal(op.Status) {
				status.Running = true
			}
		}
		out = append(out, status)
	}
	return out, nil
}

// Run evaluates immediately (the missed-run check) and then every tick
// until ctx ends. Tick errors go to onErr when non-nil; scheduling never
// stops for a failed tick.
func (s *Scheduler) Run(ctx context.Context, onErr func(error)) {
	report := func(err error) {
		if err != nil && onErr != nil {
			onErr(err)
		}
	}
	report(s.Tick(ctx))
	ticker := time.NewTicker(s.every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			report(s.Tick(ctx))
		}
	}
}
