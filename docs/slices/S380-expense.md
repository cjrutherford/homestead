# S380 - Expenses as an Everything catalog package

## Goal and dependencies
Thirteenth Everything app (ADR-057): `expense` (expenses with attached receipts, expense reports, statement
reconciliation, dashboard) from HeyPuter/everything at the pinned commit `e7d07c48546ef0650a8f3ab94ae82b8b50d3646a`.
Plan row: "Local-only; do not ship the 'AI OCR' claim." Depends on S367 and S379.

## Source and licence
`third_party/everything/expense-source.tar.gz` (68 entries, 1,644,903 bytes, SHA-256
`4d4f6e81de6314872f69f3c57a87b0d9227ccda64011a395016d43ebd73f47f0`), same recipe as the other archives (the recipe
reproduced the Sudoku digest first). **MIT** (repository root; the app has no licence file and `package.json` has no
licence field). The only runtime dependency, `lucide`, is ISC and is bundled and served from the app's own origin.
Build-time only: jsdom, vite, vitest. No non-permissive licence, so no DEFERRED licence line.

## AI and OCR
Upstream reads each captured receipt with a Puter vision model (`ai.chat`, then `ai.img2txt`). Not shipped:
- The bridge and shim have no `ai`; the ADR-057 posture is unchanged.
- Capture goes straight to the expense form whatever the saved "read receipts automatically" setting says (that
  setting defaults to on upstream); the "AI receipt reading" settings section is removed.
- `runReceiptOcr` (still in the tree for upstream's own tests) throws "AI is not available on Homestead. Nothing you
  write is sent anywhere." before it builds a prompt or touches the SDK when the SDK has no `ai`.
- The claim is removed from the page description, the manifest, the sign-in page, onboarding, the empty dashboard,
  the capture dialog and `catalog/expense.yaml`. A test greps every string literal of the prepared source.

## SDK surface
| Call | Where | Decision |
| --- | --- | --- |
| `kv.get`, `kv.set` | settings, categories, month index | supported, per person and app |
| `fs.write`, `fs.read` | month files, reports, statements, receipts | supported (Blob/File bodies, 8 MB per file, counted against the quota) |
| `fs.delete` | a receipt, a month file, the receipts folder | supported; the folder delete now passes `{ recursive: true }` by patch |
| `auth.isSignedIn`, `auth.getUser` | boot, account | supported; every display shows "Homestead user", never the subject |
| `auth.signIn`, `auth.signOut` | sign-in page, settings | unreachable behind the bridge (env `app` hides sign-out; sign-in text reworded) |
| `ai.chat`, `ai.img2txt` | `lib/ocr.js` | not reachable: capture never calls them; the function fails closed |
| `fs.getReadURL`, `kv.share*`, `perms.*`, `events.*`, `ui.*`, `net` | none | not used; stay refused or absent |

Browser APIs: `localStorage` (theme only), a service worker (Homestead's, replacing upstream's), object URLs and
`<a download>` for receipts and CSV/JSON export, file inputs and the clipboard `paste` event. No IndexedDB or File
System Access. No new SDK calls: the ADR-057 table, bridge and shim are unchanged.

## Storage design
Expenses, reports, statements and settings live in the bridge's per-person kv and file storage. Receipts are files
`expenses-data/receipts/<id>.<ext>`; the bridge limit is 8 MB per file (upstream allowed 10 MB, so the capture
limit and its text are patched to 8 MB) and every write counts against the person's and the app's quota. The
theme is the only browser-held value: `scripts/everything/files/expense-person-storage.js` (added as
`src/expense/src/homestead/boot.js`) gives `localStorage` keys a person tag (first 8 bytes of SHA-256 of `pexp:` plus
the subject; memory only if the person cannot be identified), registers the service worker and starts the app. The
inline theme script in `index.html` reads the unprefixed key, which is never written.

## Other changes
- `scripts/everything/apps/expense.json`: 36 exact-once patches (AI off and AI wording removed, Puter wording and
  links removed, "Homestead user", 8 MB receipts, PDF preview replaced by a download hint because the page policy
  has no frame source, recursive receipt-folder delete, entry point).
- `scripts/everything/expense-sw.template.js` (`runtime` mode, replaces upstream's `public/sw.js`, which cached
  `index.html` and the hosted SDK). Never caches `index.html` or `_homestead/*`; stores only content-hashed build
  assets; fixed offline page.
- `catalog/expense.yaml`, `pin.yaml`, `THIRD_PARTY.md`, Go catalog and SSO tests,
  `tests/test_s380_expense_package.py`, `scripts/everything/gate/run_browser_check_expense.sh` and
  `browser-check-expense.mjs`.
- No change to prepare_app.py, the bridge, the shim or the CSP rule.

## Analytics and third-party scripts
None beyond the hosted SDK tag (replaced by the local shim). No fonts or CDNs. The built output has no js.puter.com,
plausible, jsdelivr, googleapis or puter.com. Dockerfile guard unchanged.

## Tests
Upstream vitest: 79 passed (9 files), pristine and patched alike (80 with an ad hoc test, not committed, that
`runReceiptOcr` rejects with the Homestead message and calls nothing when the SDK has no `ai`). Python package test.
Go: catalog rows, `TestExpenseSignInUsesTheBridgesOwnRoutes`. Real-browser check (headless Chrome 156): CSP-clean
boot, same-origin requests only for the whole run, no Puter link, hosted SDK, AI or OCR wording on any page visited,
`puter.ai` undefined, a receipt attached and an expense saved with the default settings and no request that is not
this app's own, a file over 8 MB refused and one just under stored, reload persistence, "Homestead user" in settings,
the theme stored under a person-tagged key (and the subject not in the key), a second person in the same profile sees
none of the first person's expenses, receipts or theme, the service worker serves the offline page with the server
stopped and caches only build assets.

## Not proven
See `docs/DEFERRED.md` (S380 lines).
