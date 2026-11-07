# S442 - ADR-059: no manual first run

## Goal and dependencies
Record the owner's 2026-11-07 rule that no catalog app needs a manual first run and that every app with a login gets single sign-on. Docs only. Authentik's own first run is excluded and stays with the owner. Later slices implement the app routines.

## What changed
`docs/adr/ADR-059-no-manual-first-run.md`, the amendment pointer on ADR-055, and the slice plan and index.

## Tests
No test change; the docs and ADR format tests run unchanged.
