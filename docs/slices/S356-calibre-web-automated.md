# S356 — Calibre-Web Automated

## Goal
Ship the self-filing e-book library: the owner drops a book into a shared
ingest folder, Calibre-Web Automated converts it, fetches metadata and a
cover, and files it into a Calibre library that e-readers and the web
reader can use — with the books on bulk storage, the application
databases on application storage with a consistent export, and the
catalog's first `library` integration naming the shared ingest path.

## Dependencies
S276 (catalog data placement) is complete, so all three image volumes are
declared and checked. S341 (integration metadata) is complete, so the
optional OIDC posture, the Authentik identity relationship, and the
Nextcloud shared-library declaration are available. S342 is not needed:
Calibre-Web Automated configures OIDC in its own admin panel from
discovery metadata, not from a catalog-actuated client (see Non-goals).
S343 is not needed because the image publishes on Docker Hub.

## Scope

### Manifest (`catalog/calibre-web-automated.yaml`)
- **One container.** `docker.io/crocodilestick/calibre-web-automated:v4.0.8`
  (Docker Hub; the release is `v4.0.8`, published 2026-09-28, and the tag
  carries the `v`). The image config was verified on 2026-10-26: root
  user, `8083/tcp`, entrypoint `/init` (s6), `CALIBRE_CONFIG_DIR` set to
  `/config/.config/calibre`, and volumes `/config`, `/calibre-library`,
  and `/cwa-book-ingest`. amd64 and arm64 manifests both exist.
- **The image's user is the declared owner.** The image is built on the
  LinuxServer Ubuntu base: s6 starts as root, creates the `abc` service
  user from `PUID`/`PGID`, and drops to it for the web app and the ingest
  watcher. The manifest sets `PUID=1000` and `PGID=1000`, and every
  declared directory carries `uid: 1000`/`gid: 1000` so files are
  writable from the first start. The image's init re-chowns the
  directories anyway, but matching the owner up front keeps a restore
  from handing the app root-owned files.
- **Storage.** Three declarations, one per image volume:
  - `/config` (application): `app.db` (users, settings, OIDC
    configuration), `cwa.db` (ingest and conversion settings),
    `.config/calibre`, `processed_books` (originals CWA keeps after
    processing), `user_profiles.json`, and the conversion scratch
    directory. The service sets `CALIBRE_DBPATH=/config`, so both
    databases sit directly under it.
  - `/calibre-library` (bulk): the Calibre library — `metadata.db` and
    the book folders. The books are the data, so the library is a
    declaration and a backup path, the same posture as Paperless-ngx
    documents and Nextcloud user files.
  - `/cwa-book-ingest` (bulk): the drop folder the ingest service
    watches. Files are removed after they are filed, so it is declared
    (for a predictable path and a stable share) but not a backup path.
    Nextcloud can mount the same directory as external storage; see the
    `library` integration below.
- **Storage and backup.** A `sqlite` export names `config/app.db`, so
  scheduled and pre-update backups take a `VACUUM INTO` snapshot of the
  WAL-mode database instead of trusting the live file. The tree copy
  still carries the rest of `/config` and the whole library. `cwa.db` and
  the library's `metadata.db` are WAL databases copied live (the catalog
  allows one export per package); a restore may need Calibre's database
  restore to rebuild the library catalog, and CWA's settings fall back to
  defaults. The books themselves are plain files and survive.
- **No declared secrets.** The first visit signs in with the app's
  documented default administrator (`admin`/`admin123`) and the owner
  changes the password immediately. OIDC client credentials are the
  owner's to supply in the admin panel, so no secret is generated or
  logged.
- **Requirements.** 2048 MB, two CPUs (Calibre's `ebook-convert` is the
  cost), amd64 and arm64, 8 GiB application storage (the
  `processed_books` originals grow with the library), 10 GiB bulk
  storage, container port 8083.
- **Exposure.** `web` recommends `local` and allows wider access with a
  warning that the library, reading progress, and account settings leave
  the LAN and must sit behind HTTPS with a trusted certificate.
- **SSO and integrations.** `optional` OIDC. Calibre-Web Automated has
  an enhanced OAuth 2.0/OIDC login in its admin panel with auto-discovery
  of the issuer metadata; the generic provider's redirect is
  `/login/generic/authorized`, accounts match by email, and a provider
  group can grant administrator. An `identity` integration names
  `authentik`, and local sign-in stays available. A `library` integration
  names `nextcloud`: the two apps can mount the same bulk ingest
  directory, so books dropped through Nextcloud are converted and filed.
  This is the catalog's first package where the shared path is the ingest
  folder rather than the library itself.

### Tests
- `TestCuratedCalibreWebAutomated` pins the version, image, the two
  environment variables, the absent secrets and entrypoint, all three
  data targets with their classes, subpaths, and the 1000:1000 owner, the
  backup paths (settings and library, not the transient ingest folder),
  the SQLite export, port 8083, the 2 GiB requirement, local exposure
  with a warning, optional in-app OIDC, both integrations, and a passing
  evaluation on a sufficient host.
- `curatedPackages`, `TestCuratedSSO`, and `knownImageVolumes` gain the
  package, its optional-OIDC posture, and the image's three volumes.
- `tests/test_s356_calibre_web_automated.py` pins the manifest symbols,
  the PUID/PGID ownership, the data placement, the backup paths, the
  absence of a `:latest` tag and secrets, and both integrations.

### Bundles
`calibre-web-automated` is a curated member of the media and full
bundles. Shipping it turns those reports from "missing" to available; the
rest of each profile's membership is unchanged.

## Non-goals
- Catalog-actuated OIDC clients: the provider metadata URL, client id,
  and secret are entered in the app's admin panel and stored in
  `app.db`; issuing them from Authentik is deferred.
- Migrating an existing Calibre-Web or Calibre-Web Automated installation,
  or importing a pre-existing Calibre library from outside the declared
  bulk directory.
- Pre-seeding libraries, users, shelves, or Kobo/KOSync devices.
- Configuring metadata providers, email delivery, or Send-to-Kindle; those
  stay owner settings in the app (and `HARDCOVER_TOKEN` can be set with
  `homesteadctl apps configure`).
- Per-path backup exclusions inside `/config` (for example
  `processed_books`), and a second database export.
- Public exposure defaults, host-port publishing, or a TLS front end.

## Acceptance Criteria
- The manifest loads, validates, and evaluates cleanly on a 4 GiB host
  with 16 GiB application and 20 GiB bulk free space.
- Compose renders only `PUID`/`PGID`, the three declared binds, and no
  secret mounts or entrypoint override.
- `/config` lands on application storage under the package directory;
  the library and ingest folder land on bulk.
- The SQLite export path matches the declared `/config` subpath, and
  `config` and `library` are the backup paths.
- Local exposure is recommended; widening carries a warning.
- Main build remains green and all existing tests pass.

## Required Documentation
- `docs/DEVELOPMENT.md` — package paragraph.
- `docs/SLICE_PLAN.md`, `docs/SLICE_INDEX.md` — schedule moved on.
- `docs/CATALOG_EXPANSION.md` — shipped catalog count.
- `docs/DEFERRED.md` — Calibre-Web Automated OIDC actuation, the
  live-copied `cwa.db`/`metadata.db` under the one-export limit, and the
  `processed_books` growth.

## Handoff
S357 `vikunja` follows with the task manager whose CalDAV support points
at Nextcloud.
