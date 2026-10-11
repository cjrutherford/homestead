# S259 — Backup Encryption

## Goal
Encrypt backup archives with age to the appliance identity and restore them, per ADR-022; recipient management follows in S271 and the runbook in S275.

## Dependencies
S181,S185,S186,S258,S267

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-022, Accepted 2026-10-11.
Split: this slice is the encrypting writer plus the matching restore decryption, so main never holds backups it cannot restore. Owner recipients and identity export are S271; the restore runbook is S275.

## Acceptance Criteria
- Backup Encryption implemented and documented
- Adds `filippo.io/age` to `go.mod` (dependency approved by ADR-022), pinned, with its license noted in `docs/`
- `internal/backup/backup.go` writes archives encrypted to the appliance backup identity's X25519 recipient; the identity is generated once and held in the secret store (S258); manifest of non-secret metadata stays readable with a format version
- Restore (S185/S186 paths) detects the format and decrypts with the appliance identity; plaintext backups from earlier versions still restore
- Independent decryptor test invokes the real `age` CLI binary (not the library) on a produced archive using the identity extracted from the test secret store, and verifies the extracted contents; the acceptance run fails, not skips, if `age` is absent
- Existing backup API responses report `encrypted` and format version, shown by the S267 dashboard surface and `homesteadctl backup`; `docs/PARITY.md` updated for every added or changed route (`tests/test_s037_parity.py` stays green)
- Update the "Backup encryption at rest" entry in `docs/DEFERRED.md` when landed: narrow it to recipient management and the runbook (S275 removes the entry)
- Doctor warns while backups are encrypted only to the appliance identity (ADR-022); S271 adds the owner actions that clear it
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
