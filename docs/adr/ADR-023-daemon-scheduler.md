# Daemon Scheduler

## Status
Accepted

Accepted by the owner on 2026-10-11.

## Decision
`homestead-server` gets one in-process scheduler. It never executes work
itself: when a schedule is due it enqueues an operation through the
existing operation runner, so scheduled runs are visible, streamed, and
audited exactly like owner-initiated ones. Last-run state is stored in
SQLite. Missed-run policy: if a window was missed while the daemon was
down, run once at startup; there is no burst catch-up. No systemd timers
or cron are introduced, and internal callers invoke services directly
rather than going through HTTP.

**Overlap policy: skip and audit.** The scheduler never runs the same job
concurrently. If a job is due while a previous run of it is still
queued or running, the new run is skipped and a skipped-run audit entry
is recorded (job id, due time, id of the operation that blocked it). A
skipped run is not queued for later; the next due window is evaluated
normally.

## Amended by ADR-026
Unavailable job inputs retry with backoff without spending the window, and
the outage is audited periodically.

## Amended by ADR-028
Stateless packages have no backup schedule and no scheduler job.

## Context
No scheduler exists. The only periodic mechanisms are the operation runner
wait loop (`internal/operation/runner.go:171`), SSE polling and heartbeats
(`internal/server/operations.go:107-109`), and ACME wait loops. Backup
schedules persist (`internal/backup/schedule.go`) but never fire.
`cert.Check` (`internal/cert/monitor.go:126`) has a caller in
`Wizard.ValidateIssued` (`internal/cert/wizard.go:242`), which runs only when
the owner validates an issuance, but no scheduled caller. Drift scans are
on-demand only.

## Alternatives considered
- systemd timers: split state across units, harder to keep CLI/dashboard
  parity (ADR-004), not inspectable through the API.
- Catch-up of every missed run: can stampede backups after downtime.
- Queue overlapping runs: backlog grows without bound behind a stuck job.
- Scheduling over loopback HTTP: needless coupling and, after ADR-020,
  needs a credential.

## Consequences
- Schedules are only as available as the daemon; documented.
- Operations are the single audit trail for scheduled work; skipped runs
  are audited too.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
- Addendum (review of S260): skip-and-audit means a hung operation
  blocks its job indefinitely. The skip audit entry and scheduler status
  name the blocking operation, and cancelling it (until a cancel route
  exists, restarting the daemon, whose startup recovery cancels live
  operations) resumes the job at its next window. When the previous
  run's state cannot be read, the scheduler fails closed and skips. Skips
  audit with outcome `skipped`. The window is spent before enqueue, and
  a live run whose id was never saved is found by its payload, so a
  persistence fault cannot start a second concurrent run.
