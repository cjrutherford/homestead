# S255 — Operator Session Boundary

## Goal
Session middleware on the TCP listener in report-only mode, a code-declared public allowlist, and a route-classification test, per ADR-020.

## Dependencies
S200,S020

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-020, Accepted 2026-10-11.
Report-only: nothing is rejected in this slice, so existing Go tests that call routes unauthenticated and `scripts/acceptance.sh` keep passing unchanged. Enforcement lands in S257.

## Acceptance Criteria
- Operator Session Boundary implemented and documented
- Session middleware wraps the TCP mux in `internal/server` using `internal/auth`; in report-only mode it logs and audits the rejection it would have made and always passes the request through
- Would-be-rejection audit entries are rate-limited and aggregated (one entry per route and reason per minute with a count) and never contain tokens or cookies
- Public allowlist declared in code: `GET /healthz`, `GET /version`, `GET /api/v1/identity`, `POST /api/v1/claim`, `POST /api/v1/admins/setup`, `POST /api/v1/sessions`, and the dashboard static catch-all `/` (`internal/server/dashboard.go`)
- Pre-claim surface (amends ADR-015) is modelled and reported: before claim only the allowlist plus claim-wizard saves (`/api/v1/wizards/{id}`, used pre-login by `dashboard/src/app/claim/claim.ts:141`) would be acceptable, and wizard routes stop being acceptable once claim completes. Today no handler checks claim state (for example `handleConfigApply`, `internal/server/config.go:90`), so ADR-015 is already violated pre-claim; report-only mode surfaces this and S257 enforces the fix
- Root-only Unix socket path (`POST /api/v1/admins/recover`, `internal/server/recover.go:19`) stays exempt; `cmd/homestead-server/main.go` wiring keeps both listeners working
- Route-classification test enumerates all 58 TCP registrations (57 `HandleFunc` plus the dashboard `Handle("/")`; the socket-only recover route is excluded and asserted separately) and fails on any not classified as public, claim-window, or session; every session route is marked pending-enforcement with owning slice S257
- Classification explicitly covers the routes an earlier draft missed: `PUT` and `DELETE /api/v1/wizards/{id}` (`internal/server/wizards.go:21-22`, claim-window), `POST /api/v1/catalog/{name}/drift` (`internal/server/drift.go:24`, session), and every `/api/v1/backups/*` route including the reads (`internal/server/backup.go:18-22`, session)
- `handleLogout` (`internal/server/auth.go:110`), `handleChangePassword`, and `handleLockoutStatus` keep their inline session checks; S257 replaces them with the middleware
- Existing Go tests that call routes unauthenticated keep passing; `scripts/acceptance.sh` is unaffected
- Pre-claim and wizard-window classifications are covered by tests with an injected claim state
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
