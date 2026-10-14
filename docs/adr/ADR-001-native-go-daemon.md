# Native Go Control Daemon

## Status
Accepted

## Decision
Homestead uses a self-contained Go daemon installed as a root-owned systemd service so management and repair remain available independently of Docker.

## Amended by ADR-027
Platform binary replacement happens in a separate privileged apply unit, not in the daemon; see ADR-027.

## Consequences
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
