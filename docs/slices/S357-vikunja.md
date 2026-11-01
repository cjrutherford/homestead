# S357: Vikunja

## Goal
Ship `vikunja` (Wave 2, shared to-do lists with CalDAV) with Authentik
sign-in in the same slice (owner rule, 2026-11-01), through the env-oidc
adapter.

## Dependencies
S341 (catalog expansion), S342 (secrets), S402 (env-oidc), S413
(`secretfile`), S410 (combined trust bundle), ADR-052, ADR-054.

## Scope
- `catalog/vikunja.yaml`: package and `sso.oidc` env-oidc block.
- A secret consumer may mount its file at a path (`SecretConsumer.Path`).
- Not in scope: group-to-team sync, Nextcloud CalDAV wiring, email, importers
  (DEFERRED).

## Facts (Vikunja 2.7.1, released 2026-10-20; vikunja.io docs, go-vikunja/vikunja `pkg/config`, Docker Hub)
- Single image `vikunja/vikunja:2.7.1` (amd64, arm64, armv7), `FROM scratch`
  (no shell), user 1000, port 3456, `VIKUNJA_SERVICE_ROOTPATH=/app/vikunja/`,
  `VIKUNJA_DATABASE_PATH=/db/vikunja.db`, no VOLUME. Entrypoint is the binary;
  no command runs the web server.
- Every key is an environment variable `VIKUNJA_<SECTION>_<KEY>`; a `.file`
  key (env `..._FILE`) is read at startup. `service.secret` (`VIKUNJA_SERVICE_
  SECRET_FILE`) signs sessions; `service.JWTSecret` is deprecated.
- `service.publicurl` builds links, CORS and the OpenID callback and ends in a
  slash. Vikunja cannot run under a path, so the package is served on a name of
  its own (default `serve: host`, like Gitea).
- `auth.openid.providers` is a map keyed by a provider id. The loader skips
  `.file` handling for it ("Setting openid provider values will remove
  everything but the value from file"), so the client secret cannot come from
  a file variable; environment fields are only applied to a provider the
  config file already names. The config file is looked up as `config.<ext>` in
  the rootpath, `/etc/vikunja/`, `~/.config/vikunja` and `.`; `--config` needs
  a file name with an extension (viper picks the type from it).
- Provider fields: `name`, `authurl` (the issuer; endpoints are discovered),
  `clientid`, `clientsecret`, `scope` (default `openid profile email`),
  `forceuserinfo`, `usernamefallback`, `emailfallback` (both default false;
  the docs warn of account takeover). Callback: `<publicurl>/auth/openid/<id>`.
  `auth.openid.enabled` switches the feature; `auth.local.enabled` (default
  true) is left alone.
- Roles: Vikunja has no administrator role to set from claims. Teams can be
  filled from a `vikunja_groups` claim through an Authentik scope mapping
  (`vikunja_scope`); not wired.
- Trust: Go reads `SSL_CERT_FILE` as its whole store, so the combined bundle
  (as Gitea, S416).

## Mechanism (ADR-052 amendment, S357)
The image has no shell, so the supervisor cannot export, and the provider list
is config-file only. A secret consumer now declares `path:` instead of `env:`;
the generated Compose uses the long secret form with `target:` so the 0600
file appears read-only at `/etc/vikunja/config.yml`. Validation: absolute,
clean path outside `/run/secrets`, no space or colon; excludes `env`, `uri`,
`export`. `serviceSecretWiring`, adopt and `WireDeclaredSecret` (old stacks)
render and patch the long form; drift already read it.

## Built
- Secrets: `jwt_secret` (immutable, `VIKUNJA_SERVICE_SECRET_FILE`),
  `oidc_client_secret` (no consumer), `oidc_config` (initial `{}`, immutable,
  path `/etc/vikunja/config.yml`).
- Sign-in: secretfile template renders
  `{"auth":{"openid":{"providers":{"authentik":{...}}}}}`; `set` has only
  `VIKUNJA_AUTH_OPENID_ENABLED=true`; redirect `/auth/openid/authentik`;
  sign-in path `/login`; trust `SSL_CERT_FILE` combined. Off restores `{}` and
  removes the variable. Local login and registration stay on. The plan warns
  that there is no admin mapping.
- The generated stack mounts the config, so a container recreate (the apply's
  override change) is what loads it; Vikunja reads config only at start.
- `TestVikunjaSignInPinnedToVerifiedVersion` fails when the version changes
  until the file shape is re-verified.

## Tests
`internal/appsso/vikunja_test.go` (pin, escaped config JSON, plan redirect and
warning, apply/re-apply/off with no secret in compose or override),
`internal/catalog/secretpath_test.go` (validation, long-form render, old-stack
patch), curated/SSO/image-volume tables, `tests/test_s357_vikunja.py`.

## Verify live (VM 2)
1. Install `vikunja` (JSON below), open it at its route, register the first
   local account, confirm CalDAV answers at `/dav/`.
2. Plan sign-in: redirect `https://<name>/auth/openid/authentik`; apply.
3. Sign out; the login page shows "Log in with Authentik"; sign in as an
   Authentik user in the app's group; an account named for the Authentik
   username exists. Local login still works.
4. `docker exec` is not possible (scratch); check `docker inspect` shows no
   secret, and `ls -l <stack>/secrets/vikunja_oidc_config` is 0600 owner 1000.
5. Turn sign-in off; the file returns to `{}` and the button is gone.
If discovery fails with a TLS error, the trust bundle is the first thing to
check (`SSL_CERT_FILE` in the override).

## Live results (VM 2, 2026-11-01, 11f190c)
Installed local-only with sign-in approved at install (others: no access);
install and setup succeeded (address, sign-in, people).
- `/api/v1/info`: v2.7.1, local login on, OpenID enabled with provider
  `authentik`, client `homestead-vikunja`, discovered from the local issuer
  (`https://homestead.lan/apps/auth/...`), so `SSL_CERT_FILE` trust works
  and the issuer's trailing slash is accepted.
- `docker inspect` holds no client secret; mounts include
  `/etc/vikunja/config.yml`; the stack's `secrets/vikunja_oidc_config` is
  0600, uid 1000.
- Authentik authorize with `redirect_uri=https://vikunja.lan/auth/openid/authentik`
  → 302 to its login flow; a foreign redirect URI → 400. The callback route
  serves the web app.
- Not yet checked: a browser sign-in creating the account, sign-in off.
