# S403: Mealie signs in through Authentik

## Goal
Turn the S402 `env-oidc` adapter on for Mealie, and make sign-in apply
work for a Mealie installed before the manifest declared the secret.

## Change
- `catalog/mealie.yaml`: `sso.oidc` with adapter `env-oidc`, service
  `mealie`, redirect paths `/login` and `/login?direct=1`, sign-in path
  `/login`, trust variable `OIDC_TLS_CACERTFILE` (never `SSL_CERT_FILE`).
  Env set: `OIDC_AUTH_ENABLED`, `OIDC_CONFIGURATION_URL`, `OIDC_CLIENT_ID`,
  `OIDC_PROVIDER_NAME=Authentik`, `OIDC_SIGNUP_ENABLED=true` (Authentik
  groups gate access), `OIDC_AUTO_REDIRECT=false` (the local administrator
  keeps working), `OIDC_REMEMBER_ME=true`, `OIDC_GROUPS_CLAIM=groups`,
  `OIDC_ADMIN_GROUP` and `OIDC_USER_GROUP` from the app's Homestead groups
  (ADR-043).
- Declared secret `oidc_client_secret`, consumed by `mealie` as
  `OIDC_CLIENT_SECRET_FILE` (Mealie reads the secret from a file; no
  export, no entrypoint). `BASE_URL` is untouched.
- Install access mode is now `managed`, not `self-managed`.

## Existing installs
Configure does not regenerate a stack, and a full regeneration needs the
install-time volumes, networks and route hosts, so rewriting the whole
file is risky. Instead `catalog.WireDeclaredSecret` patches the stack's
`compose.yaml` with only the missing wiring: the top-level secret, the
service's reference and the file-path variable. Everything else, and the
provenance header, stays. The secret file is written first, drift is
re-baselined afterwards, and the app is recreated by the sign-in step.
The plan says so ("<App>'s stack file is rewritten to deliver the sign-in
secret"). If the file cannot be patched, apply refuses and tells the owner
to reinstall.

## Tests
Real Mealie manifest validates; plan and apply against a fake Authentik
(redirect URIs, env keys, secret as a file, not in plan, compose or
override); old-stack case (plan notice, patched compose only adds lines,
re-baseline, second wiring is a no-op); install access mode is managed.

## Out of scope
See DEFERRED.
