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
	"encoding/json"
	"errors"
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

// ErrUnavailable marks a Build failure caused by something not there
// right now (a source or mount that has not appeared), as opposed to a
// fault in the job itself. The window is not spent: the job retries with
// backoff (RetryBase doubling, capped at its interval) and the outage
// is audited on its first attempt, then at least daily and every
// ReauditAttempts attempts (ADR-026), so a late-mounting disk still gets its
// scheduled backup instead of waiting a whole interval.
var ErrUnavailable = errors.New("scheduler: job inputs unavailable")

// RetryBase is the first retry delay after an unavailable build.
const RetryBase = 5 * time.Minute

// retryState is the in-memory backoff for one job. A restart retries
// immediately, which is the safe direction.
type retryState struct {
	attempts int
	next     time.Time
	since    time.Time // first unavailable attempt of this outage
	audited  time.Time // last time the outage was audited
}

// ReauditEvery and ReauditAttempts bound how long an ongoing outage can
// go unaudited: a repeat audit entry is written at least daily and at
// every ReauditAttempts-th attempt (ADR-023 amendment, ADR-026).
const (
	ReauditEvery    = 24 * time.Hour
	ReauditAttempts = 10
)

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
	// LastSkipDue is the window the last skip spent; LastSkipBlocking is
	// the live operation that blocked it.
	LastSkipDue      time.Time `json:"last_skip_due"`
	LastSkipBlocking int64     `json:"last_skip_blocking"`
	NextDue          time.Time `json:"next_due"`
	Running          bool      `json:"running"`
	// UnavailableAttempts and UnavailableSince describe an ongoing
	// outage (builds failing with ErrUnavailable, window not spent);
	// zero when the job is not waiting. In-memory, reset on restart.
	UnavailableAttempts int       `json:"unavailable_attempts"`
	UnavailableSince    time.Time `json:"unavailable_since"`
}

