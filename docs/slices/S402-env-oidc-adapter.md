# S402: env-configured apps sign in through Authentik

## Goal
Every app with user accounts signs in through Authentik. Immich,
Nextcloud and Jellyfin have bespoke adapters; this slice builds the
generic one for apps configured by environment variables. No real
manifest uses it yet (Mealie is next).

## Change
- Schema: adapter `env-oidc` with `sso.oidc.env` (redirect paths, sign-in
  path, declared secret, optional trust variable, `set` templates).
  Validation refuses bad env keys, `{{client_secret}}` and unknown
  placeholders, relative paths, and a secret that is not declared and
  consumed by the adapter's service. `env` is required for env-oidc and
  refused for the others.
- `appsso.envOIDC` adapter: redirect URIs and sign-in URL from the
  paths, admin-group role mapping, trust env, and an `EnvSetter` hook.
- `Definition` merges the `EnvSetter` result into the sign-in wiring
  (override file) on enable and removes it on disable, including when an
  issuer moves. The client secret step uses the declared package secret
  as the Authentik client secret instead of `sys_sso_app_<pkg>_secret`,
  creates it if missing and writes its file. A stack whose Compose file
  does not carry the secret yet is refused with guidance.
- ADR-052 section 4 amended; S342 `ActivateOIDC` marked superseded.

## Tests
Schema good/bad cases; adapter URLs, roles, trust and env; a full apply
and turn-off against a fake Authentik on a test manifest (keys written,
declared secret used, secret absent from plan, audit and override,
missing secret created, owner's env kept); unwired stack refused.

## Out of scope
See DEFERRED: per-app slices, removing dead S342 code, forward-auth.
