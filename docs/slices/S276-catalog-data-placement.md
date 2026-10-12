# S276 — Catalog Data Placement on Mounted Storage

## Goal
Every catalog app keeps its persistent data in bind mounts under assigned mounted storage (`<mount>/<package>/<subpath>`), declared by the catalog entry, instead of anonymous Docker volumes.

## Dependencies
S150,S153,S155,S156,S161,S180,S257

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Found in KVM validation (2026-10-12): the generated stacks under `/opt/homestead/stacks/<package>/compose.yaml` for nextcloud, immich, authentik, and jellyfin have no `volumes:` key, so container data lives in anonymous Docker volumes. `backupSources` (`internal/server/updates.go`) correctly expects `<mount>/<package>`, so scheduled backups find nothing (or pass vacuously on a stray directory). Only `internal/jellyfin/storage.go` derives volumes from storage assignments, and nothing applies it to the generated stack. `internal/server/catalog.go` takes `Volumes` from the request body, so nothing requires them. This slice implements the S180 data-path declaration that never landed.
New installs only: migrating existing installs off anonymous volumes is S277.

## Acceptance Criteria
- Catalog Data Placement on Mounted Storage implemented and documented
- Catalog schema (S150) gains per-service data declarations: container target path, storage class (application, bulk), subpath, and whether the path holds a database; `catalog/*.yaml` for every first-party app declares every persistent path its images use (verified against the image documentation and `docker inspect` of a running container)
- The install planner (S155) resolves each declaration to `<class mount>/<package>/<subpath>` from storage assignments; a missing assignment is a requirements failure, not a silent default
- The Compose generator (`internal/catalog/compose.go`, S153) renders those as bind mounts; the request-body `Volumes` path no longer bypasses declarations (declarations win; extra owner-supplied mounts remain allowed and are shown in the plan)
- Host directories are provisioned with the ownership and modes each image needs, without formatting or partitioning (ADR-013)
- Generated stacks contain no anonymous volumes for declared paths; a generator test fails if a service image declares a `VOLUME` not covered by a declaration or an explicit `ephemeral` marker
- Jellyfin's bespoke storage derivation (`internal/jellyfin/storage.go`) is replaced by the generic path, with behaviour preserved
- Plans shown in CLI and dashboard list the resolved host paths (parity, ADR-004)
- Docker Compose stays usable without Homestead: the generated file is self-contained with absolute host paths
- KVM: a fresh install of the base suite on VM 2 places all data under `/mnt/app` or `/mnt/bulk`, `docker inspect` shows only bind mounts for declared paths, and a scheduled backup of each app copies real data
- Add a `docs/DEFERRED.md` entry if any first-party image needs paths that cannot live on mounted storage, with the reason
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
