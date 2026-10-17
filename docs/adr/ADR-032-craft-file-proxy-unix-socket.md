# Craft File Proxy Reaches the Daemon over a Dedicated Unix Socket

## Status
Accepted

Owner decision 2026-10-17. Amends ADR-029 (Nextcloud WebDAV Proxy for Craft
Apps) by choosing how the edge reaches the daemon; builds on ADR-020
(Operator Session Boundary), ADR-006 and ADR-008 (no new network surface).
The ADR-029 decision is otherwise unchanged.

## Decision
ADR-029 forwards `/_homestead/files/` on each craft host from the edge to the
daemon. The daemon listens on `127.0.0.1:8080` only, and Caddy runs in a
container on the `homestead-edge` bridge network, where host loopback is not
reachable. No existing pattern routes the edge to a host process. The owner
chose a **dedicated unix socket** over a TCP listener on the bridge gateway.

### The socket
- A third listener, separate from the TCP API and from the root-only recovery
  socket (S021). Path: `/run/homestead-craft/files.sock` (flag
  `--craft-socket`). It lives in its own directory so that mounting it never
  exposes `/run/homestead/homestead.sock` (the recovery socket).
- Permissions: directory `0750`, socket `0660`, owner root, group
  `homestead-craft` when that group exists (created by
  `scripts/install-systemd.sh`), otherwise group root. Caddy runs as uid 0 in
  its container (no user namespace remap), which is the socket owner, so it
  connects without the group; an owner who runs the edge unprivileged adds
  the group to the Caddy service with `group_add`. The socket is never
  world-accessible.
- The listener serves **only** the craft-file handlers (a launch-code
  exchange and the WebDAV proxy under `/_homestead/files/`). Its mux has no
  other route: every other path is 404. The full API is never on it, and the
  craft-file routes are never on the TCP listener.
- The daemon removes a stale socket and re-creates it on every start. The
  Caddy stack mounts the **directory**, not the socket file, so a recreated
  socket is seen without restarting Caddy, and Caddy dials per connection so
  no stale connection survives. The systemd unit therefore sets
  `RuntimeDirectory=homestead homestead-craft` with
  `RuntimeDirectoryPreserve=yes`: without Preserve, systemd would delete and
  recreate the directory on a stop, leaving Caddy's bind mount pointing at a
  deleted inode. `ReadWritePaths` needs no change (the runtime directory is
  writable under `ProtectSystem=strict`).

### The Caddy stack
- The generated `compose.yaml` bind-mounts `/run/homestead-craft` into the
  Caddy container at the same path, **read-only** (connecting to a socket on a
  read-only mount is allowed; nothing is written).
- A route may name a craft app (`Route.CraftApp`, set by the catalog for craft
  packages). For such a route only, the rendered snippet puts
  `handle /_homestead/files/* { reverse_proxy unix//run/homestead-craft/files.sock { ... } }`
  **ahead of** the `handle` that proxies to the nginx upstream, inside the
  exposure policy wrapper so the origin policy (S116) governs the file path
  exactly as it governs the app. Caddy sets `X-Homestead-Craft-App` and
  `X-Homestead-Craft-Host` (`header_up`, replacing anything the client sent)
  from the route itself.
- Non-craft routes render byte-for-byte as before.

### Trusting Host and Origin behind the edge
Requests reach the daemon only through Caddy (socket permissions). The craft
listener trusts the two `X-Homestead-Craft-*` headers because only Caddy can
connect and Caddy overwrites them from the route. It additionally requires the
request `Host` to equal the craft host and, for any request that carries an
`Origin`, that origin to be exactly `<scheme>://<craft host>` (scheme from the
edge's `X-Forwarded-Proto`); a missing `Origin` is accepted only on safe
methods. Cookies are host-only and `SameSite=Strict`, bound to one craft app
and host, so a cookie minted for one craft host is refused on another.
Preflights (`OPTIONS`) and cross-origin requests are rejected; no CORS
header is ever set.

### Drift and redeploy on existing appliances
Appliances deployed before this change run a Caddy container without the
mount. Reconciling a route table that contains a craft route verifies, after
the reload, that the running Caddy container has the mount and fails with an
instruction to redeploy the edge when it does not. `POST /api/v1/edge/deploy`
rewrites the stack and recreates the container (idempotent). A doctor check
(`edge-craft-socket`) reports a craft route served by an edge missing the
mount. The mount itself is part of the generated stack, so it appears in
every fresh deploy.

### ADR-020 classification
`routeClasses` gains a class **craft-session** for the routes on the craft
socket mux. The craft listener enforces it: no credential or an unknown,
expired or revoked craft cookie gets 401; the one exchange route is the only
route that accepts a launch code instead of a cookie. The route-classification
test enumerates the craft mux as well as the TCP mux and fails on an
unclassified route in either. The management routes (link, unlink, rotate,
launch-code minting) stay on the TCP listener as ordinary session routes with
CSRF.

## Alternatives considered
- TCP listener on the `homestead-edge` bridge gateway with
  `host.docker.internal:host-gateway`: reachable by every container on the
  bridge, depends on the gateway address and start order, and needs firewall
  review (ADR-008).
- Proxying from the craft nginx container: rejected by the slice (nginx
  would hold a route to the daemon) and has the same reachability problem.
- Mounting `/run/homestead`: would expose the recovery socket to the edge.

## Consequences
- A new listener and a Caddy compose change exist; both are generated and
  inspectable, and the apps and edge still run without Homestead (the mount
  of an absent directory is harmless; only the file path stops working).
- Existing appliances must redeploy the edge once to use file storage; until
  they do the apps stay browser-local and the reconcile says so.
- The unit file changes (`RuntimeDirectory`, `RuntimeDirectoryPreserve`).
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
