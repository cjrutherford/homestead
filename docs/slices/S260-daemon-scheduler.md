# S260 — Daemon Scheduler

## Goal
In-process scheduler that enqueues due work through the operation runner, per ADR-023.

## Dependencies
S183,S238

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
ADR-023 is Proposed and must be Accepted before implementation begins.

## Acceptance Criteria
- Daemon Scheduler implemented and documented
- Scheduler in `homestead-server` fires `backup.JobDefinition` schedules from `internal/backup/schedule.go` by enqueuing operations via `internal/operation/runner.go`
- Last-run state persisted in SQLite; missed window runs once at startup with no burst catch-up
- No systemd timers; internal callers use services directly, not HTTP
- Injectable clock for tests covering due, missed, overlapping, and disabled schedules
- Scheduled runs appear in operations and audit like owner-initiated ones
- Registration interface for later periodic jobs (drift, certificates)
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
