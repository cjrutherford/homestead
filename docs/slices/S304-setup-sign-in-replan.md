# S304 — Setup names a needed sign-in re-plan

## Goal
In the fresh-install walkthrough, approving the tunnel (adopting
`homestead-vm2`) completed the Domain step and moved setup on to First apps.
The tunnel plan had said to re-plan Sign-in afterwards, but once it was
approved that note was gone: Sign-in read Done and nothing in setup said the
public dashboard address was not yet in sign-in, although `GET /api/v1/sso`
reported `replan`.

## Dependencies
S288/S291 (setup), S296 (public dashboard and the re-plan flag). No ADR:
Sign-in is still re-planned only by the owner (no automatic re-plan).

## Scope
- Setup facts gain `sign_in.replan`, from the same check as the Sign-in
  page's re-plan flag. Reading it contacts nothing.
- The Sign-in step stays done (like a dormant link) and its detail warns and
  says where to re-plan; the Domain step's detail after a tunnel says the
  same, so the Summary shows it twice where the owner looks.

Out of scope: none discovered.

## Acceptance Criteria
- `internal/setup` tests: a needed re-plan keeps Sign-in done and warns in
  the Sign-in and Domain details.
- Main build remains green; `docs/PARITY.md` unchanged (no route or surface
  change; one more fact on the existing setup status).
