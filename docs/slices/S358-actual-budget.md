# S358: Actual Budget

## Goal
Ship `actual-budget` (Wave 2, household envelope budgeting) with Authentik
sign-in in the same slice (owner rule, 2026-11-01), through the env-oidc
adapter and Actual's native OpenID.

## Dependencies
S341 (catalog expansion), S342 (secrets), S402 (env-oidc), S410 (trust),
S412/S415-S417 (onapply/onoff script pattern), ADR-052, ADR-054.

## Scope
- `catalog/actual-budget.yaml`: package and `sso.oidc` env-oidc block.
- Icon `piggy-bank-outline` in `dashboard/src/app/ui/app-icons.ts`.
- Not in scope: bank-sync providers, group-to-role mapping (impossible),
  enforcing OpenID (DEFERRED).

## Facts (actual-server 26.10.0, released 2026-10-02; actualbudget/actual `packages/sync-server` at v26.10.0, actualbudget.org docs, Docker Hub)
- Image `docker.io/actualbudget/actual-server:26.10.0` (amd64, arm64;
  node 24 bookworm-slim, tini, `CMD node build/app.js`), no VOLUME, no USER
  (root), port 5006, data dir `/data` (`server-files/account.sqlite`,
  `user-files/`).
- OpenID by environment (`load-config.js`): `ACTUAL_OPENID_DISCOVERY_URL`,
  `_CLIENT_ID`, `_CLIENT_SECRET`, `_SERVER_HOSTNAME`, `_PROVIDER_NAME`,
  `_AUTH_METHOD`, `ACTUAL_OPENID_ENFORCE`, `ACTUAL_USER_CREATION_MODE`
  (`manual` default, `login`). `ACTUAL_OPENID_CLIENT_SECRET_FILE` is read after
  config.json and beats the plain variable.
- At every start `app.ts run()` sees a discovery URL and force-bootstraps
  OpenID: discovers the issuer, deletes the `auth` row for openid, sets
  `active = 0` on the others and inserts an active openid row whose
  `extra_data` is the config JSON (client secret included). So the env alone
  switches the login method; `ACTUAL_LOGIN_METHOD` is not needed (the active
  row wins).
- Callback: `new URL('/openid/callback', server_hostname)`; redirects to other
  hosts are refused. Cannot run under a path: host serving.
- Identity: `preferred_username`, then login, email, id, sub. The first
  sign-in when no user with a name exists creates the owner (`owner=1`,
  `ADMIN`) and takes over files of the password account. Later people are
  created as `BASIC` only if `userCreationMode=login`, else must be
  pre-created by name.
- Node: `NODE_EXTRA_CA_CERTS` (bare appliance root, as S417).

## Built
- Environment: hostname `{{app_url}}` (static), discovery URL
  `{{issuer}}.well-known/openid-configuration`, client id, provider name,
  `login` creation mode. Secret `oidc_client_secret` as
  `ACTUAL_OPENID_CLIENT_SECRET_FILE`: never in env, argv, Compose or logs.
- Script (`onapply on` / `onoff off`, node:sqlite, version guard, schema
  guard on `auth` columns): on confirms the active openid row exists (retried
  while Actual starts); off deletes the openid row and reactivates the
  password row if there is one. No restart (Actual reads `auth` per request).
- Pin test `TestActualBudgetSignInPinnedToVerifiedVersion`.

## Owner/admin and password consequences
- First Authentik sign-in = permanent server owner and administrator; no
  group mapping. Sign in yourself first.
- Not enforced: a password set before sign-in stays a second choice on the
  login page. With no password, losing Authentik means sign-in off here:
  the OpenID row is removed and Actual asks for a new server password (budgets
  stay owned by the OpenID user).

## Tests
`internal/appsso/actualbudget_test.go` (pin, env and secret placement, plan
redirect and warnings, commands without restart or argv secret, the script run
with real node against a real `auth` table), curated/SSO/image-volume tables,
`tests/test_s358_actual_budget.py`.

## Verify live (VM 2)
1. Install `actual-budget` with sign-in approved at install (container port
   5006, name `actual-budget.<zone>`).
2. Plan/apply sign-in; redirect `https://actual-budget.<zone>/openid/callback`.
3. Open the app: the login page offers OpenID/Authentik; sign in as the
   owner first; Actual shows you as server owner (Server settings > users).
4. Sign in as a second Authentik user in the app's group: an ordinary user
   is created.
5. `docker inspect` holds no client secret (only `..._SECRET_FILE`).
6. Turn sign-in off; the openid row is gone (`sqlite3` or the login page),
   and Actual asks for a server password.
If discovery fails with a TLS error, check `NODE_EXTRA_CA_CERTS` in the
override.
