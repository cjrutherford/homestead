# TLS Provider Abstraction

## Status
Accepted

## Decision
Certificate issuance supports direct Caddy automation, DNS-01 orchestration, internal CA, and imported/manual paths without hard-coding one DNS plugin runtime.

## Consequences
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
