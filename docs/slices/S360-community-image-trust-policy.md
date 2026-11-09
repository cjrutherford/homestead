# S360: Community-image trust policy (F4)

## Goal
Write the policy that decides which container-image publishers the catalog
trusts, and enforce the checkable part in the manifest and a test, before the
LinuxServer.io packages of Wave 3 (S361-S366) are added. Decision record:
ADR-056.

## Dependencies
S279 (catalog), S343 (GHCR and Docker Hub update discovery), ADR-028, ADR-016,
ADR-025. Owner approval of ADR-056: 2026-11-01.

## Scope
- ADR-056 and its README index row.
- `Service.Publisher` (`publisher:`) and `Service.Justification`
  (`justification:`) optional manifest fields; `Service.Validate` refuses an
  unknown publisher, a community image without a justification (40 characters
  or more), and a justification on any other publisher.
- Every pulled service in `catalog/*.yaml` classified; craft images unchanged.
  `calibre-web-automated` is `community` with its recorded justification.
- `TestCuratedImageTrust` (Go) and `tests/test_s360_image_trust.py`.
- Fixes the SLICE_PLAN reference from ADR-035 (the dashboard ADR) to ADR-056.
- Not in scope: signature or attestation verification, digest pinning,
  registry automation, owner-facing display of the publisher (see
  `docs/DEFERRED.md`). No LinuxServer package is added here.

## Acceptance
- ADR-056 is Accepted and states publisher classes, requirements
  (multi-arch, pinned tag, digest recorded, public source, maintenance,
  update source), LinuxServer specifics, provenance and what a slice records.
- Every non-craft service declares a publisher; the catalog test fails on a
  missing one, on `linuxserver/` under another class, on a `linuxserver`
  image outside Prowlarr, Sonarr, Radarr, SABnzbd and BookStack, and on a
  community image without a justification.
- `latest` and untagged images are still refused.
- Go build and tests, and the Python suite, pass.

## Tests
- `internal/catalog/schema_test.go` `TestServicePublisherValidation`.
- `internal/catalog/curated_test.go` `TestCuratedImageTrust`.
- `tests/test_s360_image_trust.py` (ADR present and indexed, manifests carry
  publisher, CWA is community with justification, no manifest uses `latest`).

## Built
- ADR-056, schema fields, classification of all 18 non-craft packages
  (17 upstream, 1 community), tests, plan and index entries.
- Verified from the repository only; no registry was queried, so the
  per-image facts the ADR requires of future slices (digest, platforms) are
  not backfilled for existing packages.
