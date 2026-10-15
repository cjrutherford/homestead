# S279 — Craft build pipeline: signed craft manifests and local image build

## Goal
The appliance verifies a signed craft release manifest and the upstream artifact it names, then builds the craft image locally and records its digest, per ADR-028 as revised 2026-10-14 (local build only, separate craft signing key, no registry).

## Dependencies
S264,S158,S212

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-028 (Revised 2026-10-14) and ADR-025. S240 is not a dependency: nothing is published to a registry. S264 supplies the verification code and trust-key configuration this slice extends; S158 the Docker/Compose engine layer; S212 drift detection. Lands before S249; S249 to S253 consume it.
Owner-supplied input not yet recorded: storytold release source (TBD by owner), needed before any real manifest can be written. Slice tests use test keys and a fake artifact only.

## Acceptance Criteria
- Craft build pipeline implemented and documented
- Craft release manifest format, documented in `docs/architecture/RELEASE_VERIFICATION.md`: per app, upstream artifact URL(s) with SHA-256, `version`, monotonic `counter` (ADR-025 semantics), and the digest of the Dockerfile and build context; signed with minisign non-prehashed Ed25519
- Separate trust key class: a HAI craft signing public key embedded in the binary, trusted alongside the platform release key; a craft-signed manifest never verifies as a platform release and vice versa (tested); the owner adds or replaces craft trust keys through configuration over API and CLI with the same plan/audit treatment as S264 trust keys
- HAI-side signing tooling: `scripts/sign-craft-manifest.sh` (or documented minisign invocation) and `scripts/craft-manifest.sh` that generates the manifest from an artifact directory and the `images/craft/` context; added to `make lint-scripts` with `bash -n`; the private key never enters the repository; owner-key signing documented identically
- `images/craft/` shared Dockerfile: `nginx`-class base pinned by digest, artifact path and digest as build arguments with a failing check on mismatch, serves the static tree, `application/wasm` for `.wasm`, immutable caching for hashed assets, `no-cache` for the HTML entry, nothing writable beyond the paths S249 lists as ephemeral
- Appliance-side verify-then-build operation in the daemon (new package under `internal/`, using `internal/release` and `internal/docker`): (1) verify signature against trusted craft keys and the counter baseline in SQLite (audited `--allow-older` override); (2) obtain the artifact from the signed URL, or accept an owner-supplied local file (offline/air-gapped), and verify its SHA-256; (3) verify the build-context digest taken from the catalog directory; (4) only then `docker build` with the pinned base digest, context from the catalog dir
- Built image tagged `homestead/craft-<app>:<version>` and recorded (local image ID/digest, manifest version and counter, artifact digest) in SQLite; install and start refuse unless the local image matches the recorded digest; drift and installed checks (S212) compare against the recorded local digest
- Rebuild on update: an update plan verifies the new manifest and rebuilds; the previous image and record are kept until the new build verifies and the update applies, then pruned; failed builds change nothing running
- Failure handling: signature, artifact digest and build-context digest failures have no override and name the failing check; network, Docker build and cancelled-build failures leave no partial tag and no recorded digest, are audited, and are retryable; build output is captured with a size cap and contains no secrets
- Disk-space check before fetch and build (artifact plus layer headroom, documented threshold) that refuses with a clear message instead of failing mid-build
- Verdict and build status exposed through API fields shared by CLI and dashboard (plan and install/update flows), with `docs/PARITY.md` and ADR-020 route classification updated for any added route (for example craft manifest status and trust-key routes)
- Tests with a fake docker builder and test keys: accepts a valid signed manifest; rejects tampered manifest, wrong key, platform-key-signed manifest, prehashed signature, lower and equal-different counter, artifact digest mismatch, build-context digest mismatch, missing manifest; locally supplied artifact verified identically; override audited with both counters; recorded-digest mismatch refuses start; disk-space refusal; failed build leaves no record
- KVM: with a test-key manifest and a small test artifact, build on the appliance, install passes; then flipped artifact byte, tampered context file, and an untrusted-key manifest each refuse before any build; an owner-supplied artifact works with the network blocked; drift is reported after the image is replaced behind the daemon's back
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.
- `docs/THIRD_PARTY.md`: nginx base image licence and digest.
- `docs/architecture/RELEASE_VERIFICATION.md`: craft manifest section, including owner rebuild-and-sign runbook and manual verification with `docker image inspect`.
- `docs/DEFERRED.md`: CI automation of manifest generation, read-only root filesystem hardening, multi-arch builds if only amd64 first, build caching across updates.
- `docs/PARITY.md`: add or flip rows (the parity test enforces it; `tests/test_s037_parity.py`).
- `docs/DEVELOPMENT.md`: a section for this slice with the KVM transcript summary, as S272 and S278 have.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
