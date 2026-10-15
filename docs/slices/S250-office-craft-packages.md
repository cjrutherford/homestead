# S250 — Office Craft Packages

## Goal
GridCraft (spreadsheet), PdfCraft (PDF workbench) and DeckCraft (presentations) as first-party catalog packages, reusing the S249 shared shape.

## Dependencies
S249

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-018, ADR-028 (revised for local build; owner decisions 2026-10-15) and ADR-030 (local HTTPS through the internal CA, S280). Images are built on the appliance by S279 from a signed craft manifest; no registry pulls. Vendors storytold release artifacts (`https://github.com/storytold`, Apache-2.0, release asset `<app>-web-<version>.zip`); nothing is re-implemented and no package is described as clean-room.
Reuses the S249 shared shape: no new Go code is expected; if a shared change is needed it is made in the shared place with a test and recorded here.
Versions pinned at planning (2026-10-15, re-check at manifest preparation): gridcraft v0.7.0, pdfcraft v0.8.0, deckcraft v0.7.0.

## Acceptance Criteria
- S250 — Office Craft Packages implemented and documented
- Apps in this slice: gridcraft v0.7.0, pdfcraft v0.8.0, deckcraft v0.7.0; each is a separate package with its own manifest, image, host and digest; none shares a container
- Per app: the licence of that storytold release is read and judged compatible, and its licence, version, artifact URL and SHA-256 are recorded in `docs/THIRD_PARTY.md` and in NOTICE capture per the S249 pattern; acceptance fails for any app whose licence is not compatible
- Per app: an unsigned craft manifest prepared with `scripts/craft-manifest.sh` (artifact SHA-256 cross-checked against upstream `SHA256SUMS.txt`, which is unsigned) for the owner to sign offline with the owner's minisign key per the S249 runbook; the agent never holds the production secret; KVM uses a throwaway key that never ships
- Per app: `catalog/<app>.yaml` following the S249 manifest pattern (local tag `homestead/craft-<app>:<version>`, own internal host and port 8080, non-root nginx with `/healthz` healthcheck, ephemeral tmpfs paths verified with `docker diff`, stateless, local-only with `allowwider: false`, no backup paths) with descriptions that name the function without the word clean-room
- Per app: a row in the S249 per-app header verification table (`docs/architecture/`), filled from the running container: wasm MIME, cache headers, precompression, and whether SharedArrayBuffer/COOP/COEP, WebGPU, clipboard or other browser capabilities are needed; nothing is assumed from another app
- Per app: extend the curated-app list and `craftPackages` list in `internal/catalog/craft_test.go`; the shared test passes without editing assertions
- Per app: a `knownImageVolumes` row verified with `docker image inspect` of the locally built tag and recorded digest on the KVM appliance (date and result in the comment)
- Per app: the S249 header script passes against the running container, over the S280 HTTPS host
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
