# S449 - Published single-origin apps redirect their LAN names to the public name

## Goal and dependencies
Gitea, Actual Budget, Mealie and Calibre-Web Automated cannot finish sign-in from their `.lan` name once published (ADR-060, amends ADR-047). Depends on S444-S448.

## What changed
- ADR-060 (orchestrator decision, owner review pending), ADR-047 amended-by pointer, README row.
- Manifest field `single_origin_sign_in` (set on the four apps); `Route.SingleOrigin` is stamped from the catalog on every edge render (`stampSingleOrigin`: edge reconcile, tunnel/app-public reconcile) and once at daemon start (`RunSingleOriginMigration`) so installed apps take it without a manual step.
- Edge: a published single-origin route's local names (`.lan`, `.local`) answer `307 https://<public>{uri}`; the tunnel entry still serves the app. 307 so browsers do not cache it; unpublishing removes it.
- Public-address plan preview lists the redirect and its cost (warning, covered by the digest).
- Removed the S447 Actual warning, the Gitea/CWA "one address" wording, checklist notes and the DEFERRED entries for S444, S447 and S448.

## Tests
`internal/docker/singleorigin_test.go`, `internal/server/singleorigin_test.go`, `tests/test_s449_single_origin_redirect.py`.
