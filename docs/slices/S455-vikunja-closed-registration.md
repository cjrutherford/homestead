# S455 - Vikunja: registration closed, Authentik sign-in creates accounts

## Goal and dependencies
ADR-059. Depends on S357. Vikunja installed with local registration open, so anyone reaching the page could create an account, and the manifest and checklist told the owner to register the first account by hand.

## What changed
- `catalog/vikunja.yaml`: `VIKUNJA_SERVICE_ENABLEREGISTRATION=false`. Verified on the pinned 2.7.1 image that Authentik sign-in still creates the account for the owner and a granted person while `POST /api/v1/register` answers 404. Vikunja does not need `email_verified`, so nothing else was needed (Authentik 2026.8's false value is not checked).
- Manifest description, sign-in warning and integration note no longer say registration is open; the sign-in warning says people are created at first Authentik sign-in.
- Checklist: the Vikunja section no longer refers to Vaultwarden (stale note), and a new step SRV-VIKUNJA-1a checks that registration is closed.

## Live (VM 2, fresh install through the wizard, not published)
Owner and person A signed in through Authentik from vikunja.lan and were named correctly; person C was refused by Authentik; A created two tasks in Inbox, they survived reload and a container restart; the register endpoint returned 404.

## Tests
`internal/appsso/vikunja_test.go`, `tests/test_s455_vikunja_closed_registration.py`.
