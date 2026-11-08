# One Shared Authentik Gate for Apps at a Subpath

## Status
Accepted

Decided by the owner on 2026-11-08. Amends ADR-055 (forward-auth for apps
at a path), ADR-061 (the thirteen craft apps go from `publish: never` to
`publish: path`, behind the gate) and ADR-045 (a craft app may now be
published, only behind the gate). Leaves ADR-043 (per-app groups), ADR-037
and ADR-060 as written.

## Context
Authentik's forward-auth is keyed by external host: a proxy provider in
single-application mode matches a request by the host it arrived on. ADR-061
put Navidrome at `/apps/navidrome/` on the dashboard's names, so Navidrome's
provider now holds the dashboard host. A second gated app at a path could not
have a provider of its own on the same host (DEFERRED, S472).

The owner wants the craft apps (the 13 in `catalog/*craft*.yaml`) publishable
at `https://homestead.<domain>/apps/<app>/`, behind Authentik. The craft apps
have no sign-in of their own (ADR-045, S320), so publishing one without a
gate would put it on the internet open.

## Decision
### 1. One gate, on the dashboard's name
Every forward-auth-gated app at a path shares ONE Authentik proxy provider
(single application, embedded outpost) and one Authentik application, both
named for the gate: provider `Homestead: apps gate`, application slug
`homestead-apps-gate`. Its external host is the gate's name (section 5).
The provider holds the union of every app's bypass regexes. The application
has no group bindings: Authentik says who the person is, and nothing else.
An app at a path that is gated does not get a provider of its own. Uptime
Kuma is a host app (ADR-055), keeps its own provider and is never published.

### 2. The edge decides who may open each app
After the forward-auth subrequest succeeds, the edge checks the outpost's
`X-Authentik-Groups` header against the app's groups (ADR-043: the app's
users group, its admins group, and the shared owner group; for an
administrators-only app, admins and owner). A person who is signed in and in
none of them gets `403`, with a short plain body. A request with no groups
header is refused too (fail closed).

- **Header trust.** Before anything else, the route deletes every
  client-supplied `X-Authentik-*`, `Remote-User` and `X-Forwarded-User`
  header, in every handle (bypass handles included). Only the outpost's
  answer sets `X-Authentik-Username` and `X-Authentik-Groups`
  (`copy_headers` replaces, never appends). The group check and the app run
  inside one Caddy `route`, so the order is the one written, not the
  directive sort. After the check, `X-Authentik-Groups` is removed from the
  request: an app receives only the headers its manifest asks for.
- **Exact group matching.** The groups header is `a|b|c`. The check is a
  regex bounded by the start, a `|` and the end: `(^|\|)(g1|g2)(\||$)`,
  with each group name quoted. A group that contains or extends an allowed
  name never matches (`homestead-navidrome-users-old`). Group names are plain
  (`[A-Za-z0-9][A-Za-z0-9_.-]*`), validated when the route is saved.
- **The outpost path handle.** `/outpost.goauthentik.io/*` is reverse-proxied
  to the embedded outpost once, in the dashboard site, on the local entry
  and on the tunnel entry (with `X-Forwarded-Proto https`), because the
  sign-in redirect and callback land on the gate's name. Gated apps do not
  render it each; the dashboard site renders it once.
- **Bypass regexes per app.** Each manifest writes its bypass paths for the
  app's own root; at plan time they take the app's path prefix
  (`^/apps/<app>/rest/.*`), are rendered in the app's handle, and are
  unioned into the shared provider's unauthenticated paths. They are
  anchored, narrow, and never match the outpost's paths (unchanged
  `ValidateBypass`). A bypass keeps the app's own authentication and carries
  no identity. Turning one app's gate off removes only its paths from the
  provider; the provider is deleted with the last app.

### 3. Per-app access is unchanged
The People page grants (ADR-043) add a person to the app's groups in
Authentik, exactly as today. The edge reads those groups on every request, so
a change applies on the next request. The shared Authentik application binds
no group, so Authentik no longer refuses a person with no app access: the
edge does, with 403. The Authentik "my applications" list shows the gate
application to anyone; it is not a launcher (the dashboard apps page is,
ADR-050).

### 4. Navidrome moves onto the gate
Navidrome (S472) has a provider and application of its own, bound to its
groups. Re-planning its sign-in (`apps sign-in plan navidrome`, then approve
and apply) moves it: the plan names every change; apply first deletes the
app's own provider and application (two providers on one external host would
be ambiguous; the gate fails closed for that moment), then creates or updates
the shared provider and application, keeps the app's groups and their
members, and the edge route gains its groups. The gate revision changes, so
Doctor shows the drift until the move is applied. Navidrome's Subsonic
bypass, its trust headers and its own accounts are untouched. Turning the gate
off reverses it.

### 5. The gate's name
Authentik matches one host, so all gated apps at a path answer on one name:
the dashboard's public name once any gated app at a path is published and the
dashboard has one, otherwise the dashboard's own (LAN) name. The other names
of the dashboard answer a `308` to it, as in S472. An app behind the gate that
is not published while the gate is on the public name has no valid LAN
address; it must be published to be used. Craft apps do not meet this case
in S481, which gates their public path only (section 6).

### 6. Craft apps (S481)
The 13 craft manifests change `publish: never` to `publish: path`. A craft
route that is published is rendered behind the shared gate on the tunnel
entry; the plan preview says so. The craft app's groups are created and its
people grants work as for any app. The craft wrapper, file path and prefix
headers (ADR-045) are unchanged, and the group check runs before the
request reaches the daemon's craft socket or the app.

**LAN access to craft apps stays open, as ADR-045 and S320 left it.** The
owner's decision covers publishing; it did not say that LAN visitors now sign
in to a craft app, and that would change behaviour for every installed craft
app. This ADR keeps the LAN as it is. **Owner decision, 2026-11-08 (S481): keep
the LAN open.** The gate of a craft app is tunnel-only (`Gate.TunnelOnly`):
the local names render as before, the public name renders the gate. A published
craft route without its gate answers 404 on the tunnel, so publishing first and
gating second never opens the app. The gate is planned only once the app is
published, because the shared provider's external host is the public name.

### 7. Plan preview and audit
Publishing and gating stay planned, previewed, approved and audited
(AGENTS.md). The sign-in plan (`app.sign-in`, digest-bound) lists: the shared
provider and application (created or reused), the provider's bypass paths
and which apps keep them, the groups the edge admits, the 403 for everyone
else, the removal of the app's own provider (Navidrome), and that
client-sent identity headers are deleted. The operation audits each step
(`app.sign-in.authentik`, `app.sign-in.on`, `app.sign-in.off`). The public
path of a craft app is published by the ADR-044 path plan, which now also
says that the app is behind the Authentik gate and who is admitted.

## Consequences
- One provider serves every gated path app. Adding the next one is an edge
  change plus a provider update, not a new Authentik host.
- Authentik no longer refuses a signed-in person who has no access to an
  app; the edge does. Authentik's own audit shows the sign-in; Caddy shows
  the 403. A person with no app access still gets an Authentik session.
- The gate fails closed: no outpost, no groups header, or no matching group
  means no access.
- Authentik group names reach the edge in a header. A group renamed in
  Authentik by hand stops matching until the sign-in is planned again.
- A craft app stays open on the LAN (owner decision, S481); only its public
  path is gated. If the owner later wants a LAN sign-in, the gate's name would
  have to cover the LAN host and every craft LAN link would become a sign-in.

## Slices
S480 shared gate and Navidrome moved onto it; S481 craft `publish: path`
behind the gate; S482 live acceptance of the subpath work, absorbing S478.
