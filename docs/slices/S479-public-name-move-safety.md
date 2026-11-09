# S479 - Public-name move safety

## Goal and dependencies
Closes the gaps S471 left in the move from a public name to a subpath (ADR-061, `docs/DEFERRED.md` S471). Depends on S471. Runs before S472 (owner request), so the first package to use the move has a safe one.

## What changed
- **The new address is checked before the old name goes.** After `verify-path` the move requests `https://<dashboard public name>/apps/<app>/` through the public edge (`probe-public` step, 4 tries, 2 s apart). A 404 (the edge's answer for an unpublished path) or a 5xx or a connection error does not count; a sign-in redirect, 401 or 403 does. If it never answers the run stops and the connector entry and CNAME stay.
- **A half-done move can be finished.** The old name is written to `app_move_pending` (migration 58) when the move starts and cleared by the last step. When a move stops after the edge step, `GET /catalog/<app>/move` reports `finish: true` while the name is still held; `POST .../move/plan` returns a finish plan (nothing of the app changes; the check, then the removal) with its own digest, and `.../move/apply` runs it. The CLI says "unfinished"; the app page shows "Finish the move". No new route or command.
- **Doctor `public-name-plan`** (Tunnel group, local state): any connector entry or Homestead-created DNS record that no edge route carries is flagged. If the name belongs to a pending move the fix is the finish plan; otherwise `tunnel plan`.
- **Flaky tests.** `TestAppPublicAddRemove` and `TestReadLinksExposeAndRemove` read the audit log right after the operation finished, but the completion entry is written by a goroutine (`auditOperation`) that races the test. `Server` now counts those waiters and tests call `waitAudits()` before reading the log. No sleeps.

## Not covered
See `docs/DEFERRED.md` (S479): rollback to the old name (needs an owner decision), one-step removal of stray names, read-link re-pointing.

## Tests
`internal/server/appmove_public_test.go` (probe failure keeps the old name, finish, doctor, probe statuses), `cmd/homesteadctl/appmove_test.go`, dashboard `app-move.spec.ts`, `tests/test_s479_move_safety.py`.
