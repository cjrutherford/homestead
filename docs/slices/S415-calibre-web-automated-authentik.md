# S415: Calibre-Web Automated signs in through Authentik

## Goal
Turn the `env-oidc` adapter on for Calibre-Web Automated (CWA) 4.0.8 beside
its local login form (ADR-052 break-glass rule). Owner decision: configure it
by writing CWA's own SQLite settings database, accepting the dependence on
its internal schema. **After every CWA package update this approach must be
re-evaluated** (see Re-verify).

## Facts (live v4.0.8 container and upstream source)
- `/config/app.db`. Image tools: `/usr/bin/sqlite3`, `/lsiopy/bin/python`.
  Source in `/app/calibre-web-automated/cps/`. The installed version is in
  `/app/CWA_RELEASE` (baked at build; `cps/constants.py` reads it).
- `oauthProvider` columns: id, provider_name, oauth_client_id,
  oauth_client_secret, oauth_base_url, oauth_authorize_url, oauth_token_url,
  oauth_userinfo_url, oauth_admin_group, metadata_url, scope, username_mapper,
  email_mapper, login_button, active. Rows 1 github, 2 google, 3 generic, all
  inactive after first start.
- `settings` (one row): `config_login_type` (0 standard, 2 OAuth),
  `config_oauth_redirect_host`, `config_enable_oauth_group_admin_management`,
  `config_disable_standard_login`, `config_default_role`.
- Read only at startup (`generate_oauth_blueprints`), so the app restarts
  after the write. The discovery URL (`metadata_url`) is fetched then with
  `requests` (`REQUESTS_CA_BUNDLE`, combined bundle for a private issuer).
- Redirect URI `<config_oauth_redirect_host>/login/generic/authorized`;
  sign-in starts at `/login/generic`. Users match by username
  (`preferred_username`), then email; unknown users are created with the
  default role. Admin is granted or revoked at each login from the `groups`
  claim (case-insensitive match of `oauth_admin_group`) when group admin
  management is on and the claim is non-empty.

## Change
- Catalog: `sso.oidc` env-oidc, `execuser: abc`, `restart: true`,
  `onapply`/`onoff` run one inline Python script (`on` / `off`),
  `trust: REQUESTS_CA_BUNDLE` + `trustbundle: combined`, `secretfile`
  `cwa_signin` (initial `{}`) rendered with client id, client secret, discovery
  URL and admin group, and delivered as a 0600 file whose path is
  `CWA_SIGNIN_FILE` (not exported). Secret `oidc_client_secret` has no
  consumer. `set` holds two marker variables (`CWA_OIDC_ISSUER`,
  `CWA_OIDC_ADMIN_GROUP`) that nothing reads; they make the wiring change with
  sign-in and surface the admin group in the plan.
- Redirect host: `{{app_url}}` is a catalog compose token, not a sign-in
  placeholder, so the service environment carries `HOMESTEAD_CWA_URL:
  "{{app_url}}"` (as Vaultwarden's `DOMAIN`) and the script reads it from the
  environment. A public refresh moves it like `DOMAIN`, then the plan asks to
  re-plan sign-in, which re-runs the script.
- Scope is `openid profile email`: Homestead's Authentik provider has only
  those scope mappings, and its `profile` mapping carries the `groups` claim
  (Mealie reads `groups` the same way); an extra `groups` scope would name a
  mapping that does not exist.
- The script (in the manifest): checks `/app/CWA_RELEASE` equals the verified
  version (when readable), checks `PRAGMA table_info` for every column it
  writes, then in one `BEGIN IMMEDIATE` transaction (30 s busy timeout)
  UPDATEs the generic row (client id, secret, discovery URL, scope, mappers,
  admin group, button text "Authentik", active=1) and the settings row
  (login type 2, redirect host, group admin management 1). It never prints the
  secret and fails with a "re-verify" message. Off (version check skipped)
  clears client id, secret, discovery URL, admin group, active=0, login type 0
  and group admin management 0.
- Mechanism (ADR-052 amendment): new `EnvOIDC.restart`, run by the adapter
  after a successful on/off command (`restart` seam); inline scripts are
  allowed (arguments may contain newlines) and the plan prints
  `<n-line script>`.
- Plan warnings: sign-in is read from the app's database and re-checked on
  every update; only the configured address completes sign-in; an Authentik
  username equal to an existing CWA user (including `admin`) takes over that
  account; an empty groups claim keeps the admin role; the client secret is
  plaintext in `app.db` (storage and backups); the local login stays; run
  after the first start.

## Re-verify (every CWA update)
`TestCWASignInPinnedToVerifiedVersion` (`internal/appsso/cwa_test.go`) fails
when the manifest version differs from `cwaSignInVerified`. Before changing
both, check in the new image: the two tables and the columns the script writes,
`/app/CWA_RELEASE`, when and how `cps/oauth_bb.py` reads the rows, the
redirect and admin-group logic; update the script's `VERIFIED` constant,
`cwaSignInVerified`, this document and the manifest comment. Consider whether
CWA has gained a supported configuration path that replaces the script.

## Break-glass
The local login form is untouched. Turn sign-in off with the normal plan and
apply with `--off`. Manual: in `/config/app.db`, `UPDATE oauthProvider SET
active=0 WHERE provider_name='generic'; UPDATE settings SET
config_login_type=0;` then restart the container.

## Tests
`internal/appsso/cwa_test.go` (pin, schema validation, derived JSON, plan and
warnings, trust, commands and restart, apply and off with no-leak checks),
`TestCuratedCalibreWebAutomated`, `tests/test_s415_cwa_signin.py` (the script
against a temporary database: on, off, wrong version, missing column),
`tests/test_s356_calibre_web_automated.py`.

## Out of scope
Recorded in DEFERRED. No API route or CLI command changes, so
`docs/PARITY.md` is unchanged.
