# Authentik forward-auth for Navidrome and Uptime Kuma

## Status
Accepted

Owner direction, 2026-10-31: every app with user accounts signs in through
Authentik. Navidrome 0.64.2 and Uptime Kuma 2.5.7 have no OIDC, so forward-auth
is reopened **for these two apps only**. Every other app keeps app-level OIDC
(ADR-040, ADR-052).

Supersedes, scoped to these two apps, the "no forward-auth" exclusions in
ADR-041 §1 ("No forward-auth"), ADR-041 Alternatives (public apps), ADR-043
Alternatives ("Edge forward-auth in front of each app") and ADR-037
Alternatives ("A forward-auth gate ... Rejected for v1"). Those texts stay in
force for every other app and for the dashboard (which keeps OIDC, ADR-033).
Amends ADR-054: the "verified Homestead integration" for these two apps is the
gate described here. The owner's decisions of 2026-10-31 are recorded in
the sections below and in "Owner decisions". Everything still marked **Spike**
is not verified and is settled by step 0 of S418 (a spike on VM 2), before any
other code; if a spike answer breaks a decision here, the ADR is amended first.

## Context
Facts found in the repository:
- The edge is Caddy, rendered per route by `internal/docker/routes.go`
  (`renderRoute`, `renderPathHandles`, `renderTunnelEntry`). The dashboard site
  carries path apps (ADR-044); other apps have a site of their own. Headers sent
  upstream are replaced with `header_up` so the daemon can trust them (ADR-035).
- Authentik is served at `/apps/auth/` (`AUTHENTIK_WEB__PATH` in
  `catalog/authentik.yaml`, container port 9000, Authentik 2026.8.3).
- `internal/sso/authentik.go` only creates OAuth2 providers, applications,
  groups, bindings and scope mappings. There is **no** proxy provider or
  outpost code anywhere in `internal/`.
- Access is three groups per app: `homestead-<pkg>-users`, `-admins` and the
  shared `homestead-owner` (`sso.AppGroups`, `appsso.buildAccess`); the Authentik
  application is bound to all three (ADR-043). ADR-054 makes the install review
  a per-person none/user/admin decision, and refuses to claim protection the
  integration cannot enforce.
- Navidrome (`catalog/navidrome.yaml`) and Uptime Kuma
  (`catalog/uptime-kuma.yaml`) are `sso.class: unsupported` and **host-served**:
  they have no `serve: path`, so they answer on `<app>.lan` / `<app>.local` and,
  if published, `<app>.<zone>`. Kuma is local-only (`allowwider: false`) and
  cannot be published. Kuma cannot run under a sub-path at all; Navidrome could
  (`ND_BASEURL`) but this ADR does not move it.
- Published apps sign in at Authentik's public address, whose back channel stays
  on the box (ADR-047, S408), and path apps always do.

Upstream facts (checked 2026-10-31):
- Authentik Caddy forward-auth: `reverse_proxy /outpost.goauthentik.io/*
  <outpost>` plus `forward_auth <outpost> { uri /outpost.goauthentik.io/auth/caddy;
  copy_headers X-Authentik-Username X-Authentik-Groups X-Authentik-Email
  X-Authentik-Name X-Authentik-Uid ... }`, inside a `route` block so the order
  holds; header capitalisation matters; the embedded outpost may be the same
  host as Authentik. https://docs.goauthentik.io/add-secure-apps/providers/proxy/server_caddy/
- Single-application mode matches "Unauthenticated Paths" regexes against the
  request **path** (domain mode matches the full URL); sign-out is
  `/outpost.goauthentik.io/sign_out` on the app's host.
  https://docs.goauthentik.io/add-secure-apps/providers/proxy/
- Navidrome 0.64 names the options `ExtAuth.UserHeader` /
  `ND_EXTAUTH_USERHEADER` (default `Remote-User`) and
  `ExtAuth.TrustedSources` / `ND_EXTAUTH_TRUSTEDSOURCES` (CIDRs, empty denies
  all, `@` for a unix socket). The owner's notes used the older
  `ReverseProxy*` names; use the `ExtAuth` ones.
  https://www.navidrome.org/docs/usage/configuration/options/
