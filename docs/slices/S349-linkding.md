# S349 — Linkding

## Goal
Ship the smallest useful package in the wave: one container that saves,
tags, searches, and archives bookmarks, with a first sign-in the owner can
complete without editing files inside the container.

## Dependencies
S342 (supervised secret export) is complete and used: Linkding reads only a
plaintext `LD_SUPERUSER_PASSWORD`, so the initial password arrives as a
0600 file and the generated supervisor exports it before the image's own
startup script runs. S343 applies to update discovery for the pinned Docker
Hub tag, which the existing registry path already covers.

## Scope

### Manifest (`catalog/linkding.yaml`)
- **One container.** `docker.io/sissbruecker/linkding:1.47.0` (Docker Hub,
  verified against the v1.47.0 release on 2026-10-23), exposing `web` on
  9090.
- **Startup preserved.** The image has no `ENTRYPOINT`, only
  `CMD ["./bootstrap.sh"]`, which runs migrations, creates the initial
  superuser, and starts uwsgi. The manifest names that script as the
  service entrypoint so the supervisor can export secrets first and then
  exec it unchanged; no `command:` override is added.
- **Superuser password as a secret.** `LD_SUPERUSER_NAME: admin` creates
  the account once, and `LD_SUPERUSER_PASSWORD_FILE` carries the path the
  supervisor reads; the value is exported into
  `LD_SUPERUSER_PASSWORD` (ADR-052). The secret is immutable because
  Linkding never updates an existing account from the variable. The owner
  can register a chosen value before install, or reset it later with
  `manage.py changepassword` inside the container.
- **Routed CSRF origin.** `LD_CSRF_TRUSTED_ORIGINS:
  "https://{{route_host}}"` keeps logins and bookmark saves working when
  the browser origin is HTTPS and uwsgi is plain HTTP behind the proxy; the
  variable is dropped when no route exists.
- **Storage.** The image declares no VOLUME, but everything lives in
  `/etc/linkding/data` (SQLite database, generated secret key, favicons,
  previews, page archives). It is declared on application storage as
  `www-data` (uid/gid 33, which the bootstrap script enforces) and is the
  backup path.
- **No database export.** The SQLite file sits inside the backed-up data
  directory, the same file-level contract the other SQLite packages use.
- **Requirements.** 256 MB, one CPU, amd64 and arm64, 1 GiB application
  storage, container port 9090.
- **Exposure.** `web` recommends `local` and allows wider access with a
  warning that bookmarks and archived pages leave the LAN and must sit
  behind HTTPS with a trusted certificate.
- **SSO and integrations.** `optional` OIDC with an `identity` integration
  naming `authentik`; the note records that OIDC matches users by email, so
  the admin account needs one before the first OIDC login.

### Tests
- `TestCuratedLinkding` pins the version, image, `./bootstrap.sh`
  entrypoint, routed CSRF origin, superuser name, the exported immutable
  password secret (and the absence of a plain variable), the single
  supervisor, the data declaration and ownership, the backup path, no
  database export, local exposure with a warning, the identity
  integration, the rendered supervisor plus `./bootstrap.sh` command, and
  a passing evaluation on a small host.
- `curatedPackages`, `TestCuratedSSO`, and `knownImageVolumes` gain the
  package, its OIDC posture, and the verified "no image VOLUME" row.
- `tests/test_s349_linkding.py` pins the manifest symbols and the secret
  and CSRF wiring.

### Bundles
`linkding` is a curated member of the family and full bundles (S344).
Shipping it turns those reports from "missing" to available; the rest of
each profile's membership is unchanged.

## Non-goals
- OIDC client creation or writing `LD_ENABLE_OIDC`/`LD_OIDC_*`.
- A Homestead UI that reads back or resets the generated password.
- Archiving configuration beyond the image defaults, or PostgreSQL.
- Public exposure defaults, host-port publishing, or a TLS front end.

## Acceptance Criteria
- The manifest loads, validates, and evaluates cleanly on a 1 GB host with
  2 GiB of application storage.
- Compose renders the supervisor as entrypoint and `./bootstrap.sh` as
  the command; the image startup is otherwise unchanged.
- No secret value appears in Compose YAML or plain environment; only the
  file path does.
- `LD_CSRF_TRUSTED_ORIGINS` uses the rendered route host with HTTPS and no
  hard-coded address.
- Data lands under the package directory on application storage and is the
  backup path.
- Local exposure is recommended; widening carries a warning.
- Main build remains green and all existing tests pass.

## Required Documentation
- `docs/DEVELOPMENT.md` — package paragraph.
- `docs/SLICE_PLAN.md`, `docs/SLICE_INDEX.md` — schedule moved on.
- `docs/DEFERRED.md` — the first-run credential note now names both
  secret-backed owner logins.

## Handoff
S350 `mealie` follows on the GHCR image path (S343), with its own
database and secret wiring.
