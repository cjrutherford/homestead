# S353 — Audiobookshelf

## Goal
Ship the first Wave 2 package: an audiobook and podcast server the owner
installs, points at media already on bulk storage, and listens to from a
phone or browser — with the catalog on application storage, a consistent
SQLite export, and a shared library directory that another app can mount.

## Dependencies
S343 (GHCR update discovery) is complete: the image is pinned on GHCR and
shows updates from the same source as Paperless-ngx and Mealie. S341
(integration metadata) is complete, so the package declares its identity
and shared-library relationships. S342 is not needed: the image reads no
OIDC environment variables and the catalog generates no credential, so no
supervisor or secret is declared.

## Scope

### Manifest (`catalog/audiobookshelf.yaml`)
- **One container.** `ghcr.io/advplyr/audiobookshelf:2.37.1` (GHCR,
  verified against the v2.37.1 release published 2026-09-29 and the image
  config on 2026-10-23), exposing `web` and listening on 80.
- **Image defaults do the work.** The image already sets
  `CONFIG_PATH=/config`, `METADATA_PATH=/metadata`, `PORT=80`, and
  `SOURCE=docker`; `HOST` stays unset so the server binds every interface.
  The manifest overrides no environment variable.
- **Storage.** `/config` (SQLite database, users, libraries, server
  settings) and `/metadata` (covers, item metadata, scan logs, and any
  app-created backups) are application paths. `/metadata/streams` is the
  playback transcode scratch directory and is declared on bulk storage so
  stream files never ride in an application backup. The release image
  declares no `VOLUME` (verified from the image config), so every writable
  path is a Homestead declaration.
- **Media libraries stay owner volumes.** `/audiobooks` and `/podcasts`
  are not declarations: the owner adds them at install as bulk volumes
  with a host path under the bulk mount (the same `volumes` row the
  dashboard and install request already carry). A declaration would place
  the library under `<bulk>/audiobookshelf/` and copy it into every
  backup, so the shared library is referenced, never provisioned. The
  same directory can be mounted into Nextcloud external storage.
- **Storage and backup.** `config` and `metadata` are the backup paths. A
  `sqlite` database export names `config/absdatabase.sqlite`, so scheduled
  and pre-update backups take a `VACUUM INTO` snapshot instead of trusting
  a live database file. `/config` is not marked `database: true`: the file
  copy still carries migrations and any non-database file, and the export
  is authoritative for the database itself.
- **No declared secrets.** `JWT_SECRET_KEY` is generated into
  `serverSettings` inside the SQLite database on first start and restored
  with it, so sessions survive a restore. OIDC credentials are configured
  in the app; declaring them would need an environment input the image
  does not read.
- **Requirements.** 512 MB, one CPU, amd64 and arm64, 2 GiB application
  storage, 10 GiB bulk storage, container port 80.
- **Exposure.** `web` recommends `local` and allows wider access with a
  warning that audiobooks, podcasts, and listening progress leave the LAN
  and must sit behind HTTPS with a trusted certificate.
- **SSO and integrations.** `optional` OIDC with the callback at
  `/auth/openid/callback`, configured in the app's server settings. An
  `identity` integration names `authentik`; a `library` integration names
  `nextcloud` and describes the shared bulk directory. No `oidc:` client
  is declared because the credentials never travel by environment.

### Tests
- `TestCuratedAudiobookshelf` pins the version, image, absent environment
  overrides, absent secrets and entrypoint, all three data declarations
  and their classes, both backup paths, the SQLite export, port 80, local
  exposure with a warning, optional in-app OIDC, the identity and library
  integrations, and a passing evaluation on a sufficient host.
- `curatedPackages`, `TestCuratedSSO`, and `knownImageVolumes` gain the
  package, its OIDC posture, and the image's empty volume list.
- `tests/test_s353_audiobookshelf.py` pins the manifest symbols and the
  absence of media declarations, a `:latest` tag, secrets, and an
  actuated OIDC client.

### Bundles
`audiobookshelf` is a curated member of the family and full bundles
(S344). Shipping it turns those reports from "missing" to available; the
rest of each profile's membership is unchanged.

## Non-goals
- OIDC client creation or writing the app's OpenID Connect settings.
- Per-path backup exclusions inside `/metadata` (app-created backups,
  scan logs) or for the owner media volumes.
- Podcast downloads, library watching defaults, or metadata provider
  credentials.
- Public exposure defaults, host-port publishing, or a TLS front end.
- Migrating an existing Audiobookshelf install or importing a library
  index.

## Acceptance Criteria
- The manifest loads, validates, and evaluates cleanly on a 1 GB host
  with 4 GiB application and 20 GiB bulk free space.
- Compose renders no secret mounts, no entrypoint override, and no
  environment override for the service.
- `/config` and `/metadata` land under the package directory on
  application storage; `/metadata/streams` lands on bulk; neither media
  library is declared.
- The SQLite export path matches the declared `/config` subpath, and both
  application paths are backup paths.
- Local exposure is recommended; widening carries a warning.
- Main build remains green and all existing tests pass.

## Required Documentation
- `docs/DEVELOPMENT.md` — package paragraph.
- `docs/SLICE_PLAN.md`, `docs/SLICE_INDEX.md` — schedule moved on.
- `docs/CATALOG_EXPANSION.md` — shipped catalog count.
- `docs/DEFERRED.md` — in-app OIDC setup and metadata backup exclusions.

## Handoff
S354 `navidrome` follows as the local-auth exception, mounting the same
bulk music directory Jellyfin uses.
