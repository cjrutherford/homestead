# S266 — Secret-backed Container Variables

## Goal
Inject secret-store values into container environments at install and configure time.

## Dependencies
S157,S258

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

## Acceptance Criteria
- Secret-backed Container Variables implemented and documented
- `internal/catalog/compose.go` (near line 239) and `internal/catalog/configure.go` (near line 62) resolve secret references from the secret store
- Generated Compose YAML references env-file or secret mounts with restrictive permissions rather than embedding values in inspectable YAML
- Drift detection treats secret values as opaque
- Docker Compose stays usable without Homestead (documented how values are supplied)
- Tests assert no secret value appears in generated YAML, logs, or audit
- Remove the secret-backed variables entry from `docs/DEFERRED.md`
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
