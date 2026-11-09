# Published Apps Sign In at Authentik's Public Address

## Status
Accepted

The owner chose "public address, back channel kept local" on 2026-10-27,
and accepted the Let's Encrypt certificate in §3 the same day. An
appliance-root certificate was described first; it is impossible (see
Context). Amends ADR-040 §1 and ADR-044.

## Context
Checked live on VM 2 (2026-10-27):
- Nextcloud, Jellyfin and Immich have sign-in with the issuer
  `https://homestead.lan/apps/auth/application/o/homestead-<app>/`.
- Their redirect URIs already include the public names.
- Authentik builds the issuer and every endpoint from the Host it is
  asked on. An app fetches discovery from the local name, so it sends
  every browser to `https://homestead.lan/apps/auth/.../authorize/`.
  Off the home network that name does not resolve, so Authentik sign-in
  on a published app fails.
- An app takes one discovery URL, and the issuer must match on every
  call. One app therefore cannot send LAN browsers to one address and
  tunnel browsers to another.
- The edge answers public names only on the loopback tunnel entry (plain
  HTTP, 8480), not on 443.
- The appliance root is name-constrained to `.lan`, `.local`,
  `.home.arpa`, `.internal` and `localhost` (S222, ADR-030). It cannot
  vouch for the owner's public domain, and widening it would let a root
  installed in browsers vouch for a real internet name.

## Decision
1. **Public issuer for published apps.** An app with any public name,
   while Authentik's path is published (ADR-044), gets its issuer from
   Authentik's public address:
   `https://<public dashboard name>/apps/auth/application/o/<slug>/`.

   An app with no public name keeps the local issuer. Publishing or
   unpublishing changes the issuer, so the app's sign-in is re-planned.
   Whether the publish plan carries that change is settled in the
   implementing slice.
2. **The back channel stays on the box.**
   - The edge also serves the public dashboard name on 443. That site
     carries only the Authentik path.
   - The app's `compose.override.yaml` maps that name to `host-gateway`,
     as ADR-040 §1 does for the local name.
   - Token, userinfo and key calls never leave the box, so Cloudflare
     never carries tokens.
3. **The 443 site uses a publicly trusted certificate.** The certificate
   for the public dashboard name comes from the existing owner-approved
   ACME dns-01 flow (S278), using the Cloudflare token from setup. It is
   planned, approved and renewed like any placed certificate. The
   appliance root is not widened.
4. **Browsers sign in through the public name.** LAN browsers do the
   same; no split DNS is added (ADR-037 keeps local names unchanged).
   Authentik sign-in on a published app therefore needs the internet.
   Each app's local admin keeps working offline.

## Alternatives considered
- **Public issuer, back channel through Cloudflare:** simpler, but
  Cloudflare sees tokens, and sign-in also fails when the tunnel is down.
  Not chosen by the owner.
- **Keep the local issuer:** off-network app sign-in stays broken. Not
  chosen by the owner.
- **Certificate from the appliance root:** impossible under its name
  constraint; widening the constraint was rejected (see Context).
- **A second, container-only CA for the public name:** it avoids
  Let's Encrypt, but adds a CA to run and to trust in every app runtime.
  It is the fallback if the owner does not want ACME here.

## Consequences
- The app sign-in planner chooses the issuer by publication. The edge
  gains a 443 site for the public dashboard name, limited to the
  Authentik path.
- The override's host entry follows the issuer's host.
- A certificate plan for the public name is part of the flow, or a
  precondition for it.
- Published apps' Authentik sign-in depends on the internet.
- Changes require a superseding ADR.

## Amendment 2026-10-31 (S407): single-base-URL apps follow publication
An app that takes one base URL (Mealie's `BASE_URL`, Vaultwarden's `DOMAIN`)
builds its sign-in redirect and links from it, so it must hold the address the
browser used. Such settings use the `{{app_url}}` token: `https://<public name>`
once the app is published at a name of its own, the local route address
otherwise. Publishing and unpublishing plan the setting change (previewed,
covered by the digest, run before the sign-in step); installs that are public
at install time render the public address directly. Path-served apps keep the
local base URL.

## Amendment 2026-10-31 (S408): path apps sign in at the public address
The owner asked that every app served by path (`<server>/apps/<svc>/`, ADR-044)
sign in at Authentik's public address. Such an app shares the dashboard's
origin, which is already published, so §1's rule changes for it: once the edge
answers Authentik's public address on this box, a path app's issuer is the
public one whether or not the app itself is published. The back channel stays
local (§2). An app with a name of its own keeps §1 unchanged. Path apps'
Authentik sign-in therefore needs the internet even on the LAN (§4). Applied
apps move through the existing re-plan (sign-in status asks for it) and the
tunnel setup's issuer move (ADR-048 §6).

## Amended by ADR-060
A published app that signs in on only one origin has its LAN and .local names redirect to its public name (ADR-060).
