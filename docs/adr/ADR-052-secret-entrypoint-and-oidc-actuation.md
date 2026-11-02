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

### Amendment (2026-10-31, S411): Java trust store
A JVM app's `javax.net.ssl.trustStore` replaces the JDK's own CAs, so
`trustbundle: java` (env-oidc) sets the trust variable (`JDK_JAVA_OPTIONS`) to
the trustStore options for `bundle.jks` in the existing read-only CA
directory. The file is a JKS (public certificates only, mode 0644, store
password `changeit`) of the host's system CAs followed by the appliance root,
written by each sign-in apply with the standard library and removed with the
variable on a public issuer or sign-off. Applying a client secret to an
install that predates the supervisor now also wraps the exporting service in
the supervisor (entrypoint, command, script mount).

### Amendment (2026-10-31, S412): FreshRSS signs in through Authentik only
The owner chose to force FreshRSS onto Authentik. With `OIDC_ENABLED`, the
Debian image (`Docker/FreshRSS.Apache.conf`, 1.30.4) puts all of `/p/i/`, the
whole web UI including the login form, behind Apache's `mod_auth_openidc`,
and FreshRSS must run with `auth_type: http_auth`. FreshRSS's own form login
therefore stops working while sign-in is on. This is an explicit exception to
the rule that each app's local admin keeps working as a break-glass login
(ADR-039 consequences, ADR-040 consequences, ADR-043 section 3, ADR-047).
The feed API under `/p/api/` sits outside that directory and keeps its API
passwords.

- Why: the module cannot offer the form beside Authentik; the alternative,
  not offering Authentik for FreshRSS, was the owner's to decide and was
  decided the other way.
- Administrator: FreshRSS's administrator is its `default_user` (or any
  user flagged admin). The Authentik person whose `preferred_username` equals
  that username becomes that administrator; unknown people are registered as
  ordinary users at first sign-in (`http_auth_auto_register`, default true).
  The sign-in plan says this in a warning.
- Break-glass recovery: turn app sign-in off through the normal plan and apply
  (`homesteadctl apps sign-in plan freshrss --off`, then
  `apply freshrss --digest D --off`, or the dashboard). The off operation
  first runs `php cli/reconfigure.php --auth-type form` inside the container,
  then removes the OIDC environment and mounts. If the container cannot run
  the command, the manual fallback is in `docs/slices/S412-freshrss-authentik.md`.
- Mechanism: env-oidc gains declarative, static in-app commands
  (`onapply`, `onoff`, `execuser`; no placeholders, no shell), run through the
  same container exec the Nextcloud adapter uses, plus `exclusive` and
  `warnings` for plan text, `extrasecrets` (the session crypto key), and
  `trustbundle: system` with `trustpath` (the combined bundle mounted
  read-only over the image's own CA bundle for the appliance's own issuer).

### Amendment (2026-10-31, S413): a derived secret file for settings that embed the client secret
Paperless-ngx configures OIDC through one JSON variable,
`PAPERLESS_SOCIALACCOUNT_PROVIDERS`, that contains the client secret. A secret
inside a larger value cannot be a plain environment entry (it would sit in the
override file and `docker inspect`). The image's init honours `<VAR>_FILE`
for any `PAPERLESS_` variable, and the supervisor already exports a file's
content into the variable, so the least new mechanism is a secret whose value
Homestead renders:

- `SecretDecl.initial` is a fixed, non-secret starting value stored in place
  of a generated one (the secret must be `immutable`). For the provider JSON
  it is `{}`, Paperless's empty list, so a stack that never signs in still
  starts.
- `env.secretfile: {secret, template}` on env-oidc names that secret and a
  template with the usual placeholders plus `{{client_secret}}`, which is
  valid there and nowhere else (`set` still refuses it). Values are
  JSON-escaped when substituted.
- Sign-in apply renders the template into the secret store (`app_`, sealed,
  in backups) and rewrites the secret's 0600 file owned by the consuming user,
  before the stack is brought up; sign-in off restores the initial value and
  file. The client secret itself then has no consumer of its own.
