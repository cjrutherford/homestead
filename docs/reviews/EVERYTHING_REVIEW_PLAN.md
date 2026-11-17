# Everything apps review plan

See also `docs/reviews/TESTING_REPORT.md` (test results and gaps) and `docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md` (owner walkthrough on VM 2; its EV-ADR058 steps mirror the S441 section below).

Owner checklists for the Everything apps, one section per slice, newest last.
Each later app slice appends a section using the template at the end.

## S367 — Tasks spike (plain Compose on VM 2)
Follow the "Owner checklist" (7 steps) in
`docs/slices/S367-everything-bridge-and-build.md`. Superseded for installs by
S368 below; keep it only to compare behaviour.

## S368 — Tasks catalog package
Prerequisites: VM 2 on the build with S368; `homestead.lan` resolves to VM 2;
Authentik installed and sign-in set up; a second person exists in People; the
browser trusts the appliance CA.

Steps and expected results:
1. Build and plan: in the dashboard craft page (or the CLI craft plan/build) build `tasks`. Expected: image `homestead/everything-tasks:0.1.0` recorded; the build log shows the pinned archive verified.
2. Install Tasks from the catalog with a plan, preview and apply. Expected: the plan shows route `/apps/tasks/`, sign-in changes and no public exposure; before sign-in is applied, the page answers 503 "Sign-in is not set up".
3. Apply the sign-in plan. Expected: `https://homestead.lan/apps/tasks/` redirects to Authentik; owner signs in and sees Tasks. No request to js.puter.com in DevTools Network.
4. Add a list and two tasks; reload. Expected: they remain.
5. Second person, closed by default: sign in as the second person at the Tasks URL. Expected: refused (no access). Grant access in People; reload. Expected: an empty Tasks account with none of the owner's lists; their task does not appear for the owner.
6. Offline: set DevTools Network to Offline, add a task (it queues), go Online. Expected: it syncs. Reloading offline shows the offline page.
7. Quota: add data until the person quota (256 MiB) would be passed (or lower the quota in the package environment for the test). Expected: a clear quota error, existing data intact.
8. No Share button and no calendar-feed setting appear. Check DevTools Console for CSP violations: none expected.
9. Backup: run a backup, then restore to a spare location or inspect the archive. Expected: it contains `data/bridge.db`.
10. Uninstall Tasks. Expected: the route is gone, the container is removed, the data directory is kept or removed as the plan states.

Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7 [ ] 8 [ ] 9 [ ] 10

Report failures with the time; `docker logs` of the Tasks container and the daemon journal give the cause.

## S369 — Projects catalog package
Prerequisites: VM 2 on the build with S369; Authentik sign-in set up; a second
person exists in People.

Steps and expected results:
1. Build `projects` in the craft page or CLI. Expected: image `homestead/everything-projects:0.1.0` recorded.
2. Install Projects with a plan, preview and apply. Expected: route `/apps/projects/`, no public exposure.
3. Apply sign-in. Expected: `https://homestead.lan/apps/projects/` redirects to Authentik; the owner sees Projects. No request to js.puter.com.
4. Create a project, add a column and two tasks, attach a small file; reload. Expected: all remain.
5. Open the project in a second tab, edit in both. Expected: the conflict prompt appears, no silent loss.
6. Second person: closed by default, then granted in People. Expected: an empty Projects account.
7. Offline: go Offline, edit, go Online. Expected: edits sync; reloading offline shows the offline page.
8. Attachment over 8 MiB. Expected: a clear size error; existing data intact.
9. No share, calendar feed or cloud-drive picker is offered. DevTools Console shows no CSP violation.
10. Backup contains `data/bridge.db`; uninstall removes route and container.

Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7 [ ] 8 [ ] 9 [ ] 10

## S370 — Records catalog package
Prerequisites: VM 2 on the build with S370; Authentik sign-in set up; a second
person exists in People.