- An unknown header user is created automatically with a random password; the
  **first** user authenticated through the proxy becomes admin; nothing else can
  make a user admin. Proxy auth falls back to normal auth when the header is
  absent or the source is untrusted. For Subsonic clients the docs say to bypass
  proxy auth for `/rest/`, so users use a Navidrome password in their client
  (this conflicts with `EnableUserEditing=false`). The proxy must strip a
  client-sent user header and must not set it on bypassed `/share/` and
  `/rest/` paths. https://www.navidrome.org/docs/getting-started/extauth-quickstart
- Authentik's own Uptime Kuma guide uses a proxy provider with unauthenticated
  paths `^/status(/.*)?$ ^/status-page$ ^/assets/.* ^/api/push/.* ^/api/badge/.*
  ^/api/status-page/.* ^/icon.svg$ ^/upload/.*`, tells owners to press Settings >
  Security > Disable Auth, and to set Trust Proxy when Kuma is reachable only
  through the outpost. https://integrations.goauthentik.io/monitoring/uptime-kuma/
  Uptime Kuma issue #4628 reports that disabling auth also removed the API-token
  requirement on `/metrics`. https://github.com/louislam/uptime-kuma/issues/4628

## Decision
### 1. Edge mechanics
A catalog app may declare `sso.forwardauth` (new, beside `sso.oidc`; exactly one
of the two). When its gate is on, the app's site block is rendered, inside one
`route`, as:
1. `request_header -X-Authentik-*` and `-Remote-User` and the app's header name,
   for every request, first: clients never supply identity (§5).
2. One `handle` per bypass path (§4): `reverse_proxy` to the app with the
   identity headers still stripped. No `forward_auth`.
