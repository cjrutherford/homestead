# S370 — Records as an Everything catalog package

## Goal and dependencies
Third Everything app (ADR-057): `records` (connected tables, views and forms; the
Airtable gap) from HeyPuter/everything at the pinned commit
`e7d07c48546ef0650a8f3ab94ae82b8b50d3646a`. Depends on S368 and S369.

## Source and licence
`third_party/everything/records-source.tar.gz` (95 entries, 1,924,675 bytes,
SHA-256 `8ed5aa77f9264b341ec8bf0e07b41ca053f7eb3ba4edd224cc6f1472c3c26aa2`), pin
entry in `pin.yaml`. Built the same way as the projects archive (the recipe was
checked by reproducing the projects digest first). Licence MIT (repository root
`LICENSE`; `src/records` declares MIT in `package.json` and has no licence file).
Runtime dependency `lucide` 1.55.0 is ISC; upstream's `check-licenses` prebuild
step passed for all 73 locked packages. Nothing copyleft. See `docs/THIRD_PARTY.md`.

## SDK surface
| Call | Where | Bridge |
| --- | --- | --- |
| `fs.write` (overwrite, createMissingParents, Blob) | `src/data/backend.js` | supported (8 MiB per write, quota counted) |
| `fs.read` | backend.js | supported |
| `fs.stat` | backend.js (conflict stamp `modified|size`) | supported (S369) |
| `fs.delete` `{recursive}` | backend.js (delete base, purge) | supported (S369) |
| `kv.get/set` | backend.js (small prefs) | supported |
| `auth.isSignedIn/getUser/signIn/signOut`, `env` | `src/main.js` | supported; `env` is `app`, so the app starts signed in and shows no sign-out button |
| `fs.space` | backend.js, `space()` (never called by the UI) | absent; wrapped in try/catch |
| `auth.getProfilePicture` | main.js, optional call `?.()` in try/catch | absent; avatar falls back to initials |
| `ui.onLaunchedWithItems` | main.js, only when `env === 'app'` and guarded by `puter.ui?.` | absent; "open with" is a desktop feature |
| `fs.readdir`, `fs.getReadURL`, `perms`, `events`, `appID`, `ai`, `net`, sharing | not used | refused or disabled |
| IndexedDB | local edit journal (`journalName(user)`, per person) | browser; allowed by the CSP (same origin) |
| localStorage | theme and view prefs, journal id | browser |
| `showOpenFilePicker`/`showSaveFilePicker`, File System Access | not used (file inputs and downloads) | n/a |
| `navigator.clipboard` | copy link | browser |

No bridge or shim addition was needed; the S369 operations cover everything. The
ADR-057 table is unchanged.

## What this slice adds
- Pinned archive, `scripts/everything/apps/records.json`, `catalog/records.yaml`
  (path `/apps/records/`, `env-oidc`, closed by default, backup of `data/bridge.db`).
- Three wording-only patches (storage-full toasts and the delete confirmation said
  "Puter"); patch targets are pinned and preparation fails if upstream changes them.
- `prepare_app.py` accepts `"service_worker": null`: Records has no upstream worker
  and registers none, so none is added.
- Catalog, SSO and appsso test rows, `tests/test_s370_records_package.py`,
  `scripts/everything/gate/run_browser_check_records.sh` and
  `browser-check-records.mjs`.
- Icon `mdi:file-excel-outline`, already bundled.

## Tests
- Upstream suite in the pristine tree: 19 files, 445/445 passed.
- Go: `go test ./internal/... ./cmd/...`; Python: `python3 -m pytest -q tests`;
  bridge gate 8/8 (see the commit message for final counts).
- Real-browser check (headless Chrome 156 against the dev bridge): CSP-clean boot
  with no hosted SDK reference and no service worker, create a base and a record
  through the UI, base, schema and records chunk readable on the bridge with size
  and stamp, reload shows the base and no CSP violation, a write with the bridge
  down rejects `network_error`, and a second person sees nothing of the first's
  data and gets `subject_does_not_exist` for their folder.
- Image built once with `build-image.sh`: 14.3 MB (14,308,319 bytes), ID `sha256:157769171fd4...`; removed afterwards.

## Unproven
- The upstream tests were not run against the real bridge (they use a mock).
- No run on VM 2, no sign-in through Authentik.
- Records has no service worker, so there is no offline page: after a loss of
  connection an open page keeps working from its local journal, but the page
  cannot be reopened offline. The offline journal replay against the bridge was
  not exercised in a browser.
- No multi-tab conflict test on the bridge; large attachment uploads (8 MiB
  per-write limit) and the quota error text in the UI were not driven.
- Canvas grid editing, formulas, views and CSV import/export are covered only by
  the upstream mock-backed suite.

## Deferred
See the S370 lines in `docs/DEFERRED.md`.
