# S246 — Tunnel Provider

## Goal
Cloudflare Tunnel as a first-class remote-access provider with API and
CLI surfaces, preferred automatically when the router is silent.

## Dependencies
S137,S227

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

## Acceptance Criteria
- Tunnel Provider implemented and documented
- Router discovery falls through UPnP, NAT-PMP, PCP, then tunnel (ADR-017)
- Tunnel setup, status, and teardown over the API and CLI with sealed credential storage
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- Update relevant architecture/operations docs.
- Record newly discovered deferred work in `docs/DEFERRED.md`.
- Record any architectural change as an ADR before implementation.

## Handoff
Leave the repository buildable, tests passing, and the next slice able to begin without hidden manual state.
