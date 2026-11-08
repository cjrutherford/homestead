# S473 - Audiobookshelf at a subpath: checked, kept on its own name

## Goal and dependencies
ADR-061 section 3: prove Audiobookshelf can serve at `/apps/audiobookshelf/` with its Authentik sign-in before the package changes. Depends on S471, S479, S472. Outcome: it cannot, so the manifest keeps `publish: host`, `publish_reason: no_subpath`.

## Evidence (pinned 2.37.1, live on VM 2)
- Upstream supports `ROUTER_BASE_PATH` (default `/audiobookshelf`): the server mounts its router there and prepends the prefix to unprefixed requests. With `ROUTER_BASE_PATH=/apps/audiobookshelf` (a manifest `serve: path` plus `environment`) the move plan, the redirect URIs under the path and the sign-in re-plan (subfolder `authOpenIDSubfolderForRedirectURLs` = the same prefix) all built and applied.
- The page never loads. `/apps/audiobookshelf/login/` answers 200, but the Nuxt client has `/audiobookshelf/_nuxt/...` and `/audiobookshelf/icon.svg` compiled into its HTML and bundles, so the browser asks the dashboard for `/audiobookshelf/_nuxt/*`, gets the dashboard's HTML (content type text/html), and sits on the spinner. The server has no rewrite for the client (`server/Server.js` only prefixes request URLs); upstream's own comment says ABS "does currently not support subfolders for installation".
- A proxy cannot fix this without rewriting the bundles' JavaScript, which Homestead does not do; serving it at `/audiobookshelf/` on the dashboard name would break the `/apps/<app>/` rule and ADR-044.
- VM 2 was restored: sign-in off, uninstall, app directory removed, reinstall from the reverted manifest at `audiobookshelf.lan`.

## Changes
- `catalog/audiobookshelf.yaml` unchanged except a comment on `publish_reason`.
- `TestAudiobookshelfStaysOnItsOwnName` pins host, reason and no `ROUTER_BASE_PATH`; the publish fact-table fixture already lists host/no_subpath.
- Checklist steps SRV-AUDIOBOOKSHELF-2 and a new result note; `docs/DEFERRED.md` records the finding (the slice ID is not reused).

## Tests
`internal/appsso/audiobookshelf_test.go`, `internal/catalog/publish_test.go`, `tests/test_s473_audiobookshelf_subpath_check.py`.