- No secret appears in Compose, the override file, argv, logs or any
  world-readable file. A file change alone does not recreate a running
  container (the supervisor reads it at start); an issuer move that changes
  nothing else in the environment needs a restart (DEFERRED).
- Paperless keeps its local login form (`PAPERLESS_DISABLE_REGULAR_LOGIN` is
  never set), so the break-glass rule holds.

### Amendment (2026-10-31, S415): writing an app's own settings database
Calibre-Web Automated keeps its OAuth provider and login type only in rows of
its SQLite settings database (`/config/app.db`, tables `oauthProvider` and
`settings`); it has no environment, file or CLI configuration for them, and
reads them once at startup. The owner decided to write that database rather
than leave the app unconfigured. The rule that follows:

- An app's own settings database may be written only when the app has no
  other configuration path, from a declared `onapply`/`onoff` script that
  runs inside the app as its own user, updates named columns (never a
  positional insert), in one transaction with a busy timeout.
- The script carries a version guard (the app's own version marker) and a
  schema guard (every column it writes exists) and fails with a message that
  says to re-verify. The package version is pinned to the verified one by a
  Go test, so a version bump cannot land without re-verification.
- Values reach the script through a derived secret file (S413) and the
  service environment; none is in argv, Compose, the override file or logs.
  The client secret ends up in the app's database, which the plan says.
- `env.restart: true` restarts the service's container after the on or off
  command, for apps that read their settings only at startup. Arguments may
  span lines; the plan shows an inline script as `<n-line script>`.
- The app's local login stays (break-glass rule). Details:
  `docs/slices/S415-calibre-web-automated-authentik.md`.

### Amendment (2026-10-31, S416): the same rule for Gitea, with the image's sqlite3 shell
Gitea's only supported way to add an OAuth login source with a secret is
`gitea admin auth add-oauth --secret`, which puts the secret in argv (S391
found no other supported path and forbade direct database writes). The owner
decided to write Gitea's own database instead; S416 supersedes that S391
constraint. The S415 rule holds, with these differences:

- The image has `/usr/bin/sqlite3` and no Python, so the `onapply`/`onoff`
  script is an `sh -c` script (`execuser: git`) that drives the sqlite3 shell.
  No new engine mechanism: the sqlite3 function `readfile()` takes the client
  id, secret, discovery URL and admin group straight from the secretfile
  (`GITEA_SIGNIN_FILE`), so the secret is never an argument, never expanded by
  the shell, and never printed. The SQL itself carries no secret.
- The script writes only the row named `authentik` (type 6), in one
  `BEGIN IMMEDIATE` transaction with a 30 s busy timeout, and is idempotent
  (update in place, insert when absent). A row of that name whose
  `ClientID` is not `homestead-gitea` is foreign: `on` refuses, `off` leaves it
  alone. Off deactivates the row (`is_active=0`) and clears `ClientSecret`; it
  never deletes it, so accounts linked through `external_login_user` survive.
- The guards are `gitea --version` (must equal the verified version) and
  `PRAGMA table_info` for all nine `login_source` columns, with the same
  "re-verify" message. `TestGiteaSignInPinnedToVerifiedVersion` pins the package.
- Gitea registers OAuth providers only at startup, so `restart: true`. Gitea
  logs, but survives, a discovery URL that is unreachable at startup.
- Local login and local registration stay; denying a person (`none`) and closing
  registration are not enforced (DEFERRED). Details:
  `docs/slices/S416-gitea-authentik.md`.

### Amendment (2026-10-31, S417): the same rule for Audiobookshelf, with the image's node
Audiobookshelf keeps its OpenID settings only in the `server-settings` JSON row
of `settings` in `/config/absdatabase.sqlite`, reads them at startup, and its
settings endpoint (`/api/auth-settings`) logs the old and new values, client
secret included. The owner decided to write the database instead (S385 had
forbidden calling the endpoint with secrets). The S415 rule holds, with these
differences:

- The image has no Python and no sqlite3 shell but runs Node 24, so the
  `onapply`/`onoff` script is `node -e` using the built-in `node:sqlite`, as
  the image's own user (root). It reads the secretfile (`ABS_SIGNIN_FILE`)
  inside the process; the secret is never an argument and no error message
  quotes the file.
- It merges only the auth fields into the existing JSON (all other settings
  are kept) in one `BEGIN IMMEDIATE` transaction with a 30 s busy timeout, and
  is idempotent. The app does no discovery and drops `openid` unless every
  endpoint is set, so the secretfile carries the endpoints, filled from
  Authentik's fixed paths under the issuer. An OpenID setup whose client id is
  not Homestead's is foreign: `on` refuses, `off` leaves it.
- The guards are `/app/package.json` (must equal the verified version, `on`
  only), the `settings` columns and the presence of every auth field.
  `TestAudiobookshelfSignInPinnedToVerifiedVersion` pins the package, so each
  Audiobookshelf update forces a re-check of the schema, the writer and the
  logging.
- The app logs the signed-in person's profile (name, email, groups) when it
  auto-registers them; the owner accepted this, and the plan says so. The
  client secret is never logged. It restarts only (`restart: true`); the app
  never writes the row at shutdown, so no stop-write-start feature is needed.
- The app reads role names from the group claim (`admin`, `user`), so the
  manifest declares `adminvalue`/`uservalue`, which the groups claim mapping
  adds for admins and for everyone else. Existing accounts are never matched
  (`MatchExistingBy` null). Local login and the recovery administrator stay.
  Details: `docs/slices/S417-audiobookshelf-authentik.md`.

### Amendment (2026-11-01, S357): a secret mounted as a configuration file
Vikunja (a `FROM scratch` image, so no supervisor) reads its OpenID provider
list only from a config file; the provider's fields accept neither `*_FILE`
nor, without the file, environment values. A secret consumer may therefore
declare `path:` (an absolute, clean container path outside `/run/secrets`)
instead of `env:`. The generator renders the long Compose form
(`source: <key>`, `target: <path>`), so Docker bind-mounts the 0600 secret
file read-only at that path; `env`, `uri` and `export` are excluded, and
existing stacks are patched the same way as for an `env` consumer
(`WireDeclaredSecret`). With the S413 `secretfile` the file's content is the
rendered, JSON-escaped configuration (JSON is YAML), `{}` while sign-in is
off. Nothing about custody changes: the value is in the secret store and the
0600 file, not in Compose, the override, argv or the environment. Details:
`docs/slices/S357-vikunja.md`.

### Amendment (2026-11-01, S366): BookStack, a MariaDB sidecar and a Laravel key
BookStack signs in through native OIDC set by environment, with its client
secret, database password and `APP_KEY` delivered as files (`FILE__*`,
LinuxServer's convention; MariaDB's own `*_FILE`). Three points go beyond the
earlier amendments, none changing the custody rules:
- A generated secret may declare `encoding: laravel-key`: `base64:` plus 32
  random bytes, which Laravel requires for `APP_KEY`. The default stays 64 hex
  characters. The encoding is not combined with an initial value, reveal or
  the database password, and the value is never regenerated.
- Role mapping needs the app's own database: BookStack matches groups to roles
  by `roles.external_auth_id`, which no environment variable sets. The S415-S417
  rule applies unchanged to the `onapply`/`onoff` PHP script: version guard,
  schema guard, wait for migrations, one transaction, idempotent, never
  overwrites values it did not write, writes the two roles' IDs only, and a pin
  test (`TestBookStackSignInPinnedToVerifiedVersion`). The script connects to
  the sidecar with the password file already mounted for the app.
- A package's well-known seeded administrator is neutralised at install by a
  first-run routine (`bookstack`), independent of sign-in, so it holds when
  sign-in is off. Details: `docs/slices/S366-bookstack.md`.
