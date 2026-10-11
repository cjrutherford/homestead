# S261 — Periodic Drift Scan

## Goal
Scheduled file-drift scan recorded to audit and operations.

## Dependencies
S260,S212,S244,S213

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

## Acceptance Criteria
- Periodic Drift Scan implemented and documented
- Scheduler job runs the S212 file-drift scan on an owner-configurable interval
- Result recorded as an operation and an audit entry; clean scans stay quiet in the log summary
- Interval configurable over API and `homesteadctl`, and on the existing dashboard drift surface (`dashboard/src/app/drift`); `docs/PARITY.md` updated for every added or changed route (`tests/test_s037_parity.py` stays green)
- Doctor surfaces the latest scan time and drift state
- Tests use the injectable clock
- Narrow the "Drift scheduled scans and live-state comparison" entry in `docs/DEFERRED.md` to live-state comparison when landed (S262 removes it)
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
