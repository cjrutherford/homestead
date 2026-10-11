# S262 — Running Container Drift

## Goal
Compare live containers against desired state and report divergence.

## Dependencies
S261

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

## Acceptance Criteria
- Running Container Drift implemented and documented
- `internal/catalog/drift.go` extended with a live comparison using `internal/docker` for image, ports, networks, and mounts
- Findings reported through the existing drift API and `homesteadctl` drift commands with parity rows
- Included in the S261 scheduled scan
- Read-only: no automatic correction; resolution stays on the S213 flow
- Tests use a fake Docker client
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
