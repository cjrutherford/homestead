# Everything SDK: sharing, read links, net and events

## Status
Accepted

Implementation complete (S425-S441, 2026-11-03); live acceptance is the ADR-058 section of `docs/reviews/EVERYTHING_REVIEW_PLAN.md`.

Accepted on 2026-11-03. The owner decided the five points below that day and, in a second round the
same day, chose a bridge-served read-only viewer for public links, automatic
purge on People removal, and accepted every proposed limit. Extends ADR-057, whose "refused" rows
this record replaces once the slices land. Builds on ADR-053 (bridge), ADR-043
(closed-by-default apps, People), ADR-044 (apps by path, path publishing) and
ADR-037 (public exposure is planned, previewed and approved). AI stays
disabled and out of scope.

## Context
ADR-057 refuses `fs.getReadURL`, `kv.share` and friends, `perms`, `appID`,
`events` and `net`, and hides the sharing UI of Tasks, Notes, Circuits and
Parametric with pinned patches. S381 (`api-client`) is deferred for want of
`net`. The owner now wants these capabilities with Homestead semantics. This
record reads the call sites in the pinned upstream archives and settles what
the bridge provides.

What the upstream apps assume of the Puter SDK (from their code and
`dev/puter-mock.js`; the real Puter service was not consulted):
- **Tasks** (`src/lib/store.js`, `collab.js`, `share.js`):
  - `kv.share({prefix, recipient, access, label})` returns `{handle, holder,
    prefix, access, createdAt, revokedAt, appUid}`; `kv.unshare(handle)`;
    `kv.listSharedByMe`/`kv.listShared({limit, cursor})` return `{items,
    cursor}`.
  - A recipient reads and writes with an options argument `{handle}` on
    `kv.get/set/list/del`; keys are relative to the shared prefix.
  - The first share calls `perms.request(perms.Resource.KV_SHARE, {prefix,
    access})`, or the older permission string
    `manage:kv-share-write:<uuid>:<appID>:<prefix>`. `invalid_argument` means
    "cannot ask", and Tasks falls back to links.
  - Recipients are Puter usernames, compared with the sharer's own username.
    Error codes in use: `subject_does_not_exist`, `invalid_recipient`,
    `kv_handle_not_found`, `kv_share_label_invalid`,
    `events_kv_handle_not_delegated`; `kv_handles_disabled`,
    `events_kv_handles_disabled` and `events_disabled` mean "sharing is off".
  - `appID` is used in event subjects and compared with `appUid`.
  - `events.onLocal(subject, cb, {onError})` returns a promise of
    `{off()}`. Subjects: `notif:app-user` (type `kv.share.received`),
    `kv:<appID>:<prefix>*` and `kv:<handle>:*`. A callback event is `{op, self,
    viaWorker}` and `op: 'gap'` means "re-read". Tasks also polls, so events
    are an optimisation for it.
  - The link fallback writes a snapshot file and calls
    `fs.getReadURL(path, '1y')`; the URL must keep serving the file's current
    content. A recipient fetches it with no sign-in.
- **Notes**: `fs.getReadURL(path, '1d'|'7d'|'30d'|'365d')` for a published page;
  the viewer is the app's own page with the URL in the hash.
- **Circuits, Parametric**: `fs.share(path, {username}|{email}, 'read'|'write'|
  'manage')` returning `[{isNew}]`; `fs.unshare(path, recipient)` returning
  `{revoked}`; `fs.getShares(path)` (items with `holder`, `mode`,
  `inheritedFrom`); `fs.listShared({cursor, limit})` (items with `path`, `name`,
  `owner`, `mode`, `modified`). A recipient then calls `fs.read`/`fs.write`
  with the returned `path`. Errors: `user_does_not_exist`,
  `cannot_share_with_self`.
- **api-client** (not in the repository yet) is expected to use
  `puter.net.fetch`. The raw `net.Socket`/`net.tls.TLSSocket` classes exist in
  Puter; no packaged app uses them.

The shell constraint matters everywhere below: the bridge serves
`index.html` only to signed-in sessions and the page policy is
`connect-src 'self'`. A page can fetch only from its own origin.

