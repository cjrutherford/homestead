# S453 - Paperless-ngx is usable from install: new sign-ins get a working group, the owner is an administrator

## Goal and dependencies
ADR-059 (no manual first run). Depends on S346, S413. On a fresh install Paperless-ngx 3.3.4 gave every Authentik sign-in an account with no permissions at all: the page opened empty with "Error loading settings" and nothing could be uploaded until an administrator granted rights by hand.

## What changed
- New first-run routine `paperless-ngx` (`internal/firstrun/paperless.go`): once the web server answers, it runs a short Django shell script (through s6 `with-contenv`, script on stdin, no password involved) that creates the group `Homestead people` with view/add/change/delete on documents, tags, correspondents, document types, storage paths, saved views, notes and share links plus the UI-settings permissions the app's own screens read. The group is created once and never rewritten, so the owner's later edits stand; at creation, existing ordinary accounts that belong to no group join it.
- Manifest: `PAPERLESS_SOCIAL_ACCOUNT_DEFAULT_GROUPS` and `PAPERLESS_ACCOUNT_DEFAULT_GROUPS` name the group; `PAPERLESS_ACCOUNT_ALLOW_SIGNUPS=false` pins local sign-up closed; `admin_password` is now revealable and named by `firstrun:` (the local `admin` superuser comes from the image's own init).
- Admin rights: the provider asks for the `groups` scope and `PAPERLESS_SOCIAL_ACCOUNT_SYNC_SUPERUSER_GROUP` / `..._STAFF_GROUP` are `homestead-paperless-ngx-admins`, the Authentik admin group Homestead already keeps per app (owner and Admin-role people). Group sync is not enabled, so the default group is never cleared.
- Authentik 2026.8's `email_verified=false` is not a problem: Paperless sets email verification to none when outgoing email is off.

## Live (VM 2, fresh install through the wizard)
Group created with 41 permissions; owner signed in from the LAN name as an administrator, person A as an ordinary user, persons B and C (no access) were refused by Authentik; owner and A uploaded a PDF each, it was consumed and searchable, each person saw only their own, and the data survived a restart. Not published (public sign-in not tested).

## Tests
`internal/firstrun/paperless_test.go`, `internal/appsso/paperless_test.go`, `tests/test_s453_paperless_default_group.py`.
