# S496 - FreshRSS owner sign-in

## Goal and dependencies
Fix the 500 on `/i/oidc/` for the owner (DEFERRED S482). Depends on S412 and S482.

## Root cause
Confirmed on VM 2 from the FreshRSS Apache log: `oidc_cache_shm_set: could not store value since value size is too large (17759 > 16400)`. FreshRSS signs in through Apache's mod_auth_openidc, which stores the whole token session in a shared-memory entry of at most 16400 bytes. The owner holds the groups of every app (91 on VM 2) and Authentik's built-in `profile` scope lists every group a person holds, so the session overflowed. People with few groups fit.

## Fix
- New manifest flag `sso.oidc.env.trimgroups` (FreshRSS sets it). The app's Homestead claim mapping then answers to the `profile` scope instead of `groups` (`Access.ClaimScope`) and lists only the app's own three groups, which replaces the built-in `groups` claim. The standard `profile` mapping is not bound for FreshRSS (Authentik's evaluation order is not relied on): the app's mapping carries the profile claims (name, preferred_username, picture and so on) itself. Access control is unchanged: it is the Authentik application bindings, not the claim.
- `scopePKs` skips Homestead-named mappings so another app never takes a profile-scope claim mapping as the standard `profile`.
- The environment route was not used: the image reads `OIDC_SESSION_TYPE` but not the cache entry size, and a file mount would be a new decision.
- Applying FreshRSS sign-in again updates the mapping (scope and expression) through the usual plan and approval.

## Not built
Other apps keep the wide claim (see DEFERRED S496).

## Verification
Go: `internal/appsso/trimgroups_test.go`, `internal/sso/access_trim_test.go`. `tests/test_s496_freshrss_owner_sign_in.py`. Live on VM 2: owner and Alex sign in, Casey is refused (SRV-FRESHRSS-3 to 5).
