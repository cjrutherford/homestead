# Changelog

## 0.0.3

- Update Debian-managed Homestead installations through the CLI or dashboard
  using signed packages, an owner-approved APT preview, and a privileged
  package transaction. The daemon verifies health after restarting and records
  the operation durably.
- Keep source-managed binary updates available and preserve the owner’s
  existing configuration and state during package upgrades.

## 0.0.2

- Apply the supplied Fieldwork supporting identity to the dashboard shell and
  navigation, retaining the existing Homestead corn mark and dashboard theme
  tokens.
- Add monochrome catalog illustrations for package IDs present in the current
  catalog: Nextcloud, Immich, Jellyfin, Paperless-ngx, and Vaultwarden. Other
  packages keep their existing bundled icons.
- Build local Debian packages with the updated dashboard assets for amd64 and
  arm64. No API, CLI, application lifecycle, or network behavior changes.

## 0.0.1

First release. Homestead is a private-cloud appliance you own: a native Go
daemon managed by systemd, a CLI (`homesteadctl`) and an Angular dashboard
that configure the same things through the same API.

- Claim and setup wizard: the first owner claims the appliance, then a guided
  setup covers the host, storage, firewall, networking and domain.
- Curated first-party catalog on Docker Compose: Nextcloud, Immich, Jellyfin,
  Authentik and the craft apps. Compose stays usable without Homestead.
- App sign-in with Authentik, including the Jellyfin SSO plugin, and the
  dashboard's own Authentik sign-in.
- People and groups: apps are closed by default; owners grant access and
  app-admin rights from the People page or the CLI.
- Apps are served under `/apps/<name>/` on the appliance, and on public names
  once exposed.
- Cloudflare tunnel setup with a planned, previewed and audited public
  exposure, the public dashboard behind Authentik, and a Let's Encrypt
  certificate.
- Backups (nightly), and update checks. Platform releases are verified
  against a minisign signature and a rollback counter before they can be
  applied.
- Doctor: every check plus live probes, with guidance for fixing problems.
- Bootstrap and recovery scripts, systemd install and a factory reset for
  preparing a unit for a customer.

### Known limits

- The release is unsigned until the owner signs `SHA256SUMS` offline with
  minisign and publishes `SHA256SUMS.minisig`.
- The embedded HAI release key is a labelled placeholder (Doctor warns about
  it). Import your own key with `homesteadctl updates trust add` meanwhile.
- Single appliance only: no clustering or fleet management.
- Mounted storage only: no formatting or partitioning.
- Catalog is first-party only; releases are not fetched automatically.
- Binaries are for linux amd64 and arm64.