Steps and expected results:
1. Build `records` in the craft page or CLI. Expected: image `homestead/everything-records:0.1.0` recorded.
2. Install Records with a plan, preview and apply. Expected: route `/apps/records/`, no public exposure.
3. Apply sign-in. Expected: `https://homestead.lan/apps/records/` redirects to Authentik; the owner sees the Records home. No request to js.puter.com.
4. Create a base, add a table with a few field types (text, number, single select, link) and several records; reload. Expected: all remain.
5. Switch the table between grid, board, calendar, gallery and form views; add a filter and a sort; reload. Expected: views and settings remain.
6. Attach a small file to a record; reload and download it. Expected: it opens. A file over 8 MiB gives an error and existing data is intact.
7. Import a CSV as a new base and export one. Expected: both work.
8. Open the base in a second tab, edit in both. Expected: a conflict message, no silent loss.
9. Second person: closed by default, then granted in People. Expected: an empty Records account.
10. Go Offline in DevTools, edit, go Online. Expected: edits save. Reloading while offline shows the browser error page (there is no offline page).
11. Delete a base to the trash and purge it. Expected: gone, and its files are removed.
12. No Share-to-people feature appears; DevTools Console shows no CSP violation. Backup contains `data/bridge.db`; uninstall removes route and container.

Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7 [ ] 8 [ ] 9 [ ] 10 [ ] 11 [ ] 12

## S371 — Notes catalog package (AGPL)
Prerequisites: VM 2 on the build with S371; Authentik sign-in set up; a second
person exists in People.

Steps and expected results:
1. Build `notes` in the craft page or CLI. Expected: image `homestead/everything-notes:0.1.0` recorded.
2. Install Notes with a plan, preview and apply. Expected: route `/apps/notes/`, no public exposure.
3. Apply sign-in. Expected: `https://homestead.lan/apps/notes/` redirects to Authentik; the owner sees a notebook with a page. No request to js.puter.com.
4. Add a section and a page, type text, add a to-do tag, draw with a pen and insert a picture; reload. Expected: all remain.
5. Search for a word on the page. Expected: the page is found.
6. Open Assist from the ribbon. Expected: "AI assist is not available on Homestead" and no AI buttons; DevTools Network shows no request.
7. Open Share. Expected: no "Create Link" or read-only link; Copy Link to Page, Email Page and Export work.
8. Insert a PDF printout. Expected: pages appear as pictures (the PDF worker runs under the CSP).
9. Export a notebook backup and import it again. Expected: a second notebook with the same pages and pictures.
10. Second person: closed by default, then granted in People. Expected: an empty Notes account.
11. Delete a page and restore it from Deleted Notes. Expected: it returns.
12. DevTools Console shows no CSP violation. Note whether any source offer or AGPL statement is reachable from the app (expected: not yet, see DEFERRED S371). Backup contains `data/bridge.db`; uninstall removes route and container.

Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7 [ ] 8 [ ] 9 [ ] 10 [ ] 11 [ ] 12

## S372 — Contacts catalog package
Prerequisites: VM 2 on the build with S372; Authentik sign-in set up; a second
person exists in People.

Steps and expected results:
1. Build `contacts` in the craft page or CLI. Expected: image `homestead/everything-contacts:0.1.0` recorded.
2. Install Contacts with a plan, preview and apply. Expected: route `/apps/contacts/`, no public exposure.
3. Apply sign-in. Expected: `https://homestead.lan/apps/contacts/` redirects to Authentik; the owner sees an empty address book. No request to js.puter.com or plausible.io.
4. New contact with a name, company, an email and a photo; reload. Expected: it remains, with the photo.
5. Import a `.vcf` with several cards (use the Import menu item and also drag one onto the window). Expected: all appear. Export all and open the file. Expected: every contact is in it.
6. Search by name, company and email. Expected: the list narrows as you type.
7. Look for Smart add. Expected: no such button anywhere and no "Puter" wording on screen.
8. Open the account menu and a contact's menu. Expected: the app's own menus appear; there is no Sign out and no raw user id in the account menu. A contact with a birthday has no "Add birthday to Calendar".
9. Import or create a few thousand contacts (a large `.vcf`). Expected: after a reload they all load (it may take a few seconds).
10. Second person: closed by default, then granted in People. Expected: an empty address book.
11. Stop the container and reload. Expected: the "You are offline" page, no contacts shown. Start it again.
12. DevTools Console shows no CSP violation. Backup contains `data/bridge.db`; uninstall removes route and container.

Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7 [ ] 8 [ ] 9 [ ] 10 [ ] 11 [ ] 12

