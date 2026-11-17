# S377 - 3D as an Everything catalog package

## Goal and dependencies
Tenth Everything app (ADR-057): `three-d`, a Blender-style 3D editor (three.js, Rapier physics,
path tracer) from HeyPuter/everything at the pinned commit
`e7d07c48546ef0650a8f3ab94ae82b8b50d3646a`. Plan row: "IndexedDB + File System Access API;
Chromium-only caveat in the copy." Depends on S368 to S376. The package is `three-d` because
package names must start with a letter (`3d` is refused everywhere).

## Source and licence
`third_party/everything/three-d-source.tar.gz` (1,898 entries, 11,576,110 bytes, SHA-256
`1dccd506f1e94f01b29602ee7bd34370c343203e8ca97949550bdccbb06b8aed`), same recipe as the other
archives. **The app is AGPL-3.0-only** (not MIT). Compliance follows S371 (Notes): the pinned
archive, the patch list and the entry point are the Corresponding Source; no in-app source link
yet. Bundled dependencies are MIT, Apache-2.0 and OFL fonts. See `docs/THIRD_PARTY.md`.

## SDK surface
Upstream never loads the Puter SDK for files; it uses browser storage. The Homestead entry point
(`scripts/everything/files/3d-person-storage.ts`, added as `src/3d/src/homestead/boot.ts`) is the
only caller.
| Call | Where | Decision |
| --- | --- | --- |
| `auth.getUser` | entry point: who is signed in (hashed into a per-person tag) | supported; the subject is never shown |
| `kv.get/set` | entry point: the settings snapshot | supported |
| `auth.isSignedIn/signIn` | shim | supported |
| `fs.*` | none | not used (scenes stay in the browser) |
| `fs.getReadURL`, `kv.share*`, `perms.*`, `events.*`, `appID` | none reached | refused |
| `ui.showFeedbackDialog`, `exit` | none | absent |
| `ai`, `net`, `sharing`, `python` | assistant, CDN Pyodide | disabled; entry points removed |

No new SDK calls, so the ADR-057 table is unchanged.

## Storage design
Browser-local storage leaks between people who share a browser profile, so the entry point
runs before the app starts:
- `localStorage` becomes a virtual `Storage` (3 MB cap, QuotaExceededError beyond it) that loads and saves one snapshot
  (`browser-settings/local-storage.v1`) in the person's bridge kv; flushed 1 s after a change and on page hide.
- IndexedDB names get an `@<tag>` suffix and the origin-private file system root moves into `p3d-<tag>`,
  where the tag is the first 8 bytes of SHA-256 of `p3d:` plus the subject.
Scenes, libraries, presets, snapshots and the autosave stay in the browser, separated but not on the appliance
(see DEFERRED).

## Changes
- `scripts/everything/apps/three-d.json`: 17 exact-once patches (relative base; AI assistant, Report a Bug and Python removed; the
  hosted-SDK URL and CDN text removed; the script console refuses with a message), `service_worker: null`, `sdk_tag: null`.
- `scripts/everything/prepare_app.py`: generic `sdk_tag: null` with `sdk_inject_before` and `add_files` (sources in `scripts/everything/files/`; refuses to overwrite an upstream file or leave the tree); `sync-context.sh` copies the files directory.
- `internal/everything/static.go`: `'wasm-unsafe-eval'` is also granted when built JS instantiates or compiles an inline WebAssembly module (Rapier embeds its wasm as base64). `'unsafe-eval'` stays refused. The S375 rule is reused, not duplicated.
- `catalog/three-d.yaml` (copy says folder access works only in Chromium), `pin.yaml`, `THIRD_PARTY.md`, Go catalog and SSO tests, `tests/test_s377_three_d_package.py`.
- `fs.rename` from S375 is reused; no bridge change.

## Chromium-only folder access
"Open Folder..." needs `showDirectoryPicker` and is shown only when it exists; other browsers get "Add Files..." and downloads.

## Analytics and third-party scripts
None shipped. The Pyodide CDN address (and the text that named it) is removed by patch; the built output has no
js.puter.com, plausible or jsdelivr. Dockerfile guard unchanged.

## Tests
Upstream vitest: 3,018 passed, 5 skipped, pristine and patched alike. Go: `TestPolicyAllowsWebAssemblyForScriptThatInstantiatesAnInlineModule`,
catalog rows, `TestThreeDSignInUsesTheBridgesOwnRoutes`. Python package test (11). Real-browser check
(`scripts/everything/gate/run_browser_check_three_d.sh`, headless Chrome 156): CSP-clean boot, narrow wasm policy,
same-origin requests only, no Assistant, Report a Bug, Python or service worker, preference kept across reload via the bridge,
database names carry the person tag, a second person (same profile) sees none of the first person's settings or autosave,
boot with the folder picker removed. Image `homestead/everything-three-d` builds (29.6 MB on disk).

## Not proven
See `docs/DEFERRED.md` (S377 lines): VM 2 and Authentik, WebGL rendering and modelling in a browser, Firefox and Safari,
offline, source offer, scripting.
