# S253 — Media Craft Packages

## Goal
Photocraft, filmcraft, and soundcraft as first-party catalog packages,
following the S249 static-container pattern with media caveats called
out.

## Dependencies
S252

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

## Acceptance Criteria
- Media Craft Packages implemented and documented
- WebCodecs, GPU, and audio-hardware requirements validated and warned per package; hard requirements refuse with reasons
- Generic install, configure, remove, backup, and update flows
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
