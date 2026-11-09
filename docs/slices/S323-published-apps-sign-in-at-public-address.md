# S323 — Published apps sign in at Authentik's public address

## Goal
Checked on VM 2 (2026-10-27): apps on the tunnel send every browser to
Authentik's local address (`https://homestead.lan/apps/auth/...`), which
does not resolve off the home network. So Authentik sign-in on a
published app fails.

## Owner decisions (2026-10-27)
- Published apps use Authentik's public address, and the box answers that
  name itself, so the apps' token calls stay local (ADR-047).
- The 443 site's certificate comes from Let's Encrypt through the existing
  ACME dns-01 flow (S278), not from the appliance root.

## Scope
- Edge: while Authentik's path is published and a public certificate
  covering the dashboard's first tunnel name is placed, the dashboard
  route's snippet gains a 443 site for that name.
  - The site serves only `/apps/auth/`; every other path answers 404.
  - Without such a certificate nothing is rendered, and Caddy never asks a
    CA for the name. A local leaf never serves it.
  - Issuance plans count the dashboard route as served by a certificate
    covering that name, so issuing or renewing it refreshes the snippet.
- App sign-in: an app with a public name signs in at
  `https://<public dashboard name>/apps/auth/application/o/<slug>/` once
  the edge serves that site.
  - An unpublished app keeps the local issuer.
  - A published app, while the site is not served yet, keeps the local
    issuer, and its plan warns that tunnel visitors cannot sign in, and
    says to add the certificate in TLS.
- The override's `extra_hosts` entry follows the issuer's host. A plan
  that moves the issuer (`replaced_issuer`) drops the old host entry, and
  its changes name the move.
- Re-planning is not folded into the tunnel or publish plans. When the
  issuer the app should have changes, its sign-in status reports a
  waiting re-plan (the existing `replan`), which the owner approves.

## Rules
AGENTS.md slice rules apply. ADR-047 is written and accepted first. No
routes, CLI commands or dashboard surfaces change, so PARITY is unchanged.

## Acceptance Criteria
- Docker tests:
  - the 443 site appears only with a public certificate and a published
    Authentik path, carries only Authentik, and is reported as served;
  - issuing for the name refreshes the dashboard snippet.
- App sign-in tests:
  - the issuer is chosen by publication and by whether the site is served;
  - the warning names what is missing;
  - a re-plan names the issuer move, and status asks for it;
  - applying swaps the override's host entry, and turning off removes it.
- A server test covers how the public address is gathered.
- All Go, Python and dashboard checks pass.

## Live check
Deploy to VM 2. Approvals are the owner's:
1. Plan, approve and issue a DNS-01 certificate for the public dashboard
   name in TLS.
2. Re-plan and approve each published app's sign-in.

Then, from inside the nextcloud container, discovery at the app's issuer
must show public endpoints and an issuer that matches.