3. `reverse_proxy /outpost.goauthentik.io/*` to Authentik's embedded outpost,
   with the request Host preserved (Caddy's default), so the outpost picks the
   provider by host.
4. `forward_auth` to the outpost, `uri /outpost.goauthentik.io/auth/caddy`,
   `copy_headers` of exactly the headers the app needs (Navidrome:
   `X-Authentik-Username` only; Kuma: none), then the normal `reverse_proxy`
   (WebSockets included, §10).
The same shape is rendered on the LAN site, the `.local` alias and the tunnel
entry. The gate is a new `Gate` field on `docker.Route`, set only by the
sign-in operation, so route tables, previews and digests carry it.

**Verified on VM 2 (2026-10-31):** Authentik's embedded outpost answers at the
**root** path `/outpost.goauthentik.io/` on the `authentik-server` container
(port 9000, inside the Docker network): `/outpost.goauthentik.io/ping` returns
204. It does **not** answer under `/apps/auth/outpost.goauthentik.io/`, which
falls through to the Authentik UI (404), even though
`AUTHENTIK_WEB__PATH=/apps/auth/`. `/outpost.goauthentik.io/auth/caddy`
returned 404 for an unknown host (no provider yet), as expected. So:
- `forward_auth` points at
  `authentik-server:9000`, `uri /outpost.goauthentik.io/auth/caddy`, over the
  internal network (not through the public or path address);
- every gated app host (site, alias, tunnel entry) gets a
  `handle /outpost.goauthentik.io/*` proxied to `authentik-server:9000`, Host
  preserved so the outpost selects the provider by host.
**Spike:** the edge can resolve and reach `authentik-server:9000` (it already
proxies the Authentik path app, so it should share that network).

### 2. Authentik objects (new, per app, owned and named by Homestead)
- Proxy provider `Homestead: <Title>` (`mode: forward_single`) with
  `external_host` = the app's canonical sign-in address, the default
  authorization flow, the shared invalidation flow, `cookie_domain` unset (the
  cookie is host-only on the app's host; it never needs to span hosts because
  Authentik lives at a different host/path), "Unauthenticated Paths" = the app's
  bypass regexes, `intercept_header_auth: false`, `basic_auth_enabled: false`.
- Application `Homestead: <Title>`, slug `homestead-<pkg>`, bound to the
  Authentik groups of §3 by the existing binding code.
- The provider is added to the **embedded outpost**
  (`/api/v3/outposts/instances/?managed=goauthentik.io/outposts/embedded`, PATCH
  `providers`, keeping the other providers it already lists). Removing sign-in
  removes the provider from the outpost first, then deletes application,
  provider and groups.
- These use the same API token flow, one-time token, plan digest and
  idempotent re-plan as ADR-040/052 (`internal/sso/authentik.go` gains a proxy
  provider kind and outpost membership; the OAuth2 code is untouched).
- **One external host per provider.** Authentik matches a request to a provider
  by its `external_host`, and the apps have up to three names. While the gate is
  on, the app answers only on one canonical name: the public name when
  published (Navidrome), otherwise `<app>.lan`; the `.local` alias and any other
  name answer with a 308 to it. Turning the gate on or off, publishing or
  unpublishing, re-plans it (same rule as the issuer move, ADR-047/048 §6).
  Owner decision 2026-10-31: accepted. The canonical name is the public name if
  published, otherwise `<app>.lan`; every other name redirects (308) to it.
  **Spike:** whether one provider can carry several hosts is no longer needed
  to ship; it is recorded only if it turns out to be free.

### 3. Access roles
- The gate is bound to the existing groups. **Kuma: admin-only.** Only
  `homestead-uptime-kuma-admins` and `homestead-owner` are bound; the users
  group is not created. ADR-054's install review offers none or admin for it.
- **Navidrome: user or admin means may reach it.** Both groups are bound, so
  "none" is enforced by Authentik. The admin *role inside* Navidrome cannot be
  mapped from a header (upstream: only the first proxy user becomes admin), so
  `RoleMapping.Mapped` is false and the plan says admins are chosen in
  Navidrome's own user list. The installer states this instead of claiming an
  admin selection (ADR-054).
- **Header identity.** The header carries the Authentik `username`. Navidrome
  matches by username. A person whose username equals an existing Navidrome local
  account would sign in as that account (impersonation of the local admin by a
  same-named Authentik user). The plan lists Navidrome's existing usernames and
  refuses (not warns) on a collision with an active person, and refuses when no
  administrator exists yet, because then the first person through the gate would
  become admin. Owner decision 2026-10-31: admins are chosen inside Navidrome,
  and the refusals stand. The owner creates the Navidrome administrator first
  (its own setup screen); Homestead has no first-run routine for it.
- Changing a person's role or state re-plans bindings as today (ADR-043, ADR-054).
  A disabled person loses the gate at Authentik; their Navidrome account stays
  (and still works for Subsonic clients with its own password; the plan says so).

### 4. Bypass paths and why each is safe
Owner decision 2026-10-31. Matched on the request path, anchored regexes, no
prefix globs wider than needed. On every bypass path the identity headers are
stripped first (§5), so a bypass can only authenticate with the app's own
credentials or with an unguessable link or token.

**Navidrome**
- `^/rest/.*`: the Subsonic API. Native clients cannot do a browser login. It
  keeps Navidrome's own authentication (user plus app password or token), which
  is the posture today. Safe because the header is stripped, so it can only
  authenticate with those credentials. Cost: an app password must be set by
  each person in Navidrome (`EnableUserEditing` stays true); Subsonic
  brute-force protection is Navidrome's own.
- `^/share/.*`: public share links an owner makes on purpose, so they must work
  without an account. In Navidrome 0.64 the share routes are, from the
  upstream router and **to be confirmed by the spike** (open a share link in a
  browser and in a player with the network log on): `/share/{id}` (the share
  page), `/share/s/{id}` (audio stream), `/share/img/{id}` (cover art) and the
  download variant under the same `/share/` prefix. All sit under `/share/`,
  so the one regex covers them. Safe because each id is an unguessable token
  the owner created and can delete; the endpoints serve only the shared items;
  the header is stripped. **Spike:** the share page's own JS and CSS load from
  `/app/` (the SPA bundle). If the log shows they do, add the narrowest
  anchored static prefix (for example `^/app/(assets|static)/.*`) as a third
  bypass: those files hold no user data. If a share needs anything else, it is
  listed here before code.

**Uptime Kuma**
- `^/api/push/.*`: push monitors, called by cron jobs and other apps with a
  secret token in the URL. Safe: the token is the credential, the endpoint
  only records a heartbeat.
- Public status pages and what they need to render, because the owner wants
  them open: `^/status/.*` (the page), `^/api/status-page/.*` (page JSON and
  heartbeats), `^/api/badge/.*` (badges), `^/assets/.*` (the SPA bundle),
  `^/upload/.*` (uploaded logos and images) and `^/icon\.svg$` (and the
  favicon if the spike sees it requested). Safe: they show only the monitors
  the owner chose to put on a status page and the data Kuma itself publishes
  for them; none accepts input that changes state, and they were already
  designed to be anonymous. The cost is that status pages are open to the LAN
  without Authentik; Kuma stays unpublishable (`allowwider: false`), so they
  are never open to the internet.
- **socket.io is not bypassed.** Kuma's status pages are plain HTTP (they do
  not open a socket; the SPA skips the socket on `/status/*` pages), so no
  bypass is needed. The spike confirms this in the network log. Bypassing the
  socket would hand the admin UI to anyone, because Kuma's login is turned off
  behind the gate (§6).
