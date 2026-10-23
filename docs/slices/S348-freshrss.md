# S348 — FreshRSS

## Goal
Ship a household feed reader that fetches on its own schedule, keeps its
database and reading history on the appliance, and stays reachable only
where the owner puts it.

## Dependencies
S342 is complete. Nothing in this package uses it: the owner completes the
web installer on first visit, which creates the administrator account and
the SQLite database in the data volume, so no credential is declared or
exported. S343 applies only to update discovery for the pinned Docker Hub
tag, which the existing registry path already covers.

## Scope

### Manifest (`catalog/freshrss.yaml`)
- **One container.** `docker.io/freshrss/freshrss:1.30.4`, the plain
  (Debian) tag — the `-alpine` variant has no `mod_auth_openidc`, so only
  the Debian image backs the optional OIDC declaration. Verified against
  the 1.30.4 release (2026-10-21) and the Docker Hub tag.
- **Background refresh.** `CRON_MIN: "*/20"` installs the image's cron job
  so feeds refresh every twenty minutes without a browser open. The image
  default is no cron at all.
- **Internal listener.** `LISTEN: 0.0.0.0:80` pins Apache's listener to the
  container interface the proxy reaches; routes proxy to port 80 and no
  host port is published.
- **Owner setup.** No `FRESHRSS_INSTALL` or `FRESHRSS_USER` block: the
  upstream README recommends the web installer, and automating it would
  put the account password in plain environment for a one-time command
  that cannot change later. The owner opens the routed URL, accepts the
  SQLite defaults, and creates the administrator account.
- **Storage.** The image declares no VOLUME, but it writes
  `/var/www/FreshRSS/data` (SQLite database, user configuration, cached
  articles) and `/var/www/FreshRSS/extensions`. Both are declared on
  application storage as `www-data` (uid/gid 33); `data` is the backup
  path and extensions are re-installable.
- **No secrets, no database export.** Credentials are the owner's own
  account, created through the installer; the SQLite file lives inside the
  backed-up data directory, which is the same file-level contract other
  SQLite packages use.
- **Requirements.** 512 MB, one CPU, amd64 and arm64, 1 GiB application
  storage, container port 80.
- **Exposure.** `web` recommends `local` and allows wider access with a
  warning that subscriptions and reading history leave the LAN and must sit
  behind HTTPS with a trusted certificate.
- **SSO and integrations.** `optional` OIDC with an `identity` integration
  naming `authentik`; local accounts keep working, and no `OIDC_*`
  variable is set until the owner configures the provider.

### Tests
- `TestCuratedFreshRSS` pins the version, Debian image (no `-alpine`), cron
  schedule, listener, both writable paths with `www-data` ownership and
  application class, the single backup path, absence of secrets, database
  export and entrypoint overrides, local exposure with a warning, the
  identity integration, and a passing evaluation on a small host.
- `curatedPackages`, `TestCuratedSSO`, and `knownImageVolumes` gain the
  package, its OIDC posture, and the verified "no image VOLUME" row.
- `tests/test_s348_freshrss.py` pins the manifest symbols, the Debian tag,
  the refresh settings, and the absence of generated credentials.

### Bundles
`freshrss` is a curated member of the family and full bundles (S344).
Shipping it turns those reports from "missing" to available; the rest of
each profile's membership is unchanged.

## Non-goals
- Automated install (`FRESHRSS_INSTALL`/`FRESHRSS_USER`) or a
  Homestead-generated administrator password.
- OIDC client creation or writing `OIDC_*` variables.
- Postgres/MySQL backends, mail notifications, or the API.
- Public exposure defaults, host-port publishing, or a TLS front end.

## Acceptance Criteria
- The manifest loads, validates, and evaluates cleanly on a 1 GB host with
  2 GiB of application storage.
- The pinned image is the Debian tag; no `-alpine` tag ships.
- Cron is configured, so articles refresh without user interaction.
- Both writable directories land under the package directory and are owned
  by the user Apache runs as; `data` is the backup path.
- No secret is declared and no entrypoint override is rendered.
- Local exposure is recommended; widening carries a warning.
- Main build remains green and all existing tests pass.

## Required Documentation
- `docs/DEVELOPMENT.md` — package paragraph.
- `docs/SLICE_PLAN.md`, `docs/SLICE_INDEX.md` — schedule moved on.

## Handoff
S349 `linkding` follows: the simplest package in the wave, one container
with SQLite and an owner-created account.
