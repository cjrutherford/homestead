# S419: Uptime Kuma signs in through the Authentik gate

## Goal
Use S418's forward-auth engine for Uptime Kuma 2.5.7 (ADR-055): Kuma is gated as
a whole, administrators only, with **Kuma's own login turned off while gated**
(owner decision 2026-10-31) and only the push endpoint and public status pages
left open.

## Dependencies
S418 (engine, spike results) and ADR-055 Accepted (done).

## Scope
- `catalog/uptime-kuma.yaml`: `sso.forwardauth` with no header (`copyheaders`
  empty), `admin-only` access (only the admins and owner groups are bound; no
  users group), bypass list from ADR-055 §4: `^/api/push/.*`, `^/status/.*`,
  `^/api/status-page/.*`, `^/api/badge/.*`, `^/assets/.*`, `^/upload/.*`,
  `^/icon\.svg$` (each with its reason; spike may add the favicon). Never
  `/metrics`, `/dashboard`, `/socket.io/`, `/status-page`. Keep
  `allowwider: false` and the refusal to publish; rewrite the header comment and
  the exposure warning (status pages are open on the LAN, never public).
- **Kuma DB write** (ADR-055 §6, ADR-052 pattern as S415-S417): `onapply` sets
  the `setting` row `key='disableAuth'` to true; `onoff` sets it to false. The
  script runs in the Kuma container against `/app/data/kuma.db` with a version
  guard (Kuma version must equal the verified one), a schema guard (`setting`
  table with `key`, `value`, `type`; `user` table present), a database-type
  guard (read `/app/data/db-config.json`; refuse unless SQLite or absent), one
  `BEGIN IMMEDIATE` transaction with a busy timeout, idempotent update-or-insert,
  no secret in argv, logs or output; `restart: true` after the write.
  Pin test `kumaSignInVerified = "2.5.7"` (fails when the manifest version moves,
  and checks the script's VERIFIED constant), as `absSignInVerified` does.
- **Order:** on = gate (render, reload, probe), then `disableAuth` true and
  restart. Off = `disableAuth` false and restart (confirm), then remove the
  gate, then Authentik objects. A failed restore keeps the gate and stops. An
  interrupted run finishes in the same order. Engine hook: an app-script step
  that can run between the gate and the Authentik objects in each direction.
- **Refusals:** no Kuma administrator yet (empty `user` table; the plan says
  create it first), a database type other than SQLite, a published host port in
  Compose (precondition: Kuma reachable only through the edge; the manifest
  publishes none today and a test pins that).
- Engine extension only where Kuma needs it: admin-only binding mode, an app
  with no header and no env settings, the canonical-name rule for a local-only
  app (`<app>.lan`, `.local` redirected). `authentik_host` follows ADR-055 §8;
  the plan says LAN sign-in needs the public Authentik name to resolve.
- ADR-054 install review: none or admin for Kuma; the text says people reach it
  only as administrators, that Kuma's own login is off while gated, and that
  anyone who passes the gate is Kuma's administrator.
- Do not set Trust Proxy (manual step in the plan: Settings > Reverse Proxy;
  DEFERRED).
- Plan warnings: single login; `/metrics` stays gated because disabled auth
  drops its token check (Kuma issue #4628); status pages and badges are open on
  the LAN; push monitors still work; Authentik is required.

## Spike items (first step, on VM 2; amend ADR-055 if any breaks a decision)
1. The `setting` table layout and the stored `disableAuth` value format in
   2.5.7 (`true` JSON text), and whether a direct write plus restart is enough
   or the 60 s settings cache means a restart is mandatory.
2. Which SQLite driver the image provides for the script (no `sqlite3` shell
   assumed; `@louislam/sqlite3` or Node's `node:sqlite`).
3. Behaviour with `disableAuth` on and an empty `user` table (expected: refused
   by the plan, so only observed).
4. What a status page requests (network log): confirm no socket.io, and list
   any path beyond the bypass list.
5. `/metrics` with `disableAuth` on, through and around the gate.
6. `db-config.json` contents for the SQLite and MariaDB choices.

## Acceptance
- Plan/apply/off on both surfaces for Kuma; only admins/owner pass the gate; a
  user-group person is refused.
- After apply: through the gate Kuma opens straight to the dashboard with no
  Kuma login and live updates work over socket.io; after off, Kuma's login is
  back and the plain route is restored.
- Order holds: a forced failure of the restore keeps the gate on; Kuma is never
  reachable without the gate or its login.
- `/api/push/<token>` and a status page with its API, badge, assets and uploads
  answer without sign-in; `/metrics`, `/dashboard`, `/status-page` and
  `/socket.io/` are refused without sign-in.
- Refuses with no Kuma administrator, with a non-SQLite database, and if a host
  port is published.
- Authentik down: Kuma unreachable by browser, push and status pages still
  work; off restores the plain route and removes the Authentik objects.

## Tests
`internal/appsso/forwardauth_test.go` Kuma cases (admin-only bindings, no users
group, bypass set excludes `/metrics`, `/dashboard`, socket.io, refusals,
on/off order and failed-restore behaviour); the Kuma script test (version,
schema, db-type and no-user guards, idempotence, single transaction) in
`internal/appsso/kuma_test.go` with the pin `kumaSignInVerified = "2.5.7"`;
manifest test that no host port is published; Caddy golden for the Kuma gate
with a websocket-capable proxy block; plan no-leak checks; `internal/catalog`
schema cases for header-less forward-auth.

## Docs
ADR-055 (spike results), `docs/DEFERRED.md` (Trust Proxy step, MariaDB-backed
Kuma, Prometheus scraping through the gate), `docs/SLICE_INDEX.md`;
`docs/PARITY.md` unchanged (no new routes).

## Files likely to change
`catalog/uptime-kuma.yaml`, `internal/appsso/forwardauth.go`, new
`internal/appsso/kuma_test.go`, `internal/docker/routes*.go` and goldens,
`internal/catalog/*` tests, `docs/adr/ADR-055-*`, `docs/DEFERRED.md`,
`docs/SLICE_INDEX.md`.

## Live check on VM 2
Install Kuma; create its administrator; apply sign-in; reach it as the owner
(gate only, no Kuma login), see live monitor updates, add a push monitor and
call its URL with `curl` without a session; publish a status page and open it
logged out (page, badge, logo); confirm `/metrics` and `/dashboard` redirect to
Authentik; a non-admin person is refused; stop Authentik and check the
fail-closed result; turn sign-in off and confirm Kuma's login is back and the
route is plain.

## Out of scope
Kuma user mapping, Prometheus scraping through the gate, MariaDB-backed Kuma,
other apps.