## S373 — Inventory catalog package
Prerequisites: VM 2 on the build with S373; Authentik sign-in set up; a second
person exists in People; a phone or laptop with a camera for step 6.

Steps and expected results:
1. Build `inventory` in the craft page or CLI. Expected: image `homestead/everything-inventory:0.1.0` recorded.
2. Install Inventory with a plan, preview and apply. Expected: route `/apps/inventory/`, no public exposure.
3. Apply sign-in. Expected: `https://homestead.lan/apps/inventory/` redirects to Authentik; the owner sees the first-run welcome, with no "Puter" wording. No request to js.puter.com or plausible.io.
4. Skip the welcome, add a product with a name, SKU, opening stock and a photo; reload. Expected: it remains, with the photo as its thumbnail.
5. Import a CSV of products, add a location and a supplier, record a stock movement and open Reports. Expected: all work; Export gives a CSV.
6. Scan a barcode with the camera on the Scan button (allow the camera). Expected: the camera opens and a scanned code finds or starts a product.
7. Open Settings. Expected: no Documentation or Guides links, no link to Puter, no Sign out, and the account row says "Homestead user" (no raw user id).
8. Create a backup in Settings and restore it. Expected: the data returns.
9. Second person: closed by default, then granted in People. Expected: an empty inventory, none of the first person's products or photos, even in the same browser.
10. Stop the container and reload. Expected: the "You are offline" page, no product names. Start it again.
11. Check a large photo (12 megapixels). Expected: it is shrunk and saved; storage use stays small.
12. DevTools Console shows no CSP violation. Backup contains `data/bridge.db`; uninstall removes route and container.

Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7 [ ] 8 [ ] 9 [ ] 10 [ ] 11 [ ] 12

## S374 — Time tracking catalog package
Prerequisites: VM 2 on the build with S374; Authentik sign-in set up; a second
person exists in People.

Steps and expected results:
1. Build `time-tracking` in the craft page or CLI. Expected: image `homestead/everything-time-tracking:0.1.0` recorded.
2. Install Time tracking with a plan, preview and apply. Expected: route `/apps/time-tracking/`, no public exposure.
3. Apply sign-in. Expected: `https://homestead.lan/apps/time-tracking/` redirects to Authentik; the owner sees the welcome screen with no "Puter" wording. No request to js.puter.com or any other host.
4. Choose "Set up tracking", add a client and a project, start the timer, stop it, and log a manual entry (`1:30`). Reload. Expected: everything remains.
5. Open Settings. Expected: the account row says "Homestead user" (no raw user id), no Sign out, no link to Puter.
6. Create a client invoice from unbilled time, record a payment, open the printable invoice and timesheet, upload a logo. Expected: all work; the logo shows after a reload.
7. Import a Toggl, Clockify or Harvest CSV and run Reports with CSV export. Expected: both work.
8. Use "Back up now" in Settings, then restore it. Expected: the data returns.
9. Second person: closed by default, then granted in People. Expected: an empty app, none of the first person's entries, even in the same browser.
10. Stop the container and reload. Expected: the "You are offline" page, no entry names. Start it again.
11. Check a week's entries, install the app as a PWA from the browser and open its "Log time" shortcut. Expected: the form opens.
12. DevTools Console shows no CSP violation. Backup contains `data/bridge.db`; uninstall removes route and container.

Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7 [ ] 8 [ ] 9 [ ] 10 [ ] 11 [ ] 12

## S375 — Circuits catalog package
Prerequisites: VM 2 on the build with S375; Authentik sign-in set up; a second
person exists in People.

