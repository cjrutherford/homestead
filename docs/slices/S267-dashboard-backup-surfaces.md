# S267 — Dashboard Backup Surfaces

## Goal
Dashboard surfaces for backup destination, schedules, and runs.

## Dependencies
S238,S248

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Dashboard slices follow the S248 design system (tokens, StateMessage) and update `docs/PARITY.md`.

## Acceptance Criteria
- Dashboard Backup Surfaces implemented and documented
- Angular routes cover every `/api/v1/backups/*` endpoint using the S248 tokens and `StateMessage` for empty, loading, and error states
- `docs/PARITY.md` rows for `/api/v1/backups/*` flip from `n/a` to dashboard symbols
- No 375px overflow and 44px targets
- Component tests cover the primary flows
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
