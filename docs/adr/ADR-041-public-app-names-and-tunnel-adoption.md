# Public App Names on the Tunnel, and Tunnel Adoption

## Status
Accepted

In part superseded by ADR-044: apps and Authentik are served under one host by path.

The owner decided this on 2026-10-21, for S290.

This record **amends** ADR-037 (§3, §4) and ADR-006: an installed app can
get a public name on the tunnel, and an existing tunnel can be adopted.
Nothing else in ADR-006, ADR-017, ADR-037 or ADR-038 changes unless this
record says so.

## Context
S290 was written before ADR-037. It described a public app route as an
approved *public* edge route (S270) plus a tunnel ingress entry. ADR-037
replaced that model. Public names are now tunnel names on an ordinary local
edge route (`Route.Tunnel`). Caddy serves them only on its loopback tunnel
entry, and the tunnel plan approves them by digest. That plan publishes only
the dashboard and Authentik, and it refuses once a tunnel exists, so:
- an app cannot get a public name;
- a tunnel set up outside Homestead cannot be taken over without tearing
  it down.

## Decision
### 1. A public app name is a per-app plan, approved by digest
- An installed app can get public names under the tunnel's zone,
  `<app>.<domain>` by default. Each name is added to one of the app's local
  edge routes, as a tunnel name.
- **Plan:** `POST /api/v1/catalog/{name}/public/plan` reads Cloudflare and
  changes nothing. It shows:
  - the name, the app and the edge route it serves;
  - the DNS change: a proxied CNAME to the tunnel, created or replacing
    records, as in ADR-037 §3;
  - the connector's ingress before and after;
  - warnings.
- **Approve:** `.../public/apply` carries the digest, with 409 on drift. It
  runs as an operation and is audited (`app.public.add` and
  `app.public.remove`).
- **Removal** takes out the ingress entry, the edge name and the CNAME
  together. The CNAME is deleted only while it still points at the tunnel.
  Records the add replaced are put back, as in ADR-038.
- **This digest is the approval.** No separate exposure approval (S270) is
  needed. Planned, previewed, approved and audited is ADR-006's rule, and
  the plan meets it. Public edge routes with `Exposure: public` (router port
  forwards) keep their S270 approvals.
- **Refused:**
  - no tunnel;
  - the app is not installed;
  - the name is a local name;
  - the name is outside the tunnel's zone;
  - the name is already used by any edge route or tunnel route;
  - the route is not one of the app's routes;
  - the app is Authentik. Its public name belongs to the tunnel plan
    (ADR-037 §4).
- **No forward-auth.** The app's own login, and sign-in with Authentik if
  it is on (ADR-040), protect it. The plan warns about this, and recommends
  turning sign-in on first when the app offers it. When sign-in is already
  on, the new name shows as a sign-in re-plan, and the owner approves that.

### 2. The connector follows the recorded routes
- After any change, the connector config is rendered from the recorded
  tunnel routes: one ingress entry per name, all to the edge's tunnel entry,
  then the `http_status:404` catch-all.
- It is written atomically (a temporary file, then a rename).
- The connector restarts only when the file changed. It is started if it
  was not running.

### 3. Adopting a tunnel is a tunnel plan that takes one over
- The tunnel plan and setup take `adopt: <tunnel id>` instead of creating a
  tunnel. The credential is the Domain step's Cloudflare token
  (ADR-037 §2). No other credential is taken, and the account comes from
  the zone. The run token is fetched from Cloudflare and written to the
  connector's token file.
- **Verified before the plan is offered:**
  - Cloudflare knows the tunnel in the zone's account;
  - the tunnel is configured locally, not from the Cloudflare dashboard;
  - this appliance's connector config names that tunnel;
  - Homestead has no tunnel recorded yet.
  A tunnel with no live connections is a warning, not a refusal.
- **The plan lists every existing ingress entry and what happens to it:**
  - an entry at a name Homestead plans is kept, but now goes through the
    edge;
  - any other entry is dropped. The owner approves the drop.
- **Names and DNS:** the dashboard and Authentik names follow ADR-037 §4. A
  CNAME that already points at the tunnel is kept and recorded as
  Homestead's, so teardown removes it. Other records at a planned name are
  replaced, as at setup.
- **After approval** the tunnel is Homestead's: status, app public names and
  teardown all treat it like one it created. The audit entry is
  `tunnel.adopt`.

## Consequences
- The tunnel carries the dashboard, Authentik and any number of app names,
  all in one zone, all through the edge.
- An adopted tunnel is torn down like any other. Teardown deletes the
  Cloudflare tunnel.
- Uninstalling an app does not remove its public names. They are listed as
  deferred work.
- Apps on public names get no gate from Homestead. A weak app login is
  exposed. The plan says so.
- Tunnels configured from the Cloudflare dashboard cannot be adopted.

## Alternatives Considered
- **An S270 exposure approval as well as the plan:** two approvals for one
  change, and the S270 record describes router port forwards, not tunnel
  names.
- **Keeping unmanaged ingress entries verbatim:** Homestead could no longer
  say what the tunnel publishes. Dropping them with an approved preview
  keeps the plan the whole truth.
- **Authentik forward-auth in front of every public app:** this breaks the
  Jellyfin, Immich and Nextcloud mobile apps, which cannot do a browser
  login. It is deferred.
- **Separate adopt routes:** adoption is the tunnel plan without the
  create step. One plan and one review keep the dashboard and Authentik
  names under the same rules.
