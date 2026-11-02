# S375 - Circuits as an Everything catalog package

## Goal and dependencies
Eighth Everything app (ADR-057): `circuits` (schematic capture and PCB layout with a
WebGL2 board view, a Rust/WebAssembly geometry kernel for design rule checks and
copper pours, autorouter, KiCad import, Gerber/drill export, 3D view, part editor)
from HeyPuter/everything at the pinned commit `e7d07c48546ef0650a8f3ab94ae82b8b50d3646a`.
Plan row: "One fs call; hobbyist gap." Depends on S368 to S374.

## Source and licence (MIT)
`third_party/everything/circuits-source.tar.gz` (138 entries, 2,952,550 bytes, SHA-256
`a26240e8f85292e33b7b6980503edfa93b7652e47a4110e0a79e25d4dec4f685`), same recipe as the
other archives (re-run first on inventory and time-tracking; both digests reproduced).
Licence MIT (root `LICENSE`, Puter Technologies Inc.); not AGPL. Runtime dependencies
bundled: earcut (ISC), fflate (MIT), lucide (ISC), rbush (MIT). The committed
`kernel.wasm` is used as shipped; the image build does not run Rust. See
`docs/THIRD_PARTY.md`.

## SDK surface
| Call | Decision |
| --- | --- |
| `fs.write/read/readdir/stat/mkdir/delete` (project files, version snapshots under `Projects/.versions/`) | supported |
| `fs.rename(path, newName)` | new in this slice: bridge operation `rename` (file kind), same folder only, never replaces (409 -> `item_with_same_name_exists`), quota-checked, per person |
| `kv` | supported |
| `auth.isSignedIn/getUser`, `env` | supported; a patch keeps the opaque subject off screen and shows "Homestead user" |
| `ui.*` pickers, `fs.share`, `fs.getReadURL`, `perms`, `ai`, `net` | fail closed; sharing, the Puter picker, desktop open and profile picture are hidden by patches |

## Changes
- `internal/everything/store.go`, `handler.go`: `Store.Rename`, `ErrExists`, the `rename` operation.
- `scripts/everything/puter-bridge.js`: `fs.rename` and the 409 mapping (synced to `images/everything`).
- `internal/everything/static.go`: a per-app `wasm` flag; the page policy adds `'wasm-unsafe-eval'` only when the built app ships a `.wasm` file. JavaScript eval stays refused.
- `scripts/everything/apps/circuits.json`: 19 exact-once patches (Puter wording, outbound links, sharing, picker, user name), `service_worker: null` (upstream has none and the worker mechanism cannot add a file).
- `catalog/circuits.yaml`, `pin.yaml`, `THIRD_PARTY.md`, catalog and SSO tests, `tests/test_s375_circuits_package.py`, ADR-057 table.

## Analytics and third-party scripts
None. The hosted SDK tag is replaced by the local shim; the built output has no
js.puter.com or plausible. The headless run shows only same-origin requests. The
Dockerfile guard is unchanged.

## Tests
Go: rename scoping, no-replace, quota growth, handler 200/409/404/400; wasm policy only
with a `.wasm` file. Python package test. Real-browser check
(`scripts/everything/gate/run_browser_check_circuits.sh`, headless Chrome): CSP-clean
boot, example save, DRC and pours in the kernel worker, rename, reload and reopen,
second-person isolation.

## Not proven
See `docs/DEFERRED.md` (S375 lines): VM 2 and Authentik, upstream tests against the real
bridge, WebGL rendering visuals, 3D/autorouter/KiCad/Gerber/print, no offline page, recents in localStorage.
