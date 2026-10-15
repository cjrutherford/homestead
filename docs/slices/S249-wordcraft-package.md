# S249 — Wordcraft Package

## Goal
Wordcraft (word processor) as a first-party catalog package: the vendored storytold release in a locally built static container on its own internal host. This slice also builds the shared static-package shape the other craft slices reuse.

## Dependencies
S158,S276,S277,S279,S212,S262,S280

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-018, ADR-028 (revised for local build; owner decisions 2026-10-15) and ADR-030 (local HTTPS through the internal CA, S280). Images are built on the appliance by S279 from a signed craft manifest; no registry pulls for craft images. Vendors storytold release artifacts; nothing is re-implemented and the package is not described as clean-room.
Blocked until the owner records the storytold release source in `docs/THIRD_PARTY.md` (recorded 2026-10-15 as `https://github.com/storytold`; see ADR-028; the THIRD_PARTY.md entry is still the slice gate). Do not start the slice, and do not guess a URL, before that is recorded.
This slice builds the shared static-craft shape once (manifest pattern, shared test, stateless backup handling, test relaxations, header verification table, signing runbook) and proves it end to end with WordCraft (storytold/wordcraft v0.7.0 at planning, asset `wordcraft-web-0.7.0.zip`, re-check at manifest preparation); S250 to S253 are batches that reuse it and add YAML, manifests and table rows only.
Signing: the owner generates the minisign craft key offline and holds the secret; this slice writes the runbook and the agent prepares the unsigned manifest for the owner to sign. KVM uses a throwaway key that never ships. Upstream `SHA256SUMS.txt` is unsigned; our signed manifest records the zip SHA-256, cross-checked against it at preparation time.
PhotoCraft's `packaging/web/README.md` is the reference for the static shape (no server code, non-root `nginx` on 8080, `/healthz`, precompressed gzip assets, `application/wasm`, `no-cache` for index.html/app/<name>-web.js/app/parts.txt, `public, max-age=31536000, immutable` for hashed .wasm/.js/.part-* files, no COOP/COEP needed). Other apps may differ, so this slice builds the per-app table and verifies WordCraft itself rather than assuming. The community Docker recipe builds from Rust source and is not used.
Inherited unverified items from S279 KVM (`docs/DEVELOPMENT.md`): the `composeUpServices` database-only start path may skip the craft gate, and the edge route to the synthetic app returned nothing. Both are acceptance criteria below.

