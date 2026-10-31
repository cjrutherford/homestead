# S404: env-oidc sign-in leaves the owner's settings alone

## Goal
Fixes from the review of S402/S403.

## Change
- One placeholder reader (`catalog.RenderEnvTemplate`, `EnvTemplateUses`)
  serves validation and rendering, so `{{ issuer }}` renders.
- `ApplySignInWiring` removes a variable on off only while it holds the
  value sign-in set, and reports kept and replaced keys (audited). On on,
  sign-in's value wins; the plan warns "<KEY> set by the owner is
  replaced while sign-in is on" (`Inputs.OwnerEnv`). A moved issuer
  removes the old values first, then sets the new ones.
- The client-secret step error no longer prints `<nil>`.
- Validation refuses `set` keys that collide with the service's manifest
  environment or a secret consumer variable.
- Rotating the env-oidc client secret of an applied app returns a
  warning (Authentik keeps the old secret until sign-in is applied again).
- Mealie's keys, `_FILE` secret variant and redirect paths were checked
  against mealie.io (OIDC_CLIENT_SECRET supports `_FILE`); no change needed.

## Tests
Spaced placeholders; owner value kept on off, replaced with a warning on
on; local-to-public move without duplicates; key collisions.
