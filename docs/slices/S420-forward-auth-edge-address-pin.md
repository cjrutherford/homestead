# S420: Forward-auth trusts only the edge container's address

## Goal
Close the S418 finding from VM 2: Navidrome's `ND_EXTAUTH_TRUSTEDSOURCES` was the
whole `homestead-edge` subnet, which contains the bridge gateway the host reaches
containers from, so any local user could forge `X-Authentik-Username`. Trust the
edge container's own IPv4 address as a `/32` (ADR-055 §5 amendment, 2026-11-01).

## Dependencies
S418.

## Scope
- `edgeSourceAddr` replaces the subnet lookup: `docker inspect homestead-caddy`,
  the container's IPv4 on `homestead-edge`, as `<ip>/32`.
- The plan is refused when the edge container is not running or has no IPv4
  address on that network.
- Drift: the gate revision covers the trusted source, so a recreated edge
  (new address) makes sign-in status report re-plan needed. Doctor adds a warning
  per gated app, "Re-plan and apply sign-in for <app>". Navidrome fails safe
  meanwhile (own login). No auto-rewire.
- Out of scope: pinning the edge address in Compose; Kuma (no header trust).

## Acceptance
- The plan's `ND_EXTAUTH_TRUSTEDSOURCES` is a single `/32`, never a subnet.
- A stopped or addressless edge refuses the plan with the edge-network message.
- A changed edge address sets `replan` in status; Doctor warns with the guidance.

## Tests
`TestParseEdgeAddr`, `TestEdgeAddrFailureRefusesThePlan`,
`TestForwardStatusReplansWhenEdgeAddressMoves`, existing plan and apply tests
updated to a `/32`.

## Built
`internal/server/forwardauth.go` (`edgeSourceAddr`, `parseEdgeAddr`,
`gatedTrustDrift` added to the `authentik-outpost` probe), tests above, ADR-055
§5 amendment, DEFERRED and SLICE_INDEX lines.

## Verify live
On VM 2 (needs the S418 gate applied for Navidrome):
1. Re-plan and apply Navidrome sign-in; check
   `docker exec navidrome env | grep TRUSTEDSOURCES` shows the edge's `/32`
   (`docker inspect homestead-caddy` for its address on `homestead-edge`).
2. From the host, `curl -H 'X-Authentik-Username: admin' http://<navidrome-ip>:4533/`
   no longer signs in (own login page).
3. `docker compose up -d --force-recreate` the edge; if its address changes,
   status shows re-plan, Doctor warns, and re-applying restores sign-in.
