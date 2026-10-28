# S330 — Jellyfin asks for each scope once

## Goal
ADR-039. Found live: Jellyfin's "Sign in with Authentik" link redirected
to Authentik with `scope=openid profile openid email profile groups`. The
pinned `jellyfin-plugin-sso` always requests `openid profile` itself and
appends the configured `OidScopes`; the adapter also listed `openid` and
`profile`, so each was named twice.

## Scope
- The Jellyfin adapter writes `OidScopes` as `email` and `groups` only. The
  email and the groups claim (app-admin groups, ADR-043) still arrive.
- The record keeps the plugin version and a settings generation
  (`4.0.0.4+scopes2`). A sign-in applied before differs, so the app shows a
  re-plan whose change line says the plugin settings are rewritten; applying
  rewrites the whole settings file, leaving no stale scopes.
- Plan text for a real plugin version change is unchanged.

## Rules
AGENTS.md slice rules apply. No new routes, commands or surfaces, so
PARITY is unchanged. No migration: the existing column holds the string.

## Acceptance criteria
- Go tests: the generated plugin settings list exactly `email groups`; a
  record without the generation replans and names the settings, not a
  plugin replacement; a current record does not replan.
- All Go and Python checks pass.

## Live check
Open the Jellyfin sign-in link: the Authentik authorize URL names `openid`,
`profile`, `email` and `groups` once each. Approve the re-plan first on an
appliance set up earlier.
