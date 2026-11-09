# Dashboard sign-in with Authentik (S286, ADR-033)

The owner can sign in to the dashboard with the Authentik that runs on the
appliance. The local `owner` account is permanent and is the break-glass
path: it cannot be removed, disabled or made dependent on Authentik, and
local password login works whether or not Authentik is reachable.

Code: `internal/sso` (OIDC verification, the Authentik admin API, the
manager), `internal/server/sso.go` (routes), `internal/server/boundary.go`
(route classes and the step-up list), `internal/auth` (session method,
fresh password check), `dashboard/src/app/sso` (pages), `cmd/homesteadctl/sso.go`.

## The flow
1. **Set up (once).** Sign-in settings page or `homesteadctl sso plan`,
   `approve`, `apply`: Homestead plans the OAuth2/OpenID provider and the
   application in Authentik, the owner approves the plan by its digest, and
   apply runs as the operation `sso.apply`.
2. **Link (once).** The owner re-enters the local password; the daemon makes a
   single-use link intent (10 minutes, bound to `state`, not to the Strict
   session cookie) and the browser runs the OIDC flow asking Authentik for a sign-in no older than 30 seconds (`max_age=30`; `prompt=login` loops in Authentik 2026.8, see the KVM review).
   The callback records the pair `(issuer, sub)`. Exactly one link exists.
3. **Sign in.** The login page shows "Sign in with Authentik" beside the
   password form when the daemon says it is set up and linked (local state
   only; Authentik is never probed to decide). `GET /api/v1/sso/login`
   redirects to Authentik; `GET /api/v1/sso/callback` finishes and mints the
   ordinary ADR-020 session (HttpOnly cookie, CSRF token, 12 h idle, 7 d
   absolute, same revocation) tagged `oidc`, then redirects to
   `/login?sso=done`, where the dashboard learns who signed in and goes on.

Authorization code with PKCE (S256), `state` and `nonce`. Homestead is a
confidential client: it generates the client secret, seals it as
`sys_sso_client_secret` (ADR-021) and puts it in Authentik. The secret is
never in YAML, plans, audit entries, responses or exports.

## What Authentik is told
`POST` to the admin API (`/api/v3/...`, the version pinned by the catalog's
Authentik image tag, 2026.8.x):
- provider **Homestead**: OAuth2/OpenID, `client_type` confidential,
  `client_id` `homestead`, strict redirect URIs, scope mappings `openid`,
  `profile`, `email`, a named signing key (RS256; default
  `authentik Self-signed Certificate`), `sub_mode` `hashed_user_id`,
  `issuer_mode` `per_provider`, authorization flow
  `default-provider-authorization-implicit-consent` and invalidation flow
  `default-provider-invalidation-flow` (stock flows, only referenced);
- application **Homestead** (slug `homestead`) bound to the provider.

Group bindings (ADR-043 §2, ADR-050 §3): the application is bound to
`homestead-owner` and `homestead-people` only once the owner group has a
member. Before the owner is linked no group is bound at all, because any
binding would make Authentik refuse the unlinked owner at the **Link**
step ("Policy binding 'None' returned result 'False'"). Homestead's own
`(issuer, sub)` and active-person checks still refuse everyone else in
the meantime. The re-plan after linking (the tunnel setup runs one) adds
both bindings.

Users, groups, flows, policies and every other application are never touched.
Apply is idempotent: it finds the provider by name and the application by
slug, changes only what differs (including a drifted redirect URI or client
type) and reports "unchanged" otherwise. Drift is shown on re-plan, not fixed
behind the owner's back. Disabling does not delete the objects in Authentik
(the token is gone); delete them there if wanted.

The API token: given at plan (optionally, to read what exists, in memory only)
and at approval, sealed as `sys_sso_authentik_token`, used by apply and
**forgotten after a successful apply** unless the owner ticked "keep" (or
`--keep-token`). A failed apply keeps it so a retry needs no retyping. The
review states plainly that an Authentik token can do anything an Authentik
administrator can.

## Addresses
- **Issuer.** `<Authentik address>/application/o/homestead/`, fixed at
  configuration time; a discovery document whose `issuer` differs is refused,
  and every endpoint in it must sit on the issuer's origin. The Authentik
  address defaults to its route in the edge table (host `authentik.*` or
  `*-authentik.*` to the `server` service), for example
  `https://authentik.lan`. The owner can edit it in the plan.
