# S379 - Sudoku as an Everything catalog package

## Goal and dependencies
Twelfth Everything app (ADR-057): `sudoku` (four difficulties, daily puzzle, pencil marks, explained hints,
records) from HeyPuter/everything at the pinned commit `e7d07c48546ef0650a8f3ab94ae82b8b50d3646a`.
Plan row: "Same shape as solitaire". Depends on S367 and S378.

## Source and licence
`third_party/everything/sudoku-source.tar.gz` (53 entries, 1,537,150 bytes, SHA-256
`18d0c4c6dc4d86b5ea7e52cffedf01e1f1339c6e8670dd64a5cd31d80f4cdb1b`), same recipe as the other archives (the
recipe reproduced the Solitaire digest first). **MIT** (repository root; the app has no licence file). Bundled from
npm and served from the app's own origin: `@fontsource/inter` (SIL OFL 1.1, font files) and `lucide` (ISC). Both
permissive; no non-permissive licence, so no DEFERRED licence line. Build-time only: jsdom, vite, vitest.

## SDK surface
| Call | Where | Decision |
| --- | --- | --- |
| `auth.isSignedIn` | `src/net/puter.js`, gate for every sync | supported (always true behind the bridge) |
| `kv.get`, `kv.set`, `kv.del` | `sudoku:stats`, `sudoku:save` | supported, per person and app |
| `kv.MAX_VALUE_SIZE` | size check | undefined in the shim; check skipped, the bridge enforces 8 MB |
| `auth.getUser` | account label | supported; the name is replaced by patch with "Homestead user" |
| `auth.signIn`, `auth.signOut` | account button | unreachable: patch makes the button a label (`hosted = true`) |
| `env` | `isHostedSession` | undefined in the shim, so "web"; patched to hosted in the one place that matters |
| `fs.*`, `ui.*`, `ai`, `net`, `sharing`, `perms`, `events`, `getReadURL` | none | not used; stay refused or absent |

Browser APIs: `localStorage` (settings, records, saved game), a Web Worker (puzzle generator, a bundled
same-origin file, runs under the CSP), a service worker (added by Homestead). No IndexedDB or File System Access.
No new SDK calls: the ADR-057 table, bridge and shim are unchanged.

## Storage design
Same as S378. Upstream keeps records and the game in progress in `localStorage` and merges that copy into the
signed-in person's kv copy, so a second person on a shared browser profile would receive the first person's.
`scripts/everything/files/sudoku-person-storage.js` (added as `src/sudoku/src/homestead/boot.js`) replaces
`localStorage` before the game starts with one whose keys carry a tag (first 8 bytes of SHA-256 of `psud:` plus
the subject); memory only if the person cannot be identified. It also registers the service worker. One
unavoidable exception: the inline theme script in `index.html` reads the real, unprefixed `sudoku.settings.v1`
key before the entry point runs; nothing writes it any more, so it only ever yields the default theme.

## Changes
- `scripts/everything/apps/sudoku.json`: 8 exact-once patches (comment naming the hosted site, "Powered by Puter"
  link, entry point, account label "Homestead user" and no Puter wording; sign-in and sign-out unreachable).
- `scripts/everything/files/sudoku-sw.js` (added as `public/sw.js` through `add_files`; `service_worker: null`,
  because upstream ships none and mode `runtime` needs an upstream file to replace). Never caches `index.html`
  or `_homestead/*`; stores only content-hashed build assets; fixed offline page.
- `catalog/sudoku.yaml`, `pin.yaml`, `THIRD_PARTY.md`, Go catalog and SSO tests, `tests/test_s379_sudoku_package.py`,
  `scripts/everything/gate/run_browser_check_sudoku.sh` and `browser-check-sudoku.mjs`.
- No change to prepare_app.py, the bridge, the shim or the CSP rule.

## Analytics and third-party scripts
None beyond the hosted SDK tag (replaced by the local shim). Fonts are bundled locally, none loaded from a CDN. The
built output has no js.puter.com, plausible, jsdelivr, googleapis or puter.com. Dockerfile guard unchanged.

## Tests
Upstream vitest: 333 passed (16 files), pristine and patched alike. Python package test. Go: catalog rows,
`TestSudokuSignInUsesTheBridgesOwnRoutes`. Real-browser check (headless Chrome 156): CSP-clean boot (including the
generator worker), same-origin requests only, no Puter link or outbound link, a digit placed, the game kept across
reload and written to the appliance, label "Homestead user", a second person (same profile) sees none of the first
person's browser keys or game, the service worker serves the offline page with the server stopped and caches only
build assets.

## Not proven
See `docs/DEFERRED.md` (S379 lines).
