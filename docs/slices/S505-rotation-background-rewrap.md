# S505 - Recovery key rotation re-wraps in the background

## Goal and dependencies
Rotation must return quickly and survive a large backup set. Depends on S492 and ADR-022.

## Problem
Rotation re-wrapped every run inside the HTTP request, so a large destination timed out, a backup run could race the mint, a retired-key line that failed to parse broke restore, and a failed re-wrap left no durable trace.

## Fix
- `RotateIdentity` only mints (backup gate `TryLock`, then identity lock; 409 `ErrBackupRunning`/`ErrRewrapRunning`). The handler refuses an unacknowledged current-key export, writes an incomplete marker, and starts the `backup.rewrap` operation (`clean`, `rewrap`).
- `POST /api/v1/backups/identity/rewrap`, `homesteadctl backup identity rewrap` and a dashboard button re-run it; idempotent, audited, one at a time.
- Backup jobs hold the gate lazily (`Job.RecipientSource`) and release it on every end, including a failed start.
- Stale `.rewrap-*` temps are cleaned; retired identities parse tolerantly; the summary is stored in the new table `backup_rewrap_state` (migration 59) instead of a settings row.
- Doctor check `backup-rewrap`; ADR-022 amendment.
- The CLI writes the new key to its file or stdout before following the operation.

## Not built
Retired-key pruning, and resuming an interrupted re-wrap (it is cancelled; Doctor shows it pending).

## Verification
Go: `internal/backup/rotate_test.go`, `internal/server/backuprecipients_test.go`, `cmd/homesteadctl/backuprecipients_test.go`; dashboard `backups.spec.ts`; `tests/test_s492_recovery_key_rotation.py`. Tests assert no key material in operations, audit or output.
