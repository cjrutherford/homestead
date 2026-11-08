# S460 - Immich: administrator role through the claim, and the welcome tour

## Goal and dependencies
ADR-059 (no manual first run). Depends on S298 and S456. The acceptance run reported the owner (akadmin) as an ordinary Immich user and a welcome tour for every person.

## Findings
- (a) The role mapping already exists: sign-in puts `claims["immich_role"] = "admin" if admin else "user"` in the groups scope mapping (`internal/appsso/adapters.go`, `access.go`) and writes `roleClaim: immich_role` into Immich's OAuth config. Immich 3.3.0 (`auth.service.js`) reads the claim at every login and updates `isAdmin` for existing accounts too. The owner's account in the run had been created before the mapping was in place; after sign-in was applied again it was an administrator. Nothing needed changing in code.
- (b) No supported way to skip the tour in 3.3.0; see docs/DEFERRED.md (S460 entry) for the evidence. It stays deferred.

## Live (VM 2)
With the owner's Immich account forced to non-admin, one owner sign-in made it admin again and `/admin/users` opened. Person B (granted for the check, revoked afterwards) signed in for the first time: new account, ordinary user, welcome tour shown. Person A: ordinary user, `/admin` redirects to photos. Person C refused by Authentik ("Permission denied").

## Tests
`tests/test_s460_immich_admin_role.py` (claim, roleClaim, version pin), `internal/appsso/access_test.go` (existing claim and roleClaim tests).
