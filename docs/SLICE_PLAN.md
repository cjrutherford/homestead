# Remaining Slice Plan

Status: proposed schedule (2026-10-26). Scope is every remaining slice from
`docs/CATALOG_EXPANSION.md`, the Everything repackaging assessment in
`docs/reports/catalog-expansion.html`, and the craft build/serve validation
gate in section 2.6. Slice IDs are assigned here so the
tracks cannot collide; each slice's contract file is written when it is
scheduled, not now.

Numbering continues from S341 (catalog integration contract). The plan ends
at **S385** — 45 slices. S383 fixes catalog port metadata; S384 adds Audiobookshelf first-run and S385 is its Authentik adapter. Work with no ID is listed in section 7 and is not
committed to.

S384 is the Audiobookshelf first-run prerequisite; S385 is its planned Authentik adapter. S383 is completed separately as the catalog container-port correction; see `docs/slices/S383-catalog-container-port-requirements.md`.

## 1. Rules that apply to every slice below

- One slice, one commit on main. A slice that turns out to need two is two
  slices.
- Read `docs/PROJECT.md`, `docs/ARCHITECTURE.md`, the accepted ADRs and the
  slice file before coding.
- Tests plus docs, and `docs/DEFERRED.md` for anything discovered but not
  done.
- `docs/PARITY.md` updated whenever a route, CLI command or dashboard
  surface changes. The parity test enforces it.
- Every catalog package declares its `integrations:` (S341). A package that
  cannot name its identity provider or shared library fails the load.
- No package ships with a `latest` tag, an undeclared writable path, a
  privileged container, host networking, or a secret in an environment
  variable.

## 2. Track A — Docker catalog

### 2.1 Foundations

| ID | Slice | Depends on | Scope |
|----|-------|-----------|-------|
| S341 | Catalog integration contract | S150,S152,S153,S154,S161,S202-S204,S276 | `integrations:` manifest metadata, derived L1-L4 levels, cross-package load gates, `apps show` and dashboard "Works with". ADR-051. **Done.** |
| S342 | OIDC client actuation and secret delivery (F1) | S341,S202,S204,S266 | Create Authentik OIDC clients from the catalog declaration; write the client secret through the secret store; supervised entrypoint that exports 0600 files into the app environment at start. ADR-052 amends ADR-021. Gates all of Wave 1. **Done.** |
| S343 | GHCR update discovery (F3) | S190,S263 | Token-based GHCR tag discovery beside the existing Docker Hub path; provenance recorded per package. **Done.** |
| S344 | Catalog bundles and capacity (F5) | S152,S154,S172,S341 | Starter/Family/Media/Full bundles with combined RAM and storage checks, shown in the plan and the dashboard. **Done.** |

### 2.2 Wave 1 — everyday suite

Each is one manifest, one test set, one slice. Gated on S342; `freshrss`
and `mealie` also wait on S343.

| ID | Package | Depends on | Notes |
|----|---------|-----------|-------|
| S345 | `vaultwarden` | S342 | Owner signup, admin page off by default; local/overlay exposure. **Done.** |
| S346 | `paperless-ngx` | S342,S343,S276 | Postgres + Redis; shared scans volume with Nextcloud; OCR languages. **Done.** |
| S347 | `gitea` | S342 | `ROOT_URL` from the route host; HTTPS-only, no SSH in v1. **Done.** |
| S348 | `freshrss` | S342,S343 | Debian image so OIDC exists. **Done.** |
| S349 | `linkding` | S342 | Simplest package in the wave. **Done.** |
| S350 | `mealie` | S342,S343 | GHCR image. **Done.** |
| S351 | `uptime-kuma` | S342 | Documented local-auth exception; never a public status page. **Done.** |

### 2.3 Second foundation

| ID | Slice | Depends on | Scope |
|----|-------|-----------|-------|
| S352 | MariaDB/MySQL backup export (F2) | S180,S266 | `mariadb`/`mysql` server kinds, consistent dumps with each engine's client, documented restore, password secrets. Required before BookStack. **Done.** |