## Acceptance Criteria
- Wordcraft Package implemented and documented
- Edge route fix/verify: reproduce the S279 finding that the edge route to the app returned nothing, find the cause, fix it in the shared place with a test; the app answers through its own `.lan` route over HTTPS with a leaf certificate from the S280 internal CA, verified with curl using the S280 root (headers recorded)
- Compose-up gate: audit `composeUpServices` (the DB-only start path used by migrations) for a way to start a craft service without the craft gate; close it with a test that a craft service cannot start with a missing or mismatched recorded image on any start path, and prove it live on KVM
- Per-app header verification table in `docs/architecture/` (columns: app, version, wasm MIME, cache headers per asset class, precompression, COOP/COEP/SharedArrayBuffer need, WebGPU/WebGL2/clipboard needs, healthcheck, nginx user/port); WordCraft is the first row and S250 to S253 add rows
- Shared image/nginx rules enforced in the build context: non-root `nginx` on 8080, `/healthz`, zero data and no volumes, stateless; a runtime healthcheck in the manifest
- Signing runbook in `docs/architecture/`: owner-offline minisign key generation, `scripts/craft-manifest.sh` use, upstream SHA256SUMS cross-check, owner signing, `craft trust add`, key loss and rotation, throwaway-key rules; the throwaway KVM key is never committed or shipped
- Prerequisite check: the storytold release source (`https://github.com/storytold`) and the chosen release version are recorded in `docs/THIRD_PARTY.md`; the licence of that release is read and judged compatible with redistribution inside a Homestead-built image (acceptance fails if not); licence text, version and artifact SHA-256 are recorded there
- Manifest `catalog/wordcraft.yaml` follows the shape of `catalog/jellyfin.yaml`: one service on the local image `homestead/craft-wordcraft:<version>` (built by S279; no registry reference, no catalog digest), own internal host, port, `exposes: web`, tiny `requirements` (memory, cpu, no storage class), no `backuppaths`, `sso: none`-equivalent, and a title, blurb and description that say what the app does without the word clean-room
- Catalog validation (`internal/catalog/schema.go`) accepts the local craft tag form and, for craft-class packages, requires that the tag appears in the signed craft manifest; a registry-qualified image reference is refused for craft packages; tests cover valid, registry-qualified and `latest`
- Writable paths of the nginx-class image (cache, pid, temp; exact list taken from `docker diff` of a running container on the KVM appliance) are declared as `ephemeral` data with reasons (`internal/catalog/data.go` `DataPath`), rendering as tmpfs; no read-only-rootfs schema change
- Exposure: `exposure.endpoints[0]` is `recommended: local` with `allowwider: false` and a warning that the app stores nothing on the appliance until Nextcloud wiring (S254); the owner can still override through the ADR-006 plan (`internal/exposure/overrides.go`)
- Curated catalog test relaxations: `internal/catalog/curated_test.go` stops hard-coding `len(names) != 4` and the four name checks; the expected set becomes a named list in one place that each craft slice extends, so adding an app edits one list; requirements/data tests no longer assume every package has a storage requirement or a mount
- Zero-mount packages are valid end to end: install planner (`internal/catalog/install.go`, `requirements.go`) does not demand a storage assignment for a package with no persistent data; Compose output has tmpfs and no `volumes:` bind; `internal/catalog/data_test.go` coverage test treats an image with no VOLUMEs and only ephemeral declarations as covered
- `knownImageVolumes` in `internal/catalog/data_test.go` gains a row for the craft image, verified with `docker image inspect` of exactly that locally built tag and recorded digest on the KVM appliance (state the date and the result in the comment, as the existing rows do); the table-membership test fails if the manifest version is bumped without re-verifying
- Shared table-driven test `internal/catalog/craft_test.go`: for every manifest in a `craftPackages` list it asserts local craft tag listed in the craft manifest, unique host/port, only ephemeral data, stateless, local-only with `allowwider: false`, a `knownImageVolumes` row, and a licence entry in `docs/THIRD_PARTY.md`
- Stateless backup handling per ADR-028: add `Manifest.Stateless()` in `internal/catalog` (no non-ephemeral data declaration); `POST /api/v1/backups/schedules` (`internal/server/backup.go`) refuses a stateless package with 422 naming it; `backupSource` (`internal/server/scheduler.go`) builds no job for stateless packages; `backupJob` and `guardBackupSources` (`internal/server/updates.go`, `internal/server/backupguard.go`) return an explicit stateless result for zero persistent sources and never wrap `scheduler.ErrUnavailable` for it; the pre-update backup path (`updates.go` apply with `backup: true`) skips with a recorded note
- Backup status, scheduler status (`GET /api/v1/scheduler/jobs`), `homesteadctl` and the dashboard backup surface (S267) show `stateless` for these packages as a no-op; existing stateful behaviour is unchanged and tested
- Image trust: install and update of Wordcraft go through the S279 verify-then-build operation; tests prove refusal on artifact digest mismatch and on a recorded-digest mismatch
- wasm check: against the running container on KVM, `curl -I` of the `.wasm` asset returns `Content-Type: application/wasm` and the documented cache headers; if the app uses SharedArrayBuffer, the required COOP/COEP headers are present and recorded; the check is a script under `scripts/` reused by S250 to S253 (`bash -n` in lint-scripts)
- Install, configure, remove, update flows reuse the generic catalog machinery with no craft-specific Go code beyond the shared items above
- KVM acceptance on a fresh appliance: install, confirm the app's own `.lan` HTTPS route answers with the app loading (curl with the S280 root, headers recorded), remove it cleanly (container, stack directory, route, DNS name, edge entry), reinstall, run an update to a bumped manifest version (rebuild), and confirm drift scan (S212, S262) reports clean after install and after update; additionally confirm a schedule create for wordcraft is refused and `scheduler/jobs` shows no wordcraft job
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.
- `docs/THIRD_PARTY.md`: storytold source, Wordcraft release version, licence (Apache-2.0 expected, read it), NOTICE text capture, artifact URL and digest.
- `docs/architecture/`: a short static craft package guide (manifest pattern, local image build, stateless rule), reused by S250 to S253.
- `docs/PARITY.md`: add stateless indications to existing backup rows' notes; no new routes expected.
- `docs/DEFERRED.md`: read-only root filesystem schema field; craft apps' document storage until S254.
- `docs/DEVELOPMENT.md`: S249 section with KVM results.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
