# S351 — Uptime Kuma

## Goal
Close Wave 1 with the appliance's own health board: one page that says
whether the installed apps answer, kept strictly on the local network so
monitor credentials and LAN probes never become a public status page.

## Dependencies
S342 is complete and deliberately unused here: Uptime Kuma is the
documented SSO exception, the owner creates the administrator account in
the app on first visit, and nothing is generated or exported. S344's
starter bundle becomes complete with this package, which is why the
bundles test that expected missing Wave 1 members changes in this slice.

## Scope

### Manifest (`catalog/uptime-kuma.yaml`)
- **One container.** `docker.io/louislam/uptime-kuma:2.5.7` (Docker Hub,
  verified against the 2.5.7 release of 2026-10-23), exposing `web` on
  3001.
- **No environment overrides.** The image sets
  `UPTIME_KUMA_IS_CONTAINER=1` and listens on 3001; the administrator
  account is created through the application's own first-run screen.
  There is no secret to deliver, so the image entrypoint
  (`dumb-init` before the Node process) runs unchanged.
- **Storage.** The image's `/app/data` volume (SQLite database, uploaded
  status-page assets, monitor history) is declared on application storage.
  The image runs as root, so no owner ids are set; `data` is the backup
  path and there is no separate database export.
- **Local only, by design.** `web` recommends `local` and sets
  `allowwider: false`: widening is refused with the S351 exception text
  rather than merely warned about. The app holds monitor credentials and
  probes LAN addresses, so it must never be published as a status page.
- **SSO.** `unsupported`, the documented local-auth exception. No
  `identity` integration is declared, because there is nothing to
  integrate.
- **Requirements.** 1 GB memory, one CPU, amd64 and arm64, 2 GiB
  application storage, container port 3001. The memory floor matters: the
  image bundles Chromium and an embedded MariaDB for browser monitors.

### Tests
- `TestCuratedUptimeKuma` pins the version, image, absence of secrets and
  entrypoint overrides, `/app/data` declaration, backup path, no database
  export, local exposure that refuses widening, the unsupported SSO class,
  no integrations, a passing evaluation on a sufficient host, and a
  failing one below the memory floor.
- `curatedPackages`, `TestCuratedSSO`, and `knownImageVolumes` gain the
  package, its unsupported posture, and the verified "no image VOLUME"
  row.
- `TestCheckBundleGradesAvailableMembers` now asserts the starter profile
  is complete, and `TestCheckBundleNamesMissingMembers` keeps the missing
  path covered with the family bundle.
- `tests/test_s351_uptime_kuma.py` pins the manifest symbols and the
  exposure refusal.

### Bundles
`uptime-kuma` completes the starter, family, and full profiles' Wave 1
membership. The family and full reports still name later-wave members as
missing, so the incomplete-bundle path stays exercised.

### Documentation
- `docs/CATALOG_EXPANSION.md` records that Wave 1 shipped and the catalog
  now holds 24 packages.
- `docs/DEVELOPMENT.md`, `docs/SLICE_PLAN.md`, `docs/SLICE_INDEX.md`
  carry the package and the next slice.

## Non-goals
- Public status pages of any kind; widening is refused, not warned.
- SSO, an identity integration, or OIDC client creation.
- Notification channels, SMTP, or webhooks configured by Homestead.
- Postgres/MariaDB backends, heartbeat retention tuning, or browser
  monitor setup.
- Host-port publishing or a TLS front end.

## Acceptance Criteria
- The manifest loads, validates, and evaluates cleanly on a 2 GB host with
  4 GiB of application storage, and fails on a 512 MB host.
- `allowwider` is false and the refusal text explains the exception.
- SSO is unsupported and no integration is declared.
- `/app/data` lands under the package directory on application storage and
  is the backup path.
- No secret is declared and no entrypoint override is rendered.
- The starter bundle reports complete; the family bundle still reports
  its missing members by name.
- Main build remains green and all existing tests pass.

## Handoff
Wave 1 is complete. S352 (`docs/SLICE_PLAN.md`) moves to the second
foundation: MariaDB/MySQL backup export (F2), required before BookStack.
