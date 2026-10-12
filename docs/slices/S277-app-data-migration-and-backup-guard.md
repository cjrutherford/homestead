# S277 — App Data Migration and Backup Source Guard

## Goal
Move existing installs' data out of anonymous Docker volumes onto the S276 placement through a planned, previewed, approved operation, and make backups fail when their sources do not match what the app actually stores.

## Dependencies
S276,S181,S186

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Existing appliances (for example KVM VM 1) installed before S276 keep data in anonymous volumes. Moving it is a destructive-risk change, so it follows the plan, preview, and approve flow (ADR-006 pattern) and is never automatic.

## Acceptance Criteria
- App Data Migration and Backup Source Guard implemented and documented
- Detection: per installed app, compare the running containers' mounts with the S276 declarations; report `placed`, `needs-migration` (anonymous or named volume holds a declared path), or `unknown`; shown in status, doctor, CLI, and dashboard
- Migration plan per app: target host paths, sizes, free space on the target mount, expected downtime; refuses when space is insufficient
- Approved migration runs as an operation: take a pre-migration backup (or require the owner to acknowledge none is possible), stop the stack, copy volume contents preserving ownership, modes, and xattrs, regenerate the stack with bind mounts, start, health-check, and only then mark the old volume for removal (removal is a separate explicit owner action, never automatic)
- Failure at any step leaves the app runnable on its original volume, and the operation reports exactly where it stopped
- Backup guard: `backupJob` (`internal/server/updates.go`) fails with a clear error when an app's declared sources are missing or empty, or when the app's running mounts are not covered by the sources; it no longer passes vacuously on a stray directory
- Postgres-backed apps: migration copies the database directory only with the database stopped, and the result is verified by starting the database
- API, CLI, and dashboard parity for detection, plan, approve, and old-volume removal; `docs/PARITY.md` updated; routes classified for ADR-020
- KVM: VM 1 (pre-S276 install with nextcloud, immich, authentik, jellyfin) migrates each app with data intact (spot-check app-level data, for example a Nextcloud file and a Jellyfin library entry), and a scheduled backup then captures real data
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs, including an owner runbook for migration and rollback.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
