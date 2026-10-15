# S249 — Wordcraft Package

## Goal
Wordcraft (word processor) as a first-party catalog package: the vendored storytold release in a HAI-built, signed static container on its own internal host. This slice also builds the shared static-package shape the other craft slices reuse.

## Dependencies
S158,S276,S277,S279,S212,S262

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-018, ADR-028 (Accepted 2026-10-14). Vendors storytold release artifacts; nothing is re-implemented and the package is not described as clean-room.
Blocked until the owner records the storytold release source in `docs/THIRD_PARTY.md` (currently: storytold release source: TBD by owner). Do not start the slice, and do not guess a URL, before that is recorded.
This slice builds the shared pieces once (manifest pattern, shared test, stateless backup handling, test relaxations); S250 to S253 reuse them and add YAML, an image and table rows only.

## Acceptance Criteria
- Wordcraft Package implemented and documented
- Prerequisite check: the storytold release source and the chosen release version are recorded in `docs/THIRD_PARTY.md`; the licence of that release is read and judged compatible with redistribution inside a Homestead-built image (acceptance fails if not); licence text, version and artifact SHA-256 are recorded there
- Manifest `catalog/wordcraft.yaml` follows the shape of `catalog/jellyfin.yaml`: one service on image `<registry>/hai-craft-wordcraft:<version>@sha256:<digest>` (from S279), own internal host, port, `exposes: web`, tiny `requirements` (memory, cpu, no storage class), no `backuppaths`, `sso: none`-equivalent, and a title, blurb and description that say what the app does without the word clean-room
- Writable paths of the nginx-class image (cache, pid, temp; exact list taken from `docker diff` of a running container on the KVM appliance) are declared as `ephemeral` data with reasons (`internal/catalog/data.go` `DataPath`), rendering as tmpfs; no read-only-rootfs schema change
- Exposure: `exposure.endpoints[0]` is `recommended: local` with `allowwider: false` and a warning that the app stores nothing on the appliance until Nextcloud wiring (S254); the owner can still override through the ADR-006 plan (`internal/exposure/overrides.go`)
- Curated catalog test relaxations: `internal/catalog/curated_test.go` stops hard-coding `len(names) != 4` and the four name checks; the expected set becomes a named list in one place that each craft slice extends, so adding an app edits one list; requirements/data tests no longer assume every package has a storage requirement or a mount
- Zero-mount packages are valid end to end: install planner (`internal/catalog/install.go`, `requirements.go`) does not demand a storage assignment for a package with no persistent data; Compose output has tmpfs and no `volumes:` bind; `internal/catalog/data_test.go` coverage test treats an image with no VOLUMEs and only ephemeral declarations as covered
- `knownImageVolumes` in `internal/catalog/data_test.go` gains a row for the craft image, verified with `docker image inspect` of exactly that tag@digest on the KVM appliance (state the date and the result in the comment, as the existing rows do); the table-membership test fails if the digest is bumped without re-verifying
- Shared table-driven test `internal/catalog/craft_test.go`: for every manifest in a `craftPackages` list it asserts digest-pinned image, unique host/port, only ephemeral data, stateless, local-only with `allowwider: false`, a `knownImageVolumes` row, and a licence entry in `docs/THIRD_PARTY.md`
- Stateless backup handling per ADR-028: add `Manifest.Stateless()` in `internal/catalog` (no non-ephemeral data declaration); `POST /api/v1/backups/schedules` (`internal/server/backup.go`) refuses a stateless package with 422 naming it; `backupSource` (`internal/server/scheduler.go`) builds no job for stateless packages; `backupJob` and `guardBackupSources` (`internal/server/updates.go`, `internal/server/backupguard.go`) return an explicit stateless result for zero persistent sources and never wrap `scheduler.ErrUnavailable` for it; the pre-update backup path (`updates.go` apply with `backup: true`) skips with a recorded note
- Backup status, scheduler status (`GET /api/v1/scheduler/jobs`), `homesteadctl` and the dashboard backup surface (S267) show `stateless` for these packages as a no-op; existing stateful behaviour is unchanged and tested
- Image trust: install and update of Wordcraft go through the S279 verifier; tests prove refusal on digest mismatch
- wasm check: against the running container on KVM, `curl -I` of the `.wasm` asset returns `Content-Type: application/wasm` and the documented cache headers; if the app uses SharedArrayBuffer, the required COOP/COEP headers are present and recorded; the check is a script under `scripts/` reused by S250 to S253 (`bash -n` in lint-scripts)
- Install, configure, remove, update flows reuse the generic catalog machinery with no craft-specific Go code beyond the shared items above
- KVM acceptance on a fresh appliance: install, confirm the app's own `.lan` route answers with the app loading, remove it cleanly (container, stack directory, route, DNS name, edge entry), reinstall, run an update to a bumped digest, and confirm drift scan (S212, S262) reports clean after install and after update; additionally confirm a schedule create for wordcraft is refused and `scheduler/jobs` shows no wordcraft job
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.
- `docs/THIRD_PARTY.md`: storytold source, Wordcraft release version, licence, artifact digest.
- `docs/architecture/`: a short static craft package guide (manifest pattern, image rebuild, stateless rule), reused by S250 to S253.
- `docs/PARITY.md`: add stateless indications to existing backup rows' notes; no new routes expected.
- `docs/DEFERRED.md`: read-only root filesystem schema field; craft apps' document storage until S254.
- `docs/DEVELOPMENT.md`: S249 section with KVM results.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
