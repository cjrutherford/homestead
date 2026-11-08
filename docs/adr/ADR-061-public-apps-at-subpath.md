# Public apps at a subpath of the dashboard's name

## Status
Accepted

Decided by the owner on 2026-11-08. The owner's answers to the three
questions raised in the draft are recorded under "Owner decisions" at the end.
No code has changed yet; the slices below implement it.

Amends ADR-044 (section 3 and the "existing installs" rule), ADR-047 (the
redirect URIs and issuer of a moved app) and ADR-055 (Navidrome moves to a
path; Uptime Kuma is unchanged). Supersedes ADR-041 for every app that
becomes a subpath app, and the "public app names by plan digest" rule of
S290 for those apps only. Leaves ADR-037, ADR-058 and ADR-060 as written.

## Context
Owner decision, 2026-11-08: an app that can be served at a subpath is
published at the dashboard's public name,
`https://homestead.<domain>/apps/<app>/`, not at its own public subdomain.
An app that cannot run at a subpath keeps its own public name: Immich
(ADR-044), and the single-origin sign-in apps Gitea, Actual Budget, Mealie
and Calibre-Web Automated (`single_origin_sign_in: true`, ADR-060, Accepted
by the owner as written). Existing subdomain public names go away through a
planned, previewed, approved change (AGENTS.md: public exposure must be
planned, previewed and auditable). Management is unchanged (ADR-037: only
the dashboard is public, behind Authentik).

What exists already (read from the code and manifests, 2026-11-08):
- ADR-044 already serves every `serve: path` package at `/apps/<svc>/` on
  `homestead.lan`, `homestead.local` and, once published, the public name.
  `planPathAdd` (`internal/server/apppublic.go`) publishes a path app with
  no DNS change and refuses a name of its own for it. The plan warns that
  the app shares one origin with the dashboard.
- 30 of 46 packages are `serve: path` (14 Everything apps, 13 craft apps,
  Nextcloud, Jellyfin, Authentik). Those are already subpath apps; for them
  this ADR changes nothing except hardening (section 3).
- The other 16 are `serve: host` (the default) and are published as
  `<app>.<zone>`: one CNAME, one tunnel ingress entry, one edge route.
- So the decision changes the public form of host packages only. Of the 16,
  the manifests say five cannot run under a path (Immich, Memos, BookStack,
  Vikunja, Actual Budget), Uptime Kuma cannot (ADR-055) and refuses public
  exposure, five are on their own names by owner decision (four single-origin, plus
  Vaultwarden), and Navidrome (ADR-055 names `ND_BASEURL`) plus five more
  (Audiobookshelf, Komga, paperless-ngx, Linkding, FreshRSS) have an upstream base-path setting that
  no manifest uses yet. That setting is **not verified** here; each is
  verified in its own slice. Memos, the owner's example, cannot run under a
  path according to its manifest, so it keeps `memos.<domain>`.

## Decision
1. **Rule.** A package is published at
   `https://<dashboard public name>/apps/<app>/` when it is `serve: path`.
   A `serve: host` package is published at its own name `<app>.<zone>`
   (one label below the zone, ADR-041). A package never has both. Vaultwarden
   is `host` by owner decision (reason `owner_choice`): a password manager
   should not share the dashboard's origin, even if a subpath would work.
2. **Package declaration.** A manifest states its public form with a new
   field `publish`: `path` (must be `serve: path`), `host` (with a `reason`
   code: `no_subpath`, `own_name_adr044`, `single_origin`, `owner_choice`), or `never`
   (`allowwider: false`). It is derived from today's fields for existing
   packages and validated: `single_origin_sign_in: true` implies `host`;
   `allowwider: false` implies `never`. The fact table below is a test
   fixture, so it cannot drift from the manifests.
3. **Moving a host app to a subpath is a package change**, one app per
   slice, with its sign-in (owner rule 2026-11-01): the app's base-path
   setting is set from the manifest, `serve: path` is declared, and a live
   check proves the app and its Authentik sign-in work at the subpath on the
   LAN and in public before the package is changed. An app that fails the
   check stays on its own name and is recorded in `docs/DEFERRED.md`.