// Scheduler evaluates registered sources and enqueues due jobs.
type Scheduler struct {
	db     *sql.DB
	runner *operation.Runner
	ops    *operation.Store
	audit  *audit.Service
	clock  func() time.Time
	every  time.Duration

	tickMu sync.Mutex // serializes Tick; never held by Status
	// srcReported is when each failing source was last reported; guarded
	// by tickMu.
	srcReported map[string]time.Time
	// retries holds backoff for jobs whose last build was unavailable;
	// guarded by tickMu.
	retries map[string]*retryState
	retryMu sync.Mutex // guards retries and its states; leaf lock
	mu      sync.Mutex // guards sources only; never held across Build or enqueue
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

// Now reports the scheduler's clock, so sources can stamp work with the
// same injectable time the schedule is evaluated against.
func (s *Scheduler) Now() time.Time { return s.clock() }

// state is one scheduler_jobs row. Rows for jobs whose schedule was
// deleted are orphaned and harmless: nothing reads a row unless a
// declared job with that ID exists, and re-declaring the ID resumes it.
type state struct {
	lastRun      time.Time
	lastOp       int64
	lastErr      string
	lastSkip     time.Time
	skipDue      time.Time
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
	var run, skip, skipDue string
	err := s.db.QueryRow(
		`SELECT last_run_at, last_operation_id, last_error, last_skip_at, last_skip_due, last_skip_blocking
		 FROM scheduler_jobs WHERE job_id = ?`, id,
	).Scan(&run, &st.lastOp, &st.lastErr, &skip, &skipDue, &st.skipBlocking)
	if err == sql.ErrNoRows {
		return state{}, nil
	}
	if err != nil {
		return state{}, fmt.Errorf("scheduler: load %q: %w", id, err)
	}
	st.lastRun, st.lastSkip, st.skipDue = parse(run), parse(skip), parse(skipDue)
	return st, nil
}

// nextDue is one interval after the latest of the anchor, the last run,
// and the last skip: a skipped window is spent, not queued. Recorded
// times in the future (the clock stepped backwards) count as now, so a
// bad clock step cannot silence a job until the clock catches up.
func nextDue(job Job, st state, now time.Time) time.Time {
	st = clampState(st, now)
	base := job.Since
	if st.lastRun.After(base) {
		base = st.lastRun
	}
	if st.lastSkip.After(base) {
		base = st.lastSkip
	}
	return base.Add(job.Interval)
}

func clampState(st state, now time.Time) state {
	if st.lastRun.After(now) {
		st.lastRun = now
	}
	if st.lastSkip.After(now) {
		st.lastSkip = now
	}
	return st
}

// jobs reads every source. The first job to claim an ID wins; later
// duplicates are dropped and reported in dups (never silently).
//
// A source that errors is isolated: its jobs are skipped this tick, the
// other sources still run, and the failure is returned in srcErrs.
func (s *Scheduler) jobs() (out map[string]Job, ids []string, dups []error, srcErrs map[string]error) {
	s.mu.Lock()
	sources := append([]namedSource(nil), s.sources...)
	s.mu.Unlock()
	out = map[string]Job{}
	srcErrs = map[string]error{}
	for _, ns := range sources {
		jobs, err := ns.src.ScheduledJobs()
		if err != nil {
			srcErrs[ns.name] = fmt.Errorf("scheduler: source %q: %w", ns.name, err)
			continue
		}
		for _, j := range jobs {
			if j.ID == "" || j.Interval <= 0 {
				continue
			}
			if _, dup := out[j.ID]; dup {
				dups = append(dups, fmt.Errorf("scheduler: source %q: duplicate job id %q ignored", ns.name, j.ID))
				continue
			}
			out[j.ID] = j
			ids = append(ids, j.ID)
		}
	}
	sort.Strings(ids)
	return out, ids, dups, srcErrs
}

// SourceErrorEvery rate-limits reporting of one failing source: it is
// audited and returned from Tick at most this often, not every tick.
const SourceErrorEvery = time.Hour

// reportSources audits each failing source (rate-limited per source)
// and returns the errors that were newly reported. Callers hold tickMu.
func (s *Scheduler) reportSources(srcErrs map[string]error, now time.Time) []error {
	names := make([]string, 0, len(srcErrs))
	for name := range srcErrs {
		names = append(names, name)
	}
	sort.Strings(names)
	if s.srcReported == nil {
		s.srcReported = map[string]time.Time{}
	}
	for name := range s.srcReported {
		if _, failing := srcErrs[name]; !failing {
			delete(s.srcReported, name) // recovered: report again if it fails anew
		}
	}
	var out []error
	for _, name := range names {
		if last, ok := s.srcReported[name]; ok && now.Sub(last) < SourceErrorEvery && !now.Before(last) {
			continue
		}
		s.srcReported[name] = now
		out = append(out, srcErrs[name])
		if _, err := s.audit.Record(audit.Entry{
			Actor: Actor, Action: "scheduler.source", Outcome: audit.OutcomeError,
			Detail: fmt.Sprintf("%v; its jobs are skipped, other sources still run", srcErrs[name]),
		}); err != nil {
			out = append(out, err)
		}
	}
	return out
}

// Tick evaluates every enabled job once at the injected clock's time.
// The first Tick after a restart is the missed-run check: a job whose
// window elapsed while the daemon was down is due and runs exactly once.
// Ticks serialize with each other but not with Status: Build and enqueue
// run without the status lock. ctx is honoured between jobs.
func (s *Scheduler) Tick(ctx context.Context) error {
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	jobs, ids, dups, srcErrs := s.jobs()
	now := s.clock()
	var errs []error
	errs = append(errs, dups...)
	errs = append(errs, s.reportSources(srcErrs, now)...)
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		job := jobs[id]
		if !job.Enabled {
			continue
		}
		st, err := s.load(id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if clamped := clampState(st, now); clamped != st {
			// Persist the clamp so the anchor is stable across ticks.
			if err := s.save(id, "last_run_at = ?, last_skip_at = ?", format(clamped.lastRun), format(clamped.lastSkip)); err != nil {
				errs = append(errs, err)
				continue
			}
			st = clamped
		}
		due := nextDue(job, st, now)
		if now.Before(due) {
			continue
		}
		if s.backingOff(id, job, now) {
			continue
		}
		if err := s.fire(ctx, job, st, due, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// scanLimit bounds how many recent operations activeOp inspects when
// looking for a live run the saved state does not know about.
const scanLimit = 500

// activeOp returns a non-terminal operation that blocks the job, or
// ok=false when the job is provably idle. Any error means idle cannot
// be proven. A definitively missing last-operation row counts as idle.
// Beyond the recorded last operation it scans recent operations for a
// live run whose payload names the job, which covers a run whose id was
// never saved.
func (s *Scheduler) activeOp(job Job, st state) (id int64, ok bool, err error) {
	if st.lastOp != 0 {
		op, err := s.ops.Get(st.lastOp)
		switch {
		case err == nil:
			if !operation.Terminal(op.Status) {
				return op.ID, true, nil
			}
		case errors.Is(err, operation.ErrNotFound):
		default:
			return st.lastOp, false, err
		}
	}
	recent, err := s.ops.List(scanLimit)
	if err != nil {
		return 0, false, err
	}
	for _, op := range recent {
		if operation.Terminal(op.Status) {
			continue
		}
		var p struct {
			Scheduled bool   `json:"scheduled"`
			Job       string `json:"job"`
		}
		if json.Unmarshal([]byte(op.Payload), &p) == nil && p.Scheduled && p.Job == job.ID {
			return op.ID, true, nil
		}
	}
	return 0, false, nil
}

func (s *Scheduler) fire(ctx context.Context, job Job, st state, due, now time.Time) error {
	blocking, active, err := s.activeOp(job, st)
	if err != nil {
		return s.unverifiable(job, due, blocking, err)
	}
	if active {
		return s.skip(job, due, now, blocking)
	}
	def, err := job.Build()
	if errors.Is(err, ErrUnavailable) {
		return s.unavailable(job, due, now, err)
	}
	if err != nil {
		s.clearRetry(job.ID)
		return s.failed(job, now, fmt.Errorf("build: %w", err))
	}
	s.clearRetry(job.ID)
	raw, err := json.Marshal(struct {
		Scheduled bool   `json:"scheduled"`
		Job       string `json:"job"`
		Due       string `json:"due"`
	}{true, job.ID, format(due)})
	if err != nil {
		return s.failed(job, now, fmt.Errorf("payload: %w", err))
	}
	// Pending marker: spend the window before enqueueing. If anything
	// after this point fails to persist, the job cannot fire again this
	// window, and the payload scan in activeOp finds the live run next
	// window. A failure here starts nothing.
	if err := s.save(job.ID, "last_run_at = ?, last_operation_id = 0, last_error = ''", format(now)); err != nil {
		return err
	}
	op, err := s.runner.Start(ctx, def, string(raw))
	if err != nil {
		return s.failed(job, now, fmt.Errorf("enqueue: %w", err))
	}
	if err := s.save(job.ID, "last_operation_id = ?", op.ID); err != nil {
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

// unavailable records a build that found its inputs missing without
// spending the window: last_run_at stays put so the job remains due, the
// error is visible in status, and the next attempt waits out a capped
// exponential backoff. The audit entry is written once per window.
func (s *Scheduler) unavailable(job Job, due, now time.Time, cause error) error {
	s.retryMu.Lock()
	if s.retries == nil {
		s.retries = map[string]*retryState{}
	}
	r := s.retries[job.ID]
	if r == nil {
		r = &retryState{since: now}
		s.retries[job.ID] = r
	}
	r.attempts++
	delay := RetryBase
	for i := 1; i < r.attempts && delay < job.Interval; i++ {
		delay *= 2
	}
	if delay > job.Interval {
		delay = job.Interval
	}
	r.next = now.Add(delay)
	attempts, since := r.attempts, r.since
	doAudit := r.audited.IsZero() || now.Sub(r.audited) >= ReauditEvery || attempts%ReauditAttempts == 0
	if doAudit {
		r.audited = now
	}
	s.retryMu.Unlock()
	if err := s.save(job.ID, "last_error = ?", "build: "+cause.Error()); err != nil {
		return err
	}
	if !doAudit {
		return nil
	}
	_, err := s.audit.Record(audit.Entry{
		Actor: Actor, Action: "scheduler.run", Outcome: audit.OutcomeError,
		Detail: fmt.Sprintf("job %s due %s waiting: %v; attempt %d since %s; retrying with backoff (next retry in %s), window not spent", job.ID, format(due), cause, attempts, format(since), delay),
	})
	return err
}

// backingOff reports whether an unavailable job is still inside its delay.
func (s *Scheduler) backingOff(id string, job Job, now time.Time) bool {
	s.retryMu.Lock()
	defer s.retryMu.Unlock()
	r, ok := s.retries[id]
	return ok && now.Before(r.next) && r.next.Sub(now) <= job.Interval
}

func (s *Scheduler) clearRetry(id string) {
	s.retryMu.Lock()
	delete(s.retries, id)
	s.retryMu.Unlock()
}

// ClearHint tells the owner how to unblock a job held by a hung
// operation. There is no cancel route yet (docs/DEFERRED.md); startup
// recovery cancels every operation a dead daemon left live.
const ClearHint = "cancel that operation to resume (no cancel route yet: restart homestead-server, whose startup recovery cancels live operations)"

func (s *Scheduler) skip(job Job, due, now time.Time, blocking int64) error {
	if err := s.save(job.ID, "last_skip_at = ?, last_skip_due = ?, last_skip_blocking = ?", format(now), format(due), blocking); err != nil {
		return err
	}
	_, err := s.audit.Record(audit.Entry{
		Actor: Actor, Action: "scheduler.skip", Outcome: audit.OutcomeSkipped,
		Detail: fmt.Sprintf("job %s due %s skipped: operation %d still active; %s", job.ID, format(due), blocking, ClearHint),
	})
	return err
}

// unverifiable skips fail-closed when the previous run's state cannot be
// read: running a second copy is worse than waiting. The window is not
// spent, so the next tick retries the check.
func (s *Scheduler) unverifiable(job Job, due time.Time, blocking int64, cause error) error {
	_, err := s.audit.Record(audit.Entry{
		Actor: Actor, Action: "scheduler.skip", Outcome: audit.OutcomeError,
		Detail: fmt.Sprintf("job %s due %s skipped: cannot verify operation %d is idle: %v", job.ID, format(due), blocking, cause),
	})
	if err != nil {
		return err
	}
	return fmt.Errorf("scheduler: job %q: cannot verify idle: %w", job.ID, cause)
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
	jobs, ids, _, _ := s.jobs()
	now := s.clock()
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
			LastSkipAt: st.lastSkip, LastSkipDue: st.skipDue, LastSkipBlocking: st.skipBlocking,
		}
		if job.Enabled {
			status.NextDue = nextDue(job, st, now)
		}
		s.retryMu.Lock()
		if r, ok := s.retries[id]; ok {
			status.UnavailableAttempts, status.UnavailableSince = r.attempts, r.since
		}
		s.retryMu.Unlock()
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
		if err != nil && ctx.Err() == nil && onErr != nil {
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
