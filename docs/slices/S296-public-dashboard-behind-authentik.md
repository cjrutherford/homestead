# S296 — Public dashboard behind Authentik

## Goal
Publish the dashboard through the tunnel by default, but only behind Authentik sign-in. Publish Authentik at `auth.<domain>` so remote sign-in works (ADR-037 §4).

## Dependencies
S294, S295. ADR-037, ADR-033, ADR-035.

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

## Acceptance Criteria
- **Default plan:**
  - The tunnel plan proposes `homestead.<domain>` (dashboard) and `auth.<domain>` (Authentik), routed to the edge. The owner may rename either.
  - The dashboard name is left out, with the reason, until Authentik sign-in is linked.
- **Dashboard route:**
  - The edge dashboard route accepts the public name.
  - The socket's `Host` and `Origin` checks accept it.
  - Router public exposure of the dashboard stays refused.
- **Authentik-only sign-in:** on the public dashboard host, every session-creating route except the SSO login and callback is refused (password login, claim, recovery, link ceremony). This is tested route by route.
- **SSO re-plan:** approving the public name re-plans Sign-in with its origin and redirect URI, which the owner approves.
- **Authentik's address:** the public name works as Authentik's address for the browser, and the token exchange stays on the local address. This is verified live on the KVM VM.
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- `docs/PARITY.md`, `docs/DEFERRED.md`, `docs/DEVELOPMENT.md`, `docs/architecture/SETUP_WALKTHROUGH.md`.

## Handoff
Next: S290 app routes on the tunnel, built on S296's edge routing.
