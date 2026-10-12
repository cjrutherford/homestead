# S266 — Secret-backed Container Variables

## Goal
Deliver secret-store values to containers as 0600 files via Compose `secrets:`, per ADR-021.

## Dependencies
S157,S258,S231,S277

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-021, Accepted 2026-10-11.
Re-scoped 2026-10-12 (owner chose S266 before S259): secret files never enter plaintext backups; S259 adds them to encrypted backups. Also in scope, because it is why S266 was pulled forward: scheduled and pre-update database backups read the database password from the secret store (KVM showed Authentik and Immich backups failing with "postgres backup needs a database password", and Immich crash-looping without database credentials).

## Acceptance Criteria
- Secret-backed Container Variables implemented and documented
- `WriteStack` in `internal/catalog/compose.go` (near line 237) and the configure plan in `internal/catalog/configure.go` (near line 59) resolve secret references from the secret store
- Resolved values are written as mode 0600 files under the application directory and referenced from the generated Compose YAML through the top-level `secrets:` mechanism with `file:` sources; values are never `environment:` entries
- Drift detection treats secret files as opaque (presence and mode only)
- Docker Compose stays usable without Homestead (documented how to supply the files by hand)
- Tests assert no secret value appears in generated YAML, environment, logs, audit, or drift output, and that files are 0600
- Secret files are excluded from configuration export and from plaintext backups (S259 adds them to encrypted backups; note this in the S259 slice)
- Catalog manifests declare which variables are secret-backed (for example Postgres passwords for Authentik and Immich, Immich's `DB_PASSWORD`); install generates a strong value into the secret store when none exists, and configure can rotate it through the normal plan and approve flow
- `backupJob` (`internal/server/updates.go`) resolves the database password from the secret store for scheduled and pre-update backups; a missing secret fails with a clear error naming the secret, never an empty password
- Existing installs (for example KVM VM 1 Authentik with a password set before S266) can register their current database password into the secret store through API, CLI, and dashboard, without regenerating the stack
- KVM: on VM 2 a scheduled Authentik and Immich backup includes a real database export; on VM 1 Immich gets credentials and starts, then migrates with S277
- Remove the secret-backed variables entry from `docs/DEFERRED.md` when landed
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
