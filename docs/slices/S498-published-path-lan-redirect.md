# S498 - Published path apps redirect the LAN to the public address

## Goal and dependencies
Owner decision 2026-11-08: when an app at a path is published, `homestead.lan/apps/<app>/` answers 308 to `https://homestead.<zone>/apps/<app>/`, as Navidrome does through the S480 gate. Depends on S480, S481, S497, ADR-061 and ADR-062.

## Built
- `internal/docker/routes.go`: `Route.redirectsLANPath`; `renderPathHandles` emits a 308 for the bare path and a catch-all handle for the app path on the local site when the app is published, ungated, not a craft app and not the sign-in path. The tunnel entry never redirects, so there is no loop.
- `internal/server/apppublic.go`: the path publish and unpublish plans carry an "Edge change" warning (redirect added or removed; craft apps say nothing changes).
- ADR-062 amendment, owner guide `docs/guide/exposure.md`, checklist steps for Komga, Linkding and Paperless-ngx.

## Not built
A per-client bypass for LAN API clients that cannot follow a 308 (DEFERRED S498).

## Verification
Go tests `internal/docker/lanredirect_test.go`, `internal/server/pathpublic_test.go`; `tests/test_s498_published_path_lan_redirect.py`; live on VM 2: Komga, Paperless-ngx and Linkding answer 308 on the LAN with path and query kept, FigCraft stays 200, the public URLs load, Doctor `--live` has 0 fails.
