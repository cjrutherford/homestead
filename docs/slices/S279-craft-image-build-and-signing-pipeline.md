# S279 — Craft Image Build and Signing Pipeline

## Goal
Reproducible, HAI-built static craft images published by digest, with a signed image manifest the appliance verifies, per ADR-028 (extending ADR-025).

## Dependencies
S264,S240

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-028, Accepted 2026-10-14. Added because image build, publish and signing is substantial infrastructure that S249 and the per-app slices all consume; it lands before S249.
Inputs owned by the owner and not yet recorded: storytold release source (TBD by owner), the container registry to publish to (TBD by owner), and whether images are signed with the existing HAI release key or a separate image-signing key (TBD by owner).

## Acceptance Criteria
- Craft Image Build and Signing Pipeline implemented and documented
- A shared Dockerfile under a new `images/craft/` directory: `nginx`-class base pinned by digest, one build argument for the vendored artifact path and its SHA-256, the build fails when the artifact digest differs, serves the static tree, sets `application/wasm` for `.wasm`, long-lived immutable caching for hashed assets, `no-cache` for the HTML entry, and declares nothing writable beyond the paths S249 lists as ephemeral
- `scripts/build-craft-image.sh` (added to `make lint-scripts` with `bash -n`) builds one app's image from a downloaded artifact directory, tags it `name:version`, and prints the digest; rebuilding the same inputs twice produces the same layer digests (documented caveat if base-image metadata prevents bit-identical image IDs)
- Publish and sign step: `scripts/publish-craft-images.sh` pushes to the owner-recorded registry and writes the image manifest (reference, digest, vendored artifact digest per image, plus `version` and monotonic `counter`) in the ADR-025 checksums style, then signs it with minisign non-prehashed Ed25519; the private key never enters the repository
- Appliance-side verification in the daemon: a new `internal/release` image-manifest verifier reuses the ADR-025 code in `internal/release` (signature parse, trusted keys from configuration, counter baseline in SQLite, audited `--allow-older` override); failures of signature or digest have no override
- Install and update planning (`internal/catalog/install.go`, `internal/update`) call the verifier: refuse when the manifest is unverified, when the catalog digest differs from the signed digest, or when the counter is older; after pull and before start the daemon inspects the image (`internal/docker`) and refuses unless its `RepoDigests` contain the pinned digest
- Catalog schema (`internal/catalog/schema.go` `Service.Validate`) accepts `name:tag@sha256:<64 hex>` and a new manifest-level rule requires the digest for packages marked as craft class; tests cover valid, tag-only, malformed digest and `latest`
- CLI and dashboard parity for the verification verdict in install/update plans (same API fields); `docs/PARITY.md` updated for any added route (for example `GET /api/v1/images/manifest` status) and ADR-020 route classification updated
- Operations runbook section in `docs/architecture/RELEASE_VERIFICATION.md` for the image manifest: how an owner rebuilds an image, signs their own manifest, adds their key, and verifies by hand with `docker image inspect`
- Tests: verifier accepts a signed manifest; rejects tampered manifest, wrong key, prehashed signature, lower and equal-different counter, digest mismatch against catalog, missing manifest; override audited with both counters; image inspected with a stubbed engine that reports a different digest
- KVM: build the first craft image on the dev machine, publish to the registry, sign, copy the manifest to the appliance, and show that a correct install passes while a flipped digest byte and a manifest signed by an untrusted key each refuse before any container starts
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.
- `docs/THIRD_PARTY.md`: nginx base image licence and digest.
- `docs/architecture/RELEASE_VERIFICATION.md`: image manifest section.
- `docs/DEFERRED.md`: image acquisition by Homestead (no fetching, consistent with ADR-025), CI automation of the pipeline, read-only root filesystem hardening, multi-arch images if only amd64 ships first.
- `docs/PARITY.md`: confirm no row changes are needed, or add/flip the rows (the parity test enforces it; `tests/test_s037_parity.py`).
- `docs/DEVELOPMENT.md`: a section for this slice with the KVM transcript summary, as S272 and S278 have.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
