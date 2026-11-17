# S409: Linkding signs in through Authentik

## Goal
Turn the `env-oidc` adapter (S402) on for Linkding 1.47.0, which uses
mozilla-django-oidc, and make sign-in apply work for a Linkding installed
before the manifest declared the client secret.

## Facts (checked against Linkding v1.47.0 `bookmarks/settings/base.py`)
- `LD_ENABLE_OIDC` turns the backend on; endpoints, client id and secret
  come from `OIDC_OP_*` and `OIDC_RP_*` variables. There is no discovery.
- `OIDC_RP_SIGN_ALGO` already defaults to RS256; it is set explicitly.
- `OIDC_USERNAME_CLAIM` defaults to `email`; Homestead sets
  `preferred_username` so created accounts get a readable name. Existing
  accounts still match by email (mozilla-django-oidc default).
- `LD_CSRF_TRUSTED_ORIGINS` is split on commas, without trimming.
- Sign-in starts at `/oidc/authenticate/` and returns to `/oidc/callback/`.

## Change
- `catalog/linkding.yaml`: `sso.oidc` (env-oidc) setting `LD_ENABLE_OIDC`,
  the four endpoints, client id, `OIDC_RP_SIGN_ALGO=RS256` and
  `OIDC_USERNAME_CLAIM`. Trust variable `REQUESTS_CA_BUNDLE`, set only for
  the appliance's own issuer (`TrustEnvFor`, S405).
- New env-oidc placeholder `{{authentik_base}}` (the issuer without
  `/application/o/<slug>/`), validated like the others. Endpoints use it;
  JWKS is `{{issuer}}jwks/`.
- Secret `oidc_client_secret`, consumed by `linkding` as
  `OIDC_RP_CLIENT_SECRET_FILE` with `export: true`. The existing supervisor
  reads the 0600 file and exports `OIDC_RP_CLIENT_SECRET`; the value is
  never in Compose, argv, the override file or logs. The file is owned by
  the data owner (33:33).
- `LD_CSRF_TRUSTED_ORIGINS` is `{{route_url}},{{app_url}}`: the local
  origin plus the published one (equal, so harmless, until a public name
  exists). S407's refresh keeps it current.

## Existing installs
`catalog.WireDeclaredSecret` patches compose.yaml as for Mealie (S403) and
now also regenerates the supervisor script of a service that exports the
secret, since the old script does not read the new file.
`SecretConsumerWired` reports unwired while that script is stale.

## Tests
Real manifest validates and is pinned in `TestCuratedLinkding`; plan and
apply on an old stack (redirect, sign-in URL, endpoints, secret export in
the supervisor, no secret value in any file, re-baseline, idempotent,
stale supervisor repaired); trust by issuer; `authentikBase`.

## Out of scope
See DEFERRED.
