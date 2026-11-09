# S342 — OIDC Client Actuation and Supervised Secret Delivery

## Goal
Make SSO and plaintext-variable packages installable from the catalog:
let a manifest declare the OIDC client it needs, actuate that client at
the identity provider, store the issued credentials as catalog secrets,
and deliver secret values to images that have no `*_FILE` input.

Foundation item F1 in `docs/CATALOG_EXPANSION.md`. Wave 1 packages are
gated on this slice.

## Dependencies
S202 (identity provider interface), S203 (SSO metadata), S204 (SSO
setup wizard), S266 (secret-backed container variables), S276, S341
(catalog integration contract). All complete.

## Scope

### Supervised secret export (`internal/catalog`)
- **`Service.Entrypoint []string`**: the image's own entrypoint, needed
  when Homestead has to supervise startup. Empty arguments refuse.
- **`SecretConsumer.Export bool`**: deliver the value into the
  environment at container start instead of exposing only the path.
- **Validation** refuses, with the offending secret named: an exporting
  consumer on a service without a declared entrypoint; an `Env` that
  does not end in `_FILE`; an export target that the manifest also sets
  as a plain variable; two secrets exporting the same variable on one
  service; and an export combined with `URI`.
- **`EntrypointScriptsFor`** renders one supervisor per exporting
  service. Each fails closed on a missing or unreadable file, reads the
  value, exports it, unsets the path variable, and `exec`s the image
  entrypoint plus command. The script holds paths, never values.
- **Compose rendering** sets `entrypoint: [/homestead/secret-entrypoint.sh]`,
  moves the declared entrypoint plus `command:` into the exec list, and
  bind-mounts the script read-only from the stack directory.
- **`WriteStack`** writes the scripts at 0755 beside `compose.yaml`.

### OIDC client actuation (`internal/identity`)
- **`ClientSpec`** (package, slug, name, redirect URIs, scopes) with
  shape validation: absolute redirect URIs only.
- **`Client`** reports the issued client id, the one-time secret, the
  provider primary key, and whether it was created.
- **`ClientManager`** is separate from `Provider`: read-only metadata
  and live writes stay separable, so a setup flow can describe a
  provider without holding write access.
- **`Authentik`** implements `Provider` (name, capabilities, credential
  fields, `CheckConnection`) and `ClientManager` over Authentik's API:
  flow lookup by slug, existing-application reuse with a redirect
  refresh, provider and application creation, orphan-provider cleanup
  when the application cannot be created, and repeatable deletion.
  Errors carry the method, path, and status only — never a response
  body, which may echo request data.
- **`TokenSecretName`** names the secret-store row holding a provider's
  API token.

### Catalog declaration and orchestration (`internal/catalog`)
- **`SSODeclaration.OIDC *OIDCClient`** declares redirect paths, scopes,
  an optional slug, and the two secrets that receive the issued
  credentials. Validation requires an SSO class that admits SSO, the
  `oidc` protocol, declared credential secrets, and a web endpoint.
- **`Manifest.RedirectURIs(host)`** and **`Manifest.OIDCClientSpec(host)`**
  resolve the declaration against a route host; an empty host refuses,
  because the provider would accept a redirect the app never serves.
- **`ActivateOIDC`** ensures the client, stores the issued id and
  secret as `app_<package>_<name>` rows, is idempotent, keeps the stored
  secret on reuse, and refuses when the provider holds a client whose
  secret was never stored.
- **`DeactivateOIDC`** removes the provider client and the stored rows,
  repeatably.

## Non-goals
- Provider endpoint and token configuration, the owner-facing API/CLI
  command, and the dashboard control. They belong to the first slice
  that installs an SSO package; this slice lands the mechanism they
  call. Recorded in `docs/DEFERRED.md`.
- Any package manifest change. Wave 1 packages add `export: true`,
  `entrypoint:`, and OIDC declarations when they ship.
- SAML and LDAP client actuation, secret rotation at the provider, and
  authorisation-group mapping.
- Health checks and start ordering.

## Acceptance Criteria
- An exporting service renders a supervisor entrypoint, keeps its
  original entrypoint and command, and mounts the script read-only.
- A supervisor exports the value, drops the path variable, and fails
  closed with a named error when the file is missing.
- A manifest exporting a secret without an entrypoint, with a
  non-`_FILE` variable, or with a plain collision refuses to validate.
- The Authentik client creates a provider and application with strict
  redirect entries, reuses an existing client while refreshing
  redirects, cleans up an orphan provider, and deletes repeatably.
- Activation stores issued credentials as catalog secrets; a second run
  keeps them; a lost secret refuses instead of silently reissuing.
- No secret value appears in Compose YAML, generated scripts, results,
  or errors.
- Main build remains green and all existing tests pass.

## Required Documentation
- `docs/adr/ADR-052-secret-entrypoint-and-oidc-actuation.md` (Accepted,
  amends ADR-021) — the supervised export trade and the actuation model.
- `docs/adr/ADR-051-catalog-integration-metadata.md` (Accepted) — the
  ADR S341's slice doc referenced.
- `docs/DEVELOPMENT.md` — identity and secret-delivery sections.
- `docs/DEFERRED.md` — the surfaces this slice deliberately leaves out.

## Handoff
Wave 1 packages can now declare OIDC clients and plaintext-variable
secrets with no further foundation work. The next slice is S343.
