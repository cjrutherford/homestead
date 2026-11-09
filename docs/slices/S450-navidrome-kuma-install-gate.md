# S450 - Navidrome and Uptime Kuma get their administrator and forward-auth gate at install

## Goal and dependencies
ADR-059 point 3 (amends ADR-055 point 3): no first-account screen, no manual step. Depends on S418-S420, S444-S449.

## What changed
- First-run routines `navidrome` (admin created through `/auth/createAdmin`, password generated, sealed, revealable, sent on stdin) and `uptime-kuma` (admin row inserted into Kuma's database through the manifest `account` argv; Kuma's own login stays on until the gate is applied).
- Both manifests declare `firstrun:` and a revealable `admin_password` secret; `ForwardOwnLoginOff.Account` is a new static argv.
- The install sign-in plan is anticipated for forward-auth apps (`anticipatedAccounts`), so its digest equals the live plan and the install wizard offers address, publish and sign-in together. ADR-055 refusals (no admin, collision, host block) remain as checks.
- The Kuma `on` script also sets Trust Proxy, so that manual step is gone.
- Install notice, public-address warning and curated copy describe the gate; stale checklist notes replaced.

## Tests
`internal/firstrun/navidrome_test.go`, `internal/firstrun/uptimekuma_test.go`, `internal/appsso/ownlogin_test.go`, `internal/server/forwardauth_test.go`, `tests/test_s450_navidrome_kuma_install_gate.py`.
