# S252 — Deck and Vector Packages

## Goal
Deckcraft (presentations) and vectorcraft (vector design) as first-party
catalog packages, following the S249 static-container pattern.

## Dependencies
S251

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

## Acceptance Criteria
- Deck and Vector Packages implemented and documented
- Both packages on their own internal hosts per ADR-018
- Generic install, configure, remove, backup, and update flows
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
