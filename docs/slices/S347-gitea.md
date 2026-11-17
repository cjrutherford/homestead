# S347 — Gitea

## Goal
Ship a private Git forge the owner installs, signs into, and pushes to over
HTTPS through the routed host — with no SSH surface, no database server, and
no configuration file edited inside the container.

## Dependencies
S342 is complete, though this package does not need it: Gitea generates its
own signing keys and internal token into its data volume, so no secret is
declared and no entrypoint supervisor is generated.

## Scope

### Manifest (`catalog/gitea.yaml`)
- **One container.** `docker.io/gitea/gitea:28.2.0` (Docker Hub, verified
  against the v28.2.0 release on 2026-10-23), exposing `web` and listening
  on 3000.
- **Environment-only configuration.** Gitea's `GITEA__section__KEY`
  variables feed `app.ini` at every start, and
  `GITEA__security__INSTALL_LOCK: "true"` skips the web installer. Nothing
  writes or edits a file in the container; the generated `app.ini` lives on
  the data volume.
- **Routed HTTPS root.** `GITEA__server__ROOT_URL:
  "https://{{route_host}}/"` builds clone URLs, webhooks, and links from the
  address the browser uses; the variable is omitted when no route is
  assigned, leaving Gitea's local default. TLS terminates at the proxy, so
  Gitea itself speaks plain HTTP on 3000.
- **HTTPS-only clones.** `DISABLE_SSH: "true"` and
  `START_SSH_SERVER: "false"` keep the SSH server and `ssh://` clone URLs
  off in v1; no SSH port is exposed.
- **Owner registration.** Registration stays open so the first account
  creates the administrator; `REQUIRE_SIGNIN_VIEW: "true"` keeps
  repositories and users private to signed-in accounts. Closing
  registration afterwards is an in-app admin setting, not a manifest edit.
- **Storage and backup.** The image's `/data` volume (SQLite database,
  repositories, LFS objects, attachments, generated `app.ini`) is declared
  on application storage as the git user (uid/gid 1000), and `data` is the
  backup path. A `sqlite` database export names `data/gitea/gitea.db`, so
  scheduled backups use `VACUUM INTO` instead of copying a live database.
- **No declared secrets.** Gitea generates `SECRET_KEY`, `INTERNAL_TOKEN`,
  and the JWT secrets into the backed-up data volume on first start;
  declaring them would add a supervisor without changing behavior.
- **Requirements.** 512 MB, one CPU, amd64 and arm64, 5 GiB application
  storage, container port 3000.
- **Exposure.** `web` recommends `local` and allows wider access with a
  warning that repositories and issues leave the LAN and must sit behind
  HTTPS with a trusted certificate.
- **SSO and integrations.** `optional` OIDC with an `identity` integration
  naming `authentik`; OIDC sources are configured in the app, and local
  accounts keep working.

### Tests
- `TestCuratedGitea` pins the version, image, environment coordinates,
  routed HTTPS root with a trailing slash, HTTPS-only settings, installer
  lock, open registration, private-by-default views, `/data` declaration
  and ownership, absence of secrets and entrypoint overrides, the SQLite
  export, the backup path, local exposure with a warning, the identity
  integration, and a passing evaluation on a sufficient host.
- `curatedPackages`, `TestCuratedSSO`, and `knownImageVolumes` gain the
  package, its OIDC posture, and the image's `/data` volume.
- `tests/test_s347_gitea.py` pins the manifest symbols and the absence of
  the installer path, secrets, and a `:latest` tag.

### Bundles
`gitea` is a curated member of the family and full bundles (S344).
Shipping it turns those reports from "missing" to available; the rest of
each profile's membership is unchanged.

## Non-goals
- SSH clone support, an exposed SSH port, or deployed SSH keys.
- OIDC client creation or writing Gitea authentication sources.
- Forgejo as an alternative, or migrating between the two.
- Postgres/MySQL backends, mail, Actions runners, or package registries.
- Public exposure defaults, host-port publishing, or a TLS front end.

## Acceptance Criteria
- The manifest loads, validates, and evaluates cleanly on a 2 GB host with
  5 GiB of application storage.
- Compose renders no secret mounts and no entrypoint override for the
  service; configuration is entirely environment variables.
- `ROOT_URL` uses the rendered route host with HTTPS and no hard-coded
  address; SSH is disabled in both settings.
- `/data` lands under the package directory on application storage and is
  the backup path; the SQLite export path matches the declared subpath.
- Local exposure is recommended; widening carries a warning.
- Main build remains green and all existing tests pass.

## Required Documentation
- `docs/DEVELOPMENT.md` — package paragraph.
- `docs/SLICE_PLAN.md`, `docs/SLICE_INDEX.md` — schedule moved on.
- `docs/DEFERRED.md` — SSH clone support and the open-registration
  default.

## Handoff
S348 `freshrss` follows, pinned to the Debian-based image so OIDC is
available; its feed database stays on SQLite.
