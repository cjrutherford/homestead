# S258 — Secret Re-key

## Goal
Versioned secret keys and an audited, crash-safe re-key of the secret store, per ADR-021.

## Dependencies
S096,S248,S257

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-021, Accepted 2026-10-11.
Depends on S257 so the new re-key route is never exposed ungated.

## Acceptance Criteria
- Secret Re-key implemented and documented
- Versioned key files `secret.key.v<N>` under `layout.LibDir`; `internal/secret/secret.go` records the key version per row and opens each row with its recorded key
- Migration of the existing raw 32-byte key (written at `internal/secret/secret.go:86`) to version 1 per ADR-021: copied to `secret.key.v1`, unversioned rows stamped 1, legacy file left in place and ignored; tested from a pre-S258 fixture
- Re-key order per ADR-021: write the new versioned key file (temp, fsync, rename, directory fsync) before the DB commit; re-encrypt all rows in one SQLite transaction; commit; decrypt-verify every row; only then delete the old key file
- On start, older key files are deleted once every row is on the newest version and verifies (ADR-021 step 5)
- Injected failure at each step (before key write, mid-transaction, after commit before verify, before old-key delete) leaves the store readable and rerunning re-key succeeds
- Re-key exposed over the API and `homesteadctl`; the dashboard gets a confirmed re-key action with operation progress using S248 tokens and `StateMessage`
- `docs/PARITY.md` updated for every added or changed route (`tests/test_s037_parity.py` stays green) with both CLI and dashboard cells filled
- Operation is audited without key material in logs
- Runbook states that every retained key file belongs in the owner's recovery material
- Remove the secret re-key entry from `docs/DEFERRED.md` when landed
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
