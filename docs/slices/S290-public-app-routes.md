# S290 — Public routes for apps on the tunnel

## Goal
Let the owner give an installed app a public address on the Cloudflare tunnel from the dashboard or CLI (for example Authentik at `auth.<domain>`, so dashboard sign-in through Authentik works from the public URL), and let Homestead adopt a tunnel that was set up outside it.

## Dependencies
S289

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Owner direction 2026-10-19: there must be a way to define the public route in the app.

Today a tunnel carries exactly one hostname. `RenderConnectorConfig` writes a single ingress entry, and `tunnel setup` takes one hostname and service. Public edge routes already exist (S270), refused unless they match an exposure approval. This slice joins the two paths. A public app route is an approved public edge route on Caddy plus a tunnel ingress entry that sends the hostname to Caddy on loopback with the original Host, plus the proxied DNS record. Before code, check ADR-017 and ADR-006; amend or supersede by ADR if multi-hostname ingress or routing through Caddy changes the model.

## Acceptance Criteria
- Add and remove a public route for an installed app (hostname, app, which of its edge routes) through the API, CLI and dashboard (ADR-004 parity).
  - Adding one goes through plan, preview and approve. The preview shows the hostname, the app, the DNS record and the connector change, and the change is audited.
  - Removing one takes the ingress entry, the edge route and the DNS record out together.
  - It's refused when there's no tunnel, no approval, a duplicate hostname, or the app isn't installed.
- The connector config carries several ingress entries and keeps the 404 catch-all. It's rewritten atomically, and the connector is restarted only when the config changed.
- The proxied CNAME for the hostname is created and removed through the existing DNS provider path. This closes the "Tunnel DNS route actuation" entry in `docs/DEFERRED.md`.
- Adopt an existing tunnel: given the tunnel ID and the Cloudflare credentials (file or stdin, never argv), Homestead verifies the tunnel and that the connector serves it, records its routes, and from then on manages it. This closes the S288 "no adopt command" and pre-S246 adoption entries.
- After Authentik gets a public route, the SSO defaults offer the public Authentik address.
- `docs/PARITY.md` rows, `routeClasses` entries, `docs/DEFERRED.md` updates.
- Tests for each behaviour (Go with a fake Cloudflare API, CLI, dashboard specs). The parity and route classification tests are green.
- KVM on VM 2:
  1. Adopt the hand-made `homestead-vm2` tunnel.
  2. Add `auth.hopefulaspirationsindustries.com` for Authentik, plan, approve and apply SSO with the public addresses, then sign in with Authentik from the public URL.
  3. Remove the route and check that the CNAME and ingress entry are gone.

  Never touch the tunnels `homestead` or `media-tunnel`. Record the transcript in `docs/DEVELOPMENT.md`.
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- ADR amendment if needed; tunnel and exposure operations docs.
- `docs/PARITY.md`, `docs/DEFERRED.md`, `docs/DEVELOPMENT.md`.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state. One slice, one commit on main.
