# Nextcloud WebDAV Proxy for Craft Apps

## Status
Accepted

Owner decision 2026-10-14. Extends ADR-020 (Operator Session Boundary) and
builds on ADR-021 (Secret Key Custody and Re-key) and ADR-028.

## Decision
Craft apps (static browser applications, ADR-018 and ADR-028) open and
save documents in the owner's Nextcloud through a **WebDAV proxy inside
`homestead-server`**. The daemon holds a Nextcloud **app password** sealed
in the ADR-021 secret store and performs every WebDAV request to Nextcloud
itself. No Nextcloud credential, app password, or Nextcloud session ever
reaches the browser or a craft container.

Consequence the owner accepted: **Homestead enters the file data path.**
The daemon, not the browser, talks to Nextcloud for document content.

### Design
- **Credential.** On linking, the owner supplies (or the daemon creates
  through Nextcloud's app-password endpoint, when the owner approves) one
  app password for a named Nextcloud account. It is stored as a secret
  (ADR-021), is rotatable through the normal configure plan and approve
  flow, is revocable in Nextcloud, and is never returned by any API.
- **Same-origin, no CORS.** Each craft host's edge route also forwards one
  path prefix, `/_homestead/files/`, to the daemon, so the craft app calls
  its own origin. The daemon sets no permissive CORS headers and answers no
  cross-origin preflight; cross-origin requests are rejected. If the edge
  cannot route a path prefix on a host, S254 adds that capability to the
  edge first.
- **Authz via the session boundary.** The dashboard's "open in craft app"
  action, authenticated by the operator session and CSRF (ADR-020), mints a
  short-lived one-time launch code. The craft host's proxy path exchanges
  it for an `HttpOnly`, `SameSite=Strict`, `Secure`-when-TLS cookie scoped
  to that craft host and path, bound to the operator session, expiring no
  later than it, and revoked with it (logout, password change). The proxy
  accepts no other credential. These routes are a new class in the
  ADR-020 route table, covered by the route-classification test; unknown
  or expired craft cookies get 401.
- **Confinement.** The proxy exposes a fixed subset of WebDAV (`PROPFIND`,
  `GET`, `PUT`, `MKCOL`, `DELETE`, `MOVE`, `COPY`, `HEAD`) under one
  owner-configured Nextcloud root folder. Paths are normalised and any
  traversal outside the root is refused; only the upstream Nextcloud the
  owner linked is ever contacted (no open proxy, no user-supplied URL).
- **Streaming and limits.** Bodies stream in both directions without
  buffering whole files; a configurable maximum upload size (default 512
  MiB), request timeouts, and a per-session concurrency cap apply, and
  exceeding them returns 413 or 429.
- **Audit without content.** Each file operation is audited with actor
  session, craft app, method, normalised path, status, and byte count.
  File content, credentials, and cookies are never logged or audited.
  Reads are audited too, aggregated per path and minute to avoid flooding.
- **Local-only opt-out.** Documents default to Nextcloud once linked; the
  owner may keep any craft app browser-local, and the apps work with no
  link at all.

### Alternatives considered
- Browser-side WebDAV with an app password handed to the page: a
  credential in the browser and craft host; rejected by the owner.
- Nextcloud OAuth with the browser talking directly to Nextcloud: needs
  CORS on Nextcloud, ties craft hosts to Nextcloud configuration, and
  puts a bearer token in the browser.
- Mounting Nextcloud storage into the craft containers: gives static
  containers a credential and a file path, violating ADR-018.

## Clarified 2026-10-17
Owner decision 2026-10-17, no change to the decision: the "craft-side
adapter" is a framing wrapper page served from the craft image that bridges
the unmodified app's own host protocol (or open-by-URL hook) to
`/_homestead/files/` on the same origin. It is within this ADR; the detail is
in `docs/slices/S254-nextcloud-craft-integration.md`.

## Amended by ADR-032
The edge reaches the daemon over a dedicated unix socket that serves only the
craft-file handlers; ADR-032 holds the substance (socket, Caddy mount,
render, Host and Origin trust, drift, ADR-020 class).

## Consequences
- The daemon becomes a file-transfer path: its availability, throughput
  and memory bound document access, so streaming and limits are required,
  not optional.
- Craft containers stay stateless and credential-free; ADR-028 still
  applies and their backups remain stateless no-ops. The documents are
  backed up as part of Nextcloud's own data.
- A compromise of the daemon exposes the sealed app password; the
  mitigation is that it is scoped to one Nextcloud account and revocable
  there, and that ADR-021 key custody applies.
- The edge route set for craft hosts grows one forwarded prefix; drift
  and edge validation must understand it.
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
