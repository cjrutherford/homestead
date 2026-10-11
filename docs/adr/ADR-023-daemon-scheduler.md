# Daemon Scheduler

## Status
Proposed

## Decision
`homestead-server` gets one in-process scheduler. It never executes work
itself: when a schedule is due it enqueues an operation through the
existing operation runner, so scheduled runs are visible, streamed, and
audited exactly like owner-initiated ones. Last-run state is stored in
SQLite. Missed-run policy: if a window was missed while the daemon was
down, run once at startup; there is no burst catch-up. No systemd timers
or cron are introduced, and internal callers invoke services directly
rather than going through HTTP.

## Context
No scheduler exists. The only periodic mechanisms are the operation runner
poll (`internal/operation/runner.go:171`), SSE heartbeats, and ACME waits.
Backup schedules persist (`internal/backup/schedule.go`) but never fire,
and `cert.Check` (`internal/cert/monitor.go:126`) has no caller. Drift
scans are on-demand only.

## Alternatives considered
- systemd timers: split state across units, harder to keep CLI/dashboard
  parity (ADR-004), not inspectable through the API.
- Catch-up of every missed run: can stampede backups after downtime.
- Scheduling over loopback HTTP: needless coupling and, after ADR-020,
  needs a credential.

## Consequences
- Schedules are only as available as the daemon; documented.
- Operations are the single audit trail for scheduled work.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
