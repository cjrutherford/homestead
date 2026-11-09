# S388 — Validate selected people in app setup

## Goal
Reject invalid install-time people selections before installing or starting
the grant step, for both dashboard and CLI requests.

## Behavior
- Refuse people supplied without an approved sign-in plan instead of
  silently discarding the selection.
- When sign-in can run, check that all selected people are distinct and
  active before accepting the setup. Check the whole set again immediately
  before granting any person access. Existing individual grant validation
  remains in force.
- If the Authentik token is not kept, the existing documented skipped-step
  behavior remains unchanged.

## Limits
An Authentik or network failure, or an account change after preflight, can
still interrupt sequential grants after earlier grants have succeeded.
Rollback and atomic cross-service grants remain deferred. Tests use the
local people database and do not prove actual Authentik provisioning.
