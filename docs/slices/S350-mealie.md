# S350 — Mealie

## Goal
Ship the household recipe book: recipes with photos and steps, a weekly
meal plan, and a shared shopping list — all in one container whose data
stays on the appliance.

## Dependencies
S343 (GHCR update discovery) is complete: Mealie publishes only on GHCR,
so the release predicate and provenance from that slice apply. S342 is not
needed at install; the package declares no secret and the image entrypoint
runs unchanged.

## Scope

### Manifest (`catalog/mealie.yaml`)
- **One container.** `ghcr.io/mealie-recipes/mealie:v3.29.0` (verified
  against the v3.29.0 release on 2026-10-23 and the GHCR manifest, which
  resolves), exposing `web` on 9000.
- **Seeded administrator.** Mealie creates an administrator on first start
  with the upstream-documented credentials (`changeme@example.com` /
  `MyPassword`). The owner signs in through the routed URL, changes the
  password and e-mail, and creates household accounts from the admin UI.
  Nothing is generated or exported, and the manifest says so instead of
  pretending a secret covers it.
- **Closed signups.** `ALLOW_SIGNUP: "false"` keeps the public registration
  endpoint off; the seeded administrator and later accounts created in the
  UI are the only way in. Reopening is a configure value.
- **Routed base URL.** `BASE_URL: "https://{{route_host}}"` so links and
  exports use the browser address; the variable is dropped when no route
  is assigned and Mealie's local default applies.
- **SQLite.** `DB_ENGINE: sqlite` pins the embedded database rather than
  leaving the choice implicit; the file sits inside the data volume.
- **Storage.** The image's `/app/data` volume (SQLite database, generated
  secrets, uploaded recipe images, app-created backups) is declared on
  application storage. The image runs as root and keeps the volume
  root-owned, so no owner ids are set. `data` is the backup path.
- **No database export.** The SQLite file is inside the backed-up data
  directory, the same file-level contract other SQLite packages use.
- **Requirements.** 512 MB, one CPU, amd64 and arm64, 2 GiB application
  storage, container port 9000.
- **Exposure.** `web` recommends `local` and allows wider access with a
  warning that recipes, plans, and lists leave the LAN and must sit behind
  HTTPS with a trusted certificate.
- **SSO and integrations.** `optional` OIDC with an `identity` integration
  naming `authentik`; local sign-in stays available, and OIDC's user and
  admin group mapping stay in the app's configuration.

### Tests
- `TestCuratedMealie` pins the version, GHCR image, routed base URL,
  SQLite engine, closed signups, absence of secrets and entrypoint
  overrides, the `/app/data` declaration, the backup path, no database
  export, local exposure with a warning, the identity integration, and a
  passing evaluation on a small host.
- `curatedPackages`, `TestCuratedSSO`, and `knownImageVolumes` gain the
  package, its OIDC posture, and the image's `/app/data` volume.
- `tests/test_s350_mealie.py` pins the manifest symbols and the data and
  privacy posture.

### Bundles
`mealie` is a curated member of the family and full bundles (S344).
Shipping it turns those reports from "missing" to available; the rest of
each profile's membership is unchanged.

## Non-goals
- Changing the seeded administrator's credentials from Homestead, or
  generating a first-run secret.
- OIDC client creation or writing `OIDC_*` variables.
- SMTP, invitation e-mail, or password resets by mail.
- PostgreSQL, recipe scrapers beyond the image defaults, or food seeding.
- Public exposure defaults, host-port publishing, or a TLS front end.

## Acceptance Criteria
- The manifest loads, validates, and evaluates cleanly on a 1 GB host with
  4 GiB of application storage.
- `BASE_URL` uses the rendered route host with HTTPS and no hard-coded
  address.
- Signups are closed; the seeded administrator remains the documented
  first sign-in.
- `/app/data` lands under the package directory on application storage and
  is the backup path.
- No secret is declared and no entrypoint override is rendered.
- Local exposure is recommended; widening carries a warning.
- Main build remains green and all existing tests pass.

## Required Documentation
- `docs/DEVELOPMENT.md` — package paragraph.
- `docs/SLICE_PLAN.md`, `docs/SLICE_INDEX.md` — schedule moved on.
- `docs/DEFERRED.md` — the seeded default credentials note.

## Handoff
S351 `uptime-kuma` closes Wave 1 as the documented local-auth exception:
local/overlay only, never a public status page.
