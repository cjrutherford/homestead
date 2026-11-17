# S457 - Uninstall removes the app's edge routes

## Goal and dependencies
S285, S299, S452 finding. Uninstall left the app's route (for example `komga.lan`) in the recorded edge table and in Caddy, so a reinstall with "Also publish at" ticked was refused and the checklist's "no leftover route" expectation failed.

## What changed
- The uninstall operation has a `remove-edge-routes` step after the stack files are removed: `removeAppRoutes` drops the app's routes (its LAN, .local and path routes; public names were already refused by the guard) from the table and reconciles the edge. Other apps' routes and the dashboard route stay. Audit `app.uninstall.routes`.
- Not fixed here: removing a public name from a forward-auth app still leaves its Authentik provider on the old name until sign-in is planned again. That is Authentik-side, not edge rendering, and stays in DEFERRED (S451).

## Tests
`internal/server/uninstallguard_test.go` (TestRemoveAppRoutesLeavesOthers), `tests/test_s457_uninstall_removes_routes.py`.
