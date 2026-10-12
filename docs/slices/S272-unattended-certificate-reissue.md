# S272 — Certificate Issuance and Unattended Re-issue

## Goal
Owner-approved first issuance of public certificates through DNS-01, then unattended re-issue of unchanged certificates with audited, backed-off retries, per ADR-024.

## Dependencies
S263,S103,S105,S226,S258

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.
Governed by ADR-024, Accepted 2026-10-11.
Split from S263.
Expanded 2026-10-12 (owner decision): S263 review found no daemon path issues certificates at all. `DNS01.Issue`/`IssueFor` have only test callers, the TLS wizard saves strategy and readiness only, no ACME account key is stored, and issued chains never reach Caddy. This slice therefore also delivers first issuance. If it proves too large for one commit, split first issuance into the next free slice number before starting and make this slice depend on it.

## Acceptance Criteria
- Certificate Issuance and Unattended Re-issue implemented and documented
- First issuance follows plan, preview, and approve (ADR-006, ADR-010): the plan names the certificate, normalized names, provider, challenge method, CA directory, and where the chain and key will be placed; nothing is requested from the CA before owner approval; approval is audited
- ACME account key generated on first use and held in the secret store (ADR-021), never logged or returned; account registration and terms acceptance are explicit in the plan
- The approved issuance runs as an operation through `DNS01.IssueFor`, recording the approved-issuance fingerprint (S263) and public chain; the private key is written 0600 where the edge (Caddy) serves it, and the edge configuration is reloaded so the certificate is actually served
- Issuance is available over API, `homesteadctl`, and the dashboard TLS/exposure surface; routes classified for ADR-020; `docs/PARITY.md` updated
- CA directory configurable so KVM validation runs against Let's Encrypt staging; production is the default only after owner choice
- When status is renewal-due and the issuance is unchanged (exact definition in ADR-024), the scheduler job re-issues through `DNS01.Issue` (`internal/cert/dns01.go:97`), which uses the ACME client in `internal/cert/acme.go`; otherwise status reports plan-required and nothing is issued
- Every attempt is audited, success or failure, without key material
- Failures retry with exponential backoff from 1h capped at 24h; the attempt counter persists across restarts and resets on success; CA rate-limit responses back off and honour `Retry-After`
- Failures surface in status, doctor, `homesteadctl`, and the dashboard surface from S263
- Tests cover first issuance end to end against a fake ACME server (plan, approval, account key custody, placement, edge reload), success, changed-names refusal (subset, superset, wildcard versus apex, provider change, challenge change), failure backoff with the injectable clock, and CA rate-limit handling
- Remove the "Renewal re-issue" and certificate-issuance-path entries from `docs/DEFERRED.md` when landed
- KVM: issue a staging certificate for a test name on a VM (DNS-01 with a test zone if available; otherwise document what could not be verified), confirm Caddy serves it, then force renewal-due and confirm unattended re-issue
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
