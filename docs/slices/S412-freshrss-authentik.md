# S412: FreshRSS signs in through Authentik only

## Goal
Turn the `env-oidc` adapter on for FreshRSS 1.30.4 (Debian image,
`mod_auth_openidc`). Owner decision 2026-10-31: sign-in is exclusive, an
explicit exception to "each app's local admin keeps working" (ADR-052
amendment, S412).

## Facts (FreshRSS 1.30.4 source and docs)
- `Docker/FreshRSS.Apache.conf`: with `-D OIDC_ENABLED`, `/p/i/` gets
  `AuthType openid-connect` + `Require valid-user`; `OIDCRedirectURI /i/oidc/`;
  `OIDCClientSecret ${OIDC_CLIENT_SECRET}` and
  `OIDCCryptoPassphrase ${OIDC_CLIENT_CRYPTO_KEY}` come from the environment
  (no `_FILE` form). No CA directive, so libcurl/OpenSSL system CAs apply.
- `Docker/entrypoint.sh` defaults the session variables, enables the module,
  turns `:` in `OIDC_SCOPES` into spaces, then `exec "$@"`. ENTRYPOINT is
  `./Docker/entrypoint.sh`; CMD is the shell string
  `([ -z "$CRON_MIN" ] || cron) && . /etc/apache2/envvars && exec apache2 -D FOREGROUND $(... echo "-D OIDC_ENABLED")`.
- `cli/reconfigure.php` takes `--auth-type` (`form`, `http_auth`, `none`;
  `auth_type` is the deprecated spelling) and `--default-user`; it has no
  auto-register flag. `config.default.php`: `http_auth_auto_register` defaults
  to `true`, `default_user` is `_` until the installer sets it.
- `app/Models/Auth.php`: with `http_auth`, an unknown remote user is created
  when auto-register is on; admin access is `default_user === current user`
  or the user's `is_admin` flag.
- `p/api/` has its own `.htaccess` and no `AuthType`, so the Google-Reader/
  Fever API keeps API passwords.
- OIDC docs: `OIDC_X_FORWARDED_HEADERS` takes space-separated `Forwarded`,
  `X-Forwarded-Host`, `X-Forwarded-Port`, `X-Forwarded-Proto`; the edge
  (`internal/docker/routes.go`) sends X-Forwarded-Host and -Proto on every
  proxied request. Callback is `https://<host>/i/oidc/`.

## Change
- `catalog/freshrss.yaml`: `entrypoint` = the image ENTRYPOINT plus
  `sh -c <CMD>` (`$` written `$$` for Compose) so the supervisor can export
  `OIDC_CLIENT_SECRET` and `OIDC_CLIENT_CRYPTO_KEY` (two declared secrets,
  both `export: true`; the crypto key is generated once and only signs
  people out if changed). `sso.oidc` env-oidc sets `OIDC_ENABLED=1`,
  `OIDC_PROVIDER_METADATA_URL={{issuer}}.well-known/openid-configuration`,
  `OIDC_CLIENT_ID`, `OIDC_REMOTE_USER_CLAIM=preferred_username`,
  `OIDC_SCOPES=openid email profile`,
  `OIDC_X_FORWARDED_HEADERS=X-Forwarded-Host X-Forwarded-Proto`. The
  integration note no longer promises local accounts.
- Schema (`EnvOIDC`): `onapply`/`onoff` (static argv, both or neither, no
  placeholders), `execuser`, `exclusive`, `warnings`, `extrasecrets`,
  `trustbundle: system` + `trustpath`.
- Adapter: `Enable`/`Disable` exec the argv in the service container through
  `execIn` as `execuser` (retried while the recreated container starts);
  `configure-app` runs after the stack is up with the environment, and off
  runs `onoff` before the environment is removed. FreshRSS: `php
  cli/reconfigure.php --auth-type http_auth` / `--auth-type form` as
  `www-data`.
- Trust: for the appliance's own issuer only, `./homestead-ca/bundle.pem` (host
  CAs + appliance root, S410) is mounted read-only over
  `/etc/ssl/certs/ca-certificates.crt`. The file is rewritten in place so the
  single-file mount follows re-applies. A public issuer, a move to one, and
  sign-in off remove the mount and the bundle.
- Existing installs: `ensureDeclaredSecret` generates and wires the client
  secret and every `extrasecrets` entry (files first, then wiring), putting
  the service under the supervisor.
- Plan: warnings state the exclusivity, the admin rule (the Authentik person
  whose `preferred_username` equals FreshRSS's default user is the admin; the
  default user is chosen in FreshRSS's installer and is not read by the plan),
  that FreshRSS is unreachable while Authentik is, and that the installer must
  be finished first. Change lines replace "the local admin keeps working".

## Break-glass
Turn sign-in off through the normal plan and apply
(`homesteadctl apps sign-in plan freshrss --off`, then
`apply freshrss --digest D --off`, or the dashboard). Manual fallback if the
operation cannot run (container down, stack broken):
1. `docker start <container>` if needed, then
   `docker exec -u www-data <container> php cli/reconfigure.php --auth-type form`.
2. Remove the `OIDC_*` variables and the `bundle.pem` volume for `freshrss`
   from the stack's `compose.override.yaml` and run `docker compose up -d`
   in the stack directory to recreate the container.
3. If the container cannot start at all, edit `data/config.php` in the data
   volume (`'auth_type' => 'form'`), then step 2.

## Tests
`internal/appsso/freshrss_test.go` (schema validation, trust per issuer,
static argv through the exec seam, admin warnings and plan text, the full
apply/public/off lifecycle on an install from before the secrets);
`TestCuratedFreshRSS` pins the entrypoint and both secrets.

## Out of scope
Recorded in DEFERRED.
