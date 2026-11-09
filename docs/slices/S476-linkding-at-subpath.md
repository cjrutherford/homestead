# S476 - Linkding at a subpath

## Goal and dependencies
Linkding serves under `/apps/linkding/` on the dashboard's names and its Authentik OIDC sign-in moves with it (ADR-061). Depends on S471, S479, S475.

## Trial first
The pinned image (1.47.0) ran in a throwaway container on VM 2 with `LD_CONTEXT_PATH=apps/linkding/` (trailing slash, no leading one): `/` answers 404, `/apps/linkding/login/` answers 200 with every asset, manifest and opensearch link under the prefix, static files (favicon, bundle) answer 200 with the right content types, and `/apps/linkding/admin/login/` answers 200.

## What changed
- `catalog/linkding.yaml`: `publish: path`, `serve: path`, `path: /apps/linkding/`, `LD_CONTEXT_PATH: apps/linkding/`. The sign-in manifest is unchanged: origins carry the path, so the redirect URI becomes `<origin>/apps/linkding/oidc/callback/` and the sign-in URL `<origin>/apps/linkding/oidc/authenticate/`.
- Publish fact table: linkding is `path`.

## Live (VM 2)
Linkding was installed with sign-in on, one bookmark by person A, not published. `apps move plan/apply linkding` ran the move. It exposed a defect fixed here: `catalog.MoveEnv` carried only values that name the path with its leading slash or use a route token, so `LD_CONTEXT_PATH: apps/linkding/` was dropped and the app 404ed after the move; it now also matches the path without its leading slash (test in `move_test.go`). VM 2 was repaired with `apps configure linkding --service linkding --set LD_CONTEXT_PATH=apps/linkding/`. Then: owner and person A sign in through Authentik at `https://homestead.lan/apps/linkding/`; person C is denied by Authentik; A's existing bookmark is intact and a new one saves; the local admin (S458) reaches `/apps/linkding/admin/`; `linkding.lan` stopped answering; no public name existed. Doctor has no failures.

- `internal/catalog/move.go`: the move also carries a variable set to the path without its leading slash.

## Tests
`internal/catalog` curated and fact-table tests, `internal/appsso/linkding_test.go` (path plan), `internal/catalog/move_test.go` (move env), `tests/test_s476_linkding_subpath.py`.
