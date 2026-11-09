# S374 - Time tracking as an Everything catalog package

## Goal and dependencies
Seventh Everything app (ADR-057): `time-tracking` (the app is called Hourly upstream:
timer and manual entries, weekly and monthly timesheets, clients and projects with
rates, budgets and retainers, reports and CSV export, invoices with payments,
logo and printable timesheet, Toggl/Clockify/Harvest import, backups) from
HeyPuter/everything at the pinned commit `e7d07c48546ef0650a8f3ab94ae82b8b50d3646a`.
Plan row: "`fs` + `kv`." Depends on S368 to S373.

## Source and licence (MIT)
`third_party/everything/time-tracking-source.tar.gz` (75 entries, 1,914,107 bytes,
SHA-256 `fcb03e792bc2ca41c53f4a7d31c8838d71e66c03d1ccd570f5553c69d620c161`), built with
the same recipe as the other archives (re-run first on `src/inventory`, which
reproduced the S373 digest). The repository root `LICENSE` is MIT ("Copyright (c)
2026 Puter Technologies Inc."). `src/time-tracking` has no licence file, its
`package.json` has no licence field, and its README says the code is under the
repository's MIT licence. Not AGPL. One runtime dependency is bundled: `lucide`
1.28.0 (ISC). Recorded in `docs/THIRD_PARTY.md` and `third_party/everything/pin.yaml`.

## SDK surface
| Call | Where | Decision |
| --- | --- | --- |
| `kv.get`, `kv.set` | `lib/store.js` (`hourly:settings|clients|projects|months|timer|backups|invoices:v1`) | supported |
| `fs.write` (`createMissingParents`; text or a Blob), `fs.read` (returns a Blob), `fs.delete` | `lib/store.js`: entries per month (`hourly-data/entries-YYYY-MM.json`), backups (`hourly-data/backups/`), invoice logo (`hourly-data/logo-*`) | supported, already in the bridge; person-scoped, counted against quota |
| `auth.isSignedIn/getUser` | `main.js`, `lib/store.js` | supported; `uuid` only tells people apart in the browser mirror; a patch keeps the opaque name off screen |
| `auth.signIn/signOut` | `views/auth.js`, `views/settings.js` | unreachable (`env` is `app`: no sign-in screen, no Sign out) |
| `env` | `lib/env.js` | supported (`app`) |
| `kv.del`, `kv.list`, `fs.readdir/stat/mkdir`, `ui.*`, `ai`, `net`, `perms`, sharing, `fs.getReadURL` | not used | not used |
| `localStorage` (`hourly:cache:v1:*`), `navigator.serviceWorker`, `Notification`, `URL.createObjectURL`, `FileReader`/CSV import | offline mirror and write queue, worker, forgot-to-stop alerts, logo and imports | browser-native; the CSP allows same-origin workers and `blob:` images |

No bridge or shim change was needed. ADR-057's table already covers every call.

## Analytics and third-party scripts
None in this app: `index.html` has only the hosted SDK tag (replaced by the local shim
in `prepare_app.py`), there is no Plausible block and no handbook site. The built
`dist` has no `js.puter.com`, no `plausible` and no `developer.puter.com` (the only
outbound link, in the unreachable sign-in view and in Settings > About, is patched).
The image build's existing guard (`js.puter.com`, `plausible.io`) is unchanged. The
headless run shows only same-origin requests.

## Patches (13, each with a `why` in `scripts/everything/apps/time-tracking.json`)
All wording or link changes, none changes behaviour: two `main.js` error strings
(Homestead, not Puter); `store.js` `this.user = null` (keeps the opaque subject out
of the sidebar, Settings and the printed timesheet header, whose fallback business
name was the user name); `shell.js` and `settings.js` user fallback "Homestead user";
five Settings strings (account line, logo, backup, automatic backup, About with the
developer.puter.com link removed); onboarding; two sign-in-view strings including the
outbound link.

The app registers its own `./sw.js`, but only off localhost/127.0.0.1; a runtime-mode
worker from `time-tracking-sw.template.js` replaces upstream's (network-only
navigations, fixed offline page, only content-hashed assets cached), as for Inventory.
The manifest, icons and `base: './'` are already relative, so no path patch was needed.

## Tests
- Upstream, pristine tree with `npm ci --ignore-scripts`: 205/205 (10 files).
- `tests/test_s374_time_tracking_package.py` (archive, licence, SDK calls, patches
  apply, worker, Dockerfile guard, catalog posture, bookkeeping), catalog rows
  (`everything_test.go`, `data_test.go`, `sso_test.go`, `curated_test.go`) and an
  `appsso` sign-in test for time-tracking.
- Headless Chrome 156 (`scripts/everything/gate/run_browser_check_time_tracking.sh`):
  CSP-clean boot; worker registered by the app and scoped to the path; same-origin
  requests only; no hosted SDK, analytics or Puter link; sidebar and Settings say
  "Homestead user", no raw subject, no Sign out; sample clients and projects loaded,
  a time entry logged through the form (entry file under `hourly-data/`, projects in
  KV), a backup written from Settings; reload keeps the entry; offline page without
  data; a second person sees nothing of the first (no entry, no projects, cannot
  read the entry file, backups hold nothing of the first) and the first person's
  browser mirror (localStorage) is wiped. The check serves the page from a mapped
  host name (`hs-check.test`) because upstream skips its worker on loopback.
- Gate bridge mode and the Go and Python suites: see the run in the report.

## Unproven
Not run on VM 2 or through Authentik (owner checklist in
`docs/reviews/EVERYTHING_REVIEW_PLAN.md`). The running timer across tabs, weekly
grid, invoices and payments, the printable invoice and timesheet, logo upload, CSV
imports and reports were not driven in a browser (they rely on the upstream tests).
Upstream tests were not run against the real bridge. See `docs/DEFERRED.md`.
