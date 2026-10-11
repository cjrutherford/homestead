# Backup Encryption Format

## Status
Proposed

## Decision
Backup archives are encrypted with the age format (`filippo.io/age`,
X25519 recipients) so the owner can decrypt them with the standard `age`
CLI without Homestead. The appliance generates a backup identity and keeps
it in the secret store (ADR-021). The owner can export a recovery identity
and add their own recipients; backups are encrypted to every configured
recipient. The manifest of non-secret metadata (package names, timestamps,
sizes, format version) may remain readable beside the archive. A runbook
documents restoring with plain `age` and standard tools.

## Context
`internal/backup/backup.go` copies data as plaintext. Backups land on
mounted storage (ADR-013) that may be removable or shared, and the secret
store and exported configuration can be included in them.

## Alternatives considered
- Custom AES container: creates lock-in; only Homestead could read it.
- GPG: heavier key management, weaker defaults, harder to document.
- Symmetric passphrase only: single point of failure, no multi-recipient
  recovery.
- Leave plaintext and rely on disk encryption: not under Homestead's
  control and not verifiable.

## Consequences
- Losing every identity makes a backup unrecoverable; export prompts and
  doctor warnings are required.
- Existing plaintext backups remain restorable; restore detects format.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
