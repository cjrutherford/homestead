# S273 — Platform Update Rollback

## Goal
Automatic and manual rollback of failed platform updates.

## Dependencies
S265

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-025, Accepted 2026-10-11.
Split from S265.

## Acceptance Criteria
- Platform Update Rollback implemented and documented
- On failed post-restart health check the previous binary is restored and the daemon restarted, as part of the apply operation
- Manual rollback route and `homesteadctl` command, plus a rollback action on the existing dashboard updates surface; `docs/PARITY.md` updated for every added or changed route (`tests/test_s037_parity.py` stays green)
- Rollback is audited with both versions and respects the ADR-025 counter policy (it is an explicit owner override when it moves to an older counter)
- Tests use the fake systemd and filesystem harness, including failure of the rollback restart itself (surfaced loudly, previous binary retained)
- Remove the "Platform update actuation" entry from `docs/DEFERRED.md` when landed
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
