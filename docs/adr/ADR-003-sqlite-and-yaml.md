# SQLite and YAML State

## Status
Accepted

## Decision
SQLite stores operational state. YAML stores customer-readable desired configuration and generated references.

## Consequences
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
