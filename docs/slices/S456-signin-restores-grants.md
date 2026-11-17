# S456 - Turning sign-in back on restores people's access

## Goal and dependencies
ADR-043, ADR-059. Depends on S314. Turning an app's sign-in off deletes its Authentik groups; turning it on made them empty, so every person with a recorded grant read as drifted and was refused until the owner ran `homesteadctl people resolve PERSON put-back`.

## What changed
- `people.Service.RestoreApp(ctx, app)` adds each person with a recorded grant for the app back to its user group (not for admin-only apps) and, for the admin role, to its admin group. It only adds members.
- `appsso.Runtime.RestoreAccess` and a `restore-people` step in the sign-in apply operation, right after Authentik is configured (OIDC and forward-auth apps alike), so it is part of the same approved apply. A failure fails the apply; turning sign-in off does not run it. The audit records `app.sign-in.people` with a count.
- No API, CLI or dashboard surface changed.

## Tests
`internal/appsso/appsso_test.go` (TestApplyRestoresPeoplesAccess), `internal/people/people_test.go` (TestRestoreAppRepopulatesGroupsFromGrants), `tests/test_s456_signin_restores_grants.py`.
