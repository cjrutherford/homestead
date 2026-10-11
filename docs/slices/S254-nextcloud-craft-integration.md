# S254 — Nextcloud Craft Integration

## Goal
Craft apps open and save through Nextcloud file storage, so documents
live in customer-owned storage instead of browser-local state.

## Dependencies
S253

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

## Acceptance Criteria
- Nextcloud Craft Integration implemented and documented
- File-provider wiring between craft packages and the Nextcloud package with sealed credentials
- Documents default to Nextcloud storage with explicit local-only opt-out
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
