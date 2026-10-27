# S315: Apps under one host, by path

## Goal
Implements ADR-044 (read it first; Accepted). After S315, apps and
Authentik are served at `<server>/apps/<service>/`:
- public: `https://homestead.<zone>/apps/nextcloud/` and
  `https://homestead.<zone>/apps/auth/`;
- local: `https://homestead.lan/apps/nextcloud/` and
  `https://homestead.local/apps/nextcloud/`.

Immich keeps a name of its own. Every public name is the zone apex or one
label below it, so Cloudflare's free certificate covers it and no wildcard
is needed.

Depends on S312, S313 and S314.

## Scope
- **Packages:**
  - The catalog schema gains how an app is served: `serve: path|host`,
    `path` (default `/apps/<name>/`, under the reserved `/apps/` prefix)
    and `strip_prefix`.
  - The packages pass the path to each app:
    - Nextcloud: `OVERWRITEWEBROOT` (with `OVERWRITEHOST` or the CLI URL as
      needed), plus trusted domains for `homestead.lan`, `homestead.local`
      and the public host;
    - Jellyfin: Base URL;
    - Authentik: `AUTHENTIK_WEB__PATH=/apps/auth/`;
    - Immich: `serve: host`;
    - craft apps: check each one, and record any that cannot run under a
      path.
  - Follow the existing procedure for package changes. Never touch
    `catalog/craft/craft-manifest.yaml`.
- **Edge:**
  - Route records carry an optional path.
  - The appliance's host blocks (`homestead.lan`, `homestead.local`, the
    public dashboard name) carry the dashboard at `/` plus a `handle` for
    each path app, with the prefix stripped or kept per the package.
  - A path never shadows `/api/` or the dashboard's assets.
  - Nextcloud's `/.well-known/caldav` and `/.well-known/carddav` redirect
    into its path.
  - On the public host, an unpublished app's path answers 404.
  - Tests cover the generated Caddy config.
- **Public exposure:**
  - Publishing a path app is still a digest-bound plan, previewed and
    approved. It shows the path and the public URL and adds no DNS
    record. Publishing makes the path answer on the public host;
    unpublishing removes it.
  - Host apps (Immich) keep the ADR-041 CNAME flow.
  - The S312 coverage check still runs. With the defaults it is always
    covered.
- **Authentik:**
  - The issuer and endpoints move under `/apps/auth/`.
  - Dashboard sign-in (ADR-033), app sign-in (ADR-040, including the back
    channel through the edge on the host) and the public dashboard login
    (ADR-037) plan from it.
  - The tunnel/domain setup no longer proposes `auth.<domain>`.
- **Install wizard and CLI (S313 surfaces):**
  - The address step shows `https://homestead.lan/apps/<name>/`,
    `https://homestead.local/apps/<name>/` and, when publishing,
    `https://homestead.<zone>/apps/<name>/`. Host apps show their own
    names as today.
  - The resolver note applies to `homestead.lan`.
  - The reach check tests the path URLs.
  - The CLI prints the same.
- **Shared origin:** the Tunnel or Domain page states the ADR-044 trade-off
  once, in plain words.
- **Existing installs:**
  - Apps on old host names keep them.
  - Manage offers "Move to <server>/apps/<name>/" as a plan the owner
    approves, re-planning sign-in where the issuer changed.
  - No silent migration.
- **Docs:**
  - INSTALL.md: addresses, tunnel/domain, the Authentik URL and
    troubleshooting.
  - PARITY for any surface change, DEFERRED for anything left out, and the
    ADR-044 cross-references.

## Rules
- AGENTS.md slice rules apply.
- Lamplight design language and the lamplight spec apply.
- Planning, digest binding and approval are never weakened.

## Acceptance Criteria
- Go tests cover:
  - the Caddy config for path apps (strip and keep), the reserved prefix,
    and no shadowing of `/api/`;
  - the `.well-known` redirects;
  - 404 for an unpublished app on the public host;
  - publish and unpublish plans and their digests;
  - the Authentik issuer and redirect URIs under the path;
  - each package's rendered path settings;
  - the reach check URLs;
  - the existing host-name install move plan.
- Dashboard specs cover the address step for path and host apps, the
  shared-origin note, and the Move offer.
- Go, Python and dashboard tests pass; build and embed succeed; PARITY is
  updated.
- Screenshots of the address, review and done steps and the Tunnel page in
  dark, desktop and 375px, reviewed by looking at them.
- A live check on VM 2 (done by the lead, not the implementer):
  - `homestead.<zone>/apps/auth/` and `/apps/nextcloud/` load over the
    tunnel;
  - Nextcloud's Authentik sign-in works;
  - the `homestead.lan` paths work locally.
