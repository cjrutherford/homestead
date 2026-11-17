# S444 - Gitea: no manual first run

## Goal and dependencies
ADR-059: Gitea needs no manual first account. Depends on S442/S443.

## What changed
- `internal/firstrun/gitea.go` (routine `gitea`): waits for `/api/healthz`, and when Gitea has no administrator creates `homestead-admin` with `gitea admin user create --admin --random-password --random-password-length 32 --must-change-password=false` run as the git user (`su-exec`). That command cannot take a password from stdin or a file, so Gitea generates it and Homestead reads it from the command output: it is never an argument. It is sealed as the revealable `admin_password`; if sealing fails the account is deleted again. Any existing administrator means nothing is changed.
- `catalog/gitea.yaml`: `DISABLE_REGISTRATION` is now true, so nobody can claim the app; `firstrun` and the `admin_password` secret added; copy updated.
- S416 reconciled: sign-in still needs no administrator, but now one always exists. The name `homestead-admin` is unlikely to equal an Authentik username, and account linking asks for its password before any sign-in could take it over. Sign-in demotes only non-listed admins at their own sign-in; the local admin is not touched unless it signs in through Authentik.
- Checklist SRV-GITEA-1a rewritten, dashboard curated copy updated.

- Platform fixes found live: (1) the public-address plan approved before install never matched the one the setup re-checks after install when the Authentik token is kept (sign-in text and warnings differed), so first install with a public name stopped at "plan changed"; the anticipated plan now says "needs no change" when the token is kept, and the sign-in plan's own warnings are no longer repeated in a no-change public plan. (2) Uninstall left `people_grants` rows, so a reinstall of a managed app refused the access choice ("Previous app sign-in or grants remain") with no way out; uninstall now forgets the app's grants.

## Tests
`internal/firstrun/gitea_test.go`, curated and appsso tests, `tests/test_s444_gitea_first_run.py`.
