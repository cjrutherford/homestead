# S507 - Nextcloud admin follows the exact Homestead group and is removed on revoke

## Goal and dependencies
Nextcloud administrator rights must come only from the Homestead admins group, and end when Homestead takes a person's access away. Depends on S500 and S317.

## Problem
1. The S500 listener granted Nextcloud's `admin` group to anyone in any group whose display name was `admin`, so a group not made from the Homestead provider's admins claim could grant admin to a user_oidc member.
2. Admin rights were removed only at the person's next sign-in, so a revoked person kept them through open sessions and app passwords.

## Fix
- The listener computes the ids user_oidc gives the Homestead provider's admins group, sha256 of `<provider>_0_admin`, for both the provider's database id (read at run time from user_oidc's `ProviderMapper` by the identifier `authentik`) and the identifier itself, and grants only for membership of one of those exact groups. Display names are never matched.
- People revoke of Nextcloud, a change from admin to user, disable and remove call `DropAppAdmin`. The daemon runs, in the Nextcloud container, `occ user:list --info --output=json` and `occ group:removeuser admin <uid>` for the person's `user_oidc` account, found by email (or uid equal to the username). The local `admin` account and every other backend are never touched. Nothing happens when Nextcloud sign-in is off.
- A failure never blocks the People action: it is audited (`people.app-admin-drop`, no secrets) and returned in a `warnings` list in the response.

## Not built
The group id derivation is taken from S500's recorded user_oidc 8.13 behaviour, not verified live; if user_oidc keys it differently nobody is granted admin until the listener is adjusted (the owner's local admin still works). Accounts without an email and with a hashed uid are not found by the removal and still lose rights at next sign-in. Removing someone from the admins group directly in Authentik is not covered. See DEFERRED S500.

## Verification
Go: `internal/people/s507_test.go`, `internal/appsso/s507_test.go`. PHP lint: `TestNextcloudAdminAppIsValidPHP`.
