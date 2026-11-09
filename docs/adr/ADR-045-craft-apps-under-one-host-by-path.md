# Craft Apps Under One Host, by Path

## Status
Accepted

Accepted by the owner on 2026-10-27 after the VM 2 acceptance run. The
owner saw that only Nextcloud, Jellyfin and Authentik follow
`<server>/apps/<name>`. Publishing a craft app failed with "route.tunnel:
craft app routes are not delivered by the tunnel yet". The owner asked for
the craft apps to follow the same convention and to be deliverable through
the tunnel. Amends ADR-044, where the craft apps stayed `serve: host`, and
ADR-032 for the craft file path.

## Context
ADR-044 put the core apps at `<server>/apps/<name>/` on the dashboard's
own names, and publishes them by plan on the dashboard's public name. The
craft apps were left on names of their own (`cadcraft.lan`), for two
reasons:
- The craft file path (ADR-029, ADR-032) is bound to the craft host.
- The Homestead wrapper page inside the craft image uses absolute paths
  (`/_homestead/open.js`, `href="/"`, `/_homestead/files/`).

Checked on 2026-10-27, all thirteen craft release artifacts load their
assets by relative paths and register no service worker. Each app
therefore runs unchanged under a prefix once the edge strips it.

The wrapper ships inside the signed craft build context: changing it
needs a new signed craft manifest, which only the owner can produce.

## Decision
### 1. Craft packages are served by path
Every craft package declares `serve: path` with `strip_prefix: true`, at
the default `/apps/<name>/`. The image and its signed build context do not
change. The catalog no longer refuses `serve: path` for craft packages.

### 2. The edge forwards the wrapper and the file path to the daemon
On a craft path route, the edge forwards every request under
`<path>_homestead/` to the daemon's craft socket (ADR-032), with the
prefix stripped. This covers the wrapper page and the file path. The edge
sets these headers with `header_up`, replacing anything the client sent:
- the craft app;
- the craft host, which is the name the request came on;
- the route's prefix (`X-Homestead-Craft-Prefix`);
- the scheme.

The daemon serves its own copy of the wrapper page, which uses relative
paths so it works under any prefix. It serves the copy with the same
headers the image's nginx sets, including the CSP. The wrapper copy in
the image stays unused on path routes until the next signed build removes
it. Host-served craft routes, recorded before the move, keep the image's
wrapper.

### 3. The craft session follows the path
- The craft cookie's path is `<path>_homestead/files/`.
- The launch URL is `<path>_homestead/open.html#code=…` on the name the
  dashboard was opened on.
- The launch code is bound to the app and to that name.

The same checks as before apply: same-origin, no CORS, refused preflights,
and refused traversal. The one exception is that the wrapper's static
files may be opened by direct navigation, as the image allowed.

### 4. Publishing is the ADR-044 path plan
A craft app is published like any path app: a plan on the dashboard's
public name, with a digest and an owner approval. The plan adds no DNS
record. The route's exposure stays local or overlay, and the package's
`allowwider: false` still keeps the route itself from widening. The
publish plan warns, as it does for every path app, that the app has no
sign-in of its own in front of it. For a craft app, anyone who reaches it
can use the app, but the file path still needs a Homestead operator
session opened from the dashboard.

### 5. Existing installs
Amended by the owner on 2026-10-27, after the VM 2 live check: existing
craft installs move under the hood instead of waiting for the owner.

When the daemon starts, and once the edge is up, every craft app still on
a name of its own moves to its path. The move changes only the edge
route: the path, the prefix strip and the package's redirects. A craft
app has no settings that name its own address, so there is nothing else
to change. The route keeps its craft app name. The name of its own then
stops answering. The edge is reconciled, and the move is audited as
`craft.move` with the actor `system`. If the edge cannot be reconciled
yet, the move is retried every five minutes.

The ADR-044 move offer stays for the other apps, whose settings change
with their address. Moving a craft app is not a public exposure: the
route stays local or overlay, and publishing it is still a plan the
owner approves (§4).

## Alternatives considered
- **Rewrite the wrapper in the image.** This needs a new signed craft
  manifest and leaves main unable to build craft apps until the owner
  signs it.
- **Deliver craft host routes through the tunnel by names of their own.**
  This does not meet the owner's ask (one host, `/apps/<name>`), and it
  needs one more DNS record per app.

## Consequences
- The craft apps share one web origin with the dashboard and the other
  path apps (accepted for ADR-044). The craft apps are first-party and
  signed, which makes this less of a risk than for third-party apps. The
  craft cookie stays HttpOnly and is scoped by path. Path scoping is not a
  security boundary within one origin; the operator session behind it is.
- The wrapper exists in two places until the next signed craft build. The
  daemon's copy is the one used on path routes.
- Slice: S320.
