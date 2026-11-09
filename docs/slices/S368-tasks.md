# S368 — Tasks as an Everything catalog package

## Goal and dependencies
Make Tasks an ordinary catalog package (ADR-057): locally built image, path
route, sign-in through the normal plan. Depends on S367 (bridge, spike), S382
(craft serving), ADR-043 and ADR-053. Not a signed-craft-manifest change.

## What this slice adds
- `docs/adr/ADR-057-everything-apps-as-catalog-packages.md`.
- Generic recipe: `images/everything/Dockerfile` (`ARG APP`), per-app
  `scripts/everything/apps/tasks.json`, `scripts/everything/prepare_app.py`
  (`prepare_tasks.py` is a thin wrapper), `sync-context.sh`, `build-image.sh`.
- `internal/craft/bridge.go`: the `everything.build` operation reusing the craft
  pipeline; `ParseRef` accepts `homestead/everything-`; `BuildSpec` gained
  `File` and `Network`; `internal/docker/build.go` honours them.
- Server: craft `plan` and `build` route to the bridge pipeline for bridge apps
  (same routes, no PARITY change).
- Catalog: `catalog/tasks.yaml`, `BridgeImages()`, `strip_prefix` refused for
  bridge apps, tests in `internal/catalog` and `internal/appsso`.
- Bridge: fail-closed CSP, await-sign-in mode, working shim `auth.signOut`.
- `scripts/install-systemd.sh` ships the context and pinned archives under
  `<catalog>/everything/`.
- Review plan: `docs/reviews/EVERYTHING_REVIEW_PLAN.md`.

## Not changed
`catalog/craft/craft-manifest.yaml`, its signature, `scripts/craft-manifest.sh`
and `images/craft`.

## Tests
`go test ./internal/craft ./internal/catalog ./internal/appsso ./internal/everything ./cmd/everything-bridge`,
`tests/test_s368_tasks_package.py`, `tests/test_everything_source.py`, and the
bridge gate (`scripts/everything/gate/run_gate.sh DIR bridge`).

## Adding the next app (S369+)
Definition JSON, pinned archive and pin entry, `catalog/<app>.yaml`, catalog test
rows, `sync-context.sh`.

## Deferred
See the S368 lines in `docs/DEFERRED.md`.
