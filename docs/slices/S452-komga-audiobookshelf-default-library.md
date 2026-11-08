# S452 - Komga and Audiobookshelf get a usable default library at install, on bulk storage

## Goal and dependencies
ADR-059 (no manual first run): after install there must be somewhere to put the first comic or audiobook, and nothing to create by hand. Depends on S319 (first-run libraries), S411, S415-S417.

## What changed
- Komga declares `/library` on the bulk class (`<bulk>/komga/library`); its first run creates a `Comics` library over it (scan on startup, hourly). Audiobookshelf declares `/audiobooks` and `/podcasts` on bulk; its first run signs in as the recovery administrator and adds `Audiobooks` and `Podcasts` libraries. Both are matched by name, so a re-run adds nothing twice; Audiobookshelf touches nothing when the administrator is not Homestead's own, and Komga finishes an interrupted setup only when its own recovery account logs in.
- Manifest `firstrun.libraries` is accepted for the `komga` and `audiobookshelf` routines (kinds `comics`; `audiobooks`, `podcasts`), not only Jellyfin.
- Komga sign-in found live to need more than S411 assumed (all fixed here): a shell entrypoint drops the client secret until the client id is set (Spring refuses a secret with no id), a redirect URI template, `usernameAttribute: sub`, and `KOMGA_OIDCEMAILVERIFICATION=false` (Authentik 2026.8 sends `email_verified=false`).
- Curated first-login copy and manifest descriptions say the libraries already exist.

## Live (VM 2)
Komga and Audiobookshelf reinstalled from scratch through the wizard: libraries exist on `/mnt/bulk`, a CBZ and a WAV placed in them were picked up, owner and person A signed in from the LAN names, person C was refused by Authentik, data survived a restart.

## Tests
`internal/firstrun/komga_test.go`, `internal/firstrun/audiobookshelf_test.go`, `internal/catalog/curated_test.go`, `tests/test_s452_default_libraries.py`.
