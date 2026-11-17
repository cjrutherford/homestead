# S418: Forward-auth engine and Navidrome sign-in

## Goal
Implement ADR-055 (Accepted 2026-10-31): the generic
forward-auth gate and turn it on for Navidrome 0.64.2, so
`homesteadctl apps sign-in plan|apply [--off] navidrome` and the dashboard card
gate Navidrome behind Authentik, with the Subsonic API (`/rest/*`) and share links (`/share/*`) open and
keeping their own credentials or tokens.

## Dependencies
S417 (env-oidc adapters), ADR-043, ADR-044, ADR-047, ADR-052, ADR-054, and
ADR-055 Accepted (done). S419 depends on this slice.

## Step 0: spike on VM 2 (before code; record results in ADR-055)
Already verified (do not redo, ADR-055 §1): the embedded outpost answers at the
root `/outpost.goauthentik.io/` on `authentik-server:9000` (ping 204), not under
`/apps/auth/`; `/auth/caddy` returns 404 for an unknown host. Remaining, on
Authentik 2026.8.3 with `AUTHENTIK_WEB__PATH=/apps/auth/`:
1. The edge reaches `authentik-server:9000` (shared network) and the outpost
   picks the provider from the preserved Host.
2. `authentik_host`: how to set it on the embedded outpost (instance `config`
   via `/api/v3/outposts/instances/`, or managed-outpost settings), that the
   public-if-answering-else-local rule (ADR-055 §8) works, and that it does not
   conflict with anything Homestead or Authentik already sets (no Homestead
   code writes it today). The browser redirect and callback on the canonical
   name work for a LAN name and a public name.
3. The API calls: create proxy provider (`/api/v3/providers/proxy/`, mode
   `forward_single`), add to the embedded outpost (PATCH `providers`, merging),
   set unauthenticated paths.
4. Navidrome 0.64.2: `ND_EXTAUTH_USERHEADER` / `ND_EXTAUTH_TRUSTEDSOURCES`
   behaviour, the first-proxy-user admin rule on a database with an admin,
   username collision behaviour, `/rest/*` with and without the header, how to
   list existing usernames safely (API with the recovery admin, or database
   read), whether the edge's address on the network can be fixed.
5. Share links: with the network log on, open a share link in a browser and a
   player; list every path requested (`/share/{id}`, `/share/s/{id}`,
   `/share/img/{id}`, download, and any `/app/` static files) and settle the
   bypass list in ADR-055 §4.
If an answer breaks an ADR-055 decision, stop and amend the ADR before coding.

## Scope
- Catalog schema: `sso.forwardauth` (mutually exclusive with `sso.oidc`):
  `userheader`, `copyheaders`, `bypass` (anchored regexes with a reason each; `/outpost.goauthentik.io/*` is handled by the engine, never listed),
  `env` (trust settings, with `{{edge_source}}`), `admin: mapped|local`, and `afterlogin` hooks are not part of this slice (the Kuma `disableAuth` write is S419). Validate
  anchors, no bare `.*`, header names, and that env names are not secrets.
- `internal/sso`: proxy provider (forward single), application, outpost
  membership (merge, never replace other providers), idempotent
  Inspect/Reconcile/Remove, same token and scrub rules as the OAuth2 code.
- `internal/appsso`: a forward-auth adapter alongside the env-oidc one: plan
  (objects, groups, bindings, bypass reasons, canonical name and redirects,
  existing Navidrome usernames, refusals: username collision, no Navidrome
  administrator yet, no Authentik, name not covered), apply order and off order
  (ADR-055 §7, §11), status, record and digest.
- `internal/docker`: `Route.Gate` and rendering (strip identity headers, bypass
  handles, per-host `handle /outpost.goauthentik.io/*` proxied to
  `authentik-server:9000`, `forward_auth` to
  `authentik-server:9000` with `uri /outpost.goauthentik.io/auth/caddy`,
  canonical-name redirects) on the
  site, alias and tunnel entry; `ValidateRenderable`, reload and probe; the
  gate is set and cleared only by the sign-in operation.
- Navidrome manifest: `sso.class` becomes forward-auth with the settings above;
  `ND_EXTAUTH_USERHEADER=X-Authentik-Username`; `/rest/*` and `/share/*` bypass (plus any static prefix the spike
  finds); update the
  header comment (it said the header needs an edge that injects it).
- ADR-054 integration: Navidrome offers none or user/admin as "may reach", with
  the plan stating admins are chosen in Navidrome.
- Outpost `authentik_host`: set to the public Authentik address when it
  answers, otherwise the local one; re-set by the issuer-move re-plan.
- Doctor: a check that the embedded outpost answers when a gate is on.
- Public name: publishing or unpublishing a gated app re-plans sign-in and the
  canonical name (ADR-047/048 §6 rules).

## Acceptance
- Plan and apply on both surfaces; digest, previews and audit as for OIDC apps;
  no token or secret in argv, logs, Compose, env or responses.
- Apply on a fresh Navidrome: browser request to the app redirects to
  Authentik, then returns signed in; a person outside the groups is refused by
  Authentik; a client-sent `X-Authentik-Username` is ignored on every path.
- `/share/<id>` pages, streams and cover art open without sign-in; the
  identity header is stripped there; an invalid id is refused by Navidrome.
- `/rest/ping.view` with Subsonic credentials works without the gate; with a
  forged header and no credentials it fails.
- Authentik down: browser gets a refusal, `/rest/*` keeps working; off restores
  the plain route and removes provider, application, groups, outpost membership.
- Plan states the LAN-only consequence (public Authentik name must resolve
  from the LAN).
- Refuses on username collision and when no Navidrome admin exists.
- Re-plan is idempotent; interrupted apply and off finish on the next run.

