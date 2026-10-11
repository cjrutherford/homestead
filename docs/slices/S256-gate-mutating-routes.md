# S256 — Gate Mutating Routes

## Goal
Require a session on every state-changing route and make the CLI and API client carry it.

## Dependencies
S255

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

## Acceptance Criteria
- Gate Mutating Routes implemented and documented
- `PUT /api/v1/config`, `POST /api/v1/config/import`, firewall, exposure, tunnel, edge, updates, and catalog mutations return 401 without a session
- Storage registration and backup schedule routes (`/api/v1/storage/*`, `/api/v1/backups/*`) mutations return 401 without a session
- `internal/apiclient` sends the session token; `homesteadctl` login flow stores and reuses it
- Dashboard auth interceptor sends the token and routes to login on 401
- `tests/test_s037_parity.py` stays green and `docs/PARITY.md` notes the session requirement
- Classification test marks the mutating routes as gated
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
