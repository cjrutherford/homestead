# S376 - Parametric as an Everything catalog package

## Goal and dependencies
Ninth Everything app (ADR-057): `parametric`, a feature-based 3D CAD modeller
(replicad and the OpenCascade WebAssembly kernel in a module worker) from
HeyPuter/everything at the pinned commit `e7d07c48546ef0650a8f3ab94ae82b8b50d3646a`.
Plan row: "One fs, one ai (disabled)." Depends on S368 to S375.

## Source and licence
`third_party/everything/parametric-source.tar.gz` (138 entries, 3,163,907 bytes, SHA-256
`4406f14a97a05131d5881739cb30d8886c1ae728e149b7d5d2c0968bcc90dfe4`), same recipe as the
other archives. The app is MIT. It bundles the OpenCascade kernel (`replicad-opencascadejs`,
**LGPL-2.1-only**, about 23 MB `.wasm`) from npm at build time, unmodified and as a
separate file; licence texts are served from `public/licenses/`. See `docs/THIRD_PARTY.md`.

## SDK surface
| Call | Where | Decision |
| --- | --- | --- |
| `fs.write/read/delete/readdir` | parts and crash recovery | supported; recovery moved from browser OPFS to the per-person bridge fs by a patch |
| `fs.rename` | rename a saved part | supported (S375 operation, reused) |
| `kv.get/set` | settings, recents | supported |
| `auth.isSignedIn/getUser`, `env` | account menu | supported; "Homestead user" shown, never the subject |
| `auth.signIn/signOut` | account menu | supported shim (sign-in is Authentik) |
| `fs.getReadURL`, `kv.share*`, `perms.*`, `events.*` | none reached | refused (fail closed) |
| `fs.share*`, `ui.show*Picker`, `ui.*LaunchedWithItems`, `auth.getProfilePicture` | sharing, picker, desktop open | absent; entry points hidden by patches |
| `ai` | assistant panel | disabled: entry points hidden; any remaining path shows "AI is not available on Homestead. Nothing you write is sent anywhere." and sends no request |
| `net` | none | disabled |

## Changes
- `scripts/everything/apps/parametric.json`: 37 exact-once patches (Puter wording, outbound links, sharing, publish, AI off, user name, recovery to the bridge), `service_worker: null`.
- `catalog/parametric.yaml`, `pin.yaml`, `THIRD_PARTY.md`, catalog and SSO tests, `tests/test_s376_parametric_package.py`.
- No Go or bridge change: `fs.rename` and the `'wasm-unsafe-eval'` policy from S375 are reused.

## Analytics and third-party scripts
None. The hosted SDK tag is replaced by the local shim; the built output has no
js.puter.com or plausible; no fonts or CDNs. Dockerfile guard unchanged.

## Tests
Upstream tests (104 pristine and patched; 105 with an ad hoc AI-off test). Python package
test. Real-browser check (`scripts/everything/gate/run_browser_check_parametric.sh`, headless
Chrome): CSP-clean boot with the kernel, extrude create flow, reload and reopen,
no AI request leaves the page, crash recovery stays per person, second-person isolation.

## Not proven
See `docs/DEFERRED.md` (S376 lines): VM 2 and Authentik, sketching and import/export, offline.
