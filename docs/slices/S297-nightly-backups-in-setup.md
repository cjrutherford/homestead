# S297 — Nightly backups in setup

## Goal
Give a fresh appliance backups without per-app work: one nightly policy for every stateful app, proposed by a new setup step and approved by the owner (ADR-039 §2).

## Dependencies
S296. ADR-039, ADR-023, ADR-026, ADR-036.

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

Out of scope: off-appliance destinations, app sign-in (S298).

## Acceptance Criteria
- **Policy:** one record with time of day (default 02:00 local), retention (default 7) and on/off. It covers every stateful installed app, including later installs; stateless packages and apps with their own schedule are skipped. Changes go through plan and approve, and are audited.
- **Scheduler:** a wall-clock job kind runs each covered app at the first policy time after its last run; a run missed while the appliance was off runs once at start.
- **Setup step:** Backups follows Apps. It shows the proposed policy and the apps it covers, and needs a backup storage class (otherwise it explains and links to Storage). Approving it completes the step; skipping is allowed. Appliances that finished setup before the step have it waived.
- **Surfaces:** policy status, plan and apply in the API, `homesteadctl backup policy`, and the Backups page.
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- `docs/PARITY.md`, `docs/DEFERRED.md`, `docs/DEVELOPMENT.md`, `docs/architecture/SETUP_WALKTHROUGH.md`, `docs/architecture/BACKUP_RESTORE_RUNBOOK.md`.

## Handoff
Next: S298 app sign-in through Authentik.
