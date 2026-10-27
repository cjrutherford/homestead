# Dashboard on the Local Network through the Edge

## Status
Accepted

In part superseded by ADR-044: apps and Authentik are served under one host by path.

Owner decision 2026-10-19 for S289. Follows
ADR-032 (dedicated unix socket for the edge) for the dashboard and API;
builds on ADR-020 (Operator Session Boundary), ADR-030 (internal CA and
local TLS), ADR-033 (dashboard SSO origins), ADR-006 and ADR-008 (no new
network surface), ADR-015 (secure first claim) and ADR-004 (parity).
None of those decisions change.

## Decision
The owner must be able to open the dashboard from another device on the home
network, for example `https://homestead.lan`, without the tunnel. The daemon
listens on `127.0.0.1:8080` only, and Caddy runs on the `homestead-edge`
bridge network, where host loopback is not reachable. An edge route to
`host.docker.internal:8080` never reaches the daemon, even though
`ssoDefaults` (`internal/server/sso.go`) already counts such a route as a
dashboard origin. The edge reaches the dashboard and API over a **dedicated
unix socket**, as ADR-032 chose for craft files.

### The socket
- A fourth listener, separate from the TCP API, the root-only recovery socket
  (S021) and the craft socket (ADR-032). Path:
  `/run/homestead-dashboard/dashboard.sock` (flag `--dashboard-socket`, pinned
  to that path the way `ValidateCraftSocketPath` pins the craft socket). It
  lives in its own directory, so mounting it exposes neither
  `/run/homestead/homestead.sock` nor `/run/homestead-craft/files.sock`.
- Permissions: directory `0750`, socket `0660`, owner root, group
  `homestead-dashboard` when that group exists (created by
  `scripts/install-systemd.sh`), otherwise group root. Caddy connects as uid 0
  as in ADR-032. The socket is never world-accessible.
- The listener serves the **same mux wrapped by the same ADR-020 boundary** as
  the TCP listener: the same `routeClasses`, session store, cookies, CSRF
  rule, claim-window rule and public allowlist. There is no second route
  table. The recovery route and the craft-file routes are not on the TCP mux,
  so they are not on this socket either.
- A thin front wrapper runs before the boundary, on this listener only. It
  sets the client address and checks `Host` and `Origin` (below). It never
  grants anything the boundary would refuse.
- Stale socket handling, `RuntimeDirectory=... homestead-dashboard` with
  `RuntimeDirectoryPreserve=yes`, and mounting the directory rather than the
  file all follow ADR-032.

### The edge route
- The owner sets a local dashboard name (default `homestead.lan`, owner
  decision; `.lan` matches the app names, and the owner may override it,
  for example with `homestead.home.arpa` or a name under the customer
  domain's split DNS, ADR-007) through the API, `homesteadctl` and the dashboard (ADR-004).
  Homestead then writes one edge route marked as the dashboard route. Its
  snippet is `reverse_proxy unix//run/homestead-dashboard/dashboard.sock`
  with `flush_interval -1` so server-sent events stream, inside the exposure
  policy wrapper (S116).
- The route's exposure is `local` or `overlay` only. TLS is `internal`: the
  leaf is issued by the appliance CA (ADR-030). Setting `public` exposure on
  the dashboard route, or a public TLS mode, is refused by the API and by the
  exposure planner with a message that the tunnel is the public path. The
  TCP listener stays on `127.0.0.1:8080` and the tunnel keeps reaching it
  there.
- The generated Caddy `compose.yaml` bind-mounts `/run/homestead-dashboard`
  read-only at the same path, next to the craft mount.
- Drift is handled as in ADR-032: reconcile fails with a redeploy instruction
  when the running Caddy lacks the mount, `POST /api/v1/edge/deploy` fixes
  it, and a doctor check `edge-dashboard-socket` reports it.

### Client address and forwarded headers
Over the socket every request comes from Caddy, so `RemoteAddr` says nothing
about the real client. The rules:

- Caddy sets `X-Forwarded-For`, `X-Forwarded-Proto` and `X-Forwarded-Host`
  from the connection (`header_up`, replacing anything the client sent).
  Caddy's `trusted_proxies` stays unset, so a client-sent value is never
  passed through.
- On the dashboard socket only, the daemon takes the client address from
  `X-Forwarded-For`. It must be exactly one valid IP address; anything else
  is refused with 400. A socket request is never treated as loopback.
- That address is what every address-keyed check uses: the SSO callback rate
  limiter (`clientHost`), the boundary's rejection audit, login audit and the
  firewall lockout guard (`requestConnection` in
  `internal/server/firewall.go`, which today treats a socket peer as
  loopback). Audit entries for these requests record the forwarded address
  and that they came through the edge.
- On the TCP listener `X-Forwarded-For` is never read, including for tunnel
  traffic. The existing `X-Forwarded-Proto` rule on TCP (it can only make the
  cookie `Secure`) is unchanged.

### Host, origin and CSRF
- On the socket, the request `Host` must equal the configured local name
  (case-insensitive; no port, or `:443`). Other hosts get 421.
- A request that carries an `Origin` must match `https://<local name>`
  exactly. A mutating cookie-authenticated request without an `Origin` is
  refused; safe methods may omit it. No CORS header is ever set.
- `homesteadctl` from another machine works through the local name (owner
  decision): requests carrying `Authorization: Bearer` and no session cookie
  are exempt from the `Origin` requirement, because a browser never attaches
  a bearer token on its own. A request carrying both a cookie and a bearer
  token is refused. The CLI verifies the appliance certificate against the
  exported root (ADR-030); it never disables verification.
- The ADR-020 CSRF token rule applies unchanged. Cookies stay host-only,
  `HttpOnly`, `SameSite=Strict` and `Secure`, so a loopback session and a LAN
  session are separate cookies.
- `https://<local name>` joins the dashboard origins: the SSO defaults and
  pinned redirect URIs (ADR-033) and the post-claim walkthrough (ADR-034).
  `ssoDefaults` recognises the dashboard route instead of a
  `host.docker.internal` target.

### Claim window
The installer creates the default `homestead.lan` dashboard route and deploys
the edge before claim (owner decision), so a new appliance can be claimed from
any device on the LAN. Claim is allowed through the LAN name, with the same classes as on loopback:
public routes, claim-window routes until claim completes, session routes
after. Reaching the claim form does not let anyone claim: the claim token is
in a root-only file (`internal/claim/tokenfile.go`), so the person claiming
must already hold root on the appliance.

### Reachable from other machines at install (amendment 2026-10-19)
Owner requirement: the dashboard must be reachable from other machines on the
LAN as soon as install finishes. A `.lan` name resolves only once the router's
DNS or a hosts entry points at the appliance, so it cannot meet that alone.
The dashboard route therefore answers **two names**:

- the appliance's mDNS name, `homestead.local` today (S221's fixed name,
  `internal/mdns` DefaultHostname; a configurable hostname is in
  `docs/DEFERRED.md`), which the daemon already publishes. It resolves on other machines with no
  DNS setup, from the moment install finishes;
