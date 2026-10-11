# S274 — Dashboard Account Surfaces

## Goal
Dashboard surfaces for lockout status and password change.

## Dependencies
S239,S248,S256

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Dashboard slices follow the S248 design system (tokens, StateMessage) and update `docs/PARITY.md`.
Split from S269. Depends on S256 because password change revokes the administrator's other sessions (ADR-020).

## Acceptance Criteria
- Dashboard Account Surfaces implemented and documented
- Angular routes for lockout status (`GET /api/v1/admins/lockout`) and password change (`POST /api/v1/admins/password`) using S248 tokens and `StateMessage`; the UI explains that other sessions are signed out on change
- `docs/PARITY.md` rows for the corresponding routes flip from `n/a`
- Password forms never echo or store secrets in client state beyond submission
- Component tests cover the primary flows
- Remove the "Rotation and lockout dashboard surfaces" entry from `docs/DEFERRED.md` when landed
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
