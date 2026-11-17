# S428: Everything KV sharing, perms.request and appID

ADR-058 sections 2-3 and the SDK surface. Depends on S425, S426. Bridge and shim only; no daemon, API, CLI or dashboard change, so no PARITY change. The Tasks UI stays hidden (S437).

## Scope
`kv.share`, `kv.unshare`, `kv.listShared`, `kv.listSharedByMe`, the `{handle}` option on `kv.get/set/del/list` (and batch set), `perms.request` with `perms.Resource.KV_SHARE`, and `appID`. Items S426 deferred here: the share-attempt rate limit, the 24 hour session recheck, `{handle}` list cursors, `manage` -> `bridge_unsupported`, and the uniform unknown/no-access username error.

## Design as built
- Data operations `kv.share`, `kv.unshare`, `kv.listShared`, `kv.listSharedByMe`, `perms.request` go through the existing POST data route (exact Origin, Sec-Fetch-Site, content type, CSRF). New request fields: `handle, prefix, recipient, access, label, limit, cursor, resource, permission`. `internal/everything/sharing.go` holds the handling.
- Errors are JSON `{"error": code}`: `kv_handles_disabled` (403), `subject_does_not_exist` (404; unknown, purged, other-app-only, malformed and over-long names are byte-identical), `cannot_share_with_self`, `invalid_recipient` (any `@` inside the name, or empty; the bridge holds no emails), `kv_share_label_invalid`, `bridge_unsupported` (access `manage`), `invalid_argument`, `share_limit` (200 live grants), `share_rate_limited` (429), `kv_handle_not_found` (404), `kv_handle_read_only` (422). `events_kv_handle_not_delegated` is never emitted.
- Rate limit: 60 `kv.share` attempts per person per app per hour, successes and failures, in memory (injectable clock). `unshare` and listing do not count.
- Session recheck: recipient-side operations (any kv call with a `handle`, `kv.listShared`) need `SessionAger.SessionAge` under 24 hours; otherwise 401, and the shim sends the page to sign-in (silent for a person Authentik still allows). An identity that cannot report age is refused. Implemented by OIDC (session `created`) and the dev identity. The owner's own operations are not rechecked.
- `{handle}` operations resolve through `ResolveGrant` (so cross-person, cross-app, kind, revoked, purged and key-escape denial is the store's) and are rewritten to the owner's scope: the owner's quota pays. A missing key is a plain 404 (`null` in the shim); a bad handle is the JSON `kv_handle_not_found`. `list` and `listvalues` take a prefix relative to the share, and the `after`/`next` cursors are relative too (the owner's prefix never appears). Values at the bare prefix cannot be written or deleted. A handle with `kind: file` is refused (S429).
- `listShared`/`listSharedByMe` return `{items, cursor}` (default page 100, max 200, cursor `<created>:<id>`, bad cursor 400). Items: `handle, holder, owner, prefix, access, label, createdAt (unix seconds), revokedAt (null or seconds), appUid`. `listSharedByMe` includes revoked items.
- `perms.request`: `kvShare` with a valid prefix and `read|write`, or the legacy `manage:kv-share-<read|write>:<uuid>:<appID>:<prefix>` whose uuid is the caller's and appID equals `app-<app>`, resolves `true` with no state; anything else, or sharing off, rejects `invalid_argument` (Tasks reads that as "use links").
- `appID` is `app-<app>` (`AppID`), exposed by the shim as a getter once the app is known.
- Sharing is off unless the bridge runs with `-sharing` / `SHARING=1` (`Store.SetSharing`); off answers `kv_handles_disabled`.
- Shim: `jsonError` surfaces `{error}` replies as `{code}`; a 401 after a successful CSRF refresh redirects to sign-in. Gate test updated: sharing off by default; new stubbed-fetch test of the shim's mapping.

## Tests
`internal/everything/sharing_test.go` (round trip with no copy, read-only, sibling prefix, cross-person, cross-app, revoke, uniform errors, rate limit and reset, grant limit, 24 h recheck incl. no-age identity, sharing off, perms, cursors, `listvalues` cursor over 520 keys, CSRF on every new op, unknown fields, OIDC `SessionAge`). Gate bridge mode (10/10) and a Projects browser check. Python tests updated for the shim surface.

## Unproven
No live OIDC two-person run; the Tasks UI is hidden so the real Tasks code does not exercise sharing yet (S437). No package turns `-sharing` on.
