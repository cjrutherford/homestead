# S422: Uptime Kuma single sign-in

## Goal
Owner decision 2026-11-01: a gated admin-only app needs one sign-in. Uptime Kuma
2.5.7 is behind the ADR-055 gate (S419) and S421 keeps non-root host users and
foreign forwarded traffic off its port, but Kuma's own login was still on, so
the owner signed in twice. Turn Kuma's own login off while the gate is on, and
back on before the gate is removed.

## Dependencies
S419, S421, S358 and S415-S417 (the guarded app-database write pattern), ADR-055.

## Scope
- Manifest: `sso.forwardauth.requireshostblock: true` and a new
  `sso.forwardauth.ownloginoff` block (`prepare`, `onapply`, `onoff`, optional
  `execuser`): static argv run in the forward-auth service's container. Validation
  requires the host block and `access: admin-only`, and static arguments with
  no placeholders. The Kuma script is one shared `node -e` script (mode is the
  last argument) pinned to 2.5.7.
- Apply on (after Authentik and the gate): `prepare` (writes
  `/app/data/db-config.json` `{"type":"sqlite"}` when absent, refuses any other
  database type with guidance, version guard) then restart if it wrote; `on`
  (version guard, `user` and `setting` table and column guards, the first
  account only when the `user` table is empty, setting `disableAuth` = `true`
  in one `BEGIN IMMEDIATE` transaction) retried while Kuma creates its tables,
  then restart. Existing accounts and passwords are kept.