- the owner-set local name (default `homestead.lan`), which works once DNS or
  a hosts entry points at the appliance.

Both names are on the internal-CA leaf, pass the `Host` check, are accepted
`Origin` values, and join the SSO origins and the walkthrough. The installer
output and the claim docs give the `.local` address first. A browser warns on
the first visit until the appliance root is installed on that device: no
public CA issues certificates for `.lan` or `.local`, so this cannot be
avoided, and the Trust page offers the root. If the mDNS port is taken on the
appliance, the S221 rule applies (it fails loudly) and a doctor check says the
`.local` name is unavailable.

## Alternatives considered
- TCP listener on the `homestead-edge` bridge gateway with
  `host.docker.internal:host-gateway`: reachable by every container on the
  bridge, depends on the gateway address and start order, and needs firewall
  review (ADR-008). ADR-032 rejected it for the same reasons.
- Caddy with `network_mode: host`: Caddy could then reach loopback, but it
  leaves the bridge, which breaks every app route by container name and
  changes the edge's whole network model for one route.
- Binding the daemon to `0.0.0.0`: puts the full API on every interface
  without TLS, makes the boundary the only barrier, and contradicts the
  local-first default (ADR-006). Owners who want this can still pass
  `--addr`; Homestead will not recommend it.
- A separate, smaller mux for the socket: two route tables drift, and the
  dashboard needs the whole API anyway.
- Mounting `/run/homestead`: would expose the recovery socket.

## Consequences
- A new listener, a new group, a Caddy mount and a unit file change exist;
  all are generated and inspectable. Apps and the edge still run without
  Homestead; only the LAN dashboard stops.
- Existing appliances redeploy the edge once before the LAN name works.
- The owner must point devices at the name (router DNS or a hosts entry) and
  trust the root certificate; the Trust page and an operations doc say how.
- Address-keyed checks become correct for edge traffic; the firewall guard
  no longer sees edge clients as loopback.
- S289 must test: each route class (public, claim-window before and after
  claim, session with and without cookie, CSRF missing and wrong) through the
  socket gives the same status as through TCP; a forged `X-Forwarded-For` on
  TCP is ignored; a missing, multiple or invalid `X-Forwarded-For` on the
  socket is refused; the rate limiter and audit use the forwarded address;
  wrong `Host` and wrong or missing `Origin` on a mutation are refused; the
  recovery and craft routes 404 on the socket; a public exposure of the
  dashboard route is refused; the compose file mounts the directory
  read-only; the route classification test covers the socket; the SSO
  defaults include the local origin; parity rows for the name setting.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