Steps and expected results:
1. Build `circuits` in the craft page or CLI. Expected: image `homestead/everything-circuits:0.1.0` recorded.
2. Install Circuits with a plan, preview and apply. Expected: route `/apps/circuits/`, no public exposure.
3. Apply sign-in. Expected: `https://homestead.lan/apps/circuits/` redirects to Authentik; the start page says nothing about Puter and has no link to another site. No request to js.puter.com or any other host.
4. Choose "Open the example", then Save. Expected: the title shows the project saved; Open project lists it.
5. Open the account menu. Expected: "Homestead user" (not your user id), Preferences, no Sign out; the File menu has a Share entry (S439).
6. In the board, run Design rule check and add a copper pour (Repour). Expected: a result list appears, no console error; DevTools Console shows no CSP violation.
7. Rename the project from the project panel. Expected: the new name shows in Open project and the old one is gone.
8. Reload. Expected: the project reopens from Open project; the autosaved edit remains. File > Version history lists versions and restores one.
9. Draw a part into a schematic, update the board from the schematic, route a track, export Gerber and open the 3D view. Expected: all work.
10. Open a KiCad schematic or board file. Expected: it imports.
11. Second person: closed by default, then granted in People. Expected: an empty Open project list, none of the first person's projects. (Known: the recent-projects names in the same browser, see DEFERRED.)
12. Backup contains `data/bridge.db`; uninstall removes route and container.

Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7 [ ] 8 [ ] 9 [ ] 10 [ ] 11 [ ] 12

## S376 — Parametric catalog package
Prerequisites: VM 2 on the build with S376; Authentik sign-in set up; a second
person exists in People.

Steps and expected results:
1. Build `parametric` in the craft page or CLI. Expected: image `homestead/everything-parametric:0.1.0` recorded.
2. Install with a plan, preview and apply. Expected: route `/apps/parametric/`, no public exposure.
3. Apply sign-in. Expected: redirect to Authentik; no mention of Puter and no outbound link; no request to any other host.
4. Wait for the kernel to load, create a sketch and Extrude, then Save. Expected: the part saves and appears in Open.
5. Account menu. Expected: "Homestead user" (not your user id), no Sign out; the File menu has Share (S440), no Publish.
6. Look for any AI or assistant button. Expected: none; DevTools Network shows no AI request.
7. Rename the part. Expected: new name in Open, old gone.
8. Reload. Expected: the part reopens with its features.
9. Sketch, export STL/STEP, import a STEP file. Expected: all work.
10. Second person (closed, then granted): Expected: an empty Open list and no recovery prompt for the first person's work. (Known: recents names in localStorage.)
11. Backup contains `data/bridge.db`; uninstall removes route and container.

Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7 [ ] 8 [ ] 9 [ ] 10 [ ] 11

## S377 — 3D catalog package
Prerequisites: VM 2 on the build with S377; Authentik sign-in set up; a second
person exists in People; use Chrome or Edge for step 6.

Steps and expected results:
1. Build `three-d` in the craft page or CLI. Expected: image `homestead/everything-three-d:0.1.0` recorded.
2. Install with a plan, preview and apply. Expected: route `/apps/three-d/`, no public exposure.
3. Apply sign-in. Expected: redirect to Authentik; the 3D editor opens with a cube; no request to any other host; no Console CSP violation.
4. Look for AI, Report a Bug, Python and any Puter link. Expected: none; the Python console says it is not available and the script console says scripts cannot run.
5. Change a preference (for example the theme) and reload. Expected: it is kept.
6. In Chrome, use Open Folder in the file browser. Expected: the picker opens. In Firefox or Safari: only Add Files is offered.
7. Add an object, wait 3 seconds, reload. Expected: it is recovered (known race, see DEFERRED).
8. Second person (closed, then granted): Expected: default preferences and none of the first person's recovery or files.
9. Backup contains `data/bridge.db` (settings only; scenes are in the browser); uninstall removes route and container.

Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7 [ ] 8 [ ] 9

## S378 — Solitaire catalog package
Prerequisites: VM 2 on the build with S378; Authentik sign-in set up; a second person exists in People.

