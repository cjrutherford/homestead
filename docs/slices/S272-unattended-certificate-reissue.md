# S272 — Unattended Certificate Re-issue

## Goal
Unattended re-issue of unchanged certificates with audited, backed-off retries, per ADR-024, reusing the first-issuance runner from S278.

## Dependencies
S263,S103,S105,S226,S258,S278

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-024, Accepted 2026-10-11.
Split from S263.
Expanded 2026-10-12 to include first issuance, then narrowed 2026-10-14: the size check split first issuance (plan/approve, account key custody, CA directory, placement, edge reload, surfaces) into S278. This slice depends on it.

## Acceptance Criteria
- Unattended Certificate Re-issue implemented and documented
- When status is renewal-due and the issuance is unchanged (exact definition in ADR-024), the scheduler job re-issues through `DNS01.Issue` (`internal/cert/dns01.go:97`), which uses the ACME client in `internal/cert/acme.go`; otherwise status reports plan-required and nothing is issued
- Every attempt is audited, success or failure, without key material
- Failures retry with exponential backoff from 1h capped at 24h; the attempt counter persists across restarts and resets on success; CA rate-limit responses back off and honour `Retry-After`
- Failures surface in status, doctor, `homesteadctl`, and the dashboard surface from S263
- Tests cover (against the S278 fake ACME server) success with placement and edge reload, changed-names refusal (subset, superset, wildcard versus apex, provider change, challenge change), failure backoff with the injectable clock, and CA rate-limit handling
- Remove the "Renewal re-issue" entry from `docs/DEFERRED.md` when landed
- KVM: with the S278 staging certificate in place, force renewal-due and confirm unattended re-issue
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
