# S260 — Daemon Scheduler

## Goal
In-process scheduler that enqueues due work through the operation runner, per ADR-023.

## Dependencies
S183,S238

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-023, Accepted 2026-10-11.

## Acceptance Criteria
- Daemon Scheduler implemented and documented
- Scheduler in `homestead-server` fires `backup.JobDefinition` schedules from `internal/backup/schedule.go` by enqueuing operations via `internal/operation/runner.go`
- Last-run state persisted in SQLite; a missed window runs once at startup with no burst catch-up
- Overlap policy is skip and audit: the same job never runs concurrently, and a skipped run records an audit entry naming the job, due time, and blocking operation
- No systemd timers; internal callers use services directly, not HTTP
- Injectable clock for tests covering due, missed, overlapping (skipped and audited), and disabled schedules
- Scheduled runs appear in operations and audit like owner-initiated ones
- Registration interface for later periodic jobs (drift, certificates)
- Remove the "Backup job triggering" entry from `docs/DEFERRED.md` when landed
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
