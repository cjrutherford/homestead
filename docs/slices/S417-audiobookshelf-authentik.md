# S417: Audiobookshelf signs in through Authentik

## Goal
Turn the `env-oidc` adapter on for Audiobookshelf 2.37.1 beside its local
login (ADR-052 break-glass rule), so `homesteadctl apps sign-in plan|apply
audiobookshelf` works. S417 implements the planning note
`docs/slices/S385-audiobookshelf-authentik.md` under the owner decisions of
2026-10-31:

- Configure it by writing Audiobookshelf's own database. Do not use
  `/api/auth-settings`: it logs the old and new settings, client secret
  included, at debug level.
- The owner accepts that Audiobookshelf logs userinfo (name, email, groups)
  when it auto-registers a person. The plan warns about it. The client secret
  still never appears in argv, logs, Compose or environment values.
- Re-evaluate after every Audiobookshelf update: a version guard in the
  script and a pin test (`absSignInVerified`).
- Local login and the S384 recovery administrator keep working: the auth
  methods stay `local` + `openid`.

## Facts (v2.37.1 source and a fresh ghcr.io/advplyr/audiobookshelf:2.37.1 container)
- **Where:** `/config/absdatabase.sqlite`, table `settings(key, value JSON,
  createdAt, updatedAt)`, row `key='server-settings'`, `value` the JSON of
  `ServerSettings`. The auth fields (all present in a fresh row) are
  `authActiveAuthMethods` (array), `authOpenIDIssuerURL`,
  `authOpenIDAuthorizationURL`, `authOpenIDTokenURL`,
  `authOpenIDUserInfoURL`, `authOpenIDJwksURL`, `authOpenIDLogoutURL`,
  `authOpenIDClientID`, `authOpenIDClientSecret` (plaintext),
  `authOpenIDTokenSigningAlgorithm`, `authOpenIDButtonText`,
  `authOpenIDAutoLaunch`, `authOpenIDAutoRegister`,
  `authOpenIDMatchExistingBy`, `authOpenIDMobileRedirectURIs`,
  `authOpenIDGroupClaim`, `authOpenIDAdvancedPermsClaim`, plus
  `authLoginCustomMessage` and, absent until set,
  `authOpenIDSubfolderForRedirectURLs`.
- **When read:** at startup (`Database` loads the row into
  `ServerSettings`; `Auth.initPassportJs` registers the OpenID strategy only
  when `openid` is in `authActiveAuthMethods`). There is no write-back on
  shutdown (`Server.stop` only closes the watcher and the HTTP server); the
  row is rewritten only on a settings change or a version change at startup.
  So write and restart is safe; stopping first is not needed.
