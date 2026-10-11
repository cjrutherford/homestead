# Unattended Certificate Renewal

## Status
Proposed

## Decision
Unattended re-issue is allowed only when the names, provider, and
challenge method are identical to an owner-approved issuance. Any change
requires the normal plan, preview, and approve flow (ADR-010). Every
renewal attempt is audited, whether it succeeds or fails. Failures
surface in status and doctor output and retry with exponential backoff
rather than hammering the CA.

## Context
`internal/cert/monitor.go` reports renewal-due status (S105) but nothing
re-issues; DEFERRED.md records this. Issuance lives in
`internal/cert/acme.go`. Public exposure must remain planned, previewed,
and auditable, so renewal must not become a path to widen exposure.

## Alternatives considered
- Always require manual renewal: certificates expire silently.
- Renew with whatever the current config says: a config edit could change
  names or challenge unnoticed.
- Delegate to an external certbot: second source of truth, lock-in risk.

## Consequences
- The approved issuance fingerprint must be stored with each certificate.
- Rate limits and backoff must respect CA policy.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
