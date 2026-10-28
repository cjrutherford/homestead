# S328 — The certificate uses the zone chosen in setup

## Goal
ADR-047 and ADR-048 §5. An appliance set up only through the setup wizard
(Cloudflare, a dashboard name such as `homestead.example.com`, the token
stored in setup) gets the public certificate in its tunnel plan and can
issue it. Found live after a factory reset: the plan had no certificate
step and every app kept the local Authentik issuer.

## Cause
DNS-01 read its provider and zone only from the Domain page's saved
setup. The setup wizard stores a provider and the dashboard name, never a
zone, so the issuance plan was blocked ("no domain setup saved") and the
tunnel plan dropped the certificate without a visible warning.

## Scope
- `cert.Issuing.SetupZone`: a fallback used when the Domain page saved no
  zone. A Domain page setup that has a zone keeps precedence.
- The server wires it (`setupzone.go`): for Cloudflare, the zone is the
  apex Cloudflare holds for the setup domain, the tunnel's stored zone
  once a plan was applied, otherwise `ZoneFor` with the stored token. It
  is remembered per domain, so the plan digest is the same at preview,
  approval and apply, and issuance, renewal and the TLS page all agree.
- When the certificate still cannot be planned, the dashboard's tunnel
  review shows the blockers as a warning, and `homesteadctl tunnel plan`
  prints them. The blocker wording no longer says only "no domain setup".

## Rules
AGENTS.md slice rules apply. No new routes, commands or surfaces, so
PARITY is unchanged. No migration.

## Acceptance criteria
- Go tests: setup-wizard-only plan is Ready with the apex zone and a
  stable digest; Domain page precedence; blocked plan reports blockers.
- Dashboard spec covers the warning. All Go, Python and dashboard checks
  pass.

## Live check
Factory reset, run the setup wizard only, plan the tunnel: the plan shows
the certificate for the dashboard name in the zone apex; approving issues
it.
