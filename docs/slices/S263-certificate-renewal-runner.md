# S263 — Certificate Renewal Runner

## Goal
Scheduled certificate check and unattended re-issue under ADR-024.

## Dependencies
S260,S105,S226

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
ADR-024 is Proposed and must be Accepted before implementation begins.

## Acceptance Criteria
- Certificate Renewal Runner implemented and documented
- Scheduler job calls `cert.Check` (`internal/cert/monitor.go`) on a schedule and re-issues through `internal/cert/acme.go` when due
- Unattended re-issue only when names, provider, and challenge method match an owner-approved issuance; otherwise status reports plan-required
- Every attempt audited; failures retry with exponential backoff and surface in status and doctor
- Approved-issuance fingerprint stored with each certificate
- Tests cover success, changed-names refusal, failure backoff, and CA rate-limit handling
- Remove the renewal actuation entry from `docs/DEFERRED.md`
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