### 2.4 Wave 2 — family library

No wave-level gate. `audiobookshelf` needs S343; `calibre-web-automated`
needs the S341 library declaration to name its shared ingest path.

| ID | Package | Depends on | Notes |
|----|---------|-----------|-------|
| S353 | `audiobookshelf` | S343 | Bulk audiobook library shared by volume. **Done.** |
| S354 | `navidrome` | S276 | Local-auth exception; same music mount as Jellyfin. **Done.** |
| S355 | `komga` | S341 | JVM heap cap in the manifest so it cannot crowd the appliance. **Done.** |
| S356 | `calibre-web-automated` | S341 | Community image; OIDC re-verified at slice time or documented as local-only. **Done.** |
| S357 | `vikunja` | S341 | CalDAV against Nextcloud (S357: not wired; Vikunja's own CalDAV) and Authentik sign-in via env-oidc config file. **Done.** |
| S358 | `actual-budget` | S276 | Native OpenID verified (env-oidc, S358); first sign-in is the server owner. **Done.** |
| S359 | `memos` | S341 | Declarative-config OIDC (env-oidc, S359); SSO people are ordinary users. **Done.** |

### 2.5 Third foundation, then Wave 3

| ID | Slice | Depends on | Scope |
|----|-------|-----------|-------|
| S360 | Community-image trust policy (F4) | S279 | Written policy plus ADR-056: trusted publishers, multi-arch requirement, pinned tags, provenance, update source; `publisher`/`justification` manifest fields with a catalog test (S360). Gates every LinuxServer.io package. **Done.** |

| ID | Package | Depends on | Notes |
|----|---------|-----------|-------|
| S361 | `prowlarr` | S360 | Indexer manager; local only. |
| S362 | `sonarr` | S360,S341 | TV; library paths must match Jellyfin exactly. |
| S363 | `radarr` | S360,S341 | Movies; same shape as Sonarr. |
| S364 | `sabnzbd` | S360 | Usenet client; owner supplies the provider. |
| S365 | `seerr` | S341 | Jellyfin login; LAN only. |
| S366 | `bookstack` | S352,S360 | MariaDB export, OIDC, LinuxServer image. Last slice of the catalog track. Built (S366). |

### 2.6 Validation gate — craft build, serve and local names

This is a validation slice with fixes, not a catalog package. It is off the
catalog critical path but runs next: the craft packages are already built,
and their KVM acceptance never proved that a LAN client can resolve the
route names. It also de-risks S367, which reuses the craft static-image
shape.

| ID | Slice | Depends on | Scope |
|----|-------|-----------|-------|
| S382 | Craft build, serve and DNS advertisement validation | S249-S253,S279,S280,S281,S237,S84-S86,S96,S98 | Run all 13 craft packages end to end on a fresh appliance; wire the accepted S84-S86 local DNS capability into route reconciliation so every local route host resolves from another LAN client; verify the start page links, removal, foreign rewrites and doctor state; fix defects found. May add local DNS API/CLI/dashboard surfaces, so `docs/PARITY.md` applies. No new DNS architecture expected (ADR-007/ADR-009). |

## 3. Track B — Everything apps

Track B is independent of Track A until an app slice declares catalog
integration metadata (S341) and reuses the craft static-image shape
(S249/S279). It does not need F1-F5.

### 3.1 Bridge and build pipeline

| ID | Slice | Depends on | Scope |
|----|-------|-----------|-------|
| S367 | Puter bridge and app build pipeline | S279,S341 | `third_party/everything/pin.yaml`; per-app patch that swaps the CDN script and adds a fail-closed CSP; `bridge/` implementing `auth`, `env`, `fs`, `kv`, `ui`, `perms`, `exit`; multi-stage image that builds the pinned commit and serves it; upstream behavior run against the actual bridge; ADR-053 constrains the model (pin + patch + bridge, no fork, no vendored SDK) before implementation. |

S367 is the riskiest slice in the plan. If the bridge cannot pass
the Tasks behavioral gate against the actual bridge, Track B stops at S367 and
nothing after it is scheduled. The upstream mock suite alone is not the gate. Treat S367 as a spike with a hard exit criterion.

### 3.2 Planned candidates

Tasks is the pilot; its current storage, synchronization and sharing surface
must be re-evaluated against the bridge before the later candidates ship.

| ID | App | Also needs | Why here |
|----|-----|-----------|----------|
| S368 | `tasks` | — | Pilot. Current Tasks uses authentication, KV, files, offline sync and sharing; proves the pipeline end to end. |
| S369 | `projects` | — | 24 fs, 4 ui, 2 kv. First real `fs` consumer. |
| S370 | `records` | — | Tables and forms; the Airtable gap. |
| S371 | `notes` | — | AGPL. AI assist disabled behind a clear message. |
| S372 | `contacts` | — | `kv` + `ui` heavy. |
| S373 | `inventory` | — | Household inventory. |
| S374 | `time-tracking` | — | `fs` + `kv`. |
| S375 | `circuits` | — | One `fs` call; hobbyist gap. |
| S376 | `parametric` | — | One `fs`, one `ai` (disabled). |
| S377 | `3d` | — | IndexedDB + File System Access API; Chromium-only caveat in the copy. |
| S378 | `solitaire` | — | Cheapest possible package; proves the build for a 245 KB app. |

### 3.3 Cheap extras

| ID | App | Depends on | Note |
|----|-----|-----------|------|
| S379 | `sudoku` | S367 | Same shape as solitaire. |
| S380 | `expense` | S367 | Local-only; do not ship the "AI OCR" claim. |
| S381 | `api-client` | S367 | Needs the `puter.net` bridge extension — the only surface addition after S367. **Deferred by the owner 2026-11-02; un-deferred 2026-11-03 under ADR-058 and planned as S436 after `net` (S434-S435).** |

### 3.4 ADR-058 slices (S424-S441)

Accepted 2026-11-03 and strictly sequential, one commit each. Order: S424 ADR;
S425 people registry; S426 share grants store; S427 automatic purge on People
removal; S428 KV sharing, `perms`, `appID`; S429 FS sharing; S430 events;
S431 read links on local names; S432 script-free read-only viewer; S433
read-link and viewer public exposure plan (daemon, API, CLI, dashboard);
S434 net fence; S435 net operation, limits and log; S436 `api-client` (S381);
S437 Tasks, S438 Notes, S439 Circuits, S440 Parametric (one app per slice,
re-enabling the hidden UI); S441 review plan and ADR-057 table.

**Status: all of S424-S441 are implemented (ADR-058 implementation complete).**
What remains is live acceptance on a real appliance, the ADR-058 section of
`docs/reviews/EVERYTHING_REVIEW_PLAN.md`.

## 4. Critical path and recommended order

The longest chain is:

`S341 → S342 → S344 → S345…S351 → S352 → S353…S359 → S360 → S361…S366`

That is 25 slices and cannot be shortened much: Wave 1 is gated on S342 by
owner decision, and Wave 3 is gated on S360 and S352.

S382 is off the catalog critical path but runs next: the craft packages are
already built, and their KVM acceptance never proved a LAN client can resolve
the route names. Leaving that open keeps the Craft apps start page promising
links that do not work and keeps S367 building on an unverified serve path.

Track B is the only place to win time. Recommended interleave, so the
first owner-visible win lands early and the riskiest slice is tested before
the catalog is half-built:

1. S341 — catalog integration contract (done)
2. S342 — OIDC actuation and secret delivery
3. S382 — craft build/serve and DNS advertisement validation (**runs next**)
4. S367 — bridge and build pipeline (**spike exit check**)
5. S368 — `tasks`
6. S343 — GHCR discovery
7. S344 — bundles and capacity
8. S345 → S351 — Wave 1, one per slice
9. S369 → S372 — four Everything apps
10. S352 — MariaDB export
11. S353 → S359 — Wave 2
12. S373 → S378 — remaining clean Everything apps
13. S360 → S366 — Wave 3
14. S379 → S381 — extras

Rationale: S382 sits third because the craft suite is already built and its
unresolvable names block the start page; proving the serve and name path now
also de-risks S367. S367 then sits fourth because it is the only slice that
can invalidate a whole track. Finding that out after twenty catalog packages
would be an expensive discovery.

## 5. Definition of done — app slices

Every package slice (S345-S351, S353-S366, S368-S381) closes only when:

- the manifest pins a non-`latest` tag, both architectures, and declares
  every writable path on mounted storage or as ephemeral;
- requirements declare memory, CPU, storage floors and host ports;
- the package declares `integrations:` — identity provider or documented
  exception, shared library where one exists;
- secrets arrive through the S266/S342 path, never as plaintext env;
- backup covers the database export and file paths, with the restore
  story written down;
- exposure defaults to local/overlay, with any widening documented;
- every routed app's hostname resolves from the audience its exposure names
  — local DNS for local, overlay DNS for overlay, public DNS for public;
  S382 makes the local case real, and a route without a resolvable name is
  not done;
- CLI plan and dashboard agree (parity);
- tests cover manifest validation, plan rendering and install;
- `docs/DEFERRED.md` records anything found but not done;
- the slice doc exists in `docs/slices/` and is referenced here.

## 6. ADR ledger

| ADR | Slice | Decision |
|-----|-------|----------|
| ADR-051 | S341 | Catalog integration metadata is advisory, not actuation. |
| ADR-052 | S342 | Secret delivery may export a 0600 file into the app environment at start, under supervision. Amends ADR-021. |
| ADR-056 | S360 | Community image trust: accepted publishers, multi-arch, pinned tags, provenance. |
| ADR-058 | S424 | Everything SDK sharing, read links, net and events replace ADR-057's refused rows (Accepted 2026-11-03). Slices S425-S441, all implemented. |
| ADR-059 | S442 | No manual first run for any catalog app, Authentik excluded; Vaultwarden master password is the one personal step (Accepted 2026-11-07). Implemented by the slices after S442. |
| ADR-053 | S367 | Independent bridge and customer-owned per-person app data; no fork or hosted SDK. ADR-036 is the setup-wizard decision. |

## 7. Unscheduled — no ID

Recorded so nothing is silently lost. None of these blocks a scheduled
slice.

**Owner-operator apps** (bridge-clean, wrong default suite): `accounting`,
`cap-table`, `crm`, `pos`, `AssetDesk`. Schedule on request, one slice
each, after S382.

**Blocked on a capability we are not building:** `calendar`, `email`,
`meetings`, `teamchat`, `wiki`, `signatures`, `help-desk`, `field-service`,
`fleet`, `legal`, `childcare`, `property-management`, `shifts`, `hiring`,
`invoicing`, `contracts`, `whiteboard`. `survey` becomes shippable if a
worker shim ever exists.

**Craft-suite overlap:** `documents`, `spreadsheet`, `presentation`, `pdf`,
`vector-studio`, `photo-studio`, `music-studio`, `audio-studio`,
`video-studio`, `cad`, `building-design`.

**Catalog candidates already deferred:** `qbittorrent` (VPN network-mode
design), `collabora`/`onlyoffice` (Nextcloud app management), `immich-kiosk`
(companion), `ntfy` (no notification pipeline to feed it).

## 8. What could renumber this plan

- **S367 fails its exit check.** Track B stops; S368-S381 are released.
- **S382 cannot advertise local names safely.** If wiring S84-S86 needs a
  DNS design different from ADR-007/ADR-009 (for example port 53 is owned
  by the OS and cannot be resolved), the DNS half splits into its own slice
  behind a superseding ADR; the build/serve half still lands. The ID stays
  with the validation.
- **A package fails its hard gates at slice time.** The ID is not reused;
  the slice is withdrawn and the next package moves up.
- **The Compose generator gains a field** (healthcheck, `depends_on`).
  That is a slice of its own and would insert ahead of S361 if any Wave 3
  app needs a readiness gate.
- **F1 cannot deliver supervised export.** Then Wave 1 reverts to
  owner-supplied credentials and S342's scope changes, not its ID.

## 9. Public apps at a subpath (ADR-061, Proposed 2026-11-08)

Status: planned, not scheduled. ADR-061 is Accepted (owner, 2026-11-08). Strictly sequential; one slice, one commit. S469 to S472 are
platform; S473 to S477 are one package each, each with its Authentik or
forward-auth sign-in in the same slice. Each package slice first proves the
app's base-path setting works at the subpath on the LAN and public; if it
fails the check, the slice is withdrawn, the package stays on its own name,
and the finding goes to `docs/DEFERRED.md` (the ID is not reused).

| Slice | Scope | Depends on |
|---|---|---|
| S469 | Manifest field `publish` (`path`, `host` with reason, `never`), derived for all 46 packages and validated (`single_origin_sign_in` implies host; `allowwider: false` implies never; `path` implies `serve: path`). A test fixture holds the ADR-061 fact table. | ADR-061 |
| S470 | Shared-origin hardening on public path routes: `__Host-` dashboard session and CSRF cookie names, edge strips dashboard and Authentik cookies from app requests (after any forward-auth subrequest) and reserved `Set-Cookie` names from app responses, `frame-ancestors 'self'` and `Referrer-Policy: same-origin` headers, plan warning text. | S469 |
| S471 | Move plan from `<app>.<zone>` to `/apps/<app>/`: one digest-bound operation (path route, public path, `app_url`, sign-in re-plan, read links, then removal of ingress, edge route and CNAME last); clean removal with no redirect, and a preview that says old bookmarks and links stop working; Doctor "move available" check; API, CLI, dashboard, `docs/PARITY.md`. | S469, S470 |
| S479 | Public-name move safety, runs before S472 (owner request): the move probes the new subpath address through the public edge before removing the old name; a half-done move is finished through the same planned flow; Doctor `public-name-plan` flags names no route carries; audit-timing tests deterministic. | S471 |
| S472 | Navidrome `serve: path` (`ND_BASEURL`), forward-auth `handle` inside the dashboard site, public outpost path, bypass regexes with the prefix; `canonicalRoute` and `countNamed` handle path routes; move offer for existing installs. | S471 |
| S473 | Audiobookshelf at a subpath (verify, manifest, OIDC redirect URIs in its database, move). | S471 |
| S474 | Komga at a subpath (same shape). | S473 |
| S475 | paperless-ngx at a subpath. | S474 |
| S476 | Linkding at a subpath. | S475 |
| S477 | FreshRSS at a subpath (Apache mod_auth_openidc layer). | S476 |
| S478 | Live acceptance on a test appliance: move one app from a subdomain, confirm the old CNAME and ingress are gone, sign-in works, Doctor is clean; docs. | S472 and the package slices that landed |

## 10. One shared gate for apps at a subpath (ADR-062, Accepted 2026-11-08)

Status: S480 in progress. Strictly sequential; one slice, one commit. ADR-062
puts every forward-auth-gated app at a path behind one Authentik proxy
provider on the dashboard's name, with the edge admitting each app's groups
only. S478 (live acceptance of the subpath work) is absorbed into S482.

| Slice | Scope | Depends on |
|---|---|---|
| S480 | The shared gate: one Authentik provider and application for every gated app at a path, the union of their bypass paths, the edge group check (exact match, 403, client identity headers deleted, groups header removed before the app), and Navidrome moved onto it by re-planning its sign-in. Live on VM 2: owner and A in, C 403, Subsonic bypass, forged headers ignored, Doctor green. | S472, ADR-062 |
| S481 | The 13 craft apps go from `publish: never` to `publish: path`; a published craft route is rendered behind the shared gate on the tunnel entry (LAN stays open, ADR-062 section 6); the publish plan says so; the fact table and craft tests follow. | S480 |
| S482 | Live acceptance of the subpath work on VM 2 (absorbs S478): a moved app from a subdomain, a published craft app behind the gate, old names gone, Doctor clean; docs. | S481, S478 |

