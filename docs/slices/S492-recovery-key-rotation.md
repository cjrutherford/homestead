# S492 - Recovery key rotation

## Goal and dependencies
Owner 2026-11-08: add a recovery-key rotate command to the CLI and the dashboard, then rotate the key on VM 2. Depends on S271 (recipients, export, acknowledge) and S486 (parity).

## Design (see the ADR-022 amendment)
- The recovery key is the appliance age identity. Backups are encrypted to recipients: each age file wraps a random per-file key once per recipient, so the key protects every file through its header, not its payload.
- Rotation generates a new identity, retires the old one on the appliance (`sys_backup_identity_retired`), and re-wraps the header of every existing run file under the backup destination: new stanza added, existing stanzas kept, payload copied unchanged, full decrypt check before an atomic replace. Nothing is deleted. Old runs open with the new key, the old key and any owner recipient.
- `POST /api/v1/backups/identity/rotate` (`confirm: true`): step-up like export, audited (`backup.identity.rotate`, fingerprints only), fails closed, shares the export rate limit (checked before any key changes), `Cache-Control: no-store`. The reply carries the new key once, the previous fingerprint, and rewrapped/already_current/failed counts.
- The rotation is recorded as an export that must be acknowledged: Doctor warns until the owner acknowledges (`backup identity ack`, `rotate --ack`, or the page button).
- CLI: `homesteadctl backup identity rotate (--out FILE | --stdout) [--ack]`. Dashboard: Rotate recovery identity beside Export, with a confirmation dialog and the same one-time reveal and acknowledge.

## Not built
See DEFERRED (S492): other mounts, pruning retired identities, re-encrypting runs for a leaked key.

## Verification
`internal/backup/rotate_test.go`, `internal/server/backuprecipients_test.go` (rotate), `internal/server/sso_test.go` (step-up list), `cmd/homesteadctl/backuprecipients_test.go`, `dashboard/src/app/backups/backups.spec.ts`, `tests/test_s492_recovery_key_rotation.py`, live on VM 2 (PLAT-BACKUP-4).
