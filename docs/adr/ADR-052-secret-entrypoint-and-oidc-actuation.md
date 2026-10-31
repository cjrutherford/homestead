# Secret Entrypoint Export and OIDC Client Actuation

## Status
Accepted

Accepted by the owner as part of the catalog expansion plan
(`docs/CATALOG_EXPANSION.md`, F1). Amends ADR-021.

## Context
ADR-021 keeps secret values out of Compose YAML, out of the container
environment, and out of everything Homestead writes: each value reaches
a container as a mode 0600 file under the stack directory, and images
read it through their own `*_FILE` convention.

Two kinds of catalog package cannot live with that rule:

1. Images with **no `*_FILE` input at all** — they read a plaintext
   variable and nothing else. Without a way to deliver the value they
   cannot be configured by Homestead, only by editing files inside the
   container, which the catalog forbids.
2. **Single sign-on**, where the client id and secret are issued by an
   external identity provider *after* install rather than generated at
   install. A value Homestead did not generate cannot be known when the
   Compose document is rendered.

## Decision

### 1. Supervised export is the only way a value reaches an environment
A manifest may mark a secret consumer `export: true`. The consumer's
`Env` stays the `*_FILE` carrier (the variable that holds the path, as
in ADR-021) and the exported variable is that name without the suffix.

For every service with at least one exporting consumer, the generator
writes `secret-entrypoint.<service>.sh` into the stack directory and
renders the service as:

```yaml
entrypoint: ["/homestead/secret-entrypoint.sh"]
command: ["<image entrypoint>", "<image command>"]
volumes:
  - type: bind
    source: ./secret-entrypoint.webserver.sh
    target: /homestead/secret-entrypoint.sh
    read_only: true
```

The script fails closed when a file is unreadable, reads each value into
its variable, exports it, unsets the path variable, and `exec`s the
image's own entrypoint. It contains paths, never values, so it is safe
to inspect, back up, and regenerate.

### 2. The manifest must name the image entrypoint
Homestead never guesses an image's entrypoint. A service that exports a
secret must declare `entrypoint:`, which the supervisor execs along with
the service's `command:`. Validation refuses an exporting consumer on a
service that declares no entrypoint, refuses an `Env` without the
`_FILE` suffix, refuses a target that the manifest also sets plainly,
and refuses two secrets exporting the same variable on one service.

### 3. What the export changes about custody
The exported value exists in the container's own process environment
from start onwards. That is visible to processes inside that container
(including `/proc/<pid>/environ` for its own user) — the trade the owner
accepted to make these packages configurable at all.

The original guarantees hold everywhere else: no value in Compose YAML,
in generated scripts, in the secret listing API, in audit records, in
drift reports, or in exports. `docker inspect` shows the path variable,
not the value, because the value is set after the container starts.

Prefer `_FILE` delivery whenever an image offers it; `export: true` is
the sanctioned exception, not the default.

### 4. OIDC clients are actuated, credentials are catalog secrets
Homestead creates the OIDC client at the identity provider through the
provider's API and stores the issued client id and secret as
catalog-managed secrets (`app_<package>_<name>`), delivered to the app
through the supervisor above.

- The manifest declares redirect paths, optional scopes, and the names
  of the two secrets that receive the credentials. It never holds them.
- Redirect URIs are resolved against the package's route host at
  actuation, so a route change refreshes them at the provider.
- Providers return a client secret only once. Re-activation keeps the
  stored secret; if the provider holds a client whose secret Homestead
  never stored, activation refuses and tells the owner to remove it at
  the provider instead of silently issuing a credential the app cannot
  read.
- Deactivation removes the provider application and the stored rows,
  and is repeatable when the client is already gone.

### Amendment (S402): env-configured apps sign in through the appsso plan

(a) An app that is configured by environment variables alone takes its
Authentik sign-in through the same plan, approval and apply as every
other app (ADR-040, ADR-047 issuer choice, ADR-043 groups), using the
generic `env-oidc` adapter. The manifest's `sso.oidc.env` block names
the redirect and sign-in paths, the declared secret, an optional trust
variable for the appliance root, and the variables to set. Templates may
use only `{{issuer}}`, `{{client_id}}`, `{{admin_group}}` and
`{{user_group}}`; `{{client_secret}}` and unknown placeholders are
refused at load. The variables are written to `compose.override.yaml`
with the rest of the sign-in wiring and removed again on turning off.

(b) The client secret is a declared package secret (for example
`oidc_client_secret`), generated by Homestead like any other, handed to
Authentik as the client's secret, and delivered to the app by `*_FILE`
or, with `export: true`, by the supervisor above. It does not appear in
plans, audit, logs, argv, the override file or `docker inspect`. Apply
creates it when an older install lacks it; a stack that predates the
declaration must first be reinstalled or configured so its Compose file
carries the secret.

(c) The S342 `catalog.ActivateOIDC`/`DeactivateOIDC` path, which created
clients differently and stored the issued credentials, is superseded and
unused. Its removal is deferred.

## Consequences
- Wave 1 packages that read only plaintext variables become configurable
  without file surgery, and SSO becomes installable from the catalog.
- Each exporting package needs one more manifest field (its image
  entrypoint) and one generated file per service, both inspectable.
- The daemon must reach the identity provider to actuate. Provider
  endpoint and token configuration plus the owner-facing command belong
  to the slices that schedule the first SSO package; this ADR records
  only the mechanism they use.

### Amendment (2026-10-31, S410): combined trust bundle
An env-oidc app whose trust variable replaces its whole trust store (Linkding's
`REQUESTS_CA_BUNDLE`) may declare `trustbundle: combined`. For the appliance's
own (private) issuer the variable then points at `bundle.pem` in the existing
read-only CA directory: the host's system CA bundle followed by the appliance
root, regenerated atomically (0644, public certificates only) by each sign-in
apply and removed with the variable when the issuer turns public or sign-in
turns off. Without the opt-in the variable points at the bare root as before.
