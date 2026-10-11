# S271 — Backup Recipients

## Goal
Owner-held age recipients, recovery identity export, and the owner actions that clear the doctor warning.

## Dependencies
S259

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-022, Accepted 2026-10-11.
Split from S259. The restore runbook and its literal `age` CLI test are S275.

## Acceptance Criteria
- Backup Recipients implemented and documented
- API and `homesteadctl` to list, add, and remove owner recipients and to export the recovery identity (export is explicit, audited, and shown once); backups are encrypted to every configured recipient; removing the last recipient is refused
- Dashboard surface for recipient management and identity export using S248 tokens and `StateMessage` (extends the S267 backup surface)
- The S259 doctor warning clears when an owner-added recipient exists or the owner records that the recovery identity was exported (ADR-022)
- `docs/PARITY.md` updated for every added or changed route (`tests/test_s037_parity.py` stays green)
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