- The account: Homestead creates it only on a Kuma with no user. Username is the
  person who applied sign-in (reduced to plain characters, "owner" if none). The
  password is 32 random bytes that are dropped; only the bcrypt hash (cost 10, as
  Kuma's bcryptjs) is made, in Go, and goes to the script on stdin. It is never in
  argv, Compose, the plan, the audit log or output.
- Apply off: `onoff` (`disableAuth` = `false`, account kept, restart) is the first
  step after reaching Authentik, before the gate and Authentik objects go.
- Plan: says Kuma's own login turns off, why it is safe (admin-only gate plus the
  host block; the plan refuses when the block cannot be kept), what the command
  changes, and the owner's way back (`docker exec -it uptime-kuma-uptime-kuma npm
  run reset-password`). The off plan lists "login back on first".
- Bypass-path review (Kuma 2.5.7 routers): `/api/push/:token`, `/api/badge/*`,
  `/api/status-page/*`, `/status/*` have no `apiAuth` or `basicAuth` and no write
  or admin power; `disableAuth` changes only `apiAuth`/`basicAuth` and the
  socket auto-login. `/socket.io/`, `/metrics`, `/dashboard` stay behind the gate,
  so the auto-login socket is never reachable without Authentik. Static paths
  serve files. No bypass gains power.
- Docs: ADR-055 dated amendment (Status unchanged), DEFERRED, SLICE_INDEX.
- Out of scope: MariaDB (embedded or external) Kuma; a boot-time host rule;
  a dedicated edge-to-app network.

## Acceptance
- Sign-in plan for Kuma says its own login turns off and why it is safe, and
  refuses when the host block is unavailable.
- Apply on: gate on, then SQLite chosen, account created if none, `disableAuth`
  true, Kuma restarted twice at most; nothing secret in argv.
- Apply off: login back on before the gate is removed.
- A Kuma on MariaDB, another version, or without the expected columns is
  refused and unchanged.

## Tests
`internal/appsso/ownlogin_test.go` (version pin, script against a real SQLite file
through a node:sqlite shim: prepare, db-type refusal, column guard, bad stdin,
create/keep account, idempotence, off, version guard; restart only when asked;
username and hash; plan text and refusal), `internal/appsso/forwardauth_test.go`
(`TestKumaForwardApplyOrdersLoginAroundTheGate`: on and off order, stdin only),
`internal/catalog/forwardauth_test.go` (validation, Kuma pin),
`tests/test_s422_uptime_kuma_single_sign_in.py`.

## Built
`catalog/uptime-kuma.yaml`, `internal/catalog/forwardauth.go`,
`internal/appsso/{ownlogin,forwardauth}.go`, ADR-055, tests above.

## Verify live
On VM 2 (container `uptime-kuma-uptime-kuma`, never set up; `<server>` the edge
name, `UK=uptime-kuma-uptime-kuma`).
1. Plan and apply: `homesteadctl apps sign-in plan uptime-kuma`, check the text
   says its own login turns off, then `homesteadctl apps sign-in apply
   uptime-kuma --digest <digest>` and wait for the operation to succeed.
2. Database: `docker exec $UK ls /app/data` shows `db-config.json` and `kuma.db`;
   `sudo sqlite3 <data>/kuma.db "select key,value from setting where key='disableAuth'"`
   prints `disableAuth|true` (or, without sqlite3: `docker exec $UK node -e
   "const s=require('/app/node_modules/@louislam/sqlite3');new s.Database('/app/data/kuma.db').all(\"select key,value from setting where key='disableAuth'\",(e,r)=>console.log(e||r))"`),
   and `select username,length(password) from user` shows one row, hash length 60.
   Confirm the sqlite3 module path exists first: `docker exec $UK ls /app/node_modules/@louislam/sqlite3`.
3. Browser: sign in at Authentik, open `https://<kuma name>/`; the Kuma dashboard
   opens with no Kuma login or setup page. With curl, add an Authentik session
   cookie and check the page loads, or check logs: `docker logs $UK 2>&1 | tail`
   shows "Disabled Auth: auto login to admin" per socket.
4. Gate still holds: `curl -skI https://<kuma name>/dashboard` without a session
   redirects to Authentik; `/metrics` and `/socket.io/` do too; `/status/x`,
   `/api/push/x` and `/icon.svg` stay open.
5. Host block: as a non-root user, `curl -m 5 http://<container ip>:3001/` times
   out; as root it answers. `homesteadctl doctor --live` shows
   `gated-app-host-block` ok. No argv leak: `ps`/`docker inspect` show no hash.
6. Plan refuses without the block: set `firewall_mode=external`, plan sign-in for
   Kuma again; it refuses naming the host block. Restore the mode.
7. Sign-in off: `homesteadctl apps sign-in plan --off uptime-kuma`, apply. Then
   `select value from setting where key='disableAuth'` prints `false`, the Kuma
   login page shows, and the account is kept. To sign in, set a password:
   `docker exec -it $UK npm run reset-password`.
8. Re-apply on: the existing account is kept and `disableAuth` is true again.

## Live results (VM 2, 2026-11-01)

Kuma 2.5.7 had never been set up (no db-config.json, no kuma.db); the
gate (S419) and host block (S421) were already on. Node v22.23.4,
`/app/node_modules/@louislam/sqlite3` present.

- `apps sign-in plan uptime-kuma` listed the login-off and account steps;
  apply ran reach-authentik, configure-authentik, gate, login-off, record,
  all succeeded.
- `/app/data` gained `db-config.json` and `kuma.db`; setting
  `disableAuth` = `true` (type `general`); one user `cjrutherford`, hash
  length 60, active.
- A socket.io polling handshake on the container address got `autoLogin`;
  the Kuma log shows "Disabled Auth: auto login to admin".
- Through the edge with no session, `/dashboard`, `/metrics` and
  `/socket.io/` got 302 to Authentik; `/icon.svg` 200, `/status/x` and
  `/api/push/x` 404 from Kuma (open, unknown slug/token).
- Non-root direct curl to the container timed out; Doctor
  `gated-app-host-block: ok`.
- Off (`plan uptime-kuma --off`, apply): login-on ran before ungate;
  `disableAuth` = `false`, the user row stayed, the socket got
  `loginRequired`, the edge served Kuma's own login (200) with no gate.
  The host block table stayed (Navidrome still gated).
- On again: the existing account was kept, `disableAuth` = `true`,
  `autoLogin`, edge 302 to Authentik.
- Not run: `firewall_mode=external` refusal (unit-tested), owner browser
  sign-in.
- Seen: when the gate is already on and only login-off is pending, the
  plan's first line still reads "Nothing changes: the Authentik gate is
  on…" before listing the login-off step.
