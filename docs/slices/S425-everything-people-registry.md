# S425: Everything bridge people registry

ADR-058 section 1-2. Depends on S424. Bridge only (Go + shim); no daemon, API, CLI or dashboard surface, so no PARITY change.

## Scope
- Sign-in asks for `openid profile`.
- A `people(app, subject, username, username_key, uuid, first_seen, last_seen, purged_at)` table, filled from the verified ID token on every sign-in. Live names are unique per app (partial unique index on the lower-cased name). `bridge_meta` holds a random HMAC key.
- `auth.getUser()` returns the person's own `preferred_username` and `p_` + base32(HMAC(key, app, sub)) (16 bytes). The `whoami` and session answers no longer carry the `sub`.
- Optional group check: `-oidc-groups` / `HOMESTEAD_EVERYTHING_OIDC_GROUPS`. A token with a `groups` claim must list one of them (403 otherwise, nothing recorded); no claim passes.
- `everything-bridge people purge -db FILE -app APP (-subject SUB | -username NAME)`: marks the person purged and deletes their sessions in that app. Data stays. Idempotent, prints `purged N people`, never creates a database file.

## Design as built
- Name reuse: a second live person verified with the same name (any case) takes it; the older row keeps its uuid but loses the name. Names resolve to the newest verified holder.
- A purged person who signs in again (Authentik allows them) is live again, name reset to the new claim.
- A token without a usable name (empty, control characters, over 255 bytes) signs in with no name.
- Subjects without a row (development identity) still get a uuid; the dev identity supplies the subject as display name so gate users stay distinct.

## Tests
`internal/everything/people_test.go` (sign-in records, uuid stable and per app, no `sub` in answers, nameless sign-in, name reuse, group check, purge ending sessions, cross-app and cross-person isolation, idempotent/unknown, data kept, re-sign-in); `cmd/everything-bridge/tools_test.go` (purge tool); existing OIDC tests updated for the `profile` scope and `sub`-free answers. Bridge gate 9/9, Projects browser check.

## Unproven
Not exercised against a live Authentik: its `profile` scope should carry `preferred_username` and `groups`, as documented, but the claim shape was not observed.