Steps and expected results:
1. Build `solitaire` in the craft page or CLI. Expected: image `homestead/everything-solitaire:0.1.0` recorded.
2. Install with a plan, preview and apply. Expected: route `/apps/solitaire/`, no public exposure.
3. Apply sign-in. Expected: redirect to Authentik; the board is dealt; no request to any other host; no Console CSP violation.
4. Look for "Powered by Puter" and any outbound link. Expected: none.
5. Make a few moves, reload. Expected: the game and move count are kept.
6. Second person (closed, then granted): Expected: a fresh game and empty statistics, none of the first person's.
7. Stop the container and reload. Expected: the fixed "You are offline" page, not an error page.
8. Backup contains `data/bridge.db` (saved game and statistics); uninstall removes route and container.

Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7 [ ] 8

## S379 — Sudoku catalog package
Prerequisites: VM 2 on the build with S379; Authentik sign-in set up; a second person exists in People.

Steps and expected results:
1. Build `sudoku` in the craft page or CLI. Expected: image `homestead/everything-sudoku:0.1.0` recorded.
2. Install with a plan, preview and apply. Expected: route `/apps/sudoku/`, no public exposure.
3. Apply sign-in. Expected: redirect to Authentik; a puzzle is dealt; no request to any other host; no Console CSP violation.
4. Look for "Powered by Puter", any outbound link, and the account label. Expected: no link; the label reads "Homestead user" and cannot be clicked.
5. Place a few digits, reload. Expected: the puzzle and entries are kept.
6. Second person (closed, then granted): Expected: a fresh puzzle and empty records, none of the first person's.
7. Stop the container and reload. Expected: the fixed "You are offline" page, not an error page.
8. Backup contains `data/bridge.db` (records and the saved puzzle); uninstall removes route and container.

Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7 [ ] 8

## S380 — Expenses catalog package
Prerequisites: VM 2 on the build with S380; Authentik sign-in set up; a second person exists in People.

Steps and expected results:
1. Build `expense` in the craft page or CLI. Expected: image `homestead/everything-expense:0.1.0` recorded.
2. Install with a plan, preview and apply. Expected: route `/apps/expense/`, no public exposure.
3. Apply sign-in. Expected: redirect to Authentik; the welcome dialog appears; no request to any other host; no Console CSP violation.
4. Look for "AI", "OCR", "read your receipts", "Puter", any outbound link and the account name. Expected: none of the first four; no link; the name reads "Homestead user", not your sign-in name. Settings has no AI section.
5. Capture a receipt photo (and a PDF), fill in merchant and amount, save, reload. Expected: the expense and receipt are kept; the Network tab shows only requests to this app's own path, none to an AI or other host; a file over 8 MB is refused with a notice.
6. Create a report, add the expense, export CSV. Expected: works with no request outside the app.
7. Second person (closed, then granted): Expected: no expenses, receipts or theme from the first person.
8. Stop the container and reload. Expected: the fixed "You are offline" page, not an error page.
9. Backup contains `data/bridge.db` (expenses and receipts); uninstall removes route and container.

Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7 [ ] 8 [ ] 9

## S423 — Source offer (Notes, 3D, Parametric)
Prerequisites: VM 2 on the build with S423; Notes, 3D and Parametric built and installed; sign-in set up.

Steps and expected results:
1. Open each of the three apps. Expected: a small "Source" link at the bottom of the page; it covers no button or text you need.
2. Click it in Notes. Expected: a plain page naming AGPL-3.0-only, the HeyPuter/everything repository, the pinned commit and a download link; no sign-in prompt while signed in.
3. Download the tarball (Notes, 3D). Expected: `<app>-source.tar.gz`; `sha256sum` equals the checksum on the page; it unpacks to a README, the pinned archive, the patch list, the shim and the Dockerfile.
4. Open the source page for Parametric. Expected: replicad-opencascadejs 1.1.0 (LGPL-2.1-only) with a source link and a licence-text link that opens the licence; no tarball link.
5. Open the same URL in a private window that is not signed in. Expected: sign-in, not the page.
6. Open `/apps/tasks/_homestead/source`. Expected: Not Found; Tasks shows no Source link.

Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6

