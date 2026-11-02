# Everything apps review plan

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

## Template for a new app section
## SNNN — <App>
Prerequisites: ...
Steps and expected results: (install via plan/apply, sign-in, second person closed then granted, offline, quota, backup, uninstall, SDK-surface checks specific to the app)
Pass/fail: [ ] 1 [ ] 2 ...
