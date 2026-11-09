# S302 — Adopted tunnel: proposed public name and zone

## Goal
After adopting a tunnel without a setup domain (S290 live check), an app's
public status had no proposed name, a plan without a hostname was refused,
and a remove plan showed an empty zone. Derive both from the tunnel's zone.

## Dependencies
S290, S294. ADR-041 §2. No ADR: no new exposure path; plans still change
nothing and every change is approved.

## Scope
- Migration 51: `tunnel_routes.zone_name` beside `zone_id`. Tunnel setup or
  adoption and app public-name applies store it; an empty value keeps the
  stored one.
- `GET /api/v1/catalog/{name}/public` and the add plan propose
  `<app>.<domain>` from the setup domain, else the stored zone name.
- The remove plan names the zone: the stored name, else the zone Cloudflare
  finds for the name.

Out of scope: a tunnel adopted before S302 has no stored zone name until the
next applied tunnel or public-name plan; until then name the hostname.

## Acceptance Criteria
- `TestAppPublicAdoptedTunnelZone`: no proposal before a zone name is
  stored, a proposal after the first apply, the remove plan's zone from the
  store and from Cloudflare, and an empty update keeping the name.
- Tunnel setup stores the zone name (`tunnel_test.go`).
- Main build remains green; `docs/PARITY.md` unchanged (no route change).

## Live Check (VM 2, 2026-10-24)
- Migration 51 applied on the adopted `homestead-vm2` tunnel: zone IDs kept,
  zone names empty until the next apply, status unchanged.
- An add plan with a hostname names zone `hopefulaspirationsindustries.com`.
  No plan was applied, to avoid a real DNS change.
