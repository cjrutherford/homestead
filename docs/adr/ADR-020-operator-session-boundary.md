# Operator Session Boundary

## Status
Accepted

Accepted by the owner on 2026-10-11.

## Decision
The TCP listener of `homestead-server` becomes deny-by-default: a single
session middleware wraps the mux and rejects any request without a valid
operator session with 401. The only exceptions are an explicit public
allowlist declared in code (below). A route-classification test enumerates
every route registered on the TCP mux and fails when one is neither on the
allowlist nor covered by the middleware, so a new route cannot ship
unclassified. The dashboard's static catch-all (`/`, `internal/server/dashboard.go`)
is classified public so the login page can load; it serves no API data.

### Transport
Browser and CLI share one session store and one session model (ADR-004):

- Browser: an `HttpOnly`, `SameSite=Strict` session cookie, `Secure` whenever
  the request arrived over TLS. A cookie is required because the dashboard
  consumes SSE through `EventSource`
  (`dashboard/src/app/core/api.service.ts:444`), which cannot set an
  `Authorization` header. Every mutating request (POST, PUT, PATCH, DELETE)
  authenticated by cookie additionally requires a CSRF check: a
  per-session token delivered at login and echoed in an `X-CSRF-Token`
  header, compared in constant time. Requests authenticated by bearer
  header are not subject to the CSRF check because browsers do not attach
  that header ambiently.
- CLI: `Authorization: Bearer <token>`, minted by `POST /api/v1/sessions`.
  `homesteadctl` stores the token at `~/.config/homestead/session` with
  mode 0600 (directory 0700) and reuses it until it expires or is revoked.

### Lifetime and revocation
A session expires after 12 hours idle or 7 days absolute, whichever comes
first. Sessions are revoked on logout (`DELETE /api/v1/sessions`) and on
password change (all of the administrator's other sessions). This replaces
today's flat 30-day `SessionTTL` (`internal/auth/auth.go:31`) and the
"single-use token" wording in `internal/apiclient/auth.go:19`, which is
inaccurate: the token is reusable until expiry. S256 owns correcting both.

### Public allowlist and pre-claim surface
Allowlist: `GET /healthz`, `GET /version`, `GET /api/v1/identity`,
`POST /api/v1/claim`, `POST /api/v1/admins/setup`, `POST /api/v1/sessions`,
and the static dashboard catch-all.

This decision amends ADR-015. Before the appliance is claimed, only
health, version, identity, claim, and first-admin setup are reachable,
plus claim-wizard saves (`/api/v1/wizards/{id}`, which the dashboard calls
before login at `dashboard/src/app/claim/claim.ts:141`). Wizard routes are
reachable unauthenticated **only until claim completes**; after that they
require a session like everything else. Every other route is rejected before
claim regardless of method.

Today no handler checks claim state (for example `handleConfigApply` at
`internal/server/config.go:90` applies configuration on an unclaimed
appliance), so ADR-015 is already violated before claim. S255 reports and
S257 enforces the corrected pre-claim surface.

### Rollout
Staged so that each slice leaves main runnable:

1. S255 adds the middleware in **report-only** mode: it logs and audits the
   rejections it would have made but never rejects. Would-be-rejection
   audit entries are rate-limited and aggregated (one entry per route and
   reason per minute, with a count) to avoid flooding the audit log.
2. S256 adds the session transport (cookie, CSRF, bearer, TTL, revocation,
   CLI token storage, dashboard login), still report-only.
3. S257 flips the middleware to **enforcing** for all routes at once, in
   the same commit that updates the dashboard, the CLI, and
   `scripts/acceptance.sh`, so main never has a half-gated API.

### First-admin setup issues a session
`POST /api/v1/admins/setup` stays on the public allowlist but, on success,
also logs the new owner in (S257): the response sets the session cookies
and returns the token fields `POST /api/v1/sessions` returns. This is a
transport detail, not a widening of the allowlist: setup remains
claim-gated and one-shot, and it is what lets the claim wizard (whose
saves are claim-window routes that close when claim completes) and the
CLI continue authenticated without a second login.

### Audit of rejected requests
Once enforcing, rejected requests are audited without logging tokens or
cookies, and are rate-limited and aggregated like the report-only entries.

The root-only Unix socket (`POST /api/v1/admins/recover`,
`internal/server/recover.go:19`) remains the privileged recovery path and is
not subject to session checks; filesystem permissions are its boundary.

## Context
There is no auth middleware today. `handleLogout`, `handleChangePassword`
and `handleLockoutStatus` in `internal/server/auth.go` check a session
inline; nothing else does. The TCP mux has 58 registrations (57
`HandleFunc` plus the dashboard catch-all `Handle("/")`, verified by
`TestEveryRegisteredRouteIsClassified`, which enumerates them from the
registration helper; a raw grep over-counts to 65 because it includes
socket, test, and comment matches;
`recover.go:19` is socket-only), nearly all unauthenticated, including
`PUT /api/v1/config` and `POST /api/v1/config/import`, which accepts secret
values (`internal/server/configio.go`). Anyone who can reach the management
port can reconfigure the appliance, which contradicts ADR-015 and ADR-016
once an administrator exists.

## Alternatives considered
- Per-handler inline checks: what exists today; easy to forget on a new
  route and impossible to audit as a whole.
- Allow-by-default with a deny list: fails open when a route is added.
- Bearer-only for the browser: breaks `EventSource` SSE, or forces tokens
  into URLs where they leak into logs.
- Network-only protection (bind to overlay/loopback): remains the default
  per ADR-006 but is not an identity boundary; defense in depth is needed.
- External auth proxy: introduces a runtime dependency and lock-in.

## Consequences
- Rollout is staged (report-only, then transport, then atomic enforcement);
  the classification test records every route as public, claim-window, or
  session (S257 removed the pending-enforcement state).
- `apiclient` and the dashboard must send credentials and handle 401.
- Unclaimed or admin-less appliances keep working through the allowlist
  and the claim-wizard exception only.
- Cookie-authenticated mutations need the CSRF header; the dashboard
  must send it on every mutating call.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