- **Back channel.** The daemon reaches Authentik the way the Nextcloud link
  (S254) does: the appliance CA (ADR-030) is trusted, and a name in the edge
  route table is dialled on `127.0.0.1` at the URL's port with TLS and SNI
  carrying the route host, so the token exchange and JWKS fetch never leave
  the machine. Discovery is read again each time a sign-in starts, so a dead
  Authentik is found before the browser is sent there.
- **Browser side.** The authorize URL is whatever Authentik's discovery
  publishes on the same issuer host: the `.lan` name the owner's browser can
  reach (the device must trust the Homestead root, Trust page).
- **Redirect URIs.** `<origin>/api/v1/sso/callback` for each dashboard origin
  in the plan, as exact strings (strict mode, no wildcards). Defaults come
  from the appliance: `http://127.0.0.1:<port>`, edge routes that point at the
  daemon port, and tunnel hostnames whose service is the daemon
  (`https://<hostname>`). The owner edits them in the plan. At sign-in the
  request's `Host` is matched against the planned origins' hosts and the
  scheme comes from the plan; a `Host` that is not planned is refused
  (`origin`), and `X-Forwarded-*` and `Forwarded` are never read. Plain
  `http` origins are accepted only for loopback.

## Verification of the ID token
Standard library only (no OIDC dependency was added). JWKS signature with
RS256 or ES256 (P-256) only: `none`, HMAC and anything else are refused;
exact `iss`; `aud` containing the client id, `azp` equal to it when present or
when there are several audiences; the stored `nonce`; `exp` required, `iat` and
`nbf` honoured, all with a 2 minute skew. The JWKS is cached and refreshed
once when a `kid` is unknown (at most every 5 seconds). `state` is random,
single use (consumed with `DELETE ... RETURNING`), valid 10 minutes, and for
login also tied to a short-lived `HttpOnly`, `SameSite=Lax`,
`Path=/api/v1/sso` cookie (`Secure` for https origins). The PKCE verifier and
nonce stay in the server-side record.

## Operations: setting up sign-in with Authentik
1. Install Authentik through the catalog wizard (S285) and finish its first-run
   setup; create an Authentik API token (Admin, Tokens) for the owner.
2. `homesteadctl sso plan --token-file tok` (or Settings, Sign-in, Plan). The
   plan lists the OAuth2 provider and application to create or change, the
   exact redirect URIs, and what is left untouched. Review it.
3. `homesteadctl sso approve --digest <digest> --token-file tok [--keep-token]`.
   The approval is bound to the digest; a changed plan needs a new approval.
4. `homesteadctl sso apply --follow`. Idempotent; the client secret is
   generated and sealed. The token is forgotten unless `--keep-token`.
5. `homesteadctl sso link` (prompts for the local password) prints a URL; open
   it in a browser and sign in to Authentik. Or use Account, Sign in with
   Authentik, Link.
6. Sign out; the login page now offers "Sign in with Authentik".
Recovery: see Break-glass and recovery below. The local password always works.

## Break-glass and recovery
- Local password login is unchanged: five failures in ten minutes lock for 15
  minutes. A locked local account does not block SSO, and SSO success does not
  clear local failures. Re-authentication (below) feeds the same counters.
- **Authentik is down.** Nothing to do: the login page still loads, the
  button leads back with "Authentik did not answer. Use your local password.",
  and the password form works.
- **Authentik is lost, mis-upgraded, uninstalled or compromised.** Log in with
  the local password and run `homesteadctl sso disable` (or Turn off sign-in on
  the Sign-in page). It removes the configuration, the link, any pending flow
  state and both sealed secrets, and ends all `oidc` sessions. With the
  daemon's own recovery path (`homesteadctl admin recover`, root only) a lost
  local password can be reset first. Uninstalling Authentik alone makes the
  link dormant and hides the button; reinstalling brings it back.
- **Suspected compromise of Authentik.** Anyone who controls Authentik can
  mint a token for the linked subject. Unlink (fresh password) or disable;
  both revoke `oidc` sessions immediately; then review the audit trail for
  `sso.login` entries.
- **Replacing the Authentik user.** A recreated user has a new `sub` and no
  access (`unlinked`); unlink and link again.

## Step-up for `oidc` sessions
An `oidc` session must present the local password again, in the request
header `X-Homestead-Reauth`, for the routes in `stepUpRoutes`
(`internal/server/boundary.go`, tested exactly):

