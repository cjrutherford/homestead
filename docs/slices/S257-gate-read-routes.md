# S257 — Gate Read Routes

## Goal
Extend the session requirement to every remaining read route, completing ADR-020 rollout.

## Dependencies
S256

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

## Acceptance Criteria
- Gate Read Routes implemented and documented
- Config export, audit, operations, doctor, storage, and drift reads return 401 without a session
- Only the ADR-020 public allowlist remains reachable unauthenticated; the classification test has no remaining pending entries
- SSE event streams validate the session at connect time
- CLI and dashboard read flows keep working with a session; tests cover 401 and 200 for each route group
- `docs/PARITY.md` and operations docs describe the final boundary
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