- **No discovery:** the app builds `OpenIDClient.Issuer` from the stored
  endpoints, and `ServerSettings` drops `openid` from the methods at load
  unless issuer, authorization, token, userinfo, jwks, client id, secret and
  signing algorithm are all set (logout is optional). Authentik's endpoint
  paths are fixed, so the secretfile fills them without a network fetch:
  `{{authentik_base}}/application/o/authorize/`, `.../token/`, `.../userinfo/`
  (as Komga and Linkding already do), `{{issuer}}jwks/` and
  `{{issuer}}end-session/` (Authentik's discovery names them the same).
  Homestead's provider has a signing key, so `RS256`, the app's default.
- **Image:** runs as root (no `USER`), `/config` is root-owned, entrypoint
  `tini -- node dist-server/index.js`. No `sqlite3` shell, no Python. Node 24
  has built-in `node:sqlite` (no warning printed in 24.21). The database is
  in `journal_mode=delete`, no WAL; the running app opens it per query through
  Sequelize, so a `BEGIN IMMEDIATE` with a 30 s `busy_timeout` coexists.
  Version: `/app/package.json` `version`.
- **Paths:** the app builds `redirect_uri` from the request (`Host` and
  `X-Forwarded-Proto`) plus `authOpenIDSubfolderForRedirectURLs`: web
  `/auth/openid/callback`, mobile `/auth/openid/mobile-redirect` (the app
  then redirects to the whitelisted `audiobookshelf://oauth`, default in
  `authOpenIDMobileRedirectURIs`). Sign-in starts at `/auth/openid?callback=`
  (the login page's button). **The subfolder must be set:** when absent,
  the server template gives `undefined/auth/openid/callback`, and the web UI
  fills it with `ROUTER_BASE_PATH` (default `/audiobookshelf`). The script
  writes `''`, giving the plain `/auth/openid/callback`; the app serves
  un-prefixed paths too. Homestead serves the app by host name, so no
  `ROUTER_BASE_PATH` and no `{{app_url}}` are needed.
- **Groups:** with `authOpenIDGroupClaim` set the userinfo must carry that
  claim as a list; the values `admin`, `user`, `guest` (case-insensitive,
  priority in that order) set the type at every sign-in; none of them denies
  the login and removes the just-created account; the `root` user is never
  downgraded (denied instead). The app also appends the claim name to the
  requested scope (`openid profile email groups`), which names Homestead's
  groups scope mapping. Without a claim new accounts are type `user`.
- **Matching:** `authOpenIDMatchExistingBy` null/`email`/`username`. null
  never links an existing account.
- **Logs (accepted):** info `Auto-registering user with sub ... <userinfo>`,
  debug `openid callback userinfo=<userinfo>`; `/api/auth-settings` logs the
  secret (not used). Confirmed live: the secret is not in the container log.
- **TLS:** Node (axios, openid-client) honours `NODE_EXTRA_CA_CERTS`, which
  adds to the default store.

## Change
- `catalog/audiobookshelf.yaml`: `sso.oidc` is `env-oidc`, `restart: true`,
  `trust: NODE_EXTRA_CA_CERTS` (bare appliance root, no combined bundle, set
  only for a private issuer), redirect paths `/auth/openid/callback` and
  `/auth/openid/mobile-redirect`, sign-in path `/login`. Secrets
  `oidc_client_secret` (no consumer) and `audiobookshelf_signin`
  (secretfile, initial `{}`, 0600, `ABS_SIGNIN_FILE`) carrying client id,
  secret, issuer and the five endpoints. The `onapply`/`onoff` argv is
  `node -e <script> on|off`.
- The script (in the manifest, run as root): checks the mode, the version in
  `/app/package.json` (on only; off always works), the secret file (client
  id must be `homestead-audiobookshelf`, every value present), the `settings`
  columns, the row and its shape (every auth field present). In one `BEGIN
  IMMEDIATE` transaction it merges only the auth fields into the existing
  JSON and sets `updatedAt`: methods keep `local` and gain `openid`;
  `AutoRegister` true; `MatchExistingBy` null; `GroupClaim` `groups`;
  `AdvancedPermsClaim` empty; `MobileRedirectURIs` the default; subfolder
  `''`; button text "Sign in with Authentik". Off removes `openid`, clears
  client id, secret and endpoints and the group claim, and removes the
  subfolder key. An OpenID setup whose client id is not Homestead's is
  foreign: on refuses, off leaves it. It reads the file in the process, never
  prints settings, secrets or exception messages that could quote the file.
- Roles (small engine extension): `env.adminvalue`/`env.uservalue` (both or
  neither, validated) make the groups claim also list `admin` for members of
  `homestead-audiobookshelf-admins` and `user` for everyone else
  (`RoleMapping.UserAlias`, `claimExpression`). Everyone bound to the app is
  in one of the two (or the owner group), so nobody is refused for lack of a
  value, and no Authentik group can mint `admin` (the claim lists only
  `homestead-*` names plus the two values).
- Why a restart and not stop-write-start: the app never writes the row at
  shutdown, so no engine feature is needed. An in-app settings change by an
  administrator between the write and the restart (seconds) would overwrite
  it; apply again.

## Decisions
- `MatchExistingBy` null: with `email` or `username` a person whose Authentik
  email or name equals the recovery administrator's would be linked to it
  silently. Cost: the app does not check that the new username is free
  (verified live), so such a person gets a second account of that name; the
  recovery login (local, `root`) is unaffected (verified live), and the plan
  says so.
- Auto-registration on: it is the point of sign-in; new accounts are type
  `user` until the group claim sets `admin` or `user`.
- No `guest` mapping; no advanced permissions claim.

## Re-verify (every Audiobookshelf update)
`TestAudiobookshelfSignInPinnedToVerifiedVersion`
(`internal/appsso/audiobookshelf_test.go`) fails when the manifest version or
image tag differs from `absSignInVerified`. Before changing both, check in the
new image: the `settings` table and the `server-settings` auth fields
(`ServerSettings.js`, including the validity rule), `/app/package.json`,
`node:sqlite` in its node, startup-only loading and any shutdown write-back,
the redirect paths and subfolder handling, the group claim handling and role
values, and what is logged (userinfo, the secret; an API or file form that
keeps the secret out of logs would replace the script). Update the script's
`VERIFIED`, `absSignInVerified`, this document and the manifest comment.

## Break-glass
The local login form and the recovery administrator are untouched. Turn
sign-in off with the normal plan and apply with `--off`. Manual: set
`authActiveAuthMethods` back to `["local"]` in the `server-settings` row and
restart the container.

## Tests
`internal/appsso/audiobookshelf_test.go` (pin, schema validation, derived
JSON and endpoints, plan, redirects, roles and claim expression, warnings,
trust, commands and restart, script output hygiene, apply and off with no-leak
checks), `TestCuratedAudiobookshelf`, `tests/test_s417_audiobookshelf_signin.py`
(the script against a temporary database with the 2.37.1 layout: on twice,
off, foreign setup, version, schema and shape guards, bad values, rollback),
`tests/test_s353_audiobookshelf.py`. Live on 2.37.1: the script wrote a
fresh container's row (after the S384-style `/init`), `/status` listed
`local` and `openid`, `/auth/openid` redirected to the configured authorize
URL with the right client id, scope and `redirect_uri` for web and mobile,
and a stand-in Authentik (RS256 id token, userinfo) signed a person in as
`user`, another as `admin`, refused one with no role value, left the local
root login working; off restored `["local"]`.

## Out of scope
Recorded in DEFERRED. No API route or CLI command changes, so
`docs/PARITY.md` is unchanged.
