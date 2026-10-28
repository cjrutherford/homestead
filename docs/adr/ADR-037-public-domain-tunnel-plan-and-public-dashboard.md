# Public Domain, Planned Tunnel, and the Public Dashboard behind Authentik

## Status
Accepted

In part superseded by ADR-044: apps and Authentik are served under one host by path.

The owner decided this on 2026-10-20, from the setup walkthrough findings F8 and F14 to F17.

This record **amends** two earlier records:
- **ADR-017 (tunnel remote access):** the tunnel becomes a planned, approved and audited change that also manages its own DNS records.
- **The project principle "Management remains local/overlay by default"** (`AGENTS.md`): the dashboard may be published, but only through the tunnel and only behind Authentik sign-in.

Nothing else in ADR-006, ADR-008, ADR-020, ADR-033, ADR-035 or ADR-036 changes unless this record says so. Related: ADR-009 (DNS provider abstraction) and ADR-021 (secret store).

## Decision

### 1. The domain names public names only (S294)
- The setup Domain step gains a **domain** field, for example `example.com`.
  - It is stored as a non-sensitive setup choice, next to the provider and access choices.
  - It is set through `PUT /api/v1/setup/steps/domain`, `homesteadctl setup step domain --domain` and the step form (ADR-004).
- The domain is used only for **public** names: `<name>.example.com`, reached through the tunnel or a port forward.
- Local names (`*.lan`, `homestead.local`, ADR-035) do not change, and there is no split DNS.
- Refused as the domain: local-only suffixes (`.lan`, `.local`, `.home.arpa`, `.internal`, `.localhost`) and names without a dot.

### 2. One Cloudflare token (S294)
- The Cloudflare API token stored in the Domain step (`dns_cloudflare_api_token`, ADR-021) is the only Cloudflare credential. The tunnel reuses it.
  - The tunnel API, CLI and form no longer ask for a token.
  - The old separate `router_tunnel_api_token` is read only as a fallback for appliances set up before S294.
- The token needs **Zone · DNS · Edit** and **Account · Cloudflare Tunnel · Edit**. The Domain step names both permissions, and the permission copy test keeps that copy in step with the calls actually made.
- The **account ID is derived, never asked for.** The tunnel's public hostname is matched to the longest Cloudflare zone the token can see that contains it, and the account is that zone's account.
  - The account ID is stored with the tunnel in SQLite, for status and teardown.
  - It is never shown in a plan, an API response, audit entries or logs.

### 3. The tunnel is planned, approved and audited, and owns its DNS (S294)
- **Plan:** `POST /api/v1/tunnel/plan` reads Cloudflare and changes nothing. It returns:
  - the tunnel name;
  - each public hostname and where it routes;
  - the zone;
  - the DNS changes: one proxied CNAME per hostname to `<tunnel-id>.cfargotunnel.com`, each marked *create*, or *replace* when records already exist for that name (the existing records are listed);
  - warnings;
  - a digest.
- **Approve:** `POST /api/v1/tunnel/setup` carries the plan's digest.
  - The daemon plans again and refuses with 409 if anything changed since the review, so the owner approves exactly what they saw.
  - The approval is audited (`tunnel.setup`), and the change runs as an operation.
  - The DNS step deletes the listed records it replaces and creates the CNAMEs.
- **Ownership:** each record Homestead creates is recorded (zone, record ID, hostname, tunnel).
- **Teardown** (`tunnel.teardown`, audited):
  - Deletes only those recorded records, and only while they still point at that tunnel. A record the owner has since changed is left alone and reported.
  - Never deletes a record Homestead did not create.
  - Records that were replaced at setup are not restored. The plan says so.

### 4. The public dashboard is behind Authentik (S296)
The owner chose this over "edge, apps only". It replaces "management remains local/overlay by default" with:

> Management is local/overlay by default; the only public management path is the dashboard through the tunnel, behind Authentik sign-in, planned and approved by the owner.

- **The tunnel's default plan names two hostnames:**
  - the dashboard, `homestead.<domain>` by default;
  - Authentik, `auth.<domain>` by default.

  Remote Authentik sign-in needs Authentik reachable from outside. The owner may rename either hostname in the plan.
