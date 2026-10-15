# S250 — Gridcraft Package

## Goal
Gridcraft (spreadsheet) as a first-party catalog package: the vendored storytold release in a HAI-built, signed static container on its own internal host, reusing the S249 shape.

## Dependencies
S249

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-028 (Accepted 2026-10-14). Reuses the S249 shared shape: no new Go code is expected; if a shared change is needed it is made in the shared place with a test and recorded here.

## Acceptance Criteria
- Gridcraft Package implemented and documented
- Per app (gridcraft): the licence of that storytold release is read and judged compatible, and its licence, version and artifact SHA-256 are recorded in `docs/THIRD_PARTY.md`; the slice fails acceptance for any app whose licence is not compatible
- Per app: `catalog/<app>.yaml` following the S249 manifest pattern (digest-pinned image from the S279 pipeline, own internal host and port, ephemeral tmpfs paths verified with `docker diff`, local-only with `allowwider: false`, no backup paths) and descriptions that name the function without the word clean-room
- Per app: extend the curated-app list and the `craftPackages` list in `internal/catalog/craft_test.go` (S249); the shared test must pass with the new entries without editing assertions
- Per app: a `knownImageVolumes` row in `internal/catalog/data_test.go` verified with `docker image inspect` of that tag@digest on the KVM appliance (date and result in the comment)
- Per app: the S249 wasm MIME and cache header script passes against the running container
- KVM acceptance on a fresh appliance: install, confirm the app's own `.lan` route answers with the app loading, remove it cleanly (container, stack directory, route, DNS name, edge entry), reinstall, run an update to a bumped digest, and confirm drift scan (S212, S262) reports clean after install and after update, for each app in this slice, with stateless confirmations (schedule create refused, no scheduler job)
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.
- `docs/THIRD_PARTY.md`: licence entries for each app.
- `docs/PARITY.md`: confirm no row changes are needed, or add/flip the rows (the parity test enforces it; `tests/test_s037_parity.py`).
- `docs/DEFERRED.md`: record discovered out-of-scope work.
- `docs/DEVELOPMENT.md`: a section for this slice with the KVM transcript summary, as S272 and S278 have.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
