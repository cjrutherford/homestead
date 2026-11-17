# S447 - Actual Budget: the appliance owner is the server owner

## Goal and dependencies
ADR-059: Actual gives the server-owner role to whoever signs in through OpenID first while no named user exists, so a person could win it. Depends on S442-S446.

## What changed
- `internal/firstrun/actualbudget.go` (routine `actual-budget`): a node script in the container (node:sqlite) inserts the appliance owner (their Authentik username, from `firstRunOwner`) into Actual's `users` table as ADMIN and owner when no named user exists, and moves files owned by the password account to them (Actual's own rule). Idempotent; waits for the account database; does nothing while the owner's name is unknown and runs again when sign-in is applied.
- Verified against `actualbudget/actual-server:26.10.0`: users/files schema, the OpenID callback signs in the existing row by `preferred_username`, empty display name filled from the provider. Pin test `TestActualBudgetRoutinePinnedToVerifiedVersion`.
- Catalog: `firstrun` block without account or secret (schema allows that for this routine only), description and warnings corrected.
- Dashboard curated copy for actual-budget plus the 16 packages that had none (generic line; see DEFERRED), which fixes `curated-copy.spec.ts`.
- Checklist SRV-ACTUAL-BUDGET-1a.

## Tests
`internal/firstrun/actualbudget_test.go`, `tests/test_s447_actual_budget_owner.py`.
