# S372 — Contacts as an Everything catalog package

## Goal and dependencies
Fifth Everything app (ADR-057): `contacts` (address book, vCard import and
export, search, groups, birthdays) from HeyPuter/everything at the pinned commit
`e7d07c48546ef0650a8f3ab94ae82b8b50d3646a`. Plan row: "kv + ui heavy."
Depends on S368 to S371.

## Source and licence (MIT)
`third_party/everything/contacts-source.tar.gz` (109 entries, 5,885,527 bytes,
SHA-256 `1c833f87a8e839ea1664739936de202ca2bf2d8c43a450c3b75bb4b3ac0724fd`),
built with the same recipe as the other archives (reproduced against the records
digest first). The root `LICENSE` and `src/contacts/LICENSE` are both MIT
("Copyright (c) 2026 Puter Technologies Inc."); `package.json` has no license
field and no runtime `dependencies`. Recorded in `docs/THIRD_PARTY.md` and
`third_party/everything/pin.yaml`. The 5 MB is mostly store screenshots.

## SDK surface and the per-call `ui` decisions
Three outcomes were allowed for each `puter.ui.*` call: a browser-native
equivalent through a pinned patch, harmless absence, or hiding the feature.

| Call | Where | Decision |
| --- | --- | --- |
| `kv.get/set/del` | data layer | supported |
| `kv.list(prefix*)` keys | data layer | supported (1000-key limit, unchanged) |
| `kv.list(prefix*, true)` with values | loads the whole book | supported; new `listvalues` bridge operation reads bounded pages (500 entries or about 4 MiB), so a book over 1000 contacts loads |
| `auth.isSignedIn/getUser` | `main.js` | supported; the app uses `user.email` only (patch), never the opaque subject |
| `auth.signIn/signOut` | `main.js` | unreachable (`env` is `app`, so no sign-in screen and no Sign out item); listed anyway |
| `env` | `main.js` | supported (`app`) |
| `ui.contextMenu` | `contextmenu.js` | harmless absence: the app falls back to its own DOM menu |
| `ui.setWindowTitle` | `main.js` | harmless absence (in try/catch) |
| `ui.parentApp`, `ui.hideWindow`, `ui.showWindow`, `exit`, `args` | launch and service mode | harmless absence: needs `puter.args.service`, calls are in try/catch |
| `ui.launchApp` (Calendar) | birthday menu | harmless absence: `supported()` is false, so "Add birthday to Calendar" is not offered |
| `ui.onItemsOpened` | `dropimport.js` | harmless absence; browser-native routes already exist upstream (HTML5 drop, `<input type=file>` vCard import, `<a download>` export, `<input type=file>` photo), so no new patch |
| `ai.chat` (Smart add) | `smartadd.js` | hiding: button removed, guard forced to a "not available on Homestead" message, call replaced by a refusal |
| Plausible analytics, `js.puter.com` preconnect, canonical link, `nativeFetch` wrapper | `index.html`, `main.js` | removed by patches; the build fails if `js.puter.com` is in `dist` |
| `fs.getReadURL`, `kv.share*`, `perms.*`, `events.*`, `appID`, `net`, sharing | not used or refused | refused or disabled |
| `localStorage`, `URL.createObjectURL`, `navigator.clipboard`, `fetch(item.readURL)` | prefs, export, copy | browser-native; `readURL` is for Puter items only and unreachable |

Sixteen pinned patches (each with a `why` in `scripts/everything/apps/contacts.json`):
relative asset base, relative logo, manifest id/start_url/scope/icons under the
path, screenshots unlisted, relative worker registration (`./sw.js`) without the
`isPuterApp` skip, Smart add hidden, email from `user.email`, and the build script
reduced to `vite build` (upstream's build runs tests that assert the unpatched
Smart add). A runtime-mode service worker is installed from
`contacts-sw.template.js`.

## Bridge and shim additions
`ListValues` in `internal/everything/store.go` and a `listvalues` case in
`handler.go`: KV only, scoped to the person, read only (no quota use), paged by
`object_key > after`. The shim's `kv.list(pattern, true)` loops pages. ADR-057's
table has the new row.

## Tests
- Go: store tests (paging past the key cap, scoped per person, paging by bytes),
  handler tests (basic, `after`, file kind is 400), catalog and sign-in rows for
  contacts; `go build ./...` and `go test ./internal/... ./cmd/...` green.
- Gate bridge mode: 9/9 (new test: lists key-value pairs past 1000, per person).
- Upstream tests on the pristine tree: 313/313 (23 files).
- `tests/test_s372_contacts_package.py`: archive, licence, SDK calls all listed,
  patches apply, catalog posture, bookkeeping.
- Headless Chrome 156 (`scripts/everything/gate/run_browser_check_contacts.sh`):
  CSP-clean boot; worker scope and same-origin only; manifest and logo load;
  `ui`, `ai`, `exit` undefined; Smart add hidden; account menu has no Sign out or
  raw subject; create a contact; vCard import by drop; search; export; reload
  persistence; a 1103-contact book loads; offline page; second person isolated.
- Image built once on the appliance recipe: 16.4 MB, ID `1968a3fad61e`; removed.

## Unproven
Not run on VM 2 or through Authentik (owner checklist in
`docs/reviews/EVERYTHING_REVIEW_PLAN.md`). Upstream tests were not run against the
real bridge. Photo and quota error messages are unverified. See `docs/DEFERRED.md`.
