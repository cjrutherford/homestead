# S419: Uptime Kuma signs in through the Authentik gate

## Goal
Use S418's forward-auth engine for Uptime Kuma 2.5.7 (ADR-055): Kuma is gated as
a whole, administrators only, with only the push endpoint and public status
pages left open. **Kuma keeps its own login behind the gate** and Homestead
writes nothing into Kuma (owner decision 2026-11-01, ADR-055 §6 amendment).

## Why not login-off
Found live on VM 2: the appliance host reaches any container directly through
the Docker bridge gateway (Docker 26 also gives the host an address on
`--internal` networks) and Kuma needs outbound internet for its monitors, so no
network layout keeps local appliance users away from Kuma's port. With Kuma's
login off any local user would be Kuma's administrator.

## Dependencies
S418 (engine), S420 (edge address pin) and ADR-055 Accepted (done).

## Scope
- `catalog/uptime-kuma.yaml`: `sso.forwardauth` with `access: admin-only`, no
  header (`userheader`, `copyheaders`, `env` all empty) and no `accountsfrom`,
  bypass list from ADR-055 §4: `^/api/push/.*`, `^/status/.*`,
  `^/api/status-page/.*`, `^/api/badge/.*`, `^/assets/.*`, `^/upload/.*`,
  `^/icon\.svg$`, each with its reason. Never bypassed: `/metrics`,
  `/dashboard`, `/socket.io/`, `/status-page`. `allowwider: false` stays
  (status pages are open on the LAN, never public).
- Engine, minimal: `ForwardAuth.Access` (`admin-only` binds only the admins and
  owner groups and creates no users group); an app with no env skips the
  `wire`/`unwire` steps (no container recreate) and the plan says Kuma gets no
  trust settings; plan text names the admin-only audience.
- WebSockets: the gate renders a plain `reverse_proxy` after `forward_auth`
  (Caddy upgrades by itself); a test pins that nothing strips or pins
  Upgrade/Connection.
- Precondition pinned by test: Kuma publishes no host port.
- Dropped from the brief: the `disableAuth` DB write, `kumaSignInVerified`, the
  no-administrator and MariaDB refusals, the restart, the order hook.
- Trust Proxy stays a manual step (DEFERRED).

## Acceptance
- Plan/apply/off on both surfaces for Kuma; only admins/owner pass the gate; a
  user-group person is refused at Authentik.
- Through the gate Kuma shows its own login, then the dashboard; live updates
  work over socket.io.
- `/api/push/<token>` and a status page with its API, badge, assets and uploads
  answer without sign-in; `/metrics`, `/dashboard`, `/status-page` and
  `/socket.io/` are refused without an Authentik session.
- Applying does not recreate or restart Kuma; off restores the plain route and
  removes the Authentik objects.

## Tests
`internal/catalog/forwardauth_test.go` (`access` validation, `TestKumaForwardAuthPin`:
version, admin-only, no header/env, exact bypass, never-bypassed paths, no host
port), `internal/docker/gate_test.go` (`TestGatedKumaRoute`: bypass handles,
gated paths, no copied header, WebSocket passthrough),
`internal/appsso/forwardauth_test.go` (`TestKumaForwardPlan` admin-only bindings,
no users group, no accounts reader; `TestKumaForwardApplyHasNoWiringSteps`),
`tests/test_s419_uptime_kuma_forward_auth.py`.

## Docs
ADR-055 §6 amendment and owner-decision note, `docs/DEFERRED.md`,
`docs/SLICE_INDEX.md`; `docs/PARITY.md` unchanged (no new routes).

## Built
`catalog/uptime-kuma.yaml`, `internal/catalog/forwardauth.go` (`Access`),
`internal/appsso/forwardauth.go` (admin-only groups, no-env steps and text), the
tests above, ADR-055, DEFERRED and SLICE_INDEX lines.

## Verify live
On VM 2 (needs S418/S420 deployed, Authentik with dashboard sign-in and the
owner linked). Install Uptime Kuma from the catalog with its defaults (container
port 3001, local exposure; no inputs, no secrets) and create its administrator.
1. Plan and apply Kuma sign-in (`homesteadctl apps sign-in plan|apply uptime-kuma`).
   The plan lists seven open paths, admins group only, no trust settings.
   Kuma's container is not recreated.
2. Browser to `https://uptime-kuma.lan/`: redirected to Authentik, then Kuma's
   own login, then the dashboard with live monitor updates (socket.io).
3. Without a session: `curl -sk -o /dev/null -w '%{http_code}' https://uptime-kuma.lan/metrics`,
   `/dashboard`, `/status-page` and `/socket.io/?EIO=4&transport=polling` all
   redirect to Authentik (not 200).
4. Without a session: a push monitor URL, a published status page, its
   `/api/status-page/<slug>`, a badge, `/assets/...`, `/upload/...` and
   `/icon.svg` answer 200.
5. A person with the user role is refused by Authentik; an admin passes.
6. `docker ps` shows no host port for Kuma. Stop Authentik: the gated paths
   fail closed, push and status pages still answer. Turn sign-in off: route
   plain, Kuma's own login unchanged.

## Out of scope
Kuma login-off (needs the host firewall rule, DEFERRED), user mapping,
Prometheus scraping through the gate, MariaDB-backed Kuma, Trust Proxy.

## Live results (VM 2, 2026-11-01, 56151b7)
Uptime Kuma 2.5.7 installed local-only, no host port (`3001/tcp` only).
Sign-in plan listed 7 open paths, admins + owner groups only, no trust
settings. Apply succeeded (reach-authentik, configure-authentik, gate,
record; no wire step) and the Kuma container was not recreated.
- `/`, `/dashboard`, `/metrics`, `/status-page`, socket.io polling: 302 to
  the public Authentik authorize URL. For a few seconds right after apply
  they answered 404 (fail closed) until the embedded outpost loaded the new
  provider (DEFERRED).
- `/api/push/*`, `/status/*`, `/api/status-page/*`, `/api/badge/*`,
  `/icon.svg`, `/upload/*`: reach Kuma without the gate.
- `uptime-kuma.local` → 308 to `uptime-kuma.lan`. Doctor `--live`:
  `authentik-outpost: ok` for both gated apps.
- Not yet checked: a browser sign-in (Authentik, then Kuma's setup/login),
  socket.io live updates, a user-role person refused, Authentik down, off.
  Kuma's first-admin setup screen is now behind the gate, so only an
  Authentik admin can claim it.
