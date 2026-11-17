# S355 — Komga

## Goal
Ship the comic and manga library: the owner points it at a bulk comics
directory, opens the web reader or an OPDS app, and Komga indexes the
collection — with the catalog on application storage, a consistent SQLite
export, and a JVM heap cap so the server cannot crowd the appliance.

## Dependencies
S276 (catalog data placement) is complete, so the image's `/config` volume
and the owner library mount are declared and checked. S341 (integration
metadata) is complete, so the optional OIDC posture and the Authentik
identity relationship are declared. S342 is not needed: Komga's OIDC is a
Spring client registration configured by the owner, not a catalog-actuated
client (see Non-goals). S343 is not needed because the image publishes on
Docker Hub.

## Scope

### Manifest (`catalog/komga.yaml`)
- **One container.** `docker.io/gotson/komga:1.29.1` (Docker Hub,
  verified against the 1.29.1 release published 2026-10-14 and the image
  config on 2026-10-26), exposing `web` and listening on 25600.
- **The heap is capped.** The image sets no maximum heap, so the JVM
  defaults to a quarter of the *host's* RAM — several gigabytes on a
  typical appliance. The manifest sets `JAVA_TOOL_OPTIONS=-Xmx768m`, which
  fits inside the declared 1 GiB (metaspace, the native image libraries,
  and the Lucene index live outside the heap). The image's entrypoint runs
  `java` directly, so the variable takes effect without an entrypoint
  override. Owners with very large libraries raise the cap through
  `homesteadctl apps configure` (setting `JAVA_TOOL_OPTIONS` on the
  `komga` service); the 1 GiB requirement stays the install-time floor.
- **Storage.** `/config` (database.sqlite, tasks.sqlite, sessions.sqlite,
  the Lucene search index, fonts, logs, and any owner `application.yml`)
  is application storage and the backup path. The image declares `/config`
  as its only `VOLUME`.
- **The comics library stays an owner volume.** `/data` is not a
  declaration: the owner adds it at install as a bulk volume with a host
  path under the bulk mount — read-only for reading, writable if Komga's
  import and delete features are wanted. A declaration would place the
  library under `<bulk>/komga/` and copy it into every backup. The same
  directory can be exposed through Nextcloud external storage.
- **Storage and backup.** A `sqlite` export names `config/database.sqlite`,
  so scheduled and pre-update backups take a `VACUUM INTO` snapshot of the
  WAL-mode database instead of trusting the live file. The export is
  authoritative for the main database; the tree copy still carries the
  owner's `application.yml`, the Lucene index, and the tasks/sessions
  databases (both are Flyway-migrated and recreated if missing, so losing
  task history or sessions on restore is not data loss).
- **No declared secrets.** Komga has no bootstrap credential: the first
  visit creates the administrator in the app. OIDC client credentials are
  the owner's to supply, so no secret is generated or logged.
- **Requirements.** 1024 MB, one CPU, amd64 and arm64, 4 GiB application
  storage (the database holds thumbnails and metadata), 10 GiB bulk
  storage, container port 25600.
- **Exposure.** `web` recommends `local` and allows wider access with a
  warning that comics, manga, and reading progress leave the LAN and must
  sit behind HTTPS with a trusted certificate.
- **SSO and integrations.** `optional` OIDC. Komga accepts any Spring
  OAuth2/OIDC client registration in `application.yml` under `/config`
  (backed up with the config tree); the redirect is
  `{baseUrl}/login/oauth2/code/{registrationId}`, and users match by email,
  so the Komga account needs the same email as Authentik. An `identity`
  integration names `authentik`; local accounts stay available. No
  `library` integration is declared: the catalog only permits one on a
  package that places something on bulk storage, and Komga's library is an
  owner volume rather than a provisioned directory.

### Tests
- `TestCuratedKomga` pins the version, image, the single environment
  variable, absent secrets and entrypoint, `/config` with its class, the
  absence of a `/data` declaration, the SQLite export, the backup path,
  port 25600, the 1 GiB requirement that contains the heap, local exposure
  with a warning, optional OIDC, the identity integration, and a passing
  evaluation on a sufficient host.
- `curatedPackages`, `TestCuratedSSO`, and `knownImageVolumes` gain the
  package, its optional-OIDC posture, and the image's `/config` volume.
- `tests/test_s355_komga.py` pins the manifest symbols and the absence of
  a `/data` declaration, a `:latest` tag, and secrets.

### Bundles
`komga` is a curated member of the media and full bundles. Shipping it
turns those reports from "missing" to available; the rest of each
profile's membership is unchanged.

## Non-goals
- Catalog-actuated OIDC clients: Komga reads a Spring client registration
  (issuer, redirect template, client credentials) that the owner supplies;
  issuing those from Authentik is deferred.
- Pre-seeding libraries, users, read lists, or a scan schedule.
- Moving the Lucene index or the sessions/tasks databases to bulk storage.
- Disabling Komga's external metadata providers; that stays an owner
  setting.
- Migrating an existing Komga installation or library index.
- Public exposure defaults, host-port publishing, or a TLS front end.

## Acceptance Criteria
- The manifest loads, validates, and evaluates cleanly on a 2 GiB host
  with 8 GiB application and 20 GiB bulk free space.
- Compose renders no secret mounts, no entrypoint override, and only the
  heap-cap environment variable.
- `/config` lands under the package directory on application storage, and
  `/data` is supplied by the install rather than declared.
- The SQLite export path matches the declared `/config` subpath, and
  `config` is the backup path.
- Local exposure is recommended; widening carries a warning.
- Main build remains green and all existing tests pass.

## Required Documentation
- `docs/DEVELOPMENT.md` — package paragraph.
- `docs/SLICE_PLAN.md`, `docs/SLICE_INDEX.md` — schedule moved on.
- `docs/CATALOG_EXPANSION.md` — shipped catalog count.
- `docs/DEFERRED.md` — Komga OIDC actuation and the one-export limit for
  packages with several SQLite databases.

## Handoff
S356 `calibre-web-automated` follows with the community image whose OIDC
posture is re-verified at slice time or documented as local-only.
