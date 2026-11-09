# Homestead Firewall Boundary

## Status
Accepted

## Decision
Homestead manages only its own nftables table/rules and does not replace unrelated administrator firewall policy.

## Consequences
- Implementation must preserve this boundary.
- Changes require a superseding ADR.

## Clarification, 2026-11-01 (S421)
The boundary is Homestead-owned tables, identified by name, never the
administrator's. Homestead owns two: `inet homestead` (input rules for the
management and DNS ports) and `inet homestead_gate` (output and forward drops
that keep other users and routed hosts off gated apps, ADR-055 §5 amendment).
Both use policy accept and are created, replaced and deleted only by Homestead;
no other table, chain or hook of the administrator's is touched.
