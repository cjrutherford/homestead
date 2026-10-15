# S253 — Media Craft Packages

## Goal
Photocraft, filmcraft and soundcraft as first-party catalog packages (vendored storytold releases in HAI-built, signed static containers, each on its own internal host), reusing the S249 shape, with the browser requirements of media apps surfaced as advisories.

## Dependencies
S252

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-028 (Accepted 2026-10-14). Media apps need browser capabilities (WebCodecs, WebGL or WebGPU, Web Audio and audio output) that the appliance cannot see, because it only serves the files.
Owner decision: for now the capability checks are client-side advisories only. Hard refusals at install time are deferred and recorded in `docs/DEFERRED.md`.

## Acceptance Criteria
- Media Craft Packages implemented and documented
- Per app (photocraft, filmcraft, soundcraft): the licence of that storytold release is read and judged compatible, and its licence, version and artifact SHA-256 recorded in `docs/THIRD_PARTY.md`; acceptance fails for an incompatible licence
- Per app: `catalog/<app>.yaml` following the S249 pattern with digest-pinned image, own host and port, ephemeral tmpfs paths, local-only with `allowwider: false`, and the three packages registered in the S249 curated-app and `craftPackages` lists with `knownImageVolumes` rows verified on KVM
- Manifests carry a `browser` requirements note (capabilities, for example WebCodecs, GPU, audio hardware) as catalog metadata; the install plan and the package page (API fields shared by CLI and dashboard, ADR-004) show them as non-blocking warnings with a plain-language reason per capability
- Advisories are client-side only: the dashboard and the app's own entry page feature-detect (WebCodecs, WebGL2/WebGPU, `AudioContext`) in the browser and warn on missing capability; nothing in the daemon refuses install, update or start on browser grounds, and the install plan states that the appliance cannot see browser capabilities
- Media-size caveat documented: large media still lives in the browser until S254; no server-side limit is introduced here
- Header script from S249 passes for each app, including any cross-origin isolation headers the codecs or wasm threads need
- Component tests for the dashboard advisory panel (S248 tokens, `StateMessage`); CLI plan output lists the same warnings; `docs/PARITY.md` updated for any new field or surface
- KVM acceptance on a fresh appliance: install, confirm the app's own `.lan` route answers with the app loading, remove it cleanly (container, stack directory, route, DNS name, edge entry), reinstall, run an update to a bumped digest, and confirm drift scan (S212, S262) reports clean after install and after update, for each of the three apps, plus a manual check on a machine lacking one capability that the advisory appears and the app still loads
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.
- `docs/THIRD_PARTY.md`: licence entries.
- `docs/DEFERRED.md`: hard install-time refusals based on browser capability (would need a client probe reported to the daemon, deferred by owner decision 2026-10-14).
- Operations docs: media craft browser requirements table.
- `docs/PARITY.md`: confirm no row changes are needed, or add/flip the rows (the parity test enforces it; `tests/test_s037_parity.py`).
- `docs/DEFERRED.md`: record discovered out-of-scope work.
- `docs/DEVELOPMENT.md`: a section for this slice with the KVM transcript summary, as S272 and S278 have.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
