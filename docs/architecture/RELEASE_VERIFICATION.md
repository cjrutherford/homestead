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
- No installed counter exists until update actuation (S265) records one.

## Trusted keys

Stored in SQLite (`platform_trust_keys`; removal of the embedded key is
tombstoned in `platform_trust_removed`) and rendered to
`platform-trust.yaml` beside `desired.yaml`. The embedded HAI key is a
placeholder until the production key ceremony (docs/DEFERRED.md). To
rotate or after a leaked HAI key:
`homesteadctl updates trust add|replace|remove`, or the dashboard.

## Surfaces

`GET /api/v1/platform/updates`, `POST /api/v1/platform/updates/verify`,
`GET|POST /api/v1/platform/trust-keys`,
`PUT|DELETE /api/v1/platform/trust-keys/{id}`; `homesteadctl updates
platform|verify|trust`; the dashboard Updates page. See docs/PARITY.md.