## Decision
### Owner decisions (binding)
1. **Sharing** (`fs.share`, `kv.share`, `perms.request`, `appID` and received
   shares): recipients are Homestead people who already have access to that
   app. Sharing never grants app access; the owner controls access through
   People (ADR-043).
2. **Read links** (`fs.getReadURL`): anonymous, expiring, read-only. They work
   on local and overlay names. They may also work on the public tunnel name,
   but only after the owner approves an exposure plan for that app's read
   links (planned, previewed, auditable; ADR-037).
3. **net**: un-defer S381. Any person with app access may use it. Public
   internet only; private, LAN, loopback, link-local, CGNAT, multicast and
   metadata addresses are refused after DNS resolution, the connection is
   pinned to the checked address, redirects are re-checked, size and time are
   limited, and every request is logged without secrets or bodies. Then package
   `api-client`.
4. **events**: build it, scoped to what the SDK exposes and the bridge can
   honestly provide.
5. **AI** stays disabled.

### 1. Who is "a person with access to the app"
The bridge cannot ask the daemon (ADR-053: apps run without it). It also
cannot see Authentik's group list. Closed-by-default apps (ADR-043) mean only
people with access reach the bridge's callback at all.

**A person is a valid recipient once they have signed in to that app and
have not been purged.** On every verified sign-in the bridge upserts a
`people` row for (app, `sub`) from the verified ID token: `preferred_username`
and a stable pseudonymous id. The sign-in request adds the `profile` scope
(today only `openid`). A token without `preferred_username` still signs in; that
person simply cannot be a recipient and sees a one-line explanation. If the
sign-in plan passes the app's group names and the token carries a `groups`
claim, the bridge also refuses a sign-in whose claim lacks all of them. That
is defence in depth over Authentik's own binding.

- **Revocation.** The owner removes access in People; Authentik then refuses
  the next authorization. Two gaps remain and are accepted, with mitigations:
  - A live bridge session survives up to its idle (12 h) or absolute (7 d)
    expiry. Mitigation: recipient-side shared operations also require the
    session to be younger than 24 h; otherwise the bridge answers 401 and the
    page re-enters sign-in, which is silent for a person Authentik still
    allows and refused for one it no longer allows. Revocation latency for
    sharing is at most 24 h.
  - The bridge never learns of a removal by itself, so removal triggers a
    **purge** (next bullet). Purge revokes the person's sessions, grants (as
    owner and as recipient) and read links, closes their event streams and marks
    the row. Their data stays, exportable.
- **Automatic purge (owner decision).** When the owner removes a person's
  access to an app in People (ADR-043), Homestead purges that person in every
  installed Everything app.
  - **Bridge side:** the existing tool,
    `everything-bridge people purge --app <app> (--subject <sub> | --username
    <name>)`. It opens the SQLite file directly, so it is the same command a
    plain-Compose owner runs by hand with `docker compose exec`. Streams and
    sessions notice because they re-read the database on each event, heartbeat
    and request.
  - **Daemon side:** the daemon runs that command in the app's container
    (`docker exec`, through the supervised Docker client it already uses). No
    new listener, port, credential or admin endpoint is added to the bridge.
    The person's `sub` is read from Authentik by the daemon, with the username as
    fallback.
  - **Failure:** if the container is not running, the purge is stored as
    pending in the daemon's SQLite and retried when the app next starts and on
    a timer; the People result and the audit entry (`people.purge`) say
    "pending" until it succeeds. Removal itself never waits on it.
  - **Alternative rejected:** an authenticated admin endpoint on the bridge. It
    adds a network surface and a shared secret, and the bridge would need a
    second authentication mode.
- **Compared with asking the daemon:** immediate and exact, but the bridge
  would need a callback channel and a credential for the daemon, and stops
  working with the daemon off. Rejected (ADR-053).
- **Compared with reading the `groups` claim only:** it shows the same fact as
  signing in (Authentik already refused others), is stale in the same way,
  and cannot list people who have not signed in. Used only as the extra check
  above.
- **Compared with a username directory pushed from the daemon:** a second
  source of truth that goes stale. Rejected.

