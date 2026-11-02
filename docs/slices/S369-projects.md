# S369 — Projects as an Everything catalog package

## Goal and dependencies
Second Everything app (ADR-057): `projects` from HeyPuter/everything at the
pinned commit `e7d07c48546ef0650a8f3ab94ae82b8b50d3646a`. Depends on S368.

## Source and licence
`third_party/everything/projects-source.tar.gz` (137 entries, SHA-256
`368795cb9e68e8e8fb70095459b2586b409c3a816bf5fb6cba1031881748b981`), pin entry
in `pin.yaml`, licence MIT. See `docs/THIRD_PARTY.md`.

## SDK surface
| Call | Used for | Bridge |
| --- | --- | --- |
| `kv.get/set` | small settings | supported |
| `fs.write/read/delete` | project documents, attachments, snapshots | supported |
| `fs.stat` | conflict stamp (`modified:size`) | added (S369) |
| `fs.readdir` | project list, snapshots | added (S369) |
| recursive `fs.delete` | delete a project and its folders | added (S369) |
| `auth.*`, `env` | sign-in state | supported; `auth.signIn` redirects |
| `fs.getReadURL`, `kv` sharing, `perms`, `events`, `appID` | not needed | refused |
| `ui.showOpenFilePicker`, `showSaveFilePicker` | cloud-drive picker | absent; the app falls back to local files |
| `ai`, `net`, sharing | not offered | disabled |

No source patches are needed.

## What this slice adds
- Bridge: `stat`, `readdir`, `deletetree` operations, a `modified` column (added to
  older databases), file `put` returning size and stamp; shim `fs.stat`,
  `fs.readdir`, recursive delete, `fs.write` result with size and stamp.
- Service worker mode `runtime` in `prepare_app.py` with `projects-sw.template.js`
  (Projects has no upstream build plugin that fills the worker); navigation stays
  network-only.
- `scripts/everything/apps/projects.json`, `catalog/projects.yaml`, catalog and
  SSO test rows, `sync-context.sh` copies `*-sw.template.js`.
- The gate gained a folder test (8/8); `run_browser_check_projects.sh` is an
  optional real-browser check (skips without Chrome).

## Tests
Upstream suite 487/487 in the pristine tree. `internal/everything/files_test.go`,
`tests/test_s369_projects_package.py`, `scripts/everything/test_projects_sw.cjs`,
the bridge gate and the real-browser check (boot under the CSP, create a project and
task, reload, restore point, offline, second person isolated).

## Unproven
The upstream tests were not run against the real bridge (they inspect mock state
synchronously). No run on VM 2, no sign-in through Authentik, no multi-tab
conflict test on the bridge, and the 8 MiB attachment limit is documented but only
the bridge quota is enforced.

## Deferred
See the S369 lines in `docs/DEFERRED.md`.
