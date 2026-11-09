# S331 — Jellyfin's sign-in button opens under Jellyfin's path

## Goal
ADR-048 (S327). Found live: Jellyfin's "Sign in with Authentik" button
linked to `/sso/OID/start/authentik` at the root of the shared origin, so
it opened the dashboard's "Not found" page. Jellyfin is served at
`/apps/jellyfin/` (ADR-044), and the start path was taken from the sign-in
target's address, which apply sets to the origin only.

## Scope
- The button's start path comes from the package's served path
  (`/apps/jellyfin/sso/OID/start/authentik`), the same path Jellyfin's
  BaseUrl is seeded with. An address with a path is used only for a
  package served on its own name.
- The settings generation moves to `start3`, so an appliance that applied
  sign-in before shows a re-plan; its change line names the rewritten
  plugin settings and the login page button. Applying rewrites the button.

## Rules
AGENTS.md slice rules apply. No new routes, commands or surfaces, so
PARITY is unchanged.

## Acceptance criteria
- Go tests: the start path is under `/apps/jellyfin/` whether the address
  is a local or public origin or empty; a package on its own name keeps
  the root; the branding written on apply links under the path; an older
  record replans with the settings line.
- All Go and Python checks pass.

## Live check
On Jellyfin's login page, from the local and the public address, the
button opens Authentik's authorize page, not the dashboard.