## S436 — API Client catalog package
Prerequisites: VM 2 on the build with S436; Authentik sign-in set up; a second person exists in People; the host firewall egress rule from S421 in place.

Steps and expected results:
1. Build `api-client` in the craft page or CLI. Expected: image `homestead/everything-api-client:0.1.0` recorded.
2. Install with a plan, preview and apply. Expected: route `/apps/api-client/`, no public exposure; the container runs the bridge with `-net`.
3. Apply sign-in. Expected: redirect to Authentik; the app opens; no request to any other host; no Console CSP violation; no "Puter" wording or link.
4. Send a GET to a public address you own (for example a test page). Expected: status, headers and body shown; the Network tab shows only this app's own path.
5. Send to `http://127.0.0.1/`, a LAN address and the appliance name. Expected: each refused with "That address could not be reached from your Homestead"; `everything-bridge net-log` shows `blocked_address`, host and path only.
6. Send a request with a body over 2 MB, one whose response is over 8 MB, and 61 requests in a minute. Expected: a clear message for each (too large, too large, rate limited).
7. Look for New WebSocket, Share, raw sockets and the hosted-sync wording. Expected: none offered; a WebSocket import fails with "not available on Homestead".
8. Add a script to a request (pre-request or tests). Expected: unproven; note whether it runs or fails visibly (see DEFERRED S436).
9. Second person (closed, then granted): Expected: none of the first person's collections, environments or history.
10. Backup contains `data/bridge.db`; uninstall removes route and container.

Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7 [ ] 8 [ ] 9 [ ] 10

## Template for a new app section
## SNNN — <App>
Prerequisites: ...
Steps and expected results: (install via plan/apply, sign-in, second person closed then granted, offline, quota, backup, uninstall, SDK-surface checks specific to the app)
Pass/fail: [ ] 1 [ ] 2 ...

## S437 - Tasks sharing (ADR-058)
Prerequisites: Tasks installed with sign-in applied; two people granted the app (A, B) and a third with Homestead but no Tasks access (C).
Steps and expected results:
1. A makes a list with a task, opens Share, chooses "Invite people to edit". Expected: no Puter wording; an invite field.
2. A invites B by name, then a name nobody has. Expected: B can now edit; the unknown name is refused with "No one with access to Tasks is named ...".
3. B opens Tasks. Expected: the list and its task appear; A adds a task and B sees it within seconds without reload; B adds one and A sees it.
4. C (signed in, no access) opens Tasks. Expected: nothing of A's; C cannot be invited.
5. A creates a view-only link and opens it in a private window. Expected: a plain page with the list and tasks, no script, no sign-in; "Stop sharing" makes the link a 404.
6. A removes B. Expected: B no longer receives changes; B keeps a private copy as it was.
7. Settings: turn on the calendar feed. Expected: a feed URL; opening it on the LAN shows the calendar text.
8. Watch the container log: no token appears in any line.
Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7 [ ] 8

## S438 - Notes read links (ADR-058)
Prerequisites: Notes installed with sign-in applied; two people granted the app (A, B).
Steps and expected results:
1. A writes a page with text, a picture and a pen stroke, opens Share, chooses a 7 day expiry and "Create Link". Expected: a link ending in `/_homestead/view/<43 characters>`; no Puter wording.
2. Open the link in a private window with no session. Expected: a plain page with the title and text; the picture and handwriting are replaced by "not shown in this viewer"; no script, no sign-in.
3. B opens the same link while signed in. Expected: the same plain page.
4. A adds a paragraph and chooses "Create Link" again. Expected: reloading the link shows the new text.
5. B (no link) opens A's page address `#page=<id>`. Expected: B does not see A's page.
6. A chooses "Stop Sharing". Expected: reloading the link shows "not found".
7. Watch the container log: no token appears in any line.
Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7

