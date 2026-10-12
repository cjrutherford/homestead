# Secret Key Custody and Re-key

## Status
Accepted

Accepted by the owner on 2026-10-11.

## Decision
The secret-store key stays local on the appliance; no external KMS or
hosted service is used in v1. Keys are **versioned**: each key lives in its
own file `secret.key.v<N>` beside the existing key location under
`layout.LibDir`, mode 0600, and the store records the key version that
encrypted each row. The highest version is current.

### Re-key
Re-key is an audited owner operation:

1. Generate a new 32-byte key and write it as the next versioned key file
   (temp file, fsync, rename, fsync of the directory) **before** touching the
   database. The old key file is untouched.
2. In a single SQLite transaction, decrypt each row with its recorded key
   and re-encrypt with the new key, recording the new version per row.
3. Commit. Then re-read every row and decrypt-verify it with the new key.
4. Only after commit and full verification, delete the old key file
   (and fsync the directory).
5. On start, if every row is on the newest version and decrypts, delete
   any older key files. This completes an interrupted step 4.

Failure analysis: a crash or error before the commit leaves the database on
the old version, with a stray new key file that the next start ignores and
re-key reuses or removes. A crash after commit but before step 4 leaves all
rows on the new version with the old key file still present, which is
harmless and cleaned by the next start's verification. Because the new key
is durable before the commit, there is no window in which committed rows
reference a key that exists nowhere. (A design that writes the key file
after the commit, or replaces the single key file in place, does have such
a window; that is why the earlier draft's claim that failure "at any step"
is safe was not true of it.) The store opens each row with the key
version it records, so mixed-version states are readable at every point.

### Migration of the existing key
Today the key is a raw 32-byte file at `layout.LibDir/secret.key`, created
by `internal/secret/secret.go:86`, and rows carry no version. On first
start after S258 the store treats that file as key version 1: it copies
the file to `secret.key.v1` (temp, fsync, rename, directory fsync), stamps
all unversioned rows as version 1 in one transaction, and leaves the legacy
`secret.key` in place, unmodified and ignored afterward, so that rolling the
daemon binary back works until the first re-key (see Consequences). Rows without a version are always read as
version 1. The migration never changes ciphertext; the legacy file may be
removed by the owner once satisfied, and re-key never touches it.

### Secret-backed container variables
Secret-store values that reach containers (S266) are delivered as
**files**, never as environment entries: the daemon materializes each
value as a mode 0600 file under the application's own directory and
the generated Compose YAML references it through the Compose
top-level `secrets:` mechanism (`file:` sources). No secret value
appears in generated YAML, in `environment:`, in drift output, in
logs, or in audit. Compose stays usable without Homestead because the
files are plain and the YAML is valid on its own; an owner may supply
the files by hand.

Consequence for the at-rest model: the secret store remains the
encrypted source of truth; the materialized files are plaintext
on the appliance's own disk and are therefore covered by the same
owner-custody expectations as the key files (included in encrypted backups
per ADR-022, mode 0600, excluded from configuration export).

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
- Single key file replaced after commit: leaves a window where rows
  are unreadable if the process dies between commit and rename.
- Environment variables for container secrets: visible in `docker inspect`,
  process listings, and Compose YAML; rejected in favor of files.

## Consequences
- Every key file (all retained versions) must be in the owner's recovery
  material; the runbook must say so.
- Re-key requires an exclusive window; secret reads block briefly.
- Operation appears in the audit log without logging key material.
- Materialized secret files are plaintext on disk and must stay 0600
  under the application directory.
- Downgrade to a pre-S258 binary is only possible before the first
  re-key. The legacy `secret.key` is never kept in sync: the first
  re-key retires `secret.key.v1`, after which an older binary reading
  the legacy file cannot open version 2 rows. Rollback after a re-key
  means restoring the database and key files from backup.
- The daemon refuses to start, rather than guess, when `secret.key` and
  `secret.key.v1` differ before any re-key, or when stored secrets exist
  but no usable key file does; it never mints a key over ciphertext.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
