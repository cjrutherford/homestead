# S481 - The 13 craft apps at `/apps/<app>/`, behind the shared gate

## Goal and dependencies
ADR-062 section 6 (owner decision 2026-11-08). The craft apps become `publish: path` and, once published, answer at `https://homestead.<domain>/apps/<app>/` behind the shared Authentik gate; only members of the app's groups (and the owner) get in. Depends on S480. Owner decision: the LAN stays open, as ADR-045 and S320 left it.

## Subpath check
All 13 are nginx static packages that load assets by relative paths, and the edge strips `/apps/<app>` (`strip_prefix: true`); they already ran at their path on the LAN since S320. On VM 2 each one answered at its public path with the same bytes as on the LAN (FigCraft wasm and script compared by hash). None needed a base-path change; none stays `publish: never`.

## What changed
- **Manifests.** `publish: path`, `allowwider: true` (recommended stays `local`), `sso.forwardauth` with the app's service (no header, no bypass, no env), and a second exposure warning saying a published path is gated.
- **Edge.** `docker.Gate.TunnelOnly`: the local site renders a craft route exactly as before, the tunnel entry renders the gate (forward-auth, group check, strip prefix, craft wrapper handle, app). A published craft route with no gate renders nothing on the tunnel (404). A craft route may now carry a gate (it needs a path).
- **Plan.** The sign-in plan for a craft app is refused until it is published, has no redirects, is `TunnelOnly`, and says the local address stays open. The publish plan warns that the public path answers nothing until the gate is on, or names who is admitted once it is.
- **Install.** A craft install stays open on the LAN with no sign-in step (`accountlessInstallApps`); the wizard does not offer the gate.
- **Docs.** ADR-062 notes the owner decision; DEFERRED closes the open question; checklist steps `CRAFT-*-3`.

## Live (VM 2, 2026-11-08)
Published and gated all 13 through planned, digest-approved `apps public` and `apps sign-in` operations (FigCraft and GridCraft first, then the other 11). Before the gate the public path answered 404. After: signed out is redirected to Authentik (302); owner and person A (granted FigCraft and GridCraft) open the app; person C gets 403 "You do not have access to this app." (FigCraft, GridCraft, WordCraft, and Navidrome); A without a WordCraft grant gets 403; a forged `X-Authentik-Groups`, `X-Authentik-Username` and `Remote-User` change nothing (still 302 signed out); the LAN answers 200 for all 13 and Navidrome's LAN address still redirects to the public name; Doctor shows 0 fails.

## Not covered
See `docs/DEFERRED.md` (S481).

## Tests
`internal/docker/pathroutes_test.go` (`TestCraftPathRoute`: order of gate, group check, strip, wrapper, app; LAN unchanged; ungated 404), `internal/appsso/forwardauth_path_test.go` (`TestForwardPlanCraftIsTunnelOnly`), catalog fact table and craft tests, `tests/test_s481_craft_at_subpath.py`.
