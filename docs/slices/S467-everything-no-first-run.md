# S467 - No first-run dialogs or Puter wording in Everything apps

## Goal and dependencies
Acceptance run on VM 2 (EV-THREE-D-6, EV-TASKS-8). ADR-059: nobody walks through a first-run dialog in a catalog app. Depends on ADR-057 (patch-at-build), S377 (3D), S464 (pattern).

## Findings
- 3D showed a startup splash and a "Quick Setup" page on every load (its done flag lives in localStorage; `show_splash` defaults on).
- Tasks, Time tracking, Projects, Expense and Inventory each open a Welcome dialog on an empty dataset (`shouldOnboard`); Tasks' says "Everything lives in your own Puter cloud". The other nine apps have no first-run dialog.
- All apps carry hosted-Puter wording (settings footers, sign-in and error screens, calendar PRODID, share text, page metadata).

## What changed
- Per-app patches: `shouldOnboard` returns false in the five apps; 3D's `quickSetupPending` returns false, `show_splash` defaults off, and the startup splash call is removed (VM 2 showed an owner who had saved "Show at startup" earlier still got it).
- `scripts/everything/wording.json`: one shared, ordered list of exact phrases ("your own Puter cloud" to "your own Homestead", "Tasks on Puter" to "Tasks", ...) that `prepare_app.py` applies to every app's own `.js/.ts/.html` source after the patches. Tests, dev mocks, `/site/`, landing page and the hidden assistant are skipped. The rules ship in the build context (`sync-context.sh`) and in the source offer tarball.
- Images must be rebuilt (`homesteadctl craft build APP --follow`) and the app refreshed to pick this up; no manifest or env change, so a plain update of the image is enough.

## Not done (DEFERRED)
Remaining Puter strings in sign-in-only screens, the hidden AI assistants, export file headers of other formats and the "Built with Puter.js" credit line (attribution kept on purpose).

## Tests
`tests/test_s467_everything_no_first_run.py` prepares all 14 apps from the pinned archives and asserts the phrases are gone and the dialog patches are applied; S369 pin updated.