- **No dashboard name until sign-in is linked.** While Authentik sign-in is not set up and linked to the owner (ADR-033), the plan routes no dashboard hostname. The tunnel and its other names can still be created. The plan explains why, and the dashboard name is offered again once sign-in is linked. No management surface reaches the internet without Authentik.
- **Authentik only on the public name.** On the public dashboard hostname, the daemon accepts only Authentik sign-in:
  - Password login, claim, recovery and every other way of creating a session are refused there.
  - The local owner password stays the break-glass path on the local names and loopback (ADR-033).
  - Sessions remain the ADR-020 sessions. The Authentik-only rule decides only how a session may be created on that host.
- **SSO re-plan.** Adding the public dashboard name re-plans Sign-in with its origin and redirect URI, which the owner approves (ADR-033). The public `auth.<domain>` name joins Authentik's allowed hosts through the same plan.
- **Routing:**
  - The connector routes these names to the edge (Caddy), not to the daemon's TCP port.
  - The dashboard route accepts the public name next to its local names (ADR-035's `Host` and `Origin` rules are extended to it).
  - The ADR-035 refusal of *public exposure* for the dashboard route stays for router port forwards. The tunnel is the only public path.
- This builds part of S290 (several hostnames to the edge). S290's own app routes still follow it.

### Implementation note (S296)
The tunnel no longer takes a service: every public name goes to the edge's loopback tunnel entry (`127.0.0.1:8480`), which routes it by Host. On the public dashboard name the daemon refuses every session-creating route except SSO login and callback, and every step-up request, with 403 `public-host`. Sign-in records the public origins and Authentik's public address; a browser at a public origin is sent to that address, while the token exchange stays local.

## Amended by ADR-046
When sign-in is configured and the Authentik API token is kept, the
tunnel plan carries the sign-in change and its approval applies it: the
separate "SSO re-plan" above is needed only without a kept token.
ADR-046 holds the substance.

## Amended by ADR-038
Teardown puts back the records setup replaced, unless the name holds a
record again; ADR-038 holds the substance.

## Context
Walking setup from scratch on the KVM VM (2026-10-20) showed:
- the Domain step stored credentials without the domain they serve;
- the tunnel asked again for the same Cloudflare token and for the account ID;
- the tunnel never created its DNS record, so the hostname did not resolve;
- the tunnel's placeholder service published the management dashboard with password login and no exposure plan.

The owner wants to reach the dashboard from outside the home, but only through Authentik.

## Alternatives considered
- **Edge, apps only:** the dashboard never public. The owner chose remote management.
- **Edge plus an opt-in dashboard:** the owner chose the dashboard as the default, behind Authentik.
- **Password and Authentik both allowed on the public name:** this puts the owner password on the internet. Rejected.
- **Cloudflare Access in front of the dashboard:** this ties remote management to one vendor's service. Rejected (ownership rule).
- **A forward-auth gate (an Authentik proxy outpost in Caddy):** heavier, a second Authentik provider, and the dashboard already speaks OIDC. Rejected for v1.
- **A domain that also names local apps through split DNS:** the owner kept the local names unchanged.
- **Asking for the account ID:** the token and the zone already identify it.

## Amended by ADR-049
On a public dashboard name, the People step-up routes accept a fresh Authentik sign-in instead of the local password, and the sign-in plan sets up Authentik's recovery flow; ADR-049 holds the substance.

## Consequences
- The amended principle is recorded in `AGENTS.md`.
- New routes:
  - `POST /api/v1/tunnel/plan` (session class), with PARITY rows;
  - `tunnel setup` takes a digest;
  - teardown needs no account ID.
- The tunnel code needs the Cloudflare zone and DNS record calls, and the permission copy changes with them.
- A store migration adds the tunnel's account and zone and a table of the DNS records Homestead created.
- S296 must test that every session-creating route refuses on the public host except the Authentik callback, and that the dashboard name is absent from the plan until sign-in is linked.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
