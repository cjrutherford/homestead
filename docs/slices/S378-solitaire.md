# S378 - Solitaire as an Everything catalog package

## Goal and dependencies
Eleventh Everything app (ADR-057): `solitaire`, Klondike solitaire (draw one or three, undo,
statistics, a Vegas-style bank) from HeyPuter/everything at the pinned commit
`e7d07c48546ef0650a8f3ab94ae82b8b50d3646a`. Plan row: "Cheapest possible package". Depends on S368 to S377.

## Source and licence
`third_party/everything/solitaire-source.tar.gz` (79 entries, 9,631,266 bytes, SHA-256
`1ca610831752eca35a833dce4a87a78bd9932dd96af8a487a57c83bbe34a25b2`), same recipe as the other
archives. **MIT** (repository root; the app has no licence file). The vendored card deck is The
Unlicense. No npm runtime dependencies. No non-permissive licence, so no DEFERRED licence line.

## SDK surface
| Call | Where | Decision |
| --- | --- | --- |
| `auth.isSignedIn` | `services/puter-sync.js` gate for every sync | supported (always true behind the bridge) |
| `kv.get`, `kv.set` | `savedGame` and `stats` keys | supported, per person and app |
| `kv.del` | clears the saved game | supported (already in the shim) |
| `kv.MAX_VALUE_SIZE` | size check | undefined in the shim; the check is skipped, the bridge enforces 8 MB |
| `auth.getUser` | entry point only: hashed into a per-person tag; never shown | supported |
| `fs.*`, `ui.*`, `ai`, `net`, `sharing`, `perms`, `events`, `getReadURL` | none | not used; stay refused or absent |

Browser APIs: `localStorage` (saved game, statistics, mode, device id) and a service worker. No IndexedDB,
File System Access or other API. No new SDK calls, so the ADR-057 table is unchanged and the bridge and
shim are unchanged.

## Storage design
The app's own cloud sync already writes the saved game and statistics to the person's kv. But its local
copy is in `localStorage`, shared by everyone on a browser profile, and upstream merges that copy into the
signed-in person's cloud copy, so a second person would receive the first person's statistics and game.
The entry point (`scripts/everything/files/solitaire-person-storage.js`, added as
`src/solitaire/src/homestead/boot.js`) therefore replaces `localStorage` before the game starts with one
whose keys carry a tag (first 8 bytes of SHA-256 of `psol:` plus the subject). If the person cannot be
identified, storage is in memory only. Per-person data lives on the appliance (kv, backed up) with a
tagged cache in the browser. Where the app shows a user: it never does, so "Homestead user" is not needed.

## Changes
- `scripts/everything/apps/solitaire.json`: 11 exact-once patches (hosted-site addresses, the "Powered by Puter"
  link, entry point, immediate service-worker registration).
- `scripts/everything/solitaire-sw.template.js`: Homestead's worker (runtime mode, as Projects), replacing upstream's
  `public/sw.js`, which cached `index.html` (the per-session token) by network-first and every same-origin GET.
- `catalog/solitaire.yaml`, `pin.yaml`, `THIRD_PARTY.md`, Go catalog and SSO tests, `tests/test_s378_solitaire_package.py`.
- No change to prepare_app.py, the bridge, the shim or the CSP rule (the page needs no wasm).

## Analytics and third-party scripts
None shipped beyond the hosted SDK tag (replaced by the local shim). Fonts: none loaded. The built output has no
js.puter.com, plausible, jsdelivr or puter.com. Dockerfile guard unchanged.

## Tests
Upstream vitest: 209 passed (13 files), pristine and patched alike. Python package test (9). Go: catalog rows,
`TestSolitaireSignInUsesTheBridgesOwnRoutes`. Real-browser check
(`scripts/everything/gate/run_browser_check_solitaire.sh`, headless Chrome 156): CSP-clean boot, same-origin
requests only, no Puter link or outbound link, a move played, the saved game kept across reload and written to
the appliance, a second person (same profile) sees none of the first person's browser keys or game, the service
worker serves the offline page with the server stopped and caches only build assets.

## Not proven
See `docs/DEFERRED.md` (S378 lines): VM 2 and Authentik, touch play, a full game to a win, Firefox and Safari,
the card icon.
