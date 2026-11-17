# S448 - Mealie and Calibre-Web Automated: the well-known login is replaced at install

## Goal and dependencies
ADR-059: both apps ship a seeded administrator with a public password (Mealie `changeme@example.com` / `MyPassword`, CWA `admin` / `admin123`). Depends on S442-S447. They share one mechanism (a first-run routine that does a compare-and-set of the seeded password), so they land together.

## What changed
- `internal/firstrun/mealie.go` (routine `mealie`): a python script in the container drives Mealie's own API on localhost: sign in with the seeded password, change it (`PUT /api/users/password`), then replace the seeded email and user name (`homestead-admin@homestead.lan`, `homestead-admin`) so the login-page credentials banner and the first-login wizard (both keyed on any user with the seeded email) go away. Each step is safe to repeat; an account the owner already changed refuses both sign-ins and is left alone. The password arrives on stdin.
- `internal/firstrun/cwa.go` (routine `calibre-web-automated`): a script run with the image's python, as the app's own `abc` user, replaces the seeded `admin` hash in `/config/app.db` in one compare-and-set UPDATE and renames the account `homestead-admin`, so an Authentik user named `admin` cannot take it over. Login reads the hash each time, so no restart.
- Both generated passwords are sealed, immutable and revealable (`admin_password`); manifests declare the `firstrun` block. Shared reply parsing in `stepReply`.
- Verified against `mealie:v3.29.0` and `calibre-web-automated:v4.0.8` on VM 2 (old password refused, new accepted, no setup screen). Pin tests `TestMealieRoutinePinnedToVerifiedVersion` and `TestCWARoutinePinnedToVerifiedVersion`.
- Found live: Authentik 2026.8 sends `email_verified=false`, so Mealie refused every Authentik sign-in; the package sets `OIDC_REQUIRES_EMAIL_VERIFICATION=false` and warns.
- Dashboard curated install copy for both; checklist steps `SRV-MEALIE-3a` and `SRV-CALIBRE-WEB-AUTOMATED-3a`, step 7 for CWA corrected.

## Tests
`internal/firstrun/mealie_test.go`, `internal/catalog` curated tests, `tests/test_s448_mealie_cwa_seeded_login.py`.