| Route | Checked by |
|-------|------------|
| `POST /api/v1/sso/link`, `DELETE /api/v1/sso/link` | handler, for **every** session (link and unlink always need a fresh password) |
| `POST /api/v1/sso/plan`, `POST /api/v1/sso/approve`, `POST /api/v1/sso/apply`, `DELETE /api/v1/sso` | boundary, `oidc` sessions |
| `POST /api/v1/secrets/rekey`, `POST /api/v1/backups/identity/export` | boundary, `oidc` sessions |

A missing password answers 403 with `X-Homestead-Error: reauth-required`, a
wrong one 403 `reauth-invalid`, a locked account 429 `reauth-locked` (never
401, so the dashboard does not read it as a dead session). The dashboard's
re-authentication interceptor shows one dialog and retries the request with
the password; the CLI sends the header for `sso link` and `sso unlink`.
Password sessions keep today's behaviour on the boundary routes.

## Routes
Public (ADR-020 allowlist, only these three; each is 404 before claim and
unless sign-in is configured and linked): `GET /api/v1/sso/options`
(`{"enabled":bool}`; `false` at an address the plan does not cover),
`GET /api/v1/sso/login`, `GET /api/v1/sso/callback`. The one nuance: the
callback that completes the *first* link arrives while nothing is linked yet,
so a live link intent for its `state` also opens the callback.
Session class: `GET /api/v1/sso`, `POST /api/v1/sso/plan|approve|apply|link`,
`DELETE /api/v1/sso/link`, `DELETE /api/v1/sso`. Failures return to
`/login?sso_error=<code>` (or `/sign-in?...` for a link) with one of
`unavailable`, `state`, `denied`, `token`, `unlinked`, `rate-limited`,
`not-configured`, `link-conflict`, `origin`.

## Audit
`sso.plan`, `sso.approve`, `sso.apply` (and `sso.apply.authentik`,
`sso.apply.operation`), `sso.token`, `sso.link.intent`, `sso.link`, `sso.unlink`,
`sso.login` (success with `method=oidc` and the subject, refusals with the code),
`sso.reauth`, `sso.disable`, and `sso.rate-limited` (the flow routes allow 30
requests a minute; the first refusal and the window's count are audited, not
every one). No entry holds a token, a code, a client secret or a cookie.

## Security notes
The link URL is a bearer credential: whoever completes it at Authentik within its 10 minutes becomes the linked subject, because the intent is bound to the admin and the state, not to the owner's browser (binding it is a hardening item in DEFERRED.md). Sign-in rate limiting is per client address (the peer address the daemon sees, never `X-Forwarded-For`).

## Apps (S298, ADR-039 §3, ADR-040)
Immich, Nextcloud and Jellyfin can sign people in through the same
Authentik. Each app gets its own OAuth2 provider (`Homestead: <title>`) and
application (slug and client ID `homestead-<app>`), confidential, with the
app's redirect URIs on every local and overlay name it has:

| App | Adapter | Redirect URIs | Sign-in address |
| --- | --- | --- | --- |
| Immich | `immich` | `/auth/login`, `/user-settings` | `/auth/login` |
| Nextcloud | `nextcloud-user-oidc` | `/apps/user_oidc/code`, `/index.php/apps/user_oidc/code` | `/login` |
| Jellyfin | `jellyfin-sso-plugin` | `/sso/OID/redirect/authentik` | `/sso/OID/start/authentik` |

The app reaches Authentik by its local name: the service gets an
`extra_hosts` entry to the host gateway and the appliance root CA mounted
read-only at `/etc/homestead/ca` (Immich trusts it through
`NODE_EXTRA_CA_CERTS`, Jellyfin through `SSL_CERT_DIR`, Nextcloud through
`occ security:certificates:import`). The client secret is sealed as
`sys_sso_app_<app>_secret` and reaches the container only on stdin.
Jellyfin needs the third-party SSO plugin, installed at the pinned release
after its checksum matches. The app's own accounts keep working.

Turning sign-in on or off is a plan, approved by digest, run as an
`app.sign-in` operation. When the app's names change, the status says so and
the next plan updates the redirect URIs. Routes (session class):
`GET /api/v1/catalog/{name}/sign-in`, `POST .../sign-in/plan`,
`POST .../sign-in/apply`. Audit: `app.sign-in.operation`,
`app.sign-in.authentik`, `app.sign-in.on`, `app.sign-in.off`.

## Not in this slice
CLI sign-in with Authentik, RP-initiated logout, more than one link, SSO for
non-owner administrators and disabling local password login: see DEFERRED.md.
