# S345 — Vaultwarden

## Goal
Ship the first Wave 1 package: a Bitwarden-compatible password manager the
owner can install, sign into, and back up without turning on an
administrative surface or exposing it to the internet.

## Dependencies
S342 (OIDC declarations and supervised secret delivery) is complete. The
package uses neither at install: local login is the supported path, and no
generated secret is wired.

## Scope

### Manifest (`catalog/vaultwarden.yaml`)
- One container, `vaultwarden/server:1.34.3` (Docker Hub, so the S190/S343
  update path already covers the tag), exposing `web`.
- **Owner signup.** `SIGNUPS_ALLOWED: "true"` so the first account creates
  the vault. Family members join by invitation. Closing signups later is
  configuration, not a manifest edit.
- **Admin page off by default.** No `ADMIN_TOKEN` is declared and nothing
  is generated: an install must not turn an administrative surface on by
  itself. Enabling it is recorded as deferred work.
- **Domain follows the route.** `DOMAIN: "https://{{route_host}}"` is
  filled at render time, because passkeys and invitation links must match
  the exact URL the browser uses. Without a route the variable is omitted
  and Vaultwarden falls back to its own default, so a direct-IP install
  still works without pretending a hostname.
- **Storage.** The image's `/data` volume (SQLite database, attachments,
  sends, RSA keys) is declared on application storage with uid/gid 1000,
  and `data` is the backup path.
- **Requirements.** 256 MB, one CPU, amd64 and arm64, 512 MiB application
  storage, container port 80. Ports are not published; routes proxy to
  them.
- **Exposure.** `web` recommends `local`, allows wider access with an
  explicit warning that passwords leave the LAN and must sit behind HTTPS
  with a certificate the owner trusts.
- **SSO.** `optional` with `oidc`, plus an `identity` integration naming
  `authentik` (the S341 gate requires the provider statement). Nothing
  actuates it: local login remains the working path.

### Tests
- `TestCuratedVaultwarden` pins the version, image, owner-signup variable,
  absence of `ADMIN_TOKEN`, routed HTTPS domain, `/data` declaration,
  backup path, local exposure with a warning, the identity integration,
  no database export, and a passing evaluation on a sufficient host.
- `curatedPackages`, `TestCuratedSSO`, and `knownImageVolumes` gain the new
  package, image, and posture.

### Bundles
`vaultwarden` is already a curated member of the starter bundle (S344).
Shipping it turns the starter profile's report from two available members
to three; the remaining Wave 1 members stay named as missing.

## Non-goals
- Enabling the admin page, generating `ADMIN_TOKEN`, or automating
  `/admin` bootstrap.
- OIDC client creation for Vaultwarden, or writing `SSO_*` variables.
- SMTP, push notifications, or an external database.
- Public exposure defaults, Tor/onion access, or a Bitwarden mobile
  server URL preset.

## Acceptance Criteria
- The manifest loads, validates, and evaluates cleanly on a 2 GB host with
  application storage.
- Signups are open and no administrative token or page is configured.
- `DOMAIN` uses the rendered route host with HTTPS, and references no
  hard-coded address.
- Data lands on application storage under the package directory, and the
  backup path names it.
- Local exposure is recommended; widening carries a warning.
- Main build remains green and all existing tests pass.

## Required Documentation
- `docs/DEVELOPMENT.md` — package paragraph.
- `docs/SLICE_PLAN.md`, `docs/SLICE_INDEX.md` — schedule moved on.
- `docs/DEFERRED.md` — admin-page enablement; SSO surface wording updated.

## Handoff
S346 `paperless-ngx` follows with both prerequisites in place (S342 secret
export, S343 GHCR discovery). Its shared scans volume with Nextcloud is
the first real `library` integration the S341 metadata will describe.
