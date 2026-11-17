# S416: Gitea signs in through Authentik

## Goal
Turn the `env-oidc` adapter on for Gitea 28.2.0 beside its local login
(ADR-052 break-glass rule), so `homesteadctl apps sign-in plan|apply gitea`
works. Owner decision: configure it by writing Gitea's own SQLite database.
This **supersedes the "no direct database writes" constraint in
`docs/slices/S391-gitea-authentik-prerequisite.md`** by owner decision;
`gitea admin auth add-oauth --secret` stays unused because it puts the secret
in argv. **After every Gitea package update this approach must be
re-evaluated** (see Re-verify). Per-person `none` enforcement and closing
local registration are out of scope (DEFERRED).

## Facts (v28.2.0 source and a live gitea/gitea:28.2.0 container)
- `/data/gitea/gitea.db`, owned by `git` (uid/gid 1000), which runs Gitea.
  Image tools: `/usr/bin/sqlite3` 3.53 (JSON functions and `readfile()`), `sh`
  (busybox), the `gitea` wrapper. No Python. `gitea --version` prints
  `gitea version 28.2.0 built with ...`.
- `login_source` columns: id, type, name (unique), is_active, is_sync_enabled,
  two_factor_policy, cfg, created_unix, updated_unix. OAuth2 is type 6
  (`models/auth/source.go`). `cfg` is plain JSON of `oauth2.Source`
  (`services/auth/source/oauth2/source.go`): Provider, ClientID, ClientSecret
  (**plaintext**), OpenIDConnectAutoDiscoveryURL, CustomURLMapping, IconURL,
  Scopes, RequiredClaimName/Value, GroupClaimName, AdminGroup, GroupTeamMap,
  GroupTeamMapRemoval, RestrictedGroup, SSHPublicKeyClaimName,
  FullNameClaimName, ExternalIDClaim. A row made by `add-oauth` has exactly
  these keys; the script writes the same shape.
- Providers are registered in memory from the active sources in
  `oauth2.Init` at startup only (`initOAuth2Sources`); the admin UI/CLI
  register on save. A direct database write is therefore read after a
  restart. A failing discovery fetch at startup is logged and the row is kept.
- Callback `<ROOT_URL>user/oauth2/<source name>/callback`
  (`providers.go`: `setting.AppURL + "user/oauth2/" + name + "/callback"`);
  sign-in starts at `/user/oauth2/<name>`. The source name is `authentik`.
- `[oauth2_client]`: `ENABLE_AUTO_REGISTRATION` (default false), `USERNAME`
  (default `nickname`), `ACCOUNT_LINKING` (default `login`; `disabled`,
  `login`, `auto`), `OPENID_CONNECT_SCOPES` (overrides the source scopes if
  set; left unset).
- Account matching: a known external login signs in. Otherwise, with auto
  registration, the account is created from the claims; if the name or email
  exists, `login` shows a page asking for that account's local password, and
  `auto` links silently. Admin: at each sign-in `AdminGroup` membership in the
  `GroupClaimName` claim sets or clears `is_admin` (cleared too when the claim
  is empty), except the last administrator is never demoted
  (`routers/web/auth/oauth_signin_sync.go`, `services/user/update.go`).

## Change
- `catalog/gitea.yaml`: `GITEA__server__ROOT_URL` is now `{{app_url}}/` (was
  `https://{{route_host}}/`), so the callback follows the public name after a
  public refresh (S407). `sso.oidc` is `env-oidc` with `execuser: git`,
  `restart: true`, `trust: SSL_CERT_FILE` + `trustbundle: combined` (Go reads
  the file as its whole store; the bundle keeps public fetches working),
  `onapply`/`onoff` running one inline `sh` script (`sh -c <script> sh on|off`),
  and a `gitea_signin` secretfile (initial `{}`) with client id, secret,
  discovery URL and admin group, delivered 0600 as `GITEA_SIGNIN_FILE`. The
  secret `oidc_client_secret` has no consumer.
- `set` (the sign-in environment, removed on off): `ENABLE_AUTO_REGISTRATION`
  true, `USERNAME` preferred_username, `ACCOUNT_LINKING` login, plus the issuer
  and admin-group markers. Choices: auto registration is the point of sign-in;
  `preferred_username` is Authentik's stable username (`nickname` is not in
  Homestead's mapping); `login` makes a person who matches an existing local
  account prove it with that password, where `auto` would let anyone with that
  Authentik username take over the account (the CWA warning, avoided here).
- Scopes `openid profile email`; the `profile` mapping carries `groups`
  (S415), so `GroupClaimName` is `groups` and `AdminGroup` the app's Homestead
  admin group.
- The script (in the manifest): checks the mode, the database file, `gitea
  --version` equals the verified version (on), the secret file, sqlite3 JSON
  support and all nine `login_source` columns; refuses a row named `authentik`
  whose `ClientID` is not `homestead-gitea` (on: error; off: left alone);
  reads the values with `readfile()` (nothing secret is an argument or in the
  SQL text), builds the config with `json_object`, and in one `BEGIN
  IMMEDIATE` transaction (30 s busy timeout) updates Homestead's row or
  inserts it (type 6, active, no sync, empty two-factor policy). Off sets
  `is_active=0` and clears `ClientSecret`; it never deletes the row, so
  `external_login_user` links survive. It never prints the secret and fails
  with a "re-verify" message.
- Plan warnings: the database is edited and re-checked on every update, run
  after the first start with Authentik reachable; only the ROOT_URL address
  completes sign-in; matching accounts link by local password; admin comes from
  the group (and others are demoted at sign-in, never the last administrator);
  the local login and registration stay and `none` is not enforced; the secret
  is plaintext in `gitea.db` (storage and backups).
- Mechanism (ADR-052 amendment S416): no engine change. `restart`, `execuser`,
  inline scripts and the secretfile from S413/S415 are reused; only the script
  language differs (`sh` + the image's `sqlite3` instead of Python).

## Re-verify (every Gitea update)
`TestGiteaSignInPinnedToVerifiedVersion` (`internal/appsso/gitea_test.go`)
fails when the manifest version or image tag differs from
`giteaSignInVerified`. Before changing both, check in the new image: the
`login_source` columns and the type number, the `oauth2.Source` JSON fields
and where the secret is stored, how `gitea --version` prints, when providers
register (startup only?), the callback path, the `[oauth2_client]` options,
the admin-sync logic, and whether `admin auth add-oauth` has a secret-file or
stdin form (then replace the script). Update the script's `VERIFIED`,
`giteaSignInVerified`, this document and the manifest comment.

## Break-glass
The local login form and local admin are untouched. Turn sign-in off with the
normal plan and apply with `--off`. Manual: `sqlite3 /data/gitea/gitea.db
"UPDATE login_source SET is_active=0 WHERE name='authentik'"` as `git`, then
restart the container.

## Tests
`internal/appsso/gitea_test.go` (pin, schema validation, derived JSON, plan and
warnings, trust, commands and restart, apply and off with no-leak checks),
`TestCuratedGitea`, `tests/test_s416_gitea_signin.py` (the script against a
temporary database with the verified schema: on twice, update, off, foreign
row, wrong version, missing column, empty values), `tests/test_s347_gitea.py`.
Live on 28.2.0: row written, listed by `gitea admin auth list`, and
`/user/oauth2/authentik` redirects to the discovery document's authorize URL.

## Out of scope
Recorded in DEFERRED: per-person `none`, closing registration, team mapping.
No API route or CLI command changes, so `docs/PARITY.md` is unchanged.
