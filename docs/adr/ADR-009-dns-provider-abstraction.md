# DNS Provider Abstraction

## Status
Accepted

## Decision
Public/local/overlay DNS intent is provider-neutral. Initial public providers are Manual, Cloudflare, Route 53, and DigitalOcean.

## Consequences
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
