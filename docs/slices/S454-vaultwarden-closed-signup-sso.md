# S454 - Vaultwarden: registration closed, Authentik sign-in creates the account

## Goal and dependencies
ADR-059 (no manual first run, Vaultwarden exception), ADR-047. Depends on S345, S414. Vaultwarden installed with signups open: whoever reached the page first could create a vault, and the owner had to register by hand.

## What changed
- `catalog/vaultwarden.yaml`: `SIGNUPS_ALLOWED=false`. Verified on the pinned 1.37.4 image that Authentik sign-in still creates the account for a new person while local registration returns "Registration not allowed".
- Authentik 2026.8 sends `email_verified=false` and Vaultwarden refuses it ("You need to verify your email with your provider"); `SSO_ALLOW_UNKNOWN_EMAIL_VERIFICATION` only covers an absent claim, so it does not help. New generic manifest field `verifiedemail` (sso.oidc.env): the app's Homestead groups claim also carries `email_verified=true`, and the app asks for the `groups` scope (`SSO_SCOPES`). Only people the app's Authentik groups admit (owner, granted people) ever reach it; people not granted are refused by Authentik before any claim is issued.
- Each person chooses their own master password at first sign-in; Homestead never holds it. The admin page stays off (no `ADMIN_TOKEN`, so no token exists to seal or reveal).
- Dashboard first-login copy and the checklist describe the single sign-on path; the "create the first account" step is now a check that registration is refused.

## Live (VM 2, fresh install through the wizard, not published)
Owner and person A signed in through Authentik from vaultwarden.lan, each chose a master password; A saved a Login item and it survived reload and a container restart; person C was refused by Authentik ("Permission denied"); registration endpoint refused, user count unchanged.

## Tests
`internal/appsso/vaultwarden_test.go`, `internal/catalog/curated_test.go`, `tests/test_s345_vaultwarden.py`, `tests/test_s454_vaultwarden_closed_signup.py`.
