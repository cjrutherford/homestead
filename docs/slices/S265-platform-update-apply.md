# S265 — Platform Update Apply

## Goal
Apply a verified platform update: stage, keep the previous binary, replace atomically, restart via systemd, and health check; automatic rollback is S273.

## Dependencies
S264,S194,S257

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-025, Accepted 2026-10-11. Actuation is gated by S257 sessions.
Split: this slice swaps and restarts; rollback is S273. A failed health check here marks the operation failed, keeps the previous binary in place for manual restore, and the runbook documents the restore command.

## Acceptance Criteria
- Platform Update Apply implemented and documented
- Apply runs as an operation: stage the artifact, re-verify it with S264 immediately before use (call `release.VerifyDigestUnchanged` with the digest recorded at verification, immediately before replacement, and refuse on mismatch; see docs/architecture/RELEASE_VERIFICATION.md), keep the previous binary, replace atomically, restart `homestead-server` via systemd
- The restart ends the process running the operation, so the operation is finalized on next start from a persisted marker; tested with the fake harness
- Post-restart health check; failure marks the operation failed and leaves the previous binary retained
- Only artifacts verified by S264 are applied; an older-counter artifact applies only with the owner's explicit `--allow-older` override (ADR-025), which is audited with both versions and offered on the dashboard behind a confirmation
- API and CLI routes for apply, and an apply action on the existing dashboard updates surface; `docs/PARITY.md` updated for every added or changed route (`tests/test_s037_parity.py` stays green)
- Audited with before and after versions
- Tests use a fake systemd and filesystem harness
- Narrow the "Platform update actuation" entry in `docs/DEFERRED.md` to rollback when landed (S273 removes it)
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
