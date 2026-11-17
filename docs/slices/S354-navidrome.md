# S354 — Navidrome

## Goal
Ship the local-auth music server: the owner points it at the same bulk
music directory Jellyfin already reads, creates the administrator on the
first visit, and streams the collection with Subsonic-compatible apps —
with the catalog on application storage, a consistent SQLite export, and
no SSO client to maintain.

## Dependencies
S276 (catalog data placement) is complete, so the image's two volumes and
the owner music mount are declared and checked. S341 (integration
metadata) is complete, so the shared-library relationship with Jellyfin
is declared. S342 is not needed: Navidrome has no OIDC/SAML/LDAP login,
and S343 is not needed because the image publishes on Docker Hub.

## Scope

### Manifest (`catalog/navidrome.yaml`)
- **One container.** `docker.io/navidrome/navidrome:0.64.2` (Docker Hub,
  verified against the v0.64.2 release published 2026-09-24 and the image
  config on 2026-10-23), exposing `web` and listening on 4533.
- **Image defaults do the work.** The image sets `ND_MUSICFOLDER=/music`,
  `ND_DATAFOLDER=/data`, `ND_CONFIGFILE=/data/navidrome.toml`, and
  `ND_PORT=4533`. The manifest sets one variable, `ND_CACHEFOLDER=/cache`,
  so artwork and transcode caches land on bulk storage instead of the
  backed-up data volume. No config file is written; environment is the
  whole configuration.
- **Storage.** `/data` (SQLite database, users, playlists, play counts,
  generated JWT secrets) is application storage. `/cache` (artwork and
  transcode cache, both regenerable) is bulk. `/music` is an **owner
  volume**: the read-only bulk directory shared with Jellyfin, added at
  install with a host path under the bulk mount. It is deliberately not a
  data declaration: a declaration would collide with the owner volume and
  place the library under `<bulk>/navidrome/`, copying it into every
  backup. The image declares `/music` as a `VOLUME`, so the
  `knownImageVolumes` table grows an `OwnerMounts` marker for paths the
  catalog leaves to the install; the coverage test still verifies every
  other image volume.
- **Storage and backup.** `data` is the backup path. A `sqlite` database
  export names `data/navidrome.db`, so scheduled and pre-update backups
  take a `VACUUM INTO` snapshot of the WAL-mode database instead of
  trusting the live file and its `-wal`/`-shm` companions. The cache is
  regenerable and is not a backup path.
- **No declared secrets.** The JWT signing secrets are generated into the
  database on first start and restored with it. The administrator is
  created on the first visit (the app's own "Create Admin" screen), so no
  credential is seeded, generated, or logged.
- **Requirements.** 256 MB, one CPU, amd64 and arm64, 2 GiB application
  storage, 10 GiB bulk storage, container port 4533.
- **Exposure.** `web` recommends `local` and allows wider access with a
  warning that music libraries, playlists, and listening history leave the
  LAN and must sit behind HTTPS with a trusted certificate.
- **SSO and integrations.** `unsupported` SSO is the documented local-auth
  exception. Navidrome's reverse-proxy header auth needs an edge that
  injects a trusted user header, which Homestead does not do for apps, so
  it stays off. A `library` integration names `jellyfin` and describes the
  shared bulk music directory.

### Tests
- `TestCuratedNavidrome` pins the version, image, the single environment
  variable, absent secrets and entrypoint, `/data` and `/cache` with their
  classes, the absence of a `/music` declaration, the SQLite export, the
  backup path, port 4533, local exposure with a warning, unsupported SSO,
  the library integration, and a passing evaluation on a sufficient host.
- `curatedPackages`, `TestCuratedSSO`, and `knownImageVolumes` gain the
  package, its local-auth posture, and the image's volumes with `/music`
  marked as an owner mount.
- `tests/test_s354_navidrome.py` pins the manifest symbols and the
  absence of a `/music` declaration, a `:latest` tag, and secrets.

### Bundles
`navidrome` is a curated member of the family and full bundles (S344).
Shipping it turns those reports from "missing" to available; the rest of
each profile's membership is unchanged.

## Non-goals
- Reverse-proxy header authentication, OIDC, or any identity-provider
  client.
- Disabling Navidrome's external metadata services (Last.fm, Deezer,
  ListenBrainz) by default; that stays an owner setting.
- Pre-seeding the administrator, users, playlists, or a scan schedule.
- Migrating an existing Navidrome database or library index.
- Public exposure defaults, host-port publishing, or a TLS front end.

## Acceptance Criteria
- The manifest loads, validates, and evaluates cleanly on a 512 MB host
  with 4 GiB application and 20 GiB bulk free space.
- Compose renders no secret mounts, no entrypoint override, and only the
  cache-folder environment variable.
- `/data` lands under the package directory on application storage,
  `/cache` on bulk, and `/music` is supplied by the install rather than
  declared.
- The SQLite export path matches the declared `/data` subpath, and `data`
  is the backup path.
- Local exposure is recommended; widening carries a warning.
- Main build remains green and all existing tests pass.

## Required Documentation
- `docs/DEVELOPMENT.md` — package paragraph.
- `docs/SLICE_PLAN.md`, `docs/SLICE_INDEX.md` — schedule moved on.
- `docs/CATALOG_EXPANSION.md` — shipped catalog count.
- `docs/DEFERRED.md` — reverse-proxy header authentication.

## Handoff
S355 `komga` follows with the JVM heap cap that keeps the comic server
from crowding the appliance.
