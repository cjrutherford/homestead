# S413: Paperless-ngx signs in through Authentik

## Goal
Turn the `env-oidc` adapter on for Paperless-ngx 3.3.4. Its local login form
stays (break-glass, ADR-052).

## Facts (Paperless-ngx v3.3.4 docs and source)
- `docs/configuration.md`: `PAPERLESS_APPS` adds Django apps;
  `PAPERLESS_SOCIALACCOUNT_PROVIDERS` is django-allauth's provider JSON;
  `PAPERLESS_SOCIAL_AUTO_SIGNUP` (default false) creates the account from the
  provider's details; `PAPERLESS_SOCIALACCOUNT_ALLOW_SIGNUPS` defaults true;
  `PAPERLESS_DISABLE_REGULAR_LOGIN` (default false) and
  `PAPERLESS_REDIRECT_LOGIN_TO_SSO` (default false) are left alone.
- `docs/advanced_usage.md`: the OpenID Connect example sets
  `PAPERLESS_APPS="allauth.socialaccount.providers.openid_connect"` and a
  `openid_connect.APPS[]` entry with `provider_id`, `client_id`, `secret` and
  `settings.server_url` (the `.well-known/openid-configuration` URL).
- `src/paperless/settings/__init__.py`: the provider variable is parsed by
  `get_json_from_env`; no CA setting, so allauth's `requests` honours
  `REQUESTS_CA_BUNDLE`.
- `docker/rootfs/etc/s6-overlay/s6-rc.d/init-env-file/run`: every
  `PAPERLESS_*_FILE` variable (two named exceptions) is read into the
  matching variable; a trailing newline in the file draws a warning (the
  rendered file has none).
- `PAPERLESS_SOCIAL_ACCOUNT_SYNC_GROUPS` needs `groups` in the provider scope
  and only adds people to Paperless groups that already exist with the same
  name; nothing grants staff or superuser from a group. No role mapping is
  wired.
- The allauth callback is `/accounts/oidc/<provider_id>/login/callback/`; the
  provider id is `authentik`. The sign-in button is on `/accounts/login/`.

## Change
- Catalog: `sso.oidc` env-oidc (`PAPERLESS_APPS`,
  `PAPERLESS_SOCIAL_AUTO_SIGNUP=true`), `trust: REQUESTS_CA_BUNDLE` +
  `trustbundle: combined`; secrets `oidc_client_secret` (no consumer,
  immutable) and `oidc_providers` (initial `{}`, immutable, exported to
  `PAPERLESS_SOCIALACCOUNT_PROVIDERS`).
- Mechanism (ADR-052 amendment, S413): `SecretDecl.initial`,
  `EnvOIDC.secretfile`, `{{client_secret}}` only in that template. Apply
  renders and writes the 0600 file before wiring; off restores `{}`.
  Existing installs: `ensureDeclaredSecret` and the plan's
  `StackNeedsSecret` include the derived secret.
- Groups: everyone is an ordinary user with no permissions until a Paperless
  administrator grants them; the plan warns.

## Break-glass
Paperless's own login form is untouched. To turn sign-in off, use the normal
plan and apply with `--off`. Manual: remove `PAPERLESS_APPS`,
`PAPERLESS_SOCIAL_AUTO_SIGNUP` and `REQUESTS_CA_BUNDLE` for the webserver from
the stack's `compose.override.yaml`, `docker compose up -d`.

## Tests
`internal/appsso/paperless_test.go` (schema validation, JSON escaping, plan
redirect and warnings, apply/re-apply/off on an install from before the
secrets with no-leak checks on compose, override and supervisor);
`TestPaperlessSignInSecrets`; `tests/test_s346_paperless.py`.

## Out of scope
Recorded in DEFERRED.
