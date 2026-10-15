# TLS Provider Abstraction

## Status
Accepted

## Decision
Certificate issuance supports direct Caddy automation, DNS-01 orchestration, internal CA, and imported/manual paths without hard-coding one DNS plugin runtime.

## Amended by ADR-030
The internal CA path is persisted, serves local-only hosts through the edge, and its public root is distributed to owner devices; ADR-030 holds the substance.

## Consequences
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
