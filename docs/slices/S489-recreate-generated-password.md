# S489 - Recreate a missing person with a generated password

## Goal and dependencies
Close flag gap G9 (`people resolve recreate --generate`). Depends on S317 and S487.

## What changed
- The missing-person card on `/admin/people` has "Recreate" (one-time link, default) and "Recreate with a generated password". The second sends `method: generated` on `POST /api/v1/people/{id}/resolve`.
- The password comes back in `revealed` and is shown once in the existing reveal dialog, like add and reset. The daemon already supported it; no Go change. It is not logged or stored by the dashboard.

## Verification
Spec in `people.spec.ts`, `tests/test_s489_recreate_generated_password.py`, checklist row `END-PARITY-7`, live check on VM 2 with a throwaway person.
