# S247 — Dashboard Exposure Surfaces

## Goal
Tunnel-first public-exposure flows in the dashboard: review, approve,
and apply exposure plans including tunnel setup and status, assuming no
router access with router mapping kept as fallback.

## Dependencies
S245,S246

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

## Acceptance Criteria
- Dashboard Exposure Surfaces implemented and documented
- Exposure review, approval, and apply reachable from the dashboard with CLI/API parity (ADR-004)
- Tunnel-first guidance when the router is silent; router path retained
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
