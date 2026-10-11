# S255 — Operator Session Boundary

## Goal
Deny-by-default session middleware on the TCP listener with a code-declared public allowlist, per ADR-020.

## Dependencies
S200,S020

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
ADR-020 is Proposed and must be Accepted before implementation begins.

## Acceptance Criteria
- Operator Session Boundary implemented and documented
- Session middleware wraps the mux in `internal/server` and rejects requests without a valid session using `internal/auth` with 401
- Public allowlist declared in code: `GET /healthz`, `GET /version`, `GET /api/v1/identity`, `POST /api/v1/claim`, `POST /api/v1/admins/setup`, `POST /api/v1/sessions`
- Root-only Unix socket path stays exempt as the recovery path; `cmd/homestead-server/main.go` wiring keeps both listeners working
- Route-classification test enumerates every registered route and fails on any not explicitly classified (public, session, or pending-gating with owning slice)
- Inline session checks in `handleChangePassword` and `handleLockoutStatus` replaced by the middleware
- Rejected requests are audited without logging tokens
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
