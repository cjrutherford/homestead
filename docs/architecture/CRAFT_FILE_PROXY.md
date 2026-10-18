# Craft File Proxy (S254, ADR-029, ADR-032)

Craft apps (GridCraft, CADCraft, PdfCraft, FilmCraft, EffectCraft) can open
and save documents in the owner's Nextcloud. The daemon holds a sealed
Nextcloud app password and does every WebDAV request itself; no Nextcloud
credential reaches a browser or a craft container. Homestead is in the file
data path, so authorisation, limits, streaming and audit are part of the
design, not follow-ups.

## Flow

1. **Link** (owner, once). `homesteadctl craft files link`, the Craft files
   dashboard page, or `POST /api/v1/craft/files/plan` then `/approve`. The
   owner supplies the Nextcloud address, an account name, a root folder
   (default `Homestead`) and an app password, or approves the daemon creating
   one through Nextcloud's `ocs/v2.php/core/getapppassword` using the account
   password once (not kept). The daemon verifies the credential
   (`PROPFIND` of the account's file space), creates the root folder, seals
   the app password as `sys_craftfiles_nextcloud` (ADR-021) and records the
   non-secret fields in SQLite (`craft_file_link`) with an inspectable mirror
   `craft-files.yaml` beside `desired.yaml`. Rotation and unlink are planned
   and approved the same way; a daemon-created app password is revoked in
   Nextcloud on rotate and unlink, an owner-supplied one is not (the plan
   says so).
2. **Launch** (owner session). "Open with Nextcloud files" on a craft app
   (`POST /api/v1/craft/files/launch`, operator session plus CSRF) mints a
   one-time launch code, bound to that operator session, the craft app and
   the craft host, valid for 60 seconds. It is returned in a URL fragment:
   `https://<craft host>/_homestead/open.html#code=...`. Fragments are never
   sent to a server.
3. **Exchange** (craft host). The wrapper page reads the fragment, removes it
   from the address, and `POST`s it to `/_homestead/files/session`. The edge
   forwards that path to the daemon. A valid code yields the craft cookie
   `homestead_craft`: `HttpOnly`, `SameSite=Strict`, `Secure` when the route
   is HTTPS, host-only, `Path=/_homestead/files/`. Codes are one-time
   (consumed even when refused), expire, and are rejected for any other
   craft host or app.
4. **Use**. The wrapper frames the unmodified app and bridges it to
   `/_homestead/files/f/<path>` (below). Every request carries the cookie; no
   other credential is accepted.

## Session binding and revocation

A craft session is held in daemon memory (hash of the cookie value only; a
daemon restart ends them and the owner relaunches). It stores the operator
session token it is bound to and **re-verifies that operator session on every
request** through the same `auth.Service.Verify` the boundary uses. Logout,
password change (every other session), recovery, idle or absolute expiry all
delete the operator session, so the next craft request gets 401 and the craft
session is dropped. A craft session also expires at its own TTL (8 hours) and
never later than the operator session's expiry at the time of exchange. If
the operator session store cannot be read the proxy answers 503 (fail
closed). Unknown, expired, revoked or forged cookies get 401.

## Where the proxy lives (ADR-032)

The proxy is **not** on the TCP listener. The daemon opens a third listener on
`/run/homestead-craft/files.sock` (flag `--craft-socket`), directory `0750`,
socket `0660`, group `homestead-craft` when it exists (created by
`scripts/install-systemd.sh`), serving only the craft-file handlers; the
recovery socket directory is never mounted anywhere. The Caddy stack
bind-mounts `/run/homestead-craft` read-only. A route that names a craft app
(`Route.CraftApp`, set by catalog install plans and inferred for hooked apps
in older tables) renders:

```
gridcraft.lan {
	tls internal
	handle /_homestead/files/* {
		reverse_proxy unix//run/homestead-craft/files.sock {
			header_up X-Homestead-Craft-App gridcraft
			header_up X-Homestead-Craft-Host gridcraft.lan
		}
	}
	handle {
		reverse_proxy gridcraft:8080 ...
	}
}
```

inside the route's exposure-policy wrapper, so a policy that restricts origins
restricts the file path identically; a deny-all policy forwards nothing.

### Trusting Host and Origin behind the edge

Only Caddy can connect to the socket, and Caddy overwrites the two
`X-Homestead-Craft-*` headers from the route (`header_up` replaces client
values), so they identify the craft app and host. The daemon additionally
requires: the request `Host` equals the craft host (421 otherwise); any
`Origin` is exactly `<scheme>://<craft host>` with the scheme from
`X-Forwarded-Proto`, which Caddy sets itself; any `Sec-Fetch-Site` is
`same-origin`; state-changing methods carry `Origin` or `Sec-Fetch-Site`;
`OPTIONS` is never answered; no `Access-Control-*` header is ever set. Paths
with `.` or `..` segments, backslashes, NUL or doubled slashes are refused
with 400 before routing. A request without the edge headers (a direct socket
connection) is 400. Every route on the listener is classified
`craft-session` in `internal/server/boundary.go`; the listener refuses a route
that is not (the classification test covers both muxes).

### Restart behaviour

The daemon removes a stale socket and recreates it on start. Caddy mounts the
directory and dials per connection, so it follows the new socket without a
restart. The unit therefore has `RuntimeDirectory=homestead homestead-craft`
and `RuntimeDirectoryPreserve=yes`; without Preserve, systemd would delete
the directory on stop and the bind mount would point at a deleted inode.
A craft socket failure only warns: craft apps stay browser-local.

### Drift and redeploy

An edge deployed before S254 lacks the mount. Reconciling a route table with
a craft route inspects the running Caddy container after the reload and
fails with "redeploy the edge" if the mount is missing; the routes are
already live, so the apps keep working. `homesteadctl edge deploy`
(`POST /api/v1/edge/deploy`) rewrites the compose file and recreates the
container. Doctor reports it as `edge-craft-socket` (also when the socket is
missing).

## The proxy

`/_homestead/files/f/<path>` supports exactly `PROPFIND`, `GET`, `PUT`,
`MKCOL`, `DELETE`, `MOVE`, `COPY` and `HEAD` (every other method is 405).
All of it is confined to the one owner-configured root folder: paths are
normalised and any traversal is refused, `MOVE`/`COPY` destinations are
mapped under the same prefix, and the only host ever contacted is the linked
Nextcloud (no user-supplied URL; redirects are never followed and become
502). Request headers are an allowlist (`Content-Type`, `If-Match`,
`If-None-Match`, `Overwrite`, `Range`, ...); cookies, `Authorization`,
`Origin` and `X-Forwarded-*` are never forwarded, and the daemon adds its own
Basic credential. Responses forward `ETag`, `Last-Modified`, `Content-Length`
and range headers only; `GET` bodies are forced to
`application/octet-stream` with `Content-Disposition: attachment` and a
sandbox CSP so a stored HTML file is data, never a page on the craft origin.
`PROPFIND` replies (small, buffered up to 8 MiB) have the upstream layout
(`/remote.php/dav/files/<account>/<root>`) rewritten to the proxy prefix.
Nextcloud rejecting the stored credential (revoked app password) becomes a
clear `502` with `X-Homestead-Upstream: auth-rejected`; the upstream's own
text is never relayed.

Bodies stream in both directions (`io.Copy`; `PUT` is passed to the upstream
as a stream with the client's length, or chunked). No whole-file buffering.

### Limits

| Setting (`/api/v1/config`, `craft_files_*`) | Default | Effect |
|---|---|---|
| `craft_files_max_upload_mb` | 512 | larger `PUT` is refused with 413, by `Content-Length` before contacting Nextcloud, or mid-stream for chunked bodies (the upstream request is aborted) |
| `craft_files_timeout_minutes` | 30 | one request's end-to-end bound; exceeded is 504 |
| `craft_files_per_session` | 8 | concurrent requests per craft session; more get 429 with `Retry-After` |

### Transport to Nextcloud

The link requires an `https://` address. The daemon refuses `http://` unless
the host is this machine (`localhost`, `127.0.0.0/8`, `::1`) or the plan
carries the owner's explicit `allow_insecure_http: true` (CLI
`--allow-insecure-http`, a dashboard checkbox); that plan carries an
`INSECURE` warning first, and rotating such a link needs the opt-in again.

**The appliance's own Nextcloud.** The documented link address is
`https://nextcloud.lan` (the package's edge route, `tls internal`, S280).
The host does not necessarily resolve its own LAN names (the LAN resolver
and mDNS answer other machines; `/etc/hosts` and the system resolver know
nothing of `.lan` routes), so the upstream client does not rely on DNS for
them: a linked host that is in this appliance's edge route table is dialled
on the local edge, `127.0.0.1` at the URL's port, while TLS (SNI and
certificate verification) and the `Host` header keep the route name. Names
outside the table resolve normally. The client (`Upstream`) trusts the
Homestead internal CA root (ADR-030) in addition to the system roots and
rebuilds when the CA rotates, so the certificate verifies with no `insecure
skip verify` anywhere. An exposure policy on the route applies to this
request like any other, and it arrives from the loopback address: a policy
that admits only LAN sources makes the link fail with a 403 until the owner
links `http://127.0.0.1:<published port>` instead. If the Nextcloud route is
plain `http` (TLS off), link that same way or turn the route's TLS on.

Nextcloud answers 400 to a `Host` it does not trust. A fresh Nextcloud
install therefore gets `NEXTCLOUD_TRUSTED_DOMAINS` set from the planned route
hostname (the catalog `{{route_host}}` environment token; the image applies it
once, when it first installs). A Nextcloud installed before this change, or
one served under another name later, needs the name added by the owner:
`docker exec -u www-data nextcloud-nextcloud php occ config:system:set
trusted_domains 1 --value=nextcloud.lan`. The link plan reports the 400 with
that instruction.

The edge reaches an app only on the `homestead-edge` network. Installs since
S249 attach routed services to it; an older install lacks it, the edge
answers 502 for the route and the link reports that. `homesteadctl doctor`
flags it as `edge-upstream-networks` (reinstall the package, or `docker
network connect --alias <route target> homestead-edge <container>`; the
alias must be the route's target name, and the attachment does not survive
a recreate).

### Responses

Upstream non-2xx answers (other than the handled 401 and redirects) are not
relayed: the browser gets a fixed `text/plain` line with the status, and every
proxy response carries `X-Content-Type-Options: nosniff`. The scheme the
handler trusts is `X-Forwarded-Proto`, which the edge sets with `header_up`
from the route's own TLS mode (anything else is refused). While a request
runs, its craft session is re-verified every 30 seconds, so logout or a
password change cancels in-flight transfers; a password change also revokes
the changer's own craft sessions and pending launch codes. `--craft-socket`
must be the one path the edge mounts.

### Audit

Each write (`PUT`, `MKCOL`, `DELETE`, `MOVE`, `COPY`) is audited as
`craftfiles.file` with actor (operator username), craft app, a short
non-secret session id, method, normalised path (and, for `MOVE`/`COPY`, the
normalised destination), status and byte count. Requests refused before the
proxy ran (no or unknown cookie, bad launch code) are audited by actor
`unauthenticated`, method, normalised path and status only, aggregated per
minute. An aggregated read keeps the first non-2xx status. Reads
(`GET`, `HEAD`, `PROPFIND`) are aggregated per session, path and minute and
flushed with a count. File content, the app password, cookies and launch
codes are never logged or audited, and error bodies are the daemon's own
short messages. Link, rotate, unlink, plan, launch and opt-out changes are
audited as `craftfiles.*` (names and ids only).

## The wrapper page

`images/craft/homestead/open.html|open.js|open.css` ship in the craft image
(inside the signed build context digest) and are served by nginx at
`/_homestead/open.html` with a strict CSP (`default-src 'none'`, same-origin
script, style, connect and frame only, `frame-ancestors 'none'`). nginx does
**not** serve `/_homestead/files/` (404) and has no proxy configuration. The
wrapper frames the unmodified app and listens only to `message` events whose
`source` is its own frame and whose `origin` is its own origin.

| App | Hook (pinned release) | Wrapper behaviour |
|---|---|---|
| gridcraft 0.7.0, cadcraft 0.7.0, pdfcraft 0.8.0 | framed at `/?host=parent`; app posts `<app>:ready {version}`, `<app>:dirty {dirty}`, `<app>:save {name, bytes}` (pdfcraft adds `saveAs`), `<app>:failed {error}` with `bytes` a transferred `ArrayBuffer`; parent posts `<app>:open {name, bytes}` with `bytes` an `ArrayBuffer` or `Uint8Array` | open from a file list, save writes the file back (`If-Match` the opened ETag, `If-None-Match: *` for a new file, a confirmation on 412), unsaved-changes guard |
| filmcraft 0.8.0 | `window.filmcraft.importUrl(url, name)`, `openProject(url)` (`.fcproj`) | open only: the app fetches the same-origin URL itself; saves stay in the browser |
| effectcraft 0.10.0 | `window.effectcraft.addFile(urlOrFile, name)` | open only, as above |

WordCraft has no open hook (saving would need `showSaveFilePicker`
patching, rejected as fragile) and the other seven apps have none; they stay
browser-local. The vendored apps are not modified. `?host=parent` starts the
app with an empty workbook or document until the parent opens one.

A per-app **local-only** opt-out is desired configuration (`craft_file_local_only`,
mirrored in `craft-files.yaml`): launch refuses and the proxy answers 409 for
that app. Unlinked apps, and visitors who open `https://<app>.lan/` directly,
keep working browser-local exactly as before.

## Backup and statelessness

Craft packages remain stateless backup no-ops (ADR-028). Documents are
backed up with Nextcloud's own backup (S182). The link's non-secret state is
in SQLite and `craft-files.yaml`; the sealed app password follows the secret
store's backup rules (ADR-022).

## Operations

- Link state: `homesteadctl craft files status`, the Craft files page.
- Edge missing the mount: `homesteadctl edge deploy --follow`, then
  `homesteadctl doctor`.
- Revoked app password in Nextcloud: the proxy answers 502
  `auth-rejected`; rotate with `craft files rotate`.
