# Catalog Expansion Plan

Status: owner-approved plan (2026-10-23). This is an evaluation and a
planned list, not a scheduled slice. No catalog package is added by this
document. The nine owner questions in section 10 are resolved; the
ordering in section 9 reflects those answers.

Sources considered:

- [Puter/Everything](https://github.com/HeyPuter/everything)
- [Awesome Selfhosted](https://github.com/awesome-selfhosted/awesome-selfhosted)

## 1. Decision summary

- Add **20 application packages** in three waves, taking the catalog from
  17 packages (4 core apps plus 13 craft apps) to 37. The craft suite
  stays as it is.
- **Do not add Puter/Everything apps in v1.** They are browser SPAs that
  depend on the Puter cloud runtime (`https://js.puter.com/v2/`), they
  overlap the existing craft suite, and packaging them one-by-one would
  create a second static-app platform beside ADR-028. A Puter track is
  deferred until there is a standalone/static-app decision.
- Treat **integration as deployment integration**, not deep app-to-app
  automation: shared identity (Authentik OIDC where the app supports it),
  shared bulk libraries where the app reads media, Homestead backups,
  one edge route and exposure posture per app, and the dashboard as the
  launch point. In-app configuration (indexers, libraries, users) stays
  owner-driven through the app's own UI.
- Five foundation items should land before or with Wave 1, because the
  selected apps otherwise install but cannot be called integrated:
  OIDC client actuation (including secret delivery), MariaDB/MySQL backup
  export, GHCR update discovery, a community-image trust policy, and
  catalog bundles with capacity planning.
- Not every app fits an 8 GB appliance. The plan distinguishes four
  install profiles (starter, family, media, full) and recommends 16 GB
  for the family profile and 32 GB for the full media profile.

### 1.1 Owner decisions (2026-10-23)

| Question | Decision |
|----------|----------|
| Wave 1 SSO | **Wave 1 is gated on F1.** No Wave 1 package is scheduled until OIDC client actuation works end to end. `uptime-kuma` stays the documented local-auth exception and still ships inside Wave 1 after F1 lands. |
| Secret delivery | **Supervised entrypoint wrapper.** Homestead writes the 0600 secret file, and a small supervised entrypoint exports it into the app's environment at start. F1 carries the ADR amendment; no plaintext secret is written into compose files or the unit environment. |
| Community images | **Accepted with a written policy.** F4 defines trusted publishers, the multi-arch requirement, pinned tags and provenance. LinuxServer.io is in scope for the *arr stack, SABnzbd and BookStack. |
| Git forge | **Gitea.** Forgejo is not packaged. |
| Media automation | **Full stack in Wave 3:** Prowlarr, Sonarr, Radarr, SABnzbd and Seerr. |
| Bundles | **F5 is a Wave 1 prerequisite.** Starter/Family/Media/Full profiles ship with combined RAM and storage checks. |

Consequences:

- F1 is now on the Wave 1 critical path, not a parallel track. It must
  deliver client creation, credential write, and the entrypoint wrapper
  together, because "gate all of Wave 1" is not satisfiable with the
  contract S202-S204 currently define.
- F3 (GHCR discovery) is also Wave 1 prerequisite work: Paperless and
  Mealie publish on GHCR.
- F2 (MariaDB export) and F4 (community-image policy) move off the Wave 1
  path and are scheduled before Wave 3 instead.
- The *arr packages are no longer conditional on a policy question; they
  are conditional on F4 being written and accepted.

## 2. Current baseline

The catalog baseline at planning time contained 17 packages:

| Area | Packages |
|------|----------|
| Core apps | `jellyfin`, `nextcloud`, `immich`, `authentik` |
| Craft/static apps | `wordcraft`, `gridcraft`, `pdfcraft`, `deckcraft`, `vectorcraft`, `photocraft`, `lightcraft`, `figcraft`, `designcraft`, `cadcraft`, `filmcraft`, `effectcraft`, `soundcraft` |

Wave 1 has since shipped seven more (`vaultwarden`, `paperless-ngx`,
`gitea`, `freshrss`, `linkding`, `mealie`, and `uptime-kuma` in S351),
bringing the catalog to 24; the slice ledger is `docs/SLICE_INDEX.md`.

The platform already provides AdGuard Home, Caddy, Headscale/Tailscale,
Dockge and the Homestead dashboard. Those are **not** catalog candidates:
they are platform components with their own ownership and lifecycle.

## 3. Evaluation criteria

An application enters the curated catalog only when all hard gates pass.

### Hard gates

1. **Docker-servable.** The project publishes (or an accepted trusted
   publisher publishes) a multi-arch `linux/amd64` and `linux/arm64`
   image. `latest` is refused; every manifest pins a version tag.
2. **Runs inside the generation model.** No `privileged`, `host`
   networking, device passthrough, `cap_add`, custom sysctls, or
   read-only-root requirement. The current schema cannot express those
   (device passthrough is already deferred; see `docs/DEFERRED.md`).
3. **Data placement is knowable.** Every writable image path is declared
   on mounted storage or explicitly ephemeral. No anonymous volumes and
   no data in the container writable layer.
4. **Configurable without file surgery.** The app starts from environment
   variables and secrets. Mandatory config-file editing, a one-shot
   `docker compose run` bootstrap, or an init container disqualify the
   package until a schema field exists.
5. **Backupable.** The package has a database export (SQLite or Postgres
   today) or a crash-consistent file story, plus file paths that the
   existing backup engine can capture.
6. **Identity where it matters.** Apps that hold shared or sensitive data
   should support OIDC/SAML/LDAP through Authentik. A local-auth-only app
   is acceptable only as a local/overlay service with an explicit SSO
   exception (for example a single-owner media server).
7. **Ownership and licensing.** The app is free software and the license
   permits Homestead to describe, install and update it without
   redistributing a modified work. Upstream descriptions are not copied;
   catalog copy is written for Homestead.
8. **No hidden SaaS dependency.** The app must work with owner-supplied
   configuration and no vendor account, telemetry or hosted control
   plane. Optional cloud features are acceptable when the app starts and
   works without them.
9. **Secret compatibility.** Generated values must reach the app through a
   path ADR-021 permits. Apps that read `*_FILE` variables are ready;
   apps that accept only a plaintext environment password (Paperless
   `PAPERLESS_DBPASS`, Vaultwarden `ADMIN_TOKEN`, FreshRSS
   `OIDC_CLIENT_SECRET`) need F1's delivery decision before their SSO or
   generated admin credential can be automated. An app can still install
   with owner-supplied local credentials and no generated secret.

### Soft scoring

- Value to a household or small owner-operated appliance.
- Reuse of existing catalog apps (identity, storage, media, backup).
- Operational simplicity: one or two containers, SQLite or Postgres,
  no external search engine or browser worker.
- Updateability: image on Docker Hub or GHCR, discoverable tags.
- Exposure risk: can be recommended local/overlay without losing the
  main use case.

## 4. Source evaluation

### 4.1 Awesome Selfhosted

Awesome Selfhosted is a broad directory (well over a thousand entries),
not a package format. Most entries are not suitable as-is: they are
desktop tools, unpinned `latest` images, single-arch builds, apps that
require Kubernetes, or apps whose Docker image is a community fork. The
list is useful as a **candidate pool**, not as a catalog.

The planned list below is the result of screening that pool against the
hard gates. The rejected set is recorded in section 6.4 with the reason,
so a later review does not have to repeat the exercise.

Two structural observations drove the selection:

- The strongest fits are single-binary or two-container apps with SQLite
  or Postgres. They map directly to `services`, `data`, `database` and
  `secrets`, and they update cleanly.
- The PHP/MySQL family is large but blocked by the current backup model,
  which supports only SQLite and Postgres exports. MariaDB/MySQL export is
  therefore a foundation item, not an app-by-app exception.

### 4.2 Puter/Everything

Puter/Everything is a monorepo of roughly forty Vite single-page apps
(Office, Collaboration, Creative, Engineering, Productivity, Finance,
Business, Developer Tools, Games). The repository is mostly MIT, with
some apps carrying AGPL-3.0 (for example Notes). It has no Dockerfiles
and no server component per app.

Every app sampled loads `https://js.puter.com/v2/` and describes storage
in "your own Puter cloud". Documents, Notes, Wiki and Tasks all follow
this pattern. That makes them dependent on a Puter backend for identity
and file storage; a plain nginx container is not enough.

They also overlap the existing craft suite almost one-for-one:

| Puter app | Homestead equivalent |
|-----------|----------------------|
| Documents | WordCraft |
| Spreadsheet | GridCraft |
| Presentation | DeckCraft |
| PDF | PdfCraft |
| Photo Studio | PhotoCraft |
| Vector Studio | VectorCraft |
| CAD | CADCraft |
| Audio Studio | SoundCraft |
| Video Studio | FilmCraft |
| 3D Studio | No direct equivalent |

The genuine gaps are Whiteboard, Notes, Records, Tasks, Projects, Wiki,
Teamchat, Email, Contacts, Calendar, Signatures, Time Tracking, Surveys,
the finance/business apps and the games. Most of those are multi-user or
business tools, and mature Docker alternatives already exist for the
household cases (Memos, Vikunja, BookStack, WBO). They would still need a
Puter backend or a storage/auth adapter.

Options considered:

1. **Add Puter as one platform package.** Self-hosted Puter would bring
   its own identity, file store and app launcher. That duplicates
   Homestead's dashboard, identity and Nextcloud storage, and would be a
   second control plane. Not recommended.
2. **Add selected Puter SPAs as static packages.** This needs a new
   static-app build and signing pipeline, a decision to fork or patch the
   Puter SDK dependency, and a storage adapter (browser storage,
   Nextcloud WebDAV, or a small service). ADR-028 currently covers the
   storytold craft pipeline, not a general static-app source. An ADR
   would be required before implementation.
3. **Do not add them now.** This is the recommendation. Revisit if HAI
   wants a second static-app source or a Whiteboard/Notes niche that the
   Docker catalog cannot fill.

Recommendation: **no Puter package in Waves 1-3**. A time-boxed spike is
reasonable only for Whiteboard or Notes, and only after the static-app
question is answered.

### 4.3 Everything repackaging assessment (2026-10-23)

Owner reopened the Puter track for in-repo repackaging. The evaluated
model is **pin + patch + bridge**, not a fork: Homestead pins an upstream
commit, patches the one CDN script tag per app, and serves its own
`window.puter` implementation instead of `https://js.puter.com/v2/`. The
SDK surface actually used by the 51 apps is small — `auth`, `env`, `fs`,
`kv`, `ui`, `perms`, `exit` sit inside a bridge; `workers`, `peer`,
`email`, `hosting`, `ai` do not.

Per-app verdicts, measured from the source tree rather than the README,
are in `docs/reports/catalog-expansion.html` (§The Everything apps):
11 apps ship clean (`tasks`, `projects`, `records`, `notes`, `contacts`,
`inventory`, `time-tracking`, `circuits`, `parametric`, `3d`, `solitaire`),
18 are blocked on a capability the appliance should not provide, and 10
overlap the craft suite. This track is separate from the Docker catalog
waves above and does not change them.

## 5. Foundation work

These are not application packages. They are the pieces that make the
new packages genuinely integrated instead of merely installed.

| # | Item | Why it is needed | Blocking |
|---|------|------------------|----------|
| F1 | **OIDC client actuation and secret delivery** | S202/S204 define the identity contract and record intent, but do not create clients or write credentials into app configuration. The OIDC client secret is itself a secret: ADR-021 delivers secret-store values as 0600 files, while several selected apps read only plaintext environment variables. **Owner decision:** a supervised entrypoint wrapper exports the 0600 file into the app's environment at start, under an ADR-021 amendment. Wave 1 is gated on this landing. | Wave 1 (all packages), generated admin credentials |
| F2 | **MariaDB/MySQL backup export** | `DatabaseKind` supports `sqlite` and `postgres` only. BookStack and most PHP apps are MariaDB-only. Copying a live database directory is not crash-consistent. | BookStack, future PHP apps |
| F3 | **GHCR update discovery** | S190 discovers Docker Hub tags only. Paperless-ngx, Mealie, Audiobookshelf, Seerr and Immich publish on GHCR. Without this, those apps install but never show updates. | Paperless, Mealie, Audiobookshelf, Seerr |
| F4 | **Community-image trust policy** | Some good apps (the *arr family, SABnzbd, BookStack) have no upstream-published image and are normally run from LinuxServer.io. Homestead needs a written policy for trusted community publishers, multi-arch support, tag pinning and update provenance. | *arr, SABnzbd, BookStack |
| F5 | **Catalog bundles and capacity planning** | S172 models a recommended suite but not a general bundle. Owners need "Starter", "Family" and "Media" profiles with combined RAM/storage checks instead of installing twenty apps one at a time. | Capacity honesty |

Supporting and cross-cutting work:

- **Secret delivery for `_FILE`-less apps.** ADR-021 allows secrets only as
  files. The catalog can add apps that read `*_FILE` today (Postgres
  containers, Immich), and apps with owner-set local credentials, but not
  apps that require Homestead to generate and inject a plaintext
  environment secret. F1 owns the decision; every package slice verifies
  the app's `_FILE` support or records an owner-supplied credential path.
- **Integration metadata.** A small manifest section naming the identity,
  file, media or companion relationships (for example `authentik`,
  `jellyfin`, `nextcloud`) would let the dashboard show "works with" and
  let a bundle explain why an app is present. It is metadata only; it
  must not imply that Homestead configures the app's internals.
- **Shared-library UX.** Owner volumes already support an existing
  directory under a class mount (`VolumeSpec.HostPath`). The dashboard
  and docs should make "use the same media library as Jellyfin" a normal
  path rather than a CLI-only detail.
- **Health and start order.** The generator has no `healthcheck` or
  `depends_on` field. Most selected apps restart until their database is
  ready, which is acceptable; an app that needs a true readiness gate is
  out of scope until the schema grows.
- **Backup restore surfaces.** Restore remains package-level and largely
  manual (documented in S275). Adding apps increases the value of a real
  restore surface, but it is not a gate for install.

## 6. Planned list

The list is ordered by waves. A wave is a review and delivery boundary,
not a requirement to install every app in it.

### 6.1 Wave 1 — Everyday suite

| Package | Category | Why | SSO | Data / DB | Integrates with | Notes / blockers |
|---------|----------|-----|-----|-----------|-----------------|------------------|
| `vaultwarden` | passwords | Lightweight Bitwarden-compatible password manager; the highest-value self-hosted service after files and photos. | OIDC (optional; local login works) | SQLite; app data; owner-set admin token | Authentik, backups, edge | Single container, low RAM. The admin token and OIDC client secret are plaintext env today, so a basic install can omit the admin page and use owner signup; automated OIDC waits on F1. Public exposure is possible but should default to local/overlay with a TLS warning. |
| `paperless-ngx` | documents | Scan, OCR, index and archive household documents. | OIDC | Postgres + Redis; bulk document library | Nextcloud (shared scans folder), Authentik, backups | Three services. The Django secret, DB password and admin password are plaintext env today; F1 must provide a delivery path or the owner supplies them. OCR languages and admin bootstrap are env-driven. GHCR image, so F3 matters. |
| `gitea` | development | Private Git forge for owner projects, with issues, packages and CI hooks. Forgejo is an acceptable substitute; pick one, not both. | OIDC | SQLite or Postgres; repository data | Authentik, backups, edge | One container. Route host is used in `ROOT_URL` and SSH clone needs an owner decision; start HTTPS-only. |
| `freshrss` | feeds | Simple RSS/Atom reader; a natural daily-use app. | OIDC (Debian image; the Alpine variant lacks it) | SQLite or Postgres | Authentik, backups | One container. Pin the Debian-based image so OIDC is available. |
| `linkding` | bookmarks | Minimal bookmark manager with tags, search and a browser extension. | OIDC | SQLite | Authentik, backups | One container, very low RAM. |
| `mealie` | recipes | Recipes, meal planning and shopping lists for a household. | OIDC | SQLite or Postgres | Authentik, backups | One container. GHCR image; F3 matters. |
| `uptime-kuma` | monitoring | Monitors Homestead apps and URLs; useful as the "is everything up?" view. | None (local admin) | SQLite | All local apps; notifications later | SSO exception: local/overlay only. Do not add it as a public status page in v1. |

### 6.2 Wave 2 — Family library

| Package | Category | Why | SSO | Data / DB | Integrates with | Notes / blockers |
|---------|----------|-----|-----|-----------|-----------------|------------------|
| `audiobookshelf` | audio | Audiobooks and podcasts with mobile apps and progress sync. | OIDC | SQLite; bulk audiobook/podcast library | Jellyfin library conventions, Authentik, backups | One container. Shared bulk library is an owner volume. GHCR image; F3 matters. |
| `navidrome` | music | Subsonic-compatible music streaming with a large client ecosystem. | None (reverse-proxy header only) | SQLite; bulk music library | Jellyfin (same library), backups | SSO exception: local/overlay. Avoid duplicate music libraries; document the shared mount. |
| `komga` | books/comics | Comics, manga and PDF library with OPDS and a web reader. | OIDC | SQLite; bulk comics library | Jellyfin-adjacent library; Authentik, backups | One container. JVM memory is the main cost. |
| `calibre-web-automated` | books | E-book library with automatic ingest, conversion and a web reader. | None (verify; reverse proxy only) | SQLite + Calibre metadata; bulk ebook library | Nextcloud/Calibre ingest folder, backups | Community image. No native OIDC found; local-only exception. |
| `vikunja` | tasks | Shared to-do lists and projects, with CalDAV support. | OIDC | SQLite or Postgres | Nextcloud calendar (CalDAV), Authentik, backups | One container. |
| `actual-budget` | finance | Local-first household budgeting with bank import and multi-device sync. | OIDC (verify at slice) | SQLite | Backup, edge | One container. If OIDC is not usable, local-only exception. |
| `memos` | notes | Fast private notes with tags, search and a simple API. | OIDC | SQLite or Postgres | Authentik, backups | Complements Nextcloud rather than replacing it. If the owner wants Nextcloud Notes, that is a Nextcloud app decision, not a catalog package. |

### 6.3 Wave 3 — Media automation and wiki

| Package | Category | Why | SSO | Data / DB | Integrates with | Notes / blockers |
|---------|----------|-----|-----|-----------|-----------------|------------------|
| `prowlarr` | media automation | Indexer manager for the *arr apps. | None | SQLite; config | Sonarr, Radarr | LinuxServer image; F4. Local-only. |
| `sonarr` | media automation | TV library automation. | None | SQLite; config; shared media library | Prowlarr, SABnzbd, Jellyfin | LinuxServer image; F4. Library paths must match Jellyfin's. |
| `radarr` | media automation | Movie library automation. | None | SQLite; config; shared media library | Prowlarr, SABnzbd, Jellyfin | LinuxServer image; F4. |
| `sabnzbd` | media automation | Usenet download client; avoids the legal and network complications of public torrent traffic. | None | SQLite/config; downloads library | Prowlarr, Sonarr, Radarr | LinuxServer image; F4. Owner supplies a Usenet provider. |
| `seerr` | media requests | Request portal for Jellyfin with approvals. | None (Jellyfin login) | SQLite | Jellyfin, Sonarr, Radarr | One container. Treat as a LAN service; do not expose publicly. |
| `bookstack` | wiki/docs | Structured wiki for household documentation. | OIDC | MariaDB + uploaded files | Authentik, backups | Blocked on F2 (MariaDB export) and F1 (DB password is plaintext env). LinuxServer image; F4. |

Secret note: unless a row says otherwise, the OIDC client secret in these
apps is a plaintext environment variable, not a `*_FILE` input. That makes
SSO automation depend on F1's delivery decision. Local-auth installs are
not blocked by this; they use owner-set credentials or first-run accounts.

Conditional, not counted in the twenty:

- `qbittorrent` — only after a supported VPN/network-mode design. The
  current schema cannot express `network_mode: service:vpn`, and running
  a public torrent client directly on the appliance's home IP is not a
  default we should ship.
- `collabora` / `onlyoffice` — potential Nextcloud office add-ons, but
  they need Nextcloud app configuration (`richdocuments`) that Homestead
  does not own yet. Revisit after the Nextcloud app-management decision.
- `immich-kiosk` — small and useful, but a companion to an existing app
  rather than a new suite category.
- `ntfy` — a good notification hub for backups and monitoring, but no
  current Homestead notification pipeline consumes it. Infrastructure
  slice first.

### 6.4 Not planned, with reasons

| Candidate | Reason |
|-----------|--------|
| Mailcow, Mailu, docker-mailserver, Maddy | Port 25, PTR/SPF/DKIM/DMARC, blocklist reputation and abuse handling are a different product. Email is the largest operational trap in self-hosting and is not a v1 app. |
| Synapse/Matrix, Element, Rocket.Chat, Mattermost, Zulip | Federation/public exposure, heavy services, and identity duplication with Authentik/Nextcloud. Revisit only with a chat product decision. |
| Home Assistant | Requires host networking or device access for many integrations; the schema forbids both. Needs an ADR and a device-passthrough model. |
| MinIO, Garage | Object storage is infrastructure, not a household app. Could be an internal platform service later. |
| PhotoPrism, Piwigo, Photoview, Lychee, Ente | Immich already owns the photo category. A second gallery splits libraries and backups. |
| Seafile, ownCloud, Syncthing | Nextcloud owns files and sync. Syncthing also lacks SSO and needs discovery ports. |
| Portainer, Dockge, Watchtower, Homepage, Dashy, Homarr, Traefik, Caddy, Nginx Proxy Manager | Platform-owned or directly conflicts with the Homestead dashboard, edge and update engine. |
| Grafana, Prometheus, Loki, Dozzle | Monitoring/logging infrastructure; Homestead doctor, operations and Dockge cover the owner path. Revisit as platform internals, not catalog apps. |
| WordPress, Ghost, WriteFreely | Public CMS/blog workloads bring plugin/theme maintenance and a different exposure posture. Possible later "publish" category. |
| OnlyOffice, Collabora, CryptPad, Grist | Office collaboration overlaps the craft suite and Nextcloud; the missing piece is Nextcloud app wiring, not another package. |
| Stirling PDF, IT-Tools | Useful utilities, but stateless and already covered in spirit by PdfCraft and the browser. Not worth a catalog slot. |
| Karakeep, Wallabag, Miniflux | Linkding and FreshRSS cover the bookmark/feed jobs with simpler stacks. Karakeep adds Chrome and search infrastructure. |
| Firefly III | Actual Budget is lighter and local-first. Firefly also needs MariaDB. |
| Docmost, Outline, Wiki.js | BookStack (or Memos for light notes) covers the need with a clearer OIDC and backup story. Docmost's SSO is edition-sensitive. |
| Cal.com, Rallly, Easy!Appointments | Scheduling needs reliable outbound email, which is out of scope. |
| Baïkal, Radicale | Nextcloud already provides CalDAV/CardDAV. |
| Tube Archivist, Pinchflat, MeTube | Downloaders add copyright/ToS risk, heavy dependencies (Elasticsearch/Redis) and large media growth. Not a default suite app. |
| Passbolt, Bitwarden Server, Psono | Vaultwarden is the lightest path to the same job; running two password servers is not a suite. |
| Puter apps | See section 4.2. Cloud-coupled SPAs; deferred pending a static-app decision. |

## 7. Integration model

The catalog expansion uses four integration levels. Only L1-L3 are
promised by this plan; L4 is owner-configured inside the app unless an
app exposes a stable environment-variable handoff.

| Level | Meaning | Examples |
|-------|---------|----------|
| L1 co-install | Same appliance, same backup/edge/update lifecycle. | Every package |
| L2 shared identity | Authentik OIDC/SAML/LDAP, or a documented local-auth exception. | Vaultwarden, Paperless, Gitea, Linkding, Mealie, Audiobookshelf, Komga, Vikunja, BookStack |
| L3 shared library | A common bulk directory mounted into two apps. | Jellyfin + *arr + Seerr; Audiobookshelf/Komga/Navidrome + a shared media library; Paperless + a scans folder |
| L4 workflow handoff | One app calls another (request → indexer → downloader → library). | Seerr → Sonarr/Radarr → SABnzbd → Jellyfin |

Per integration surface:

- **Authentik.** OIDC is the default recommendation for every app that
  supports it. Local auth remains available for break-glass and for the
  documented exceptions. F1 creates the client and writes credentials;
  until then, SSO is a documented manual step.
- **Nextcloud.** Nextcloud remains the canonical file store. Paperless
  can consume from a shared scans directory; ebooks and media can use a
  shared bulk library. There is no automatic Nextcloud-to-app API
  handoff in this plan.
- **Immich.** Immich remains the only photo library. No second gallery
  is added; `immich-kiosk` is a later companion if wanted.
- **Jellyfin.** Jellyfin remains the playback surface. The *arr apps and
  Seerr share its library conventions and paths; Seerr uses Jellyfin
  login. This is the one place where L4 is realistic because the apps
  are designed to talk to each other.
- **Backups.** Every persistent path is declared. SQLite and Postgres
  exports work today; MariaDB needs F2. Large shared media libraries are
  owner data and should not be duplicated into appliance backups by
  default; the bundle UI must say so.
- **Edge and exposure.** Every package gets a route and a recommended
  exposure. The default is local. Vaultwarden, Paperless, Gitea and
  BookStack can be widened only with explicit warnings; the *arr stack,
  SABnzbd, Seerr and Uptime Kuma stay local/overlay.
- **Dashboard.** The catalog page, install plan and app launch links are
  the owner's entry point. A "related apps" line can be added later from
  integration metadata; it is not a runtime dependency.

## 8. Capacity guidance

Estimates are planning figures, not measurements. Each package slice
records the real requirement in its manifest and tests.

| Profile | Suggested RAM | Packages | Estimated app RAM |
|---------|---------------|----------|-------------------|
| Starter | 8 GB | Nextcloud, Jellyfin, Vaultwarden, Linkding, FreshRSS, Uptime Kuma | 4-5 GB |
| Family | 16 GB | Starter + Immich, Paperless, Mealie, Vikunja, Actual, Audiobookshelf, Navidrome, Gitea, Memos | 10-12 GB |
| Media | 16-32 GB | Starter + *arr (Prowlarr/Sonarr/Radarr/SABnzbd), Seerr, Komga, Calibre-Web Automated | 8-10 GB |
| Full | 32 GB | Family + Media + BookStack; optional qBittorrent with a VPN design | 18-22 GB |

The existing base suite already recommends 1 GB for Nextcloud, 2 GB for
Jellyfin, 4 GB for Immich and roughly 1 GB for Authentik. An 8 GB
appliance should be sold as a starter, not as "run the whole catalog".

## 9. Suggested slicing

Foundation items should be scheduled first; they are prerequisites, not
nice-to-haves. App slices follow the existing one-package-per-slice
pattern (S160/S170/S171) and each one updates the catalog, tests, docs
and `docs/PARITY.md` only if a surface changes.

Indicative order (IDs are assigned when a slice is actually scheduled;
the next free number after S281 is S341):

1. Catalog integration contract (metadata + docs), plus an ADR if the
   manifest schema changes.
2. OIDC client actuation (F1), including the supervised secret-entrypoint
   wrapper and the ADR-021 amendment. This gates Wave 1.
3. GHCR update discovery (F3). Needed by Paperless-ngx and Mealie.
4. Catalog bundles and capacity planning (F5).
5. Wave 1 packages, one slice each: Vaultwarden, Paperless-ngx, Gitea,
   FreshRSS, Linkding, Mealie, Uptime Kuma.
6. MariaDB/MySQL export (F2). Not on the Wave 1 path; required before
   BookStack.
7. Wave 2 packages: Audiobookshelf, Navidrome, Komga, Calibre-Web
   Automated, Vikunja, Actual Budget, Memos.
8. Community-image trust policy (F4), with an ADR. Required before the
   LinuxServer.io packages.
9. Wave 3 packages: Prowlarr, Sonarr, Radarr, SABnzbd, Seerr, BookStack.

Each app slice must answer, in its own manifest and tests:

- image tag, architectures, publisher and update source;
- services, data paths, ownership, database and secrets;
- SSO class and protocols, or the documented exception;
- integration points and whether they are L2, L3 or L4;
- recommended exposure and warnings;
- memory, CPU and storage floors;
- backup and restore story.

## 10. Decisions

Resolved by the owner on 2026-10-23:

1. **Gitea or Forgejo?** Gitea. Forgejo is not packaged.
2. **Actual Budget or Firefly III?** Actual Budget. Firefly III is not
   planned (section 6.4).
3. **How far into media automation?** The full stack: Prowlarr, Sonarr,
   Radarr, SABnzbd and Seerr in Wave 3.
4. **Is OIDC a Wave 1 gate?** Yes. Wave 1 is gated on F1; `uptime-kuma`
   remains the documented local-auth exception inside Wave 1.
5. **Do we want catalog bundles?** Yes. F5 lands before Wave 1.
6. **Do we want a Puter track?** No. Deferred pending a static-app ADR
   (section 4.2).
7. **How do we deliver secrets to apps that only accept plaintext
   environment variables?** Supervised entrypoint wrapper: a 0600 file is
   exported into the app environment at start, under an ADR-021
   amendment carried by F1.
8. **What is the public-exposure posture for Vaultwarden, Gitea and
   BookStack?** Default local/overlay. Widening to the public edge is an
   explicit owner action with a TLS and threat-model warning; no package
   ships publicly exposed by default.
9. **Do we accept LinuxServer.io images?** Yes, under the written F4
   policy (trusted publisher, multi-arch, pinned tags, provenance).

## 11. Sources

- [Awesome Selfhosted](https://github.com/awesome-selfhosted/awesome-selfhosted)
- [Puter/Everything](https://github.com/HeyPuter/everything)
- [Puter/Everything Documents app](https://github.com/HeyPuter/everything/tree/main/src/documents)
- [Catalog schema and loader](internal/catalog/schema.go)
- [Backup contract](docs/slices/S180-backup-contract.md)
- [Catalog data placement](docs/slices/S276-catalog-data-placement.md)
- [Identity provider interface](docs/slices/S202-identity-provider-interface.md)
- [Application SSO metadata](docs/slices/S203-application-sso-metadata.md)
- [SSO setup wizard](docs/slices/S204-sso-setup-wizard.md)
- [Recommended base suite](docs/slices/S172-recommended-base-suite.md)
