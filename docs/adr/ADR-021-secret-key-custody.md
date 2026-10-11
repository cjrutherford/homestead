# Secret Key Custody and Re-key

## Status
Proposed

## Decision
The secret-store key stays local on the appliance; no external KMS or
hosted service is used in v1. The key file carries a version, and the
store records which key version encrypted each row. Re-key is an audited
owner operation: generate a new 32-byte key, re-encrypt every row inside a
single SQLite transaction, and write the new key file atomically
(temp file, fsync, rename, fsync of the directory). The previous key is
retained until the transaction commits and the new file is durable. A
failure at any step leaves the store readable with the old key.

## Context
`internal/secret/secret.go` stores AES-GCM ciphertext rows in SQLite and
keeps one 32-byte key at `layout.LibDir/secret.key` (mode 0600). There is
no rotation path; DEFERRED.md records that losing the key strands
ciphertext and that rotation must exist before long-lived production
secrets depend on it. Backup encryption (ADR-022) and secret-backed
container variables build on this store.

## Alternatives considered
- External KMS or cloud key service: violates local-first and customer
  ownership; adds a runtime dependency.
- Passphrase-derived key: forces interactive unlock at every daemon start,
  breaking unattended boot under systemd.
- Re-key without a transaction: a crash mid-way leaves mixed-key rows.

## Consequences
- The key file must be included in the owner's recovery material; the
  runbook must say so.
- Re-key requires an exclusive window; secret reads block briefly.
- Operation appears in the audit log without logging key material.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
