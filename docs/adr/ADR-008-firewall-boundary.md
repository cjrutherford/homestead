# Homestead Firewall Boundary

## Status
Accepted

## Decision
Homestead manages only its own nftables table/rules and does not replace unrelated administrator firewall policy.

## Consequences
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