4. **Existing installs are moved, never silently.** An installed app whose
   package becomes `serve: path` keeps its names until the owner approves
   one **move plan** (ADR-044's Manage "move", extended). If the app is
   published at `<app>.<zone>`, the plan has these steps, in one digest, in
   one operation: (a) move the app's local route to its path (existing);
   (b) publish the path on the dashboard's public name (no DNS record);
   (c) set the app's own address setting (`app_url`) to the path URL;
   (d) re-plan its sign-in (issuer and redirect URIs, section 5);
   (e) remove the old public name: tunnel ingress entry, edge route, proxied
   CNAME (the existing remove steps of S290/S300); (f) re-point read links
   (ADR-058) if the app has them. The preview lists every URL that stops
   working and the new URL. A failure stops the run and reports; it never
   half-removes the old name before the new one answers (publish and verify
   first, remove last).
5. **No redirects.** Removal is clean (owner decision 2026-11-08). The CNAME,
   tunnel ingress entry and edge route go away, so the old name stops
   resolving and nothing serves a redirect. The plan preview says plainly
   that **old bookmarks and links on the removed name stop working**, and
   that mobile and desktop clients with a saved server URL (Subsonic and
   Komga clients, for example) must be changed by hand.
6. **Doctor and preview.** Doctor reports: an installed app whose package is
   `serve: path` but still publishes a subdomain ("move available"); a public
   name with no matching approved plan; and, for a subpath app, that the
   dashboard has a public name. The plan preview shows, for a subpath app, the
   URL, "no DNS record changes", the shared-origin warning (section 3), and
   for removed names the CNAME, ingress entry and URLs that go away, with the
   plain statement that old bookmarks and links stop working.
7. **Parity.** The move is the existing
   app-public plan/approve API (`/api/v1/apps/<name>/public/...`) with new
   plan fields; the CLI (`homesteadctl apps public ...`, `apps move`) and
   dashboard call the same routes (ADR-004). `docs/PARITY.md` rows change in
   the slice that adds them.

### 3. Shared public origin
ADR-044 accepted the shared origin for the LAN and for public path apps. The
change widens who is on it: apps that had their own origin now share the
dashboard's. The owner has accepted this trade-off in ADR-044; this ADR adds
mitigations and states what is **not** isolated.

What does not separate them: scripts in one app can call the dashboard and
Authentik APIs as the signed-in person; localStorage, IndexedDB and service
workers are origin-wide (a service worker scoped to `/apps/<app>/` cannot
see other paths, but an app can register one at that scope only); cookies
with `Path=/` reach every app.

What remains or is added:
- The dashboard session cookie is HttpOnly, `Path=/`, `SameSite=Strict`,
  `Secure` over TLS (`internal/server/auth.go`); the CSRF cookie is the same
  but readable by script. Slice S470 renames both with the `__Host-` prefix
  (Secure, Path=/, no Domain, which they already satisfy) so a plain-HTTP or
  other-subdomain page cannot overwrite them.
- The edge removes the dashboard's session and CSRF cookies and Authentik's
  session cookie from requests proxied to an app at a subpath, and removes
  any `Set-Cookie` from an app response that uses a reserved cookie name
  (cookie tossing). For a forward-auth app (Navidrome) the Authentik cookie
  is stripped only after the gate's subrequest, so the outpost still sees it.
- Public path routes get `Content-Security-Policy: frame-ancestors 'self'`
  (no framing from other origins) and `Referrer-Policy: same-origin`, unless
  an app's package sets a stricter or app-specific value (S464 CSP for
  Everything apps stays). A blanket script CSP is not added; apps need their
  own inline scripts.
- Step-up re-authentication on risky dashboard actions and approval digests
  (ADR-044, ADR-049) are unchanged; a script cannot approve without step-up.
- An app's own cookies should carry `Path=/apps/<app>/`; the preview warns
  for an app known to set `Path=/`.
- Apps that are single-origin or cannot run at a path keep their own origin
  and so keep real isolation. That is a reason to keep them on their own
  names, not only a necessity.

