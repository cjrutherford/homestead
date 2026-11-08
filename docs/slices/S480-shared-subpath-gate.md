# S480 - The shared gate, and Navidrome moved onto it

## Goal and dependencies
ADR-062 (owner decision 2026-11-08). Every forward-auth-gated app at a path shares one Authentik proxy provider on the dashboard's name; the edge admits each app's groups only. Navidrome (S472) moves onto it. Depends on S472 and ADR-062. S481 (craft apps) builds on it.

## What changed
- **Edge.** `docker.Gate.Groups` (plain group names, only for an app at a path). A gated path handle renders as `route { forward_auth ...; @denied not header_regexp X-Authentik-Groups (^|\|)(g1|g2)(\||$); respond 403; request_header -X-Authentik-Groups; cookie strip; app }`, so the order is the one written. Client `X-Authentik-*`, `Remote-User` and `X-Forwarded-User` are deleted first, in every handle. `SharedGateCanonical` picks the one gate name (the dashboard's public name once any gated path app is published, else its LAN name).
- **Plan.** For a path app `GatePlan` carries `shared`, `groups`, `provider_bypass` (the union over every gated app, read from the edge table) and `other_apps`. Provider `Homestead: apps gate`, application slug `homestead-apps-gate`; the application binds no group. The plan text names the shared provider, the groups, the 403 and the removal of the app's own provider.
- **Apply.** New step `retire-own-provider` deletes the app's old provider and application (groups and members stay) before `configure-authentik` creates or updates the shared one. Turning the gate off removes only this app's paths and groups while other apps stay behind it; the provider goes with the last one (`sso.API.RemoveAccess`).
- **Gate revision** includes the groups, so an installed Navidrome shows a re-plan until the move is applied.
- **Docs.** ADR-062, SLICE_PLAN section 10, checklist steps SRV-NAVIDROME-3 and -5.

## Live (VM 2, 2026-11-08)
Navidrome re-planned and applied (operation steps all succeeded). Owner and person A reach `/apps/navidrome/` on the public name; person C signs in to Authentik and gets 403; a client-sent `X-Authentik-Groups`, `X-Authentik-Username` and `Remote-User` change nothing (unauthenticated: redirect to Authentik; person C with forged groups: still 403); `/apps/navidrome/rest/ping.view` answers without an Authentik redirect; `homestead.lan` 308-redirects to the public name; `doctor --live` has no failures.

## Not covered
See `docs/DEFERRED.md` (S480).

## Tests
`internal/docker/gatepath_test.go` (order, exact match, validation, gate name), `internal/appsso/forwardauth_path_test.go` (shared plan, off plan with another app), `tests/test_s480_shared_gate.py`.
