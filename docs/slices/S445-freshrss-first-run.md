# S445 - FreshRSS: no web installer

## Goal and dependencies
ADR-059: FreshRSS needs no manual installer. Depends on S442-S444.

## What changed
- `internal/firstrun/freshrss.go` (routine `freshrss`): runs the pinned image's own CLI as www-data (`cli/do-install.php --default-user U --auth-type form --db-type sqlite`, then `cli/create-user.php`). U is the owner's Authentik username (so sign-in maps the owner onto the default user, FreshRSS's administrator), else the manifest account `admin`. The user's password is generated and sealed (revealable `admin_password`) only after FreshRSS accepted it. It travels on stdin: the container shell reads it with `$(cat)`, so it is no argument of anything Homestead runs. Idempotent; an install made before the owner was known gets the owner's user added and made default with `cli/reconfigure.php --default-user`.
- Smallest plumbing change: `firstrun.RunAs(ctx, m, secrets, owner)` (`Run` is `RunAs` with no owner). The server resolves the linked owner's username through Authentik (`appSignIn.firstRunOwner`; `sso.RemoteUser` gained `Username`) at install and at sign-in apply. Other routines ignore it; a later app needing the owner's name (Actual Budget) can reuse it.
- Sign-in apply runs the first-run step before `reconfigure.php --auth-type http_auth`, so a first install succeeds.
- `catalog/freshrss.yaml`, dashboard curated copy, DEVELOPMENT.md and checklist SRV-FRESHRSS-1a updated.

## Tests
`internal/firstrun/freshrss_test.go`, curated and appsso tests, `tests/test_s445_freshrss_first_run.py`.
