# S257 — Enforce Operator Sessions

## Goal
Flip the session middleware to enforcing for every route at once and update all clients in the same commit, completing the ADR-020 rollout.

## Dependencies
S256

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-020, Accepted 2026-10-11.
Merged scope: this slice absorbs the earlier "gate read routes" slice (previously S257) and the earlier "gate mutating routes" enforcement. Enforcement must be atomic: a half-gated API would leave main unrunnable for the dashboard or acceptance script.

## Acceptance Criteria
- Enforce Operator Sessions implemented and documented
- Middleware flips from report-only to enforcing: every route not on the public allowlist returns 401 without a valid session, covering mutating and read routes alike (config, config import and export, firewall, exposure, tunnel, edge, updates, catalog including `POST /api/v1/catalog/{name}/drift`, storage, all `/api/v1/backups/*`, audit, operations, doctor, docker, dockge, drift reads)
- Pre-claim surface enforced (amends ADR-015): before claim only the allowlist and claim-wizard saves (`GET`/`PUT`/`DELETE /api/v1/wizards/{id}`) are reachable; wizard routes require a session once claim completes; a test proves `handleConfigApply` and the rest reject on an unclaimed appliance
- SSE event streams (`/api/v1/operations/{id}/events`) validate the cookie or bearer session at connect time
- Inline session checks in `handleLogout`, `handleChangePassword`, and `handleLockoutStatus` are replaced by the middleware
- Rejected requests are audited rate-limited and aggregated, without tokens or cookies
- In the same commit: dashboard, `homesteadctl`, and `scripts/acceptance.sh` authenticate (acceptance logs in with the claim and first-admin flow, then uses the session), and `make acceptance` steps still pass
- Route-classification test has no remaining pending-enforcement entries; only the ADR-020 allowlist and claim-window wizard routes are reachable unauthenticated
- Tests cover 401 and 200 for each route group, CSRF rejection, expired and revoked sessions, and the unclaimed appliance
- `docs/PARITY.md` and operations docs describe the final boundary
- Remove the "Authenticated operator API" entry from `docs/DEFERRED.md` when landed
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
