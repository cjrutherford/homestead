# S292 — Setup walkthrough fixes

## Goal
Fix the bugs and disconnects found walking the whole setup from scratch on the KVM test VM (2026-10-20, Authentik 2026.8.3), so that each step continues to the next without the owner retyping or hunting for what Homestead already knows.

## Dependencies
S291.

## Scope
Implement only the functionality required for this slice. Preserve all accepted ADRs and customer-ownership guarantees. Do not pull deferred features into scope without an ADR and slice update.

Owner direction 2026-10-20: "let's try to fix all these bugs". This slice covers only the bug fixes that need no architecture decision. These are left for later slices:
- storage UX (S293);
- domain field, tunnel DNS record, tunnel token reuse and the tunnel service default (S294, needs an ADR);
- app names, app SSO wiring and backups in setup (S295, needs an ADR).

## Acceptance Criteria
- **F1:** the Status page on an unclaimed appliance says the appliance is not claimed and links to Claim, instead of "Can't reach the appliance". The doctor refusal (401/403) is not treated as unreachable.
- **F2/F3:** creating the first administrator records the account step's password confirmation (`PUT /api/v1/setup/steps/account {state: done}`) and opens `/setup`. Both are best-effort, and the Done screen links to setup.
- **F5/F9/F10:** the Sign-in step and the Authentik install's "first time" copy:
  - open Authentik's address itself, not `/if/flow/initial-setup/`, which Authentik 2026.8 denies when opened directly;
  - mention the denial for about a minute right after install;
  - give the real token UI: New Token, user `akadmin`, API Token, the row's copy button;
  - say which address the hosts entry points at;
  - drop the "to be confirmed" note.
- **F6:** inside setup, the install wizard's hand-back button takes a label from the embedding step ("Continue to sign-in" for Authentik), offers no Uninstall, and lists a data folder shared by several services once.
- **F7:** `GET /api/v1/sso` `defaults` use the wire names `authentik_url` and `origins`, so the Sign-in form is prefilled.
- **F11:** the review does not ask again for an API token already entered for that review.
- **F12:** the Authentik link round trip (`/sign-in?linked=1` or `sso_error`) returns to `/setup` with the same outcome while a fresh setup is not complete.
- **F14:** `facts.domain.credentials_sealed` checks the provider chosen in the setup step first, so credentials stored there are noticed with no domain wizard behind them.
- **F21:** the `cert.renewal-check` job succeeds on an appliance that has no TLS strategy yet.
- Docs: `SETUP_WALKTHROUGH.md` copy re-checked; `DEFERRED.md` lists the out-of-scope findings; `DEVELOPMENT.md` S292 section.
- Automated tests cover primary behavior
- Main build remains green

## Required Documentation
- `docs/architecture/SETUP_WALKTHROUGH.md`, `docs/DEFERRED.md`, `docs/DEVELOPMENT.md`.

## Handoff
Next: S293 (storage step UX), then S294 and S295, each with its ADR first.