## S439 - Circuits sharing (ADR-058)
Prerequisites: Circuits installed with sign-in applied; two people granted the app (A, B) and a third with Homestead but no Circuits access (C).
Steps and expected results:
1. A opens the example, saves it, then File > Share. Expected: "Not shared with anyone yet"; the field says "Homestead username"; no Puter wording.
2. A shares with a name nobody has, then with A's own name. Expected: "Could not share: ..." each time; nothing is shared.
3. A shares with B as "Can view". Expected: B listed. B opens Open project. Expected: a "Shared with me" list with the project, "Shared by A . view only". B opens it, changes it and saves. Expected: the save is refused; A's file is unchanged.
4. A changes B to "Can edit" (choose Can edit and press Share). Expected: "Updated access for B". B edits and saves. Expected: A reopens the project and sees B's change; A has one project file.
5. C (signed in, no access) opens Circuits. Expected: no "Shared with me", no projects, cannot reach A's project.
6. A removes B in the Share dialog. Expected: B's "Shared with me" is empty; B's next save fails with a message (it does not look saved); A keeps the project.
7. There is no link to share: Circuits has no read-only link (no viewer for circuit files).
8. Watch the container log: no token or project content appears.
Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7 [ ] 8

## S440 - Parametric sharing (ADR-058)
Prerequisites: Parametric installed with sign-in applied; two people granted the app (A, B) and a third with Homestead but no Parametric access (C).
Steps and expected results:
1. A opens the Mounting Bracket example, saves it, then File > Share (or the Share button in the title bar). Expected: "Not shared with anyone yet"; the field says "Homestead username"; no Puter wording.
2. A shares with a name nobody has, then with A's own name, then with an email address. Expected: "There is no Homestead user called ... with access to Parametric.", "You already own this part.", "Could not share: ..."; nothing is shared.
3. A shares with B as "Can view". Expected: B listed. B opens Open. Expected: "Shared with me" lists the part, "From A - View only". B opens it. Expected: a notice that it is view only; B's Save makes B's own copy in B's Parts; A's file is unchanged.
4. A changes B to "Can edit" (choose Can edit and press Share). B reopens the part from Shared with me ("Can edit"), changes it (for example Part Properties, decimal places) and saves. Expected: A reopens the part and sees B's change; A has one part file.
5. C (signed in, no access) opens Open. Expected: "Nothing has been shared with you yet"; cannot reach A's part.
6. A removes B in the Share dialog. Expected: B's "Shared with me" is empty; B's next save from the open part fails with "Could not save" (it does not look saved); A keeps the part.
7. There is no link to share: Parametric has no read-only link (no viewer for part files). An outsider with no sign-in cannot see anything.
8. The OpenCascade licence notice and the source page (S423) are still reachable from Help. Expected: both open.
9. Watch the container log: no token or part content appears.
Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7 [ ] 8 [ ] 9

## ADR-058 owner acceptance (S441)
One run on VM 2 with Authentik covers everything ADR-058 added. Do the per-app sections first or fold them in as noted: S437 (Tasks), S438 (Notes), S439 (Circuits), S440 (Parametric), S436 (API Client). This section adds only what they cannot show: two real Authentik people, the purge, the public read-link plan, the edge and real network egress.

Prerequisites: VM 2 on the build with S441; Authentik sign-in applied for Tasks, Notes, Circuits, Parametric and API Client; the public dashboard name and tunnel set up (ADR-046); two real people, A and B, in People, each with access to those five apps and each signed in to each app at least once (a name is only shareable after its first sign-in); a third person C with Homestead access and none of those apps. Container names are `<package>-<service>`; the bridge database is `/data/bridge.db`. Look at the daemon audit with the Audit page or `homesteadctl audit list`.

