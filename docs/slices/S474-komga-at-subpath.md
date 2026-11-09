# S474 - Komga at a subpath

## Goal and dependencies
Komga serves under `/apps/komga/` on the dashboard's names and its Authentik OIDC sign-in moves with it (ADR-061). Depends on S471, S479, S473.

## Trial first
The pinned image (1.29.1) ran in a throwaway container on VM 2 with `SERVER_SERVLET_CONTEXT_PATH=/apps/komga`: Tomcat reports the context path, the page and its `js/`, `css/`, icons and `manifest.json` answer under the prefix with the right content types, the page sets `window.resourceBaseUrl = '/apps/komga/'` (the client builds its API URLs from it), the root answers 404 and the API answers 401 under the prefix. Unlike Audiobookshelf (S473) the client follows the prefix.

## What changed
- `catalog/komga.yaml`: `publish: path`, `serve: path`, `path: /apps/komga/`, `SERVER_SERVLET_CONTEXT_PATH: "{{route_path}}"`. The sign-in manifest is unchanged: origins already carry the path, so the redirect URI becomes `<origin>/apps/komga/login/oauth2/code/authentik` and the sign-in URL `<origin>/apps/komga/oauth2/authorization/authentik` (Spring's `{baseUrl}` template includes the context path).
- `internal/firstrun/komga.go`: the claim and library calls use the context path; the claim probe falls back to the root for an install made before the move.
- Publish fact table: komga is `path`.

## Live (VM 2)
Komga was installed and not published, with sign-in on and the S452 Comics library. `apps move plan/apply komga` ran the move (config, restart, hosts, edge, sign-in re-plan with the kept token). The old `komga.lan` name stops answering. Owner and person A sign in through Authentik at `https://homestead.lan/apps/komga/` and land on the Comics library (Sample Series), a book's reader opens and page 1 is served as a PNG; person C is denied by Authentik. Doctor has no failures (two warns existed before). No public name existed, so none was removed.

## Tests
`internal/firstrun/komga_test.go`, `internal/catalog` curated and fact-table tests, `tests/test_s474_komga_subpath.py`.
