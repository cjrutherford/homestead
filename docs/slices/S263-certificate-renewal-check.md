# S263 — Certificate Renewal Check

## Goal
Scheduled certificate check, approved-issuance fingerprint, and renewal status reporting; no issuance, per ADR-024.

## Dependencies
S260,S105,S226

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-024, Accepted 2026-10-11.
Split: this slice is read-only reporting. Unattended re-issue is S272.

## Acceptance Criteria
- Certificate Renewal Check implemented and documented
- Scheduler job calls `cert.Check` (`internal/cert/monitor.go:126`) at most daily; today its only caller is `Wizard.ValidateIssued` (`internal/cert/wizard.go:242`), which is owner-triggered
- Renewal window is 30 days or fewer before expiry; status reports ok, renewal-due, plan-required, or expired
- Approved-issuance fingerprint (normalized name set, provider id, challenge method, per ADR-024) is stored with each certificate issued through the owner-approved flow (`DNS01.Issue`, `internal/cert/dns01.go:97`)
- Renewal status exposed over API and `homesteadctl`, surfaced in doctor, and shown on the dashboard TLS/exposure surface; `docs/PARITY.md` updated for every added or changed route (`tests/test_s037_parity.py` stays green)
- Every scheduled check is an operation and clean checks stay quiet in the log summary
- Tests cover due, not due, expired, and fingerprint mismatch status with the injectable clock
- Narrow the "Renewal actuation loop" entry in `docs/DEFERRED.md` to re-issue only when landed (S272 removes it)
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
