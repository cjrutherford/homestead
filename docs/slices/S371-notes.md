# S371 — Notes as an Everything catalog package

## Goal and dependencies
Fourth Everything app (ADR-057): `notes` (notebooks, sections, pages, ink, tags,
search) from HeyPuter/everything at the pinned commit
`e7d07c48546ef0650a8f3ab94ae82b8b50d3646a`. Plan row: "AGPL. AI assist disabled
behind a clear message." Depends on S368, S369.

## Source and licence (AGPL)
`third_party/everything/notes-source.tar.gz` (84 entries, 1,427,742 bytes,
SHA-256 `035ec22bde035d8fe56cac74afdcee6126ae4d3a22c6139e1930b1964097d1d6`),
built exactly like the projects and records archives (the recipe was reproduced
against the projects digest first). The licence was checked first-hand:
`src/notes/LICENSE` is the full GNU Affero General Public License v3 and
`package.json` says `"license": "AGPL-3.0-only"`; the repository root `LICENSE`
is MIT, so Notes differs from the other apps. Dependencies are permissive
(lucide ISC, perfect-freehand MIT, temml MIT, pdfjs-dist Apache-2.0).

Compliance, recorded in `docs/THIRD_PARTY.md`: the Corresponding Source is the
pinned archive plus the patch list in `scripts/everything/apps/notes.json` plus
the shim; the patch list is the record of modifications and is guarded by pinned
patch targets. Because Notes is modified and used over the network by people
other than the owner, section 13 requires offering them its source; the in-app
offer is not built in this slice and is DEFERRED (it must be closed before the
package is shared beyond the owner's own appliance). Notices stay intact.

## SDK surface
| Call | Where | Bridge |
| --- | --- | --- |
| `fs.write` (overwrite, createMissingParents, Blob/string) | `src/data/backend.js` | supported (8 MiB per write, quota counted) |
| `fs.read` | backend.js | supported |
| `fs.readdir` | backend.js `list` (pages, assets, versions) | supported (S369) |
| `fs.delete` (no `recursive`; single files) | backend.js `remove` | supported |
| `auth.isSignedIn/getUser/signIn/signOut`, `env` | `src/main.js` | supported; `env` is `app`, so the app starts signed in and has no sign-out button |
| `auth.getProfilePicture` | `src/ui/app.js` (optional, in try/catch) | absent; avatar falls back to initials |
| `ui.showOpenFilePicker` | `src/ui/app.js` `cloudPicker()` | absent; the two "From Puter..." menu items would be disabled, so patches remove them |
| `fs.getReadURL` | backend.js `readURL`, used only by the share dialog's "Create Link" | refused; the Create Link controls are hidden by a patch |
| `ai.chat` | `src/ui/assist.js` | not provided; patched out (see below) |
| `kv`, `fs.stat`, `fs.space`, `perms`, `events`, `net`, sharing | not used | refused or disabled |
| IndexedDB | `local-backend.js`, only in a Vite dev build with `?local` | not in the built app |
| `fetch(url)` | `viewShared` (`#view=<url>` links to published pages) | blocked by the CSP (`connect-src 'self'`); no link is ever created |
| `fetch(dataUrl)` | `importBackup` (restoring a notebook backup) | blocked by the CSP; patched to decode locally |
| `navigator.clipboard`, Web Worker (pdf.js, same origin), `URL.createObjectURL` | editor, printouts | browser; allowed by the CSP |

No bridge or shim addition was needed and the ADR-057 table is unchanged.

## AI assist
`puter.ai` does not exist on the bridge. Three patches make this explicit
instead of a dead button: `available()` returns true so the Assist command
stays enabled, the Assist pane shows "AI assist is not available on Homestead.
Nothing you write is sent anywhere." with none of the AI actions or the question
box, and `ask()` throws the same message before reaching any SDK call. The
built source has no `puter.ai` reference, and the browser check records every
request made after opening Assist and calling `ask`: none.

## Patches (ten, each pinned and listed with a reason)
Assist (3), hide the public read-only link in the Share dialog (Copy Link to
Page, Email Page and Export remain), remove two "From Puter..." menu items,
decode backup data URLs locally, and three wording changes (picture command
text, two help sentences). `prepare_app.py` needed no change: Notes has a web
manifest but no service worker, so `"service_worker": null` (as for Records).

## Tests
- Upstream suite in the pristine tree: 15 files, 275/275 passed (jsdom, mock backend).
- `tests/test_s371_notes_package.py`: archive, digest, pin and licence agree; archive
  contents; every `puter.` call is listed; preparation applies the patches, leaves no
  `puter.ai`, no Create Link, no cloud picker, no `fetch(dataUrl)`; patch drift fails
  with no output; context copy in sync; catalog closed by default; AGPL obligations
  recorded; bookkeeping. Go rows in the catalog, SSO and appsso tests.
- Real-browser check (headless Chrome 156 against the dev bridge,
  `scripts/everything/gate/run_browser_check_notes.sh`): CSP-clean boot, no hosted
  SDK reference, no service worker; create a page through the app commands with
  title, text and a picture, page and asset on the bridge with size and stamp;
  reload shows the text; Assist shows the message and makes no request; Share shows
  no public link; restoring a notebook backup with an embedded picture works with
  no request; a write with the bridge down rejects `network_error`; a second person
  sees nothing of the first's data.
- Image built once with `build-image.sh`: 15.75 MB (15,750,141 bytes), ID `sha256:ce81d3768930...`; removed afterwards.

## Unproven
- The upstream tests were not run against the real bridge (they use a mock).
- No run on VM 2, no sign-in through Authentik.
- No offline page: Notes has no service worker (a later slice may add a runtime one).
- Ink, PDF printouts (pdf.js worker), equations, locking/sealed sections and
  version history are covered only by the upstream mock-backed suite; only the
  worker's CSP admission is by design (`worker-src 'self'`), not exercised.
- Multi-tab editing, large attachments near the 8 MiB limit and the quota message
  in the UI were not driven.
- The section 13 source offer inside the app is not built.

## Deferred
See the S371 lines in `docs/DEFERRED.md`.
