# S278 — Certificate First Issuance

## Goal
Owner-approved first issuance of public certificates through DNS-01: plan, preview, approve, issue as an operation, place the chain and key where Caddy serves them, and reload the edge.

## Dependencies
S263,S103,S105,S226,S270

## Scope
Split from S272 (2026-10-14) on the S272 size check: first issuance (plan/approve, ACME account custody, CA directory config, placement, edge reload, three surfaces) is a full slice on its own. Unattended re-issue stays in S272 and depends on this slice.
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

## Acceptance Criteria
- Issuance follows plan, preview, and approve (ADR-006, ADR-010): the plan names the certificate, normalized names, DNS provider, challenge method, CA directory, ACME account registration and terms, and where the chain and key will be placed; nothing is requested from the CA before owner approval; approval is audited
- ACME account key generated on first use and held in the secret store (ADR-021) under a `sys_` name, never logged or returned; registration and terms acceptance are explicit in the plan
- The approved issuance runs as an operation through `DNS01.IssueFor`, recording the S263 approved-issuance fingerprint and public chain
- The private key is written 0600 where the edge serves it; the chain is placed beside it; the edge configuration is reloaded so the certificate is served
- DNS provider credentials gain a write-only entry surface (discovered during S278: no API stored them, so DNS-01 could never be ready); values never logged, audited, or returned
- CA directory is configurable: Let's Encrypt staging for testing; production only after an explicit owner choice, never the silent default
- API, `homesteadctl`, and the dashboard TLS surface have parity; routes classified for ADR-020; `docs/PARITY.md` updated
- No secret or key material in logs, audit, API responses, or operation payloads
- Tests run against an in-process fake ACME server and a fake DNS provider; no real CA is ever called
- `docs/DEVELOPMENT.md` documents KVM end-to-end validation without public DNS (Pebble plus pebble-challtestsrv)
- Narrow the "Certificate issuance path" entry in `docs/DEFERRED.md`
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and S272 able to begin (unattended re-issue reusing this slice's issuance runner and placement) without hidden manual state.