### 5. Authentik issuer and redirect URIs
A subpath app is already a path app for ADR-047: its issuer is
`https://<dashboard public name>/apps/auth/application/o/<slug>/`, and the
back channel stays on the box. Its redirect URIs become the subpath
callback on the public name (plus the existing LAN/`.local` path callbacks);
the subdomain callback is removed in step (d) of the move. The Authentik
application's launch URL moves to the path. Sign-in after the move uses the
same adapter as before, re-planned (ADR-047: publishing changes the issuer
only when Authentik's own public status changes; here Authentik is already
public, so only the redirect list and the app's address change).

### Forward-auth for Navidrome on a subpath
Navidrome gets `serve: path` (`ND_BASEURL=/apps/navidrome/`). The gate
changes from a site of its own to a `handle` inside the dashboard site:
`/outpost.goauthentik.io/*` is reverse-proxied to the outpost on the
dashboard host (public too, in the plan preview, because the gate must run
for public visitors), and `forward_auth` guards `/apps/navidrome/*` except
the bypass paths, which become `/apps/navidrome/rest/*` and
`/apps/navidrome/share/*`. Authentik's single-application mode matches the
bypass regexes against the request path, so they are written with the
prefix. `canonicalRoute` and `countNamed` in `internal/appsso/forwardauth.go`
skip path routes today and change in S472. Uptime Kuma stays on its own
local name and is never published.

### Tunnel and DNS
A subpath app adds no CNAME and no ingress entry; the dashboard's public
name already carries it (ADR-044). The tunnel carries the dashboard name and
the own-name apps only (Immich, the four ADR-060 apps, Memos, BookStack,
Vikunja, Vaultwarden, and any package that fails its subpath check). The 404 for an
unpublished `/apps/<app>/` on the public host (ADR-044) stays. Public names
are still one label below the zone (Universal SSL).

### ADR-058 read links and ADR-060
Read links are Everything apps (all `serve: path`), already at
`https://<public host>/apps/<app>/_homestead/read/*`; they do not move.
ADR-058 is unchanged unless a moved host app later gains read links. ADR-060
is unchanged: its four apps stay `serve: host`, and the package test rejects
a manifest that is both `single_origin_sign_in` and `serve: path` until a
later ADR says otherwise.

## Fact table (packages in `catalog/`, checked 2026-11-08)
"LAN path" means `serve: path` in the manifest (served at `/apps/<svc>/` on
`homestead.lan`). "Publishable" means `exposure.allowwider` is true and the
route can be published today. Current public form is what a publish plan
produces now.

| Package | LAN path | Publishable today | Current public form | Proposed | Reason |
|---|---|---|---|---|---|
| authentik | yes | yes (tunnel setup) | subpath /apps/auth/ | subpath | unchanged (ADR-044, ADR-046) |
| nextcloud | yes | yes | subpath | subpath | unchanged |
| jellyfin | yes | yes | subpath | subpath | unchanged |
| api-client | yes | yes | subpath | subpath | Everything app, unchanged |
| circuits | yes | yes | subpath | subpath | Everything app |
| contacts | yes | yes | subpath | subpath | Everything app |
| expense | yes | yes | subpath | subpath | Everything app |
| inventory | yes | yes | subpath | subpath | Everything app |
| notes | yes | yes | subpath | subpath | Everything app |
| parametric | yes | yes | subpath | subpath | Everything app |
| projects | yes | yes | subpath | subpath | Everything app |
| records | yes | yes | subpath | subpath | Everything app |
| solitaire | yes | yes | subpath | subpath | Everything app |
| sudoku | yes | yes | subpath | subpath | Everything app |
| tasks | yes | yes | subpath | subpath | Everything app |
| three-d | yes | yes | subpath | subpath | Everything app |
| time-tracking | yes | yes | subpath | subpath | Everything app |
| cadcraft | yes | yes (S481) | none | subpath (S481, ADR-062: tunnel-only gate) | craft app: LAN open, public path behind the shared gate |
| deckcraft | yes | yes (S481) | none | subpath (S481, ADR-062: tunnel-only gate) | craft app: LAN open, public path behind the shared gate |
| designcraft | yes | yes (S481) | none | subpath (S481, ADR-062: tunnel-only gate) | craft app: LAN open, public path behind the shared gate |
| effectcraft | yes | yes (S481) | none | subpath (S481, ADR-062: tunnel-only gate) | craft app: LAN open, public path behind the shared gate |
| figcraft | yes | yes (S481) | none | subpath (S481, ADR-062: tunnel-only gate) | craft app: LAN open, public path behind the shared gate |
| filmcraft | yes | yes (S481) | none | subpath (S481, ADR-062: tunnel-only gate) | craft app: LAN open, public path behind the shared gate |
| gridcraft | yes | yes (S481) | none | subpath (S481, ADR-062: tunnel-only gate) | craft app: LAN open, public path behind the shared gate |
| lightcraft | yes | yes (S481) | none | subpath (S481, ADR-062: tunnel-only gate) | craft app: LAN open, public path behind the shared gate |
| pdfcraft | yes | yes (S481) | none | subpath (S481, ADR-062: tunnel-only gate) | craft app: LAN open, public path behind the shared gate |
| photocraft | yes | yes (S481) | none | subpath (S481, ADR-062: tunnel-only gate) | craft app: LAN open, public path behind the shared gate |
| soundcraft | yes | yes (S481) | none | subpath (S481, ADR-062: tunnel-only gate) | craft app: LAN open, public path behind the shared gate |
| vectorcraft | yes | yes (S481) | none | subpath (S481, ADR-062: tunnel-only gate) | craft app: LAN open, public path behind the shared gate |
| wordcraft | yes | yes (S481) | none | subpath (S481, ADR-062: tunnel-only gate) | craft app: LAN open, public path behind the shared gate |
| immich | no (`serve: host`) | yes | own name | own name | cannot run under a path (ADR-044) |
| gitea | no | yes | own name | own name | `single_origin_sign_in` (ADR-060) |
| actual-budget | no | yes | own name | own name | cannot run under a path; single-origin (ADR-060) |
| mealie | no | yes | own name | own name | single-origin (ADR-060); upstream path support unverified, moot |
| calibre-web-automated | no | yes | own name | own name | single-origin (ADR-060) |
| vaultwarden | no | yes | own name | own name | owner decision 2026-11-08: a password manager keeps its own origin |
| memos | no | yes | own name | own name | manifest: cannot run under a path |
| bookstack | no | yes | own name | own name | manifest: APP_URL must be the root URL |
| vikunja | no | yes | own name | own name | manifest: cannot run under a path |
| uptime-kuma | no | no (allowwider false) | none | never published | cannot run at a sub-path; holds monitor credentials (ADR-055) |
| navidrome | no | yes | own name (forward-auth site) | subpath (S472) | ADR-055 names `ND_BASEURL`; gate moves into the dashboard site |
| audiobookshelf | no | yes | own name | subpath if verified (S473) | upstream router base path; unverified |
| komga | no | yes | own name | subpath if verified (S474) | servlet context path; unverified |
| paperless-ngx | no | yes | own name | subpath if verified (S475) | script name and static URL settings; unverified |
| linkding | no | yes | own name | subpath if verified (S476) | context path setting; unverified |
| freshrss | no | yes | own name | subpath if verified (S477) | proxy prefix; its Apache OIDC layer (S412) may complicate it; unverified |

Counts: subpath 17 (already so), never published 14 (13 craft, Kuma),
own name 9 (Immich, Gitea, Actual Budget, Mealie, Calibre-Web Automated,
Memos, BookStack, Vikunja, Vaultwarden), subpath candidates to verify 6
(Navidrome and five more). Total 46.

## Slice plan
Strictly sequential, one commit each (owner rule). S469 first.
- S469: package `publish` field, derivation, validation and the fact-table test.
- S470: shared-origin hardening at the public edge (cookie names, stripping, headers).
- S471: move plan from a subdomain to a subpath (API, CLI, dashboard, audit, Doctor), clean removal, no redirect.
- S472: Navidrome at a subpath, forward-auth inside the dashboard site.
- S473 to S477: one verified package per slice (Audiobookshelf, Komga, paperless-ngx, Linkding, FreshRSS).
- S478: live acceptance on a test appliance and docs.

Details are in `docs/SLICE_PLAN.md`, section 9.

## Alternatives considered
- Keep per-app subdomains: keeps browser-level isolation, costs one CNAME and
  one name per app. Declined by the owner for apps that can run at a path.
- Redirect old subdomains (default or timed opt-in): keeps public names the
  owner removed and needs a scheduler to remove them later. Declined by the
  owner.
- Rewrite app cookie paths at the edge: fragile; apps set their own paths.
  The edge only strips reserved names and warns.
- Move every host app now: unverified upstream support would break installs;
  each candidate is proved in its own slice.

## Consequences
- The shared-origin risk now covers every published subpath app, in the same
  browser as the dashboard; the owner has accepted it (ADR-044) and the
  mitigations above reduce but do not remove it.
- Moving an app changes every URL, including the ones saved in phone and
  desktop clients; those need a manual change.
- Own-name apps remain the isolation exception, and the tunnel has fewer
  hostnames over time.
- A manifest change from `serve: host` to `serve: path` is a normal catalog
  bump plus a move offer; no install is changed without approval.
- Reversing this for an app is a package change back to `serve: host` plus a
  re-plan.

## Owner decisions (2026-11-08)
Resolved questions from the draft:
1. Vaultwarden keeps its own name even if a subpath would work, because of
   the shared-origin risk for a password manager. The other candidates
   (Navidrome, Audiobookshelf, Komga, paperless-ngx, Linkding, FreshRSS) are
   moved when verified.
2. No timed redirect. Removal is clean; the preview says old bookmarks and
   links stop working.
3. Memos, BookStack and Vikunja keep their own names.
