# S264 — Release Artifact Verification

## Goal
SHA-256 and Ed25519 signature verification of platform artifacts, per ADR-025.

## Dependencies
S194,S243

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
ADR-025 is Proposed and must be Accepted before implementation begins.

## Acceptance Criteria
- Release Artifact Verification implemented and documented
- `internal/update/platform.go` verifies artifact SHA-256 against a checksums file and the file's minisign-compatible Ed25519 signature
- HAI public key embedded in the binary; owner can add or replace trusted keys over API and CLI
- Verification failure blocks the update with no override
- `PlanPlatform` output reports verification state; `homesteadctl updates` shows it
- No automatic fetching added beyond S194/S243 behavior
- Tests cover tampered artifact, tampered checksums, unknown key, and owner-added key
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