Steps and expected results:
1. Sign-in and names. A and B sign in to Tasks in separate browsers (not private windows of one profile). Expected: each is named by their Homestead user name, never an id or email; the container log has no token.
2. Tasks sharing (run S437 steps 1-6). Expected: invite by name works, unknown name refused, B sees A's changes within seconds without reload, C sees nothing, removing B stops changes. Look at: the app, and `docker exec tasks-tasks /everything-bridge grants list -db /data/bridge.db -app tasks` (ids and names only).
3. Circuits (S439 steps 1-6) and Parametric (S440 steps 1-6). Expected: view-only save refused (Circuits) or saved as B's own copy (Parametric); edit share lands in A's one file; removal makes B's next save fail visibly. Look at: the Share dialog and "Shared with me" in each app.
4. Purge on People removal. A shares a Tasks list and a Circuits project with B, and B creates a read link in Notes. Remove B in People (page or CLI). Expected: within about two minutes B's grants as owner and recipient are revoked in every Everything app, B's read links are revoked, B's open Tasks stream ends; A's own data is intact. Look at: the audit entry `people.purge` ok per app (pending means an app container was stopped; it retries every two minutes); `grants list` and `links list` for B show nothing live. Disabling B instead of removing does not purge (see DEFERRED S427).
5. Local read links (S437 step 5, S437 step 7, S438 steps 1-6). Expected: Tasks and Notes view links open in a private window as a plain script-free page on the app's local name; "Stop sharing" gives 404; the calendar feed opens on the LAN. Look at: the link ends in `/_homestead/view/<43 characters>`; `links list` shows id, owner, expiry, hits, never the token. Limits: default expiry 7 days, longest 365 days.
6. Public read links. For Notes: `homesteadctl apps read-links plan notes`, read the plan (public origin is the dashboard's public name; exactly two public patterns; everything else refused), then `apps read-links approve notes --digest <digest>` (or the Public read links panel on the app page). Expected: the app restarts once; the audit has `readlinks.expose`; a new link made in the app starts with `https://<public name>/apps/notes/_homestead/view/`. Repeat for Tasks.
7. What the public name answers. From outside the LAN (phone on mobile data) with no session: the public view link opens the plain page. Then try `https://<public name>/apps/notes/`, `.../apps/notes/_homestead/session`, a data path such as `.../apps/notes/_homestead/data/kv`, and `.../apps/tasks/_homestead/events`. Expected: all four answer 404 (a POST to a view link also fails); only `/_homestead/read/<token>` and `/_homestead/view/<token>` answer. After "Stop sharing" the link is 404 within a moment. Later `apps read-links plan notes --off`, then `approve notes --digest <digest> --off`: audit `readlinks.unexpose`, new links are local again.
8. Events through the edge. A and B open the same Tasks list over the public name (or LAN name through Caddy), A adds a task. Expected: B sees it within seconds, with no 25-second stall (that stall means the edge buffers the stream and only the heartbeat reaches B; Tasks falls back to polling, so the app still works but slowly). Look at: the browser Network tab on `.../_homestead/events` (one open `text/event-stream` request, events arriving individually), repeated through the Cloudflare tunnel.
9. api-client egress (S436 steps 4-6). Fetch a public host you own: Expected: status, headers and body. Fetch `http://127.0.0.1/`, a LAN address and the appliance name: Expected: each refused with "That address could not be reached from your Homestead". Look at: `docker exec api-client-api-client /everything-bridge net-log -db /data/bridge.db -app api-client -limit 20`: the success has host, path, status and bytes; each refusal shows `blocked_address`; no headers or bodies.
10. Limits. 61 requests in one minute from api-client: Expected: the 61st is rate limited. A 2 MB body or an 8 MB response: Expected: a clear too-large message. A 61st share attempt in an hour: Expected: refused. Read links: a link past its expiry is 404.
11. Backup and uninstall (S436 step 10 for one app). Expected: `data/bridge.db` is in the backup; uninstall removes route and container.

Pass/fail: [ ] 1 [ ] 2 [ ] 3 [ ] 4 [ ] 5 [ ] 6 [ ] 7 [ ] 8 [ ] 9 [ ] 10 [ ] 11

Record, for DEFERRED: the claim from each step that fails; the S436 step 8 script result; Firefox and Safari behaviour of the share dialogs.
