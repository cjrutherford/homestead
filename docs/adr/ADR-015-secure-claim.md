# Secure First Claim

## Status
Accepted

## Decision
An unclaimed appliance exposes only health/identity/claim endpoints and requires a short-lived one-use claim token to create the first administrator.

## Amended by ADR-020
ADR-020 refines the pre-claim surface: first-admin setup and claim-wizard
saves (until claim completes) are also reachable, and the surface is
enforced by session middleware. See ADR-020.

## Consequences
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
