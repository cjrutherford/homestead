# Operator Session Boundary

## Status
Proposed

## Decision
The TCP listener of `homestead-server` becomes deny-by-default: a single
session middleware wraps the mux and rejects any request without a valid
operator session (`Authorization: Bearer <token>`, minted by
`POST /api/v1/sessions`) with 401. The only exceptions are an explicit
public allowlist declared in code: `GET /healthz`, `GET /version`,
`GET /api/v1/identity`, `POST /api/v1/claim`, `POST /api/v1/admins/setup`,
and `POST /api/v1/sessions`. A route-classification test enumerates every
registered route and fails when one is neither on the allowlist nor
covered by the middleware, so a new route cannot ship unclassified.

The root-only Unix socket remains the privileged recovery path and is not
subject to session checks; filesystem permissions are its boundary. The
CLI and the dashboard authenticate with the same session token through the
same API (ADR-004), so there is no CLI-only or dashboard-only privilege.

## Context
There is no auth middleware today. Only `handleChangePassword` and
`handleLockoutStatus` in `internal/server/auth.go` check a session inline.
Roughly 58 TCP routes mounted from `cmd/homestead-server/main.go`
(`internal/server/*.go`) are unauthenticated, including `PUT /api/v1/config`
and `POST /api/v1/config/import`, which accepts secret values
(`internal/server/configio.go`). Anyone who can reach the management port
can reconfigure the appliance, which contradicts ADR-015 and ADR-016 once
an administrator exists.

## Alternatives considered
- Per-handler inline checks: what exists today; easy to forget on a new
  route and impossible to audit as a whole.
- Allow-by-default with a deny list: fails open when a route is added.
- Network-only protection (bind to overlay/loopback): remains the default
  per ADR-006 but is not an identity boundary; defense in depth is needed.
- External auth proxy: introduces a runtime dependency and lock-in.

## Consequences
- Rollout is staged (middleware first, then mutating routes, then reads) so
  each slice leaves main runnable; until the final stage the classification
  test records routes explicitly marked "pending".
- `apiclient` and the dashboard must send the session token and handle 401.
- Unclaimed or admin-less appliances keep working through the allowlist
  setup routes only.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
