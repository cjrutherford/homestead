# Backup Encryption Format

## Status
Accepted

Accepted by the owner on 2026-10-11.

## Decision
Backup archives are encrypted with the age format (X25519 recipients) so
the owner can decrypt them with the standard `age` CLI without Homestead.

**New dependency approved:** `filippo.io/age`. It is not in `go.mod` today;
the first slice that needs it (S259) adds it, pinned, with its license
recorded in `docs/`. No other new dependency is approved by this record.

The appliance generates a backup identity and keeps it in the secret store
(ADR-021). Backups are encrypted to every configured recipient: the
appliance identity's recipient plus any owner-added recipients. The
manifest of non-secret metadata (package names, timestamps, sizes, format
version) may remain readable beside the archive. A runbook documents
restoring with plain `age` and standard tools.

### Owner-held recipient
An identity held only by the appliance does not protect against losing the
appliance: a backup encrypted solely to it is unreadable once the appliance
disk and its secret store are gone. The appliance identity therefore
does **not** satisfy the doctor's owner-recipient check. The owner must
either add a recipient whose private key they hold, or export the recovery
identity and store it off the appliance. Doctor reports a warning (not a
failure) while the only configured recipient is the appliance identity, and
clears it once an owner-added recipient exists or the owner records that the
recovery identity was exported.

### Verification
Acceptance must decrypt a produced backup with the real `age` CLI binary,
not the library: the library is the encryptor, so using it to verify
would not prove independence from Homestead.

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
- The `age` CLI is a test and runbook prerequisite, not a runtime one.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.

## Amendment S492: recovery key rotation
The owner can rotate the recovery identity (`homesteadctl backup identity
rotate`, Backups page). A backup file is an age header plus a payload: the
header wraps one random per-file key once per recipient, and the payload is
encrypted with that file key, never directly with the recovery identity.
Rotation therefore:
- generates a new appliance identity and stores it; new runs encrypt to it
  (and to owner recipients) only;
- keeps the old identity on the appliance as a retired identity
  (`sys_backup_identity_retired`) so restore decrypts with both;
- re-wraps every existing run under the backup destination by rewriting only
  the header, adding the new identity's stanza and keeping the stanzas already
  there (owner recipients and the old key keep working). The payload bytes are
  copied unchanged, the new file is fully decrypted with the new identity
  before it atomically replaces the old one, and nothing is deleted;
- shows the new key once and records an export that stays unacknowledged
  (the doctor warning returns) until the owner acknowledges storing it.
Rotation needs step-up authentication, an explicit confirm, is audited and
shares the export rate limit; like export it fails closed on audit failure.
What the owner must keep: the new key, and the old key until runs made before
the rotation are pruned. Rotation does not revoke the old key's read access to
older runs (the file key is unchanged); it is for replacing a lost or aged
key, not a way to lock out a leaked one. A run that cannot be re-wrapped is
reported and stays readable by the retained old identity.

## Amendment S505: rotation runs the re-wrap in the background
Rotation is mint-only and quick: it takes the export rate limit, step-up and
confirm, refuses (409) while the current key's export is unacknowledged, a
backup run is in flight or a re-wrap is running, writes the new identity and
an incomplete re-wrap marker, then starts a background `backup.rewrap`
operation (steps `clean`, `rewrap`) that removes stale `.rewrap-*` temp files
and re-wraps each run as before. The re-wrap can also be started alone
(`POST /api/v1/backups/identity/rewrap`, `homesteadctl backup identity rewrap`,
the Backups page) and is idempotent. A backup run holds a shared gate so a
rotation cannot mint a key mid-run; re-wraps are serialized. The last summary
(counts, up to 50 failed paths, never key material) is stored in
`backup_rewrap_state`, and Doctor `backup-rewrap` warns on unreadable retired
keys or an incomplete or failed re-wrap for the current key. The retired
identity list is parsed tolerantly: unparsable lines are skipped and counted,
and kept when a new key is appended.
