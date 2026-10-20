# S294 — Domain and tunnel plan

## Goal
Give the Domain step the domain its credentials serve, and make the Cloudflare tunnel a planned, approved and audited change. The tunnel reuses the one Cloudflare token, derives the account, and creates and later removes its own DNS records (ADR-037 §1–§3).

## Dependencies
S293. ADR-037.

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

Out of scope, left for S296:
- the default dashboard and `auth.<domain>` names;
- routing through the edge;
- Authentik-only sign-in on the public name.

The tunnel keeps one owner-entered hostname and service in this slice.

## Acceptance Criteria
- **Domain field:**
  - The setup choice gains `domain`. It is set by `PUT /api/v1/setup/steps/domain {domain}`, `homesteadctl setup step domain --domain NAME` and the Domain step form.
  - Local-only suffixes and dotless names are refused (422).
  - It is reported in `GET /api/v1/setup` choices.
- **Token reuse:** the tunnel reads `dns_cloudflare_api_token`, falling back to `router_tunnel_api_token`. The setup and plan bodies, CLI flags and form no longer carry a token or an account ID.
- **Account:** derived from the longest Cloudflare zone the token can see that contains the hostname. It is stored with the tunnel route and never returned or audited.
- **`POST /api/v1/tunnel/plan {name, hostname, service}`:**
  - It is read-only.
  - It returns the zone, the ingress, the DNS change (a proxied CNAME to the new tunnel's `cfargotunnel.com` name, marked create, or replace with the existing records listed), warnings (including the daemon-listener warning) and a digest.
  - Errors are clear when no token is stored or no visible zone contains the hostname.
- **`POST /api/v1/tunnel/setup {name, hostname, service, digest}`:**
  - It plans again and returns 409 on a digest mismatch.
  - The approval is audited (`tunnel.setup`).
  - The operation creates the tunnel, replaces the listed records, creates the CNAME and records its ID, then starts the connector and records the route.
  - If a later step fails, the operation removes the CNAME it created and deletes the tunnel.
- **`DELETE /api/v1/tunnel/tunnels/{id}`:**
  - It takes no account ID and is audited (`tunnel.teardown`).
  - It deletes only the records Homestead recorded that still point at that tunnel, then the tunnel and the connector.
- **Status** uses the stored account and falls back to the token's single account.
- **CLI:** `homesteadctl tunnel plan`, plus `tunnel setup --digest`. Teardown takes no account flag. The old token and account flags are refused with a message pointing to the Domain step credential.
- **Dashboard:**
  - The tunnel form loses the token, account and placeholder service fields, and gains a review table and Approve.
  - Teardown no longer asks for the account.
  - The Domain step gains the domain field, names both token permissions, and suggests the hostname under the domain.
- Automated tests cover primary behavior against a Cloudflare double, and the permission copy test covers the new calls.
- Main build remains green

## Required Documentation
- `docs/PARITY.md`, `docs/DEFERRED.md`, `docs/DEVELOPMENT.md` and `docs/architecture/SETUP_WALKTHROUGH.md` (Domain step copy).

## Handoff
Next: S295, then S296.