## Tests
`internal/sso` (proxy provider and outpost with the fake Authentik, no-leak),
`internal/appsso/forwardauth_test.go` (plan, refusals, order, off, digest),
`internal/docker/routes_test.go` and a golden Caddy snippet for site, alias and
tunnel entry (strip, bypass, redirects, fail-closed), `internal/catalog`
(schema validation, curated Navidrome, version pin test as for S417),
`internal/server` sign-in route tests, bypass-list tests for `/rest` and
`/share`, an `authentik_host` selection test, a doctor check test, and a Python
fixture test of the rendered Caddyfile via `caddy validate` where available.

## Docs
ADR-055 (status, spike results), `docs/PARITY.md` (response fields only, no new
rows), `docs/DEFERRED.md` (MariaDB-backed apps, second outpost, other apps), `docs/SLICE_INDEX.md`, and the Navidrome manifest comment.

## Files likely to change
`internal/catalog/schema.go`, `catalog/navidrome.yaml`, `internal/sso/authentik.go`
(or new `internal/sso/proxy.go`), `internal/appsso/{appsso,apply,adapters}.go`
and new `forwardauth.go`, `internal/docker/{route,routes}.go`,
`internal/server` sign-in handlers, `internal/doctor`, `internal/people` binding
use, `docs/adr/ADR-055-*`, `docs/PARITY.md`, `docs/DEFERRED.md`.

## Live check on VM 2
Install Navidrome with a music folder and an admin; apply sign-in as the owner
(Authentik token flow); sign in as the owner and as a "user" person through the
gate; a "none" person is refused; connect a Subsonic client (for example
Symfonium or `curl /rest/ping.view`) with an app password; stop Authentik and
check the fail-closed behaviour; create a share link and open it logged out
(page, audio, cover art); publish Navidrome and check re-plan and
canonical redirects; turn sign-in off and confirm the plain route returns.

## Out of scope
Kuma (S419), a Navidrome admin mapping, other apps,
and any change to dashboard or OIDC sign-in.

## Built (S418)
- Edge gate: `internal/docker/gate.go` (`Gate`, bypass validation, rendering,
  `HealGates`); gated sites strip identity headers, proxy
  `/outpost.goauthentik.io/*` to `authentik-server:9000`, then `forward_auth`
  copying only `X-Authentik-Username`. Fails closed.
- Catalog: `sso.forwardauth` (`internal/catalog/forwardauth.go`); Navidrome
  0.64.2 declares it.
- Authentik: `internal/sso/proxy.go` (proxy provider `forward_single`,
  application, embedded-outpost membership merged, never replaced).
- Plan/apply: `internal/appsso/forwardauth.go`, same `apps sign-in
  plan|apply [--off]` surface (PARITY.md unchanged). Record holds adapter
  `forwardauth`, issuer, redirect `https://<canonical>`, gate revision.
- Canonical name: public name if published, else `<app>.lan`; other names 308.
  Plan states that the public Authentik name must resolve from the LAN.
- Refusals: path-served app, no owner sub, no route or several named routes,
  accounts unreadable, no Navidrome administrator, collision with an existing
  local account (first plan only), published without a public Authentik URL.
- Doctor: "authentik-outpost" probe while a gate is on.

## Deviation from the ADR
Apply order puts the Authentik side first, then the edge gate, then app
settings, so the gate never fronts an app that still trusts no header; the
outpost ping runs after the edge reconcile and fails the step. Turning off
reverses it (app env, gate removed, then Authentik objects).

## Spike results
Resolved from code and docs: header names, outpost path, `forward_auth`
shape, Navidrome `ND_EXTAUTH_*` settings, proxy provider mode.

## Verify live (VM 2)
1. Embedded outpost `authentik_host` set via instance `config`.
2. Edge reaches `authentik-server:9000`; `wget` exists in the caddy image.
3. Share-link paths and `^/app/(assets|static)/.*` are sufficient.
4. Navidrome `user` columns `user_name`, `is_admin`; db `/data/navidrome.db`;
   `docker cp` of the WAL database reads current rows.
5. Username collision behaviour; header trust with the edge CIDR.
6. Edge network holds only the edge and routed apps.
7. Publish, re-plan, canonical 308; sign-in off restores the plain route.

## Live results (VM 2, 2026-11-01, cb78b37)
Navidrome 0.64.2 installed local-only (op 194). Admin `cjrutherford` created
in Navidrome first. Sign-in plan and apply (op 197) succeeded: reach-authentik,
configure-authentik, gate, wire, record.
- Logged-out `https://navidrome.lan/` → 302 to the public Authentik authorize
  URL; a client-sent `X-Authentik-Username`/`Remote-User` changes nothing.
- `/outpost.goauthentik.io/ping` through the gate → 204 (items 1–2 hold).
- `/rest/ping.view` reaches Navidrome (Subsonic error for missing `u`);
  `/share/<bogus>` and `/app/static/<bogus>` → Navidrome 404, not the gate.
- `/metrics` → gated. `navidrome.local` → 308 to `navidrome.lan`.
- `ReadAccounts` read the existing `cjrutherford` account (item 4).
- Header trust: the edge with the header gets a session in `/app/`; without
  it, none. **Finding:** the host also gets a session with a forged header
  sent straight to the container address, because host traffic arrives from
  the bridge gateway (192.168.48.1), which is inside the trusted /20. Any
  local user on the appliance can therefore sign in as any Navidrome user.
  A /32 pin of the edge is the fix (DEFERRED; owner decision pending).
- Doctor `--live`: `authentik-outpost: ok`.
- Not yet checked: a real browser sign-in, a real share link, publish +
  re-plan, and `apply --off`.
