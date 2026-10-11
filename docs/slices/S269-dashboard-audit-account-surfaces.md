# S269 — Dashboard Audit and Account Surfaces

## Goal
Dashboard surfaces for audit, lockout status, password change, and the Dockge link.

## Dependencies
S239,S248

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Dashboard slices follow the S248 design system (tokens, StateMessage) and update `docs/PARITY.md`.

## Acceptance Criteria
- Dashboard Audit and Account Surfaces implemented and documented
- Angular routes for audit log, lockout status, password change, and the Dockge advanced-management link using S248 tokens and `StateMessage`
- `docs/PARITY.md` rows for the corresponding routes flip from `n/a`
- Password forms never echo or store secrets in client state beyond submission
- Component tests cover the primary flows
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
