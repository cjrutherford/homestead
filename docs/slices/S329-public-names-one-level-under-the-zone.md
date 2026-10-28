# S329 — App public names sit one level under the zone

## Goal
ADR-044 and ADR-046. An app with a name of its own (Immich) is proposed
`immich.example.com`, not `immich.homestead.example.com`. Found live:
the setup domain names the dashboard (`homestead.example.com`) and the
Cloudflare zone is `example.com`. Cloudflare's free Universal SSL
certificate covers only the apex and one level, so the two-level name
failed the TLS handshake at the edge and the app was unreachable.

## Scope
- `appPublic.proposedDomain` returns the Cloudflare zone, never the setup
  domain: the tunnel's stored zone, else the zone of S328's setup zone
  resolver. With neither, no name is proposed and the plan asks for a
  hostname or a tunnel setup.
- The public-address plan refuses (422, plain message) an app name more
  than one level below its zone, naming the one-level alternative. The
  dashboard's own name and path apps are unchanged.
- The dashboard's public-address page and `homesteadctl apps public`
  already print the refusal text; no surface changes.

## Rules
AGENTS.md slice rules apply. No new routes, commands or surfaces, so
PARITY is unchanged. No migration.

## Acceptance criteria
- Go tests: setup domain `homestead.example.com` with zone `example.com`
  proposes `immich.example.com` (resolver, then stored zone); a two-level
  name is refused; path apps unchanged. Tests that encoded the two-level
  proposal use one level.
- All Go, Python and dashboard checks pass.

## Live check
Publish Immich on an appliance whose dashboard is `homestead.<zone>`: the
proposal is `immich.<zone>` and it answers over HTTPS.
