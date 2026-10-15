# S252 — Media Craft Packages

## Goal
FilmCraft, EffectCraft and SoundCraft as first-party catalog packages, with the browser requirements of media apps surfaced as advisories.

## Dependencies
S251

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-018, ADR-028 (revised for local build; owner decisions 2026-10-15) and ADR-030 (local HTTPS through the internal CA, S280). Images are built on the appliance by S279 from a signed craft manifest; no registry pulls. Vendors storytold release artifacts (`https://github.com/storytold`, Apache-2.0, release asset `<app>-web-<version>.zip`); nothing is re-implemented and no package is described as clean-room.
Reuses the S249 shared shape: no new Go code is expected; if a shared change is needed it is made in the shared place with a test and recorded here.
Versions pinned at planning (2026-10-15, re-check at manifest preparation): filmcraft v0.8.0, effectcraft v0.10.0, soundcraft v0.7.0. Media apps need browser capabilities (WebCodecs, WebGL or WebGPU, Web Audio and audio output) that the appliance cannot see. Owner decision 2026-10-14: capability checks are client-side advisories only; hard install-time refusals are deferred in `docs/DEFERRED.md`.

## Acceptance Criteria
- S252 — Media Craft Packages implemented and documented
- Apps in this slice: filmcraft v0.8.0, effectcraft v0.10.0, soundcraft v0.7.0; each is a separate package with its own manifest, image, host and digest; none shares a container
- Per app: the licence of that storytold release is read and judged compatible, and its licence, version, artifact URL and SHA-256 are recorded in `docs/THIRD_PARTY.md` and in NOTICE capture per the S249 pattern; acceptance fails for any app whose licence is not compatible
- Per app: an unsigned craft manifest prepared with `scripts/craft-manifest.sh` (artifact SHA-256 cross-checked against upstream `SHA256SUMS.txt`, which is unsigned) for the owner to sign offline with the owner's minisign key per the S249 runbook; the agent never holds the production secret; KVM uses a throwaway key that never ships
- Per app: `catalog/<app>.yaml` following the S249 manifest pattern (local tag `homestead/craft-<app>:<version>`, own internal host and port 8080, non-root nginx with `/healthz` healthcheck, ephemeral tmpfs paths verified with `docker diff`, stateless, local-only with `allowwider: false`, no backup paths) with descriptions that name the function without the word clean-room
- Per app: a row in the S249 per-app header verification table (`docs/architecture/`), filled from the running container: wasm MIME, cache headers, precompression, and whether SharedArrayBuffer/COOP/COEP, WebGPU, clipboard or other browser capabilities are needed; nothing is assumed from another app
- Per app: extend the curated-app list and `craftPackages` list in `internal/catalog/craft_test.go`; the shared test passes without editing assertions
- Per app: a `knownImageVolumes` row verified with `docker image inspect` of the locally built tag and recorded digest on the KVM appliance (date and result in the comment)
- Per app: the S249 header script passes against the running container, over the S280 HTTPS host
- Verify per app whether SharedArrayBuffer, wasm threads or WebCodecs need cross-origin isolation; if so serve `Cross-Origin-Opener-Policy`/`Cross-Origin-Embedder-Policy` from the image's nginx config through the shared build context (covered by the signed build-context digest, per-app switch, not global), record it in the header table, and test that isolation does not break any S254 same-origin proxy design
- Manifests carry a `browser` requirements note (capabilities, for example WebCodecs, GPU, audio hardware) as catalog metadata; the install plan and package page (API fields shared by CLI and dashboard, ADR-004) show them as non-blocking warnings with a plain-language reason per capability; nothing in the daemon refuses install, update or start on browser grounds, and the plan states the appliance cannot see browser capabilities
- Component tests for the dashboard advisory panel (S248 tokens, `StateMessage`); CLI plan output lists the same warnings; `docs/PARITY.md` updated for any new field or surface
- Media-size caveat documented: large media lives in the browser until S254; no server-side limit is introduced here
- KVM acceptance on a fresh appliance, for each app in this slice: install, confirm the app's `.lan` HTTPS route answers (curl with the S280 root CA, headers recorded; app entry page and wasm asset fetched), remove cleanly (container, stack directory, route, DNS name, edge entry, leaf certificate), reinstall, update to a bumped manifest version (rebuild), confirm drift scan (S212, S262) clean after install and update, schedule create refused and no scheduler job (stateless)
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.
- `docs/THIRD_PARTY.md`: licence entries for each app.
- `docs/architecture/`: extend the per-app header verification table.
- `docs/PARITY.md`: confirm no row changes are needed, or add/flip the rows (the parity test enforces it; `tests/test_s037_parity.py`).
- `docs/DEVELOPMENT.md`: a section for this slice with the KVM transcript summary, as S272 and S278 have.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state. One slice, one commit on main.
