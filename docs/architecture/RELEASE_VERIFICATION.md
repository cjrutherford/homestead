# Release Artifact Verification (S264, ADR-025)

Operations and design notes for verifying platform artifacts. Nothing is
fetched: the owner places the artifact, checksums file, and signature on
the appliance.

## Files

- Checksums file (signed), one `key=value` header pair then `sha256sum`
  lines:

  ```
  version=0.2.0
  counter=7
  <sha256-hex>  homestead-server
  <sha256-hex>  homesteadctl
  ```

  `version` and `counter` (positive integer, incremented every release)
  are mandatory. The artifact is matched by its file name.
- Signature: minisign `.minisig`, non-prehashed `Ed` only. Prehashed `ED`
  signatures are rejected. Both the file signature and minisign's global
  signature over signature plus trusted comment must verify. Create with
  `minisign -S -m SHA256SUMS`.
- Public key: minisign `.pub` file or its bare base64 line.

## Rules

- Authenticity failures (unknown key, bad signature, checksum mismatch,
  malformed file) block the release with no override.
- A counter below the installed one is refused unless the owner
  overrides (`--allow-older`, or the confirmation checkbox shown after a
  rollback refusal). The override is audited with both versions and
  counters. An equal counter with a different version is always refused.
- The installed baseline is seeded by verification: when a release whose
  version equals the running version (`version.Version`) verifies and no
  installed record exists, it is recorded as installed (version and
  counter), never overwriting. From then on an older counter is refused.
  Before that first match, no rollback check applies. S265 owns later
  changes (`RecordInstalled`).
- Failed, rollback-refused, and unverified releases are never offered to
  the planner: the plan route lists them under `refused` with the
  reason, and only `verified` releases can appear in `plan`.
- Input files must each be a regular file: symlinks (final component),
  FIFOs, devices, and directories are refused, checked on the opened
  descriptor. Limits: artifact 2 GiB, checksums and signature 1 MiB,
  checksums lines 16 KiB. The route answers "must each be a readable
  regular file" without saying which path failed or whether it exists.
  Input refusals are 400s, not recorded as verdicts.
- Checksums file names must be bare names (no `/`, `\`, `..`); a second
  `version=` or `counter=` line is rejected even when empty.
- Time of check to time of use: verification records the artifact's
  SHA-256 in the release record. Apply (S265) must call
  `release.VerifyDigestUnchanged(path, digest)` immediately before
  replacing the binary and refuse on any error, because the file the
  owner supplied can be swapped after verification.

## Trusted keys

Stored in SQLite (`platform_trust_keys`; removal of the embedded key is
tombstoned in `platform_trust_removed`) and rendered to
`platform-trust.yaml` beside `desired.yaml`. The embedded HAI key is a
placeholder until the production key ceremony (docs/DEFERRED.md); the
API, CLI, dashboard, and YAML mark it `placeholder`, and doctor warns
(`release-trust`) while it is the only trusted key (or none is trusted).
Key changes render the YAML before the audit entry: a render failure is
audited as an error. To
rotate or after a leaked HAI key:
`homesteadctl updates trust add|replace|remove`, or the dashboard.

## Surfaces

`GET /api/v1/platform/updates`, `POST /api/v1/platform/updates/verify`,
`GET|POST /api/v1/platform/trust-keys`,
`PUT|DELETE /api/v1/platform/trust-keys/{id}`; `homesteadctl updates
platform|verify|trust`; the dashboard Updates page. See docs/PARITY.md.