### 2. Names
- `auth.getUser()` returns the person's **own** `preferred_username` and a
  stable per-app uuid (`p_` + base32 of an HMAC of `sub` under a key stored in
  the bridge database; `sub` itself is never sent to a page). Today it returns
  "Homestead user". Tasks compares the typed name with its own, so the own
  name must be real.
- A person sees **others' names only** where a share links them: the sharer
  sees the recipients they chose; the recipient sees the owner's name on
  received items. There is **no directory and no typeahead**: a sharer types a
  username (`@` is stripped). A name that is unknown, purged, or belongs to
  someone without access to this app answers the same error
  (`user_does_not_exist` for fs, `subject_does_not_exist` for kv), so the
  answer reveals nothing the sender could not learn by asking the person.
  Share attempts are limited (60 per hour per person per app).
- Email recipients (`{email}`) are refused with `invalid_recipient`; the bridge
  holds no email address.
- The appliance owner sees every name in `export` and the tool listings.

### 3. Share data model
New tables in the same bridge SQLite file (backed up with it):

```
people(app, subject, username, username_key, uuid, first_seen, last_seen,
       purged_at, PRIMARY KEY(app, subject),
       UNIQUE(app, username_key) WHERE purged_at IS NULL)
grants(id, app, owner_subject, recipient_subject, kind /* fs|kv */,
       object_key, is_prefix, access /* read|write */, label,
       created_at, updated_at, revoked_at)
       -- unique live grant per (app, owner, recipient, kind, object_key)
```

- `id` is 128 random bits, URL-safe. It is the kv `handle` and the fs shared
  path's first segment. It is a name, not a capability: every operation also
  proves the caller is `recipient_subject` in a valid session.
- **Rows are not copied.** A recipient's operation is rewritten to the owner's
  (app, subject, kind, key) after checking the grant. The owner's rows stay the
  single copy.
- **kv.** `{handle}` on `kv.get/set/list/del` resolves to the owner's prefix;
  keys are relative to it. `access: 'read'` refuses writes. Prefix must match
  the app's allowed shape (`kv.share` takes a trailing-`*`-free prefix, as
  today's list rule). Listing is bounded as ADR-057's list rules.
- **fs.** `fs.share(path, {username}, mode)` grants the exact file or, for a
  folder, its prefix. `listShared` returns `path` = `~shared/<id>/<relative
  path>`, `owner` (username), `mode`, `name`, `modified`. `fs.read/write/stat/
  readdir` on a `~shared/...` path resolve through the grant. `~shared` is
  a reserved first segment that normal writes reject. A file grant allows read
  or overwrite, never delete, rename or re-share; a folder grant with `write`
  allows create, overwrite and delete inside it. `manage` is refused with
  `bridge_unsupported` (no re-sharing; slices patch the option out of the UI).
- **Quota.** Recipient writes count against the **owner's** person and app
  quotas, since the bytes live in the owner's scope. A trusted writer can fill
  the owner's quota; a per-grant byte cap is deferred. Each owner may hold at
  most 200 live grants per app.
- **Revoke.** `kv.unshare(handle)`, `fs.unshare(path, recipient)`: sets
  `revoked_at`; later recipient operations answer `kv_handle_not_found` or
  `subject_does_not_exist` and the item leaves `listShared`.
  `listSharedByMe` still lists revoked items with `revokedAt` (Tasks filters).
