# Apps Under One Host, by Path

## Status
Accepted

Accepted by the owner on 2026-10-26. Owner decisions:
- Apps are served at `<server>/apps/<service>`. `<server>` is the
  appliance's own name: `homestead.<zone>` in public, and `homestead.lan`
  and `homestead.local` locally.
- Authentik is served the same way, at `<server>/apps/auth/`.
- Immich cannot run under a path, so it keeps a name of its own:
  `immich.<zone>` in public, and `immich.lan` and `immich.local` locally.
- No wildcard certificate is needed. Every public name is the zone apex or
  one label below it, which Cloudflare's free Universal SSL covers.
- The owner accepts the shared-origin risk described under Consequences.

Supersedes:
- the per-app public names of ADR-041 (`<app>.<domain>`), except for apps
  that cannot run under a path;
- ADR-037's default Authentik name `auth.<domain>`;
- ADR-035's per-app local names, except for those same apps.

S290 and S313's planning, digest binding and approval rules stay.
ADR-041's tunnel adoption is unchanged.

## Context
The demo uses `homestead.<zone>` as the appliance's public name. Under
ADR-041 and ADR-037, apps and Authentik then got names two labels deep
(`nextcloud.homestead.<zone>`, `auth.homestead.<zone>`). Cloudflare's free
certificate does not cover those names, so they failed the TLS handshake
(S312). Fixing that needs Advanced Certificate Manager. The owner would
rather keep one name and serve the apps under it by path.

App support for this, checked on 2026-10-26:
- **Nextcloud** runs under a path with `overwritewebroot`. The `.well-known`
  CalDAV and CardDAV links at the host root must redirect into its path.
- **Jellyfin** has a supported Base URL setting.
- **Authentik** has `AUTHENTIK_WEB__PATH`, which needs a leading and a
  trailing slash. Everything in Authentik, including the OIDC endpoints,
  is served under that path.
- **Immich** does not support a sub-path. Its docs require the root of a
  domain or subdomain, and its maintainers declined base-path support.
- **Craft apps** run under a path already, or are checked per package.

## Decision
### 1. The app's place in its package
Each catalog package declares how it is served:
- `serve: path`, with a `path` (default `/apps/<name>/`) and `strip_prefix`
  (true or false), for apps whose base-path setting keeps the prefix or
  needs it stripped;
- or `serve: host`, for apps that need a host of their own (Immich).

The package passes the path to the app's own setting:
- Nextcloud: `overwritewebroot`;
- Jellyfin: Base URL;
- Authentik: `AUTHENTIK_WEB__PATH`.

An app's path is the same on every name it answers to, because each of
these settings is global to the app.

### 2. Edge routing
The edge keeps one site block per host. The appliance's hosts
(`homestead.lan`, `homestead.local` and the public name) carry the
dashboard at `/` and one `handle` per installed path app, at its path.
The routes table records path routes beside host routes. A path never
shadows the dashboard's own paths (`/api/`, the dashboard assets); the
reserved prefix is `/apps/`.

For Nextcloud, `/.well-known/caldav` and `/.well-known/carddav` on the
appliance host redirect into its path.

### 3. Public exposure stays planned and approved
Publishing a path app makes its path answer on the public host through the
existing tunnel ingress. It does not add a DNS record, but it is still an
exposure plan with a digest, previewed and approved (ADR-041 rules). It
lists the path and the public URL. Until an app is published, its path
answers only on the local hosts: the public host refuses `/apps/<name>/`
for unpublished apps with 404.

Publishing a host app (Immich) is unchanged from ADR-041: a CNAME under the
zone, one label deep.

### 4. Authentik
Authentik's issuer, authorize and token URLs move under `<server>/apps/auth/`.
- Dashboard sign-in (ADR-033) and app sign-in (ADR-040) plan their
  redirect URIs and issuer from it.
- The back channel (ADR-040) goes through the edge on the host, using the
  appliance's local host and the path.
- ADR-037's public login follows: the public dashboard redirects to
  `homestead.<zone>/apps/auth/`, which is reachable without a dashboard
  session.

### 5. Existing installs
An app installed under ADR-035/041 names keeps them until the owner
re-plans. Its Manage panel offers to move it to its path, as a plan the
owner approves. The same applies to sign-in, whose issuer changes. There is
no silent migration.

## Alternatives considered
- `apps.<zone>/<service>`: keeps the apps off the dashboard's origin at the
  same cost. Declined by the owner.
- `auth.<zone>` for Authentik: isolates the sign-in session from the apps.
  Declined by the owner.
- Advanced Certificate Manager with `*.homestead.<zone>`: about $10 a month,
  and it keeps per-app origins. Not wanted for the demo; still supported by
  the S312 coverage check for owners who choose host names.

## Consequences
- **Shared origin (accepted risk).** The dashboard, Authentik and every
  path app share one browser origin. A script flaw (XSS) in any app can
  read and act on the dashboard and Authentik with the signed-in owner's
  session. The usual same-origin protections (cookies, CSRF tokens) do not
  separate them. Defences that remain:
  - step-up re-authentication on risky dashboard actions;
  - Authentik's own sign-in for each app;
  - the plan and approval digests, which a script cannot approve without
    step-up where step-up applies.
  
  The dashboard shows this trade-off once, on the Tunnel or Domain page,
  in plain words. The owner may choose host names per app later; this ADR
  does not forbid them.
- The local names `<app>.lan` and `<app>.local` go away for path apps. The
  address step (S313) shows `https://homestead.lan/apps/<name>/`.
- `.well-known` handling for Nextcloud moves to the appliance host.
- The S312 depth check stays. With defaults, every name is covered by
  Universal SSL.
- Slice: S315 (paths, edge, packages, Authentik, public plans, the address
  step and docs). People moves to S317, after the Doctor screen (S316).
- Implemented in S315: the package fields `serve`, `path` and
  `strip_prefix` (Nextcloud strips the prefix and builds links from
  OVERWRITEWEBROOT; Jellyfin's Base URL and Authentik's
  AUTHENTIK_WEB__PATH serve under it, so the edge keeps it; Immich and the
  craft apps stay `serve: host`, amended by ADR-045: the craft apps are
  served by path since S320); path routes inside the dashboard site
  and the tunnel entry; the move offer for existing installs
  (`/api/v1/catalog/{name}/move`, `apps move`). See `docs/PARITY.md` and
  the S315 section of `docs/DEFERRED.md`. Supersedes in part ADR-035,
  ADR-037 and ADR-041; ADR-043's groups and bindings are unchanged.
