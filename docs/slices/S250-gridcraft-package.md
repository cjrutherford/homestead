# S250 — Gridcraft Package

## Goal
Gridcraft (clean-room spreadsheet) as a first-party catalog package,
following the S249 static-container pattern.

## Dependencies
S249

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

## Acceptance Criteria
- Gridcraft Package implemented and documented
- Static-container packaging per ADR-018 on its own internal host
- Generic install, configure, remove, backup, and update flows
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
