# S337 — The apps list API and CLI

## Goal
One API for the apps page (ADR-050 §4, §5): the owner's list and the list a
person would see, with CLI parity. No person sessions (S338), no pages (S339).

## Scope
- `GET /api/v1/me/apps`: every installed app with a web address.
- `GET /api/v1/people/{id}/apps`: installed apps with a web address where
  Homestead's record (`people_grants`) grants the person users or admins.
  The owner previews with no Authentik call. A disabled person gets an
  empty list (they cannot sign in); an unknown id is 404.
- Item: `name`, `title`, `icon`, `role`, `url`, sorted by title then name.
  `icon` is the manifest's `icon` name, as in the catalog list. `role` is
  `owner`, `user` or `admin`.
- `url`: on a public dashboard name (`onPublicHost`) and the app published,
  the public address (a path app: `docker.PublicServerName` plus its path
  when `Published`; a host app: its first tunnel name). Otherwise local: a
  path app on the dashboard route's host (`docker.ServedRoutes`), a host
  app on its route host; `https` unless the route's TLS is off.
- Excluded: apps not installed, apps with no edge route (no web address),
  and Authentik, which is the sign-in provider and not grantable. Apps are
  found with `appsso.AppRoutes`, falling back to the craft route lookup.
- Both routes are `ClassSession`; S338 adds the person allowlist.
- CLI: `apps mine [--json]`, `people apps PERSON [--json]` (username or id).
  `--json` is new; no other command had it.
- Dashboard: `myApps()` and `personApps()` on the people service only, for
  PARITY.

## Acceptance
Go tests for grants, public versus local, owner list, CLI output; parity
and the classification test pass.