- **Inert, not deleted.** A grant whose owner or recipient is purged (or whose
  recipient's session is older than the 24 h recheck) cannot be used.
- **Backup/export/import.** Backup copies the whole database, grants included.
  Export format bumps to `homestead-everything-export/2`: adds `people` and
  `grants` without sessions, tokens, hashes, keys or logs; import still reads
  `/1` and, for `/2`, restores grants only for people already present.
- **Errors and fallbacks.** `events_kv_handle_not_delegated` is never emitted.
  A deployment with sharing off (an app definition without `sharing`) emits
  `kv_handles_disabled`, which Tasks treats as "use links".

**perms.request.** It maps to the sharing permission and shows no prompt.
`perms.Resource = {KV_SHARE: 'kvShare'}`. A request is validated (kvShare with
an allowed prefix and `read|write`, or the legacy string whose uuid and appID
equal the caller's) and resolves `true` with no state. Anything else rejects
`invalid_argument`. The share click in the app is the consent. A bridge-drawn
confirm was considered and rejected: it lives in the same origin as the app's
script, so it cannot protect against that script, and it adds a prompt to every
first share. **appID** is the constant string `app-<app>` from the app
definition, the same for every person.

### 4. Read links
- **Token.** 32 random bytes, base64url, shown once. Stored as SHA-256 only,
  in `read_links(id, app, owner_subject, object_key, token_hash, created_at,
  expires_at, revoked_at, last_used_at, hits)`. The URL is
  `<origin><base>/_homestead/read/<token>`; no file name or owner in it.
- **Meaning.** The link names one **file**, not a copy: it serves the file's
  current content (Tasks depends on this). `getReadURL(path, expiresIn)`
  accepts `'1d'` style strings or seconds; default 7 days, **cap 365 days**
  (owner-lowerable per app by environment), minimum 1 minute. No
  non-expiring links.
- **Revocation.** Deleting, renaming over, or recursively deleting the file
  revokes its links permanently, so "Stop sharing" in Tasks and Notes works and
  re-creating the file does not revive an old link. Purge and the tool
  (`everything-bridge links list|revoke`) revoke the rest. At most 10 live
  links per (owner, path) and 500 per owner per app; beyond that
  `getReadURL` fails with `read_link_limit`.
- **Route.** `GET|HEAD <base>/_homestead/read/<token>`. It ignores cookies and
  `Authorization`, sets none, and answers a uniform 404 for unknown, expired,
  revoked and malformed tokens (no oracle). Response headers:
  `Content-Type` from a fixed allowlist by file extension else
  `application/octet-stream`; `X-Content-Type-Options: nosniff`;
  `Content-Security-Policy: sandbox; default-src 'none'`;
  `Content-Disposition: attachment` for everything except `application/json`
  and `text/plain` (those inline, so the app can `fetch` them);
  `Cache-Control: private, no-store`; `Referrer-Policy: no-referrer`;
  `Cross-Origin-Resource-Policy: same-origin`. No CORS headers (see Origin
  below). No byte ranges; size cap equals the stored-object cap (8 MiB).
- **Rate limit.** Per client address (the slice must establish how the edge passes the
  real address and trust it only from the edge hop): 60
  requests per minute, and 600 per minute per app. Unknown tokens cost double.
  A miss never reveals the owner.
- **Logging.** Never log the token or URL path of the route. The access log line
  records the link `id`, status and byte count.
- **Origin rule.** A page can `fetch` only its own origin (`connect-src
  'self'`), and the apps pack the read URL into a link to the app itself. So
  `getReadURL` returns a URL on **the origin the caller is using**, taken from
  the request after the same allowlist check as CSRF:
  - on a local or overlay name: always allowed;
  - on the public name: allowed only when the owner has approved read-link
    exposure for that app; otherwise it fails with `read_links_not_exposed`
    and a message saying so.
- **Public exposure plan (daemon, with CLI and dashboard parity).** Modelled on
  ADR-044 path publishing and ADR-037:
  - `POST /api/v1/apps/<app>/read-links/plan` reads state and changes
    nothing. It returns the public URL pattern
    (`https://<public host>/apps/<app>/_homestead/read/*` and
    `.../_homestead/view/*`), what stays refused
    (every other path of the app, which stays 404 unless the app itself is
    published), the live link count read from the bridge tool, the expiry cap,
    warnings (anyone holding a link can read that file until it expires; links
    are bearer secrets) and a digest.
  - `POST /api/v1/apps/<app>/read-links/approve` carries the digest; the daemon
    plans again and refuses 409 on drift; the change is an operation, audited
    (`readlinks.expose`), and teardown (`readlinks.unexpose`, audited) removes
    the route.
  - The edge (Caddy) routes **only** those two path patterns on the public host
    to the app, with no gate and no other path. The app is never exposed by
    this plan. The bridge itself is told by an
    environment value (`HOMESTEAD_EVERYTHING_PUBLIC_READ_ORIGIN`, set through
    Compose, so a daemon-less restart keeps it) which public origin may mint
    links.
  - The tunnel gains no new hostname; the existing public host carries it.
  - CLI and dashboard use the same API (AGENTS.md); `docs/PARITY.md` gains
    rows.
- **What an outsider gets: a bridge-served viewer (owner decision).**
  `GET|HEAD <base>/_homestead/view/<token>` uses the **same token, expiry,
  revocation, rate limit and uniform 404** as the raw route, and renders the
  linked file as a plain page. The raw route stays for machines (for example a
  calendar client reading a Tasks `.ics` feed, served as `text/calendar`
  inline; the Content-Type allowlist gains `.ics`). The viewer is not the app:
  no app shell, session or script is involved, which is why the public name
  exposes only `read` and `view`.
  - **Policy:** `default-src 'none'; style-src 'sha256-<hash of the fixed
    stylesheet>'; img-src 'none'; base-uri 'none'; form-action 'none';
    frame-ancestors 'none'`, plus `nosniff`, `no-store`, `no-referrer`. No script
    at all, no external requests, no images.
  - **Rendering is by file kind**, chosen from the JSON `kind` field (else the
    extension). Every string is HTML-escaped by the renderer before anything
    else happens.
    - `tasks.shared-list` snapshot: list name, owner, updated time and tasks
      with title, notes (escaped text, line breaks kept), due date and time,
      priority, tags, subtasks and done state, ordered as the owner's manual
      order. Fields the snapshot does not carry are not shown.
    - Notes `notes-shared-page`: title and each outline's text as escaped
      text with line breaks kept; headings, lists and emphasis only through a
      tiny server-side subset renderer that works on already-escaped text; links
      only for `http:` and `https:` with `rel="nofollow noopener noreferrer"`.
      Images, ink, embeds and anything else are replaced by a visible
      "not shown in this viewer" marker. It is a reading view, not a copy of the
      page.
    - A file of any other kind: a page that says there is no viewer for it
      (the raw route still works). The viewer never guesses and never
      sniffs.
  - **Changes to the apps:** the link an app shows its user must be the viewer
    URL (`/view/` with the same token) instead of the app-with-hash link, so the
    per-app slices patch the link construction (pinned, with a test for the
    patch target). Signed-in people can open it too.
  - **Honest limits:** the viewer shows a snapshot of text; formats beyond
    the two above need their own renderer slice.

### 5. net
- **Surface.** `puter.net.fetch(url, init)` only, returning a `Response`.
  `net.Socket`, `net.tls.TLSSocket`, WebSocket and wisp URLs stay
  `bridge_unsupported`. HTTP and HTTPS only; no streaming bodies.
- **Transport.** One CSRF-protected same-origin data operation (`net.fetch`)
  with JSON `{url, method, headers, body}` (body base64). The reply is
  `{status, statusText, headers, body}` (body base64); the bridge script builds
  the `Response`. Request body at most 2 MiB; response body at most 8 MiB
  (truncation is an error, not silent); 10 s to connect, 30 s total.
- **Who.** Any person with a valid session; the app definition must list
  `net` in its surface (only `api-client`).
- **Address fence (after resolution, every hop).**
  1. Parse the URL; scheme http or https; no userinfo; host not empty.
  2. Resolve the host once (system resolver). Take every A/AAAA answer.
  3. **If any answer is blocked, refuse** (fail closed; no picking the
     public one). Blocked: IPv4 `0.0.0.0/8`, `10/8`, `100.64/10` (CGNAT, which
     also holds Alibaba metadata), `127/8`, `169.254/16` (link-local, AWS and
     GCP metadata), `172.16/12`, `192.0.0.0/24` (Oracle metadata), `192.0.2/24`,
     `192.168/16`, `198.18/15`, `198.51.100/24`, `203.0.113/24`, `224/4`,
     `240/4`; IPv6 `::`, `::1`, `fc00::/7` (ULA, AWS IPv6 metadata),
     `fe80::/10`, `fec0::/10`, `ff00::/8`, `64:ff9b::/96`, `2001::/32`,
     `2001:db8::/32`, `2002::/16`, `100::/64`. IPv4-mapped/compatible IPv6 is
     unwrapped and judged as IPv4. IP literals take the same check, so the
     check does not rely on spelling.
  4. **Pin.** Dial the checked address directly (`ip:port`), trying the
     checked answers in order; TLS verifies against the original host name with
     the system roots, TLS 1.2 minimum, no `InsecureSkipVerify`. A dialer
     `Control` callback re-checks the actual remote address at connect time.
     Proxy environment variables are ignored.
  5. **Redirects**: at most 5; each hop repeats steps 1 to 4; https to http is
     refused; `Authorization` and `Cookie` are dropped across origins.
  6. Ports `25, 110, 143, 465, 587, 993, 995` are refused (mail abuse).
- **Requests.** The bridge adds its own `User-Agent`; drops `Host`, `Cookie`,
  `Proxy-*`, `Forwarded`, `X-Forwarded-*`, hop-by-hop headers; has no cookie jar
  (responses' `Set-Cookie` is not returned). Response headers returned are an
  allowlist.
- **Limits.** Per person: 60 requests a minute, 5 concurrent, 5,000 a day.
  Per app: 20 concurrent. Over the limit: `429` mapped to `net_rate_limited`.
- **Logging.** One persistent row per request in `net_log` (kept 30 days or
  10,000 rows): time, app, person pseudonym, method, scheme, host, port,
  path **without the query string** (keys often live there), resolved address,
  status, bytes in and out, duration, error class. Never headers or bodies.
  Listed by `everything-bridge net-log`. The daemon surfaces it in a later
  slice; the tool is enough for ADR-053 recovery.
- **Defence in depth** (deferred): a host firewall rule limiting the bridge
  container's egress (ADR-008, S421). The in-process fence does not depend on it.

### 6. events
- **Surface.** `events.onLocal(subject, cb, {onError})` returning `{off()}`
  (what Tasks uses), plus `events.on` as an alias. Honest scope: the bridge
  can say what happened in **this app's own stores for this person**:
  - `notif:app-user` with `{type: 'kv.share.received' | 'kv.share.revoked' |
    'fs.share.received' | 'fs.share.revoked'}` for the signed-in recipient;
  - `kv:<appID>:<prefix>*` (own prefix) and `kv:<handle>:*` (a shared
    handle): `{op: 'set'|'del', key, self}`, never values;
  - `{op: 'gap'}` when events were dropped or the stream resumed.
  Not provided: other apps' events, `fs` change events (recipients use
  `listShared`), anything about people outside a grant. Unknown subjects
  reject `bridge_unsupported`.
- **Authorisation.** Subscribing to a subject uses the read rule of its
  operation: own prefix for the owner; an active grant for the recipient.
  Subscriptions are re-checked on every event delivery and die on revoke.
- **`self`.** True only for the page instance that made the write: each page
  sends a random client id with data calls and the stream, so a second tab is
  told about the first tab's write.
- **Transport (within `connect-src 'self'`).** `GET <base>/_homestead/events?
  client=<id>`, a Server-Sent Events stream with the session cookie, an
  exact `Origin`/`Sec-Fetch-Site` check and no state change. Subscribing and
  unsubscribing are CSRF-protected POST data operations (`events.subscribe`,
  `events.unsubscribe`). Long-polling was considered and rejected: it holds
  the same connection with worse latency. WebSocket needs a path upgrade the
  edge does not carry today.
- **Limits.** 5 streams per person, 50 subscriptions per stream, a 256-event
  queue (overflow collapses to one `gap`), a heartbeat every 25 s, a stream
  closed after 1 h or when the session ends or goes stale (the client
  reconnects and re-validates). Events are in memory, one hub per bridge
  process; a restart sends `gap`. Everything still works by polling, which
  Tasks already does. The edge must not buffer the stream (`flush_interval -1`);
  the slice verifies it and falls back to polling otherwise.

### 7. Page policy and SDK surface
- The page policy does not change (`connect-src 'self'` covers `fetch`,
  `EventSource` and the read URL when same-origin).
- ADR-057's surface table is replaced as the slices land: `sdk_surface` in each
  app definition gains `sharing`, `read_links`, `events` and (api-client
  only) `net`. Anything not listed keeps failing closed with `bridge_unsupported`.
- The pinned patches that hide sharing UI are removed one app per slice, with
  a test that the patch target is gone and the control works against the real
  bridge.
- New data operations and routes need the existing origin, CSRF and
  content-type checks; the read route and the SSE stream are the only
  non-POST additions.

## Threat model
| Threat | Defence |
| --- | --- |
| Reading or writing another person's data | Scope is always derived from the session `sub`; shared access resolves a grant bound to the caller's `sub`. The handle is a name, not a capability. Tests: a second person, guessed handles, wrong app, wrong kind, revoked, purged. |
| Sharing used to gain app access | Recipients must already exist in `people`, created only by a verified sign-in. The bridge never creates an Authentik user, group or session. |
| Stale access after removal | Authentik refuses new sign-ins; recipient-side sharing needs a session under 24 h; purge tool ends the rest. Automatic purge (S427) ends owner-side shares and read links; residual: the window while a purge is pending. |
| Username enumeration | No directory; one uniform error; rate limit. A sharer can only learn that a name has or has not signed in to this app. |
| SSRF through `net` | Post-resolution fence on every address and hop, pin, mapped-address unwrapping, mail ports, no proxy, no cookies, no internal DNS answers (container names resolve into blocked ranges). Tests include rebinding with a resolver that changes answers, redirect to 127.0.0.1 and to metadata, IPv6 forms, and decimal/hex hosts. |
| Abuse of `net` as a proxy or scanner | Per-person and per-app rate and concurrency limits, size and time caps, log of host and port, mail ports refused. A person with app access can still reach the public internet from the appliance's address; the owner accepted this by choosing the capability. |
| Secrets in logs | Queries, headers, bodies, tokens never logged; read route paths not logged; keys sent by `api-client` are not stored. |
| Link leakage | Bearer tokens: random, hashed at rest, expiring, `no-store`, `no-referrer`, uniform 404, revoked on file delete and purge. Unavoidable: someone holding a link reads that file until it expires. The plan says so. |
| Abuse of the public read route | Only the `read` and `view` paths are routed publicly, GET/HEAD, size cap, rate limit, no listing, no way to name an owner or path, uniform 404, no CORS, `sandbox` CSP and `nosniff`, `attachment` for active types so a hostile upload cannot run script on the app's origin. |
| Stored XSS via shared content or the viewer | Same page policy as today; the viewer escapes every string first and its policy has no script, no images and a hashed stylesheet; shared files are data read through the SDK; read responses are sandboxed. |
| CSRF on new operations | All new operations are POST data operations with the exact `Origin`, `Sec-Fetch-Site` and `X-Homestead-CSRF` checks. The SSE stream only reads and requires same-origin. The read route is anonymous by design and changes nothing except a hit counter. |
| Resource exhaustion | Caps on grants, links, streams, subscriptions, request and response sizes; quotas charge the owner. |
| Cross-app mixing | Every table is keyed by app and every query filters on it. |

## Alternatives considered
- **Query the daemon or Authentik for access:** rejected (daemon independence;
  the bridge holds no management credential).
- **Copy data into the recipient's scope:** simpler reads, but two copies,
  broken write-back for Circuits and Parametric ("their saves update this
  file"), and double quota. Rejected.
- **Email or free-form recipients:** the bridge has no emails and would be
  creating access. Rejected.
- **Directory with typeahead:** nicer, but discloses everyone who signed in to
  every person. Rejected for v1.
- **Signed (stateless) read URLs:** cannot be revoked and cannot be expired
  early. Rejected.
- **Read links public by default, or one switch for the whole appliance:** the
  owner required a per-app, previewed and approved plan.
- **Always return the public origin for read links:** breaks `connect-src
  'self'` for pages opened on the local name. Rejected.
- **net as an owner-only feature:** the owner chose any person with access.
- **A network allow-list per app:** the owner chose public internet with a
  fence; an allow-list may be added later.
- **Raw sockets, WebSocket for net:** no packaged app needs them. Rejected.
- **WebSocket or long-poll for events:** SSE chosen (above).
- **Bridge-drawn consent for `perms.request`:** rejected (above).
- **Anonymous app shell, or route-only links, for outsiders:** the owner chose
  a separate script-free viewer, so the app is never exposed.

## Slice plan
Strictly sequential, one commit each, after this record is accepted. IDs
S424 and up were unused in `docs/SLICE_INDEX.md` and `docs/SLICE_PLAN.md`.

| ID | Slice |
| --- | --- |
| S424 | This record (docs only). |
| S425 | People registry: `profile` scope, `people` table from verified claims, own `getUser` username and uuid, optional group check, `people purge` tool. |
| S426 | Share grants store: `grants` table, resolution, owner-quota charge, export `/2` and import, `grants list|revoke` tool. Go only; no SDK surface. |
| S427 | Automatic purge: the daemon runs `people purge` in each installed Everything app's container when People removes access; pending retry, audit `people.purge`, API, CLI, dashboard, PARITY. |
| S428 | KV sharing in the bridge: `kv.share/unshare/listShared/listSharedByMe`, `{handle}` option, `perms.request/Resource`, `appID`; error codes; definition flag `sharing`. |
| S429 | FS sharing in the bridge: `fs.share/unshare/getShares/listShared`, `~shared` paths for read, write, stat and readdir. |
| S430 | Events: hub, SSE stream, `events.subscribe/unsubscribe`, `events.onLocal`, share notifications, edge flush check. |
| S431 | Read links on local names: `read_links` table, raw route, headers, rate limit, `getReadURL`, revoke on delete, `links` tool; verifies how the edge passes the client address. |
| S432 | Read-only viewer: `/_homestead/view/<token>`, strict script-free policy, renderers for Tasks list snapshots and Notes pages, `.ics` content type. |
| S433 | Read-link exposure plan: daemon plan/approve/teardown for the `read` and `view` routes only, audit, edge route, bridge origin variable, API, CLI, dashboard, PARITY. |
| S434 | net fence: address classifier, pinned dialer and redirect checker as a tested library (rebinding and redirect tests). |
| S435 | net operation: `net.fetch` data operation, limits, per-person and per-app rate limits, `net_log`, `net-log` tool, SDK shim. |
| S436 | Package `api-client` (S381, un-deferred 2026-11-03): app definition, pin, catalog, sign-in, tests. |
| S437 | Tasks: remove the sharing patches; enable KV sharing, link fallback to the viewer link, events. |
| S438 | Notes: enable read links with the viewer link. |
| S439 | Circuits: enable sharing. |
| S440 | Parametric: enable sharing. |
| S441 | Review plan and acceptance docs (`docs/reviews/EVERYTHING_REVIEW_PLAN.md`) for the new surface; ADR-057 table refresh. |

## Accepted limits (owner, 2026-11-03)
Read links: 7-day default, 365-day cap. Recipient-side sharing rechecks the
session every 24 h. `net`: 60 requests a minute and 5,000 a day per person,
mail ports refused. The unknown-username error is uniform. No question is
open; S431 verifies how the edge passes the real client address before the
rate limit relies on it.

## Consequences
- The bridge gains a people, grants and read-link model and a network client;
  this is the largest widening of ADR-053's boundary so far, and each
  capability has its own slice, tests and rollback (remove the surface flag).
- ADR-057's "refused" rows become "supported per app" as slices land.
- `auth.getUser` now reveals a person's own username to their own page.
- Daemon-free recovery is unchanged: every new table is plain SQLite, readable
  with `sqlite3`, and the tools run without the daemon.
- Removing a person in People purges their grants and links in every Everything
  app (S427). Until it succeeds (the container may be stopped) it is shown as
  pending, and a session can still last up to 24 h for shared operations.
  Plain Compose keeps the manual purge command.
- Nothing here is verified in a browser or on a live appliance; the
  implementing slices own that evidence.
