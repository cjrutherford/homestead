# S275 — Backup Restore Runbook

## Goal
A runbook for restoring encrypted backups with the plain `age` CLI and standard tools, proven by an automated test, per ADR-022.

## Dependencies
S271

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-022, Accepted 2026-10-11.
Split from S271.

## Acceptance Criteria
- Backup Restore Runbook implemented and documented
- Runbook in `docs/` shows restoring a backup without Homestead using the plain `age` CLI, an exported recovery identity or owner identity, and standard tools
- An automated test follows the runbook commands literally using the real `age` binary (not the Go library); the test is skipped with a clear message only when `age` is absent locally, and the acceptance environment installs `age`
- Remove the "Backup encryption at rest" entry from `docs/DEFERRED.md` when landed
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
