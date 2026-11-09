# S289 — Dashboard on a local network address

## Goal
Let the owner open the dashboard from another device on the home network (for example `https://homestead.lan`) without the Cloudflare tunnel, so the post-claim walkthrough, including Authentik and dashboard sign-in, can be done and tested entirely on the LAN.

## Dependencies
S288

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Owner direction 2026-10-19: the setup process must be testable without the tunnel.

Today the daemon listens on `127.0.0.1:8080` only. Caddy runs on the `homestead-edge` bridge network, where host loopback can't be reached. An edge route that targets `host.docker.internal:8080` therefore never reaches the dashboard, even though `internal/server/sso.go` already treats such a route as a dashboard origin. ADR-032 chose a dedicated unix socket over a bridge-gateway TCP listener for the craft file proxy. This slice needs the same kind of decision for the dashboard and API, so it starts with an ADR.

## Acceptance Criteria
- ADR-035 (Accepted by the owner before code) decides how the edge reaches the dashboard. The recommended option is a dedicated dashboard socket mounted into the Caddy stack, serving the same mux and the same ADR-020 session boundary as the TCP listener. The recovery socket and the craft socket stay unexposed. The ADR also covers:
  - local and overlay exposure only, never public through Caddy (the tunnel stays the public path);
  - forwarded-address trust for rate limiting and audit;
  - the origin and CSRF checks for the new host.
- The owner sets a local dashboard name (default `homestead.lan`, owner may override) through the API, CLI and dashboard (ADR-004 parity). Homestead then writes the edge route with internal-CA TLS (ADR-030) and adds the name to the dashboard origins, the SSO defaults and the walkthrough. A public exposure of that route is refused.
- Requests through the edge get exactly the session, claim-window and public classes they get on loopback. Tests cover each class plus a forged `X-Forwarded-For`.
- The Trust page and docs tell the owner how to point a device at the name (router DNS or a hosts entry) and install the root certificate.
- `docs/PARITY.md` rows, `routeClasses` entries, `docs/DEFERRED.md` updates.
- Tests for each behaviour (Go, CLI, dashboard specs). The parity and route classification tests are green.
- KVM on VM 2, which is bridged onto the LAN at 192.168.1.x (owner direction 2026-10-19). From the workstation's browser, via the LAN address:
  - open the dashboard and sign in;
  - finish Authentik first-run at `https://authentik.lan`;
  - run the Sign-in plan, approve and apply it, then link;
  - sign out, then sign in with Authentik.

  The break-glass local password still works. Record the transcript in `docs/DEVELOPMENT.md`.
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- ADR-035.
- Operations doc: reaching the dashboard on the LAN.
- `docs/PARITY.md`, `docs/DEFERRED.md`, `docs/DEVELOPMENT.md`.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state. One slice, one commit on main (the ADR may land first as its own planning commit).
