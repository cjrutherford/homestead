# S503 - Gate and published-path redirects are temporary (307)

## Goal and dependencies
Unpublishing an app, turning its gate off or moving the shared gate must take effect in browsers that already visited. Depends on S480, S481, S498, ADR-055 and ADR-062.

## Problem
The Caddy redirects rendered for the forward-auth gate (other names to the canonical name) and for the S498 published-path LAN redirect answered 308 (permanent). Browsers cache a 308 indefinitely, so after an unpublish they kept jumping to the old public name, which then answered 404. ADR-060 host apps already used 307.

## Fix
- `internal/docker/gate.go` and `internal/docker/routes.go` render 307 for the gate redirects, the published LAN redirects and the bare-path to trailing-slash redirect. Nothing else in the rendered output changes.
- Decision on the slash-append redirect (`redir /apps/<app> /apps/<app>/`): it is on the same host, but it exists only while the route does and a move or removal would leave a cached 308 pointing at a path that no longer exists, so it is 307 as well. No 308 remains in the rendered Caddy output.
- Comments, the publish plan warning, the owner guide (`docs/guide/exposure.md`) and the acceptance checklist say 307. ADR-055 section 2 and ADR-062 keep their accepted text and carry an "Amended by S503" note.

## Not built
A browser that already cached a 308 keeps it until its cache is cleared; this is a one-time cleanup per browser (clear the site's cached data or use a private window), and Homestead cannot clear it from the server. `docs/guide/troubleshooting.md` has a row for it.

## Verification
Go: `internal/docker/gate_test.go`, `gatepath_test.go`, `lanredirect_test.go`, `pathroutes_test.go` expect 307; `TestRenderedRedirectsAreTemporary` asserts no rendered output contains " 308".
