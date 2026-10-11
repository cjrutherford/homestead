# S269 — Dashboard Audit and Advanced Surfaces

## Goal
Dashboard surfaces for the audit log and the Dockge advanced-management link.

## Dependencies
S239,S248

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Dashboard slices follow the S248 design system (tokens, StateMessage) and update `docs/PARITY.md`.
Split: account surfaces (lockout status, password change) are S274.

## Acceptance Criteria
- Dashboard Audit and Advanced Surfaces implemented and documented
- Angular routes for the audit log (`GET /api/v1/audit`) with filtering and pagination as the API allows, and the Dockge advanced-management link (`GET /api/v1/dockge`, extending `dashboard/src/app/advanced`), using S248 tokens and `StateMessage`
- `docs/PARITY.md` rows for the corresponding routes flip from `n/a`
- Audit view never renders tokens, cookies, or secret values; entries from ADR-020 aggregation show their counts
- Component tests cover the primary flows
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
