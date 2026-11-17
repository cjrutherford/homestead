# S504 - Plans name gated apps whose local address moves with the shared gate

## Goal and dependencies
The plans the owner approves must say what the single shared Authentik gate does to other apps' local addresses. Depends on S480, S481, S498, S503, ADR-055 and ADR-062.

## Problem
ADR-062 section 5: the shared gate answers on one name, which is the dashboard's public name once any gated path app is published. Every gated app at a path then redirects from the local name to it, and a gated app that is not published answers 404 there. The publish plan and the sign-in plan only said "your local address stays open", so the owner approved a plan without learning that another app would become unreachable on the LAN. The publish warning also said every request is checked with Authentik first, without mentioning the gate's bypass paths.

## Fix (owner decision: warn in the plan, do not refuse)
- `docker.GateCanonicalMove` (internal/docker/gate.go) computes `SharedGateCanonical` for the edge table now and as it would be after one path route changes, and lists the other gated, non-tunnel-only, unpublished path apps. `GateMove.Lines` words the line for LAN to public ("will redirect to ..., so it will be unreachable until it is published or its sign-in gate is turned off") and public to LAN ("stops redirecting ..."). No line when the canonical does not move.
- The sign-in plan (`internal/appsso`, turning the gate on or off) adds the lines to Changes; the publish and unpublish plans for a path app (`internal/server/apppublic.go`) add them to Warnings. Both are inside the plan digest. Inputs carry `EdgeRoutes` and `GateTitles`; the edge table used is the one the plan is built over, so a sign-in plan embedded in a publish plan stays quiet.
- The publish warning for a gated app lists its bypass paths with the sign-in plan's wording (`appsso.OpenWithoutGateLine`), prefixed with the app's path.

## Not built
No refusal and no behaviour change: the redirect rules are as in ADR-062. A host-served gated app (not at a path) has no shared gate and gets no line.

## Verification
Go: `internal/docker/gatemove_test.go`, `internal/appsso/forwardauth_path_test.go` (`TestForwardPlanNamesAppsWhoseLocalAddressMoves`), `internal/server/apppublic_gatemove_test.go` (both directions, unmoved, digest coverage, bypass sentence present and absent).
