# S256 — Session Transport

## Goal
Cookie, CSRF, bearer, lifetime, revocation, CLI token storage, and dashboard login, all still report-only, per ADR-020.

## Dependencies
S255

Replaces the earlier "gate mutating routes" S256 draft; enforcement moved to S257.

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-020, Accepted 2026-10-11.
Still report-only: the middleware does not reject. This slice makes every client able to authenticate so that S257 can flip enforcement without breaking main.

## Acceptance Criteria
- Session Transport implemented and documented
- `POST /api/v1/sessions` sets an `HttpOnly`, `SameSite=Strict` session cookie, `Secure` when the request arrived over TLS, and still returns the bearer token in the body for the CLI; both resolve to one session store (ADR-004)
- CSRF check on cookie-authenticated mutating requests (POST, PUT, PATCH, DELETE) using a per-session token delivered at login and sent as `X-CSRF-Token`, constant-time compared; bearer-authenticated requests are exempt; in report-only mode a missing or wrong token is reported, not rejected
- Session lifetime 12h idle and 7d absolute, replacing the flat 30-day `SessionTTL` (`internal/auth/auth.go:31`); idle time refreshes on use
- Sessions are revoked on logout (`DELETE /api/v1/sessions`) and on password change (all other sessions of that administrator)
- The "single-use token" wording at `internal/apiclient/auth.go:19` is corrected to reusable-until-expiry or revoked, and any code relying on the old reading is updated
- `homesteadctl login` and `logout` store, reuse, and delete the token at `~/.config/homestead/session` with mode 0600 (directory 0700); `internal/apiclient` sends `Authorization: Bearer`
- Dashboard login uses the cookie transport, sends `X-CSRF-Token` on every mutating call, keeps `EventSource` SSE working with the cookie (`dashboard/src/app/core/api.service.ts:444`), and routes to login on 401
- Rejection-report audit entries stay rate-limited; no token, cookie, or CSRF value is logged
- `docs/PARITY.md` updated for every added or changed route (`tests/test_s037_parity.py` stays green) with a note on the session requirement
- Tests cover cookie and bearer login, CSRF accept and reject, idle and absolute expiry with an injected clock, revocation on logout and password change, and CLI token file mode
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
