# S475 - Paperless-ngx at a subpath

## Goal and dependencies
Paperless-ngx serves under `/apps/paperless-ngx/` on the dashboard's names and its Authentik OIDC sign-in moves with it (ADR-061). Depends on S471, S479, S474.

## Trial first
The pinned image (3.3.4) ran in a throwaway container on VM 2 (Valkey beside it, SQLite) with `PAPERLESS_FORCE_SCRIPT_NAME=/apps/paperless-ngx` and `PAPERLESS_STATIC_URL=/apps/paperless-ngx/static/`: the root redirects to `/apps/paperless-ngx/accounts/login/`, static files answer under the prefix with the right content types (`base.css` as text/css, `main.js` as JavaScript), a form login lands on `/apps/paperless-ngx/dashboard`, the page carries `<base href="/apps/paperless-ngx/">` and the Angular bundles load from `/apps/paperless-ngx/static/frontend/...`, and the API answers 200 under the prefix.

## What changed
- `catalog/paperless-ngx.yaml`: `publish: path`, `serve: path`, `path: /apps/paperless-ngx/`, `PAPERLESS_FORCE_SCRIPT_NAME: "{{route_path}}"`, `PAPERLESS_STATIC_URL: "{{route_path}}/static/"`. `PAPERLESS_URL` stays scheme and host. The sign-in manifest is unchanged: origins carry the path, so the redirect URI becomes `<origin>/apps/paperless-ngx/accounts/oidc/authentik/login/callback/` and the sign-in URL `<origin>/apps/paperless-ngx/accounts/login/`.
- Publish fact table: paperless-ngx is `path`.

## Live (VM 2)
Paperless-ngx was installed with sign-in on and three documents, not published. `apps move plan/apply paperless-ngx` ran the move (config, restart, hosts, edge, sign-in re-plan with the kept token); `paperless-ngx.lan` stopped answering. Owner and person A sign in through Authentik at `https://homestead.lan/apps/paperless-ngx/`; person C is denied by Authentik; the three documents are intact and a PDF uploaded by A was consumed and found by search. No public name existed, so none was removed. Doctor has no failures.

## Tests
`internal/catalog` curated and fact-table tests, `internal/appsso/paperless_test.go` (path plan), `tests/test_s475_paperless_subpath.py`.
