# Unattended Certificate Renewal

## Status
Accepted

Accepted by the owner on 2026-10-11.

## Decision
Unattended re-issue is allowed only when the issuance is **unchanged**
from an owner-approved issuance. Any change requires the normal plan,
preview, and approve flow (ADR-006: public exposure is planned,
auditable, and reversible). Every renewal attempt is audited, whether it
succeeds or fails.

### Definition of unchanged
All of the following must hold between the stored approved-issuance
fingerprint and the issuance about to run:

- The set of names is equal after normalization (lowercased, trailing dot
  stripped, IDNA/punycode form, de-duplicated); order is irrelevant.
- Wildcards are compared literally: `*.example.com` equals only
  `*.example.com`, and neither implies nor is implied by `example.com` or any
  host name beneath it.
- The DNS provider id is identical.
- The challenge method is identical (for example `dns-01`).

Any difference, including a subset or superset of names, means
plan-required: the runner reports it in status and doctor and does not
issue.

### Schedule and backoff
- Renewal window: begin attempting when the certificate has 30 days or
  fewer remaining. The check runs on the scheduler (ADR-023), at most
  daily.
- Failures retry with exponential backoff starting at 1 hour, doubling each
  consecutive failure and capped at 24 hours, with the attempt counter
  stored in SQLite. A success resets it. CA rate-limit responses are
  treated as failures and additionally honour any `Retry-After`.
- Failures surface in status and doctor output. Attempts continue until the
  certificate expires; expiry itself is surfaced as an error.

## Context
`internal/cert/monitor.go` reports renewal-due status (S105) but nothing
re-issues; DEFERRED.md records this. The issuance entry point is
`DNS01.Issue` (`internal/cert/dns01.go:97`); `internal/cert/acme.go` is the
ACME protocol client beneath it. Public exposure must remain planned,
previewed, and auditable, so renewal must not become a path to widen
exposure.

## Alternatives considered
- Always require manual renewal: certificates expire silently.
- Renew with whatever the current config says: a config edit could change
  names or challenge unnoticed.
- Treat a subset of names as unchanged: narrows the certificate, but would
  still break services silently.
- Delegate to an external certbot: second source of truth, lock-in risk.

## Consequences
- The approved issuance fingerprint (normalized name set, provider id,
  challenge method) must be stored with each certificate.
- Rate limits and backoff must respect CA policy.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
