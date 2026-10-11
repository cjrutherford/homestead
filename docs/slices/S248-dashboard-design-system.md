# S248 — Dashboard Design System

## Goal
Clean-room design-system foundations in the dashboard drawn from the
optimistic-tanuki system: tokens, state components, and responsive rules.

## Dependencies
S247

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

## Acceptance Criteria
- Dashboard Design System implemented and documented
- Token-driven spacing, type, radius, and shadow scales (ADR-019, no vendored Tanuki code)
- Shared empty, loading, and error states across dashboard routes
- Canonical breakpoints with 44px targets and no 375px overflow
- A validation-board route rendering every primitive under each theme
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
