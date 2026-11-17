# S472 - Navidrome at a subpath

## Goal and dependencies
First app to use the S471/S479 move (ADR-061). Navidrome serves under `/apps/navidrome/` on the dashboard's names and its Authentik forward-auth gate (ADR-055) moves inside the dashboard site. Depends on S469, S471, S479.

## What changed
- **Manifest.** `catalog/navidrome.yaml`: `publish: path`, `serve: path`, `path: /apps/navidrome/`, `ND_BASEURL: "{{route_path}}"`. The bypass regexes stay written for the app's root; they are prefixed at plan time (`docker.PathBypass`).
- **Gate inside the dashboard site.** A gated path route renders (`renderGatedPath`) as a `route`: strip trusted request headers, bypass handles (cookie strip plus proxy), then the `forward_auth` handle. The cookie strip is after the forward-auth subrequest so Authentik still sees its cookie. The public `/outpost.goauthentik.io/*` handle is added on the dashboard's public site.
- **One canonical name.** Authentik proxy providers match one external host, so a gated path app has one: the dashboard's public name when the path is published and the dashboard has a tunnel name, otherwise the dashboard Host (`homestead.lan`). Other dashboard names 308-redirect to it (`PathGateCanonical`).
- **Sign-in.** `appsso` forward-auth accepts path routes (sign-in URL is canonical plus the path, bypasses prefixed); `setGate` no longer skips them. The move's `edge` step converts an existing gate; `planSignIn` accepts forward-auth manifests.
- **Forward only.** No rollback (owner decision). A failed probe keeps the old name (S479); the manifest would go back to `publish: host` with reason `no_subpath`.

## Live (VM 2)
Moved through `apps move plan/apply`. Owner and person A reach `https://homestead.<domain>/apps/navidrome/` behind forward-auth, person C is refused, a track plays, `/apps/navidrome/rest/ping.view` answers without an Authentik redirect, the old name and CNAME are gone, Doctor has no failures.

## Not covered
See `docs/DEFERRED.md` (S472).

## Tests
`internal/docker/gatepath_test.go`, `internal/appsso/forwardauth_path_test.go`, `internal/server/appmove_gate_test.go`, `internal/catalog` fact table and curated tests, `tests/test_s472_navidrome_subpath.py`.
