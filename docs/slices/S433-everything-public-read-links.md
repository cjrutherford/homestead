# S433 Everything public read links (ADR-058)

## Scope
Opens `<base>/_homestead/read/<token>` and `<base>/_homestead/view/<token>` of one Everything app on the public tunnel name, and nothing else of the app. Planned, previewed, approved by digest and audited like other public exposure. Daemon API, CLI and dashboard have parity.

## Design as built
- `internal/docker` route flag `read_links` (path routes only; refused with `craft_app`, `strip_prefix`). The tunnel entry for such a route renders `handle <path>_homestead/*` with a matcher for `GET HEAD` and the path regexp `^<path>_homestead/(read|view)/[A-Za-z0-9_-]+$` proxied to the app, everything else under the path answers 404, and no other handle exists for that route, so the app itself stays 404 on the public name. The tunnel entry keeps `header_up X-Forwarded-For {http.request.header.Cf-Connecting-Ip}`, which replaces whatever the client sent; `trusted_proxies` is never set.
- `internal/server/readlinks.go`: `GET /api/v1/apps/{name}/read-links`, `POST .../plan`, `POST .../approve`. Plan: origin (the dashboard's public name), route, the two patterns, what stays refused, live link count (not in the digest), expiry cap, warnings, digest. Approve re-plans, returns 409 on a changed digest, and runs one operation: expose sets the bridge env `HOMESTEAD_EVERYTHING_PUBLIC_READ_ORIGIN` through the Compose override, recreates the stack, then sets the route flag and reconciles the edge; unexpose closes the edge first. Audits `readlinks.expose` and `readlinks.unexpose`. Posted route tables cannot change the flag (`preserveTunnelNames`). Only Everything bridge apps (image `homestead/everything-*`) have the routes; others get 404.
- Bridge: `-public-read-origin` / the env value. `fs.getReadURL` from the approved public origin mints links on that origin, any other operation from it is 403, and a public-looking origin that is not approved gets `read_links_not_exposed` (the S431/S432 deferred check). The session and CSRF checks are unchanged.
- CLI: `apps read-links status|plan|approve`. Dashboard: Public read links panel on the app page (`readLinks`, `readLinksPlan`, `readLinksApprove`).

## Tests
- `internal/docker/readlinkroutes_test.go`: the public route set is exactly the two prefixes (accept/reject table for the regexp), an unapproved route renders nothing, removal un-exposes only that app, the local site is unchanged, no duplicate handle with Published, forwarded address header, validation.
- `internal/server/readlinks_test.go`: plan changes nothing, unapproved exposes nothing and audits nothing, expose and remove (edge render, override env, audit), posted tables cannot change the flag, refusals, removable without a public name, status.
- `internal/everything/publicread_test.go`: public origin mints only when approved, still needs a session, no other operation from it, origin validation, read route behind the tunnel uses the forwarded address as `-trusted-proxies` expects.
- `cmd/homesteadctl/readlinks_test.go`, `dashboard/src/app/catalog/read-links.spec.ts`; parity and CLI-shape tests updated.

## Unproven
No live Caddy, tunnel or public name. See DEFERRED.md S433.
