# Native Go Control Daemon

## Status
Accepted

## Decision
Homestead uses a self-contained Go daemon installed as a root-owned systemd service so management and repair remain available independently of Docker.

## Consequences
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
