# S414: Vaultwarden signs in through Authentik

## Goal
Turn the `env-oidc` adapter on for Vaultwarden. Password login stays
(`SSO_ONLY` is not set; ADR-052 break-glass rule). Vaultwarden gains SSO in
1.35.0, so the package moves from 1.34.3.

## Version
1.37.4 (latest stable, 05 Oct; not a pre-release). 1.35.x is superseded and
its SSO has fixed advisories (1.36.0: SSO login CSRF, existing-user binding).
Sources: github.com/dani-garcia/vaultwarden/releases and tags 1.35.0, 1.36.0,
1.37.0, 1.37.4; Docker Hub tag `vaultwarden/server:1.37.4` exists.

## Facts
- Wiki "Enabling SSO support using OpenID Connect": `SSO_ENABLED`,
  `SSO_AUTHORITY` must equal the discovery `issuer` exactly; Authentik needs
  the trailing `/` (the Homestead issuer has it). `SSO_SCOPES` default
  `email profile` (`openid` implicit). `SSO_PKCE` default true.
  `SSO_SIGNUPS_MATCH_EMAIL` default true; `SSO_ALLOW_UNKNOWN_EMAIL_VERIFICATION`
  default false (Authentik reports `email_verified`). Redirect URI is
  `<DOMAIN>/identity/connect/oidc-signin`. SSO "does not control" the master
  password: it is still required to decrypt the vault.
- `src/util.rs` `get_env_str_value`: every config variable accepts `<VAR>_FILE`
  (trimmed); defining both panics. `SSO_CLIENT_SECRET` is a `Pass` setting, so
  `SSO_CLIENT_SECRET_FILE` is used and nothing exports it.
- TLS: reqwest 0.13 with `rustls-no-provider` (system verifier, native certs);
  the wiki says a private CA goes in `/etc/ssl/certs` or `SSL_CERT_FILE` /
  `SSL_CERT_DIR`; the image sets `SSL_CERT_DIR=/etc/ssl/certs`. So
  `SSL_CERT_FILE` -> the combined bundle (host public roots plus appliance
  root) is additive. Set only for a private issuer (S405).
- Authentik's refresh needs the `offline_access` scope mapping and the
  refresh-token grant, which Homestead's provider does not have (scopes
  openid/profile/email, grant authorization_code). Without them the session
  would last one access token, so `SSO_AUTH_ONLY_NOT_SESSION=true` keeps
  Vaultwarden's own session lifetime (wiki, "Session Lifetime").
- Upgrade 1.34.3 -> 1.37.4: no manual step for this package. Release notes
  list: `IP_HEADER=X-Forwarded-For` client IP rule (not set here), removed
  feature flags and `DUO_USE_IFRAME` (not set), removed legacy register and
  prelogin endpoints (no current client), `DATABASE_URL` unknown scheme now an
  error (unset here, so SQLite), custom admin/email templates (none). The
  database migrates forward at first start and cannot go back: back up first.

## Change
- Catalog: version/image 1.37.4; `sso.oidc` env-oidc with `SSO_ENABLED`,
  `SSO_AUTHORITY={{issuer}}`, `SSO_CLIENT_ID`, `SSO_AUTH_ONLY_NOT_SESSION`;
  `trust: SSL_CERT_FILE` + `trustbundle: combined`; secret
  `oidc_client_secret` consumed as `SSO_CLIENT_SECRET_FILE`; plan warnings
  (master password unchanged and unrecoverable; email match; DOMAIN origin).
- No new mechanism, no ADR change.

## Break-glass
Password login is untouched. Turn sign-in off with the normal plan and apply
with `--off`. Manual: remove the `SSO_*` and `SSL_CERT_FILE` keys for
vaultwarden from the stack's `compose.override.yaml`, `docker compose up -d`.

## Tests
`internal/appsso/vaultwarden_test.go`, `TestCuratedVaultwarden`,
`internal/catalog/data_test.go` image pin, `tests/test_s345_vaultwarden.py`.

## Out of scope
Recorded in DEFERRED.
