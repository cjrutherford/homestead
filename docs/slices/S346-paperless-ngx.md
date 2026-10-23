# S346 — Paperless-ngx

## Goal
Ship the household document archive: scanned paper becomes a searchable,
tagged library the owner installs once and can back up, with the database,
queue, and secret handling all configured by Homestead rather than by
editing files inside a container.

## Dependencies
S342 (supervised secret export), S343 (GHCR update discovery), and S276
(data declarations) are complete. Paperless is the first shipped package
that needs all three: its image reads only plaintext variables, it lives on
GHCR, and it declares all four of its image volumes.

## Scope

### Manifest (`catalog/paperless-ngx.yaml`)
- **Three services.** `webserver` (the only one exposing `web`), `db`
  (`docker.io/library/postgres:18`), and `broker`
  (`docker.io/valkey/valkey:9-alpine`), following the official v3.3.4
  compose pins.
- **Image.** `ghcr.io/paperless-ngx/paperless-ngx:3.3.4`, verified against
  upstream on 2026-10-23 (released 2026-10-22). The image declares
  `ENTRYPOINT ["/init"]` and no command: s6-overlay starts the web server,
  task workers, and the consume watcher itself.
- **Supervised export.** The image has no `*_FILE` input, so the three
  secrets are delivered per ADR-052: `PAPERLESS_SECRET_KEY_FILE`,
  `PAPERLESS_DBPASS_FILE`, and `PAPERLESS_ADMIN_PASSWORD_FILE` hold the
  file paths, and the generated entrypoint exports the values into
  `PAPERLESS_SECRET_KEY`, `PAPERLESS_DBPASS`, and
  `PAPERLESS_ADMIN_PASSWORD` before exec'ing `/init`. The manifest names
  `/init` as the service entrypoint and adds no command, so the supervisor
  runs exactly what the image would have run.
- **Secrets.** `django_secret` is immutable (rotation would invalidate
  sessions and signed tokens). `db_password` is consumed natively by
  Postgres through `POSTGRES_PASSWORD_FILE` and exported for the
  webserver. `admin_password` is immutable because Paperless reads it only
  when it first creates the superuser: changing the variable later does not
  reset an existing account. The owner can register a chosen value before
  install, or reset a generated one afterwards with
  `manage.py changepassword` (documented in the manifest header).
- **Database.** `db` is declared as a Postgres backup export with database
  and user `paperless` and the `db_password` secret, so scheduled backups
  dump metadata consistently.
- **Storage.** All four image VOLUMEs are declared as the paperless user
  (uid/gid 1000): `data` on application storage, `media` (the document
  library), `consume` (the inbox), and `export` (manual exports) on bulk
  storage. `data` and `media` are backup paths; `consume` is transient and
  `export` is a staging area.
- **Queue.** Valkey's `/data` is declared ephemeral (128 MiB tmpfs): the
  consumer re-reads the consume folder on start, so a lost queue loses no
  documents or metadata.
- **Configuration.** `PAPERLESS_URL: "https://{{route_host}}"` so links and
  CSRF origins match the browser address and drop cleanly when no route
  exists; `PAPERLESS_DBENGINE`, `PAPERLESS_DBHOST`, `PAPERLESS_DBNAME`,
  `PAPERLESS_DBUSER`, and `PAPERLESS_REDIS` are plain service coordinates;
  `PAPERLESS_OCR_LANGUAGE: eng` sets the default OCR language (the image
  already ships eng, deu, fra, ita and spa; extra packs are a configure
  value); `PAPERLESS_ADMIN_USER`/`PAPERLESS_ADMIN_MAIL` name the first
  superuser.
- **Requirements.** 2 GB memory, two CPUs, amd64 and arm64, 2 GiB
  application and 10 GiB bulk storage, container port 8000.
- **Exposure.** `web` recommends `local` and allows wider access with a
  warning that scanned documents leave the LAN and must sit behind HTTPS
  with a trusted certificate.
- **SSO and integrations.** `optional` OIDC with an `identity` integration
  naming `authentik` (local login stays the working path), and a `library`
  integration naming `nextcloud`: Nextcloud can mount the bulk consume
  folder as external storage so scans land where Paperless picks them up.

### Tests
- `TestCuratedPaperless` pins the version, service count, GHCR image,
  `/init` entrypoint, service coordinates, routed HTTPS URL, OCR language,
  all four declared volumes and their classes, the three exported
  variables, the database export, backup paths, local exposure with a
  warning, both integrations, and a passing evaluation on a sufficient
  host.
- `curatedPackages`, `TestCuratedSSO`, and `knownImageVolumes` gain the
  package, its OIDC posture, and verified rows for the paperless image,
  `postgres:18` (VOLUME `/var/lib/postgresql`) and `valkey:9-alpine` (no
  VOLUME).
- `TestCuratedPlansPlaceDataOnMountedStorage` now counts the supervisor
  bind mount a secret-exporting service renders; Paperless is the first
  shipped package that exercises it.
- `tests/test_s346_paperless.py` pins the manifest symbols, the absence of
  plaintext credential variables, and the absence of a `command:` override
  that would replace the s6 startup.

### Bundles
`paperless-ngx` is a curated member of the family and full bundles (S344).
Shipping it turns those reports from "missing" to available; the rest of
each profile's membership is unchanged.

## Non-goals
- OIDC client creation or writing `PAPERLESS_SOCIALACCOUNT_PROVIDERS`.
- Automating the first superuser through a Homestead UI, or reading a
  generated secret back to the owner.
- Language-pack installation at start, SMTP, or email ingestion.
- Sharing the consume folder automatically with Nextcloud; the owner adds
  the read-only volume and Nextcloud external storage as documented.
- Public exposure defaults, host-port publishing, or a TLS front end.

## Acceptance Criteria
- The manifest loads, validates, and evaluates cleanly on a 2 GB host with
  application and bulk storage.
- Compose renders `entrypoint: [/homestead/secret-entrypoint.sh]` with
  `command: [/init]` for `webserver`, and the supervisor exports the three
  values before startup.
- No secret value appears in Compose YAML or plain environment; only file
  paths do.
- All four image volumes are declared, postgres data is marked as a
  database on application storage, and the rest of the library is on bulk
  storage.
- The OCR language and routed HTTPS URL are set; no hard-coded hostname.
- Local exposure is recommended; widening carries a warning.
- Main build remains green and all existing tests pass.

## Required Documentation
- `docs/DEVELOPMENT.md` — package paragraph.
- `docs/SLICE_PLAN.md`, `docs/SLICE_INDEX.md` — schedule moved on.
- `docs/DEFERRED.md` — owner-chosen first-run admin credentials.

## Handoff
S347 `gitea` follows: `ROOT_URL` from the route host, HTTPS-only, and no
SSH in v1. Its database and secret wiring can reuse the Postgres and
export patterns this package established.
