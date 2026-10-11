# S249 — Wordcraft Package

## Goal
Wordcraft (clean-room word processor) as a first-party catalog package:
its own pinned static container on its own internal host.

## Dependencies
S158

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

## Acceptance Criteria
- Wordcraft Package implemented and documented
- Static-container packaging per ADR-018: pinned image, own `.lan` host, correct wasm MIME and cache headers, no state or volumes
- Install, configure, remove, backup, and update flows reuse the generic catalog machinery
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
