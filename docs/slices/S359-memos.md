# S359: Memos

## Goal
Ship `memos` (Wave 2, fast private notes) with Authentik sign-in in the same
slice (owner rule, 2026-11-01), through the env-oidc adapter and Memos's own
declarative configuration.

## Dependencies
S341 (catalog expansion), S342 (secrets), S357 (secret mounted as a config
file), S402 (env-oidc), S410 (trust), ADR-052, ADR-054.

## Scope
- `catalog/memos.yaml`: package and `sso.oidc` env-oidc block.
- Icon `notebook-edit-outline` in `dashboard/src/app/ui/app-icons.ts`.
- No engine change: the S357 secret `path:` consumer, `secretfile` template
  and `{{authentik_base}}` (S417) are enough.
- Not in scope: administrator mapping, bootstrap users, managing instance
  settings, S3 storage.

## Facts (Memos 26.10.0, released 2026-10-31; usememos/memos tag 26.10.0, usememos.com docs, Docker Hub)
- Image `docker.io/neosmemo/memos:26.10.0` (amd64, arm64, arm; alpine 3.21,
  one Go binary, `VOLUME /var/opt/memos`, `EXPOSE 5230`, `MEMOS_PORT=5230`).
  Starts as root; `entrypoint.sh` chowns the data directory to uid/gid 10001
  and re-executes Memos as that user with `su-exec`. SQLite
  (`memos_prod.db`) by default. No sub-path support: host serving.
- Since 26.10.0 Memos reads declarative configuration: every regular `*.json`
  file directly in `/etc/memos/config` (`--config-dir` / `MEMOS_CONFIG_DIR`)
  is merged and applied at startup after migration. Documents are
  `memos.api.Configuration`: `identityProviders`, `instanceSettings`, `users`,
  `lifecycle`, `removed`; unknown fields are errors. `${file:/abs}` /
  `${env:NAME}` placeholders exist but are not needed here. The old
  `/etc/secrets/memos-idp-*.json` files are no longer read.
- Managed resources are recorded in the database and are read-only in the UI
  and API (`MANAGED_BY_CONFIGURATION`). A managed resource that no file
  declares fails the plan ("add a removed entry to destroy or release it")
  and Memos does not start (`core/configuration/plan.go planUndeclared`); the
  plan only looks at resources in its state record, so a `removed` entry for
  something never managed is ignored.
- OAuth2 provider shape: `name: identity-providers/<uid>`, `type: OAUTH2`,
  `title`, `identifierFilter`, `config.oauth2Config{clientId, clientSecret,
  authUrl, tokenUrl, userInfoUrl, scopes, fieldMapping{identifier,
  displayName, email, avatarUrl}}`. Title, client id and identifier mapping
  are required. Callback: `<origin>/auth/callback`; PKCE supported.
- First SSO sign-in (`server/api/auth_service_sso.go`): a linked identity
  signs in; otherwise, if `DisallowUserRegistration` is set, "user
  registration is not allowed"; else a new user with role USER is created,
  username = the mapped identifier if valid and free, else a UUID-based name.
  Never linked to an existing account by name or email, except when an
  already signed-in user binds an identity from settings.
- Password sign-in stays unless `disallowPasswordAuth` is set (not managed
  here). Disabling it with no provider is refused by the plan.
- Go binary: `SSL_CERT_FILE` with the combined bundle (S410).

## Built
- Secret `memos_config` (immutable, 0600, owner = data uid 10001) mounted
  read-only at `/etc/memos/config/homestead.json`; `oidc_client_secret` has no
  consumer of its own. The client secret exists only in the Homestead
  secret store, that file, and Memos's database; never in Compose, override,
  argv, environment or logs.
- On: the file holds one provider `identity-providers/authentik` with
  Authentik's fixed endpoints (`{{authentik_base}}/application/o/authorize/`,
  `token/`, `userinfo/`), scopes `openid profile email`, identifier
  `preferred_username`, display name `name`, email `email`, avatar `picture`.
  Compose recreates the container (marker `HOMESTEAD_OIDC_ISSUER` and the trust
  variable change) so Memos applies it at start.
- Initial and off value: `{"removed":[{"resource":"identity-providers/authentik","destroy":true}]}`.
  Not `{}`: after on, `{}` would make Memos refuse to start. On a fresh
  install it names nothing managed and is ignored; on off it deletes the
  provider (and its stored secret).
- `MEMOS_INSTANCE_URL={{app_url}}`.
- Tests: `internal/appsso/memos_test.go` (pin `TestMemosSignInPinnedToVerifiedVersion`,
  escaped JSON shape, plan redirect `/auth/callback`, apply/off through real
  Compose generation and secret files, starting value),
  `tests/test_s359_memos.py`, curated and SSO tables in `internal/catalog`.
  No script runs, so there is no script test; Memos's own loader is
  verified live.

## Admin, registration and password consequences
- No group-to-role mapping. SSO people are ordinary users. The administrator
  is the first account created in Memos with a password: create it before
  anyone signs in. The owner may link their Authentik identity to that account
  from Memos settings (verify live).
- New people can first sign in only while registration is allowed; Homestead
  does not manage the `GENERAL` setting.
- Password sign-in is unchanged. Do not disable it in Memos and then turn
  Authentik sign-in off.

## Verify live (VM 2)
Container port 5230, name `memos.<zone>` (host serving).
1. Install Memos; open it and create the first account with a password
   (administrator). Plan and apply sign-in on the app's page; the plan shows
   redirect `https://<name>/auth/callback`, the warnings above, and no
   secret.
2. Check `/etc/memos/config/homestead.json` is mode 0600 uid 10001 in the
   container, and Memos logs no configuration error. Memos settings shows the
   Authentik provider as managed.
3. Sign out; the login page offers Authentik. Sign in as an Authentik person:
   an ordinary account named from the username; sign out and in again returns
   to the same account.
4. Link test: as the administrator, link Authentik in settings (if offered).
5. Turn sign-in off: Memos restarts cleanly, the Authentik button is gone,
   password sign-in works, the person's account remains.
6. Plan sign-in on again after off; it works.
