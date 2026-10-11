# S258 — Secret Re-key

## Goal
Key versioning and an audited, crash-safe re-key of the secret store, per ADR-021.

## Dependencies
S096

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
ADR-021 is Proposed and must be Accepted before implementation begins.

## Acceptance Criteria
- Secret Re-key implemented and documented
- `internal/secret/secret.go` records a key version per row and in the key file
- Re-key generates a new key, re-encrypts all rows in one SQLite transaction, then writes the key file atomically (temp, fsync, rename, directory fsync)
- Previous key retained until commit succeeds; injected failure at each step leaves the store readable with the old key
- Re-key exposed over the API and `homesteadctl` with dashboard parity row in `docs/PARITY.md` (dashboard may be `n/a` with reason)
- Operation is audited without key material in logs
- Remove the secret re-key entry from `docs/DEFERRED.md`
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
