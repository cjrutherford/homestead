# S459 - Nextcloud: no welcome video over anyone's first page

## Goal and dependencies
ADR-059 (no manual first run). Depends on S298. Every person who signed in to a fresh Nextcloud 35 (owner and person A on VM 2) found the "Introducing the new Nextcloud" welcome video open over the dashboard until they closed it. It is the bundled `firstrunwizard` app: a per-person introduction rather than setup, but it is a first-run screen that blocks the first page.

## What changed
- Applying sign-in (`internal/appsso/adapters.go`, Nextcloud `Enable`) now also runs `occ app:disable firstrunwizard`. occ answers success when the app is absent or already off, so the step is idempotent and an owner who turns the app back on is not undone until sign-in is applied again.
- Checklist step SRV-NEXTCLOUD-3 expects no welcome video.

## Live (VM 2)
Nextcloud 35.0.1 installed through the wizard: owner and person A signed in from the LAN path name, person C was refused by Authentik, person A uploaded a text file and downloaded it with an identical sha256, the local `admin` signed in with the revealed password and no setup page, data survived a restart. After re-applying sign-in the welcome video no longer opens.

## Tests
`internal/appsso/appsso_test.go` (step order and the disable call), `tests/test_s459_nextcloud_no_wizard.py`.