- **`/metrics` and `/dashboard` stay gated, and are never in the bypass list.**
  Kuma issue #4628: with `disableAuth` on, `/metrics` also loses its API-token
  check, so the gate is the only protection it has. `/status-page` (the owner's
  management list, which needs the socket) and `/manage-status-page` are also
  gated; Authentik's own guide bypasses `^/status-page$`, which this ADR does
  not copy.
- Nothing else is bypassed. The Authentik routes are not touched.

### 5. Header trust boundary
- Caddy deletes every client-supplied identity header (`X-Authentik-*`,
  `Remote-User`, `X-Forwarded-User`) before any other directive, on all sites and
  the tunnel entry, then `forward_auth` sets them from the outpost's answer
  (`copy_headers` replaces, never appends).
- Navidrome gets `ND_EXTAUTH_USERHEADER=X-Authentik-Username` and
  `ND_EXTAUTH_TRUSTEDSOURCES` set to the edge's address on the shared network
  only (a `/32` where Compose can pin it, otherwise the edge network's CIDR). A
  request that reaches Navidrome from anywhere else falls back to Navidrome's
  login, so spoofing needs a second container on the edge network. The catalog's
  container keeps no published host port (as today), so nothing on the LAN
  reaches it directly. **Open:** whether the edge network holds only edge and
  routed app containers, and whether the edge's address can be fixed.
- Kuma has no header; it trusts nothing from the gate. **Residual risk, accepted with the owner's choice to turn Kuma's login off:** every routed app container shares the edge network, so a compromised app container could reach Kuma's port directly, without the gate or a login. Kuma's own login (sign-in off) closes it; a dedicated edge-to-Kuma network is the deferred hardening. **Precondition (verified
  in `catalog/uptime-kuma.yaml` 2026-10-31):** the manifest publishes no host
  port, only the container's 3001 on the Docker network, and no other service
  shares it. Because Kuma's own login is off behind the gate (§6), the edge
  must be the only way in; the plan refuses if the app's Compose publishes a
  port, and a pin test fails if the manifest ever adds one. **Spike:** the
  edge network holds only the edge and routed containers.
- The trust settings are written in the app's Compose through the existing env
  and secretfile mechanisms (ADR-052), so they come and go with sign-in.

### 6. Local and recovery login
- **Navidrome:** the login form stays. It is the Subsonic path's login anyway,
  and the recovery route if Authentik is down: remove `ND_EXTAUTH_TRUSTEDSOURCES`
  (or turn sign-in off) and the normal login works with the local accounts.
  Local accounts keep working through `/rest/` at all times.
- **Kuma: its own login is turned OFF while gated** (owner decision
  2026-10-31, reversing this ADR's earlier recommendation to keep it). One
  login: Authentik. Homestead sets Kuma's `disableAuth` setting when the gate
  turns on and restores it when the gate turns off. Consequences the plan
  states: anyone who passes the gate is Kuma's administrator (so only the
  admins and owner groups are bound, §3); a stale Authentik session is a Kuma
  session; `/metrics` loses its token check (issue #4628) and therefore stays
  behind the gate; and Kuma's port must be unreachable except through the edge
  (§5 precondition).
  - **Where:** Kuma 2.5.7 stores settings in its database, table `setting`
    (`key`, `value`, `type`), row `key='disableAuth'`, `value` the JSON text
    `true` / `false`. The manifest sets no database environment and mounts only
    `/app/data`, so the database is SQLite at `/app/data/kuma.db`. Kuma 2 can
    instead use MariaDB (chosen at its setup screen, recorded in
    `/app/data/db-config.json`); the guard reads that file and the script
    **refuses** unless the type is `sqlite` (or the file is absent). MariaDB is
    DEFERRED. These facts are from the Kuma source and the settings model;
    **Spike** confirms the table, the exact stored value format and the cache
    on the real container.
  - **Pattern (ADR-052, S415-S417):** `onapply`/`onoff` script run in the app
    container with a version guard (the app's version marker must equal the
    verified version), a schema guard (the `setting` table with `key`, `value`
    and `type` columns exists; the `disableAuth` row is updated if present and
    inserted if not), one `BEGIN IMMEDIATE` transaction with a busy timeout,
    idempotent, and the pin test `kumaSignInVerified = "2.5.7"` (fails on a
    manifest bump so the script is re-evaluated). The script writes no secret
    and the SQL carries none. **Spike:** which SQLite driver the image offers
    (no `sqlite3` shell is assumed; Kuma ships `@louislam/sqlite3` and Node).
  - **Restart:** Kuma caches settings in memory for about 60 seconds and reads
    `disableAuth` on each socket connection, so after the write the container is
    restarted (`restart: true`) so the new value is certain, not eventually
    true. **Spike** measures whether a restart is strictly needed.
  - **Order (never open without either a gate or a login):** on = the gate
    first (rendered, reloaded, probed), then disable Kuma auth, then restart.
    Off = restore Kuma auth first (write `disableAuth` false, restart, confirm),
    then remove the gate, then the Authentik objects. If the restore fails,
    the gate stays on and the operation stops (ADR-048 stop-on-failure). An
    interrupted on or off is finished by the next run, in the same order.
  - **No administrator yet:** Kuma's first-run setup (create the admin) has not
    been done when the `user` table is empty. With `disableAuth` on and no user
    Kuma has nobody to act as, and its setup screen is not reachable (the
    socket asks for setup only while `needSetup`). The plan therefore
    **refuses** until an administrator exists (the script checks the `user`
    table for a row; the plan reads it the same way). The owner creates the
    account first, the same precondition as Navidrome's admin (§3). The
    refusal text says so. **Spike** confirms what Kuma does with no user.
  - **Trust Proxy** (client addresses in Kuma) is still a manual step and
    DEFERRED.
  - Break-glass: turn sign-in off with the normal plan, which restores the login
    first. If Authentik and the daemon are both unavailable, the documented
    manual step is the `disableAuth` row set back to `false` in
    `/app/data/kuma.db` (stop the container, edit, start).
- The Homestead recovery model (ADR-052 break-glass) is unchanged: turn
  sign-in off with the normal plan, or remove the gate by hand as documented in
  the slice.

### 7. Plan, preview and audit
- Same surface: `homesteadctl apps sign-in plan|apply [--off] <app>` and
  `/api/v1/catalog/{name}/sign-in/*`. The plan is the existing digest-bound,
  previewed, audited `app.sign-in` operation; for these apps it lists: the proxy
  provider, application, groups, bindings, the outpost membership, the Caddy
  gate (rendered diff of the site block), the canonical name and redirects, the
  bypass paths with reasons, the Navidrome usernames found, and the warnings.
  No new route, command or screen, so `docs/PARITY.md` keeps its rows; the
  response gains fields and the dashboard card shows them from the same JSON.
- Applying order: Authentik objects first (so the outpost knows the provider),
  then Compose env, then the Caddy gate with a reload and a probe, then verify.
  Turning off removes the gate first, then Authentik objects. Kuma adds the
  `disableAuth` write: after the gate on, and before the gate off (§6).
- The Authentik token never lands in argv, logs, Compose or env (unchanged).

### 8. Public versus local addresses
Decision (owner, 2026-10-31, consistent with ADR-047/S408): the embedded
outpost's `authentik_host` (where the browser is redirected to sign in) is the
**public Authentik address once it answers**, otherwise the local one
(`https://homestead.lan/apps/auth/`). The outpost's back channel to Authentik
is local (`authentik-server:9000`). The outpost has one `authentik_host`, so
all gated apps share it. **Consequence, stated in the plan:** a LAN-only app's
(Kuma's) sign-in then needs the public Authentik name to resolve and answer
from the LAN, exactly as path apps already do; with no internet the redirect
fails and the app stays unreachable (fail closed, §9). When the public address
starts answering, the issuer-move re-plan (ADR-048 §6) also re-sets
`authentik_host`.
**Spike:** how to set `authentik_host` on the embedded outpost (the outpost
instance's `config` through `/api/v3/outposts/instances/`, or the managed
outpost's settings), and whether that conflicts with anything Homestead already
sets (a repository search finds no current writer of `authentik_host` or the
outpost; the spike checks the live value). If a single value proves
unworkable, the fallback is the second, non-embedded outpost container, which
needs a new ADR note first.

### 9. When Authentik is down
Fail closed. `forward_auth` gets no answer, so Caddy returns 502/403 and the app
is unreachable by browser. `/rest/*` and `/share/*` (Navidrome) and `/api/push/*` plus
status pages (Kuma) keep working because they bypass the gate. Recovery is local and documented: turn
sign-in off (renders the plain route; the Authentik objects are left to the
next re-plan) or run the daemon-free manual step. Doctor gains a check that the
outpost answers when a gate is on. No silent fail-open.

### 10. WebSockets
Kuma's UI is socket.io. `forward_auth` authenticates the upgrade request like
any HTTP request (the cookie is sent), and Caddy proxies the upgraded
connection. An open socket is not re-authenticated until it reconnects. The gate
does not need a bypass for `/socket.io/`. Verified live on the VM in S419; for
Kuma the gate is the only login, so a socket upgrade without a session must be
refused (acceptance test).

### 11. Turning sign-in off
`--off` for Navidrome removes the gate from the route (the site returns to a plain
`reverse_proxy`, and the other-name redirects go), removes the Navidrome trust
env, then removes the provider from the outpost, deletes application, provider
and the app's two groups (the shared owner group stays), exactly the order and
rules of ADR-043/052. The apps are open on the LAN again, as before. The plan
for turning off says so. For Kuma the order differs (§6): Kuma's login is restored first, then the
gate, then the Authentik objects. A re-plan finds an interrupted off and
finishes it.

## Consequences
- A new proxy-provider and outpost layer is the only new Authentik surface; it
  is used by two apps. The gate costs one Caddy round trip per request.
- Kuma has one login (Authentik) while gated; Navidrome needs an app password in
  native clients.
- Kuma is open to anyone who passes the gate and its port must stay
  unpublished; its status pages are open on the LAN.
- Each of these apps answers on one canonical name while gated.
- Authentik becomes a hard dependency for browser access to the two apps.
- Catalog schema, `Route`, the edge renderer and the plan gain gate concepts;
  tests must cover the strip, bypass and fail-closed rules.
- Slices: S418 (engine plus Navidrome) and S419 (Uptime Kuma). Each is one
  commit.

## Alternatives considered
- **Domain-level forward-auth (one provider for all hosts):** one cookie domain
  across `navidrome.<zone>` works, but the LAN names share no domain and it
  cannot apply per-app group policy. Single-application mode is the one Authentik
  recommends for per-app policy.
- **Gate in front of every public app:** still rejected (ADR-041): it breaks
  the native clients of Jellyfin, Immich and Nextcloud.
- **Keep Kuma's login as a second factor:** this ADR first recommended it
  (two logins, no change to Kuma); the owner chose a single login instead.
- **Navidrome without a gate, keeping local accounts (status quo):** rejected by
  the owner's decision that every account app uses Authentik.
- **Navidrome `/rest/*` behind the gate:** breaks every native Subsonic client.
- **An Authentik proxy outpost container per app:** more containers; kept as the
  fallback only for §8.
- **Moving Navidrome to a path:** possible with `ND_BASEURL`, but a path
  would put it on the dashboard's shared origin (ADR-044) and Kuma cannot move.

## Owner decisions (2026-10-31)
1. One canonical name per gated app: accepted.
2. Kuma's own login off while gated, with a reversible, ordered write (§6).
3. Navidrome admins are chosen inside Navidrome; refuse with no admin or on a
   username collision.
4. Open paths: Navidrome `/rest/*` and `/share/*`; Kuma `/api/push/*` and
   public status pages with their assets (§4); `/metrics` and `/dashboard`
   stay gated.
5. `authentik_host` is the public Authentik address when it answers, otherwise
   the local one; LAN-only sign-in needs the public name to resolve (§8).

## Open questions
None for the owner. Remaining unknowns are spike items in S418 step 0 (the
share page's static assets, `authentik_host` setting, the edge reaching
`authentik-server:9000`, Navidrome's trusted source address) and S419 (Kuma's
settings cache, driver, no-user behaviour).
