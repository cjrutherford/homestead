# S272 — Unattended Certificate Re-issue

## Goal
Unattended re-issue of unchanged certificates with audited, backed-off retries, per ADR-024.

## Dependencies
S263

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-024, Accepted 2026-10-11.
Split from S263.

## Acceptance Criteria
- Unattended Certificate Re-issue implemented and documented
- When status is renewal-due and the issuance is unchanged (exact definition in ADR-024), the scheduler job re-issues through `DNS01.Issue` (`internal/cert/dns01.go:97`), which uses the ACME client in `internal/cert/acme.go`; otherwise status reports plan-required and nothing is issued
- Every attempt is audited, success or failure, without key material
- Failures retry with exponential backoff from 1h capped at 24h; the attempt counter persists across restarts and resets on success; CA rate-limit responses back off and honour `Retry-After`
- Failures surface in status, doctor, `homesteadctl`, and the dashboard surface from S263
- Tests cover success, changed-names refusal (subset, superset, wildcard versus apex, provider change, challenge change), failure backoff with the injectable clock, and CA rate-limit handling
- Remove the "Renewal actuation loop" entry from `docs/DEFERRED.md` when landed
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
