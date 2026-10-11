# S266 — Secret-backed Container Variables

## Goal
Deliver secret-store values to containers as 0600 files via Compose `secrets:`, per ADR-021.

## Dependencies
S157,S258,S231,S259

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-021, Accepted 2026-10-11.

## Acceptance Criteria
- Secret-backed Container Variables implemented and documented
- `WriteStack` in `internal/catalog/compose.go` (near line 237) and the configure plan in `internal/catalog/configure.go` (near line 59) resolve secret references from the secret store
- Resolved values are written as mode 0600 files under the application directory and referenced from the generated Compose YAML through the top-level `secrets:` mechanism with `file:` sources; values are never `environment:` entries
- Drift detection treats secret files as opaque (presence and mode only)
- Docker Compose stays usable without Homestead (documented how to supply the files by hand)
- Tests assert no secret value appears in generated YAML, environment, logs, audit, or drift output, and that files are 0600
- Secret files are excluded from configuration export and included in encrypted backups
- Remove the secret-backed variables entry from `docs/DEFERRED.md` when landed
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
