# S264 — Release Artifact Verification

## Goal
SHA-256 and minisign Ed25519 verification of platform artifacts with rollback counter and owner-managed trust keys, per ADR-025.

## Dependencies
S194,S243,S257

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-025, Accepted 2026-10-11.
Scope of the `PlanPlatform` change (`internal/update/platform.go:131`): add verification state to its inputs and output only. It stays a pure planner; applying updates is S265. Fetching releases remains deferred (`docs/DEFERRED.md`, "Platform release acquisition"); the owner supplies artifact, checksums, and signature files locally.

## Acceptance Criteria
- Release Artifact Verification implemented and documented
- Verifier checks artifact SHA-256 against a signed checksums file whose minisign non-prehashed Ed25519 signature verifies against a trusted key; prehashed signatures are rejected
- Checksums file carries `version` and a monotonic `counter`; artifacts older than the installed counter are refused unless the owner explicitly overrides, which is audited with both versions; equal counter with different version is refused without override
- HAI public key embedded in the binary; the owner adds, replaces, and removes trusted keys through configuration over API and `homesteadctl` (stored in SQLite, rendered to YAML), no binary release needed
- Signature or checksum failure blocks the update with no override flag
- `PlanPlatform` reports verification state per release (verified, unverified, failed) without changing its planning rules; new read route for the platform plan (for example `GET /api/v1/platform/updates`) and trust-key routes classified for ADR-020
- `homesteadctl updates` shows verification state and manages trusted keys; the existing dashboard updates surface (`dashboard/src/app/updates`) shows verification state and trust keys; `docs/PARITY.md` updated for every added or changed route (`tests/test_s037_parity.py` stays green)
- No fetching is added
- Tests cover tampered artifact, tampered checksums, unknown key, owner-added key, prehashed signature rejection, older counter refused, and audited override
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
