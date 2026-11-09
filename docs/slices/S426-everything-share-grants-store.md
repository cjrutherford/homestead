# S426: Everything bridge share grants store

ADR-058 section 3. Depends on S425. Bridge only (Go); no SDK surface, no daemon, API, CLI or dashboard change, so no PARITY change.

## Scope
- A `grants(id, app, owner_subject, recipient_subject, kind, object_key, is_prefix, access, label, created_at, updated_at, revoked_at)` table with a unique live grant per (app, owner, recipient, kind, key). `id` is 128 random bits, URL-safe.
- Store operations: `CreateGrant` (typed username, upsert keeps the id), `RevokeGrant` (owner, by id), `RevokeShare` (owner, by name and object), `GrantsByOwner` (revoked included), `GrantsForRecipient` (usable only), `ResolveGrant`, and `GrantGet`/`GrantPut`/`GrantDelete` through a grant.
- `PurgePerson` also revokes the person's grants as owner and as recipient.
- Export format `homestead-everything-export/2` adds `people` and `grants`; `Import` still reads `/1`; `ImportAll` restores `/2` grants only when both people already exist live in the target.
- `everything-bridge grants list [-app APP] [-all]` and `grants revoke -app APP -id ID`.

## Design as built
- Rows are never copied. `ResolveGrant` rewrites the recipient's operation to the owner's (app, subject, kind, key) after checking id, app, kind, recipient, revoked state, and that neither person is purged (an "inert" grant). Every failure that could reveal anything is `ErrNotFound`; `ErrReadOnly` is returned only to the proven recipient of a read grant.
- Key rules: a single-file grant takes only an empty relative key; an fs folder grant joins `prefix/rel` and refuses anything not already clean (`..`, `/`, `\`, `./`, trailing `/`); a kv prefix grant appends the relative key and the result is validated like any KV key. A kv share prefix may not end in `*`.
- Recipient writes call `Put` with the owner's scope, so the owner's person and app quotas pay (tested: the recipient's own quota is untouched).
- Folder or kv-prefix grants refuse deleting the folder itself and a value at the bare prefix; a single-file grant never allows delete.
- Limit: 200 live grants per owner per app (`ErrGrantLimit`); updating an existing share does not count.
- Typed names that are unknown, purged, other-app-only, empty, over-long or malformed all give `ErrNoRecipient`; naming yourself gives `ErrSelfShare`; a purged owner cannot share.
- Export `people` rows carry no uuid (it derives from a key exports never hold). Import does not create people. `grants list` prints ids, names, keys and state, never values.

## Tests
`internal/everything/grants_test.go`: kv round trip with relative keys and no copy, read-only refusal, cross-person and cross-app denial, key escapes, file vs folder grants, owner-quota charge, revoke/upsert/listing, uniform recipient errors and input validation, the 200 limit, purge as owner and recipient (and no revival on re-sign-in), inert grants, export `/2` round trip with leak checks and import preconditions, filtered export, `/1` import, upgrade of an older file. `cmd/everything-bridge/tools_test.go`: grants tool and export/import of grants.

## Unproven
Nothing calls the store from a request yet. The 24 hour session recheck and the 60 per hour share-attempt limit belong to the first handler slice that exposes a share operation.
