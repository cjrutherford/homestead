# App Sign-in: Back Channel, Trust, and Adapters

## Status
Proposed

Needs owner review.

Implements ADR-039 §3 (S298). Builds on ADR-030 (appliance CA), ADR-033
(dashboard sign-in with Authentik), ADR-021 (secret store) and ADR-035 (local
names).

## Context
An app that signs in through Authentik has to reach Authentik's issuer from
inside its container, for discovery, the token exchange and the JWKS. It
also has to trust Authentik's certificate. Authentik is served by the
appliance's own edge at a `.lan` name, under a certificate from the
appliance CA. Inside an app container that name does not resolve, and the
CA is not trusted. Each of the three apps keeps its OIDC settings
somewhere different. Applying sign-in also needs Authentik admin rights,
and Homestead does not keep an admin token unless the owner opted in at
dashboard sign-in.

## Decision
### 1. The back channel goes through the edge on the host
- The service that talks to Authentik gets
  `extra_hosts: <authentik host>:host-gateway` in the stack's
  `compose.override.yaml`. Its traffic reaches the edge, which publishes
  443 on the host, and is served by the same Authentik route the browser
  uses. The issuer URL is identical inside and outside the container, so
  the issuer check holds without any rewriting.
- No container network is shared between stacks. Docker Compose without
  Homestead keeps working, because the override is an ordinary Compose
  file.

### 2. Trust is the appliance root, mounted read-only
- The public appliance root is written to `<stack>/homestead-ca/ca.crt`.
  It is mounted at `/etc/homestead/ca` read-only. No private key leaves
  Homestead.
- Each runtime is told to trust it in its own way:
  - **Immich (Node):** `NODE_EXTRA_CA_CERTS`.
  - **Jellyfin (.NET):** `SSL_CERT_DIR` with the system directory first.
  - **Nextcloud:** `occ security:certificates:import`.
- Turning sign-in off removes exactly these override entries and the CA
  directory. The owner's own environment entries stay.

### 3. Adapters live in Go; the manifest names one
- A manifest declares `sso.oidc: {adapter, service, plugin?}`. There are
  three adapters: `immich`, `nextcloud-user-oidc` and
  `jellyfin-sso-plugin`. Unknown adapters fail validation.
- **Immich:** an upsert of the `oauth` block in `system_metadata`
  ('system-config') through `psql` in the database container, then a
  restart of the server. The SQL travels on stdin.
- **Nextcloud:** `occ` as `www-data`:
  - enable or install `user_oidc`;
  - import the CA;
  - set `allow_multiple_user_backends` and `allow_local_remote_servers`;
  - create the `authentik` provider, with the secret read from stdin.
  Off deletes the provider and disables the app.
  `allow_local_remote_servers` is left on, because other apps may rely on
  it. The plan says so.
- **Jellyfin:** download the pinned release zip, verify its sha256 before
  anything else, and unpack only `meta.json` and `.dll` entries, with no
  paths. Other `SSO-Authentication_*` directories are removed, the plugin
  is copied into `/config/plugins/`, and `SSO-Auth.xml` is written through
  stdin. Then the server restarts. Off rewrites the configuration with no
  providers. The plugin stays installed, inert.
- No secret is ever placed in a command's arguments, so it never shows up
  in `ps` or `docker inspect`.

### 4. Names, state, and the token
- **Names per app:**
  - Client ID and application slug: `homestead-<pkg>`.
  - Provider: `Homestead: <Title>`.
  - Client secret: sealed as `sys_sso_app_<pkg>_secret`, reused on
    re-apply.
- **Redirect URIs:** one for each of the app's edge names (`AllNames`, so
  `.lan`, `.local` and tunnel names). A change of names shows on the app
  page as a re-plan for approval.
- **State:** the applied state is the SQLite table `app_sign_in`
  (migration 50): adapter, client ID, issuer, redirect URIs, plugin version
  and actor. It never holds a secret.
- **The admin token:**
  - It comes from the apply request and is used only in memory, or from
    the sealed dashboard token if the owner kept it (ADR-033).
  - Without either, apply answers 422 and asks for one.
  - It is never stored, logged or audited.
- **Removal:** deletes only the Authentik provider and application whose
  client ID matches Homestead's. It needs Authentik to be reachable.

### 5. Routes
`GET /api/v1/catalog/{name}/sign-in`, and `POST .../sign-in/plan` and
`.../sign-in/apply`. All three are session class.

Apply is not a step-up route. It changes one app's sign-in, not the
dashboard's, and the install wizard chains it after install.

The install plan carries a `sign_in` preview built from the routes the
wizard adds. The wizard applies that preview's digest after the routes
land. If the names changed in between, apply answers 409 and the owner
reviews the plan on the app page.

## Consequences
- Each app's edge route and Authentik's must both exist. Without an
  address, sign-in is not offered.
- Containers resolve the Authentik name to the host gateway. If the edge
  is down, sign-in to the app fails. The local admin account still works.
- Uninstalling an app does not remove its Authentik objects (recorded in
  DEFERRED).
- Jellyfin's plugin release targets a Jellyfin ABI. A Jellyfin update
  without a matching plugin pin can break sign-in until the catalog
  updates the pin.

## Alternatives Considered
- **Joining app containers to the Authentik network** and using the
  internal URL: the issuer would differ from the browser's, which breaks
  the issuer check. It would also couple the stacks.
- **Rewriting each image's CA bundle:** this changes images and does not
  survive pulls.
- **Declarative adapter steps in YAML:** this is a scripting language in the
  catalog, and harder to review than three small Go adapters.
