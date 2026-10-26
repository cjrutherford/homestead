# S312 — Public names the edge certificate covers; apps that set themselves up

## Goal
Two failures from the owner's VM 2 acceptance run (2026-10-26):

1. **Login through the tunnel was impossible.** The Authentik public name
   became `auth.homestead.<zone>`. Cloudflare's Universal SSL covers only
   the zone apex and names one label below it (`*.<zone>`), so Cloudflare
   rejected the TLS handshake, and the dashboard's sign-in redirect could
   never load. Homestead planned and approved a name that could not work.
2. **Nextcloud sign-in "failed" after a successful install.**
   `app.sign-in` refused with `ErrNextcloudNotSetUp`, because Nextcloud's
   first-run admin setup is manual.

## Scope

### Edge certificate coverage (owner decision, 2026-10-26)
Names two levels deep (`auth.homestead.<zone>`, `<app>.homestead.<zone>`)
are a supported, deliberate choice; the demo uses them. Do not refuse by
depth and do not rewrite the owner's domain or defaults. Instead the plan
checks whether Cloudflare will serve a certificate for each public name and
says so before approval.

- One shared daemon check, used by every plan that publishes a name (tunnel
  setup, public app names, any other tunnel CNAME). A name is covered when:
  it is the zone apex or one label below it (Universal SSL); or an active
  certificate pack on the zone lists it exactly or by a single-label
  wildcard (`GET /zones/{id}/ssl/certificate_packs`); or Total TLS is on
  (`GET /zones/{id}/acm/total_tls`).
- An uncovered name is a **warning** in the permit, not a refusal; the
  owner may approve anyway. Text: "Cloudflare has no certificate for
  auth.homestead.example.com yet: browsers will fail to connect. Names more
  than one level below example.com need Advanced Certificate Manager on the
  zone (a certificate for *.homestead.example.com, or Total TLS). Names one
  level below, such as auth.example.com, are covered for free."
- If the token cannot read coverage, the warning says so, names the
  permission (Zone, SSL and Certificates, Read) and applies the Universal
  SSL rule only. Never fail the plan for this. INSTALL.md lists the
  permission as recommended.
- Tunnel and app public-address status show coverage as a lamp
  ("certificate covers it" / "no certificate yet").
- INSTALL.md (Domain/tunnel step and troubleshooting): the rule, the
  Advanced Certificate Manager option for deeper names, and the TLS
  handshake symptom.

### Apps that need a first run
- Nextcloud installs fully set up. The package generates an admin
  password as a package secret, using the existing `secrets:` mechanism,
  and passes `NEXTCLOUD_ADMIN_USER` and `NEXTCLOUD_ADMIN_PASSWORD_FILE` (or
  the image's supported equivalent) so the image auto-installs on first
  start.
- The owner can read the admin credential where other package secrets are
  shown (Secrets page and `homesteadctl`), with the same reveal rules.
- The sign-in operation waits, bounded, for Nextcloud's install to finish
  before applying. `ErrNextcloudNotSetUp` stays for an existing manual
  install.
- Where any app sign-in still cannot proceed because the app is not set
  up, the operation and the dashboard say what to do, link the app's
  address, and offer **Apply sign-in again**, instead of a bare failed
  operation.
- Existing Nextcloud installs are untouched: env-based auto-install only
  runs on an empty install.
- Catalog signing: if the catalog packages are digest-pinned or signed,
  follow the existing procedure for package changes. Never touch
  `catalog/craft/craft-manifest.yaml`.

## Rules
AGENTS.md slice rules apply.
- Write an ADR only if a principle changes (none is expected).
- Update `docs/PARITY.md` if a route, command or surface changes.
- Update `docs/DEFERRED.md` for anything left out.

## Acceptance Criteria
- Go tests for the depth check cover: apex, one level, two levels, a
  domain below its zone, and case and trailing dot.
- Tunnel and public-app plan tests show the refusal and the
  never-failing defaults.
- A package test checks that Nextcloud's admin secret and environment are
  rendered. A sign-in test covers the bounded wait and the guided failure.
- Go, Python and dashboard tests pass; build and embed succeed.
