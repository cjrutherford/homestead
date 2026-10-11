# S259 — Backup Encryption

## Goal
Encrypt backup archives with age and document restoring without Homestead, per ADR-022.

## Dependencies
S181,S258

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
ADR-022 is Proposed and must be Accepted before implementation begins.

## Acceptance Criteria
- Backup Encryption implemented and documented
- `internal/backup/backup.go` writes archives encrypted with `filippo.io/age` to X25519 recipients; manifest of non-secret metadata stays readable
- Appliance backup identity stored in the secret store (S258); owner can export a recovery identity and add recipients over API and CLI
- Restore (S185/S186 paths) detects format and decrypts; plaintext backups from earlier versions still restore
- Runbook in `docs/` shows restoring with the plain `age` CLI and standard tools, verified by a test using the library as an independent decryptor
- Doctor warns when no owner-held recipient is configured
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
