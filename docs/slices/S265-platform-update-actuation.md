# S265 — Platform Update Actuation

## Goal
Apply a verified platform update: replace the binary, restart via systemd, roll back on failure.

## Dependencies
S264,S194

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

## Acceptance Criteria
- Platform Update Actuation implemented and documented
- Apply runs as an operation: stage verified artifact, keep the previous binary, replace atomically, restart `homestead-server` via systemd
- Post-restart health check; on failure restore the previous binary and restart
- Only artifacts verified by S264 are applied
- API and CLI routes with rows in `docs/PARITY.md`
- Audited with before and after versions
- Tests use a fake systemd and filesystem harness
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
