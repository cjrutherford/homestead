# Development Guide

S002 repository bootstrap. Toolchains and layout for local development.

## Prerequisites

| Tool | Required | Notes |
|------|----------|-------|
| Go | 1.26+ | `go build`, `go test`, `go vet` — no external Go dependencies yet (stdlib only) |
| Node.js | ≥ 22.22.3 (24 LTS recommended) | Angular 22 requirement; older Node cannot run the dashboard toolchain |
| pnpm | 12.x (pinned `pnpm@12.9.1` via `packageManager`) | Dashboard package manager; `corepack prepare pnpm --activate` or standalone |
| Python | 3.x | Constitution/ADR/bootstrap conformance tests in `tests/` |

## Layout

```text
cmd/homestead-server/   homestead-server binary (HTTP skeleton in S010; see ADR-001)
cmd/homesteadctl/       CLI: status/version/config over the API (S018; ADR-004)
internal/server/        daemon HTTP surface: health/version + graceful shutdown (S010)
internal/layout/        authoritative filesystem layout + provisioner (S012)
internal/store/         versioned SQLite operational state (S013; ADR-003)
internal/desired/       versioned YAML desired-state storage (S014; ADR-003)
internal/schema/        typed configuration metadata + validation (S015; ADR-004)
internal/config/        validated configuration service: get/plan/apply (S016)
internal/apiclient/     reusable Go API client for local consumers (S017; ADR-004)
internal/claim/         single-use claim tokens + UNCLAIMED→CLAIMED (S019; ADR-015)
internal/dashboard/     production UI serving + embed split (S031)
internal/auth/          first administrator + session login/logout (S020)
internal/discover/      read-only host facts: OS today, hardware/network/storage next (S040+)
internal/operation/     operation records: model, state machine, CRUD store (S060; runner is S061)
internal/docker/        Engine plus Compose v2 versions via CLI (S070; ADR-005), Debian install operation (S071), stack model plus runner-backed lifecycle (S074/S075), platform networks (S080), stack model (S074)
internal/firewall/      nftables detection, domain model, table generation (S050-S052; ADR-008)
internal/               shared Go packages (API client arrives in S017)
dashboard/              Angular 22 dashboard app, pnpm-managed (real UI arrives in S030+)
tests/                  Python conformance tests (S000 constitution, S001 ADR, S002 bootstrap)
docs/                   product, architecture, ADRs, slices, development guide
assets/                 brand assets
```

## Commands

```bash
make test               # all tests: Python conformance + go test ./...
make test-py            # Python conformance tests only
make test-go            # go test ./... only
make build              # go build ./...
make lint               # gofmt check + go vet (S002 lint baseline)
make dashboard-install  # pnpm install --frozen-lockfile in dashboard/
make dashboard-build    # ng production build in dashboard/
make check              # lint + build + test
```

## Running homestead-server (S010 skeleton)

```bash
go run ./cmd/homestead-server                 # binds 127.0.0.1:8080 (loopback only)
go run ./cmd/homestead-server --listen 127.0.0.1:9090
curl localhost:8080/healthz                   # {"status":"ok","version":"..."}
curl localhost:8080/version                   # {"version":"..."}
```

Ctrl-C (or SIGTERM) drains connections gracefully (5s timeout) and exits 0.
Only `/healthz` and `/version` exist: per ADR-015 an unclaimed appliance
exposes health/identity/claim and nothing else — claim (S019) and the admin
API arrive in later slices.

## Operational state (S013)

SQLite via `modernc.org/sqlite` (pure Go, no cgo — first external Go
dependency, chosen so the daemon stays a static binary per ADR-001):

- Database lives at `/var/lib/homestead/homestead.db` (`store.DefaultPath`).
  The daemon never creates the parent: S012 owns directories, the store fails
  loudly without them.
- Schema is versioned (`schema_migrations`, `store.CurrentVersion`); v1
  creates skeleton `operations`, `identities`, `sessions`, `onboarding`
  tables. Owning slices extend them through new migrations.
- Connections: `MaxOpenConns(1)`, WAL + `foreign_keys=ON` + `busy_timeout`.

## Desired state (S014)

YAML via `gopkg.in/yaml.v3` (second direct Go dependency; pure Go):

- Desired state lives at `/etc/homestead/desired.yaml`
  (`desired.DefaultPath`). Like the store, load/save fail loudly without
  the S012 parent directory.
- `desired.State` is a versioned envelope (`version` field, currently 1).
  Loads reject newer versions and unknown fields (strict parsing); saves are
  atomic (temp file + `fsync` + rename, mode 0644).
- The settings schema inside the envelope arrives with S015; the serving API
  with S016. Secret *values* never live here — credentials stay in the
  secret store (S096), referenced by ID.

## Configuration schema (S015)

`internal/schema` (ADR-004: one source for CLI and dashboard):

- Types: `string`, `integer`, `boolean`, `choice`. Validation is strict and
  non-coercing (no `"yes"` → `true`); integers accept `int`/`int64` with
  optional `Min`/`Max`; choices must list non-empty options.
- Fields carry `Description`, `Group`, `Required`, `Default`, plus UX hints:
  `Sensitive` (mask in UI, redact in logs) and `Advanced` (hide in simple
  views, still offered by wizards).
- `Schema.ValidateDefinition` checks schema authorship (names, types,
  choices/bounds placement, valid defaults, no duplicates); `Validate`
  checks values (unknown keys, missing required, type/bounds/choice) with
  stable field-sorted errors; `Defaults` supplies unset defaults.
- The framework defines no appliance settings itself — domain slices add
  their fields, S034 renders them, S016 serves them. Settings travel inside
  `desired.State.Settings`.

## Configuration API (S016)

`internal/config.Service` over a schema + file, mounted at (ADR-004: the
same routes serve CLI and dashboard):

```bash
curl localhost:8080/api/v1/config                          # read
curl -X POST localhost:8080/api/v1/config/plan \            # dry-run
  -d '{"settings":{"hostname":"homestead"}}'
curl -X PUT localhost:8080/api/v1/config \                 # validate + persist
  -d '{"settings":{"hostname":"homestead"}}'
```

- Valid plans/reads answer 200; invalid candidates answer 422 with
  `{"valid":false,"errors":[{"field","reason","value"}]}` and persist
  nothing; malformed bodies answer 400.
- The daemon currently wires an empty schema (no domain fields registered
  yet), so non-empty settings 422 as unknown until domain slices land.

## Go API client (S017)

`internal/apiclient` (ADR-004: `homesteadctl` in S018 uses this, never raw
HTTP): `Healthz`, `Version`, `GetConfig`, `PlanConfig`, `ApplyConfig` — all
context-propagated with a 10s default timeout (`WithTimeout`), swappable
transport (`WithHTTPClient`, the future Unix-socket hook). Transport
failures return `*StatusError`; validation outcomes stay data, mirroring
`internal/config`.

## homesteadctl (S018)

```bash
go run ./cmd/homesteadctl status                        # via local daemon
HOMESTEAD_SERVER=http://127.0.0.1:8080 homesteadctl status
homesteadctl --server http://host:8080 config get
homesteadctl config plan -f settings.json               # dry-run, exit 1 if invalid
homesteadctl config apply -f settings.yaml              # persist, exit 1 if invalid
homesteadctl admin recover --username owner --password-file ./new.pass  # root-only socket (S021)
```

- Exit codes: 0 success, 1 runtime/API/validation failure, 2 usage error.
- `-f` takes JSON or YAML by extension (bare settings or `{"settings": …}`);
  no `--set` flags — flat strings cannot carry typed values.
- Everything goes through `internal/apiclient`; the CLI never reads state
  files or the database. `claim` (S019), `admin setup` (S020), and
  `admin recover` (S021) extend the S018 foundation.

## Secure claim (S019)

```bash
mkdir -p /tmp/hs-data   # dev only: production uses provisioned /var/lib/homestead
go run ./cmd/homestead-server --data-dir /tmp/hs-data --claim-token-file /tmp/hs-data/claim-token
# ... the journal/console says once that the appliance is UNCLAIMED; the token
# itself (single use, 30min TTL) is only in the root-only token file
curl localhost:8080/api/v1/identity              # {"state":"UNCLAIMED",...}
go run ./cmd/homesteadctl claim token --file /tmp/hs-data/claim-token   # prints token + expiry
# on an installed appliance: sudo homesteadctl claim token
go run ./cmd/homesteadctl --server http://127.0.0.1:8080 claim <TOKEN>
curl localhost:8080/api/v1/identity              # {"state":"CLAIMED",...}
```

- Tokens are 24-byte crypto/rand, base64url, SHA-256 hashes in SQLite,
  constant-time compared, expired ones pruned on mint. Since S288 the daemon
  never prints a token: it keeps one live token in `/run/homestead/claim-token`
  (systemd RuntimeDirectory, tmpfs, mode 0600, replaced atomically, removed on
  claim) and re-mints on a timer when it expires. Read it with
  `sudo homesteadctl claim token`.
- The daemon never creates the state directory: without provisioned
  `/var/lib/homestead` (or an existing `--data-dir`) it exits loudly.
- Claim carries no credentials — the first administrator and sessions
  arrive with S020 on top of the CLAIMED state.

## Administrator authentication (S020)

```bash
homesteadctl admin setup --username owner --password-file ./owner.pass
homesteadctl login --username owner --password-file ./owner.pass   # prints + stores the session
homesteadctl logout                          # revokes it and deletes the stored file
```

- Setup succeeds exactly once: CLAIMED state, no administrator yet (403
  unclaimed, 409 exists, 422 weak input). Usernames match
  `[a-z][a-z0-9_-]{0,63}`; passwords minimum 12 characters.
- Passwords are bcrypt hashes; sessions are SHA-256-hashed tokens in
  SQLite (lifetime and transport: see "Session transport (S256)"). Login never distinguishes unknown users
  from wrong passwords. Password files should be 0600.
- Sessions are created here and required by the operator session boundary
  (S257). Recovery is S021.

## Operator session boundary (S255, enforcing since S257)

- `internal/server/boundary.go` wraps the TCP mux (never the Unix socket)
  in a deny-by-default classifier (ADR-020). `routeClasses` declares every
  route as public (7: health, version, identity, claim, first-admin setup,
  session create, dashboard `/`), claim-window (3 wizard routes, open only
  until claim completes) or session (48).
- Enforcing: a request that fails the boundary is answered by the
  middleware and never reaches a handler. Status codes: `401` no, invalid,
  expired or revoked session (also a wizard route after claim completes,
  reasons `no-session`, `invalid-session`, `claim-window-closed`); `403`
  before claim for anything but the public allowlist and wizard routes
  (`pre-claim`), an unclassified route (`unclassified-route`), or a
  cookie-authenticated POST/PUT/PATCH/DELETE with a missing or wrong
  `X-CSRF-Token` (`csrf-missing`, `csrf-mismatch`; bearer is exempt);
  `503` when claim state or the session store cannot be read (fail
  closed). Rejections are counted per route and reason and flushed by a
  background ticker as one `session.boundary.rejected` audit entry (plus a
  log line) per minute with the count; requests themselves never write
  audit, and the ticker's final flush completes before `Run` returns (so
  before stores close). The method-less dashboard catch-all `/` is public
  only for GET/HEAD outside `/api/`; any other fallthrough (unregistered
  method, unknown API path) is classified as a session route. Tokens and
  cookies are never recorded.
- Handlers read the verified session from the request context
  (`operatorFromContext`; `handleLogout`, `handleChangePassword` and
  `handleLockoutStatus` no longer check credentials themselves). Long-lived
  SSE streams call `sessionStillValid` on each heartbeat
  (`EventHeartbeatInterval`) and stop once the session is revoked or
  expired. Revocation latency: a revoked session can keep an open
  stream for up to one heartbeat (30s) before it is dropped (tracked in
  `docs/DEFERRED.md`).
- `POST /api/v1/admins/password` answers a wrong current password with
  401 plus `X-Homestead-Error: invalid-current-password`; the boundary's
  session 401 carries no code, so clients (`homesteadctl admin passwd`)
  tell them apart by the header, not by body text.
- First-admin setup (`POST /api/v1/admins/setup`) logs the owner in: the
  201 response sets the cookies and carries `token`, `csrf_token` and
  `expires_at` like login, so the claim wizard's later saves and the CLI's
  next command are authenticated. The Unix-socket recovery route stays
  session-free.
- Tests: handler tests that exercise a route in isolation mount the
  handler without the boundary; anything about access control mounts the
  real boundary (`MountSessionBoundary`) with real claim and auth services
  and logs in (see `internal/server/enforcement_test.go`).
- Production routes are mounted by `server.MountApp`, shared by
  `cmd/homestead-server` and the classification test so wiring cannot
  drift. Add the pattern to `routeClasses`; `TestEveryRegisteredRouteIsClassified` enumerates the
  real registrations (58 today) and fails on any unclassified or stale
  entry.

- Proven live in KVM (S255-S257, fb8e88b): in-place upgrade of a
  running appliance and a fresh install of a second one. Unauthenticated
  API 401 (403 pre-claim; wizard routes reachable only pre-claim),
  cookie login with HttpOnly session cookie, CSRF 403 without or with a
  wrong header and success with it, bearer exempt, CLI login (0600
  session file) and logout revocation, SSE stream ending within one
  heartbeat of logout, root-socket recovery without a session (revokes
  old sessions), aggregated credential-free `session.boundary.rejected`
  audit entries, installed apps undisturbed, two concurrent first-admin
  setups with different usernames yielding exactly one 201 and one 409,
  and `scripts/acceptance.sh` green on both. Found and fixed: the
  installed binary lacked the embedded dashboard and acceptance never
  registered storage on a fresh appliance. Note: Nextcloud requires host
  port 80, so acceptance on an appliance whose edge proxy already holds
  :80 refuses the plan until the edge is stopped.

## Session transport (S256)

One session store (ADR-004), two transports (ADR-020):

- `POST /api/v1/sessions` returns `{token, csrf_token, expires_at}` and
  sets two cookies: `homestead_session` (the same token; `HttpOnly`,
  `SameSite=Strict`, `Path=/`, `Secure` when the request arrived over TLS
  or via a proxy sending `X-Forwarded-Proto: https`) and `homestead_csrf`
  (the per-session CSRF token; same attributes but readable by script).
  `EventSource` SSE works because same-origin requests carry the cookie.
- CSRF is a double-submit scheme checked against the server-side hash: a
  mutating (POST/PUT/PATCH/DELETE) request authenticated by cookie must
  send the CSRF token in `X-CSRF-Token`; it is SHA-256-hashed and
  constant-time compared with the session's stored hash. Bearer requests
  are exempt (browsers never attach that header ambiently); bearer wins
  when both are present. The boundary rejects with 403 and records
  `csrf-missing` / `csrf-mismatch` (never the values).
- Lifetime: 12h idle (refreshed on use, written at most once a minute) and
  7d absolute (`auth.IdleTTL`, `auth.AbsoluteTTL`); legacy 30-day rows are
  capped at 7d. Expired rows are deleted when seen and swept at login.
- Revocation: `DELETE /api/v1/sessions` (bearer or cookie) deletes the
  session and clears both cookies; a password change revokes every other
  session of the administrator and keeps the caller's; recovery (S021)
  revokes all.
- `Verify` is one indexed lookup (`sessions_token_hash_unique`, migration
  23), not a scan, so the boundary can run it on every request.
- CLI: `homesteadctl login` writes `~/.config/homestead/session` (JSON
  with server URL, token, expiry; file 0600, directory 0700) and every
  command sends it as `Authorization: Bearer`. Precedence: `--token`
  (where a command has it), then `HOMESTEAD_TOKEN`, then the stored
  session. A file for a different `--server` is ignored with a hint
  ("stored session is for X; run homesteadctl login"); an expired one or
  one with group/world permissions or a symlink is ignored silently.
  `login` refuses to write through a symlinked directory or file, creates
  the directory 0700, and leaves a pre-existing directory's mode alone
  (refusing only if it is group/world writable). `logout` revokes
  server-side and deletes the file.
- Dashboard: `SessionService` keeps only a display hint (username,
  expiry) in `localStorage`; the credential is the HttpOnly cookie. An
  interceptor adds `X-CSRF-Token` to mutating API calls and, on a 401
  (other than a failed login), drops the hint and routes to `/login`.

## Local admin recovery (S021)

```bash
sudo homesteadctl admin recover --username owner --password-file ./new.pass
```

- The daemon also listens on `/run/homestead/homestead.sock` (`--socket`
  overrides; dev: `--socket /tmp/hs.sock`). The socket file is 0600, so
  only root can connect — that file mode, not self-attestation, makes the
  recover endpoint root-only. It is mounted solely on the socket mux:
  `/api/v1/admins/recover` 404s over TCP.
- Recovery resets the password with no claim-state gate and no session:
  break-glass by design. The administrator must exist (creation stays in
  `admin setup`); the new password faces the same ≥12 policy.
- A missing socket directory fails startup loudly; a stale socket file is
  replaced at listen time.

## Local identity hardening (S200)

- Rotation proves the current credential: wrong current answers invalid
  without saying more, weak replacements refuse under the setup policy,
  and success revokes every session so only fresh logins continue.
- Recovery is a boundary: the reset revokes all of the identity's
  sessions and clears its lockout, since socket possession already
  proved the owner — pre-recovery sessions stop working at completion.
- Login lockout throttles guessing: five failures inside ten minutes
  lock the attempted username for fifteen, tracked persistently
  (schema v12 `login_attempts`) so restarts never forgive. Attempts
  track before lookup, so unknown and known names behave identically
  and lockout never confirms an account exists; success forgives.
- Rotation guesses feed the same lockout; rotation success forgives it.
- Rotation and lockout status are served over the API and CLI
  (S239); dashboard surfaces belong to later slices.

## Account surfaces (S239)

```bash
export HOMESTEAD_TOKEN="$(homesteadctl login --username owner --password-file ./owner.pass --print-token | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')"
homesteadctl admin lockout
homesteadctl admin passwd --current-password-file ./owner.pass --new-password-file ./new.pass
```

- Self-service rotation and lockout status over the API and CLI:
  rotation proves the current credential, refuses weak
  replacements and throttled callers with their own statuses,
  and revokes every earlier session; lockout status answers for
  the session owner only, and unknown sessions never reach
  either. Dashboard surfaces belong to later slices.

## Identity provider interface (S202)

- `internal/identity` holds the provider-neutral integration contract:
  protocol capabilities (OIDC, SAML, LDAP), credential fields without
  values (secrets live in the store and arrive at call time), one live
  `CheckConnection` call proving the endpoint answers before setup
flows trust it, and a registry refusing duplicates and unknown
lookups. Local administrator identity stays in `internal/auth`;
this package never authenticates anyone itself. SSO posture (S203),
also in `internal/catalog`, lets manifests declare unsupported,
optional, recommended, or required with protocols drawn from the
identity capabilities: support without a protocol and protocols on
unsupported apps both refuse, unset reads as unsupported, and the
curated packages pin honest postures — clients optional with the
protocols upstream supports, Authentik itself unsupported.

## SSO setup wizard (S204)

- `internal/catalog` guides per-app SSO over the generic wizard
  store, the catalog, and the identity registry: package, provider,
  review, done. Unsupported apps, unregistered providers, and
  protocols either side does not speak all refuse with the failing
  side named; plans review without writing; apply records one
  intent per application (schema v13 `sso_integrations`) under the
  acting owner, replacing earlier pairings.
- Recovery independence holds by construction: the wizard never
  touches administrator identities or sessions, and every plan
  carries the boundary in the record — Homestead administrator
  login and socket recovery stay local, only app logins change.

## Secret delivery to plaintext-variable images (S342)

- ADR-021 delivers every secret as a 0600 file under the stack
  directory. Images with no `*_FILE` input cannot read one, so a
  manifest may mark a consumer `Export: true` (ADR-052): the `*_FILE`
  variable keeps carrying the path, and a generated
  `secret-entrypoint.<service>.sh` exports the value into the same name
  without the suffix at start, unsets the path variable, and `exec`s
  the image's declared `Entrypoint` plus `Command`.
- Exporting requires a declared `Entrypoint`. Validation refuses a
  missing entrypoint, a variable without the `_FILE` suffix, an export
  target the manifest also sets plainly, a `URI` consumer, and two
  secrets exporting the same variable on one service.
- The script holds paths, never values, is written 0755 beside
  `compose.yaml`, and is bind-mounted read-only at
  `/homestead/secret-entrypoint.sh`. An unreadable file fails the start
  with a named error instead of running the app unconfigured.
- The value then exists in the container's own environment, which is the
  ADR-052 trade. Every other guarantee holds: no value in Compose YAML,
  generated scripts, secret listings, audit records, drift reports, or
  exports. Prefer `_FILE` delivery wherever an image offers it.

## OIDC client actuation (S342)

- `internal/identity` gains `ClientSpec`, `Client`, and `ClientManager`
  beside `Provider`: read-only metadata stays separate from live
  writes. `Authentik` implements both over its own API — flow lookup by
  slug, existing-application reuse with a redirect refresh, provider and
  application creation, orphan cleanup, and repeatable deletion. Errors
  carry method, path, and status only, never a response body. The API
  token lives in the secret store under `identity_<provider>_token`.
- `internal/catalog` declares what a package needs:
  `sso.oidc` names redirect paths, optional scopes, an optional slug,
  and the two secrets that receive the issued credentials. Validation
  requires an SSO class that admits SSO, the `oidc` protocol, declared
  credential secrets, and a web endpoint; redirect URIs resolve against
  the package's route host and refuse an empty one.
- `ActivateOIDC` creates or refreshes the client and stores the issued
  id and secret as `app_<package>_<name>` rows, delivered through the
  supervisor above. It is idempotent: reuse keeps the stored secret and
  refuses when the provider holds a client whose secret was never
  stored, rather than issuing a credential the app cannot read.
  `DeactivateOIDC` removes both, repeatably.

## Bootstrap (S220)

```bash
sudo ./scripts/bootstrap.sh   # clean Debian/Ubuntu checkout: deps, toolchain, install
```

- Root-gated and idempotent: refuses non-Debian/Ubuntu
  distributions instead of guessing, requires systemd and apt,
  installs only ca-certificates and curl from the OS, then takes
  the Go toolchain version from `go.mod` (overridable via
  `HOMESTEAD_GO_VERSION`) — checksum-verified official binaries,
  never distribution packages — and finishes through
  `scripts/install-systemd.sh`, which converges layout, binary,
  unit, and service.

## First-run local discovery (S221)

```bash
avahi-browse -r _homestead._tcp   # or open http://homestead.local:8080
```

- `internal/mdns` answers mDNS on every multicast-capable LAN
  interface: `homestead.local` A records plus a `_homestead._tcp`
  advertisement with SRV port and TXT claim state and version, so
  owners find unclaimed boxes with no DNS server, directory, or
  cloud. IPv4 only; no probing — single-appliance scope puts one
  Homestead per LAN by design.
- The daemon starts discovery in the background with live claim
  state and addresses; a taken port or missing interfaces only
  warn on stderr, never fail the boot.

## Local security baseline (S222)

- `internal/baseline` gates external exposure on one readiness
  report: claim (appliance redeemed), admin (an administrator
  exists, via `HasAdmin`), firewall (nftables present, ruleset
  readable, Homestead table applied — unreadable rulesets fail as
  unverifiable, not unapplied), and management TLS (a fresh
  internal authority issues a serving certificate for
  `homestead.local` verifying with margin, in pure crypto). Ready
  only when every leg passes, each failure naming its remedy;
  unwired dependencies read as incomplete, never ready. No
  persisted authority or served endpoint exists yet — the leg
  proves the mechanics, and provisioning belongs to later slices.

## Domain onboarding (S223)

- `internal/onboard` connects the wizard-selected customer domain:
  the S222 baseline must hold (failing legs named), the provider
  must list the zone (ownership proof; manual attests by
  construction), and the validated plan records as the single
  connected domain (schema v16 `domain_onboarding`) under the
  acting owner. Re-runs re-validate and replace; gated attempts,
  failed proofs, and actorless calls record nothing. No HTTP
  surface: bootstrap slices drive it from here.

## Split-DNS bootstrap (S224)

- `internal/dns` reaches `SPLIT_DNS_READY` without going public:
  the zone intent carries a local view (FQDN to the appliance LAN
  address, default interface preferred) with no public view, local
  answers ensure in local DNS without deleting foreign rewrites,
  and the public audience previews with changes held — applying
  nothing, ever. API-less providers skip remote proof explicitly.
  Re-runs converge; refusals record nothing. The stage records one
  row (schema v17 `split_dns_state`) for later bootstrap slices;
  the overlay view belongs to S225.

## Optional overlay bootstrap (S225)

- `internal/overlay` reaches `OVERLAY_READY` before public
  exposure: the remote-wizard selection names the provider, live
  connectivity validates with node addresses resolving, and the
  overlay view intent (FQDN to node addresses, A and AAAA)
  validates against the zone without applying anywhere — no
  overlay DNS provider exists to receive it yet. Local-only
  selections skip with a recorded none. The stage records
  provider plus addresses (schema v18 `overlay_state`); re-runs
  converge, refusals record nothing. No HTTP surface: bootstrap
  slices drive it from here.

## TLS readiness bootstrap (S226)

- `internal/cert` reaches `TLS_READY` without requiring public
  app ingress: private names stay internal, public names prefer
  DNS-01 where the provider speaks it (issuance before exposure,
  no ingress evidence needed), then direct challenges on verified
  ingress only — wildcards excluded, since only DNS-01 issues
  them — and finally the manual owner path. Unknown providers
  and malformed names refuse instead of defaulting somewhere
  unsafe. The decided strategy records one row (schema v19
  `tls_readiness`) for later issuance; fetching certificates
  belongs to later slices. No HTTP surface: bootstrap slices
  drive it from here.

## Public exposure bootstrap (S227)

- `internal/exposure` approves and applies route widening: approval
  names the actor, re-validates the edge with a fresh aligned
  assessment, and takes explicit CIDRs for zone-wide wide sources
  (the planner never invents them); stale, misaligned, no-op, and
  malformed-network approvals refuse. Approvals record per host
  (schema v20 `exposure_approvals`) for the audit trail.
  Application composes the management baseline with every
  approval — approving never drops earlier routes — collapsing
  exact duplicates and refusing same-named conflicts naming both
  owners, then writes the edge snippet with a proxy reload.
  Unapproved hosts refuse before anything runs. No HTTP surface:
  later slices drive it from here.

## Public validation bootstrap (S228)
- `internal/exposure` reaches `PUBLIC_VALIDATED` only after every
  check passes: the verdict gates on a public-target approval
  (unapproved, mismatched, and non-public hosts refuse), runs the
  five DNS/port/TLS/response layers without short-circuiting, and
  trips when caller-named management hosts resolve to the public
  address. Every verdict records with failed checks named
  (schema v21 `public_validation`); status surfaces read the last
  word back, showing `PUBLIC / HEALTHY` only from passing
  records. No HTTP surface: later slices drive it from here.

## Bare-metal acceptance (S229)

```bash
sudo HOMESTEAD_APP_MOUNT=/mnt/app HOMESTEAD_BULK_MOUNT=/mnt/bulk \
  HOMESTEAD_PASSWORD_FILE=/root/owner.pass \
  HOMESTEAD_CLAIM_TOKEN=<from sudo homesteadctl claim token> ./scripts/acceptance.sh
```

- `scripts/acceptance.sh` walks a fresh supported OS to the base
  suite with validated access through supported surfaces only:
  preconditions (root, distro, tools, mounts), bootstrap,
  claim, admin setup plus login, storage register and assign (fresh
  appliances have no assigned mounts, which catalog requirements read),
  doctor, firewall apply, per-app
  review-then-install with `--follow` (jellyfin, nextcloud,
  immich on isolated networks), drift-clean proof per package
  (no manual Compose editing, mechanically asserted), and access
  validation one level down (generated stacks publish no host
  ports by design — every service owns a container, secret-free
  apps prove health, secret-gated ones report status). With
  `HOMESTEAD_EXPORT_FILE` (plus secrets file) set, the run
  imports configuration after admin and before the suite —
  disaster recovery through the same script. Every phase asserts
  and fails loudly; plan files generate from environment with
  sane port defaults. Backup configuration
  has CLI/API surfaces since S238 (`homesteadctl backup`,
  `/api/v1/backups/*`) and a dashboard Backups page since S267 (storage setup has a dashboard Storage page since S268; edge deploy and route reconcile have a dashboard Edge page since S270, which streams operation progress and loads the recorded table; the reconciler owns only the hosts it last wrote, never removes exposure-owned or hand-placed snippets, refuses public routes without a matching exposure approval, and rolls files back if the proxy refuses the reload); the
  run still ends noting manual backup configuration before
  production use.

## Control-plane recovery (S230)

```bash
sudo ./scripts/recover-control-plane.sh
```

- Reinstalls the daemon without destroying workloads: stops the
  service (containers keep running — the daemon never parents
  them), snapshots the operational database aside, reinstalls
  binary and unit through `install-systemd.sh`, waits on
  `/healthz`, then diffs the running-container set — any
  disappearance fails the run with the snapshot path attached. A
  missing database refuses as disaster recovery (S234) instead of
  booting amnesiac over live workloads. Every phase asserts;
  safe to re-run.

## Configuration export (S231)

- `internal/desired` bundles portable configuration: desired
  state plus provenance (export and Homestead versions, store
  schema, timestamp) and the secret-reference manifest — names
  only, since only `List` is ever called and values are
  structurally unreachable. Documents stay inspectable YAML at
  0644 with strict parsing, version bounds, and atomic writes;
  import builds on the same reader.

## Configuration import (S232)

- `internal/desired` validates and imports exported documents:
  version bounds plus store/desired schema compatibility refuse
  newer documents, secret completeness proves before anything
  writes (missing names listed, blanks counted missing, extras
  refused), secrets set before the desired file lands so failures
  leave no half-imported state, and re-runs converge with values
  decrypting under a fresh key — recovery, not copying.

## Docker-only emergency recovery (S233)

```bash
cd /opt/homestead/stacks/jellyfin
docker network create homestead-edge 2>/dev/null || true
docker compose up -d
docker compose ps
```

- Every curated stack runs under vanilla `docker compose` with
  Homestead nowhere in the loop — proven per package by
  `TestGeneratedStacksValidateWithCompose`, which renders each
  manifest and passes it to the real `compose config` (plus an
  override-merge case): no daemon, no store, no proprietary
  extensions. If Homestead is unavailable: take the stack
  directory, ensure the external `homestead-edge` network exists,
  `up -d`, and confirm with `ps` plus the published ports
  (`compose port` shows the owner-mapped host ports).
- Data stays where the compose file binds it (assigned mounts —
  do not move it); configured variables already ride in
  `compose.override.yaml`, so nothing needs re-entering; `down`
  never touches volumes or binds. Not covered here: TLS
  termination and DNS (Caddy's job), dashboard and operations,
  backups, and updates — recover the control plane (S230) or
  rebuild from backups (S234) for the rest.

## Disaster recovery acceptance (S234)

```bash
sudo HOMESTEAD_APP_MOUNT=/mnt/app HOMESTEAD_BULK_MOUNT=/mnt/bulk \
  HOMESTEAD_PASSWORD_FILE=/root/owner.pass \
  HOMESTEAD_CLAIM_TOKEN=<from sudo homesteadctl claim token> \
  HOMESTEAD_EXPORT_FILE=/root/homestead-export.yaml \
  HOMESTEAD_SECRETS_FILE=/root/homestead-secrets.json \
  ./scripts/acceptance.sh
```

- `GET /api/v1/config/export` serves the portable document and
  `POST /api/v1/config/import` validates plus restores it with
  per-call secret values (`homesteadctl config export/import`
  carry both over files); the acceptance run imports when an
  export file is given and continues fresh otherwise, so the
  same script proves both fresh installs and rebuilds from
  documented artifacts. Proven in KVM: export, total state loss,
  re-run with export plus secrets, suite reinstalled with
  validated access. Backup-run restoration still awaits backup
  surfaces; dashboard import/export surfaces belong to later
  slices.

## Storage setup surfaces (S235)

```bash
homesteadctl storage mounts
homesteadctl storage register --mountpoint /mnt/app --label app
homesteadctl storage assign --class application --mountpoint /mnt/app
```

- Mount registration and class assignment over the API and CLI:
  discovered mounts list live candidates, phantom paths refuse
  against live discovery, unknown classes refuse before touching
  storage, and unregistered mounts refuse assignment. Installs
  read assignments for space checks, so setup precedes planning.
  Proven live in KVM against real bind mounts — including the
  sandbox lesson that owner mounts need `ReadWritePaths`
  coverage or the daemon sees them read-only. Dashboard surfaces
  belong to later slices.

## App removal surfaces (S236)

```bash
homesteadctl apps uninstall nextcloud --follow
homesteadctl apps uninstall immich --remove-data --data-dir /mnt/bulk:immich --follow
```

- Uninstall runs through the API and CLI over the S158 removal
  operation: unknown packages 404, missing stacks refuse before
  anything runs, data survives by default with explicit roots
  required for removal, and `--follow` streams progress like
  installs. Dashboard surfaces belong to later slices. Proven
  live in KVM: remove plus reinstall cycles with data preserved.

## Proxy serve surfaces (S237)

```bash
homesteadctl edge deploy --follow
homesteadctl edge routes set -f routes.json --follow
```

- Caddy deployment and route-table reconciliation over the API
  and CLI, both through the operation engine: invalid tables
  and malformed bodies refuse before running anything, route
  files take capitalized keys matching the docker model, and
  `--follow` streams progress. Proven live in KVM: edge
  deployed, app reinstalled on the edge network, route
  reconciled, serving proven over the proxy. Dashboard surfaces
  belong to later slices.

## Backup configuration surfaces (S238)

```bash
homesteadctl backup destination
homesteadctl backup schedule --package jellyfin --interval 24 --retain 7 --actor owner
homesteadctl backup schedules
homesteadctl backup unschedule --package jellyfin
```

- Destination resolution plus schedule records over the API and
  CLI: unassigned destinations name their absence instead of
  guessing paths, schedules persist with hourly floors and actor
  presence, malformed bodies and short intervals refuse, deletes
  converge idempotently, and lists read empty (never null).
  Scheduled runs fire from the daemon scheduler (S260) and are
  ordinary operations; there is no separate backup-runs endpoint.
  The dashboard Backups page (S267) covers destination, schedules
  (inspect, save, confirmed remove), and a read-only scheduled-jobs
  panel.
- Encryption (S259): `GET /api/v1/backups/destination` also reports
  `encrypted` and `format_version` (shown by `homesteadctl backup
  destination` and the dashboard Backups page). Decrypt any artifact
  without Homestead: `age -d -i identity.txt X.tar.age | tar -x`
  (database files: `age -d -i identity.txt X.sql.age > X.sql`). Tests:
  `go test ./internal/backup` runs a real `age` CLI check when `age` is
  on PATH; set `HOMESTEAD_REQUIRE_AGE=1` to fail instead of skip when it
  is missing. `scripts/acceptance.sh` (`make acceptance`) requires `age`
  (installed by `scripts/bootstrap.sh`) and runs that test with the
  variable set, so acceptance fails closed. SQLite exports use tmpfs
  scratch (`/dev/shm`, swept at daemon start); tmpfs pages can be
  swapped on hosts with swap, so disable swap or encrypt it if that
  matters.
- Proven live in KVM (S259, 72ae5c1, deployed with
  `scripts/install-systemd.sh`; VM 1 four apps on binds with a disk image
  copied first, VM 2 ext4 data disks): `sys_backup_identity` appeared in
  the secret store at the first backup and its key never appeared in the
  journal, audit entries, any API response, the CLI, manifests, or the
  backup tree. `GET /api/v1/backups/destination` answered
  `encrypted: true, format_version: 2` (`install-systemd.sh` installed only the daemon at the time; it now
  builds and installs the CLI too). Scheduled runs of all four apps wrote only `<entry>.tar.age`,
  `<name>.age` and a plaintext `backup.json` (format 2, `archives` map,
  no secret names or values), mode 0600 files; Authentik and Immich held
  `*.sql.age` (230 and 72 tables) and `stack-secrets/<pkg>/secrets.tar.age`.
  No catalog app uses the SQLite export, so `/dev/shm` stayed empty
  while sampled every 100 ms (the SQLite path is covered by tests only).
  The `age` CLI with the identity (exported with a throwaway helper,
  shredded afterwards) listed and decrypted every artifact, and a
  different key was refused. Restore onto a stopped Jellyfin brought back
  a deleted `jellyfin.db` and the app turned healthy; the secrets entry
  came back as a 0700 directory with 0600 files owned by 70/1000 after a
  deliberate chmod/chown; a format 1 plaintext run still restored
  identically; truncated, tail-cut, and bit-flipped archives were refused
  with the target tree unchanged. With retain 2, repeated runs left the 2
  newest, and a stale directory without a manifest was removed. A daemon
  SIGKILL during a Nextcloud backup left an encrypted partial that never
  counted as a run, the last good run survived, and the next run removed
  it. Doctor showed the `backup-encryption` warning and the dashboard
  routes answered with cookie plus CSRF (403 without the header).
  `scripts/acceptance.sh` stops at preconditions when `age` is absent;
  the full script cannot rerun on appliances that already hold the apps.

- Proven live in KVM (S271, 0169914 + 53e7484, deployed with
  `scripts/install-systemd.sh`; both appliances at schema 31): the doctor
  `backup-encryption` warning was present first; `backup recipients add`
  listed the owner key and wrote only the public key to
  `/etc/homestead/backup-recipients.yaml`, and the warning cleared. A
  scheduled Authentik run after the add decrypted (`.tar.age`, `.sql.age`,
  `secrets.tar.age`) with `age -d -i` and the owner identity alone; an
  earlier run did not. After removal the warning returned, a new run
  refused the owner key and the earlier post-add run still opened;
  removing the appliance identity answered 409. `backup identity export
  --out` wrote a 0600 file, a second export to the same path refused
  (`file exists`), doctor said "exported but not acknowledged", `ack`
  cleared it, and the exported identity decrypted every run including
  those from before any recipient existed (file shredded afterwards). With
  the cookie, export, ack and add without CSRF (or a wrong token) were 403;
  with it, the sixth export in an hour was 429 and audited. The
  `AGE-SECRET-KEY` prefix and the key body appeared nowhere in the journal,
  any table other than `secrets`, `/etc/homestead`, the backup tree, or the
  recipients, identity, operations, audit, doctor, destination and
  schedules responses. `/backups` was served and the API flows worked with
  cookie plus CSRF. The CLI export refuses an unwritable path before it
  calls the daemon (no export is recorded). Not run live: a fresh state
  GET (both appliances already held an identity; the handlers only read
  and the unit test asserts nothing is created). Environment notes: after
  a VM reboot the scheduler's first tick can run before the bind mounts
  exist and spends the window (`no backup sources`); resetting
  `scheduler_jobs.last_run_at` and restarting the daemon triggers runs. A
  VM 2 Authentik run failed on `lstat .../postgres/base/1/pg_internal.init`
  (live PostgreSQL files vanishing mid-copy), unrelated to S271.

- Proven live in KVM (f06e3d0, 15b7078, 1a23576, 3920948; deployed with
  `scripts/install-systemd.sh` on both appliances, identical binaries):
  five forced scheduler cycles per appliance (20 backup operations each)
  all succeeded, including Authentik on VM 2, which previously failed on
  vanished `pg_internal.init`. Authentik and Immich runs excluded their
  Postgres data directory and warned that the export is authoritative,
  with `.sql.age` present; Jellyfin and Nextcloud had no warnings. Each
  run held `stack/<pkg>.tar.age` with `compose.yaml` and an empty
  `secrets/` directory and no secret files. Unavailable retry on VM 1:
  with `/mnt/backup` unmounted and the jobs due, status showed
  `unavailable_attempts` and `unavailable_since`, `last_run_at` stayed,
  one audit row per job was written, the 5-minute retry made attempt 2
  without another audit, and after remounting the next retry ran all four
  jobs and cleared the status. A missing backup class assignment on VM 2
  failed the jobs and spent the window (audited error entries); the
  assignment was re-inserted unchanged. Runbook without Homestead on VM 2
  (daemon stopped): Jellyfin was restored by the doc's blocks, extracted
  verbatim, and its `jellyfin.db` sha256 matched; Authentik restored its
  data tree, stack, secrets, and Postgres dump, then came up healthy with
  a marker table and `akadmin` present. After restarting the daemon a
  drift scan was clean, doctor was unchanged, and backups ran. The
  recovery identity was exported, acknowledged and shredded. The stack
  restore fix (3920948) is unit-tested; live restore used the runbook path.
  Notes: the stack archive carries an empty `secrets/` directory entry;
  the retry state is memory-only so a daemon restart retries at once.

- S275 backup restore runbook: `docs/architecture/BACKUP_RESTORE_RUNBOOK.md`
  restores encrypted (format 2) and plaintext (format 1) runs with plain
  `age`, `tar`, `cp`, and `docker`. Its `sh runbook <name>` blocks are
  executed literally by `internal/backup/runbook_test.go` (real `age` and
  `tar`, fake `docker`); the test skips without `age` and fails with
  `HOMESTEAD_REQUIRE_AGE=1`, which `scripts/acceptance.sh` sets.

- Wire format: schedule records are snake_case JSON (`package`,
  `interval_seconds`, `retain`, `enabled`, `actor`, `updated_at`),
  matching the save request body. This changed from PascalCase keys
  before any release; other responses (exposure and update plans)
  still use PascalCase (docs/DEFERRED.md).
- Status codes: reading an absent schedule is 404, validation
  failures on save are 422, and store errors are 500. `retain` 0
  keeps every run. Actor and package are trimmed; whitespace-only
  values are refused.

- Proven live in KVM (S267, ffa7f89): the installed daemon served
  `/backups` and `/secrets`, and the page's API flows worked with cookie
  plus CSRF on both appliances: destination, list, save (200), short
  interval (422), get, absent get (404), the scheduler jobs panel feed,
  and delete (204, repeated 204), with snake_case bodies; mutating calls
  without the CSRF header were 403 and unauthenticated reads 401.

- Proven live in KVM (S268, dccdbad + c7db40c, deployed with
  `scripts/install-systemd.sh`, identical binaries on both appliances): the
  installed daemon served `/storage` (200) and the embedded bundle held the
  storage endpoints plus `acknowledge_impact` and `affected_packages`. The
  mounts feed showed real capacity, accessible and read-only for ext4 data
  disks (VM 2) and bind mounts (VM 1); tmpfs, overlay, proc, read-only and
  devtmpfs mounts answered `registrable: false` with a reason and register
  returned 422. Found and fixed: kernel interface filesystems (hugetlbfs,
  mqueue, bpf, tracefs, debugfs, ...) were registrable; they now refuse.
  A bind-mounted `/mnt/scratch` registered, renamed (re-POST), refused a
  duplicate label (422) and a missing CSRF header (403). Moving
  `application` off `/mnt/app` on VM 2 answered 409 listing authentik,
  immich, jellyfin and nextcloud, and the CLI said the same with no change;
  a `backup` move (exempt) and its restore went through without 409. On VM 1
  `--acknowledge-impact` moved `application` to `/mnt/bulk`, wrote
  `storage.assign.impact_acknowledged`, scheduled backups then failed naming
  `/mnt/app` (Authentik: "no storage class is assigned to any more") or the
  source guard's declared-versus-bound path, and after moving the class back
  all four backups succeeded; VM 2 backups also succeeded after its checks.
  With the daemon stopped, the API call failed at the transport (curl exit
  56, status 0). Doctor was unchanged apart from the existing
  `release-trust` warning. Not proven live: the Angular page in a browser;
  there is no unregister route, so the scratch registration was removed with
  SQL.

- Proven live in KVM (S269, c4dd6de + efd4118, deployed with
  `scripts/install-systemd.sh` after `make dashboard-build dashboard-embed`;
  identical binaries on both appliances): the served bundle held
  `/api/v1/audit`, `/api/v1/dockge` and the `audit` and `advanced` routes,
  with `/audit` and `/advanced` answering 200. `GET /api/v1/audit` with limit
  100 and 1000 returned `ok` and `error` entries (no `skipped` or `warning`
  in the live data) and aggregated `session.boundary.rejected` entries
  (no-session, invalid-session, csrf-missing) after the 1-minute flush; every
  detail matched the dashboard's `rejected ROUTE (REASON) xN since TS` parser.
  Canary secrets sent through a failing secret register (wrong database
  password), tunnel setup, a recipient add with an AGE secret key, and
  bearer/cookie rejections never reached the table or the API. Found and
  fixed: a failed login stores the attempted username as the actor, and the
  actor was not redacted, so `password=...` or `Bearer ...` typed into the
  username field persisted; it is now redacted at write time. Entries
  written before the fix are untouched (redaction is write-time only), and
  the only such entries were the validation canaries. A bare password typed
  alone as the username is not a recognisable shape and is still recorded.
  `/api/v1/dockge` answered `installed: false` with host, exposure, TLS,
  alignment and empty findings on both appliances (the Advanced empty
  state); Dockge itself is not installed there. Doctor was unchanged apart
  from the existing `release-trust` warning. Not proven live: the Angular
  pages in a browser, and the Dockge-installed rendering with findings.

- Proven live in KVM (S270, 894701a + f22fa34, deployed with
  `scripts/install-systemd.sh` after `make dashboard-build dashboard-embed`;
  identical binary on both appliances, schema 32): the served bundle held
  `/api/v1/edge/deploy` and `/api/v1/edge/routes`, with `/edge` answering 200.
  `GET /api/v1/edge/routes` and `homesteadctl edge routes get` read
  `{"routes": []}` before any reconcile. On VM 1 (Caddy edge, a pre-existing
  `jellyfin.lan` snippet) a private route set reconciled and Caddy served it
  (302 from Jellyfin once it was attached to `homestead-edge`); GET returned
  exactly what was sent; hand-placed and exposure-style snippets kept their
  sha256. A public route with no approval and one differing from the recorded
  approval (port changed) answered 422 with nothing written; the exact approved
  route (`app.example.com`, from the earlier S245 test approval) was accepted,
  written and later removed by a reconcile that left the other snippets alone.
  `tls: auto` on `jellyfin.lan` answered 422 up front with files unchanged. A
  hand-placed invalid snippet made the reload fail on both an add and a
  removal: the operation failed at `reload`, files and the recorded table
  rolled back, and Caddy kept serving the prior routes. `POST
  /api/v1/edge/deploy` and `homesteadctl edge deploy --follow` streamed to
  completion with the stack files and routes unchanged. On VM 2 (no edge) deploy
  created the stack from scratch and the reconcile, 422 and rollback checks
  repeated; VM 2 has no exposure approval, so the matching-public case is
  proven on VM 1 and by unit tests. Doctor was unchanged apart from the
  existing `release-trust` warning. Test scaffolding was removed afterwards
  (VM 1 route files and approvals identical to the pre-test state; VM 2 edge
  stack and network removed). Not proven live: the Angular Edge page in a
  browser.

## Periodic drift scan (S261)

```bash
homesteadctl drift schedule                       # cadence + last scan
homesteadctl drift schedule --interval 6h --enabled true
```

- The scheduler job `drift:scan` scans every installed package's
  managed files (S212) on the owner interval: default 24h, minimum 5m,
  anchored when saved (the unsaved default anchors at first daemon
  tick, so a fresh install does not scan immediately).
- Each run is a `catalog.drift-scan` operation and one `drift.scan`
  audit entry (actor `scheduler`). A clean scan audits a single
  `clean: N package(s) scanned` line; drift names packages and files;
  unreadable packages make the audit outcome and operation error.
- Scans never accept or restore: resolution stays with
  `drift review|accept|restore` and the dashboard drift page.
- `homesteadctl doctor` and the dashboard show a `drift-scan` check
  (warn on drift or unreadable, ok when clean or not yet run).
- Migration v26 adds `drift_scan_schedule` and `drift_scan_latest`.
- A clean scan still creates one operation and one audit row per
  interval. That is deliberate (ADR-023: this is how scheduled runs
  stay visible); "quiet" means the audit line is one terse sentence,
  not that nothing is recorded. Lengthen the interval to record less.
- Accepting or restoring a package drops it from the stored latest scan
  (drift and unreadable entries), so the API and doctor never show
  drift the owner already resolved; the scan time is unchanged and
  doctor prints the scan age.
- Scan, accept, and restore take the same per-project lock
  (`composeMu` via `lockProject`, keyed by stack name, which equals the
  Compose project name) around their file work, so a scan never reads
  files mid-restore. A scan holds one package's lock at a time.
- `POST /api/v1/drift/schedule` requires both `interval_seconds` and
  `enabled` (400 otherwise) and audits the authenticated operator.
- One failing scheduler source (for example an unreadable drift
  schedule) is audited as `scheduler.source` at most hourly and skipped;
  other sources, such as backups, still run that tick.
- The dashboard edits the interval in whole minutes (minimum 5), the
  exact unit of the API minimum, so any saved interval that is a whole
  number of minutes round-trips unchanged.

- Proven live in KVM (S274, 1ed4876 + 621c3a7, deployed with
  `scripts/install-systemd.sh` after `make dashboard-build dashboard-embed`;
  identical binary on both appliances): the served bundle held
  `/api/v1/admins/lockout` and `/account` answered 200. Lockout status read
  `failures: 0, locked: false` fresh, counted 1-4 failed logins unlocked, and
  the 5th answered 429 with `locked: true` and `locked_until` 15 minutes
  ahead; `homesteadctl admin lockout` printed the same, an existing session
  kept working, and a correct login stayed 429 until root-socket recovery
  (which clears the lockout) restored the original password. Password change:
  a wrong current password answered 401 with
  `X-Homestead-Error: invalid-current-password` and the session stayed valid;
  73-byte (ASCII and multi-byte) new passwords answered 422 "at most 72
  bytes" without raising `failures`; a 72-byte non-ASCII password succeeded,
  the caller's session survived, a second session answered 401, the new
  password logged in and the old one did not. Recovery with a 73-byte
  password was refused ("password must be at most 72 bytes") and left the
  password unchanged. A bogus cookie on a GET answered 401. Not exercised
  live: first-admin setup max length (both appliances are claimed; covered by
  tests), and the dashboard pages in a browser. VM 1's administrator is
  `owner`, not `alice`; a login for an unknown name is throttled identically.

- Proven live in KVM (S261, da3362f): on both appliances the default was
  24h enabled; `--interval` below 5m was a CLI usage error (exit 2) and
  the API answered 422 for a short interval and 400 for a missing
  `enabled`. At a 5m interval the first scan ran within seconds of its
  due time as a `drift.scan` audit entry (actor `scheduler`) plus a
  `scheduler.run` entry, and a clean result was one terse line. Editing a
  generated `compose.yaml` comment was found by the next scan (audit and
  doctor named the package, file, and scan age) and nothing resolved it
  automatically; `drift restore` (one appliance) and `drift accept` (the
  other) removed it from the latest scan and doctor, and the following
  scan was clean. The drift routes answered with cookie plus CSRF (403
  without the header). Found and fixed: scheduled and pre-update backups
  treated the backup-class mount as a source and counted a mount once per
  class, so a scheduled backup failed with a duplicate step name; the
  scheduled run then completed and wrote its manifest.

## Certificate renewal check (S263)

```bash
homesteadctl tls renewal
```

- The daemon checks daily (job `cert:renewal-check`, an operation). Window:
  the certificate's renewal days, capped at 30. Statuses: `ok`,
  `renewal-due`, `plan-required` (desired names, provider id, or
  challenge differ from the approved issuance; wildcards compare
  literally), `expired`. Expired fails the operation and exits 1 in the
  CLI; due and plan-required warn in doctor. The check itself only
  reports; unattended re-issue (S272) is below and first issuance is S278.
- `GET /api/v1/tls/renewal` returns `{recorded_issuances, summary,
  certificates}`. A desired certificate with no recorded issuance reports
  `no-recorded-issuance`; with none recorded at all, `summary` states
  "no recorded issuances" (also an ok `tls-renewal` doctor check, the CLI
  first line, and the dashboard). Until a certificate is issued through
  the S278 flow this is the normal state.
- Statuses also include `error`: the stored chain does not parse
  (corrupt). It fails doctor, the scheduled check, and exits 1 in the CLI.
  A certificate with `auto` renewal off never reports `renewal-due`; the
  detail says to renew manually.
- Names normalize through the IDNA Lookup profile (UTS #46 mapping with
  NFC, non-transitional, strict DNS rules) from `golang.org/x/net/idna`;
  empty labels and double trailing dots are invalid and never match a
  valid name. Clean checks leave no audit entry; any status other than
  ok or no-recorded-issuance records `cert.renewal.check` under actor
  `scheduler`.

- Proven live in KVM (S263, da3362f): `homesteadctl tls renewal` and
  `GET /api/v1/tls/renewal` reported the no-recorded-issuances state on
  both appliances (nothing there issues certificates; the edge proxy's
  own local CA certificates are not Homestead issuances), doctor showed
  the matching ok `tls-renewal` check, and `scheduler jobs` listed
  `cert:renewal-check` having run as an operation. No ACME issuance was
  attempted.

## Unattended certificate re-issue (S272, ADR-024)

```bash
homesteadctl tls renewal                          # status, failures, owner actions
homesteadctl tls issuance reregister-account CERT # owner action, any CA class
```

- The daily `cert:renewal-check` job (operation `cert.renewal-check`, steps
  `reissue`, `check`, `report`) re-issues every ACME dns-01 certificate that
  is `renewal-due` (30 days or fewer left), whose approval's renewal mode
  is `auto`, and whose issuance is unchanged. The mode is the owner's
  choice recorded at approval (`--renewal auto|manual`, API `renewal`,
  dashboard checkbox; default `auto`, and approvals that predate it read
  `auto`, migration 38); the strategy's own `Renewal.Auto` flag is not
  consulted. `manual` leaves renewal to the owner: status and doctor warn
  `renewal-due` and nothing is issued. Status, `tls renewal` and the
  dashboard show the mode. Its hourly companion `cert:renewal-retry`
  (operation `cert.renewal-retry`) is declared enabled only while a renewal
  is failing (or a renewed certificate waits for an edge reload); both only
  attempt a certificate once its persisted next-attempt time has passed.
- Unchanged means, beyond the ADR-024 fingerprint (normalized name set, DNS
  provider id, challenge method; wildcards literal), the same CA directory
  (so the same class) and DNS zone as the owner's recorded approval, and,
  for a production directory, an approval that carried the production
  confirmation. The directory the existing certificate was actually issued
  from (`cert_issuances.directory`, kept since migration 38; empty for older
  rows, which then fall back to the approval's) must match too. Anything else reads `plan-required` in status, doctor, CLI
  and dashboard, and nothing is issued; renewal never reaches a CA, class,
  or zone the owner did not approve. Served routes are never widened: only
  the approved plan's routes have their snippets refreshed.
- A re-issue runs the S278 steps (`DNS01.IssueFor`, validated placement of
  chain and key, route refresh, forced edge reload) and records the new
  issuance. It always orders a new certificate, except a retry after a
  refused reload, which only redoes the reload.
- Retry state lives in `cert_renewal_state` (migration 37): consecutive
  failures, last error, next attempt. Backoff is 1h, 2h, 4h, 8h, 16h, then
  24h; success resets it; a CA `Retry-After` (seconds or HTTP date) on a
  failure extends the wait past the backoff (bounded at 7 days). A new
  approval or manual issuance clears it. Every attempt writes
  `cert.renewal.attempt` (ok or error, actor `scheduler`, attempt number and
  next time, never key material).
- Account recovery: when the CA answers `accountDoesNotExist`, test and
  staging directories register a new account key and retry once, audited as
  `cert.account.reregister` (actor `system`). A production directory stops
  and waits: status shows `account_recovery_required`, doctor fails, and the
  owner runs `homesteadctl tls issuance reregister-account CERT` (or the
  dashboard Certificates button), `POST
  /api/v1/tls/issuance/account/reregister`, audited under the owner. It
  registers with the approved directory and swaps the stored account only
  after the CA accepted it. Test and staging use the terms agreement on the
  approval. Production needs a fresh acknowledgement: the request must carry
  `agree_terms: true` (CLI `--agree-terms`, dashboard checkbox) after the
  owner has seen the CA's current terms URL (`GET
  /api/v1/tls/issuance/account/terms`, one directory GET, shown beside the
  URL recorded at registration). The terms URL is stored with the account
  at registration; an automatic test or staging re-registration proceeds
  even if it changed, and its audit entry names the new URL and the old one.
- The daily and hourly runs never block each other: a run that finds the
  other still working is audited `cert.renewal.attempt` as skipped and
  tries again at its next evaluation.
- Surfaces: `GET /api/v1/tls/renewal` and `tls renewal` carry `attempts`,
  `last_error`, `next_attempt_at`, `pending_reload`,
  `account_recovery_required`; doctor warns `tls-renewal:<name>` while
  failing and fails when the owner must act; the dashboard Certificates page
  lists them with the re-register button.
- Tests use the fake ACME CA (`acmeDouble`: scripted faults, Retry-After,
  forgotten account), fake DNS and an injectable clock; no network.

KVM validation (Pebble, steps of the S278 section below): configure Pebble
with `"validityPeriod": 1728000` (20 days) so every certificate is already
inside the window, issue `app` with `Auto=true` through plan, approve, apply,
and note the serial served on 443. Then
1. `systemctl restart homestead-server` after
   `sqlite3 /var/lib/homestead/homestead.db "DELETE FROM scheduler_jobs
   WHERE job_id='cert:renewal-check'"` (the daily job is otherwise not due
   again for 24h), or wait for the next daily tick. `homesteadctl scheduler
   jobs` shows the check run; `homesteadctl tls renewal` shows no failure
   lines (a 20-day certificate is still `renewal-due` until Homestead
   re-issues one valid for more than 30 days, so a Pebble certificate never
   reads plain `ok`), the served serial (`openssl s_client`, as in step 7) changed, the file under
   `routes/certs/app/` is new, and the audit trail has
   `cert.renewal.attempt ok` under `scheduler`.
2. Failure and backoff: stop Pebble, delete the job row again and restart:
   `tls renewal` shows 1 failed attempt and a next attempt an hour out,
   doctor warns, `scheduler jobs` lists `cert:renewal-retry` enabled; to
   see the retry fire without waiting, move the next attempt with `sqlite3
   ... "UPDATE cert_renewal_state SET next_attempt_at='2000-01-01T00:00:00Z'"`
   (the hourly job runs within an hour of its last run). Restart Pebble: the
   next retry fails once with `accountDoesNotExist`, recovers by registering
   a new account (class `test`) and renews; audit shows
   `cert.account.reregister` then `cert.renewal.attempt ok`.
3. Production-class owner action (verified without issuing, because the
   Cloudflare shim is plain http and a production-class plan refuses that
   override): add a second certificate (`prod`, names `prod.pebble-lab.net`)
   whose directory is `https://ca.pebble-lab.net:14100/dir` with
   `ca.pebble-lab.net` in the VM's `/etc/hosts` as 127.0.0.1, a throwaway
   certificate for that name appended to the daemon's `SSL_CERT_FILE` bundle,
   and a small local HTTPS stand-in serving a directory with
   `termsOfService`. Remove `HOMESTEAD_ACME_CLOUDFLARE_API` from the drop-in
   for the moment (nothing is ordered), plan, approve with
   `--confirm-production`, then `reregister-account prod` is refused
   (`terms of service agreement required`, the CLI prints the terms URL and
   asks for `--agree-terms`); `GET /api/v1/tls/issuance/account/terms`
   returns the URL; with `--agree-terms` the account registers. The full
   production renewal path (order, `account_recovery_required`) needs a real
   production-class CA and is covered by the unit tests only.
4. Restore as in the S278 steps; remove `cert_renewal_state` rows
   for `app`.

- Proven live in KVM (S272, 06cbfd9 plus 1ebc52c, deployed with
  `scripts/install-systemd.sh` on VM 2 against local Pebble 2.10.1; an
  nftables rule on the VM rejected outbound 80/443 to non-private addresses
  for the whole run, so Caddy's automatic HTTPS could not reach Let's
  Encrypt, and no real CA was contacted): approval defaulted to renewal mode
  `auto` (shown by `tls renewal`); deleting the `cert:renewal-check` row and
  restarting re-issued unattended and the served serial changed
  (386C... then 0BFA... then 19A7...), audit `cert.renewal.attempt ok`
  under `scheduler` with no key text. Re-approving with `--renewal manual`
  left the next check at a `renewal-due` warning (doctor `tls-renewal:app`
  warn) with no order at Pebble. With Pebble stopped, attempts 1 to 7 waited
  1h, 2h, 4h, 8h, 16h, 24h, 24h (by moving `next_attempt_at` back and
  deleting the `cert:renewal-retry` row), and the hourly job was enabled
  while failing and disabled after success. A stand-in CA answering 429
  `rateLimited` with `Retry-After: 172800` on port 14000 moved the next
  attempt two days out. Restarting Pebble (accounts forgotten) gave
  `cert.account.reregister` by `system` naming the terms URL, then
  `cert.renewal.attempt ok`. A production-class directory refused
  re-registration without `agree_terms` (422, terms URL shown) and accepted
  it with. With the daily run held on a stopped (SIGSTOP) Pebble, the
  simultaneous hourly run was audited `cert.renewal.attempt skipped`. A bad
  snippet beside the route made the renewal place the new certificate, fail
  at `reload-edge` (status `pending_reload`, doctor warn), and after the
  snippet was removed the next retry reloaded with no new order at Pebble.
  VM 2 was restored to its baseline (no edge, drop-in, Pebble, credentials,
  approvals, issuances, renewal state, wizard state, edge route row, account
  secrets or firewall rule; doctor matched).
- Found and fixed in KVM (S272): the certificate download was the only ACME
  request that did not retry a `badNonce`, and it dropped the problem body,
  so a re-issue failed with `acme download: status 400` whenever the CA
  rejected the nonce (Pebble rejects about 5% on purpose); it now retries
  once and returns a typed problem (1ebc52c). Corrected steps: Pebble
  issued some certificates for about 6 days even with `validityPeriod`
  1728000 (both lengths sit inside the window); repeated daemon restarts
  within a few seconds hit systemd's start limit (`systemctl reset-failed
  homestead-server`, and leave about 15 seconds between restarts); the
  production-class step above replaces the earlier one, which could not work
  with the plain-http shim. Not proven live: the Angular re-register button
  and a real production CA.

## Certificate first issuance (S278)

```bash
homesteadctl tls issuance credentials cloudflare api_token=@/root/cf-token
homesteadctl tls issuance plan [CERT]
homesteadctl tls issuance approve CERT --digest D --agree-terms [--confirm-production]
homesteadctl tls issuance apply CERT --follow
homesteadctl tls issuance approvals
```

The dashboard Certificates page (`/tls`) does the same through the same API
(`/api/v1/tls/issuance/{plans,approvals,credentials,apply}`).

- Flow (ADR-006, ADR-010): the plan is computed from the saved TLS strategy
  (S106/S226) and the saved domain setup (S097) with no network access. For
  each ACME dns-01 certificate it names the normalized names, DNS provider
  and zone, challenge, CA directory and its class, whether the ACME account
  exists or will be registered, that approving records agreement to the CA's
  terms, where chain and key will be placed, which edge routes will serve
  it, blockers (missing credentials, domain/provider mismatch, names outside
  the zone), and a digest. Approving binds to that digest, is audited
  (`cert.issuance.approve`), and needs terms agreement; production CAs also
  need `--confirm-production`. Apply refuses (409) without an approval of the
  current digest: any change to names, provider, zone, challenge, directory,
  placement, or the routes that would serve it needs a fresh review. Nothing
  is requested from the CA before then.
- CA directory: the directory of the certificate's ACME issuer in the TLS
  strategy. There is no default. Classes: `staging` (Let's Encrypt staging),
  `test` (loopback, RFC 1918, unique-local or link-local address, or a
  private suffix such as `.lan`, `.internal`, `.home.arpa`, `.test`,
  `.localhost`; for Pebble), `production` (everything else, including public
  IP literals and single-label names, which may resolve publicly). Test
  against staging first.
- DNS credentials had no entry surface before this slice. They are stored
  write-only (`POST /api/v1/tls/issuance/credentials`) under the
  conventional `dns_<provider>_<field>` secret names; the CLI reads values
  from a file or stdin, never argv; only field names are audited.
- ACME account key: generated on first use per directory, held in the secret
  store as `sys_acme_account_<directory hash>` (ADR-021: neither owner
  routes nor the catalog can write `sys_`), stored before registration so
  the account is never orphaned, never exported or returned.
- The apply operation (`cert.issue`): `issue-and-place` runs
  `DNS01.IssueFor`, delivers chain and key to
  `<caddy stack>/routes/certs/<name>/{fullchain.pem,privkey.pem}` (key 0600,
  directory 0700, inside the directory Caddy already mounts) and only then
  records the S263 fingerprint and chain;
  `refresh-routes` rewrites the existing snippets of automatic-TLS routes now
  covered (never creating one: an approved but unapplied exposure stays
  unapplied); `reload-edge` reloads Caddy, rolling the snippets back if it is
  refused (the placed files and the recorded issuance stay, so a retry
  works). Routes with automatic TLS whose host the certificate covers serve
  it through `tls <chain> <key>`; later reconciles and exposure applies keep
  doing so. Wildcards cover one label (`*.example.com` is not the apex).
  Placement validates first (the key must pair with the chain and the leaf
  must cover every approved name literally and not be expired), writes key,
  chain and metadata into a staging directory and swaps the directory in
  whole, restoring the previous one if the swap fails, so a new key never
  sits beside an old chain. Symlinked placement directories and files are
  refused, and a placement that is not a valid pair is ignored by route
  rendering, never breaking unrelated route applies. Interrupted staging is
  cleaned (or rolled back) at daemon start. Snippet rewrites are temp file
  plus rename and only touch the approved served routes; rollback keeps file
  modes. Retrying an apply after a refused reload keeps the placed, recorded
  certificate instead of ordering again; a newer approval orders afresh.
  Until S272, Caddy does not renew a placed certificate and nothing
  re-issues it: the renewal check warns (`renewal-due`) inside the window
  for ACME issuances whatever `Renewal.Auto` says (it defaults to false when
  the strategy omits it), and re-running this flow replaces the certificate.
- The ACME client uses POST-as-GET (RFC 8555 section 6.3) for
  authorizations, the order and the certificate, and polls the order URL
  from newOrder's Location header. DNS TXT cleanup runs exactly once on every
  path on its own 30s context, and a failed propagation check stops before
  the CA is asked to validate; once the CA has issued, the certificate is
  delivered and recorded even if cleanup fails (the operation then ends
  failed with that reason).
- Caddy's official image runs as root inside the container, which is why the
  0700 directory and 0600 key written by the daemon are readable there. With
  Docker user-namespace remapping or a non-root Caddy this would need a group
  or ownership change; none is made.
- Environment for validation only (root-owned unit, never API-settable):
  `HOMESTEAD_ACME_CLOUDFLARE_API` (Cloudflare API origin),
  `HOMESTEAD_ACME_DNS_RESOLVER` (host:port the propagation check queries;
  default 1.1.1.1:53). Doctor warns (`acme-overrides`) while either is set,
  and a plain-http Cloudflare origin blocks the plan unless the directory is
  a `test` CA. Trusting a private CA for the directory uses Go's
  standard `SSL_CERT_FILE`.

- Proven live in KVM (S278, c8919e5 + 55c8dd9 + 00813a6 + 45cc807, deployed
  with `scripts/install-systemd.sh` on VM 2 against a local Pebble 2.10.1,
  pebble-challtestsrv and the shim; no public CA was used by Homestead): the
  plan named names, provider, zone, challenge, directory (class `test`),
  placement, served route and a digest, and neither Pebble nor challtestsrv
  saw a request before approval. Approval without terms was refused (422), a
  wrong digest 409, and an apply after the directory changed 409. Apply ran
  `issue-and-place`, `refresh-routes`, `reload-edge` to success: the
  directory was 0700 and the key 0600 (root), key and chain paired, the
  snippet gained `tls <chain> <key>`, `curl --cacert <Pebble root>
  --resolve` got the nginx upstream through the Pebble-issued leaf,
  `cert_issuances` recorded it and `tls renewal` reported it ok. The account
  key lived at `sys_acme_account_<hash>`; plan and approval JSON carry only
  that name; audit held `credentials` (field names), `approve` (including
  the refused attempts), `start` and `cert.issuance`, with no token or key
  text anywhere; the challenge TXT was gone from challtestsrv after issue.
  A directory on a non-private host classed `production`: with the
  plain-http Cloudflare override the plan was blocked, without it approval
  needed `--confirm-production` (422); doctor warned `acme-overrides` while
  the drop-in was set and was `ok` after. With a bad snippet placed beside
  the route, a re-apply issued, placed, then failed at `reload-edge` with the
  route snippet restored byte for byte; after removing the bad snippet a
  retry succeeded with no new Pebble order. Against 20-day Pebble
  certificates with `Auto=false`, `tls renewal` and doctor
  (`tls-renewal:app`) reported `renewal-due`. VM 2 had no edge before: the
  stack, upstream, network, drop-in, helpers, dummy credential, account
  secret, wizard states and issuance rows were removed afterwards and doctor
  matched the baseline plus an ok `acme-overrides`.
- Found and fixed in KVM: (1) a CA that reuses a valid authorization (Pebble
  half the time, Let's Encrypt for about 30 days) made every authorization
  already valid, so there were no TXT values and issuance failed with
  "authorizations must not be empty"; it now skips placement and finalizes
  (00813a6). (2) Re-issuing rewrites the certificate files under an
  unchanged snippet, so a plain `caddy reload` was a no-op and Caddy kept
  serving the old certificate while the operation succeeded; the reload is
  now `--force` (45cc807). Corrected steps: challtestsrv takes `-dnsserver`
  not `-dns01` (and `-doh ""`), and Pebble's config paths are
  relative. Not proven live: the Angular Certificates page in a browser and
  real Let's Encrypt staging.

### KVM validation without public DNS (Pebble)

Pebble is a small ACME test CA; `pebble-challtestsrv` is its DNS test server,
and `scripts/pebble-dns-shim.py` lets the daemon's Cloudflare provider place
DNS-01 values into it. Nothing leaves the VM. Use a name that is not under a
private suffix (`.test`, `.lan`, `.example`... are refused for automatic TLS),
for example `app.pebble-lab.net` in zone `pebble-lab.net`.

1. On the host, build Pebble (outside the repo) and copy it with its test
   certs to the VM: `GOBIN=/tmp/pebblebuild go install
   github.com/letsencrypt/pebble/v2/cmd/pebble@latest
   github.com/letsencrypt/pebble/v2/cmd/pebble-challtestsrv@latest`. From the
   module's `test/` directory take `certs/` and `config/pebble-config.json`
   and copy them, the two binaries (rename `pebble-challtestsrv` to
   `challtestsrv`) and `scripts/pebble-dns-shim.py` to `/opt/pebble/` on the
   VM. The config names its certificates relative to the working directory,
   so rewrite them: `sed 's#test/certs#/opt/pebble/certs#'
   pebble-config.json > /opt/pebble/pebble-config.json`.
2. On the VM, as root, start the three helpers (separate shells or units):
   - `challtestsrv -http01 "" -https01 "" -tlsalpn01 "" -doh ""
     -dnsserver :8053 -management :8055 -defaultIPv4 127.0.0.1`
   - `PEBBLE_VA_NOSLEEP=1 pebble -config /opt/pebble/pebble-config.json
     -dnsserver 127.0.0.1:8053` (listens on https://localhost:14000/dir; its
     test certificate is valid for `localhost` and `127.0.0.1`)
   - `python3 /opt/pebble/pebble-dns-shim.py --zone pebble-lab.net`
3. Give the daemon a CA bundle that trusts Pebble's directory certificate and
   point the Cloudflare provider and propagation check at the helpers:
   `cat /etc/ssl/certs/ca-certificates.crt /opt/pebble/certs/pebble.minica.pem
   > /opt/pebble/bundle.pem`, then `systemctl edit homestead-server` with
   `[Service]` and `Environment=SSL_CERT_FILE=/opt/pebble/bundle.pem`,
   `Environment=HOMESTEAD_ACME_CLOUDFLARE_API=http://127.0.0.1:8081/client/v4`,
   `Environment=HOMESTEAD_ACME_DNS_RESOLVER=127.0.0.1:8053`; restart it.
4. Deploy the edge and a throwaway upstream: `homesteadctl edge deploy
   --follow`; `docker run -d --name tls-upstream --network homestead-edge
   nginx:alpine`; reconcile one route (host `app.pebble-lab.net`, target
   `tls-upstream:80`, exposure `local`, TLS `auto`) with `homesteadctl edge
   routes set -f routes.json --follow`. A `tls: auto` route makes Caddy
   itself try to obtain a certificate for the host from Let's Encrypt as soon
   as it is reloaded (that is Caddy's automatic HTTPS, not Homestead's
   issuance); with a name nobody serves it fails and retries quietly until
   Homestead places a certificate, but it is a real request to a real CA, so
   only use a hostname you are content to name there.
5. Save the domain setup and TLS strategy through `PUT /api/v1/wizards/domain`
   (`{"step":"zone","payload":{"provider":"cloudflare","zone":"pebble-lab.net"}}`)
   and `PUT /api/v1/wizards/tls` (`{"step":"strategy","payload":{"certificates":
   [{"Name":"app","DNSNames":["app.pebble-lab.net"],"State":"pending",
   "Renewal":{"Auto":true},"Issuer":{"Name":"pebble","Type":"acme","Directory":
   "https://localhost:14000/dir","Challenge":"dns-01","DNSProvider":
   "cloudflare"}}]}}`), then store any non-empty token: `printf x | homesteadctl
   tls issuance credentials cloudflare api_token=-`.
6. `homesteadctl tls issuance plan app`: expect directory class `test`,
   account `new`, route `app.pebble-lab.net` under "serves", ready yes, and no
   request in challtestsrv's or Pebble's log yet. Approve with the printed
   digest and `--agree-terms`; `apply app --follow` should end `succeeded`.
7. Verify: `ls -l /opt/homestead/stacks/caddy/routes/certs/app/` shows
   `privkey.pem` mode 0600; `homesteadctl tls renewal` now reports `app`;
   fetch Pebble's issuing root with `curl -sk
   https://localhost:15000/roots/0 -o /tmp/pebble-root.pem`, then `curl
   --cacert /tmp/pebble-root.pem --resolve app.pebble-lab.net:443:127.0.0.1
   https://app.pebble-lab.net/` succeeds (Caddy serves the issued chain), and
   `openssl s_client -connect 127.0.0.1:443 -servername app.pebble-lab.net
   </dev/null | openssl x509 -noout -issuer -serial` names Pebble's CA and
   the placed certificate's serial (also after a re-issue). The
   audit trail shows `cert.issuance.approve`, `cert.issuance.start`, and
   `cert.issuance`, none with key or credential values.
8. Restore: remove the systemd drop-in and restart the daemon, stop the
   helpers, delete the route, `docker rm -f tls-upstream`, and remove the
   `certs/app` directory.

Pebble keeps its accounts and root in memory: restarting it invalidates the
ACME account Homestead stored (an apply or renewal then meets
`accountDoesNotExist`: test and staging directories register a new account
automatically, S272; `tls issuance reregister-account` is the owner action) and changes the issuing root, so
fetch `/roots/0` again. Pebble also reuses a recent authorization for the
same name about half the time and a certificate lifetime comes from
`validityPeriod` in its config, which a shorter value (for example 1728000
for 20 days) uses to exercise the `renewal-due` warning.

Real Let's Encrypt staging additionally needs a real public zone and
provider token; that run is the owner's, not Pebble's.

## Daemon scheduler (S260)

```bash
homesteadctl scheduler jobs
```

- Backup schedules fire from the daemon: a due job enqueues an
  operation through the runner (actor `scheduler`), so runs show in
  operations and audit. A window missed while the daemon was down
  runs once at startup; there is no burst catch-up.
- A schedule's window starts at creation and restarts only when its
  interval changes or it is re-enabled; retention or actor edits never
  delay a run.
- Overlap policy is skip and audit. A skipped run records a
  `scheduler.skip` entry (outcome `skipped`) naming the job, the due
  time, and the blocking operation id. If a hung operation keeps a job
  skipped forever, the blocking id is in that entry and in
  `scheduler jobs` (`last_skip_blocking`). There is no cancel route yet
  (docs/DEFERRED.md); restarting `homestead-server` cancels every live
  operation at startup and the next window runs normally.
- If the scheduler cannot read the previous run's state it skips with an
  error-outcome audit entry rather than risk a second concurrent run.
- Orphaned `scheduler_jobs` rows for deleted schedules are harmless and
  resume if the same job id is declared again.

- Proven live in KVM (S260, ffa7f89): with the daemon stopped, a
  backup schedule's `updated_at` (and, for a second run, the job's
  `last_run_at`) was moved two hours back to make a one-hour job due. On
  start exactly one `backup.job` operation ran (actor `scheduler`),
  completed, wrote the tree and `backup.json` under the backup root, and
  recorded `scheduler.run`; an immediate restart ran nothing more.
  Overlap, simulated by a running `operations` row naming the job with
  the daemon up, recorded one `scheduler.skip` naming the operation id
  (restart cancelled it). A job with no package directory under an
  assigned mount records `build: no backup sources for "<pkg>"`, and a
  Postgres app records `build: postgres backup needs a database
  password`, both as `scheduler.run` error entries with no operation
  (generated stacks do not bind data under assigned storage yet, so a
  package directory must exist under a mount for a scheduled backup to
  run). Discovery needs real mountpoints, so the VMs used bind mounts for
  `/mnt/backup` and `/mnt/app`.

## Updates CLI (S243)

```bash
homesteadctl updates list
homesteadctl updates apply jellyfin --backup --follow
```

- Review and apply over the updates API: the review lists
  installed, current, and backup-gated packages with moves,
  warnings, and reasons; apply carries backup opt-in with a
  database password file, reports no-ops without opening
  operations, and streams with follow. Proven live in KVM
  against real registry discovery.

## Drift CLI (S244)

```bash
homesteadctl drift review jellyfin
homesteadctl drift accept jellyfin
homesteadctl drift restore jellyfin --follow
```

- Review, accept, and restore over the drift API: review prints
  both sides per drifted file, accept adopts with per-path
  confirmation, restore reports no-ops without opening
  operations and streams with follow. Proven live in KVM
  against hand-edited compose files.

## Exposure surfaces (S245)

```bash
homesteadctl exposure review
homesteadctl exposure approve --actor owner --network 203.0.113.0/24
homesteadctl exposure apply app.example.com --follow
```

- Review, approval, and application over the API and CLI:
  review recomputes from saved wizard inputs with missing
  selections reading as absent, approvals name the actor with
  explicit CIDRs and record per host, and application runs
  firewall composition plus edge write through the operation
  engine. Dashboard surfaces belong to later slices.

## Tunnel provider (S246)

```bash
homesteadctl tunnel status
homesteadctl tunnel setup --name homestead --hostname app.example.com --service http://127.0.0.1:8080 --api-token TOKEN --account-id ID --follow
homesteadctl tunnel teardown <tunnel-id> --account-id ID --follow
```

- Tunnel remote access over the API and CLI: discovery prefers
  automated router protocols and falls through to the tunnel when
  the router stays silent, setup seals the API token and runs
  tunnel creation plus connector enablement through the operation
  engine, teardown deletes the tunnel and forgets its routes, and
  status never leaks credentials. Dashboard surfaces belong to
  S247.

## Dashboard exposure surfaces (S247)

- Exposure review, approval, and apply from the dashboard with
  CLI/API parity: the selection saves through the generic wizard
  endpoint while review always recomputes, approvals name the actor
  with explicit CIDRs, and apply links out to operation detail.
- Tunnel remote access from the dashboard with CLI/API parity:
  discovery leads with the tunnel when the router stays silent,
  setup seals the token server-side without ever showing it again,
  and teardown names its account explicitly. Router mapping stays
  available where it works.

## Dashboard design system (S248)

Clean-room (ADR-019): scales are reimplemented, nothing is vendored
from Tanuki or any UI kit.

- Tokens live in `dashboard/src/tokens.css` (imported by
  `styles.css`): spacing, type, radius, shadow, z-index, surface
  colors, `--touch-target` (44px). Components consume custom
  properties; pages never hardcode these values.
- Themes: `light` (default on `:root`), `dark`, and `high-contrast`,
  selected with a `data-theme` attribute on any element.
- Canonical breakpoints (xs 30rem, sm 40rem, md 48rem, lg 64rem, xl
  80rem) are documented in `tokens.css`; custom properties cannot be
  used in media queries, so `@media` rules use those literals. The
  shell collapses below sm; 375px must not overflow horizontally and
  buttons, inputs, selects, and nav links keep the 44px minimum.
- `app-state-message` (`app/state/`) is the one empty, loading,
  error, and status component. Errors use `role="alert"`, statuses
  `role="status"`, the rest no role; a null or empty headline
  renders nothing so nullable signals bind directly. Do not add ad hoc
  `<p class="error">` markup.
- The validation board at `/validation-board` renders every
  primitive under every theme. It is static and API-free, so it needs
  no session and has no parity row (PARITY.md covers API routes).

## Host discovery (S040+)

`internal/discover` reports read-only host facts, Linux-only, failing
loudly elsewhere: distro ID/name/version (`/etc/os-release`), kernel
(`/proc/sys/kernel/osrelease`), arch (`GOARCH`), hostname, memory
(`/proc/meminfo`), CPU count plus model (`/proc/cpuinfo`, ARM-aware).
Hardware (S041) adds machine vendor/model (DMI), the sorted CPU flag
union with HasFlag, and GPU devices (DRM render/card nodes, NVIDIA nodes,
PCI VGA/3D controllers with vendor/device/driver) — all optional sources,
silent when absent. Network (S042) adds interfaces with
addresses/MTU/MAC/flags, the lowest-metric IPv4 default gateway and its
interface (`/proc/net/route`), and resolvers (`/etc/resolv.conf`);
missing routes stay silent, IPv6 gateways extend later. Mounted storage
(S043) adds every mount from `/proc/self/mountinfo` with statfs usage,
UUID/label identity from `/dev/disk`, stale mounts marked inaccessible
rather than fatal, longest-prefix lookup — and never formats anything
(ADR-013).

## Firewall detection (S050)

`internal/firewall` observes only (ADR-008): `nft` presence and version,
`nft --json list ruleset` tables with the homestead-table flag,
iptables backend flavor. Unprivileged unlistability reports
`RulesetAccessible=false`, never an error; process execution is stubbed
in tests and scoped to this package. Planning and mutation belong to
later slices; ufw/firewalld service detection belongs to S057.

## Firewall domain model (S051)

Zones (`local`/`overlay`/`public`, shared with exposure language),
services (tcp/udp, port or range), sources (zone plus optional CIDR),
rules (interfaces, source, service, accept/drop, priority), policies
(service plus allowed sources; empty blocks), and ordered sets with
uniqueness and canonical priority ordering. Pure data and validation —
rendering is S053, apply is S054, and only accept/drop exist (no reject).

## Firewall table generation (S052)

`Expand` derives deterministic rules from policies against live network
facts (explicit CIDRs verbatim; local resolves to LAN subnets; overlay
and public never resolve implicitly — accepts without addresses fail
rather than opening the world). `Render` emits the isolated
`table inet homestead` with one accept-default base chain, so Homestead
rules filter only what they match and compose with administrator policy;
no default-drop exists by design. `ManagementBaseline` accepts the daemon
port from LAN subnets then drops it from everywhere else (public denied).
A skip-guarded `nft --check` golden test validates output where
privilege allows (S229 bare metal).

## Firewall planner (S053)

`Build` diffs desired rules against canonical live rules parsed from
`nft --json` (comment names, set/range/prefix rights, counters ignored,
unknown shapes become foreign removals) into create/add/remove/update
changes with the rendered script attached for S054. Missing table means
create-table, never an error. Lenient by design: hand edits surface for
review instead of crashing.

## Firewall atomic apply (S054)

`ApplySet` validates, pre-checks (`nft --check`), commits one `nft -f`
transaction (idempotent add-table, scoped flush, chain plus rules), then
post-verifies the live table is in sync — distinct errors for each stage.
Temp scripts are 0600 and removed. Observers see the old or new table,
never a half-written one; only the homestead table is ever named.
Lockout protection (S055) constrains what plans may contain.

## Firewall wizard (S056)
`/firewall`: LAN subnets from live discovery, recommended rules as the
rendered script, review of the planned diff, then guarded apply with
snapshot-backed rollback — all framed by the S036 shell with persisted
progress. The daemon also serves status/plan/apply for
`homesteadctl firewall status|plan|apply` (same guarded path). The
management baseline accepts loopback plus LAN and drops public; overlay
accepts await overlay resolution (S110+), and external-firewall mode is
S057. Unix-socket callers verify as loopback (same-machine by
construction).

## External firewall mode (S057)

`firewall_mode` (`managed` default, `external` opt-out) is the first real
domain setting: defined in `internal/firewall`, served through the
existing config API, rendered by the config page, settable via CLI files.
External mode never mutates — plan returns the rendered rules as
instructions with an informational change, apply refuses 409, while
status keeps diagnosing (including active ufw/firewalld managers).
Homestead manages only its own table either way (ADR-008).

## Firewall lockout protection (S055)

`Guard` proves the desired set keeps the administrator's connection
usable before anything mutates: first match in priority order decides —
an earlier drop fails naming the rule, an accept or total silence
(accept-default chain) passes. `ApplyGuarded` guards then applies, changing
nothing on guard failure. `TakeSnapshot`/`Restore` capture exact table
text and roll back byte-equal on failed changes. Active reachability
probing (dial-back) is out: atomic apply plus post-verify plus the guard
is the mechanism; S126 does active probing for exposure.

## System doctor (S044)

`internal/doctor` judges the facts: eight stable checks (distro gate,
memory, CPU, gateway, resolvers, root disk, stale mounts, state dirs) with
ok/warn/fail and remediation-minded detail, served at
`GET /api/v1/doctor`, printed by `homesteadctl doctor` (exit 1 on any
failure), and listed in the status page Diagnostics section. Thresholds
(memory 1/2 GiB, root 5/10 GiB, 2 cores) are S044 baselines to tune with
experience, never silently.

## Operation domain (S060)

`internal/operation`: typed operations (`dotted.names`) and ordered steps
sharing one state machine (pending → running → succeeded/failed/cancelled;
terminal states never move), JSON-validated payloads, and a CRUD store over
migration v5 (operation columns plus `operation_steps`). Execution belongs
to the runner (S061); events (S062), audit (S063), and progress UI (S064)
observe these records.

## Operation events (S062)

Poll-backed SSE, no broker: `GET /api/v1/operations/:id` fetches one
record; `GET .../events` sends the current state immediately, a `state`
event per 500ms-snapshot change, heartbeats every 30s, and closes after
the terminal state (clients reconnect idempotently; no replay log).
`homesteadctl operations get|follow` consumes it (follow exits 1 on
failed/cancelled, 0 on SIGINT); the dashboard wraps `EventSource` in
`ApiService.operationEvents` with an injectable factory (jsdom lacks
it). Rich progress UI is S064; operation producers arrive with their
own slices (nothing creates operations yet).

## Operation progress UI (S064)

`/operations` lists records newest-first linking to `/operations/:id`,
which fetches once and follows the SSE stream until terminal (dying with
the component via `takeUntilDestroyed`). `GET /api/v1/operations` (limit
validated, clamped server-side), `homesteadctl operations list`, and
`ApiService.operations` complete the read surface; no producers exist
yet, so pages stay honest about empty states.

## Audit trail (S063)

`internal/audit` appends actor/action/plan/outcome records plus explicit
owner overrides (migration v6, no update or delete path by design).
Login attempts are recorded server-side with the attempted username —
success and failure alike, never with secrets, and audit failures log
instead of failing auth. Read newest-first via `GET /api/v1/audit`
(`?limit`, clamped) or `homesteadctl audit list`; broader wiring follows
the enforcement slices.

## Operation runner (S061)

`Start` validates the definition, records operation plus steps, marks
running, and returns while a goroutine executes on a detached context —
dropping the HTTP connection never stops the work. Failures stop fast
(unstarted steps stay pending), panics fail instead of stranding,
`Cancel` lands running work cancelled (idempotent), `Wait` polls to
terminal, and `Recover` fails stale records at daemon startup (wired in
`main`). Events (S062), audit (S063), and progress UI (S064) observe.

## Installing as a system service (S011)

```bash
sudo make install                         # build, install binary + unit, enable, (re)start
systemctl status homestead-server
journalctl -u homestead-server -f         # daemon logs (stdout/stderr)
curl localhost:8080/healthz
sudo make uninstall                       # stop, disable, remove binary + unit
```

Details:

- Binary lands at `/usr/local/bin/homestead-server`, unit at
  `/etc/systemd/system/homestead-server.service` (see `ARCHITECTURE.md` paths).
- Install is idempotent: re-running picks up a rebuilt binary (`restart`).
  When `internal/dashboard/dist/index.html` exists (after
  `make dashboard-build dashboard-embed`) the binary is built with
  `-tags embeddash`, so the installed daemon serves the dashboard.
- Install provisions the filesystem layout first
  (`scripts/provision-layout.sh`, mirrors `internal/layout`):

  | Directory | Mode | Holds |
  |-----------|------|-------|
  | `/etc/homestead` | 0755 | desired configuration, customer-readable YAML (S014) |
  | `/var/lib/homestead` | 0700 | operational state, SQLite (S013) — private: sessions/tokens |
  | `/var/log/homestead` | 0755 | file logs (journal is primary) |
  | `/run/homestead` | 0755 | runtime sockets/pidfiles (also systemd `RuntimeDirectory`) |
  | `/opt/homestead/stacks` | 0755 | Compose stacks (populated S074+) |

  All root-owned (ADR-001, single appliance per ADR-012). Uninstall removes
  the service and binary only — state and config directories are customer
  data and are never deleted.
- The unit binds loopback only and runs `Restart=on-failure`, with
  `ReadWritePaths` granting exactly the state directories above.
  `/opt/homestead/stacks` follows when stack lifecycle lands (S074+).
- Platform updates (S265, ADR-027): the daemon unit has no write grant on
  `/usr/local/bin`. `make install` also installs the oneshot
  `homestead-apply.service` (never enabled), the only unit that writes the
  binaries; the daemon starts it to apply a staged, verified release
  (`homestead-server apply-platform`). `homestead-server.service` bounds
  crash loops with `StartLimitIntervalSec=300`/`StartLimitBurst=5`. See
  `docs/architecture/RELEASE_VERIFICATION.md`.
- Install requires the Go toolchain on the machine. Appliance-side
  provisioning (fetching Go or prebuilt binaries) belongs to the bootstrap
  script (S220), not to this slice.
- On the target machine, validate with
  `systemd-analyze verify homestead-server.service` after install.

## Dashboard workflow

```bash
cd dashboard
pnpm install            # first checkout (or make dashboard-install)
pnpm start              # ng serve on :4200, proxying /healthz /version /api to :8080
pnpm build              # production build into dashboard/dist/ (gitignored)
pnpm exec ng test --watch=false  # Vitest unit tests (or make dashboard-test)
```

Layout: `src/app/app.ts` shell (brand header, nav, outlet), `app.routes.ts`
(status page, redirect, not-found; claim/auth/config routes arrive in
S032/S033/S035), `core/api.service.ts` (typed HttpClient over relative
URLs), `status/` daemon health + claim state, `not-found/` fallback,
`claim/` token-then-administrator onboarding wizard (S032).
`proxy.conf.json` maps API paths to the local daemon for `pnpm start`;
production serves same-origin from the daemon (S031), so the service uses
no absolute URLs.

## Claim wizard (S032)

`/claim`: token step (single-use claim token from `sudo homesteadctl claim token`, 401 invalid/expired, 409
skips ahead) then first-administrator step (username, ≥12 password with
confirmation, 409 lands on done). The wizard reads identity on init so
already-claimed appliances start at the admin step.

## Authentication UI (S033)
`/login` plus `SessionService`: login stores only username/expiry as a
display hint in `localStorage` (`homestead.session`); the credential is the
HttpOnly session cookie (S256, "Session transport"); the shell nav shows the username with logout instead of the login link.
Logout clears locally first and destroys server-side best-effort. Sessions
are created here, not yet required anywhere — no route guards yet. A
mistyped password remains recoverable via `homesteadctl admin recover`
(S021).

## Schema renderer (S034)

`GET /api/v1/config/schema` serves S015 field metadata; `app/schema/`
renders it: text (password-masked when sensitive), number with min/max,
checkbox, select, grouped fieldsets, advanced fields behind a toggle
(untouched advanced values are preserved on submit), empty optionals
omitted so server defaults apply, 422 errors under their fields. The
server stays the validation source of truth. No config page yet — S035
builds on the renderer.

## Configuration UX (S035)

`/config`: loads schema plus current values, renders the S034 form,
submits to plan first — valid candidates show an added/removed/changed
diff for confirm, invalid ones surface per-field errors without leaving
the editor. Confirm PUTs and refreshes to the persisted state with a
saved notice; revert reloads the saved state and resets the form;
back abandons review keeping the draft.

## Parity enforcement (S037)

`docs/PARITY.md` maps every daemon route to its CLI command and dashboard
surface (or an explicit `n/a` with a reason); `tests/test_s037_parity.py`
parses it and fails on drift in either direction. Adding an endpoint,
command, or UI surface requires a matrix row — update the doc first,
the test enforces the rest.

## Wizard framework (S036)

`internal/wizard` persists per-wizard `{step, payload}` (`wizard_state`,
migration v4) behind `GET/PUT/DELETE /api/v1/wizards/:id`; the dashboard
`WizardService` plus `app-wizard-shell` (step indicator, projected content,
Back) frame each flow. Steps and payloads are opaque to the server;
payloads hold UI progress only — never tokens, passwords, or sessions.
The claim wizard dogfoods it: step names persist, appliance state drives
resume (UNCLAIMED always restarts at the token step), so stale saves can
never misdirect.

## Embedded dashboard (S031)
```bash
make dashboard-build dashboard-embed   # refresh internal/dashboard/dist/ (gitignored)
go build -tags embeddash ./...         # appliance binary serving the UI at /
```

-    `internal/dashboard` serves files verbatim with SPA fallback to
  `index.html` (never cached; hashed assets carry their own identity) and
  rejects non-GET/HEAD. The catch-all never shadows `/api/*` — mux
  longest-match wins, locked by test.
- Plain `go build`/`go test` stay hermetic and API-only (noembed stub with
  a startup notice); only `-tags embeddash` carries the UI, so everyday Go
  iteration never waits on pnpm. CI builds both.

Adding a dashboard dependency that runs an install script requires explicit
approval first: `pnpm approve-builds <pkg>...` records it in
`dashboard/pnpm-workspace.yaml`. Unapproved scripts fail the install
(`ERR_PNPM_IGNORED_BUILDS`), including `--frozen-lockfile` in CI.

## Docker platform (S070)

`internal/docker` observes Engine plus API versions and the Compose v2
plugin version through the CLI (`docker version --format json`,
`docker compose version --format json`), flagging legacy `docker-compose`
v1. Absence is data, never an error; runtime health arrives in S073.
S071 adds the install operation: `InstallDefinition` plans prerequisites,
the Docker apt repository (key fetched in-process, no curl/gnupg
footprint), packages, and verify — each step short-circuits when
satisfied, executed through the S061 runner, surfaced by the bootstrap
(S220). Ubuntu (S072) reuses the shape with isolated identity and
repository URLs; exactly one distro detector matches any host. Health
(S073) reads Engine versions plus image/container/network inventory over
the Unix socket with stdlib HTTP (no SDK), served at
`GET /api/v1/docker/health` and printed by `homesteadctl docker health`;
unreachable daemons report data, not errors. Stacks (S074) model
definition (name, directory under `/opt/homestead/stacks`, desired
services, enabled), validate names and placement, and group live
containers into observed stacks by Compose labels — one-offs and
unlabeled containers excluded, wanted flags from the desired set.
Lifecycle (S075) runs pull/up/stop/restart plus two-step redeploy as
runner-backed operations. Platform networks (S080): `homestead-edge`
for proxy-to-app traffic and `homestead-platform` for platform-service
consumers, both bridged with egress, created idempotently with managed
labels (driver mismatches refuse instead of touching), verified after
ensure. Caddy (S081) deploys the edge proxy as ordinary Compose
artifacts under the caddy stack directory — `compose.yaml` (pinned
`caddy:2.11.7`, port 80/443, read-only Caddyfile/routes mounts, data
and config volumes, attached to `homestead-edge`), a base `Caddyfile`
with container-local admin plus a routes-snippet import, and
`routes/`, `caddy-data/`, `caddy-config/` directories — runnable with
vanilla `docker compose` per the recovery rule. `DeployDefinition`
plans ensure-edge-network, write-stack, up, and verify (compose ps
proving the proxy running), idempotent throughout. Routes (S082) model
desired proxy entries — canonical lowercase hostname, upstream host
plus port, exposure in the shared local/overlay/public vocabulary
(ADR-006), provider-abstract TLS mode off/internal/auto (ADR-010), and
optional active health probing — with shape-only validation; policy
enforcement belongs to plan/preview layers. The generator (S083)
renders each desired route to a `<host>.caddy` snippet in the stack
`routes/` directory (TLS off pins `http://`, internal emits
`tls internal`, exposure rides as a comment for firewall planning),
removes only stale managed snippets, reloads through the proxy
container (Caddy validates before swapping), and closes with a serving
verify — planned as an idempotent `caddy.reconcile` operation. AdGuard
Home (S084) deploys initial local DNS as ordinary Compose artifacts
under the adguard stack directory — `compose.yaml` (pinned
`adguard/adguardhome:v0.107.79`, DNS on 53/tcp+udp, web/API on 3000,
work/conf data volumes, attached to `homestead-platform`) — runnable
with vanilla `docker compose` per the recovery rule. No configuration
file is generated; defaults arise on first start and initial setup
belongs to the provider adapter (S085). `AdguardDeployDefinition`
plans ensure-platform-network, write-stack, up, and verify (container
running; DNS-level proof belongs to the adapter). The local DNS
provider adapter (S085) in `internal/dns` speaks the AdGuard control
API over the published web origin with basic auth: `FetchStatus`
proves the service answers and reports whether DNS runs, and
`ListRewrites`/`AddRewrite`/`DeleteRewrite` manage custom DNS answers
as validated rewrites (canonical lowercase domains matching the route
model, IP answers). Decoding stays tolerant of API evolution; install
wizard automation stays out until a slice needs end-to-end setup.
Friendly names (S086) in `internal/docker` bind a local-exposure route
to a LAN answer: `WriteNameSnippet` renders the proxy snippet,
`AddRewrite` answers it in DNS, `ReloadProxy` adopts both, and
`VerifyName` proves the route renders and the name resolves to the
answer through local DNS (non-local exposures refused, so public names
cannot arrive by accident). `EnsureNameDefinition` plans the four steps
as an idempotent `names.ensure` operation. DNS intent (S090),
also in `internal/dns` but provider-neutral, models zones with baseline
records plus per-audience views (local/overlay/public per ADR-007, one
view per audience), A/AAAA/CNAME/TXT records with per-type values and
bounded TTLs, relative names with underscore labels for challenge
names, CNAME exclusivity, and deterministic ordering. Providers (S091+)
render from this shape; merging baseline with views belongs to
split-DNS reconciliation (S098). The provider contract (S091), also in
`internal/dns`, binds intent to implementations: capabilities
(records, ACME DNS-01), credential requirements without values (values
live in the secret store, S096), a `Provider` surface of list plus
ordered apply, pure `DiffRecords` set math with stable ordering, and a
registry refusing duplicates. Diff surfaces deletes but never
authorizes them — destructive application stays gated at the
reconciliation layer (S098). The manual provider (S092) is the
human-operated implementation: no capabilities, no credentials, API
methods returning `ErrNoAPI`. It publishes only the zone's public view
as exact master-file lines (`$ORIGIN` plus name/TTL/class/type/value,
CNAME absolute, TXT quoted; LAN answers never leak and view-less zones
fail), and `VerifyRecords` proves propagation through a public
resolver with per-type normalization, aggregating gaps into one report
while TTLs stay unobserved. The Cloudflare provider (S093) adapts the
same contract to the v4 API with a token: exact-single zone resolution,
paginated listing normalized to intent (FQDN to relative names,
automatic TTL to default, foreign types and zones skipped), and ordered
apply with creates posting and deletes resolving through a fresh
listing to provider IDs (missing targets are errors, so retries
converge). The Route 53 provider (S094) signs the query API with SigV4
from stdlib only (no SDK, IAM key pair, STS out of scope), prefers
same-name public zones over private ones, expands multi-value sets to
intent with TXT unquoted while non-simple and foreign sets stay
unmanaged, and applies one transactional ChangeBatch where per-key
final sets compute from fresh state (unchanged sets skip, emptied sets
delete; TTL prefers created values, then actual, then explicit 300).
The DigitalOcean provider (S095) speaks the v2 JSON API with a bearer
token: exact zone existence, paginated listing normalized to intent
(relative names direct, CNAME targets trimmed, foreign types skipped),
and ordered apply with creates posting (TTL-less records omit the
field for the API default, CNAME absolute) and deletes resolving
through a fresh listing to record IDs. Secrets (S096) live outside
ordinary YAML, logs, and API responses by construction: `internal/secret`
keeps AES-GCM rows in the operational database (schema v7: name, nonce,
ciphertext) with the key in a root-only file that is generated once,
refused when group/other readable, and validated for length. Reads fail
closed on missing names and wrong keys, deletes converge, and listing
reports names and timestamps but never values. The domain wizard
(S097) in `internal/domain` guides provider choice, zone naming,
credential storage, access validation, and Homestead subdomain planning
over the generic wizard store: six ordered steps with cumulative
payload validation, credential values refused in wizard state and kept
under `dns_<provider>_<field>` secrets with exact field sets,
validation proving auth and zone access through the live provider
(manual passing on syntax with propagation deferred to real records),
and a plan emitting provider, zone, base, and FQDN without mutating
provider state. It owns no HTTP surface; later slices drive it.
Split-DNS reconciliation (S098), also in `internal/dns`, merges zones
per audience (baseline stands except where the audience's view covers
the same name/type key; view-less audiences fall back to baseline),
gates destructive application (deletes hold unless explicitly allowed
while creates proceed), previews effective tables with gated changes
for owner review, and reconciles by listing, previewing, and applying
the gated set. Address records bridge to local-DNS rewrites for shared
friendly names. DNS diagnostics (S099), also in `internal/dns`,
explain resolution failures per record in the doctor vocabulary (ok
serves the value, fail serves nothing or the wrong thing, warn marks
timeouts and probe trouble), with details naming causes and TTLs kept
out as unobservable. `VerifyView` diagnoses merged audience tables so
owners see each view's intended state verified. Certificates (S100)
in `internal/cert` model TLS identities: canonical DNS names with one
wildcard label allowed, issuers per type (internal bare, ACME with
https directory and known challenge with dns-01 bound to a provider,
manual bare), lifecycle states with failure text only on failed, and
renewal policies with manual auto-renew refused and window math for
monitors. Tables keep unique names with single-owner DNS names plus
lookup; issuance mechanics belong to later slices. DNS-01
infrastructure (S102), also in `internal/cert`, orchestrates
challenges provider-neutrally: authorizations map to
`_acme-challenge` TXT records (wildcards stripped to base, validated
before any provider call), placement creates only so sibling TXT
records survive, propagation awaits through the shared verifier with
caller-owned polling and deadlines, and cleanup removes exactly
placed values idempotently. Provider-backed DNS-01 (S103) wires the
three API providers (manual stays out: no API to place through) into
issuance: issuers resolve to providers for dns-01 only, credentials
assemble from secrets under conventional names, and a minimal RFC 8555
client (dns-01 only, P-256 JWS, nonce discipline, terms requiring
owner agreement) orders, places, awaits, triggers, finalizes with a
fresh key, downloads the chain, and cleans up even on failure. The internal CA (S104), also in
`internal/cert`, mints locally trusted HTTPS: a single-tier P-256
authority (decade lifetime, no intermediates to track) with storage
round-trips proving key-certificate match, and server issuance under
fresh keys with handshake usage, server-auth EKU, and hours-to-years
lifetimes. Distribution into trust stores is host mutation owned by
the security baseline (S222), never the minting unit. Certificate
monitoring (S105), also in `internal/cert`, tracks expiry, renewal,
and health: chain inspection reporting names, windows, issuer, depth,
and days remaining; live handshake capture with SNI that inspects
without trusting; and per-certificate grades reusing each
certificate's own renewal policy. It reports only — re-issuance
scheduling lives above it. The TLS wizard (S106), also in
`internal/cert`, guides strategy, prerequisites, and validation over
the generic wizard store: strategies validate as coherent sets
(private names to internal or manual issuance only), prerequisites
prove readiness without fetching (dns-01 credentials plus reachable
directory, configured authority, manual and direct challenges needing
nothing checkable), plans pair strategy with readiness, and validation
grades served material through monitoring. Issuance runs between
prerequisites and validation at the caller's layer. It owns no HTTP
surface. Remote access (S110) in `internal/overlay` models
provider-neutral overlay intent per ADR-011 (none/headscale/tailscale,
disabled meaning unconfigured): node hostnames, staged subnet routes
refusing default and unroutable destinations, accept-routes posture,
and overlay DNS intent, plus observed nodes and networks validated
for shape. Adapters own all provider contact (S111+). The Tailscale
adapter (S111) adopts existing access: absence is data, live tailnets
map from status JSON (backend state driving presence, peers sorted,
self-addresses excluded from offers), up converges login and settings
in one runner-backed step advertising enabled routes only, logout is
the off state, and a labeled demo profile previews the UX without
enrollment. The Headscale adapter (S112) pins the same client flow to
a validated server origin for enrolment, reuses the identical status
mapping with no second parser to drift, and declares the pre-auth and
API-key secret names for later slices. Existing Headscale setup
(S113) enrols against an external server: validated setup (origin,
user, hostname, routes) plus a per-call pre-auth key plans up with a
verify proving online-with-addresses, offline results aborting; keys
arrive from secrets, never stored state. The Headscale deployment
profile (S114) in `internal/docker` manages the optional control
plane as ordinary Compose artifacts (compose.yaml with 8080
published, config directory with a minimal server-url/listen/SQLite
config, data directory) running under vanilla Compose per the
recovery rule. Server URLs must be https — LAN http enrols directly
instead — and DNS stays at defaults for S115. Deploy plans
ensure-network, write, up, and verify; user bootstrap converges on
existing names. Overlay DNS (S115) in `internal/docker` bridges
split-DNS address records to control-plane extras with
fully-qualified names, reconciles dns sections into deployed managed
configurations preserving every other key, and plans write, restart,
and verify idempotently; Tailscale-side DNS stays admin-console
owned with no adapter pretending otherwise. The remote access wizard
(S117) in `internal/overlay` guides provider choice, overlay config,
credentials, and connectivity over the generic wizard store:
cumulative validation with Headscale server settings required,
provider-exact credential storage (Tailscale adopting keyless,
Headscale requiring pre-auth), prerequisites proving binary plus
settings plus secrets, and connectivity verified as
online-with-addresses. Enrolment runs between credentials and
validation at the caller's layer. It owns no HTTP surface. Public
exposure (S120) in `internal/docker` evaluates recommended versus
selected route exposure with no calls and no mutations: least
privilege governs ties with private names always resolving local,
warnings for unreachable-as-declared exposure, information for
narrowed posture, invalid routes failing instead of grading, and
enforcement left to planners. The exposure planner (S121) in
`internal/exposure` produces offline change sets across DNS, TLS,
firewall, and edge with an embedded engine assessment: private names
refused public widening, public records under the zone origin with
known addresses, TLS work from coverage plus monitoring, firewall
desired as unexpanded policies for live expansion at apply, and edge
carrying the target exposure. Application metadata (S122), also in
`internal/exposure`, lets catalog packages declare per-endpoint
posture ahead of phase 14: recommended origins with open or closed
ceilings over a narrow-to-wide order, bounded package-authored
warnings, and lookup by endpoint. Declarations only, never
application; later slices refuse widening closed ceilings. The exposure wizard
(S123), also in `internal/exposure`, guides private routes to
reviewed public plans over the generic wizard store: cumulative
validation with the planner proving reviewability before anything
persists, already-placed and unplannable selections refused, and
reviewed plans recomputed from saved inputs. It owns no HTTP
surface. Safety recommendations (S124), also in `internal/exposure`,
merge engine, package, and certificate counsel with warns before
information: ceiling breaches advise audited overrides instead of
refusing, plaintext-public and unhealthy certificates warn, quiet
postures confirm explicitly, and presentation layers quote rendered
text verbatim. Connectivity validation (S126), also in
`internal/exposure`, proves public serving across five layers with no
short-circuiting: DNS to the expected address, both edge ports over
TCP, TLS verified against system roots with trust failures
classified, and served responses meeting path/status/body contracts.
Dial overrides serve split-horizon checks; plaintext stays confined
to LAN and test use. Storage classes (S140) in `internal/storage`
define the five-way vocabulary with handling contracts: local-only
databases, evictable caches, and backup defaults with bulk, targets,
and caches out. Unknown classes fail; profiles never promise what
discovery facts cannot match. Selection and provisioning belong to
later slices. Mount registration (S141), also in `internal/storage`,
approves discovered mounts under unique owner labels over schema v9:
registration validates mounted, serving, writable, and never
pseudo-filesystems, re-registration renames, and removal converges.
Storage assignment (S142), also in `internal/storage`, maps classes
to registered mounts over schema v10: suitability ranked against
live facts with databases kept off network filesystems, assignment
requiring approval plus current suitability, shared mounts across
classes, and re-assignment migrating without churn. Directory
provisioning (S143), also in `internal/storage`, converges
application directories on registered mounts with approval
re-checked at run time: relative contained paths with symlink
escapes refused against live state, existing files never converted,
modes within 0700-0775 without world-write, explicit ownership with
untouched defaults, and per-directory runner steps. The storage wizard (S144), also in
`internal/storage`, guides mount selection and class assignment over
the generic wizard store: shape-checked mounts with validated class
bindings, readiness previewed against live facts with reasons where
not, and approvals plus bindings applied at the caller's layer with
offenders named. It owns no HTTP surface. Storage health (S145),
also in `internal/storage`, grades registered mounts with evidence
for presence, writability, and space: degraded covering read-only,
stale, and low-space mounts, missing shared deliberately between
loss and never-existed, roll-ups mirroring the worst, and checks
pure against facts. Catalog manifests (S150) in `internal/catalog`
define first-party applications: pinned non-latest images with
services linked to declared endpoints, semantic versions, bounded UX
copy, memory/storage/port requirements, exposure bound to the
manifest name, and contained relative backup paths. The catalog loader (S151),
also in `internal/catalog`, reads local curated directories with
one manifest per package file, filenames bound to manifest names,
strict fields failing typos loudly, every entry validated, and
local files only with versions surfacing verbatim. Requirements
(S152), also in `internal/catalog`, evaluate CPU, RAM, arch, GPU,
storage, and network prerequisites against explicit host facts with
both sides compared and missing data failing: optional floors keep
older manifests valid while declared ones gate. Compose generation
(S153), also in `internal/catalog`, renders inspectable documents
from manifests plus caller-positioned ports, volumes, and networks:
deterministic services with provenance headers parsing back as
Compose, invalid bindings refused before rendering, and
configuration left to later slices. Network recommendations
(S154), also in `internal/catalog`, materialize per-endpoint route
defaults from metadata with service linkage, exposure, and
hostname-agnostic TLS hints; owner inputs concretize them into
validated routes, and planners look up ceilings by service. The Jellyfin package (S160)
in `catalog/` is the first curated application: pinned images with
declared requirements, exposure, and backup paths, loading valid in
CI with versions surfacing verbatim. Packages ship with the code
they were reviewed against; runtime path resolution lands with the
install surfaces. The Nextcloud package (S170) in `catalog/` is
the second curated application with pinned images, declared
requirements, exposure, and backup paths, loading valid alongside
the first with SQLite as the default database. The Immich package (S171) in
`catalog/` is the third curated application with official release
pins across four services (internal ones carrying no endpoint),
declared requirements, exposure, and backup paths. Services without
endpoints serve nothing routable by construction. Catalog integration metadata (S341) lives in
`internal/catalog/integrations.go`: manifests declare `identity`,
`library`, `handoff` and `companion` relationships, levels are derived
from the kind (never authored), and the loader enforces cross-package
gates — shipped targets, providers this build drives
(`identity.Builtins`), bulk paths on both sides of a library pair, and no
identity declaration on an SSO-unsupported package. The detail response
resolves levels and target titles under `Works`, `homesteadctl apps show`
prints them, and the dashboard package page renders "Works with". Plan
notes flag declared handoff targets that are not installed. The contract
is documented in `docs/CATALOG_INTEGRATION.md`; ADR-051 records that the
metadata is advisory, never actuation. The base suite (S172), also in
`internal/catalog`, resolves the curated bundle in install order with joint
grading: per-package verdicts plus combined memory footprints and cross-member
port collisions naming both owners. Catalog bundles (S344) generalize that
into four curated profiles — starter, family, media, full — where membership
is curated intent, not a promise that every package ships today: a member the
catalog does not yet carry is reported as missing instead of silently dropped.
`CheckBundle` grades combined memory and summed per-class storage against one
host and fails on missing host data rather than passing on it; per-package
verdicts, ports and exposure stay install-time concerns, since container
ports live on separate networks and routes never publish them. `GET /api/v1/catalog/bundles` serves the
graded profiles, `homesteadctl apps bundles` prints them, and the dashboard
catalog page lists them above the package list. The Authentik package (S201) in
`catalog/` is the fourth curated application following the official
compose pins (server, worker, postgres) with internal services
carrying no endpoint: the first manifest declaring a Postgres
database export for backup, with credentials wiring post-install
through configuration. The Vaultwarden package (S345) in `catalog/` is the
first Wave 1 application: one container on application storage with owner
signup open, the administrative page deliberately off (no generated
`ADMIN_TOKEN`), and `DOMAIN` following the routed host through
`{{route_host}}` so passkeys and invitation links match the browser URL.
Exposure recommends local with an explicit widened-access warning, and the
manifest declares optional OIDC with an Authentik identity integration for
the SSO wizard. The Paperless-ngx package (S346) in `catalog/` is the first
three-service Wave 1 application: `webserver` on the pinned GHCR image,
Postgres 18 for metadata, and Valkey 9 for the task queue. Its image reads
only plaintext credentials, so the Django key, database password, and
first-run admin password travel as 0600 files and the generated supervisor
exports them before exec'ing the image's own `/init` (s6-overlay); the
manifest therefore names `/init` as the entrypoint and adds no command.
All four image volumes (data, media, consume, export) are declared, `data`
and `media` are backup paths, the consumer inbox sits on bulk storage for
Nextcloud external storage, OCR defaults to `eng` as a configure value, and
`PAPERLESS_URL` follows the routed HTTPS host. The Gitea package (S347) in
`catalog/` is the private forge: one container whose `GITEA__section__KEY`
variables render `app.ini` at every start with the installer locked, so no
file inside the container is edited. `ROOT_URL` follows the routed HTTPS
host with a trailing slash, `DISABLE_SSH` and `START_SSH_SERVER` keep v1
HTTPS-only, registration stays open until the owner creates the first
(administrator) account, sign-in is required to view repositories, and a
`sqlite` database export backs up `data/gitea/gitea.db` with `VACUUM INTO`
instead of copying a live file. Gitea generates its signing keys into the
data volume, so the manifest declares no secret and no supervisor.
The FreshRSS package (S348) in `catalog/` is the feed reader: the plain
Debian `freshrss/freshrss` tag because the alpine variant has no
`mod_auth_openidc`, so the optional OIDC declaration holds. `CRON_MIN`
installs the image's refresh job and `LISTEN` pins Apache to
`0.0.0.0:80`; the owner completes the recommended web installer on first
visit, which creates the administrator account and the SQLite database.
Both writable directories (`data`, `extensions`) are declared on
application storage as `www-data` (uid/gid 33), and only `data` is a
backup path.
The backup contract (S180),
also in `internal/catalog`, lets entries declare database exports
and consistency hooks: per-engine parameters with cross-engine
fields refused, staged commands bounded and service-linked, and
credentials kept out of declarations. Catalog data placement (S276)
adds per-service `data:` declarations to manifests: container target,
class (application or bulk), subpath, a holds-database flag, optional
mode and numeric owner, or an `ephemeral: true` marker with a reason
(rendered as tmpfs, never an anonymous volume). The planner resolves
each to `<class mount>/<package>/<subpath>` from the storage
assignments (the server fills class mounts from assignments; a missing
one fails requirements), the generator renders bind mounts with
absolute host paths, and `plan.Mounts` lists every resolved mount for
the CLI and dashboard. Request-body volumes remain allowed as extras
but a declared target is refused, never overridden; owner media
libraries pass `HostPath` (an existing directory under the class mount,
mounted as given, never provisioned), which replaced the bespoke
`internal/jellyfin` storage derivation. Binds render
`create_host_path: false` so a missing source fails the start instead
of writing to the root disk, ephemeral tmpfs mounts are capped
(`sizemb`, default 256), nested declarations (Nextcloud `data` inside
`html`) provision their mountpoint stub with the nested owner, and plan
and install verify each class mount is a real, accessible, writable
mountpoint. Installing an already installed package is refused (409;
configure or update instead, or S277 for pre-S276 stacks), and the
dashboard disables Install. Recorded generated bytes stay authoritative:
drift restore rewrites exactly what install recorded.
`internal/catalog/data_test.go` holds a static table of each curated
image's VOLUMEs (cross-checked against locally present images when the
container CLI can inspect them) and fails when an image VOLUME has no
declaration or ephemeral marker, or when a tag bump has no table row.
Backup runs for packages on several mounts (S276 follow-up) place
each source at `<run>/<class>/<path>` with step `backup:<class>:<path>`;
the run manifest keeps `sources` (entry to mountpoint) and a new `paths`
map (entry to package-relative path) so restore returns each tree to its
own mount. Older manifests (entry = path, no `paths`) restore unchanged,
and sources sharing a destination are refused before copying.
Backup copies recreate relative symlinks that stay inside the tree
(Nextcloud's code tree has 121) as links, never following them, still
refuse absolute or escaping links, and keep owner and group when the
daemon runs as root so a restored Postgres or Nextcloud tree stays
usable by its container.

- Proven live in KVM (S276, 278332f plus the fixes after it, fresh VM 2
  with `/mnt/app`, `/mnt/bulk`, `/mnt/backup` ext4 data disks in
  fstab): `scripts/acceptance.sh` ran to completion; plans listed the
  resolved host paths; Authentik joined by hand. `docker inspect` of
  all nine containers showed only bind mounts under
  `/mnt/app/<pkg>` or `/mnt/bulk/<pkg>` plus the capped valkey tmpfs,
  and `docker volume ls` stayed empty. Provisioned ownership held
  (authentik 1000, alpine postgres 70, immich postgres 999, nextcloud
  33 including the nested `html/data` stub); Nextcloud installed with
  `occ`, and a WebDAV upload landed in `/mnt/bulk/nextcloud/data`.
  With `/mnt/bulk` unmounted a plan and install were refused (422) and
  no directories appeared on the root disk; reinstalling an installed
  app is 409. Scheduled backups for jellyfin, nextcloud, and immich
  wrote distinct `<run>/<class>/<pkg>` trees with real data (Jellyfin
  `jellyfin.db`, the Nextcloud test file and `owncloud.db`, Immich's
  Postgres directory) and manifests with sources and paths; a Jellyfin
  restore onto a stopped stack returned the deleted database and the
  app came back healthy. Authentik's scheduled backup records the known
  "postgres backup needs a database password" error (docs/DEFERRED.md).
  Image inspection showed every `knownImageVolumes` row correct except
  valkey, which declares no VOLUME (row fixed). Found and fixed:
  Nextcloud backups failed on its in-tree symlinks, and backup copies
  dropped file ownership.

- Proven live in KVM (S277, ffe6446 plus fa6075d and ce383d8, VM 1 with
  four pre-S276 apps on anonymous volumes; disk image copied first):
  schema 29; detection reported `needs-migration` for all four on VM 1
  and `placed` for all on the fresh VM 2. Seeded data survived. Nextcloud
  (installed with `occ` first, SQLite): a 3 MB WebDAV file kept its
  sha256 (6158d5ba...) after migration, `html` landed on
  `/mnt/app/nextcloud/html` and the nested `data/` on
  `/mnt/bulk/nextcloud/data` (no double copy; `html/data` is only the
  empty mount point), and the owner/mode listing of both trees matched
  the old volume apart from the mount-point stub mode. Jellyfin: tree
  listing (type, mode, owner) identical to the old volume, every config
  XML, a sentinel file, and `jellyfin.db` kept their sha256. Authentik
  (Postgres): tree identical, `verify-database` passed, `pg_isready`
  answered, and a user and group created before the migration were still
  queryable; its container-layer `/media`, `/templates`, `/certs` came
  up as new empty binds as the plan said. Immich cannot start its
  database on VM 1 (no password, empty volume), so its migration fails
  at `verify-database` and rolls back: the app restarts on the original
  volumes, `compose.yaml` pins them, the copied `library` tree (with a
  seeded 0640 uid 1000 file) was moved to `library.rollback-<stamp>`
  with ownership intact, nothing was deleted, and the retry planned
  cleanly. A second approve during a Nextcloud migration answered 409
  and uninstall during it 422. Pre-migration backups held the stopped
  volumes (Nextcloud 28292 files). After migration the scheduled backups
  of Nextcloud and Jellyfin captured real data from `/mnt/app` and
  `/mnt/bulk`; Authentik passed the guard and then stopped at the known
  password requirement. With Jellyfin's `/config` directory renamed the
  guard failed the run naming the path (restored afterwards); on VM 2
  every app's scheduled backup passed it. Old-volume removal: an
  unrecorded volume was refused (409), a recorded one held by another
  container was refused, then both Jellyfin volumes were removed and
  audited (`app.data.old-volumes.remove`); Nextcloud's and Authentik's
  old volumes remain `pending-removal`. Doctor shows `app-data:*`
  checks, `/data` is served, and the data routes answered with cookie
  plus CSRF (403 without the header, 401 unauthenticated). Found and
  fixed: the Engine list endpoint leaves a volume mount's `Source` empty
  for some volumes (plan failed measuring volume ""), and `verify-stopped`
  could read a container as still running just after `compose stop`.
  Not exercised: `scripts/acceptance.sh` (it installs apps and needs a
  checkout, a free :80, and an unclaimed or known-password appliance), a
  successful Immich migration (needs configured database credentials),
  and a Postgres-app backup after migration (needs a database password).
Local backups (S181) in
`internal/backup` resolve backup-class destinations with live
re-checks, copy trees preserving modes with symlinks refused,
manifest runs last with re-walked counts, and runner-backed
execution fails fast. Image discovery (S190) in `internal/update`
compares manifest pins against running images and filters Docker
Hub listings to newer same-variant versions with numeric ordering;
outages error instead of reading as current. GHCR discovery (S343)
adds the token flow GitHub Container Registry requires: one anonymous
pull token, then the tag listing with a bearer header, at most two
pages. Registries Homestead does not support refuse by name, the plan
records per-service provenance (registry, repository, whether it
answered, and why not), and locally built craft images are marked as
such, so a service with no registry update says why instead of reading
as current. Version comparison tolerates the `v` prefix GHCR projects
use. Update planning (S191), also in `internal/update`,
reviews registry maxima against manifest pins with current
services planning empty, flags backups with database-first reasons,
lists affected services, and warns major version jumps with
versions attached. Safe updates (S192), also in
`internal/update`, run backup hooks plus pull, pin rewrite, up
with running-state verification, and rollback on verify failure
with both outcomes reported; undescribed plans refuse before
anything runs. The update review API (S193), in
`internal/server`, serves per-package drift plans with installed
marking plus approved application: backup-gated plans refuse
without backup requested, current packages report no-op instead of
empty operations, and long executions return operation IDs onto
existing streams with database passwords flowing through once and
never stored. The updates dashboard page reviews moves, warnings,
and backup reasons with opt-in backup plus one-shot database
passwords, disables guarded apply until backup is opted in, and
links started updates to operation detail; CLI rows wait for a
later updates CLI slice. The platform update model (S194), in
`internal/update`, names Homestead's own components apart from
catalog applications: daemon and CLI with the dashboard riding
daemon releases because its assets embed in the binary, so a
dashboard-only update is indescribable. Desired versions arrive
owner-declared with no hosted feed by customer-ownership rule;
current components plan empty, rebuild suffixes move, downgrades,
duplicates, strangers, and unparseable versions refuse, major
jumps and daemon restarts warn, and moves always require backup
first. The application apply route turns platform names away so
compose flows never try to replace the daemon out from under
itself. S193 also repaired catalog template
bindings to parser-legal forms: Angular templates reject arrow
functions and computed keys, so row edits go through component
patch methods, which unblocked the whole dashboard suite. The backup wizard (S184), also in
`internal/backup`, guides package plus cadence over the generic
wizard store with plans previewing without writing and schedules
persisting under acting owners. It owns no HTTP surface. Restore
planning (S185), also in `internal/backup`, inventories runs
newest-first with foreign and partial runs staying out, defaults
entries to provenance mounts with missing provenance requiring
explicit targets, and marks existing targets destructive with
named warnings including
quiescence for databases. Restore execution (S186), also in
`internal/backup`, copies approved plans with mirror semantics
plus closing verification comparing file sets and sizes, failing
fast with offenders named. Database exports (S182), also in
`internal/backup`, run application-aware: pre hooks, engine export,
post hooks even on failure with both reported; SQLite snapshots
online with busy absorption, Postgres dumps through container
tooling with passwords confined to process environment. Backup schedules
(S183), also in `internal/backup`, persist hourly-floored intervals
with retention counts per package over schema v11, and retained job
definitions copy files, export databases, prune to newest, and close
with manifests; results record through operations while wall-clock
triggering belongs to timers. Jellyfin storage (S161) in `internal/jellyfin`
maps configuration and media to assigned mounts: config provisioned
under application, libraries referenced (never created) under bulk
with existence validated, every required class assigned, and
installer volumes plus provision-only-config outputs. Hardware
acceleration (S162), also in `internal/jellyfin`, maps discovered
GPUs to transcode paths: discrete NVIDIA, then Intel Quick Sync,
then AMD VAAPI, with half stacks falling to software and group
additions deliberately absent pending host video GIDs. The Jellyfin
route (S163), also in `internal/jellyfin`, builds the media route
from catalog metadata with recommended exposure, internal TLS, and
the public-info health probe, rendering through the shared
generator with widening left to ceremony. Jellyfin DNS (S164),
also in `internal/jellyfin`, answers the media hostname locally
through the provider adapter with resolution verified to the bound
answer, while split-horizon public records plan validated but
unapplied with application owned by exposure flows. Jellyfin TLS (S165), also in
`internal/jellyfin`, gates route strategies without fetching,
mints internal serving certificates under fresh keys with pre-store
verification, and proves material covering exact and single-label
wildcard names inside validity windows. Dashboard install (S167) serves catalog
list, detail, plan, install, and configure routes with server-side
host facts and operation IDs onto existing streams, plus a catalog
page with review-then-install and configuration forms; CLI rows wait
for S168. CLI install (S168) completes the equivalent flow through
`homesteadctl apps`: list, reviewed plan, approved install with
optional follow, and service configuration by flags over shared API
client methods, with file-driven bodies matching the dashboard
shape. Parity acceptance (S169) proves CLI and dashboard converge:
identical bodies plan byte-identical change sets at runtime, form
keys match server keys exactly, and catalog parity rows carry both
surfaces with no gaps. Jellyfin exposure (S166), also in
`internal/jellyfin`, selects and enforces route posture with
ceilings holding unless matching live overrides name the widening,
private names never going public, and assessment plus
recommendations riding along. The install planner (S155),
also in `internal/catalog`, produces offline change sets across
requirements, routes, compose, DNS, TLS, firewall, and directories
with lifecycle verbs: unmet requirements and unknown inputs refuse
before anything writes, public routes need zone and address, TLS
strategies follow shared eligibility, and firewall scopes to widest
exposure. Install execution (S156), also in `internal/catalog`,
runs approved plans as one runner-backed operation — per-directory
provisioning plus stack writing plus up — failing fast with
offenders named, with production directories resolving from plans
and isolated overrides for tests. Configure execution (S157), also
in `internal/catalog`, modifies installed apps through layered
override files with reviewed per-service set/unset plans: merges
preserving foreign state, emptied files deleting, 0600 permissions,
base files staying generated-only, and write plus up execution.
Removal (S158), also in `internal/catalog`, uninstalls with data
preserved by default: down plus generated-file removal, explicit
data removal naming every root with escapes refused and missing
roots failing loudly, ambiguous intents and never-installed apps
refused, and a shared compose execution seam. Router integration (S130) in `internal/router`
generates exact NAT instructions: purposes mapping to port sets with
unknown purposes failing, merged sets deduplicated and sorted,
matched external/internal ports with validated appliance addresses,
and instructions naming DHCP stability, verification, CGNAT limits,
and management restraint. Gateway discovery (S131), also in
`internal/router`, identifies the likely NAT upstream from
default-route facts with absent routes failing: UPnP by multicast
with WAN connection control URLs, NAT-PMP/PCP distinguished by
version on the shared port with external addresses disclosed, and
unsupported protocols staying out without error while real
handshakes wait for later slices. Assisted setup (S132), also in
`internal/router`, guides gateway facts, purposes, and instructions
over the generic wizard store with owner confirmation recording only
the owner sees their router UI, plans recomputed from saved inputs,
and UPnP mappings queried read-only with 714 meaning absent while
external proof waits for verification. Port verification (S133),
also in `internal/router`, attributes ingress across service, host
firewall, and router layers from kernel tables, LAN dials, and UPnP
queries: ordered evidence names the first failure without blending
faults, unaskable gateways report unknown, and hairpin-prone external
claims stay out. The mapping contract (S134), also in
`internal/router`, binds automation behind explicit opt-in: add,
remove, and read with disabled automation refusing consent-first,
incapable, disabled, and absent kept distinct, bounded lifetimes
refreshing instead of fossilizing, and a manual provider carrying no
automation. PCP mapping (S135), also in `internal/router`, speaks
RFC 6887 version-2 MAP datagrams with nonces and defaulted
lifetimes: deletions converge by zero lifetime, gateway port
reassignments fail loudly for fixed-port serving, result codes
diagnose, silence exhausts retries, and querying stays unsupported
with disabled automation refusing before the network. UPnP mapping
(S137), also in `internal/router`, automates AddPortMapping,
DeletePortMapping, and single-entry query across both WAN services
with explicit leases defaulting: conflicts fail loudly with codes,
absent deletes converge with absent reads reporting, and review
lives one layer up in the assisted flow. NAT-PMP mapping (S136),
also in `internal/router`, speaks RFC 6886 version-0 family
datagrams with defaulted lifetimes: deletions converge by zero
lifetime, gateway port reassignments fail loudly for fixed-port
serving, result codes diagnose, silence exhausts retries, and
querying stays unsupported with disabled automation refusing before
the network. Owner overrides (S125), also in `internal/exposure`,
record per-host exposure exceptions with required reasons, named
actors, and management flags over schema v8: re-grants replacing
with fresh audit, revocation converging, reads enforcing with the
trail explaining. Unreachable targets and closed ceilings hold
absolutely; everything else audits with override marking. Exposure policies (S116)
in `internal/docker` narrow routes beyond their origin zone over
shared firewall sources: empty denies all, zone-wide-only stays
firewall-governed, and listed networks enforce at the proxy through
allowlists with aborts elsewhere. Nil policies render byte-identical
output; TLS stays site-level above enforcement. Automatic HTTPS
(S101) in `internal/docker` gates standard Caddy automation on direct
challenge validity: private names (single-label, LAN/reserved
suffixes, IP literals) never face public ACME, direct challenges count
only for public names on public exposure, recommendations automate
there and go internal elsewhere, and rendering refuses automatic mode
on private names instead of deploying routes Caddy could never bring
up. DNS-01 (S102) and the internal CA (S104) cover the rest. Dockge (S210)
in `internal/docker` deploys the expert Compose interface as ordinary
Compose artifacts under the dockge stack directory — `compose.yaml`
(pinned `louislam/dockge:1.5.0`, UI on 5001, engine socket and data
volumes, the stacks directory mounted at the same path inside and
out so experts see Homestead-managed stacks beside their own, root
file ownership) — runnable with vanilla `docker compose` per the
recovery rule. No credentials are generated; login arises on first
start. `DockgeDeployDefinition` plans ensure-platform-network,
write-stack, up, and verify (container running). The socket and
stacks mounts grant full engine control by design; who may reach
the interface belongs to the administration link (S211). The link
(S211), also in `internal/docker` with `GET /api/v1/dockge` and a
dashboard Advanced page, exposes the interface under its assessed
access policy: the route constructor hard-codes local exposure with
internal TLS so engine control cannot describe anything wider, the
engine assessment proves the posture aligned and finding-free, the
API serves the link read-only with installed status from engine
labels, and the page states the policy beside the link without
changing anything. Drift detection (S212), in `internal/catalog`
over schema v14, watches generated files by recorded SHA-256:
installs and configures re-baseline fingerprints on write, removals
clear them, and `Detect` names modified and removed files with
stable ordering while foreign files never report and unreadable
files fail instead of guessing. Resolution belongs to S213;
detection only names. Drift resolution (S213), also in
`internal/catalog` over schema v15, offers the three choices:
records carry managed content so review pairs both sides without
regeneration, accept adopts current files as managed, and restore
rewrites recorded bytes with generated modes then converges
through a `catalog.restore` operation. `GET` and `POST
/api/v1/catalog/{name}/drift` serve review plus accept/restore
with no-drift no-ops instead of empty operations, and a dashboard
Drift page reviews both sides with accept and restore buttons
linking started restores to operation detail.

Secret re-key (S258, ADR-021) lives in `internal/secret`: keys are
versioned files `secret.key.v<N>` (mode 0600) beside the legacy
`secret.key`, and each `secrets` row records its key version (schema
v24; 0 reads as version 1). First start after S258 copies the legacy
key to `secret.key.v1` and stamps unversioned rows 1 without touching
ciphertext; the legacy file stays, ignored. Re-key is the
`secret.rekey` operation (steps `write-key`, `reencrypt`, `verify`,
`retire-old`): the new key is durable (temp, fsync, rename, directory
fsync) before the one re-encrypt transaction, rows are decrypt-verified
after commit, and only then are older key files deleted; start also
retires older keys once every row verifies on the newest version. A
key file left by an interrupted run is reused by the next run.
`GET /api/v1/secrets/key` and `POST /api/v1/secrets/rekey`
(`{"confirm": true}` required) serve `homesteadctl secrets
<status|rekey --confirm [--follow]>` and the dashboard Secrets page;
audit records `secret.rekey.start` and `secret.rekey` with versions
and operation ids only. **Runbook:** every retained key file
(`secret.key.v*`, plus the legacy `secret.key` until the owner removes
it) belongs in the owner's recovery material; losing the key file a
row records strands that row. Re-key blocks secret reads briefly.
Downgrading to a pre-S258 binary is only possible before the first
re-key (re-key retires `secret.key.v1`; the legacy file is not kept in
sync). Start refuses, with a clear error, when the legacy key and
`secret.key.v1` differ before any re-key, or when secrets exist and no
key file does; it never mints a key over existing ciphertext.

- Proven live in KVM (S258-S267, ffa7f89): on an upgraded appliance
  holding pre-S258 secrets and a fresh install, start stamped rows v1
  and wrote `secret.key.v1` byte-identical to the legacy key; the same
  secrets decrypted unchanged afterwards. `secrets rekey --confirm
  --follow` moved both to v2 and retired `secret.key.v1`, secrets still
  decrypted after a daemon restart, and audit held only versions and
  the operation id. `GET /api/v1/secrets/key` and the rekey route
  answered with cookie plus CSRF (403 without the header, 400 without
  `confirm`), and the Secrets and Backups dashboard routes were served by
  the installed binary. Moving every key file away with secrets present
  refused to start with the clear error and minted nothing; restoring
  them started normally. Found and fixed: restoring only the legacy
  `secret.key` after a re-key started the daemon (copying it forward as
  v1) and stranded the v2 rows; start now refuses naming the missing
  key files.

## Secret-backed container variables (S266, ADR-021)

Catalog manifests declare secret-backed variables under `secrets:` (a
logical `name`, the `consumers` that receive it as service plus
environment variable, and `immutable` for values that must never rotate)
and name the database password with `database.passwordsecret`. Plain
non-secret environment (hostnames, usernames, database names) is the
per-service `environment:` map; a variable may not be both.

- Secret-store name: `app_<package>_<name>` (dashes become underscores).
  The `app_` prefix marks app-owned secrets; configuration export lists
  only non-app secrets, because app secrets are recovered from encrypted
  backups (S259), not the export document.
- Install generates 32 random bytes (64 hex characters) per declared
  secret that the store lacks, never overwriting an existing value, then
  writes one file per consuming service: `<stack dir>/secrets/<service>_<name>`,
  mode 0600 in a 0700 directory, owned by the uid/gid that service's
  declared data paths use (so a non-root container user can read it).
  Files live under the stack directory (`/opt/homestead/stacks/<package>/`),
  never under `<mount>/<package>`, so plaintext backups do not copy them.
- The generated `compose.yaml` carries a top-level `secrets:` map with
  `file: ./secrets/<key>` sources and a per-service `secrets:` list. The
  environment variable receives only the in-container path
  (`/run/secrets/<key>`), or a `file://` URI for Authentik. No value
  appears in `environment:`, the override file, drift, logs, audit,
  operation payloads, or config export.
- Delivery mechanism per image: Postgres (`postgres:16-alpine`,
  `immich-app/postgres`): `POSTGRES_PASSWORD_FILE`; Immich server:
  `DB_PASSWORD_FILE` (documented `_FILE` support); Authentik server and
  worker: `AUTHENTIK_POSTGRESQL__PASSWORD` and `AUTHENTIK_SECRET_KEY` as
  `file:///run/secrets/<key>` URIs (Authentik's `file://` syntax).
- Configure: `apps configure <pkg> --rotate-secret NAME` (or the
  dashboard Rotate button) runs through the normal configure operation:
  a database password is changed in the running database with
  `ALTER ROLE` (SQL on stdin, never argv), then stored, then files are
  rewritten in place and consuming services restart. Immutable secrets
  (Authentik `secret_key`) refuse rotation. Setting a secret-backed
  variable (or its plain form such as `POSTGRES_PASSWORD`) through
  `--set` is refused. Every configure also repairs missing or mis-moded
  secret files.
- Drift treats secret files as opaque: presence and mode (0600) only,
  never content; restore fixes a wrong mode but cannot recreate a missing
  file (run configure).
- Backups resolve the Postgres password from the store through
  `database.passwordsecret` for scheduled and pre-update runs; a missing
  secret fails naming it (for example `app_authentik_db_password`).
- Existing installs that predate S266 (adopt, never reinstall).
  Postgres applies `POSTGRES_PASSWORD_FILE` only at initdb, so a newly
  generated password over an existing data directory is rejected, and
  uninstall plus reinstall of a pre-S276 (anonymous-volume) stack orphans
  its data. The supported path is: (1) `homesteadctl apps secrets
  register <pkg> db_password --value-file PATH|-` (dashboard: Register
  existing value) stores the current password; it is verified by
  authenticating to the running database over its network address, after
  first confirming the server rejects a random wrong password over that
  same path (a trust-auth server cannot verify anything and is refused
  unless `--skip-verify`). (2) `homesteadctl apps configure <pkg>
  --adopt-secrets` (dashboard: Adopt secrets) runs the configure
  operation that regenerates ONLY the wiring: secret files, compose
  `secrets:` and the `*_FILE` variables, with the plaintext credential
  variables scrubbed from the override file and fingerprints re-recorded;
  the stack restarts and the database password is health-checked, and a
  failed check restores the previous compose and override bytes. Secrets
  other than the database password (Authentik `secret_key`) are read from
  the running container's current environment, else must be registered.
  Data and passwords are never re-initialised.
- Registering is guarded: a value that differs from an existing one needs
  a verified database password or an explicit `--force` (audited);
  immutable secrets with a value refuse; a changed value on a wired stack
  rewrites the files and restarts the consumers.
- Rotation is one secret per operation and journaled: the new value is
  written as `app_<pkg>_<name>_pending` before `ALTER ROLE`, then files,
  promotion, consumer restart and a health check; any failure after the
  role may have changed reverts the role to the old value. A failed
  revert is an operation error naming the manual recovery (the pending
  row keeps the value the role holds). At daemon start a pending row is
  reconciled: the database is asked which password it accepts and store
  and files follow it (retrying while the database starts). An unknown
  current value refuses rotation up front.
- Secret files are written atomically (0600 temp file created exclusively
  in `secrets/`, mode and owner set on the descriptor, fsynced, renamed);
  symlinked files or directories are refused. The `app_` prefix is
  reserved: owner routes (`Store.Set`, config import) refuse it and only
  the catalog writes it (`Store.SetApp`).
- Install refuses to generate a database password when the database data
  directory already holds an initialised cluster (`PG_VERSION`); register
  the existing password first. Uninstall removes `secrets/` (the store
  keeps the values) and refuses a stack whose S277 detection is
  needs-migration unless `abandon_volumes` / `--abandon-volumes` is given
  (audited); run `homesteadctl data plan <pkg>` first. Delivery of
  Authentik's `file://` URIs and Immich's `DB_PASSWORD_FILE` is proven
  live (see the KVM note below).
- Apps whose database was never initialised (a crash-looping pre-S266
  Immich with an empty data directory): there is no running database to
  verify against, so register a new password with `--skip-verify`
  (allowed only while no value is stored), then adopt; Postgres runs
  initdb with it on the restart, and adopt waits for the database to
  accept connections before verifying. Never use this on a database that
  already holds data.
- `drift accept` refuses (422) while a secret file has the wrong mode or
  is missing, naming the files; accepting would only hide the finding.
  Use `drift restore` (mode) or configure (missing file).
- Running Compose by hand without Homestead: the YAML is valid on its
  own. Create `./secrets/<key>` files next to `compose.yaml` containing
  the value (`umask 077`; chown to the consuming service's uid, for
  example `chown 70 secrets/postgresql_db_password`, because the file
  is bind-mounted with its host owner and mode 0600), keep one value
  across the services that share a secret, then `docker compose up -d`.

- Proven live in KVM (S266, 8e9b66d plus ede12ba, 5682948, f5395cc; VM 2
  fresh S276 install, VM 1 pre-S266 apps with a disk image copied
  first; the catalog directory under `/usr/local/share/homestead/catalog`
  must be redeployed with the binary or the daemon keeps the old
  manifests and reports no secrets). With initialised Postgres data
  present, install of Authentik and Immich was refused naming the secret
  and the register command; after moving the old data directories aside
  it installed. `secrets/` was 0700 and files 0600 owned by 70/1000/999
  (Immich server's file is root, its container user); compose carried
  top-level `secrets:` with `file:` sources and `docker inspect` showed
  only `POSTGRES_PASSWORD_FILE`, `DB_PASSWORD_FILE` and
  `file:///run/secrets/...` values; no secret value appeared in
  compose, journal, audit, container logs, inspect, or `ps`.
  Authentik's `file://` URI syntax works for both the password and the
  secret key: server and worker authenticated, applied migrations, and
  answered 200 on `/-/health/live/` and `ak healthcheck`. Immich's
  `DB_PASSWORD_FILE` works: migrations ran and the API answered
  `pong`; the role rejects a wrong password over TCP and accepts the
  file value. Scheduled backups of both included a real `pg_dump` file
  (Authentik 230 tables, Immich 72) with mode 0600 and no secret files in
  the tree; `ps` sampling every 50 ms during the runs showed the password
  never in argv (only `read -r PGPASSWORD` from stdin). Rotation of the
  Authentik and Immich passwords changed the role (old rejected, new
  accepted), rewrote all files, restarted the consumers healthy, and a
  later backup worked; `secret_key` rotation and `--set POSTGRES_PASSWORD`
  were refused (422). A daemon SIGKILL during rotation, timed at the end
  of the `ALTER ROLE` exec (once with the file already rewritten, once
  before it), left a `_pending` row and, after systemd restarted the
  daemon, reconciliation recorded `app.secret.reconcile` ("database
  accepts the new password; settled to it"), files and store agreed and
  Authentik was healthy. A secret file chmod 0644 showed as mode drift
  without any content; restore fixed it, and accept was found to report
  success while hiding it (fixed: 5682948). On VM 1 Immich (empty,
  crash-looping database): a verified register is impossible with the
  database down, so a generated password was registered with
  `--skip-verify` and adopted; adopt found a bug (verification ran while
  initdb listened only on its socket and rolled back; fixed: f5395cc),
  after which Postgres initialised with the password, the server started
  healthy, an admin user was created, and the S277 migration moved the
  database and library to binds with the user, seeded files and
  checksums intact and a real database export in the next backup.
  Uninstall of the needs-migration Immich was refused (409) without
  `--abandon-volumes`. VM 1 Authentik (migrated, pre-S266): registering a
  wrong password was rejected, the right one accepted, adopt left it
  healthy with the `secret_key` captured from the container
  (user data intact) and its backup now holds a real export. Owner import
  of an `app_` secret is refused (422); export lists only `dr_probe`.
  The Secrets API answered with cookie plus CSRF (403 without the header).
  Found in the environment, not the slice: VM 1's default QEMU CPU lacks
  x86-64-v2, so Immich machine learning crash-looped and failed the
  migration health check (rolled back cleanly); VM 1 now boots with
  `-cpu host`.

## Provisional decisions (revisit, do not work around silently)

- **Go module path is `homestead`** (bare). No VCS remote exists in the planning
  packet, so no org URL is claimed. When a remote is chosen, rename the module in
  `go.mod` plus the `homestead/internal/...` import prefixes (one mechanical change).
- **Lint baseline is `gofmt` + `go vet`.** Adopting `golangci-lint` (or another
  aggregator) belongs to a later slice; do not add ad-hoc linters per slice.
- **Direct Go dependencies need an owning slice** (ADR-001): `modernc.org/sqlite`
  (S013, pure Go — cgo drivers would break the static daemon),
  `gopkg.in/yaml.v3` (S014, pure Go), and `golang.org/x/crypto` (S020 bcrypt,
  pure Go). Further deps arrive only with the slices that need them.

## Dashboard account surfaces (S274)

The Account page (`/account`) shows own lockout status
(`GET /api/v1/admins/lockout`: `locked`, `failures`, and `locked_until`
while locked) and changes the password (`POST /api/v1/admins/password`).
Password policy is 12 to 72 UTF-8 bytes (`auth.MinPasswordLength`,
`MaxPasswordLength`; bcrypt cannot hash more than 72). The daemon refuses
an over-long password with 422 before checking the current password, in
setup, recovery and change alike; the client counts bytes with
`TextEncoder` and mirrors both bounds. The inputs are uncontrolled and
reset by the form after every submit attempt, including validation failure,
so passwords never sit in component state; a readonly username field lets
password managers pair the credential. A successful change keeps the
caller's session and signs out the others (ADR-020). The auth interceptor
treats a 401 carrying `X-Homestead-Error: invalid-current-password` on
`POST /api/v1/admins/password` only as a form error; any other 401
expires the session and redirects to login.

## Running container drift (S262)

```bash
homesteadctl drift live <package>     # read-only live comparison
```

`GET /api/v1/catalog/{name}/drift/live` returns `{checked, findings[]}`;
`checked=false` means the Docker engine was unreachable (not clean). The
same findings ride in the S261 scheduled scan (`drifted[].live`, plus
`live_skipped` when Docker could not be read, which never fails the scan
or its operation) and the `drift-scan` doctor check. The comparison only
reads: it parses the on-disk `compose.yaml` plus `compose.override.yaml`
and compares each service's containers (matched by Compose project and
service labels) with the Engine inventory.

Findings: `image`, `port`, `network`, `mount` (wrong type, anonymous or
named volume where a bind is declared, wrong source, wrong read-only
flag, missing, unexpected), `missing`, `stopped`, `restarting`, `extra`
(unknown project container or too many containers for a service).

Normalizations, each avoiding a false positive:
- Images: `docker.io/` and `library/` prefixes drop, no tag means
  `:latest`; an image the Engine reports by ID (its tag moved) resolves
  through the image list (unresolvable IDs are not drift); digest pins
  compare by digest, using `RepoDigests` when the container shows a tag;
  `build:` services skip the image check.
- Ports: tcp default; `0.0.0.0`, `::`, and empty host IPs are one
  wildcard and the Engine's v4/v6 duplicates collapse; host IP compared
  only when compose names one (a loopback bind served on all interfaces
  is a finding); container-only mappings just need the port published;
  exposed-only ports ignored; ports compared for running containers only
  (stopped containers list none); `network_mode` services skip ports and
  networks.
- Networks: no `networks:` means `<project>_default`; keys resolve to
  `<project>_<key>` unless external or `name:`d.
- Mounts: relative bind sources resolve against the stack directory;
  symlinked paths compare by real path; secrets are read-only file
  mounts at `/run/secrets` with host source ignored; anonymous volumes
  at undeclared destinations are image `VOLUME`s and ignored; Engine-
  managed `/etc/hosts`, `/etc/hostname`, `/etc/resolv.conf` ignored;
  tmpfs size is not compared.
- Containers: `profiles` services are not expected; `scale` and
  `deploy.replicas` set the expected count; a `restart: no` service that
  exited 0 is finished; `compose run` one-offs are ignored.
- Override: image replaces; ports, volumes, tmpfs, secrets append;
  networks union; environment is irrelevant.

Resolution stays on the S213 flow; nothing is corrected automatically.
Each finding names its path. `restore` (missing, stopped, image):
S213 restore runs `compose up -d`, which starts and recreates those
containers. `recreate` (port, network, mount): `up` recreates only when
compose configuration changed, so if `compose.yaml` already matches,
remove the container (`docker compose rm -sf <service>`) and restore.
`manual` (restarting, extra): no Homestead flow converges them.
Accepting files never changes containers, and keeps the package's live
findings in the stored scan. Restore, which previously answered "no
drift" when files were clean, now also runs when at least one finding has
resolution `restore` (or file drift exists); recreate-only and manual-only
findings do not offer it, because `up` would change nothing. Each such
finding carries a `hint` with the exact command, for example
`docker compose -p <project> rm -sf <service>` then Restore (recreate),
`docker compose -p <project> logs <service>` (restarting), or
`docker rm -f <container>` (extra). Desired state is the on-disk compose
files, so a hand-edited `compose.yaml` is compared as edited (file drift
reports the edit separately).

Stopped stacks (ADR-005): when no expected service has a running or
restarting container (for example `docker compose stop` or `down` by
hand), the package reports `stack_state: "stopped"` (API, `drift live`,
dashboard, and `stopped` in the scan). It is information, not drift: it
is excluded from `Drifted`, `Clean()`, and the doctor warning, and no
Restore is offered; missing and stopped findings are suppressed (other
findings such as a wrong image still stand). Only a partial stack (some
services up, others missing or stopped) reports missing and stopped
findings with restore. Limitation: Homestead persists no desired run
state, so a stack stopped by accident reads the same as one stopped on
purpose.

Unsupported compose features are skipped, never reported as drift, and
listed as `unsupported` (API and CLI) and `live_notes` (scan): a value
with `${...}` or `$VAR` interpolation skips that field (image, a
service's ports, volumes, or networks); ports or volumes the comparison
cannot parse skip that service's comparison; `extends:` or `include:`
skips the whole package. An unparseable compose file keeps the package's
file drift and records `live_error` beside it (the single-package route
answers 422).

Implementation notes: the live compose read holds the per-project lock
(`lockProject`), as scans, accept, and restore do; the Engine is read once
per request or scan, volume mountpoints are looked up once per volume, and
the host image list is read only when an image ID or digest must be
resolved; symlinks in bind sources resolve only for a local unix-socket
Engine (a remote `DOCKER_HOST` compares paths lexically).

- Proven live in KVM (S262, cb1eb26; both VMs with jellyfin, nextcloud,
  immich, and authentik; VM 1 also runs the homestead-caddy edge and the
  authentik override): `drift live` reported zero findings on every
  healthy app (secrets mounts, tmpfs valkey, v4/v6 port pairs,
  S277-migrated binds). Stopping `immich-machine-learning` gave a
  `stopped` finding with restore and Restore started it; `docker compose
  stop` of jellyfin gave `stack_state: stopped` with no finding, no
  Restore, and no doctor warning (the scan lists it as "stopped (not
  drift)"); a retagged `immich-cache` image gave an `image` finding that
  Restore converged; `docker network connect bridge` gave a `network`
  (recreate) finding that stayed until the hinted `compose rm -sf cache`
  and Restore; a project-labelled stray container gave `extra` with the
  `docker rm -f` hint. A 5m scan recorded live findings (doctor warns on
  real drift only), and with Docker stopped it recorded
  `live_skipped: Docker engine unreachable` with the drift-scan check
  still ok; every container returned after Docker restarted. Note: the
  Engine can list a container as running for a moment after `docker stop`
  returns (observed on a health-checked container), so a live comparison
  taken in that instant reads clean; this is an Engine timing artifact,
  not a comparison bug. Not exercised: an app attached to the
  `homestead-edge` network (no app is exposed on VM 1; caddy is not a
  catalog package).

## Platform update apply (S265, ADR-027)

- Proven live in KVM (S265, bb4f451, deployed with
  `scripts/install-systemd.sh`; both appliances; throwaway Ed25519
  minisig-format key, releases built with `-ldflags` versions under
  `/srv/releases`): `homestead-apply.service` was installed and the
  daemon unit had no `/usr/local/bin` write grant. A daemon apply with
  `--follow` ran the apply unit (journal), restarted the daemon whose
  `/proc/<pid>/exe` and installed file matched the artifact digest,
  succeeded, audited before and after versions, moved the installed
  baseline, kept `homestead-server.previous` and cleared the marker;
  `--follow` survived the restart. A CLI apply replaced the binary with
  no daemon restart. Counter 2 applied; counter 1 afterwards was
  refused at verify and at apply without `--allow-older`, and with it
  applied and was audited with both versions and counters. A build that
  exits at start: the unit's 90 s health timeout failed the operation,
  the marker was cleared, the previous binary stayed, systemd stopped
  restarting at the start limit (daemon `failed`), doctor reported
  `platform-apply` failed and the journal carried the manual restore
  command, which (with `reset-failed`) brought the daemon back; the
  operation stayed failed. A binary whose embedded version differed from
  its checksums version failed the health check on the version probe and
  was reported truthfully. A tampered file under `/srv` after verify was
  refused by the daemon; the staged copy tampered while a drop-in delayed
  the apply unit was refused by the unit's re-verification
  (`verify-staged`) with the installed binary unchanged. `clear-apply`
  was refused while the unit ran; after the unit was stopped and the
  marker aged past 15 minutes (the row's `created_at` edited, the
  threshold being a constant) doctor warned, `clear-apply` failed the
  operation and cleared the marker, audited, and a second call answered
  404.
- Found and fixed in KVM: verifying a CLI release whose version equalled
  the daemon's running version seeded the CLI baseline, so the real CLI
  apply was refused as installed (ff731c7); an open operation event
  stream held the daemon's graceful shutdown until its timeout, so it
  exited with an error during an apply restart (876d943); several
  restarts within five minutes made the apply unit's own restart fail
  on the start-rate limit and left the daemon down after the undo
  (bb4f451: reset-failed first, restart again after an undone swap).
- Not exercised: the dashboard apply and clear actions; a power loss
  between the rename and the health check; a daemon killed mid-apply.
  The plan view still reports the daemon's version as the CLI's running
  version.

## Platform update rollback (S273, ADR-027)

- Proven live in KVM (S273, f4f63d8 plus 96593b7, deployed with
  `scripts/install-systemd.sh`; both appliances; throwaway Ed25519
  minisig-format key, test releases built with `-ldflags` versions under
  `/srv/releases`):
  - Automatic rollback. A build whose embedded version differs from its
    checksums failed the health probe, and a build that exits at start
    (daemon not running, past the systemd start limit) failed the health
    check; both ended `failed (rolled back)` inside the apply unit: the
    installed file and `/proc` daemon matched the previous release's
    digest, the daemon was healthy on it, the audit trail named both
    versions, the installed baseline was unchanged, the marker was clear,
    and `homestead-server.rolledback-<stamp>` held the bad build (correct
    digest, 0755). Only the last two copies were kept. Doctor reported
    `platform-apply` failed until the next successful platform operation.
  - Manual rollback. After a good apply, `rollback-platform --follow`
    without `--allow-older` was refused (422, the CLI printed the
    override hint, the API carried `X-Homestead-Error:
    older-release-needs-override`); with it the daemon rolled back, the
    baseline moved to the previous release, and the override was audited
    with both versions; `--follow` survived the restart. A rollback to
    the same version as the running binary answered 409. With the
    previous record's counter cleared in SQLite (throwaway edit), and
    for a first CLI apply whose previous was the unrecorded original, the
    rollback was refused even with `--allow-older`.
  - Failure paths. A corrupted `.previous` was refused up front by the
    daemon (no operation, nothing changed). With the apply unit delayed
    by a drop-in and `.previous` corrupted inside the window, the unit
    ended `rollback failed` ("the installed binary was not changed"),
    and doctor failed. With `.previous` corrupted while a bad apply's
    health check was running, the automatic rollback failed with the
    "daemon is still on the failed binary" message and the manual
    recovery command; following it (cp, mv, reset-failed, restart) from
    the kept binary restored the daemon.
  - The CLI component rolled back without a daemon restart (same
    daemon main PID) after a CLI apply.
  - Both appliances were restored to the main baseline, the installed
    records reset (VM 1 back to its counter 10), the test key removed,
    releases and rollback copies deleted; doctor showed only the
    release-trust warning and the apps were running.
- Found and fixed in KVM: two applies back to back (a follower starting
  the next apply when it saw the previous one end) failed with "staged
  copy size mismatch" because the apply unit cleaned the staging
  directory after finalizing and the daemon side's end-of-operation
  cleanup ran late over the new apply's staging (96593b7). Twelve
  alternating CLI applies passed afterwards on both appliances.
- Not exercised: a rollback restart that itself fails in systemd (only
  an unrestorable `.previous`; the restart-failure path is covered by the
  fake-systemd tests); the dashboard rollback action; power loss during
  a rollback; the free-space skip of the rolled-back copy.

## Craft build pipeline (S279, ADR-028)

What exists: `internal/craft` (manifest parser, verify-then-build
operation, recorded-image store and gates), `internal/docker/build.go`
(`BuildClient`, the docker CLI behind `craft.Builder`), `images/craft/`
(shared nginx Dockerfile and `nginx.conf`, base pinned by digest),
`scripts/craft-manifest.sh` and `scripts/sign-craft-manifest.sh`,
migration 39 (`craft_trust_keys`, `craft_trust_removed`, `craft_baseline`,
`craft_verdicts`, `craft_images`), `internal/server/craft.go` (routes), the
`craft` CLI group and the Craft images panel on the Updates page. Formats,
the check order, signing and the manual digest check are in
`docs/architecture/RELEASE_VERIFICATION.md`. No manifest, catalog entry or
storytold source existed then; S249 added them (below).

Local testing:

- `go test ./internal/craft` uses a fake docker builder, test keys and a
  fake fetcher; it also runs the manifest script against the daemon's
  digest. `HOMESTEAD_DOCKER_TESTS=1 go test ./internal/docker -run
  TestRealCraftBuild` builds a real image from a synthetic site with a
  throwaway key (needs Docker and network for the pinned base), runs it
  with a read-only root and only `/tmp` writable, and checks the `no-cache`
  HTML, immutable hashed assets and `application/wasm`.
- Craft routes are session routes (ADR-020); the install gate and the
  Compose-up gate are package seams in `internal/catalog/craft.go`
  (`CraftChecker`, `CraftRecordedID`), set by `Server.MountCraft`. S249
  names a craft image `homestead/craft-<app>:<version>`.

### KVM acceptance steps (synthetic artifact; proven live, see below)

On the appliance (Debian 13; `sudo apt-get install -y minisign`), after
`scripts/install-systemd.sh` has deployed the daemon and catalog (the
script copies `images/craft` to
`/usr/local/share/homestead/catalog/craft/context`), and `homesteadctl
login`:

```
W=/srv/craft-kvm; sudo mkdir -p $W && sudo chown $USER $W && cd $W && mkdir -p art site
echo '<html>kvm</html>' > site/index.html; printf '\0asm' > site/app.0123456789ab.wasm
tar -czf art/synthcraft-1.0.0.tar.gz -C site .
minisign -G -W -p craft.pub -s craft.key                       # throwaway key
CTX=/usr/local/share/homestead/catalog/craft/context
~/homestead/scripts/craft-manifest.sh --counter 5 --artifact-dir art --context $CTX \
    --app synthcraft 1.0.0 synthcraft-1.0.0.tar.gz > craft-manifest.yaml   # no --url: offline
~/homestead/scripts/sign-craft-manifest.sh craft-manifest.yaml --key craft.key
# throwaway catalog entry (not committed); contents in the S279 transcript below
sudo cp synthcraft.yaml /usr/local/share/homestead/catalog/ && sudo systemctl restart homestead-server
```

1. Untrusted key: `homesteadctl craft plan synthcraft --manifest
   $W/craft-manifest.yaml --artifact $W/art/synthcraft-1.0.0.tar.gz`
   refuses (signature check). `homesteadctl craft trust add --key-file
   $W/craft.pub` then plan verifies. `craft-trust.yaml` appears beside
   `desired.yaml`.
2. `craft build synthcraft ... --follow` (same flags) succeeds with the
   network blocked (`sudo iptables -I OUTPUT 1 -o ens+ -m conntrack
   --ctstate NEW -j REJECT`, removed afterwards; a blanket REJECT also
   kills the SSH session; pre-pull the pinned nginx base with `docker pull`
   first, since the daemon fetches it); `craft status` shows the tag matching
   its recorded ID. `apps plan/install synthcraft -f body.json` (the
   acceptance-script body with `container_ports: {"web": 80}`) installs;
   install is refused before the build (and again after step 5).
   (Review hardening: a counter below the highest of any craft app is a
   replay and needs `--allow-older`; archives with `..`, links or devices,
   or expanding past the cap, are refused; the Dockerfile base must be
   digest-pinned.)
   Builds are asynchronous: add `--follow` to see the failing check;
   without it the CLI prints only the operation ID.
3. Refusals each print the failing check and leave no tag or record
   (`docker images`, `craft status`): flip one byte of the artifact copy
   (`artifact-digest`); change a file in
   `/usr/local/share/homestead/catalog/craft/context` and restore it
   (`build-context-digest`); a manifest signed by a second, untrusted key
   (`signature`); an older counter (`counter`, with the override hint) and
   then `--allow-older` (audited with both counters).
4. A fetch failure (a manifest with `--url https://127.0.0.1:9/x`) is
   retryable and leaves nothing. For mirrors, serve the artifact over local
   HTTPS (openssl self-signed cert with `subjectAltName=IP:127.0.0.1`) and
   trust it for the daemon with a drop-in `Environment=SSL_CERT_FILE=...`
   (daemon-reload and restart; remove afterwards); give the manifest two
   `--url` flags with the wrong bytes first, and a third URL that 302s to
   `http://`.
5. Replace the image behind the daemon's back (`docker tag alpine:latest
   homestead/craft-synthcraft:1.0.0`): `craft status` reports it differs,
   `apps configure`/restore refuse to start the stack (the operation
   fails at `up`, after `write-config`), and a container recreated outside
   Homestead (`docker compose up -d --force-recreate` on the stack) makes
   `homesteadctl drift live synthcraft` report the image finding (a running
   container keeps its image, so the finding needs a recreate); a rebuild
   repairs it (or re-tag the original image ID). Keep the tag intact before
   step 6: an update run while the tag points elsewhere fails at `rewrite`.
6. Bump to 1.1.0 (catalog entry and a counter 6 manifest):
   `updates list` shows the craft move, `updates apply synthcraft
   --craft-manifest ... --craft-signature ... --craft-artifact ...
   --follow` rebuilds, updates, then prunes 1.0.0 (tag and record).
7. Remove the throwaway key (`craft trust remove <key id>`), catalog
   entry, stack (`apps uninstall`), images (`docker rmi` the craft tag and
   the pulled base) and, if you want no trace, the `craft_images`,
   `craft_baseline` and `craft_verdicts` rows. `scripts/install-systemd.sh`
   re-syncs the catalog and deletes the throwaway `synthcraft.yaml`: copy
   it again after every redeploy.

#### Proven live in KVM (2026-10-15, VM 2, main 9d90f8b plus 44801da)

Synthetic artifact (a 205-byte tarball), throwaway minisign key, throwaway
catalog entry placed only on the VM. All steps above passed:

- Untrusted key: `craft plan` refuses (`signature`); after `craft trust
  add` it verifies and `craft-trust.yaml` appears. A craft-signed file
  given to `updates verify` is refused as an untrusted platform key. The
  reverse (platform-signed craft manifest) is covered only by unit tests:
  the platform private key is discarded.
- Offline build with new outbound connections rejected: verified, built
  with `docker build --network none` (seen in the process list), image
  recorded by full ID; install served `application/wasm`, `no-cache` HTML
  and `public, max-age=31536000, immutable` hashed assets from the
  container; `drift live` clean.
- Refused before any build, leaving no tag or record: flipped artifact byte
  (`artifact-digest`), tampered context file (`build-context-digest`),
  unknown key (`signature`), counter 4 below 5 (`counter`; accepted with
  `--allow-older`, audited as `craft.build.override` with both counters),
  counter 5 with another artifact (`counter-conflict`), unpinned `FROM`
  (`build-context-digest`), archives with a symlink, a `..` entry or 1.5
  GiB of expansion (`artifact-digest`, scan-archive).
- Fetch: with the first of two HTTPS mirrors serving wrong bytes the second
  was used; an https to http redirect was refused; an untrusted
  certificate failed as a retryable `fetch`.
- Replacing the image behind the daemon: `craft status` reports DIFFERS,
  configure refused to start (`local image does not match the recorded
  digest`), `drift live` reported the image finding after an outside
  recreate, a rebuild repaired it.
- Update to 1.1.0 (counter 7): rebuilt, updated, then pruned the 1.0.0 tag
  and record; the running image was never pruned.

Bug found and fixed (44801da): every build failed with `mkdir
/root/.docker: read-only file system` under the hardened unit; the build
now gets a private `DOCKER_CONFIG`.

Not proven live: `${VAR}` image indirection (the override file rejects an
`image` field and every Homestead start path regenerates `compose.yaml`
first, so only unit tests cover it); the Compose-up gate for the
database-only migration start (`composeUpServices`); the edge/proxy route
to the app (checked on the container address instead); the mirror path
over a publicly trusted certificate. Note `--allow-older` lowers the
recorded baseline to the overriding manifest's counter (the baseline moves
forward otherwise).

Planning note (2026-10-15): the Compose-up gate for `composeUpServices` and the edge route to the app are acceptance criteria of S249; local HTTPS for the route comes from S280.

Synthetic catalog entry used above (valid against the S160 schema):

```yaml
name: synthcraft
version: 1.0
title: Synthcraft
blurb: Synthetic craft image for S279 acceptance.
description: Throwaway package used only to exercise the craft build pipeline.
category: media
icon: mdi:web
services:
- name: web
  image: homestead/craft-synthcraft:1.0.0
  exposes: web
  data:
  - target: /tmp
    ephemeral: true
    reason: nginx pid and temporary files
requirements:
  memorymb: 64
  cpucount: 1
  architectures: [amd64, arm64]
exposure:
  package: synthcraft
  endpoints:
  - endpoint: web
    recommended: local
    allowwider: false
```

## Internal CA trust distribution and local TLS (S280, ADR-030)

Implemented: persisted internal CA (`internal/cert/localca.go`; key sealed as
a `sys_` secret, public material in SQLite and `internal-ca.yaml`), leaf
issuance and renewal for local-only edge hosts (`internal/cert/localtls.go`,
scheduler job `tls:local-leaves`, edge snippets via the route reconciler),
API routes under `/api/v1/tls/local-ca` (root.pem/root.der are the one
unauthenticated read-only exception, ADR-030), `homesteadctl tls root
show|export` and `tls local status|create|leaves|reissue|rotate`, the
dashboard Trust page (`/trust`), doctor checks (`local-ca`,
`local-leaf:HOST`), and docs/architecture/INTERNAL_CA_TRUST.md. Parity rows
are in docs/PARITY.md. Management TLS served by the daemon itself stays
deferred (docs/DEFERRED.md).

Leaf lifetime is 90 days; `HOMESTEAD_LOCAL_LEAF_LIFETIME` (a duration of at
least 1h, root-owned unit only) shortens it for acceptance. Doctor grades the
root warn inside 365 days and fail inside 30 days of expiry; a leaf is due for
renewal with a third of its lifetime left.

Automated coverage: CA persistence across restart and restore, missing-key
refusal, key confined to the secret store, name constraints, leaf chain and
SANs, edge presentation only for local-only hosts, renewal with an injected
clock, rotation plan and stale-digest refusal, route classification, absence
from the recovery socket, no key in responses, audit or configuration export.

KVM acceptance (2026-10-17, VM 2, commit cdd388f deployed with
`scripts/install-systemd.sh` and an embedded dashboard build; Debian 13,
Docker, Caddy deployed by `homesteadctl edge deploy`). All steps passed; no
product bug found.

1. Create and fingerprint: `homesteadctl tls local create`, then the CLI
   (`tls root show`), the authenticated `GET /api/v1/tls/local-ca`, the
   unauthenticated `root.pem` and `root.der` (also fetched through an ssh
   tunnel from the KVM host) and `openssl x509 -fingerprint -sha256` all gave
   `BD:33:0E:98:...:50:7E:19`; `tls root export` reported the match. No
   private key in either download. `root.pem` and `root.der` return 200
   without a session; `local-ca`, `leaves` return 401 and the rejection is
   audited. The SPA `/trust` route serves and the bundle contains the "Trust
   this appliance" page (the page was not driven in a browser).
2. Local HTTPS: route `s280.lan` (Exposure local, TLS internal) to an nginx
   container on `homestead-edge`. The leaf was issued on the route reconcile
   and presented by Caddy. `curl --resolve` failed with verify 20 before
   trust and returned 200 without `-k` after `update-ca-certificates` on the
   guest, and from the KVM host through a tunnel to 443 with
   `--cacert root.pem`.
3. Renewal: with `HOMESTEAD_LOCAL_LEAF_LIFETIME=3h`, `tls local reissue`
   changed the served serial (`s_client`) with the Caddy container start time
   and daemon PID unchanged. With `1h` the scheduler job `tls:local-leaves`
   (every 10 minutes) renewed the leaf unattended at its one-third mark
   (14:33 to 15:02, serial changed, audited as actor `scheduler`), again with
   no Caddy or daemon restart.
4. Rotation: `tls local rotate` printed the plan and digest. A made-up digest
   and a digest made stale by adding a second route were both refused (409,
   root unchanged). Approving the fresh digest created generation 2
   (`BE:76:09:...:DE:E5`, matches CLI, download and `openssl`), re-issued both
   leaves under it (edge verifies against the new root only; the old root,
   still trusted on the guest, now fails curl until replaced), and the secret
   `sys_local_ca_key_1` was replaced by `sys_local_ca_key_2`.
5. Doctor showed `local-ca` and each `local-leaf:HOST` as ok, before and after
   rotation.
6. No plaintext private key: `PRIVATE KEY` is absent from the sqlite dump and
   the raw database and WAL, `/etc/homestead` (including `internal-ca.yaml`),
   `config export`, the audit list, and the journal (only my own grep
   commands matched). The CA key is only the sealed `sys_local_ca_key_N`
   secret row. The leaf private keys exist as plaintext only in the Caddy
   routes cert directory (mode 0600, root), which the edge needs, as in S278.
   Per-package backups (age-encrypted app data) contain no `PRIVATE KEY`
   match and do not carry the daemon database, so no backup job was run for
   this check.
7. Removal: after the routes were cleared the leaves showed as `orphan`; the
   scheduler removed them after the 15 minute grace (audited
   `tls.local-leaf.remove`), leaving the cert directory empty and
   `local_leaves` empty. Root removed from the guest store
   (`update-ca-certificates --fresh`) and the host's temporary CA file. Not
   exercised: a database restore (covered by automated tests) and a browser
   run of the Trust page.

Left behind on VM 2: the S280 daemon build, the internal CA itself
(generation 2; there is no remove command), the `homestead-edge` network and
`/opt/homestead/stacks/caddy` files (the Caddy container is removed), and
`/home/tester/hs-s280`. The test container, unit drop-in and trust-store entry
were removed. Disk snapshot before the run: `vm2-pre-s280.qcow2`.

## WordCraft package and the shared static craft shape (S249, ADR-028)

Built: the first craft package and the shape S250 to S253 reuse
(`docs/architecture/STATIC_CRAFT_PACKAGES.md`).

- `catalog/wordcraft.yaml` (version 0.7.0, image
  `homestead/craft-wordcraft:0.7.0`, one `ephemeral` data path `/tmp`, no
  storage class, local-only exposure with `allowwider: false`).
- Vendored release storytold/wordcraft v0.7.0 (still the latest on
  2026-10-17): `wordcraft-web-0.7.0.zip`, SHA-256
  `98df7c68a0ac788a9ac0f531fd34c31ab7d89965abb4935f57168d287dab3189`,
  equal to upstream `SHA256SUMS.txt`. Licence `MIT OR Apache-2.0`, judged
  compatible; details, NOTICE capture and the artifact record are in
  `docs/THIRD_PARTY.md`.
- `catalog/craft/craft-manifest.yaml`: the **unsigned** craft manifest
  (counter 1, built with `scripts/craft-manifest.sh`). No signing key was
  generated or committed. A test fails if `images/craft/` changes without
  the manifest being regenerated.
- `images/craft/` reworked to the shared shape: non-root `nginx` on 8080,
  `/healthz` and an image `HEALTHCHECK`, precompressed `.gz` assets with
  `gzip_static`, hashed-asset detection that matches `name-<hex>_bg.wasm`,
  top-level archive directory stripped, config made readable for the
  unprivileged user whatever mode the staged context carries (the real
  Docker integration test found that: a 0600 `nginx.conf` crashed the
  non-root server).
- `scripts/craft-header-check.sh` (in `lint-scripts`), the header table
  (`docs/architecture/CRAFT_HEADER_TABLE.md`) and the signing runbook
  (`docs/architecture/CRAFT_SIGNING_RUNBOOK.md`).
- Catalog: craft-tag validation in `Manifest.Validate` (local form only,
  own app, manifest version), `Manifest.Stateless()`, `CraftImages()`,
  `ValidateCraftListing`; relaxed curated tests (`curatedPackages`); shared
  `craftPackages` table test; `knownImageVolumes` row.
- Stateless backup handling: schedule save 422, per-package read answers
  `stateless`, no scheduler job, `backup.ErrStateless` from `backupJob` and
  `guardBackupSources`, pre-update backup skipped with a note and an
  `update.backup.skipped` audit entry; CLI and Backups/Updates pages show it.

### Inherited S279 items: causes found

Both were reproduced by reading the code, not live; the live proof is the
KVM acceptance below.

1. **Compose-up gate.** `composeUp` called `checkCraftStack`, but its
   siblings did not: `composeUpServices` (the database-only
   `up SERVICE...` of migrations, which also creates dependencies) and
   `composeStart` were bare `composeRun` calls, and the update applier ran
   its own `docker compose up -d` without the gate. A craft service could
   therefore start on those paths with a missing or replaced image. All
   three now take the same gate (`TestEveryComposeStartPathTakesTheCraftGate`
   covers mismatch, missing record and no checker for each path).
2. **Edge route answered nothing.** The planner's route upstream is the bare
   service name (`web:80`), which resolves only on a network shared with
   Caddy (`homestead-edge`). The edge network was added only if the caller's
   `networks` list contained it: the dashboard defaults it in, but the CLI
   and `scripts/acceptance.sh` send `package-net`, so the stack never joined
   the edge and the proxy had no path to it (the S280 test route worked
   because that container was attached by hand). `PlanInstall` now always
   joins a routed stack to `homestead-edge` (keeping the project default
   network when none was named) with a plan note
   (`TestPlanInstallAttachesRoutedStacksToTheEdgeNetwork`).

### Verified locally (dev workstation, 2026-10-17)

A real build of v0.7.0 with the shared Dockerfile (base pinned by digest,
`--build-arg` artifact digest) run with `--read-only --tmpfs /tmp
--cap-drop ALL`: `docker image inspect` shows no VOLUME, user `nginx`,
healthcheck `healthy`, `docker diff` empty. `scripts/craft-header-check.sh`
PASS: `/healthz` 200; `index.html` `no-cache`; hashed `_bg.wasm`
`application/wasm`, `public, max-age=31536000, immutable`, gzip (39.8 MB to
14.9 MB, decompressed SHA-256 equal to the original); hashed `.js` immutable;
no COOP/COEP. `HOMESTEAD_DOCKER_TESTS=1 go test ./internal/docker -run
TestRealCraftBuild` passes with the new shape. The local image was removed
afterwards. Not a substitute for the appliance run.

### KVM acceptance (2026-10-17, VM 2, HEAD 120d517, dashboard embedded build)

Run on VM 2 (Debian 13, Docker, internal CA generation 2 from S280; the Caddy
edge was redeployed with `homesteadctl edge deploy`) with a throwaway minisign
key (`minisign -G -W`, generated and signed on the guest, trusted with `craft
trust add`, removed with `craft trust remove` and shredded afterwards; nothing
committed). The daemon built `wordcraft-web-0.7.0.zip` after downloading it
over HTTPS from GitHub. Binary sha256 `c41f148a...` equals a root build of the
same tree on the guest. Disk snapshot before the run: `vm2-pre-s249.qcow2`.
Two product bugs were found (below); the other steps passed.

1. **Verify-then-build and install: pass.** `craft plan` with the signed
   manifest verified (counter 1); `craft build --follow` verified, fetched,
   scanned, built (about 9 s) and recorded
   `sha256:e924534d0a86...`. `apps install wordcraft` through the CLI with a
   body naming only `wordcraft-net` and `container_ports {"wordcraft": 8080}`
   passed (`verify-craft-images`, `write-stack`, `up`), the plan note showed
   the edge attach, and the generated compose joins `homestead-edge`. After
   `edge routes set` (host `wordcraft.lan`, upstream `wordcraft:8080`, local,
   TLS internal) the edge answered: the S279 "route returns nothing" finding
   is closed live. The install does not create the route; the owner or
   dashboard sets it with the route table (existing behaviour).
2. **Image and container: pass.** `docker image inspect`: `Config.Volumes`
   null, user `nginx`, healthcheck on `/healthz`, container `healthy`;
   `docker diff` empty with `/tmp` the only mount (a tmpfs). The
   `knownImageVolumes` row comment in `internal/catalog/data_test.go` is
   finalised.
3. **HTTPS: pass except gzip through the edge.**
   `https://wordcraft.lan/` answered with the internal CA leaf (curl with
   the root, no `-k`, `--resolve` to 127.0.0.1); the page loads its
   `index.html`, `.js` and `_bg.wasm`, and the wasm equals the upstream zip's.
   No `SharedArrayBuffer` in the JS or wasm and no COOP/COEP sent. Verified
   values are in `docs/architecture/CRAFT_HEADER_TABLE.md`. **Bug 1:**
   `scripts/craft-header-check.sh` FAILS "not gzip-encoded" through Caddy
   (39,785,016 bytes, no `Content-Encoding`) while the container answers gzip
   (14,947,550 bytes) when asked directly: nginx `gzip_proxied` defaults to
   `off` and Caddy adds `Via`. Repro: any `curl -H 'Accept-Encoding: gzip'`
   of the `_bg.wasm` through `https://wordcraft.lan/` (or directly with `-H
   'Via: 1.1 Caddy'`). Fix, checked on a container with the config mounted:
   `gzip_proxied any;` in `images/craft/nginx.conf`, then regenerate and
   re-sign the craft manifest (the context digest changes). Not applied here.
4. **Compose-up gate: pass for the reachable paths.** With
   `homestead/craft-wordcraft:0.7.0` re-tagged to busybox, `craft status`
   reported DIFFERS; `apps configure` (operation fails at `up` with "local
   image does not match the recorded digest", after `write-config`) and `drift
   restore` (after a hand edit of compose.yaml; `restore-files` then `up`
   refused) both refused and the running container was untouched; `apps
   install` over the install answers 409. `updates apply` with the swapped tag
   did not refuse: the update's own verify-then-build ran first and re-tagged
   the verified image (a rebuild repairs by design), then failed at `rewrite`
   with `image "sha256:e924..." not found in compose` because the package was
   already current (cosmetic: the message names an ID, not the tag, and a
   no-op apply rebuilds). **Not reachable live for wordcraft:** the
   database-only `composeUpServices` and `composeStart` (migration; `data
   plan wordcraft` answers 422 "nothing to migrate") and the secret-rotation
   restart (wordcraft declares no secrets; `--rotate-secret` answers 422).
   Those gates remain covered by `TestEveryComposeStartPathTakesTheCraftGate`.
   The tag was restored with `craft build`.
5. **Stateless: pass.** `backup schedule --package wordcraft` and the API
   answered 422 naming wordcraft; `GET /backups/schedules/wordcraft` answers
   200 `stateless: true`; `backup schedules` and `scheduler jobs` list no
   wordcraft job; `updates apply wordcraft --backup` proceeded and audited
   `update.backup.skipped` ("wordcraft is stateless and keeps nothing on the
   appliance").
6. **Trust refusals: pass.** A copy of the zip with one flipped byte was
   refused by `craft plan` and `craft build --artifact` ("artifact-digest
   check failed", audited, no tag change); the recorded-digest mismatch is
   step 4.
7. **Lifecycle: install and remove pass; drift after update FAILS (bug 2).**
   Drift scan (scheduler job `drift:scan`, interval set to 5 minutes for the
   run and restored to 24 h) reported clean with 5 packages after install.
   `apps uninstall` removed the container and the generated compose files
   (the stack directory remains, empty, as for every package); clearing the
   route removed the Caddy snippet; the leaf showed `orphan` until the S280
   grace period; the craft image stays. Reinstall passed with the same leaf.
   Update to a throwaway catalog entry 0.7.1 plus a counter 2 manifest (same
   artifact, signed with the throwaway key): `updates list` showed the craft
   move, `updates apply --backup --craft-manifest ... --craft-signature ...
   --follow` verified, built `0.7.1`, recorded, rewrote, started and pruned
   the 0.7.0 tag. **Bug 2:** the next scheduled drift scan then reported
   `drift: wordcraft compose.yaml (modified)` (managed 0.7.0, current 0.7.1).
   The update applier (`internal/update/apply.go`, `rewriteImages`) rewrites
   `compose.yaml` but never calls `catalog.RecordFingerprints`, so the stored
   hash is stale after every update, not only craft updates. Repro: install,
   `updates apply` any pending move, wait for or run a drift scan. A
   `drift accept wordcraft` clears it; `drift live` stayed clean. Not fixed here.
8. **Cleanup: done.** Package uninstalled, route table emptied, throwaway
   key removed from the appliance and shredded, work directory and
   temporary files deleted, catalog redeployed with `install-systemd.sh`
   (the bumped catalog entry and signature are gone), the 0.7.1 craft image
   removed, the Caddy container removed.

Left behind on VM 2: the S249 daemon build and catalog, the
`homestead-edge` network and `/opt/homestead/stacks/caddy` files, the empty
`/opt/homestead/stacks/wordcraft` directory, an orphan `wordcraft.lan` leaf
awaiting its scheduler cleanup (the daemon removes it after the grace
period), `craft_baseline`, `craft_verdicts` and `craft_images` rows for the
throwaway run (counter 2 baseline), Docker build-cache layers from the craft builds
and `/home/tester/hs-s249`. Daemon hash
unchanged from the build above.

Both bugs were fixed in commit 37fa9c2 and re-verified live (below).

### S249 KVM fix re-verification (2026-10-17, VM 2, HEAD 37fa9c2)

Fixes: `gzip_proxied any;` in `images/craft/nginx.conf` (manifest
`context_digest` regenerated to `sha256:1880843e...`, still unsigned in the
repo; `TestRealCraftBuild` now also requests with `Via: 1.1 Caddy`), and
`internal/update` records generated-file fingerprints after a successful
update and after a rollback (`UpdateRequest.Store`). `craft status` DIFFERS
now shows the recorded digest. The no-op-apply error naming an image ID
remains (DEFERRED). Binary sha256 `792ee683...` equals a build of the same
tree on the guest. Throwaway minisign key and manifests (counter 3, then 4)
lived in `/srv/s249fix`, trusted with `craft trust add`; nothing committed.

- Install and edge: `craft build` (counter 3), `apps install wordcraft`,
  route `wordcraft.lan` (local, TLS internal); `scripts/craft-header-check.sh`
  **PASS** including gzip through the edge: wasm 14,947,550 bytes with
  `Accept-Encoding: gzip` versus 39,785,016 identity (before the fix: 39.8 MB
  either way).
- Update to 0.7.1 (counter 4, `updates apply --craft-manifest ...`)
  succeeded; the scheduled drift scan afterwards (16:19Z, 5 packages) was
  **clean** with no `drift accept`; `drift review wordcraft` also clean.
- Cleanup: app uninstalled, routes emptied, Caddy container and craft images
  removed, trust key removed, key shredded, `/srv/s249fix` deleted, drift
  interval restored to 24 h, catalog and daemon redeployed with
  `install-systemd.sh`. Left: `craft_baseline` at counter 4, craft rows, the
  caddy stack files and `homestead-edge` network, the `/home/tester/hs-s249`
  tree (now at 37fa9c2).

## Office craft packages: GridCraft, PdfCraft, DeckCraft (S250, ADR-028)

Built: three more packages on the S249 shared shape
(`docs/architecture/STATIC_CRAFT_PACKAGES.md`), YAML, manifest entries, test
rows and docs only. No Go code and no change to `images/craft/` (the build
context digest is unchanged).

- `catalog/gridcraft.yaml` (0.7.0), `catalog/pdfcraft.yaml` (0.8.0),
  `catalog/deckcraft.yaml` (0.7.0): each its own image
  `homestead/craft-<app>:<version>`, own host, one `ephemeral` `/tmp`, no
  storage class, no `requirements.ports`, `sso.class: unsupported`,
  local-only with `allowwider: false`, stateless.
- Vendored releases (latest on 2026-10-17; each SHA-256 equals upstream
  `SHA256SUMS.txt` and an independent hash of the download):
  gridcraft v0.7.0 `ad10af2b...` (7,113,559 bytes), pdfcraft v0.8.0
  `1f9ea51f...` (14,113,243 bytes), deckcraft v0.7.0 `4ab91994...`
  (9,385,879 bytes). The full digests, licence judgement (all three
  `MIT OR Apache-2.0`, compatible) and verbatim NOTICE captures are in
  `docs/THIRD_PARTY.md`. DeckCraft's NOTICE adds symphonia (MPL-2.0, used
  unmodified) and Ubuntu Light (Ubuntu Font Licence 1.0).
- `catalog/craft/craft-manifest.yaml` regenerated with
  `scripts/craft-manifest.sh`: four apps, **counter 2** (one above the
  S249 manifest; one counter for the whole set), context digest unchanged,
  still **unsigned**. No key was generated or committed. The owner signs the
  four-app manifest per the runbook.
- Test rows: `curatedPackages`, `craftPackages`, `TestCuratedSSO`, and three
  `knownImageVolumes` rows (finalised after the KVM run).
- Header table: three new rows and notes. Layout is the WordCraft layout in
  every case (one hashed js, one hashed `_bg.wasm`, optional hashed
  favicon, no `parts.txt`, `.part-*`, workers or extra asset classes), no
  `SharedArrayBuffer`, COOP/COEP or workers; WebGPU (wgpu, WebGL2
  fallback) and the clipboard want a secure context.

### Verified locally (dev workstation, 2026-10-17)

Each release was downloaded into a fresh empty directory, hashed, unzipped
and inspected without running anything from it; the shared Dockerfile was
built with the real artifact and run with a read-only root, tmpfs `/tmp`,
all capabilities dropped. `scripts/craft-header-check.sh` passed for all
three (healthz 200, entry `no-cache`, `application/wasm`, immutable, gzip,
no COOP/COEP); containers reported `healthy`, `docker diff` was empty,
`Config.Volumes` absent, user `nginx`. The `.htaccess` request answers 404.

### KVM acceptance (2026-10-17, VM 2, HEAD fab4a5b, dashboard embedded build)

Run on VM 2 (Debian 13, Docker, internal CA from S280). Binary sha256
`b7d26121...` equals a guest build of the same tree (with `embeddash`). A
throwaway minisign key (`minisign -G -W`, generated on the guest in `/srv`,
trusted with `craft trust add`) signed a four-app manifest at counter 5
(install; same bytes as the committed manifest apart from the counter) and
at counter 6 (update, same artifacts with versions bumped to gridcraft
0.7.1, pdfcraft 0.8.1, deckcraft 0.7.1, plus matching throwaway edits of
the installed catalog). Artifacts were downloaded on the guest over HTTPS
and matched the recorded SHA-256. Nothing was committed. **All steps passed
for all three apps; no product bug found.**

- **Build and install:** `craft plan` verified (counter 5) and `craft build
  --follow` built each image (`sha256:0145d7b5a524...` gridcraft,
  `f6c54ba1dcfb...` pdfcraft, `61127991a062...` deckcraft). `apps install`
  through the CLI (body: `domain`, the app's own network, `container_ports
  {app: 8080}`) passed `verify-craft-images`, `write-stack`, `up`. Routes
  `<app>.lan` (local, TLS internal) set with one `edge routes set`.
- **Image and container:** `Config.Volumes` null, user `nginx`, healthcheck
  on `/healthz`, container `healthy`; `docker diff` empty; `/tmp` the only
  mount (tmpfs); the stack is on its own network plus `homestead-edge`.
- **HTTPS and headers:** entry page and wasm fetched over `https://<app>.lan/`
  with the S280 root (no `-k`); `scripts/craft-header-check.sh` PASS for all
  three, gzip through Caddy included: gridcraft wasm 7,045,260 bytes gzip
  versus 18,886,586 identity, pdfcraft 14,064,403 versus 38,059,629,
  deckcraft 9,345,219 versus 25,537,284. Gzip-decoded and identity bodies
  are equal and equal the wasm in the upstream zip. Values are in
  `docs/architecture/CRAFT_HEADER_TABLE.md`.
- **Drift:** the scheduled scan (interval set to 5 minutes for the run,
  restored to 24 h) was clean with 7 packages after install and again after
  the update, with no `drift accept`; `drift review` clean for each app.
- **Stateless:** `backup schedule --package <app>` answered 422 naming the
  app; `backup schedules` and `scheduler jobs` list no craft job; `updates
  apply` skipped the pre-update backup with a note.
- **Remove and reinstall:** `apps uninstall` removed the container and
  compose file (the empty stack directory remains, as for every package);
  clearing the route table removed the Caddy snippets and left no upstream.
  The leaves showed `orphan`; the same leaves came back `ok` on reinstall
  (same expiry, no re-issue). Orphan leaves are swept by the `tls:local-leaves`
  job (6 h interval), not at the 15-minute grace, so the orphans were still
  listed at the end of the run (a `wordcraft.lan` orphan from the S249 run
  was still there 1 h 50 m later). The craft images stay after uninstall by design.
- **Update:** `updates list` showed `homestead/craft-<app>:<old> -> <new>
  (manifest)`; `updates apply --craft-manifest ... --craft-signature ...`
  verified (counter 6), built, recorded, rewrote, started and pruned the old
  tags for all three; header check PASS again. The rebuilt images are
  byte-identical (same image IDs), so the build is reproducible.
- **Cleanup:** apps uninstalled, route table emptied, Caddy container and
  the 0.7.1/0.8.1/0.7.1 images removed, trust key removed, secret key
  shredded, `/srv` work directory deleted, drift interval restored,
  catalog and daemon redeployed with `install-systemd.sh`. Left on VM 2:
  `craft_baseline` at counter 6 (gridcraft, pdfcraft, deckcraft at 6,
  wordcraft at 4), craft verdict and image rows, orphan `.lan` leaves until
  the next sweep, the empty stack directories, the `homestead-edge` network
  and Caddy stack files, and `/home/tester/hs-s250`.

## Graphics craft packages: VectorCraft, PhotoCraft, LightCraft, FigCraft (S251, ADR-028)

Built: four more packages on the S249 shared shape
(`docs/architecture/STATIC_CRAFT_PACKAGES.md`): YAML, manifest entries, test
rows, docs, and one script option. No Go code and no change to `images/craft/`
(the build-context digest `sha256:1880843e...` is unchanged).

- `catalog/vectorcraft.yaml` (0.12.0), `catalog/photocraft.yaml` (0.9.0),
  `catalog/lightcraft.yaml` (0.8.0), `catalog/figcraft.yaml` (0.6.0): each its
  own image `homestead/craft-<app>:<version>`, own host, one `ephemeral`
  `/tmp`, no storage class, no `requirements.ports`, `sso.class: unsupported`,
  local-only with `allowwider: false`, stateless. Descriptions name the
  function and carry the WebGPU/WebGL2 and secure-context advisory.
- Vendored releases (latest on 2026-10-17; each SHA-256 equals upstream
  `SHA256SUMS.txt` and an independent hash of the download): vectorcraft
  v0.12.0 (v0.11.0 at planning) `83ef4aaf...` (22,585,168 bytes), photocraft
  v0.9.0 `b3fa5ab6...` (11,467,035), lightcraft v0.8.0 `bfabb7ed...`
  (66,660,218), figcraft v0.6.0 `a414970e...` (8,892,594). Licence for all
  four is `MIT OR Apache-2.0`, judged compatible; full digests, licence
  hashes and verbatim NOTICE captures are in `docs/THIRD_PARTY.md`. LightCraft's
  NOTICE records that its optional GPL-3.0 RawNIND weights, SAM 3 weights and
  GeoNames data are never shipped (user-initiated downloads only).
- `catalog/craft/craft-manifest.yaml` regenerated with
  `scripts/craft-manifest.sh`: eight apps, **counter 3** (one above S250), one
  counter for the whole set, still **unsigned**. No key was generated or
  committed.
- Test rows: `curatedPackages`, `craftPackages`, `TestCuratedSSO` and four
  `knownImageVolumes` rows finalised after the KVM run.
- Header table: four rows and notes. VectorCraft, PhotoCraft and FigCraft are
  the WordCraft layout. LightCraft keeps unhashed file names (so `no-cache`,
  never `immutable`), runs a module worker and ships an extra font and
  precompressed files; the existing shared rules already serve it correctly,
  so no image change. `scripts/craft-header-check.sh` gained `--unhashed`
  (expects `no-cache` for wasm and js) and `bash -n` still passes. None of the
  four needs `SharedArrayBuffer`, COOP or COEP.
- The stale WordCraft gzip-through-the-edge finding in the header table was
  rewritten as resolved (fix 37fa9c2, re-verification 2026-10-17).

### Verified locally (dev workstation, 2026-10-17)

Each release was downloaded into a fresh empty directory, hashed, unzipped
and inspected without running anything from it; the shared Dockerfile was
built with the real artifact and run with a read-only root, tmpfs `/tmp`,
all capabilities dropped. `scripts/craft-header-check.sh` passed for all four
(LightCraft with `--unhashed`): healthz 200, entry `no-cache`,
`application/wasm`, gzip (also with `Via: 1.1 Caddy`), no COOP/COEP; the
containers reported `healthy`, `docker diff` was empty, `Config.Volumes` null,
user `nginx`; gzip-decoded wasm equals the zip's.

### KVM acceptance (2026-10-17, VM 2, HEAD fbf4e9e, dashboard embedded build)

Run on VM 2 (Debian 13, Docker, internal CA from S280). Binary sha256
`a7a06123...` equals a root build of the same tree (with `embeddash`) on the
guest. A throwaway minisign key (`minisign -G -W`, signed with `-l`, the
non-prehashed form the daemon requires) was trusted with `craft trust add`
and signed the eight-app manifest at counter 7 (install; same bytes as the
committed manifest apart from the counter) and counter 8 (update, versions
bumped to vectorcraft 0.12.1, photocraft 0.9.1, lightcraft 0.8.1, figcraft
0.6.1, plus matching throwaway edits of the installed catalog). Artifacts
were downloaded by the daemon over HTTPS and matched the recorded SHA-256.
Nothing was committed. **All steps passed for all four apps; no product bug
found.** An earlier attempt was killed by a rate limit before changing
anything the daemon kept (no key, manifest, app or image was left), so the
run started from the S250 end state.

- **Build and install:** `craft plan` verified (counter 7) and `craft build
  --follow` built each image (`sha256:86362484a3f8...` vectorcraft,
  `93f9c69875c3...` photocraft, `a03024d960b3...` lightcraft,
  `38609e258554...` figcraft). `apps install` through the CLI passed
  `verify-craft-images`, `write-stack`, `up`; routes `<app>.lan` (local, TLS
  internal) set with one `edge routes set`.
- **Image and container:** `Config.Volumes` null, user `nginx`, healthcheck
  on `/healthz`, containers `healthy`; `docker diff` empty; `/tmp` the only
  mount (tmpfs).
- **HTTPS and headers:** entry page and wasm fetched over `https://<app>.lan/`
  with the S280 root (no `-k`); `scripts/craft-header-check.sh` PASS for all
  four (`--unhashed` for lightcraft), gzip through Caddy included: wasm gzip
  versus identity bytes, vectorcraft 22,507,398 versus 64,737,882, photocraft
  11,417,725 versus 25,033,027, lightcraft 10,458,639 versus 27,259,177,
  figcraft 8,802,810 versus 22,507,647; decoded and identity bodies equal.
  LightCraft's `worker.js`, `lightcraft_web.js` and the 16.4 MB
  `lightcraft_zh_hans.otf` (served gzip, 13,588,233 bytes,
  `application/octet-stream`) all answer 200 with `no-cache`. Values are in
  `docs/architecture/CRAFT_HEADER_TABLE.md`.
- **Drift:** the scheduled scan (interval set to 5 minutes for the run,
  restored to 24 h) was clean with 8 packages after install and again after
  the update, with no `drift accept`; `drift review` clean for each app.
- **Stateless:** `backup schedule --package <app> --interval 1 --actor alice`
  answered 422 naming the app; `backup schedules` and `scheduler jobs` list
  no craft job.
- **Remove and reinstall:** `apps uninstall` removed the container and
  compose file (the empty stack directory remains, as for every package);
  clearing the route table removed the routes. The leaves showed `orphan`
  and came back `ok` on reinstall (same expiry, no re-issue).
- **Update:** `updates list` showed `homestead/craft-<app>:<old> -> <new>
  (manifest)`; `updates apply --craft-manifest ... --craft-signature ...`
  verified (counter 8), built, recorded, rewrote, started and pruned the old
  tags for all four; header check PASS again. The rebuilt images are
  byte-identical (same image IDs), so the build is reproducible.
- **Cleanup:** apps uninstalled, route table emptied, Caddy container and the
  four 0.x.1 images removed, trust key removed, secret key shredded, `/srv`
  work directory deleted, drift interval restored to 24 h, daemon redeployed
  with `install-systemd.sh`. Left on VM 2: `craft_baseline` at counter 8
  (vectorcraft, photocraft, lightcraft, figcraft), 6 (gridcraft, pdfcraft,
  deckcraft) and 4 (wordcraft), craft verdict and image rows, orphan `.lan`
  leaves until the `tls:local-leaves` sweep, the empty stack directories,
  the `homestead-edge` network and Caddy stack files, and
  `/home/tester/hs-s251`.

## Media craft packages: FilmCraft, EffectCraft, SoundCraft (S252, ADR-028)

Built: three more packages on the S249 shared shape
(`docs/architecture/STATIC_CRAFT_PACKAGES.md`) plus the client-side browser
capability advisories the slice asks for. No change to `images/craft/` (the
build-context digest `sha256:1880843e...` is unchanged), so no COOP/COEP
mechanism was needed and none was added.

- `catalog/filmcraft.yaml` (0.8.0), `catalog/effectcraft.yaml` (0.10.0),
  `catalog/soundcraft.yaml` (0.7.0): each its own image
  `homestead/craft-<app>:<version>`, own host, one `ephemeral` `/tmp`, no
  storage class, `sso.class: unsupported`, local-only with
  `allowwider: false`, stateless. Descriptions name the function, state the
  media-size caveat (large media lives in the browser until S254) and say
  Homestead lists the browser needs as advisories; none uses "clean-room".
- Vendored releases (latest on 2026-10-17, matching planning; each SHA-256
  equals upstream `SHA256SUMS.txt` and an independent hash): filmcraft v0.8.0
  `6b13956f...` (13,326,588 bytes), effectcraft v0.10.0 `b922abed...`
  (19,031,497), soundcraft v0.7.0 `b735de61...` (5,939,077). Licence for all
  three is `MIT OR Apache-2.0`, judged compatible; digests, licence hashes and
  verbatim NOTICE captures are in `docs/THIRD_PARTY.md`. FilmCraft and
  EffectCraft ship no NOTICE or OFL texts in the web zip (deferred).
- `catalog/craft/craft-manifest.yaml`: eleven apps, **counter 4** (one above
  S251), one counter for the whole set, still **unsigned**. The three new
  entries came from `scripts/craft-manifest.sh --counter 4` and were
  appended to the existing eight; the context digest is unchanged. No key
  was generated or committed.
- Browser advisories (the only Go in the slice, an optional additive
  manifest field, no new route, command or page): `Manifest.Browser` is a
  list of `capability` and `reason` (closed capability set, one reason of up
  to 300 characters each, validated). `PlanInstall` copies it to
  `InstallPlan.BrowserAdvisories` and adds a note that the appliance cannot
  see the browser; planning still succeeds and nothing in the daemon
  refuses install, update or start on browser grounds. The existing catalog
  detail (`Browser`) and plan (`BrowserAdvisories`) responses carry it, so
  the CLI and dashboard read the same fields (ADR-004). CLI: `apps plan`
  prints `browser: <capability> (advisory) - <reason>` lines. Dashboard: a
  shared `app-browser-advisories` component (S248 `StateMessage` for the
  caveat) on the package page and the reviewed plan. `docs/PARITY.md` notes
  updated; the parity test needed no row change. Tests:
  `internal/catalog/browser_test.go`, `apps_test.go`,
  `browser-advisories.spec.ts`, `catalog.spec.ts`.
- Header table: three rows and notes. SoundCraft is the hashed layout.
  FilmCraft (unhashed names loaded with `?v=`) and EffectCraft (unhashed
  names, workers, service worker, manifest) are served correctly by the
  shared rules (`no-cache`); both are checked with
  `craft-header-check.sh --unhashed`. EffectCraft's `.webmanifest` is served
  as `application/octet-stream` (deferred).
- Cross-origin isolation: none of the three needs `SharedArrayBuffer`,
  COOP or COEP (upstream `HOSTING.md`, `docs/web.md`, the shipped JS). The
  S254 same-origin proxy design is unaffected.
- Test rows: `curatedPackages`, `craftPackages`, `TestCuratedSSO` and three
  `knownImageVolumes` rows commented PENDING KVM verification.

### Verified locally (dev workstation, 2026-10-17)

Each release was downloaded into a fresh empty directory, hashed, unzipped
and inspected without running anything from it; the shared Dockerfile was
built with the real artifact and run with a read-only root, tmpfs `/tmp`,
all capabilities dropped. `scripts/craft-header-check.sh` passed for all
three: healthz 200, entry `no-cache`, `application/wasm`, gzip (also with
`Via: 1.1 Caddy`), no COOP/COEP; the containers reported `healthy`,
`docker diff` was empty, `Config.Volumes` null, user `nginx`; gzip-decoded
wasm equals the zip's. Gates: `make lint`, `go test ./...` (40 packages ok),
`python3 -m unittest discover -s tests` (601 ok), dashboard `ng test` (280
passed) and `ng build`. `TestRealCraftBuild` was not re-run (no
`images/craft` change).

### KVM acceptance (2026-10-17, VM 2, HEAD 42c413f, dashboard embedded build)

Run on VM 2 (Debian 13, Docker, internal CA from S280). Binary sha256
`c7cc58de...` equals a root build of the same tree (with `embeddash`) on the
guest. A throwaway minisign key (`minisign -G -W`, signed with `-l`) was
trusted with `craft trust add` and signed the eleven-app manifest at counter 9
(install; same bytes as the committed manifest apart from the counter) and
counter 10 (update, versions bumped to filmcraft 0.8.1, effectcraft 0.10.1,
soundcraft 0.7.1 with the same artifacts, plus matching throwaway edits of the
installed catalog). Artifacts were downloaded by the daemon over HTTPS and
matched the recorded SHA-256. Nothing was committed. **All lifecycle steps
passed for all three apps; two pre-existing dashboard bugs (below) block the
real-data plan panel and the dashboard install form.**

- **Build and install:** `craft plan` verified (counter 9); `craft build
  --follow` built `sha256:14bc52c4fb5a...` filmcraft, `a44e76b0221c...`
  effectcraft, `d161c915d241...` soundcraft; `apps install` through the CLI
  passed `verify-craft-images`, `write-stack`, `up`; routes `<app>.lan`
  (local, TLS internal) set with one `edge routes set`.
- **Image and container:** `Config.Volumes` null, user `nginx`, healthcheck
  on `/healthz`, containers `healthy`; `docker diff` empty; `/tmp` the only
  mount (tmpfs). The three `knownImageVolumes` comments are finalised.
- **HTTPS and headers:** entry page and wasm fetched over `https://<app>.lan/`
  with the S280 root (no `-k`); `scripts/craft-header-check.sh` PASS for all
  three (`--unhashed` for filmcraft and effectcraft), gzip through Caddy
  included: wasm gzip versus identity bytes, filmcraft 13,257,836 versus
  41,183,059, effectcraft 18,652,595 versus 62,481,427, soundcraft 5,889,741
  versus 15,463,607; decoded and identity bodies equal. EffectCraft `sw.js`,
  `worker.js` and `audio-worklet.js` 200 `application/javascript`;
  `manifest.webmanifest` 200 `application/octet-stream` (deferred), all
  `no-cache`. Values are in `docs/architecture/CRAFT_HEADER_TABLE.md`.
- **Service worker (Chromium via Playwright, ssh tunnel to the edge):** the
  leaf was pinned by SPKI (the profile has no NSS tool to import the root;
  the chain was verified against the S280 root with `openssl s_client
  -CAfile`). `https://effectcraft.lan/` loaded in a secure context, and
  `sw.js` registered and reached `activated`. `crossOriginIsolated` was
  false, as designed. Headless Chromium used SwiftShader, so GPU speed was not
  assessed.
- **Advisories:** `homesteadctl apps plan <app>` prints `browser: <label>
  [<capability>] (advisory) - <reason>` lines (6, 6 and 4) plus the "appliance
  cannot see your browser" note; the catalog detail API returns `Browser`
  with `Label`; the dashboard package page renders the panel with the
  caveat for each app. The reviewed-plan panel renders the same lines only
  when the plan response is normalised (bug 1).
- **Drift:** the scheduled scan (interval set to 5 minutes for the run,
  restored to 24 h) was clean with 7 packages after install and again after
  the update, with no `drift accept`; `drift review` clean for each app.
- **Stateless:** `backup schedule --package <app> --interval 1 --actor alice`
  answered 422 naming the app; `backup schedules` and `scheduler jobs` list
  no craft job.
- **Remove and reinstall:** `apps uninstall` removed the container and
  compose file (the empty stack directory remains, as for every package);
  clearing the route table removed the routes. The leaves showed `orphan`
  and came back with the same serial on reinstall (no re-issue).
- **Update:** `updates list` showed `homestead/craft-<app>:<old> -> <new>
  (manifest)`; `updates apply --craft-manifest ... --craft-signature ...`
  verified (counter 10), built, recorded, rewrote, started and pruned the old
  tags for all three; header check PASS again; the rebuilt images are
  byte-identical (same image IDs).
- **Cleanup:** apps uninstalled, route table emptied, Caddy container and the
  0.x.1 images removed, trust key removed, secret key shredded, `/srv`
  work directory deleted, drift interval restored to 24 h, daemon redeployed
  with `install-systemd.sh`. Left on VM 2: `craft_baseline` at counter 10
  (filmcraft, effectcraft, soundcraft), 8 (vectorcraft, photocraft,
  lightcraft, figcraft), 6 (gridcraft, pdfcraft, deckcraft) and 4
  (wordcraft), orphan `.lan` leaves until the `tls:local-leaves` sweep, the
  empty stack directories, the `homestead-edge` network and Caddy stack
  files, and `/home/tester/hs-s252`.

Bugs found (pre-existing, not S252 code; product code untouched, recorded in
`docs/DEFERRED.md`):

1. **Dashboard reviewed plan does not render for local stateless packages.**
   `POST /api/v1/catalog/<pkg>/plan` returns `DNS: null` and `Directories:
   null` for these packages; the template reads `reviewed.DNS.length` and
   `reviewed.Directories.length`, so rendering stops after "Routes" and the
   data placement, browser advisories and notes never appear. Repro: log in,
   Catalog, select FilmCraft, domain `lan`, Review plan. The same happens for
   WordCraft (the CLI and API are fine). The component spec used fixtures
   with empty arrays, so it did not catch it.
2. **Dashboard install form sends the wrong container-port key.** The catalog
   detail API returns `Services[].Name` (capitalised) but the dashboard type
   and template read `service.name`, so the field is `port-undefined` and the
   request carries `container_ports: {"undefined": 8080}`; the plan answers
   422 `no container port for service "filmcraft"`. Repro: select any
   package, enter a port, Review plan. Verified for all three apps; by the same code it also
   affects every earlier craft package.

**Re-verification after the dashboard fix (commit 4e3d3ff, VM 2):** the
dashboard bundle was built locally and embedded; its file-hash digest
matched on the guest, then `scripts/install-systemd.sh` redeployed. Both
bugs are fixed, and a third of the same kind surfaced and was fixed in the
same commit: the catalog detail sends `Storage` and `Requirements.Ports` as
`null` for stateless crafts, which threw in `select()` and left the port
field blank (the API service now normalises them). Via Chromium and Playwright
over an ssh tunnel, logged in as alice: Catalog, SoundCraft, domain `lan`,
Review plan rendered the whole panel (DNS 0, Directories 0, data placement,
all four browser advisories, the build note) with `container_ports
{"soundcraft": 8080}`; WordCraft likewise; Jellyfin's form keyed
`port-jellyfin` with 8096 (review only). The dashboard Install of SoundCraft
(throwaway minisign key, counter 11, `craft trust add`, `craft build`)
started operation 544 and the container became healthy. Non-craft
packages were affected by the key bug (every install form). Cleaned up:
app uninstalled, image, trust key and `/srv` directory removed, key shredded.
No Caddy container was started and the route table stayed empty.

## Design, CAD and launcher craft packages (S253, ADR-028)

> **Owner decision 2026-10-17 (after the KVM validation below): Craft Launcher
> removed from the catalog.** It has a compiled-in upstream suite list with
> GitHub download buttons, cannot list installed apps and calls
> `api.github.com` 14 times per load. `catalog/craft-launcher.yaml` and its
> manifest entry are gone (manifest now thirteen apps, **counter 6**, still
> unsigned, context digest unchanged), with the test lists, header-table row
> and THIRD_PARTY entry; a Homestead-owned "Craft apps" start page (S281,
> ADR-031 proposed) will replace it. The text below is the historical record
> of what S253 built and validated. A KVM appliance whose craft baseline is
> already at counter 13 from throwaway manifests would refuse a counter 6
> manifest; use a throwaway manifest at a higher counter there.

Built: three more packages on the S249 shared shape
(`docs/architecture/STATIC_CRAFT_PACKAGES.md`). No Go product code, no change
to `images/craft/` (build-context digest `sha256:1880843e...` unchanged),
no new route, command or dashboard surface (`docs/PARITY.md` needs no row
change). The `browser` advisories field from S252 is used as is.

- `catalog/designcraft.yaml` (0.8.0), `catalog/cadcraft.yaml` (0.7.0),
  `catalog/craft-launcher.yaml` (0.4.1): each its own image, own host, one
  `ephemeral` `/tmp`, no storage class, `sso.class: unsupported`, local-only
  with `allowwider: false`, stateless; none uses "clean-room".
- Vendored releases (latest on 2026-10-17, matching planning; each SHA-256
  equals upstream `SHA256SUMS.txt` and an independent hash): designcraft
  v0.8.0 `f1ebe741...` (15,306,498 bytes), cadcraft v0.7.0 `47df1708...`
  (8,492,921), craft-launcher v0.4.1 `5768d88f...` (4,250,028, asset
  `artcraft-launcher-web-0.4.1.zip`). All three are `MIT OR Apache-2.0`,
  judged compatible. CADCraft discloses one MPL-2.0 dependency (`acadrust`,
  unmodified, file-level copyleft) and the launcher carries ArtCraft
  trademarks outside the licence; both are analysed in `docs/THIRD_PARTY.md`
  together with the NOTICE records.
- Naming: the launcher app is `craft-launcher` (the repository) and its
  asset is `artcraft-launcher-web-0.4.1.zip`. The craft manifest records the
  asset in `artifact.name`; the image tag is
  `homestead/craft-craft-launcher:0.4.1` and the host `craft-launcher.lan`.
  `ParseRef` and the app-name pattern already accept it; `TestParseRef` now
  pins the round trip and `TestCraftPackages` covers the shipped manifest.
- `catalog/craft/craft-manifest.yaml`: fourteen apps, **counter 5**, still
  **unsigned**. The three new entries came from `scripts/craft-manifest.sh
  --counter 5` and were appended to the existing eleven; the context digest
  is unchanged. No key was generated or committed.
- **Launcher discovery (finding):** the launcher has a compiled-in suite
  list, no runtime configuration and only a `?lang=` query parameter; it
  lists the upstream suite with GitHub release downloads and cannot link to
  or list installed Homestead apps. Nothing is generated or mounted, so the
  signed context stays clean and no architecture change or ADR is needed to
  ship it. The wiring criterion cannot be met; the gap, manual linking and
  options (including a Homestead-owned start page, recommended) are in
  `docs/DEFERRED.md`. "Installable without any other craft app" holds
  trivially; "lists only what is installed" does not apply (it lists the
  whole upstream suite).
- Header table: three rows and notes; all hashed layout, none needs
  COOP/COEP. Test rows: `curatedPackages`, `craftPackages`, `TestCuratedSSO`,
  `TestCraftPackagesCarryBrowserAdvisories` (three new rows) and three
  `knownImageVolumes` rows commented PENDING KVM verification.

### Verified locally (dev workstation, 2026-10-17)

Each release was downloaded into a fresh empty directory, hashed, unzipped
and inspected without running anything from it; the shared Dockerfile was
built with the real artifact and run with a read-only root, tmpfs `/tmp`,
all capabilities dropped, `no-new-privileges`. `scripts/craft-header-check.sh`
passed for all three: healthz 200, entry `no-cache`, `application/wasm`,
gzip (also with `Via: 1.1 Caddy`), no COOP/COEP; containers `healthy`,
`docker diff` empty, `Config.Volumes` null, user `nginx`; gzip-decoded wasm
equals the zip's.

### KVM acceptance (2026-10-17, VM 2, HEAD e9a5d10, dashboard embedded build)

Run on VM 2 (Debian 13, Docker, internal CA from S280). The dashboard bundle
was built locally (node 24), its file-hash digest matched on the guest, and
`scripts/install-systemd.sh` deployed the tree. A throwaway minisign key
(`minisign -G -W`, signed with `-l`, kept on the guest only) was trusted with
`craft trust add` and signed the fourteen-app manifest at counter 12
(install; the committed manifest apart from the counter) and counter 13
(update: designcraft 0.8.1, cadcraft 0.7.1, craft-launcher 0.4.2, same
artifacts, plus matching throwaway edits of the installed catalog). Artifacts
were downloaded by the daemon over HTTPS and matched the recorded SHA-256.
Nothing was committed. **All lifecycle steps passed for all three apps; no
new product bug found.**

- **Build and install:** `craft plan` verified (counter 12); `craft build
  --follow` built designcraft `sha256:25732064628610...`, cadcraft
  `28e681d6d50c...`, craft-craft-launcher `848b59433394...`. The launcher was
  installed first with no other craft app installed (CLI: passed
  `verify-craft-images`, `write-stack`, `up`), then designcraft (CLI), then
  cadcraft through the dashboard (below). Routes `<app>.lan` (local, TLS
  internal) set with one `edge routes set`.
- **Dashboard install (Chromium via Playwright, ssh tunnel to the daemon):**
  cadcraft on a fresh app. The form opened with domain `lan`, port field
  `port-cadcraft` = 8080 and network `homestead-edge` (external) prefilled;
  Review plan returned 200 and rendered the panel with the browser
  advisories; Install returned operation 553 and the container became
  `healthy`. The request carried `container_ports {"cadcraft": 8080}`, so
  the S252 form fixes hold on a fresh craft app.
- **Image and container:** `Config.Volumes` null, user `nginx`, healthcheck
  on `/healthz`, containers `healthy`; `docker diff` empty; `/tmp` the only
  mount (tmpfs). The three `knownImageVolumes` comments are finalised.
- **HTTPS and headers:** entry page and wasm fetched over `https://<app>.lan/`
  with the S280 root (no `-k`); `scripts/craft-header-check.sh` PASS for all
  three (hashed names, so no `--unhashed`), gzip through Caddy included:
  designcraft wasm 15,266,666 bytes gzip versus 39,550,250 identity, cadcraft
  8,449,110 versus 22,546,717, launcher 4,204,488 versus 9,560,083. Decoded
  and identity bodies are equal and equal the wasm in the upstream zip.
  Values are in `docs/architecture/CRAFT_HEADER_TABLE.md`. Chromium loaded
  designcraft and cadcraft in a secure context; the only console error is the
  implicit `/favicon.ico` 404 (the zips ship none).
- **Drift:** the scheduled scan (interval set to the 5 minute minimum for the
  run, restored to 24 h) was clean with 7 packages after install and again
  after the update, with no `drift accept`; `drift review` clean for each app.
- **Stateless:** `backup schedule --package <app> --interval 1 --actor alice`
  answered 422 "stateless package, nothing to back up" for each; `scheduler
  jobs` lists no craft job.
- **Remove and reinstall:** `apps uninstall` removed the container and
  compose file (the empty stack directory remains, as for every package);
  clearing the route table removed the routes. The three leaves showed
  `orphan` and came back `ok` with the same expiry on reinstall (not
  re-issued; the sweep removes orphans, as in S252). Header check PASS again.
- **Update:** `updates list` showed `<old> -> <new> (manifest)`; `updates apply
  --craft-manifest ... --craft-signature ...` verified (counter 13), built,
  recorded, rewrote, started and pruned the old tags for all three; header
  check PASS again; the rebuilt images are byte-identical (same image IDs).
- **Craft Launcher in Chromium** (secure context; headless Chromium on
  SwiftShader, which needed `--use-angle=swiftshader` to paint the egui
  canvas): the page titled "ArtCraft Launcher" renders an ArtCraft
  header, "ALL 13" (the other suite apps; it omits itself) with grid and
  table views and a card per app showing the latest version and date and a
  "DOWNLOAD FOR LINUX" button; the footer reads "CHECKED JUST NOW - USING
  GITHUB". It lists the upstream suite, not the installed apps, as the
  discovery finding said. External hosts contacted by the browser at load:
  `api.github.com` only (14 `releases/latest` requests, one per suite app
  including its own, all 200); `raw.githubusercontent.com` was not contacted.
  With all non-`.lan` requests aborted (an offline browser) the page still
  renders and every card shows "NOT AVAILABLE HERE" with a disabled
  "NOT AVAILABLE" button; the 14 failed calls logged `ERR_INTERNET_DISCONNECTED`
  console errors and nothing else broke. Other console output: egui
  software-rasteriser and SetTheme warnings. The 14 API calls per load
  against the 60 per hour unauthenticated limit allow about four loads an
  hour per public IP (noted in `docs/DEFERRED.md`).
- **Cleanup:** apps uninstalled, route table emptied, Caddy container and
  the 0.x.1 images removed, trust key removed, secret key shredded, `/srv`
  work directory deleted, drift interval restored to 24 h, daemon redeployed
  with `install-systemd.sh` (catalog restored). Left on VM 2:
  `craft_baseline` at counter 13 for the three apps (counter 11 for
  soundcraft, the previous maximum), orphan `.lan` leaves until the
  `tls:local-leaves` sweep, the empty stack directories, the `homestead-edge`
  network and Caddy stack files.

## S254: Nextcloud craft file proxy

Governed by ADR-029 and ADR-032 (owner decision 2026-10-17: the edge reaches
the daemon over a dedicated unix socket). Guide:
`docs/architecture/CRAFT_FILE_PROXY.md`.

- `internal/craftfiles`: the proxy (fixed WebDAV subset, one root folder,
  traversal refusal, streamed bodies, limits, audit without content), the
  one-time launch codes and craft sessions bound to the operator session
  (re-verified on every request), the link manager (plan and approve, sealed
  app password `sys_craftfiles_nextcloud`, `craft-files.yaml` mirror) and
  the same-origin guard. Migration 42 adds `craft_file_link` and
  `craft_file_local_only`.
- Server: `GET /api/v1/craft/files`, `POST .../plan|approve|local-only|launch`
  (session class, parity rows in `docs/PARITY.md`); the craft socket mux is
  classified `craft-session` in `boundary.go` and enumerated by the
  classification test (TCP 114 routes, craft socket 11). `--craft-socket`,
  `RunCraftSocket`, doctor check `edge-craft-socket`. Settings
  `craft_files_max_upload_mb` (512), `craft_files_timeout_minutes` (30) and
  `craft_files_per_session` (8) join the config schema.
- Edge: `docker.Route.CraftApp`; craft routes render
  `handle /_homestead/files/*` to the socket ahead of the nginx upstream,
  inside the exposure policy; the Caddy compose file mounts
  `/run/homestead-craft` read-only; reconcile verifies the mount on the
  running container. Unit: `RuntimeDirectory=homestead homestead-craft`,
  `RuntimeDirectoryPreserve=yes`; `install-systemd.sh` creates the
  `homestead-craft` group.
- Image: wrapper at `images/craft/homestead/` (`/_homestead/open.html`),
  nginx `location ^~ /_homestead/` with a strict CSP, `/_homestead/files/`
  404. The craft manifest is prepared UNSIGNED at counter 7 with the new
  `context_digest` (`sha256:f3873357...`); the owner signs it.
- Upstream hooks verified from source at the pinned tags (nothing executed):
  GridCraft v0.7.0 `host.rs` and CADCraft v0.7.0 `host.rs`: app posts
  `<app>:ready {version}`, `:dirty {dirty}`, `:save {name, bytes}` (a
  transferred `ArrayBuffer`), `:failed {error}`; parent posts `:open {name,
  bytes}` with `bytes` an `ArrayBuffer` or `Uint8Array`; only the same-origin
  parent is heard or answered. PdfCraft v0.8.0 `main.rs` (`mod host`): the
  same, with `pdfcraft:save {name, saveAs, bytes}`, plus `?file=<url>`
  (unused). FilmCraft v0.8.0 `api.rs`: `window.filmcraft.importUrl(url,
  name?)` and `openProject(fileOrUrl)`. EffectCraft v0.10.0 `web/index.html`:
  `window.effectcraft.addFile(fileOrUrl, name?)` (the exported wasm
  `addFile(name, Uint8Array)` sits behind it).
- Tests: `internal/craftfiles` (fake WebDAV server: all methods, traversal,
  oversize declared and chunked, 256 MiB streamed each way with heap bound,
  upstream errors, credential never to the client, session exchange valid,
  reused, expired, wrong host or app, revoked, store failure with an injected
  clock, concurrency cap, CORS and Host/Origin refusal, audit and error
  bodies free of content, password, cookie and code, link/rotate/unlink,
  launch), `internal/server` (end to end, CSRF on launch, revocation by real
  logout and real password change, TCP listener never serves craft paths,
  the real socket: permissions, only craft paths, restart over a stale
  socket, limits through the config API, unlink, opt-out, doctor drift),
  `internal/docker` (render golden, policy nesting, compose mount,
  reconcile failing without the mount), `tests/test_s254_craft_wrapper.py`,
  dashboard spec, CLI test, and under `HOMESTEAD_DOCKER_TESTS=1` the wrapper
  headers from a real image.
- Gates at commit: `make lint` and `lint-scripts` clean; `go test ./...`
  1813 passed, 0 failed, 7 skipped; `go test -race ./internal/craftfiles/`
  ok; `python3 -m unittest discover -s tests` 610 OK; dashboard `ng test`
  288 passed (37 files) and `ng build` ok;
  `HOMESTEAD_DOCKER_TESTS=1 go test ./internal/docker -run TestRealCraftBuild`
  ok.

### KVM acceptance (2026-10-17/18, VM 2, HEAD 257763e)

Run on VM 2 (Debian 13, Docker, Nextcloud 35.0.1 from S276, internal CA from
S280). Deployed with `scripts/install-systemd.sh` (new unit, dashboard
embedded build, file-hash digest matched on the guest), `edge deploy
--follow`, a throwaway minisign key (craft manifest at counter 14, trusted
and removed), GridCraft built and installed through the CLI, routes
`gridcraft.lan` and `nextcloud.lan` (local, TLS internal), a throwaway
Nextcloud account `s254test` and its app password. Chromium (Playwright's
build, headless, SwiftShader) reached the appliance through an ssh tunnel
with `--host-resolver-rules` and the leaf certificates pinned by SPKI
(`--ignore-certificate-errors-spki-list`; no NSS tool to import the root;
the daemon itself verified `https://nextcloud.lan` against the S280 root,
no skip-verify). Nothing was committed (keys shredded). **All steps passed;
no product bug found.**

- **Socket and edge: pass.** `/run/homestead-craft` is `0750 root:homestead-craft`,
  `files.sock` `0660 root:homestead-craft`. After `systemctl restart
  homestead-server` the socket inode changed and the **not-restarted** Caddy
  (started 40 s earlier) still reached it through the read-only directory
  mount: `https://gridcraft.lan/_homestead/files/f/...` answered 401 from the
  daemon before and after (an nginx answer would be 404). `doctor`
  `edge-craft-socket` ok (also "no craft routes recorded" when the table is
  empty). Over the socket, with the edge headers, `/api/v1/*` and `/` are 404;
  without them 400. The TCP listener answers the craft paths with the
  dashboard fallback page or 401, never the proxy.
- **Link: pass.** `http://nextcloud.lan` refused 422 without the opt-in.
  `https://nextcloud.lan` linked with an owner app password (plan, `--yes`),
  and again with `--create-app-password` (the daemon-made `homestead-craftfiles`
  token appeared in Nextcloud and was revoked by unlink; the owner-supplied
  one is not, as the plan says). The app password and login password were
  absent from `craft files status`, the API, `craft-files.yaml`, `config
  get`, the SQLite file and WAL, the journal, the audit list, `docker inspect`
  (Caddy, GridCraft, Nextcloud) and the daemon environment. The Craft files
  page showed the link and the per-app table.
- **Browser: pass.** Dashboard "Open with Nextcloud files" opened
  `https://gridcraft.lan/_homestead/open.html`; the fragment was removed, the
  session exchange returned 200 and the cookie was `homestead_craft`,
  HttpOnly, Secure, SameSite=Strict, path `/_homestead/files/`, host-only.
  The wrapper listed the folder (empty), GridCraft loaded at `?host=parent`,
  a save wrote `Book1.xlsx` (PUT 201, then the list refreshed) and the file
  was in Nextcloud under `Homestead/` (WebDAV with the app password and the
  data directory; a valid xlsx). A second browser context with its own login
  relaunched, listed `Book1.xlsx`, opened it (GET 200, cells present), edited
  and saved over it (`If-Match`, PUT 204; content confirmed in Nextcloud).
  Across every request no `Authorization` header, app password, cookie
  value in a URL or CORS header was seen; `localStorage`, `sessionStorage`
  and `document.cookie` were empty. No CSP violation; the only console noise
  is the implicit `/favicon.ico` 404 and the egui software-rasteriser
  warnings. (Typing fast into the egui canvas drops characters in headless
  Chromium; keystrokes need a delay. A test artefact, not a product issue.)
- **Negatives: pass.** No launch: GET, PROPFIND and PUT 401, a forged cookie
  401. A launch code used twice: second exchange 401. Cross-origin: `Origin:
  https://evil.example` (GET and PUT), `Sec-Fetch-Site: cross-site` and a PUT
  without `Origin` are 403; `OPTIONS` is 403 with no `Access-Control-*`
  header; encoded `..` is 400; `PATCH` 405; GETs carry `attachment`, a
  sandbox CSP and `nosniff`. Logout: the next craft request is 401. Password
  change: both the changer's and another session's craft cookies are 401
  (and the password was restored). Limits: 600 MiB `PUT` at the default 512
  is 413; with `craft_files_max_upload_mb` 1, 2 MB and 3 MB declared and a 3
  MB chunked body are 413 and nothing is stored. Revoking the app password in
  Nextcloud (`occ user:auth-tokens:delete`): once Nextcloud's own token cache
  expired (a few seconds to a minute; requests kept succeeding until then,
  as Nextcloud itself did) every request is 502 with `X-Homestead-Upstream:
  auth-rejected`.
- **Audit and unlink: pass.** `craftfiles.file` rows carry craft app, short
  session id, method, path, status and bytes (reads aggregated, `x2`/`x5`),
  refused requests under actor `unauthenticated`; no content, password,
  cookie or code anywhere in audit or the journal. Local-only on: launch 409;
  off again. Unlink through plan and `--yes` deleted `sys_craftfiles_nextcloud`
  and the row, set `craft-files.yaml` to `linked: false`, and the proxy then
  answers 401.
- **Observed, by design:** the limits are cached for up to 5 seconds, so a
  changed `craft_files_*` setting takes effect within 5 s (documented
  behaviour of `limitsCache`, not yet in the guide). Not exercised: PdfCraft,
  CADCraft, FilmCraft and EffectCraft round trips (only GridCraft was
  installed), the dashboard link form (the CLI linked; the page rendered the
  state and launched).
- **Left on VM 2:** the craft baseline for gridcraft at 0.7.0, counter 14
  (maximum now 14); Caddy deployed (empty route table); orphan `.lan` leaves
  until the sweep; limits `craft_files_*` explicitly at their defaults
  (512, 30, 8); Nextcloud as before (test account, files, trusted domain and
  temporary edge-network attachment removed); `~/hs-s254` source tree.

### S254 Nextcloud link re-verification (2026-10-18, VM 2)

The KVM run needed three hand edits to link `https://nextcloud.lan`.
Classification: (a) host DNS was a **product gap** (the daemon trusted a
resolver that does not know the appliance's own routes), fixed by dialling
linked hosts that are in the edge route table on `127.0.0.1` with the route
name kept for TLS; (b) trusted domains was a **product gap for fresh installs**
(the catalog never passed the route host), fixed with `NEXTCLOUD_TRUSTED_DOMAINS`
from the planned route host, and an **old-install artifact** for VM 2's
S276 Nextcloud, which already has its `config.php` and needs one `occ` line
(the link now says so); (c) the edge network was an **old-install artifact**
(S249 already attaches routed services; VM 2's Nextcloud predates it), now
reported by doctor `edge-upstream-networks` and by the link error.

Re-run at the fix commit (source tree sha256 `9e524b61...` identical on host
and guest, deployed with `scripts/install-systemd.sh`; the dashboard bundle
was unchanged). No `/etc/hosts` entry was added anywhere. Route
`nextcloud.lan` (local, TLS internal) set; with VM 2's old Nextcloud as it
was: doctor warned `edge-upstream-networks`; the link failed with the 502
message naming the edge network; after attaching the container to
`homestead-edge` (`--alias nextcloud`, the old-install remedy) it failed with
the 400 message naming trusted domains; after `occ config:system:set
trusted_domains 1 --value=nextcloud.lan` (the old-install remedy) `craft files
link --url https://nextcloud.lan --create-app-password` succeeded, resolving
the name with no hosts entry. GridCraft (throwaway key, counter 15) was built
and installed, its route set, a launch code exchanged through the edge
(200), then `PUT` 201, `PROPFIND` 207 listing the file and `GET` 200 through
`/_homestead/files/f/` with the internal CA root verified by curl.
Cleaned up: unlinked, gridcraft uninstalled, routes empty, key untrusted and
shredded, `/srv` directory removed, test account, trusted domain and the
edge attachment removed. Not exercised live: a fresh Nextcloud install
picking up `NEXTCLOUD_TRUSTED_DOMAINS` (covered by catalog tests; proving it
would need wiping VM 2's Nextcloud data).

### S254 review fixes

Security review of S254 fixed: https required for the Nextcloud link
(loopback or explicit `allow_insecure_http` opt-in, INSECURE plan warning);
in-flight requests re-verify their session every 30 s and are cancelled on
revocation; upstream non-2xx bodies are replaced by a fixed text/plain
message and all proxy responses are nosniff; account `.`/`..` refused; the
scheme comes from an edge-set `X-Forwarded-Proto` (`header_up`); audit gains
the MOVE/COPY destination, denied-request events and first-failure read
aggregation; unlink deletes the secret before the row; the wrapper validates
save names; `--craft-socket` is pinned to the mounted path, the socket has
idle and header limits plus per-request read deadlines, and a password
change revokes the changer's craft sessions. Craft manifest prepared unsigned
at counter 8 (`context_digest` `sha256:440467bc...`); the owner signs it.


## S281: Craft apps start page (ADR-031)

Built: a Homestead-owned start page replacing the dropped Craft Launcher
(option b). `GET /api/v1/craft/apps` (session class in `routeClasses`,
read-only), `homesteadctl craft apps`, and the dashboard Craft apps page
(`/craft-apps`, nav entry "Craft apps"). Data: catalog craft packages
(`CraftImages()` non-empty) that are installed (`catalog.Installed`), joined
with the recorded edge route table; the URL is `https://<host>/` (`http` only
when the route's TLS is off), and an installed app without a route is listed
with no URL and a reason. No external call, no third-party asset, nothing
stored or generated. Details in `docs/architecture/STATIC_CRAFT_PACKAGES.md`.
Tests: empty list, several apps, with and without a route, removed app, route
classification, a store-backed install/route/uninstall walk with the default
HTTP transport and DNS resolver forbidden, CLI output, dashboard component
spec. `docs/PARITY.md` has the row. No change to `catalog/craft/` or
`images/craft/`; the craft manifest signature is untouched.

KVM acceptance (2026-10-18, VM 2, HEAD cf079da): **pass, no S281 bug**.
Deployed with `scripts/install-systemd.sh` (source tree and dashboard bundle
hashes matched on the guest). Chromium (Playwright's build, headless) reached
the appliance through ssh tunnels with `--host-resolver-rules` and the leaf
certificates pinned by SPKI.
- **Signed manifest: pass.** The shipped manifest (counter 8) verified with the
  embedded HAI craft key `B03DB3F5AA66DB3E` with no throwaway key and no
  `craft trust add`: `craft plan` got as far as the counter check. VM 2's
  baseline (max 15 from throwaway runs) refused it as a rollback ("counter 8
  < recorded 15", with the `--allow-older` hint), the expected anti-rollback
  behaviour. `craft plan/build --allow-older` then verified and built
  soundcraft and gridcraft (audited override, recorded counter now 8).
  Cosmetic: `craft trust list` still prints `[PLACEHOLDER: verifies no real
  release]` beside the now-real embedded craft key.
- **CLI: pass.** With both apps installed and no route, `craft apps` listed
  both with `(no URL)` and the reason; with routes `soundcraft.lan` and
  `gridcraft.lan` (local, TLS internal; target host is the service name) it
  listed `https://...`; with only soundcraft routed gridcraft showed the
  reason again; none installed prints `no craft apps installed`. (A first
  route table whose target host was the container name `gridcraft-gridcraft`
  did not match: the join keys on `CraftApp` or the service name, as designed.)
- **Browser: pass.** Logged in as alice, `/craft-apps` listed both; each link
  (new tab) opened over HTTPS in a secure context and the app loaded (a canvas
  and the GridCraft ribbon). Request hosts for the whole session: the
  dashboard origin (11 requests), `https://soundcraft.lan` (4) and
  `https://gridcraft.lan` (4); nothing else, no failed request.
- **Uninstall: pass.** After `apps uninstall soundcraft` a reload listed only
  GridCraft; after gridcraft too the page showed the empty state, with no
  regeneration step.
- **Cleanup:** apps uninstalled, routes empty, craft images removed, no
  throwaway key used, temp Playwright directory removed. Left: Caddy running,
  orphan `.lan` leaves until the sweep, craft baseline for both apps at
  counter 8, `~/hs-s281` source tree.

## S282: Dashboard visual foundation

Built: tokens (palette, type, radius by hierarchy, elevation, motion) in
`dashboard/src/tokens.css` for light, dark and high-contrast; bundled
Atkinson Hyperlegible fonts; base, primitive and shell styles
(`base.css`, `primitives.css`, `shell.css`, `glyphs.css`); grouped sidebar
shell with a mobile drawer; `app-dialog` on the native `<dialog>` replacing
every inline `alertdialog` (edge, storage, backups, secrets); `ThemeService`
and the Account page theme choice; one `<h1>` per page; status badges; the
updated validation board. Design reference:
`docs/architecture/DASHBOARD_DESIGN.md`. No route, API or CLI change, so
`docs/PARITY.md` is unchanged.

Checks: `theme/tokens-contrast.spec.ts` computes WCAG contrast for every
text and control token pair in all three themes (and that the system-dark
block equals the dark theme). Specs cover the shell (nav groups, logged-out
filtering, drawer, collapse persistence, skip link), dialog focus behaviour,
theme persistence and the board. A local headless Chromium run against the
built bundle with stubbed API data found no horizontal overflow on 26 routes
at 375, 390, 768, 1024 and 1440 px; screenshots in `/tmp/hs-s282-local/`
(not committed). The initial bundle is 514 kB (was 484 kB); fonts add 73 kB
of assets fetched on demand. The initial budget warning moved to 560kB.

KVM visual review (VM 2, 2026-10-18): **done.** Screenshots in
`/tmp/hs-s282-kvm/` (not committed): every route at 1440x900 and 390x844 in
light and dark, status/backups/storage in high contrast, the logged-out login
and status pages, the mobile drawer, and focus rings in three themes. Run
with Playwright through an ssh tunnel as `owner`, read only; logged out at the
end. Horizontal overflow: none on 24 routes (including an operation detail)
at 375, 390, 768, 1024 and 1440 px. No external requests were made.

Found and fixed:
- Sidebar: all five groups open made a 29-row nav that scrolled at 900px and
  cut Certificates off in the drawer. Now only the current page's group is
  open; owner choices persist (`homestead.nav.groups`); rows are 40px on fine
  pointers, 44px on touch. Logged-in desktop nav went from about 1,400px of
  links to 5 headers plus the current group.
- Backups warning ("no owner-held recipient") rendered as a green success
  callout. Added a `warning` kind to `app-state-message` and moved the
  caution messages on backups, TLS and craft files to it. Messages and
  `.warning` callouts now use ink text with a coloured start bar and glyph
  instead of coloured text on a tint.
- Empty-state messages inside lists showed a stray bullet; audit badges
  touched the action text.
- Theme flash: Angular inlined critical CSS without the `data-theme` blocks,
  so a stored dark or high-contrast theme painted light first (4 of 5
  reloads). Fixed with `public/theme-init.js` (same origin, no CSP needed;
  the daemon sets no dashboard CSP) plus `inlineCritical: false`. Six
  reloads now paint dark from the first frame.
- The coloured top bar on the danger dialog is not present in the current
  build (verified light, dark and 390px); the danger dialog is a brick border
  only.

Left for S283/S284: the Tunnel "Tear down" button is a primary submit
(destructive styling belongs with the page restructure); Updates and Storage
are long unbroken lists (grouping, tables); the Status diagnostics list reads
as a wall of rows; the login heading sits tight to its form; Atkinson's
slashed zero shows in IPs and versions (font design, left as is).

## S283: Dashboard states, transitions and work orders

Built (all under `dashboard/src`, no daemon, API or CLI change, so
`docs/PARITY.md` is unchanged; the rail reads the existing
`GET /api/v1/operations` and `/operations/{id}/events`):
- `ui/loading.ts` (`app-loading`): delayed (150 ms), minimum-display (300 ms)
  content-shaped skeletons. Replaced every "Loading…", "Working…" and bare
  `…` on 24 pages (Config, Firewall, Catalog, Updates, Secrets, Drift,
  Advanced, Data, Tunnel, Operations and detail, Edge, TLS, Trust, Account,
  Audit, Backups, Storage, Craft apps, Craft files, Status).
- `state/state-message`: revised with `detail`, one next action and a
  `notice` kind; `loading` kind removed. Empty states on operations, backups,
  craft apps, exposure and TLS now lead with an action.
- `core/http-error.ts` `isNotSetUp`: TLS issuance and exposure review treat a
  404 as "not set up yet" (an empty state with a link), not a red error.
- Page transitions: `withViewTransitions` with only `<main>` crossfading,
  skipped under reduced motion (`core/view-transition.ts`).
- Feedback: `core/toast.service.ts` and `ui/toast-region.ts` (polite
  aria-live region plus dismissible toasts).
- Work orders: `core/work-orders.service.ts`, `core/work-order.interceptor.ts`,
  `core/operation-labels.ts`, `core/operation-view.ts`, `ui/work-orders-rail`
  (sidebar slot, bottom sheet below md), `ui/step-list`,
  `ui/operation-progress`. Every operation started from the dashboard joins
  the rail, running operations are re-read on load and on navigation, finished
  orders settle after 8 seconds. Operation detail, Edge, TLS, Trust, Catalog,
  Updates (package, platform, craft), Data, Drift, Secrets, Exposure and
  Tunnel show the same step list; no "Operation N: running" text remains.
- Dialogs: enter animation and a short exit (inert copy left behind), both off
  under reduced motion.
- Scroll fix (commit 2b6c9a3): the router had no scroll handling, so the
  document kept its offset across route changes and a scrolled-down long page
  left the next one at its bottom. `withInMemoryScrolling` restores the
  position on back and forward and starts new pages at the top.
- New tokens `--rail-*` (contrast-checked in all themes).

Checks: dashboard 511 specs (skeleton timing under fake timers, reduced-motion
view-transition and dialog exit, rail add/update/finish/reload, toast and
aria-live output, interceptor, step list, labels). A local headless Chromium
run against the built bundle with stubbed API data and screenshots in
`/tmp/hs-s283-local/` (not committed) found and fixed one layout bug: the new
toast host became a grid cell and pushed `<main>` under the sidebar. Removed
the redundant "Caddy deployment started." notice, since the progress panel,
the rail and the toast already say it.

KVM visual review (VM 2, 2026-10-18): **done.** Screenshots in
`/tmp/hs-s283-kvm/` (not committed). Playwright through an ssh tunnel as
`owner`; logged out at the end; tunnel and settings untouched (public URL 200
throughout). App used: GridCraft 0.7.0 (signed craft manifest, counter 8): a
dashboard "Verify and build" (15 s the first time, 3 to 4 s cached) then a
dashboard install, then a CLI uninstall (the dashboard has no uninstall).
Passed: the rail followed the build across five sidebar navigations at
0:00 to 0:15 and settled 8 s after Done; a reload mid-build re-read it (live
text "started", step moved on); a CLI-started build joined the rail on the
next navigation; the sheet at 390px (summary bar, open list, content clears
it, no horizontal overflow); toast "Craft app built" and a polite
`role=status aria-live` region carrying the step text and the result; skeleton
absent at 0 and 80 ms API delay, on at 150 ms, held 300 ms (250 ms delay:
254 to 555 ms), 1.2 s delay on until data; reduced motion gives 1e-5 s
transitions and no dialog animation, normal motion fades the dialog in and out
(about 0.2 s) and returns focus; new pages land at scrollY 0.
Found and fixed (commit cfbe408):
- Back did not restore scroll (0 instead of 187 and 385): pages load data
  after the router restores, so the browser clamped to the top. A retry
  (`core/scroll-restore.ts`) now waits up to 2.5 s for the page to grow and
  yields to wheel, touch or keys. Back now gives 187 and 385/340.
- Rail and step list showed raw step slugs ("obtain-artifact"); now words.
- Operations list kept saying "Running" for a finished order; it refreshes
  when an order starts or settles.
- Orders found on load or started from the CLI got generic labels
  ("Catalog remove"); added labels for install, remove, configure, restore,
  migrate, reconcile, storage and Docker setup.
- Backups destination on a fresh appliance (422) was a red error with daemon
  text; now an empty state linking to Storage.
- Skeleton bars were nearly invisible on the page background (added a border);
  Escape now closes the mobile sheet.
Left (DEFERRED.md): a craft install without the edge network fails at `up` and
stays recorded as installed; no dashboard uninstall; the browser console still
logs the 404/422 probe requests. Cleanup on VM 2: uninstalled, craft image,
`craft_images`/`craft_baseline`/`craft_verdicts` rows, the test
`homestead-edge` network and stack directory removed; only audit and
operation history remain.

### Post-S283 fix: install on a factory-fresh appliance

Two KVM findings, one commit.
- Root cause 1: S249 made `PlanInstall` join every routed stack to the external
  `homestead-edge` network, but only the Caddy edge deploy created it, so a
  first install failed at compose `up` with "network homestead-edge not found".
  The install operation now runs an `ensure-homestead-edge` step first
  (`docker.EnsureEdgeNetwork`, the same code the edge deploy uses, idempotent).
  It creates the network only; it does not deploy Caddy. The plan sets
  `EdgeNetwork` and its note says the network is created and that routes will
  not serve until the proxy is set up on the Edge page.
- Root cause 2: `write-stack` records fingerprints and writes `compose.yaml`
  before `up`, and `Installed` counts either, so a failed `up` left the package
  installed and the retry answered 409. Install records no failed state; the
  failure now rolls back (`rollbackInstall`): compose `down` (never with
  volumes), then the generated files and fingerprint records are removed. The
  package reads as not installed and the route allows a retry. Kept on
  purpose: provisioned data directories and bind data, generated secrets (a
  partly started database may already have initialised with them), and the edge
  network (shared, harmless). If cleanup itself fails, the error says so and
  points at `apps uninstall`.

## S285: Catalog install wizard

Built (dashboard; one small daemon change, below):
- `catalog/install-wizard.ts` and `.html`, route `/catalog/:name/install`
  (loaded on demand, so the initial bundle stays at 559.6 kB under the 560 kB
  budget). The old inline install panel is gone; the Catalog page (`catalog.ts`)
  is now the list, with Install, Manage and Uninstall per app. Manage keeps the
  variables and secrets forms (configure, register, rotate, adopt) unchanged.
- Step model (`catalog/wizard-model.ts`): What it is, Requirements, Storage,
  Address, Secrets and sign-in, Review, Install, Done; a step that does not
  apply is left out. Stateless craft app: About, Requirements, Address, Review,
  Install, Done (6). Storage adds Storage (Nextcloud, Jellyfin, Immich: 8 with
  sign-in). Secrets and sign-in is shown when the manifest declares secrets or
  its sso class is not `unsupported` (Authentik: secrets, so shown, with its own
  "this is the sign-in service" paragraph). Design doc: Wizard pattern.
- Calls: `GET /catalog/{name}`, `GET /catalog`, `GET /docker/health`, storage
  registrations, mounts and assignments, `GET /craft`, `POST /catalog/{name}/plan`
  then `POST /catalog/{name}/install`, `POST /craft/build` for craft apps. The
  plan route is asked again on every entry to Requirements and Review and when
  Storage continues, so the reviewed plan is the applied one. Plan and install
  routes are unchanged.
- Requirements reads edge readiness from Docker health: the `homestead-edge`
  network ("created on install" since the post-S283 fix) and the
  `homestead-caddy` container. Caddy missing is a warning with a link to the
  Edge page that the owner acknowledges; Docker unreachable and a refused plan
  are hard stops with the daemon's words and a fix. One daemon change:
  `PlanInstall`'s "requirements unmet" error now names every failed check
  ("memory: host offers 1024 MB for 4096 required"), which Requirements shows.
- Craft verify-and-build: the Review button for a craft app whose image is not
  built (`GET /craft` has no present, matching image of that version) is
  "Verify and build GridCraft". It starts `POST /craft/build {app}` (the
  daemon verifies signature, counter, artifact digest and context again), the
  Install step follows it, and on success offers "Install GridCraft". A refused
  build shows the verdict's reason and points at the craft keys on the Updates
  page. Install is never offered before the image is built.
- Done: Open the app when its edge route exists, otherwise a notice with the
  host and upstream to add on the Edge page (install does not add the route);
  curated first-login hints (`catalog/curated-copy.ts`, 17 packages: nextcloud,
  jellyfin, immich, authentik and the 13 craft apps; a spec fails when a
  catalog manifest has none); where the data lives; the backup page; Uninstall.
- Uninstall (owner-approved scope addition, slice file Scope): `uninstall-app.ts`,
  a danger dialog on the Catalog list and the done page. It says what is removed
  and what is kept, lists every data root as `<mount>/<package>` from the
  storage assignments before "Also delete its data", and offers the
  abandon-volumes acknowledgement only after a 409 or 502. It uses
  `POST /catalog/{name}/uninstall`, joins the rail ("Uninstall GridCraft",
  "GridCraft uninstalled") and shows progress inline. PARITY: uninstall row now
  `uninstallPackage`; docker health row now `dockerHealth`.
- Also: `registerPackageTitles` so the rail says "Install GridCraft", not
  "Install Gridcraft"; step names in words ("Start the containers");
  `.danger.quiet` outlined destructive button; `scroll-padding-top` below md so
  the sticky top bar does not cover focused elements.

Checks: dashboard 581 specs (was 511): step sequencing per package shape,
blocked and acknowledged preflight, Docker down, storage selection and free
space, existing folder as a host path, back and next keeping entries, plan
rendering, install start with rail entry, done, failure and retry, resume after
leaving, craft build then install, build refused, uninstall (keep, delete with
roots, refused, unknown roots, stateless), curated-copy coverage of every
manifest. `ng build` clean, `make lint`, Python 611 tests, `go test ./...` all
green. Local headless Chromium against the built bundle with stubbed API data
(`/tmp/hs-s285-run/server.js` and `shots.js`, not committed) and screenshots in
`/tmp/hs-s285-local/` (87 PNGs: light, dark and 390 px for each wizard step of a
craft app, Nextcloud storage and sign-in, Authentik, done, failure, build, and
the uninstall dialog). Critique and one iteration: duplicate error text on a
failed install (message, progress panel and toast all said it) now says it once;
the secrets list ran name and purpose together, now sentences; storage errors
tied to their field; disclosures had no marker; the red filled Uninstall
dominated rows, now outlined; plan notes started lowercase; install step help
said "working" during a failure. Removed the "Generated Compose file"
disclosure, which duplicated the raw plan.

Address gap closed (bd7209a, dashboard only, existing edge routes API): after a
successful install the wizard merges the reviewed plan's routes into the
recorded table (same host replaced, every other route sent back untouched;
public routes never added) and reconciles. Requirements offers "Set up the
edge" (confirm dialog, then the Caddy deploy operation in the rail) or
acknowledge and carry on; with no proxy, Done says the app is not reachable
until the edge is set up and offers the same dialog. Done shows the address as
a link and, when this device cannot open it, a Trust link. Uninstall removes
the app's route. Initial bundle 555.7 kB (the Catalog page became lazy to make
room).

KVM review on VM 2 (2026-10-19, HEAD bd7209a then 8dbc557): **done.** Screenshots
in `/tmp/hs-s285-kvm/` (not committed). Playwright through ssh tunnels as
`owner`, `--host-resolver-rules` to guest 443 and the leaf pinned by SPKI; the
tunnel and public URL (200) untouched throughout.
- GridCraft on a fresh appliance (no Caddy, no image): Requirements showed the
  edge warning; Set up the edge deployed Caddy (rail "Deploy Caddy"); Verify and
  build, then install (about 1 s); the route was added by the wizard (rail
  "Reconcile routes"); `https://gridcraft.lan` loaded the spreadsheet over
  HTTPS. A browser without the pin showed the Trust note.
- Nextcloud with storage (registered `/mnt/app` and `/mnt/bulk` on the Storage
  page): installed; first-login hint shown; route added; `https://nextcloud.lan`
  answered with Nextcloud. Leaving 0.3 s after Install and returning: the rail
  tracked it, the Catalog row said "Setting up" with a Continue link, the wizard
  resumed the same operation and finished on Done with the route added.
- Uninstall: GridCraft keeping data, Nextcloud deleting data (dialog named
  `/mnt/app/nextcloud` and `/mnt/bulk/nextcloud`, both gone afterwards). Both
  routes removed, route table empty, only `homestead-caddy` left.
Found and fixed (8dbc557): (1) port 80 blocked Nextcloud once Caddy held 80
(`ports: [80]` is checked as a host port; Nextcloud now declares none). (2) A
mount chosen in the wizard failed the plan with "no space data" unless the class
was also assigned (the daemon read free space from assignments only; now also
from discovery for the chosen mount, with a Go test); the wizard also assigns an
unassigned class so uninstall can name data roots (before: "cannot tell which
folders", no delete option). (3) Duplicate mounts in the Storage step. (4)
Stale daemon notes ("does not deploy the proxy", "run craft build first").
(5) An install left in flight was unreachable from the Catalog (row said
Installed): it now shows "Setting up" with Continue. (6) An installed app with no
route (tab closed mid-install, CLI install) now offers "Give X an address".
Left (DEFERRED.md): Jellyfin/Immich/Authentik declare container ports as host
ports; no API to unassign a class or unregister a mount; empty stack directory
left by uninstall; Done after a later visit names data folders from assignments
only; the Storage page lists a mount twice; the daemon-side install and route
as one operation.
Final VM 2 state: Caddy deployed and running (kept, set up through the wizard)
and the `homestead-edge` network; no app containers or stacks; no routes;
storage registrations and class assignments removed (database rows deleted, as
there is no API); gridcraft and nextcloud images and `craft_images`/
`craft_verdicts` rows removed; `/mnt/app` and `/mnt/bulk` empty; tunnel running.

## S286: Dashboard sign-in with Authentik (ADR-033)
- `internal/sso` (ID token validation, Authentik admin client, manager),
  `internal/server/sso.go` routes, session `method` column (migration 43,
  schema v43), `stepUpRoutes` in `boundary.go`, `homesteadctl sso ...`, and the
  dashboard Sign-in page, Account link card, login button, SSO error codes and
  re-authentication dialog. Design and operations: `docs/architecture/DASHBOARD_SSO.md`.
- Tests: fake OIDC provider and fake Authentik admin API in `internal/sso` and
  `internal/server`; Vitest specs for login, Sign-in, link card, error codes
  and the re-auth dialog.
- KVM review on VM 2 (2026-10-19, HEAD e43d9b0, then 4ae6e12): **done.**
  Screenshots in `/tmp/hs-s286-kvm/` (not committed) with the driver scripts.
  Deployed with `scripts/install-systemd.sh` (binary sha256 checked against a
  root build of the same tree); Chromium (Playwright, `google-chrome`) on the
  host through `ssh -L` to the daemon (origin `http://127.0.0.1:8080`, a planned
  origin) and to Caddy 443 (`--host-resolver-rules` maps `authentik.lan`). No
  Cloudflare DNS, route or tunnel was touched (public URL 200 throughout).
  1. Install Authentik 2026.8.3 through `POST /catalog/authentik/plan` and
     `/install` (storage `/mnt/app`, host `authentik.lan`) then the edge route:
     PASS. 2. First-run setup through the flow executor, API token, two users
     (alice, bob): PASS. 3. `sso plan`, `approve`, `apply --follow`: PASS after
     fix 1; a re-plan with the token showed both objects "unchanged" and apply
     audited "provider already matches; application already matches" (a bare
     second `apply` answers 409 as the plan is spent; see DEFERRED). 4. Link
     alice through the Account link card and the re-auth dialog: PASS after fix
     2. 5. Login page button, Authentik sign-in, landed on `/status`; session
     cookie HttpOnly/Strict, CSRF cookie; a bad CSRF token 403: PASS. 6. Audit
     `sso.login ok method=oidc subject=...`: PASS. 7. `POST /sso/plan` in an
     oidc session: 403 `reauth-required`, wrong password 403 `reauth-invalid`,
     the dialog with the password then showed the plan: PASS. 8. bob refused:
     `/login?sso_error=unlinked`, audit `refused: unlinked`, no session cookie,
     one identity row (`owner`): PASS. 9. Authentik containers stopped: button
     still shown, click returns `?sso_error=unavailable` "Authentik did not
     answer. Use your local password.", local login works; restarted; then the
     issuer set to a path Authentik does not serve (the stored config and link
     edited in SQLite, restored after; the API cannot store a bad issuer because
     apply verifies it): the same message and local login works: PASS. (With
     only the config edited and not the link, the button disappears because the
     link no longer matches the issuer: by design.) 10. Five failed local logins
     (one was a wrong re-auth password: the counters are shared) locked the
     account (429); Authentik sign-in still worked, step-up in that session
     answered 429 `reauth-locked`, and the local login stayed 429 after the SSO
     success: PASS (the lock rows were deleted from `login_attempts` to continue).
     11. Unlink with the password file (CLI): the oidc session went from 200 to
     401; `sso disable`: status "not set up", `/sso/options` 404: PASS.
  Found and fixed (4ae6e12): (1) Authentik 2026.8 creates an OAuth2 provider
  with no grant types and then rejects every authorize request ("Invalid
  grant_type for provider", `invalid_request` at the callback, shown as
  `denied`). Apply now sets `grant_types` to `authorization_code`; plan reports
  "grant types" drift; tests cover create and repair. (2) `prompt=login` plus
  `max_age=0` looped at Authentik's login forever (`prompt` stays on the
  post-login redirect; `max_age=0` is ignored, `max_age=1` forces a prompt). The
  link now sends `max_age=30` and no `prompt`; ADR-033 amended, docs updated.
  Left (DEFERRED.md): `auth_time` not checked, bare second apply 409, the
  `keep_token` audit redaction, `homesteadctl login` prints the token.
  Final VM 2 state: Authentik installed and healthy (route `authentik.lan`,
  `akadmin` kept with a random password on the guest; test users, the API token,
  and the Homestead provider and application deleted in Authentik); SSO
  disabled, no link, owner password unchanged, no lockout; Caddy running; tunnel
  untouched. Not verified: absolute and idle session expiry of an oidc session
  (same code path as password sessions, covered by tests), a real browser
  reaching Authentik over the tunnel (deliberately not exposed), the
  `https://<edge host>` origin for the dashboard.

## S287: Post-claim setup walkthrough (ADR-034)
- `internal/setup` (state, provider copy, access options), `internal/server/setup.go`
  (`GET /api/v1/setup`, `PUT /api/v1/setup/steps/{name}`, session class), schema
  migration 44 (legacy stamp, schema v44), `homesteadctl setup ...`, and the
  dashboard Setup page (`/setup`, lazy), `setupGuard` and the Setup / Finish setup
  navigation entry. Design and operations: `docs/architecture/SETUP_WALKTHROUGH.md`.
- Tests: step derivation, resume, skip, review path, tunnel visibility, legacy
  origin and the secrets-absent check in `internal/setup` and `internal/server`;
  provider permission copy pinned to the adapters in
  `internal/setup/permissions_test.go`; store migration test; CLI output test;
  Vitest specs for the page, the guard, the exemptions and the legacy behaviour.
- KVM review on VM 2 (2026-10-19, HEAD ce943c3, then 64b9838): **done.**
  Screenshots (1440 and 390 px each) in `/tmp/hs-s287/shots/` (not committed),
  driver scripts beside them; headless Chrome through `ssh -L` to the daemon
  (origin `http://127.0.0.1:8080`) and Caddy (`authentik.lan` mapped to the
  forward). Deployed with `scripts/install-systemd.sh`; the binary sha256
  matched a guest rebuild of the same tree each time.
  Factory reset: tunnel `9ccedf12-...` torn down through the API (first
  attempt hit Cloudflare 1022, see fix 1; the retry deleted it, confirmed in
  Cloudflare; the tunnels `homestead` and `media-tunnel` untouched), daemon
  stopped, every stack `compose down -v`, `/opt/homestead/stacks`,
  `/mnt/app/authentik`, `/var/lib/homestead`, `/etc/homestead` and the
  tunnel unit removed, HEAD reinstalled, claimed, first admin `owner` with its
  existing password.
  1. Fresh: `setup status` origin fresh, gated, next account; `/status`,
     `/catalog`, `/audit` redirect to `/setup`; `/backups` stays open after
     fix 2; API and CLI ungated: PASS. 2. Step 1: export on Backups,
     acknowledge, still to do until "Confirm my password", then done and CLI
     agrees: PASS (after fix 2). 3. Step 2: Authentik installed through the
     wizard (edge set up in the wizard, storage registered on the Storage page
     after fix 3, defaults for host), akadmin first-run through the flow
     executor, `sso plan`, `approve`, `apply --follow`, `owner` linked through
     the Sign-in page and Authentik; step reads done: PASS. 4. Step 3: permission copy
     for all four providers matches the doc; the tunnel option is disabled with
     a reason for DigitalOcean, Route 53 and Manual; `setup step domain
     --acknowledge` answered 422 naming `/tunnel`, then `/exposure`; Cloudflare
     plus Cloudflare Tunnel, tunnel `homestead-vm2` set up from the Tunnel page
     (new id `941037b6-692b-49ee-bd18-c1159c93fe36`), DNS CNAME `homestead`
     repointed through the API (proxied), step read done by itself (a tunnel
     route counts), public URL 200 and the login page; the token and account ID
     are absent from the setup, wizard and credential responses, the database
     files (including the WAL), the journals and the audit log: PASS. 5. Step
     4: Jellyfin install started from the list, the rail showed "Install
     Jellyfin", the step read done at once and the guard released
     (`/status` opens): PASS. 6. CLI parity (on the fresh row): `skip account`
     read Skipped on reload, `unskip account` To do, `step account
     --acknowledge` Done: PASS. 7. Review path (row deleted): the domain step
     opened as a review, "This looks right" added one `setup.step` audit entry
     and changed nothing in `tunnel_routes`, the tunnel or DNS: PASS. 8. Legacy
     row restored: not gated, nav entry "Finish setup": PASS; real state
     restored by deleting the row and acknowledging account and domain.
  Found and fixed (64b9838): (1) tunnel teardown deleted the Cloudflare
  tunnel before stopping the connector (1022); it now stops first and restarts
  it if the delete fails. (2) Step 1 pointed to the Account page for the
  recovery export, which is on Backups, and `/backups` was gated. (3)
  `/storage`, `/edge`, `/trust` were gated, so a fresh appliance could not
  register storage from the install wizard. Left: see `docs/DEFERRED.md`.
  Not verified: the "I have finished here" and "I am finished with apps"
  buttons (state derived done first), skipping a pending domain or apps step in
  the dashboard, the Exposure (router) plan path, a throwaway DNS credential
  stored through the form (`GET` of sealed names not exposed by an API route),
  absolute session expiry.
  Final VM 2 state: freshly claimed, owner `owner` with its existing password,
  setup complete, Authentik installed (akadmin password in `~/ak.admin.pass`,
  owner's Authentik password in `~/ak.owner.pass`, 600, on the guest), SSO
  configured and linked to the Authentik user `owner`, Jellyfin installed,
  tunnel `941037b6-692b-49ee-bd18-c1159c93fe36` (`~/tunnel-id.txt`) serving
  https://homestead.hopefulaspirationsindustries.com.


## S284: Dashboard page passes

Presentation and copy only. No route, API or CLI change; `docs/PARITY.md` is
unchanged and the parity test stays green. Patterns are in
`docs/architecture/DASHBOARD_DESIGN.md` (Page patterns).

- Status: triage list, collapsed "All good", compact facts, copyable commands.
- Catalog and Craft apps: list rows with icon, badge and actions.
- Tables: Operations, Audit, Storage, Backups, Edge, Trust, Craft files,
  Tunnel routes, Data migration volumes.
- Risky actions: preview beside the button; new confirm dialogs for tunnel
  teardown and old-volume removal.
- Forms: Account, Tunnel and Exposure show inline field errors.
- Copy: shared unreachable message, "Settings" for variables, "certificate"
  for leaf, "Homestead" instead of "daemon" and "reconciler" in user text.
- Review fixes: rarely visited pages load on demand (initial bundle 588 kB to
  385.87 kB; the S283 baseline was 558.66 kB); jargon (manifest, package,
  raw states) replaced in page copy; Updates craft controls collapsed and
  Drift leads with problems; craft apps show the manifest blurb (API, CLI and
  dashboard); inline validation on the Storage register and Backups schedule
  forms; Account problems carry their field key; `.risky-preview` defined.
- Dashboard tests: 66 files, 682 tests.

KVM visual review (VM 2, headless Chrome, non-GET calls blocked or mocked):
every route screenshotted at 1440 and 390 px, light and dark, plus the
logged-out pages. Screenshots and driver scripts are in /tmp/hs-s284/ (not
committed). Results:
- Horizontal overflow: none at 375, 390, 768, 1024 and 1440 px on every route.
- Lazy loading: View Transitions crossfade between lazy and eager pages; skeletons
  appear without flash; the setup guard allows every route; the work-orders
  rail shows after a tracked POST; back-navigation restores the scroll position
  exactly on /updates (600 -> 600). No unexpected console errors.
- Fixed: raw ISO timestamps (now `when` pipe), "(s)" plurals (now `plural`),
  Status state casing, Exposure summary sentence case, Updates "Not installed"
  list collapsed, Drift list heading and chips, audit pager, summary chevrons
  shared globally, Tear down button styling, nowrap on dates and devices,
  scroll restore shifted by browser scroll anchoring (`overflow-anchor: none`),
  setup "1 app installed." copy.
- Risky actions show "What changes" previews; confirm dialogs open and cancel.
  Inline validation verified on Account, Tunnel, Storage register, Backups.
- Not verified: firewall apply and later steps, Trust rotation preview, data
  volume removal flow (all mutating).

## S288: Open bug fixes from the S285-S287 reviews

- Claim token, no log buildup. `internal/claim/tokenfile.go`: while unclaimed
  the daemon keeps exactly one live token, written to `--claim-token-file`
  (default `/run/homestead/claim-token`, inside the unit's `RuntimeDirectory`,
  so it is writable under `ProtectSystem=strict` and on tmpfs; mode 0600 via a
  temp file in the same directory plus rename). A restart reuses the file's
  token while it is still live; a lost file (reboot) or an expired token is
  replaced, and replacing drops every other unused row. The token is never
  printed: the journal gets one line per start naming `sudo homesteadctl
  claim token`. Re-minting uses a dedicated timer goroutine
  (`MaintainTokenFile`) that sleeps until the current token's expiry, rather
  than a scheduler job (scheduler jobs run through the audited operation runner
  and persist run state; the token needs an exact-expiry wake and no record).
  `Service.OnClaimed` removes the file the moment a claim succeeds and stops
  the loop. `homesteadctl claim token [--file PATH]` is local (no API): it
  reads the file, prints `token:` and `expires:`, says plainly when the file
  is root-only, expired, the appliance is claimed (asks the identity route), or
  the daemon is not running. Acceptance script reads the token that way.
- Uninstall removes the stack directory with `os.Remove` (only when empty
  after the stack files go; data folders keep it). A second uninstall answers
  404 "X is not installed" (also cleans a pre-S288 empty leftover).
- Storage: `DELETE /api/v1/storage/assignments/{class}` and `DELETE
  /api/v1/storage/registrations?mountpoint=`; 409 body names
  `affected_packages` (and `classes` for a mount still assigned); audited as
  `storage.unassign` / `storage.unregister`; the disk is never touched. CLI
  `storage unassign --class`, `storage unregister --mountpoint`. The Storage
  page dedupes mounts and has both actions behind the danger dialog.
- CLI: `tunnel teardown` and `tunnel setup` take `--account-id-file` and
  `--api-token-file` (`-` is stdin); the argv forms warn. `login` no longer
  prints the session token (`--print-token` does, for scripts).
- Dashboard: Account "Unlink Authentik" and Trust "Re-issue all" open the
  danger dialog first.
- Fresh install on VM 2 (HEAD 6480457). Reset as in S287: tunnel
  `941037b6-...` deleted in Cloudflare (stopped the connector first; direct
  Cloudflare API call, not the CLI forms; `homestead` and `media-tunnel`
  untouched), daemon stopped, every stack `compose down -v`, stack dirs,
  `/mnt/app` data, `/var/lib/homestead`, `/etc/homestead`, old `ak.*.pass`
  and tunnel-id removed; Docker, OS and disks kept; no snapshots. Dashboard
  built, rsynced, `scripts/install-systemd.sh`. The installed binary matches a
  root rebuild of the tree (a rebuild as `tester` differs; the build is not
  reproducible across users). Unclaimed-boot checks: the journal has the
  UNCLAIMED line naming `sudo homesteadctl claim token` and no token text
  (`grep -cF` of the token over the journal: 0); `/run/homestead/claim-token`
  is 0600 root; `sudo homesteadctl claim token` prints token and expiry.
  Deviation: a tunnel cannot be created before claim, so tunnel
  `homestead-vm2` (`85590208-fce0-4396-a8b1-62bf4b1abe0c`) was created through
  the Cloudflare API and the connector installed by hand
  (`/etc/homestead/tunnel/{config.yml,token.env}`, `homestead-tunnel.service`
  enabled); the `homestead` CNAME was repointed, proxied. The public URL
  returns 200 and `/claim` shows the Claim page. The appliance was left
  UNCLAIMED for the owner. Tokens re-mint every 30 minutes; get a fresh one
  with `sudo homesteadctl claim token` on the guest.
- KVM on VM 2: PENDING (to be run by the owner; steps are in the S288 report:
  unclaimed boot on a second daemon with its own state, `claim token` before
  and after expiry, uninstall/reinstall a small app, unassign/unregister a
  test mount, the two confirm dialogs).

## S289: Dashboard on a local network address (ADR-035)

- Dashboard socket: `internal/server/dashsocket.go` serves the TCP handler
  (same mux, same ADR-020 boundary) on `/run/homestead-dashboard/dashboard.sock`
  (dir 0750, socket 0660, group `homestead-dashboard`, created by
  `scripts/install-systemd.sh`; `RuntimeDirectory` gains the directory). The
  `--dashboard-socket` flag only accepts that path. The front wrapper requires
  exactly one non-loopback, non-unspecified `X-Forwarded-For` (400), checks
  Host against the dashboard route names (421), refuses cookie plus bearer,
  checks Origin (https and a route name), and refuses a cookie mutation with no
  Origin. A bearer without a cookie needs no Origin. On success it stores the
  client address in the request context and sets `RemoteAddr`; `requestConnection`,
  `clientHost`, login audit and the boundary report use it. TCP never reads
  `X-Forwarded-For`.
- Route model: `docker.Route.Dashboard` and `Aliases`. Validation refuses
  public exposure, automatic TLS and non-internal TLS; one dashboard route per
  table; names unique across hosts and aliases. Rendering: one site block with
  both names, `reverse_proxy unix//run/homestead-dashboard/dashboard.sock`,
  `header_up` for the client address, `trusted_proxies` unset. Caddy compose
  mounts `/run/homestead-dashboard` read-only; reconcile verifies the mount
  and says to redeploy the edge when it is missing. The planner and the
  exposure approve path refuse a public dashboard.
- Two names: `homestead.local` (mDNS, `internal/mdns`) and the owner-set name
  (default `homestead.lan`) share one multi-SAN internal-CA leaf
  (`cert.LocalTLS`), pass the Host check, are accepted Origins, and are added to
  the SSO defaults as `https://<name>`. `ssoDefaults` no longer treats a
  `host.docker.internal` target as a dashboard origin.
- Owner-set name: `GET`/`PUT /api/v1/dashboard/address` (session class),
  `homesteadctl dashboard address [get | set NAME [--exposure local|overlay]]`,
  Trust page section. PUT runs an operation: ensure leaf, deploy the edge, then
  reconcile. The server keeps the dashboard route when a client replaces the
  route table without it; the Edge editor hides it. The daemon records the
  default route and deploys the edge at startup (retry), before claim.
- CLI: `--ca-file` / `HOMESTEAD_CA_FILE` adds the appliance root to the trusted
  roots (`apiclient.WithRootCA`); verification is never skipped.
- Doctor: `edge-dashboard-socket` (route recorded, socket present, edge mount)
  and `dashboard-mdns-name` (mDNS responder bound; Avahi or systemd-resolved
  holding UDP 5353 is named).
- Docs: `docs/architecture/LOCAL_DASHBOARD.md`, PARITY rows, ARCHITECTURE note.
- KVM on VM 2: PENDING (run by the coordinator).

### S289 follow-up: mDNS answers per link

- Symptom: on a multi-NIC VM, `homestead.local` resolved to both the LAN
  address (192.168.1.158) and the QEMU NAT address (10.0.2.15). Browsers that
  picked the unreachable one hung, so the dashboard looked frozen.
- Cause: `internal/mdns` answered and announced every up, non-loopback IPv4,
  including NAT and Docker bridge addresses.
- Fix: addresses carry their subnet and interface (`mdns.LinkAddr`). A query is
  answered with the addresses whose subnet contains the query source (RFC 6762
  section 6.2); an unknown or off-subnet source falls back to all addresses.
  The startup announcement goes out per interface with only that interface's
  addresses. `docker0`, `br-*` and `veth*` are never advertised or joined.

## S291: Step-by-step setup wizard (ADR-036)

- Daemon: `internal/setup` gains the `storage` step (second, derived from the
  application storage assignment) and `Facts.StorageAppAssigned`; stored state
  carries `version` 2 and a one-time migration skips storage for appliances
  whose other steps were already settled, so none is re-gated. `PUT
  /api/v1/setup/steps/storage` accepts skip and unskip only. `homesteadctl
  setup` knows the step. Guard behaviour and exemptions are unchanged.
- Dashboard: `/setup` is a linear wizard (`setup/setup.ts`, steps under
  `setup/steps/`). Steps embed the existing pages (`embedded` input on Storage,
  Sign-in, Tunnel, Exposure, Install wizard) and the extracted `PasswordForm`
  and `RecoveryIdentity` components. `setupRefreshInterceptor` makes the wizard
  re-read status after a successful write elsewhere.
- Docs: `SETUP_WALKTHROUGH.md` rewritten; PARITY rows updated; deferred items
  added.
- KVM on the test VM: PENDING (run by the coordinator).

## S292: Setup walkthrough fixes

- Found walking setup from scratch on the KVM VM (Authentik 2026.8.3).
- Daemon:
  - `sso.Defaults` has JSON tags, so the Sign-in form is prefilled.
  - `cert.PendingRenewals` treats a missing TLS strategy as nothing due, so
    `cert.renewal-check` no longer fails on a fresh appliance.
  - The setup `Domain` source receives the provider chosen in the step, and
    `credentials_sealed` checks it first.
- Dashboard:
  - The Status page points an unclaimed appliance to Claim.
  - Creating the administrator records the password confirmation and opens
    `/setup`.
  - The Authentik link callback returns to `/setup` while setup is not
    complete.
  - The approve step reuses the token typed for the review.
  - The Sign-in step and the Authentik "first time" copy use the root
    address and the real token UI.
  - The embedded install wizard takes a `closeLabel`, hides Uninstall and
    dedupes data folders.
- Out-of-scope findings were added to `DEFERRED.md` for S293 to S295.
- KVM on VM 2 (2026-10-20, deployed over the existing appliance, no reset):
  - `GET /api/v1/sso` returns `defaults.authentik_url` and `origins`.
  - The Sign-in step lists the new outside tasks with `https://authentik.lan/`.
  - `facts.domain.credentials_sealed` is true for the Cloudflare token stored
    earlier in the step; before S292 it read false.
  - Not re-run live, covered by specs only:
    - claim to setup, F1/F2/F3 (the VM is claimed);
    - the Authentik link return (already linked);
    - the next daily `cert.renewal-check`. The failed run from before the
      fix is operation 1.

## S293: Storage step UX

- Daemon:
  - `storage.MountKind` classes each discovered mount as data, system or
    unusable.
  - `storage.Suggest` proposes classes for unassigned classes on data disks,
    from name hints, then sizes.
  - `GET /api/v1/storage/mounts` returns `kind` and `suggested`.
  - Register and assign default the actor to the caller (`operatorActor`).
- CLI: `storage mounts` lists data disks first with `kind=` and
  `suggested=`; `--actor` is optional.
- Dashboard:
  - The Storage page shows data disks first and folds the rest into "Other
    mounts".
  - It explains the classes and names the unassigned ones.
  - "Use the suggested layout" registers, then assigns, one call at a time.
  - The "Acting owner" fields are gone.
- The Storage step copy says bulk and backup can wait for the install that
  needs them.

- KVM on VM 2 (2026-10-20, deployed over the existing appliance, no reset):
  - `GET /api/v1/storage/mounts`: `/mnt/app`, `/mnt/backup` and `/mnt/bulk`
    are `data`; the other 52 paths are `system` or `unusable`.
  - The Discovered mounts table lists only the three disks. "Other mounts
    (52)" is folded away, and no "Acting owner" field remains.
  - Every class was already assigned there, so `suggested` was empty and the
    Suggested layout panel did not show, as intended. Applying a layout is
    covered by specs only; unassigning the owner's classes to show it was
    not done.

## S294: Domain and tunnel plan (ADR-037)

- Setup: the `domain` choice (`NormalizeDomain`) on
  `PUT /api/v1/setup/steps/domain`, `setup step domain --domain` and the
  Domain step's "Your domain" form; local-only suffixes and dotless names are
  422.
- Daemon:
  - `router` gains zone lookup (`ZoneFor`, longest suffix first), DNS record
    read, create and delete, and the owned-record table.
  - Migration 45 adds `account_id` and `zone_id` to `tunnel_routes` and the
    `tunnel_dns_records` table.
  - `POST /api/v1/tunnel/plan` is read-only: zone, ingress, the proxied
    CNAME (create, or replace with the records listed), warnings and a
    digest. Refusals: 400 validation, 422 no token, no zone, no account,
    hostname already routed or a non-address record at the name; 502 a
    Cloudflare error.
  - `POST /api/v1/tunnel/setup` plans again (409 on a digest change), audits
    `tunnel.setup`, and runs create-tunnel, dns, connector and record-route,
    undoing the record and the tunnel when a later step fails.
  - Teardown takes no account ID, audits `tunnel.teardown`, and deletes only
    owned CNAMEs that still point at the tunnel (a changed record is kept and
    audited as `tunnel.teardown.dns-kept`).
  - The token is the Domain step's `dns_cloudflare_api_token`, else the
    legacy `router_tunnel_api_token`; bodies carrying `api_token` or
    `account_id` are 400.
- CLI: `tunnel plan`, `tunnel setup --digest`, `tunnel teardown <id>`; the
  old credential flags are a usage error.
- Dashboard: the Tunnel form has name, hostname (suggested under the setup
  domain) and service, then a review panel with "Public hostnames", "DNS
  changes", warnings and "Approve and set up"; a 409 asks for a new review.
- Tests: a Cloudflare double covers plan refusals, plan, setup and teardown,
  a changed record kept at teardown, and the permission copy maps every
  client call to a listed grant.

### S294 fixes (owner review, 2026-10-20)

- Domain: refused names (local-only, dotless, bad labels) are 422
  (`setup.ErrRefused`), as the slice specifies; with Cloudflare chosen and
  its token stored, a domain outside every visible zone is 422 too.
- Replaced records (ADR-038, migration 46 `tunnel_replaced_records`): setup
  records each one before deleting it and puts them back when it fails;
  teardown's `restore-dns` step puts them back unless the name holds an
  A, AAAA or CNAME record again (audited `tunnel.teardown.dns-not-restored`).
- Status speed: `router.DiscoverGateway` runs UPnP and NAT-PMP/PCP probes
  together, and the tunnel status reuses a discovery for 5 minutes while
  the Cloudflare reads run alongside it. Tests stub the probe.
- Dashboard: `app-operation-progress` keeps the last state it saw for its
  operation, so it reads "Done" after the rail drops the order instead of
  "Starting"; the Tunnel page re-reads status when setup or teardown ends.
- `tests/test_s235_storage.py` looks for the `actor` flag name, which S293
  moved into a helper.

### S294 gaps closed (2026-10-20)

- Storing Cloudflare credentials (`POST /api/v1/tls/issuance/credentials`)
  checks the setup domain against the token's zones through a hook
  `MountSetup` sets (`Server.domainZoneWarning`). The token is stored either
  way; the response carries `warning`, which the Domain step shows and
  `homesteadctl tls issuance credentials` prints to stderr.
- `server.WarmTunnelDiscovery` probes the router once at daemon start (from
  `main`, never from a mount, so tests never probe the network).
- Migration 47 adds `comment` to `tunnel_replaced_records`; teardown puts
  the record's own comment back and marks only uncommented records.

## S295: App names (ADR-039 §1)

- Routes: `Route.LocalAlias` derives `<label>.local` for a non-dashboard,
  non-public, non-automatic-TLS route named `<label>.lan` (single label, not
  `homestead`). It is computed, not stored: `ServedAliases` adds it to the
  rendered site address and `cert.LocalTLS` adds it to the leaf, so an
  existing leaf mismatches once. The daemon runs one local TLS reconcile at
  start, so after an upgrade the leaf is reissued and the edge reloaded
  straight away rather than at the next 6 h `localtls` pass.
  `RouteSet.MDNSLabels` and `RouteSet.LANNames` feed the responder and the
  resolver.
- mDNS: `Config.Apps` adds app labels to the answered names; a 30 s recheck
  announces new names and drops conflicts for removed ones. An A answer for
  one of our names from another (non-loopback) address is kept for 15
  minutes as `State.Conflicts`, which the doctor reports as
  `mdns-name-conflict`.
- Resolver: `internal/localdns` answers A for owned `.lan` names (the
  address on the asker's subnet, else all), NODATA for other types,
  NXDOMAIN (authoritative) for other `.lan` names, REFUSED otherwise,
  FORMERR for multi-question and NOTIMP for non-query messages, over UDP and
  length-prefixed TCP. It never forwards. Binding is all or nothing.
- Daemon: `server.LocalNames` caches the route table for 10 s, stores the
  switch (migration 48 `local_resolver`), and runs the listener from `main`.
  `GET /api/v1/names`, `POST /api/v1/names/resolver/plan` (probes port 53,
  lists the firewall rules) and `.../apply` (digest, 409, audited
  `names.resolver.enable`/`disable`). The firewall's desired set gains
  `firewall.WithLocalResolver` (port 53 accepted from local subnets, dropped
  from everywhere else) while the switch is on. The doctor's
  `local-resolver` check warns when it is on but not answering.
- CLI: `names`, `names resolver plan|apply --on|--off [--digest]`,
  `apps names`.
- Live check (VM 2, 2026-10-20): `immich.local` and `authentik.local`
  answer QU mDNS queries from the host with the VM's address, and the edge
  serves them with a leaf covering both names. The responder sends
  ordinary (QM) answers to the group, so `dig -p 5353` unicast gets none;
  test with avahi or a QU query.
- Dashboard: the Names page (`/names`, Network) and "Local names" in the
  catalog Manage panel.

## S296: Public dashboard behind Authentik (ADR-037 §4)

- Edge: `Route.Tunnel` holds a route's public names. `Route.AllNames` (its
  names plus the tunnel names) feeds conflicts, the exposure refusal, the
  dashboard Host check and the SSO origins. The edge serves the tunnel names
  as `http://<name>:8480` sites (`docker.TunnelEntryPort`), published only on
  `127.0.0.1:8480`, with `X-Forwarded-For` from `Cf-Connecting-Ip`,
  `X-Forwarded-Proto https` and `X-Forwarded-Host`. A route edit keeps the
  route's tunnel names.
- Tunnel plan: the request is `{name, dashboard_hostname, auth_hostname}`
  (defaults `homestead.<domain>` and `auth.<domain>`); `hostname` and
  `service` are refused with 400. Every ingress points at
  `http://127.0.0.1:8480` and the connector config ends with a 404
  fallback. Authentik is left out when it is not installed. The dashboard
  is left out, with the reason, when it has no edge address, when sign-in
  is not linked, or when Authentik is not published. A plan with nothing
  left is 422. Setup gains an `edge` step (sets the tunnel names, deploys
  and reconciles the edge); teardown clears them.
- Public host: requests whose Host is a dashboard tunnel name refuse claim,
  `POST sessions`, `admins/setup|password|recover`, the wizard state
  routes, `sso/link` and every step-up (`X-Homestead-Reauth`) request with
  403 and `X-Homestead-Error: public-host`. `GET /api/v1/sso/options`
  answers `password:false` there, and the login page shows only Authentik.
- Sign-in: `sso.Defaults` gains `public_origins` and `public_authentik_url`
  (from the Authentik route's first tunnel name). A plan that includes a
  public origin records both. A login started at a public origin sends the
  browser to the public Authentik address; the token exchange and the
  userinfo call stay on the local Authentik address. `GET /api/v1/sso`
  reports `replan` and `replan_reason` while the configured setup misses a
  public name: `sso status` prints it, and the Sign-in page has a Re-plan
  button that adds the public origin to the form.

- Live check (VM 2, 2026-10-20): deployed; the local login still offers the
  password (`{"enabled":true,"password":true}`). The tunnel setup and the
  public sign-in round trip wait for the owner's session and approval of
  the Cloudflare changes.

## S297: Nightly backups in setup (ADR-039 §2)

- Policy: migration 49 adds `backup_policy` (one row: `time_of_day`,
  `retain`, `enabled`, actor, `updated_at`). `backup.Policies` keeps it;
  `updated_at` moves only when the time changes or the policy is switched
  on, so a retention edit never delays a run. Defaults: 02:00 local, keep
  7, on; retention 1 to 366.
- Coverage: every installed catalog app that keeps data and has no
  per-app schedule. Stateless apps and apps with their own schedule (on or
  off) are listed as left out, with the reason. Later installs are covered
  without another approval.
- Scheduler: `scheduler.Job.At` (`scheduler.Daily`) makes a wall-clock job:
  it is due at the first policy time after its last run (or after the
  policy's anchor, or the app's install, whichever is later). A time missed
  while the appliance was off runs once at the next tick. Policy jobs come
  from their own source (`backup-policy`), share the `backup:<app>` job ID
  with per-app schedules, and use the policy's retention.
- API: `GET /api/v1/backups/policy`, `POST .../policy/plan` (read-only,
  digest), `POST .../policy/apply` (digest must match, 409 otherwise;
  audited `backup.policy.set`). Enabling without backup storage is 422.
- CLI: `homesteadctl backup policy [status|plan|apply]` with `--time`,
  `--retain`, `--off`, and `--digest` for apply.
- Dashboard: a Nightly backups section on the Backups page and a sixth
  setup step, both the shared `app-nightly-policy` component.
- Setup: step `backups` ("Nightly backups") after apps. Done when a policy
  is approved (on or off); skippable; `{"state":"done"}` is 400. Without
  backup storage the detail and the step explain it and link to Storage.
  The stored record is version 3; an appliance whose older steps were all
  done or skipped has every newer step waived (the storage rule,
  generalized).
- Live check (VM 2, 2026-10-21): the setup step's proposed policy (02:00,
  keep 7, Authentik and Immich) was approved and setup read complete. The
  time was moved to two minutes ahead: both `backup.job` operations ran on
  time and succeeded. The policy was then set back to 02:00.

## S298: App sign-in through Authentik (ADR-039 §3, ADR-040)

- Manifest: `sso.oidc` declares the adapter (`immich`,
  `nextcloud-user-oidc`, `jellyfin-sso-plugin`), the service that talks to
  Authentik and, for Jellyfin, the pinned plugin (name, version, URL,
  sha256). Validation refuses unknown adapters, a plugin on the wrong
  adapter and a plugin without a 64-hex checksum.
- Package `internal/appsso`: `BuildPlan` (on or off, digest over the
  provider, application, client ID `homestead-<app>`, issuer, redirect URIs
  and app settings), `BuildStatus` (on, offered or why not, and `replan`
  when the app's names no longer match the applied redirect URIs), the
  three adapters and `Definition`, the `app.sign-in` operation.
- Apply: create or update the Authentik OAuth2 provider and application,
  seal the client secret as `sys_sso_app_<app>_secret` (ADR-021), write the
  service's `extra_hosts` and the read-only CA mount into the generated
  compose (`catalog.ApplySignInWiring`, own entries only), bring the stack
  up, then configure the app: Immich's system config, Nextcloud's
  `user_oidc` provider through `occ`, Jellyfin's plugin (downloaded,
  checksum-verified, unpacked with zip-slip refusal) and its
  configuration. Secrets reach containers only on stdin. Off reverses
  each step and deletes the Authentik objects. Migration 50 adds
  `app_sign_in` (package, adapter, client ID, issuer, redirect URIs, plugin
  version, actor, `updated_at`).
- API: `GET /api/v1/catalog/{name}/sign-in`, `POST .../sign-in/plan`
  (read-only), `POST .../sign-in/apply` (digest, 409 when stale; token in
  the body unless the dashboard sign-in kept one; returns an operation).
  All session class. The install plan carries `sign_in` (offered, reason,
  the plan, `token_kept`). Audit: `app.sign-in.operation`,
  `app.sign-in.authentik`, `app.sign-in.on`, `app.sign-in.off`, never a
  secret.
- CLI: `homesteadctl apps sign-in <status|plan|apply> <app>` with `--off`,
  `--digest`, `--token-file`, `--follow`.
- Dashboard: the install wizard offers "Sign in with Authentik" (checked)
  in the review and applies the previewed plan once the route lands; the
  catalog Manage panel has a Sign-in with Authentik section
  (`app-app-sign-in`) to review, approve, re-plan or turn it off.
- Live check (VM 2, 2026-10-21), Immich only (Jellyfin and Nextcloud are
  not installed there): the plan and apply ran with a short-lived Authentik
  token, and every operation step succeeded. Immich then showed "Sign in with
  Authentik", and `POST /api/oauth/authorize` fetched discovery through the
  back channel (`authentik.lan` via host-gateway, appliance CA trusted). It
  returned a PKCE authorize URL that Authentik accepted for
  `homestead-immich`.
- Live check, Jellyfin and Nextcloud (VM 2, 2026-10-21): both installed from
  the catalog (ops 23, 24; `lan` domain) and routed through `edge routes`
  (op 25). Each apply used a short-lived Authentik token, deleted afterwards.
  The check found two adapter faults, both fixed here:
  - Jellyfin: the pinned plugin SSO Authentication 4.0.0.4 (targetAbi
    10.11) loads on `jellyfin:12.2` and shows Active. But the first
    `/sso/OID/start/authentik` answered 400 "Provider does not exist", and
    the settings file was back to empty. The plugin's `OidConfig` carries
    `[XmlRoot("PluginConfiguration")]`; the adapter wrote `<OidConfig>`, the
    XML failed to deserialize, and Jellyfin silently saved default settings
    over it. The adapter now writes `<PluginConfiguration>`. After
    re-applying (op 27), the start URL redirects 302 to Authentik with PKCE
    and the settings survive a restart. ADR-040 stands: no adapter
    withdrawal and no image downgrade.
  - Nextcloud: an apply before Nextcloud's first-run setup failed in
    `configure-app` with occ's "no commands in the app namespace" (op 28,
    after the Authentik provider was created). The adapter now refuses with
    a plain "finish first-run setup" message. After setup (SQLite through
    `autoconfig.php`), op 29 succeeded, but `/apps/user_oidc/login/1`
    answered "You must access Nextcloud with HTTPS to use OpenID Connect".
    Nextcloud saw plain http from the edge. The adapter now also sets
    `overwriteprotocol https`. After re-applying (op 30), "Login with
    authentik" redirects 303 to Authentik (PKCE, https redirect URI), and
    Authentik answers with its login flow.
  - Not covered: a full browser login with an Authentik user for any of the
    three apps (account creation at first sign-in).

## S290: Public app names and tunnel adoption (ADR-041)

- The slice text predates ADR-037: the tunnel DNS route actuation it names
  was closed by S294. S290 adds per-app public names, a connector that
  follows the recorded routes, and adoption of an existing tunnel.
- API: `GET /api/v1/catalog/{name}/public`, `POST .../public/plan`
  (read-only, `enabled`, `hostname`, `route`) and `POST .../public/apply`
  (digest, 409 when stale; returns an `app.public.add` or
  `app.public.remove` operation). All session class. A plan refuses a local
  name, a name outside the tunnel's zone, a name already on an edge route or
  the tunnel, an app that is not installed, a missing tunnel and Authentik
  (its name belongs to the tunnel plan). DNS outcome is create, keep (a
  CNAME already pointing at the tunnel) or replace (A, AAAA, other CNAME;
  the old record is stored and restored on removal). The plan warns that
  no Authentik check sits in front of the app and recommends app sign-in
  when the app has an adapter.
- Apply: DNS, recorded route, edge tunnel name, then the connector's
  `config.yml` rewritten from the recorded routes; the connector restarts
  only when its files changed. Each step undoes the earlier ones on
  failure. Removal reverses them and restores replaced records.
- Adoption: `POST /api/v1/tunnel/plan` and `/setup` take `adopt` (a tunnel
  ID) instead of `name`. It needs the local connector config to name that
  tunnel, Cloudflare to report it as locally configured, and no recorded
  tunnel. The plan lists existing ingress entries as kept or dropped; setup
  fetches the run token with the stored Domain-step token, keeps the
  tunnel on failure, restores the previous connector files, and audits
  `tunnel.adopt`.
- CLI: `homesteadctl apps public <status|plan|apply> <app>` with
  `--hostname`, `--route`, `--off`, `--digest`, `--follow`;
  `homesteadctl tunnel plan|setup --adopt ID`.
- Dashboard: the catalog Manage panel has a Public address section
  (`app-app-public`); the Tunnel page has an adopt field and an "Approve
  and adopt" button.
- Live check (VM 2, 2026-10-21, main at 75cca1e deployed over the claimed
  appliance). This replaces the slice's original live steps:
  - the connector files left from a hand-made setup were restored, then
    `tunnel plan` with `adopt` listed the existing entry as kept, kept the
    existing dashboard CNAME and created the Authentik one; setup fetched
    the run token, rewrote the config through the edge, started the
    connector (healthy, four connections) and audited `tunnel.adopt`; the
    Domain setup step then read done;
  - the public dashboard answered 200, and Authentik's public name sent the
    browser to its login flow;
  - `immich.<domain>` was added (create), served `/api/server/ping` publicly,
    then removed: the CNAME was gone from the zone and the connector config
    dropped it; both operations were audited;
  - not covered live: replacing and restoring an existing A record (Jellyfin
    is not installed on VM 2, and the zone has no spare record); the unit
    tests cover it;
  - found: the status has no proposed name when the Domain step has no
    domain recorded (adoption sets up the tunnel without one), and the
    remove plan shows an empty zone. Both are cosmetic; recorded in
    DEFERRED.

## S299: Uninstall leftovers

- Public names (S290) and sign-in (S298) are removed by plans that need the
  app installed, so `POST /api/v1/catalog/{name}/uninstall` now refuses
  with 409 before anything changes:
  - `public-names`: the app's edge routes carry tunnel names; the message
    lists them and the `apps public plan <app> --off --hostname NAME` step.
    No override;
  - `sign-in`: sign-in is recorded; the message names
    `apps sign-in plan <app> --off`. `leave_sign_in: true` uninstalls
    anyway; a final `forget-sign-in` step deletes the sealed client secret
    and the record and audits `app.uninstall.leave-sign-in` with the client
    id (never a secret);
  - `sign-in-dependents`: uninstalling Authentik while apps have sign-in
    recorded; the message lists them. No override.
- The refusal kind is sent as `X-Homestead-Refusal` (the volume refusals
  send `volumes`), so the dashboard offers only the matching
  acknowledgement: abandon volumes, or "Uninstall anyway and leave its
  sign-in in Authentik".
- CLI: `homesteadctl apps uninstall <app> --leave-sign-in`.
- Tests: `internal/server/uninstallguard_test.go` (each refusal, the
  acknowledgement, record and audit), `cmd/homesteadctl/apps_test.go`,
  `dashboard/src/app/catalog/uninstall-app.spec.ts`.


## S300: Tunnel connector token off the command line

- `homestead-tunnel.service` no longer passes `--token ${TUNNEL_TOKEN}`:
  cloudflared reads `TUNNEL_TOKEN` from the environment, loaded from the
  root-only `/etc/homestead/tunnel/token.env`. The command line is
  world-readable in `/proc`; the environment is root-only.
- `scripts/install-systemd.sh` runs `systemctl try-restart
  homestead-tunnel.service` after `daemon-reload`, so an upgrade applies the
  unit to a running connector and leaves a stopped one alone.
- Tests: `tests/test_s300_connector_token.py`.

## S301: Claim first, then the tunnel

- Documentation only (owner decision 2026-10-24): a tunnel is not set up
  before claim. `docs/architecture/LOCAL_DASHBOARD.md` now says to claim on the
  local network, or for a remote box through `ssh -L 8080:127.0.0.1:8080`
  (the loopback listener serves the Claim page; checked on VM 2), and gives a
  hand-laid tunnel, adopted after claim, as the last resort.

## S302: Adopted tunnel zone

- Migration 51 adds `tunnel_routes.zone_name`; `router.SetTunnelAccount`
  takes it (empty keeps the stored name) and `router.TunnelZoneName` reads it.
- App public status and the add plan propose `<app>.<domain>` from the setup
  domain, else the tunnel's zone name; the remove plan names the zone from the
  store, else from Cloudflare (`appPublic.removeZone`).
- Tests: `TestAppPublicAdoptedTunnelZone`, the tunnel setup test, and the store
  upgrade tests (which now drop `zone_name` too).

## S303: Tunnel plan refusal in view

- `Tunnel.review` sets `reviewError`, shown under Review plan in the setup
  form; the page-level `error` stays for status, approve and teardown.
- The adopt hint says a new or reset appliance has no connector config.
- Test: `tunnel.spec.ts` "shows a refused plan under Review plan".

## S304: Setup names a needed sign-in re-plan

- `setup.SSOFacts.Replan` is `replanReason(spec, ssoDefaults(...)) != ""`
  for a configured sign-in; `SetupConfig.Port` feeds the default origins.
- The Sign-in step stays done and its detail warns; the Domain step's detail
  after a tunnel says the same. Neither re-gates setup.
- Tests: `internal/setup` sign-in states and the existing-tunnel review path.

## S305: Walkthrough wording (install sign-in, policy review)

- `install-wizard.html` sign-in step: the review offers Sign in with
  Authentik where it can be connected (S298).
- `NightlyPolicy` review renders the plan's `covered` and `skipped`, which
  the plan's first line ("every app below") refers to.
- The time label drops "24-hour"; the native time picker follows the locale.

## S306: Factory reset (preparing a new appliance)

```bash
# from a checkout of the release to ship, on the appliance:
sudo ./scripts/factory-reset.sh --yes \
  --wipe-mount /mnt/app --wipe-mount /mnt/bulk --wipe-mount /mnt/backup \
  --reinstall
sudo homesteadctl claim token          # the new owner's claim token
```

- Removes only Homestead's units, binaries, stacks (with volumes), edge
  container and network, layout dirs and groups; other Compose projects,
  volumes and images stay.
- `--wipe-mount` is the only way storage is touched: each path must be an
  absolute mount point outside system paths; contents go, `lost+found`
  stays. Leave it off to keep the owner's media.
- A previous owner's Cloudflare tunnel and DNS records are in their own
  account; the reset does not remove them.
- Tests: `tests/test_s306_factory_reset.py`.

## S307: Install guide (bare metal to public apps)

- `docs/INSTALL.md` is the owner and fulfilment guide; `README.md` links it.
- It names the prerequisites Homestead does not install: Docker (A3),
  cloudflared (A4) and the dashboard build (B2); see `docs/DEFERRED.md`.
- Tests: `tests/test_s307_install_guide.py` checks that the scripts, doc paths
  and `homesteadctl` commands it names exist.

## S308: Bootstrap installs Docker and cloudflared (ADR-042)

- `bootstrap.sh` installs Docker (with the Compose plugin) and cloudflared from
  the vendors' apt repositories after `fetch_key` matches the pinned
  fingerprint; `HOMESTEAD_DOCKER_KEY_FPR` / `HOMESTEAD_CLOUDFLARE_KEY_FPR`
  override for one run after a vendor key rotation.
- Existing Docker with Compose v2 and an existing cloudflared are kept;
  `--no-docker` and `--no-cloudflared` skip them. No cloudflared service is
  enabled; `homestead-tunnel` runs it after an approved plan.
- To try it without a VM, run the script in a `debian:trixie` container with
  `systemctl`, `go` and `install-systemd.sh` stubbed on PATH.
- Tests: `tests/test_s308_bootstrap_vendor_packages.py`.

## S309: Dashboard design language (the permit and the field)

- Display face: Barlow Semi Condensed SemiBold in `dashboard/public/fonts/`
  (OFL, `docs/THIRD_PARTY.md`), for `h1`, `h2`, the wordmark, the permit
  title and the stamp only.
- Every page opens with `<header class="masthead">`: the `h1` and one true
  sentence (`masthead-fact`), computed from data the page already loads or a
  one-line purpose.
- `.field-panel` with `data-state="warn|fail|ok"` draws a state furrow; the
  `.needs-you` field leads Status and Setup when anything needs the owner.
- Plan reviews use `<app-permit>` (`dashboard/src/app/ui/permit.ts`,
  `src/permit.css`). Pass `digest` for the seal, `approval` once approved
  (`approvedPlan(...)` keeps a stamped record) and `refusal` when the
  appliance would not approve the plan on screen.
- New colour tokens are in all three themes in `tokens.css`, and every new
  text or border pair is in `theme/tokens-contrast.spec.ts`; the high
  contrast permit tokens are scoped under `[data-theme='high-contrast'] .permit`.
- The validation board (`/validation`) shows fields, a ledger and the three
  permit states. See `docs/architecture/DASHBOARD_DESIGN.md`.

## S310: Lamplight, depth and dark by default

- Dark is the default: no stored theme means `:root` dark with no
  `data-theme`. `ThemeService` (`DEFAULT_THEME`) clears the key when the
  owner picks Dark; `public/theme-init.js` applies only light, system and
  high contrast before first paint.
- Depth is tokens only: `--elevation-well|plate|lifted|control|pressed`,
  `--highlight-top`, `--edge-shadow`, and `--elevation-paper` (a `filter`,
  because the permit's perforation mask clips `box-shadow`). Never write a
  raw shadow in a component.
- Attention light: `--glow-attention`, `--glow-furrow`, `--glow-focus`,
  `--halo-warn`, `--halo-fail`. Halos are background images behind the
  glyph, since filters on masked glyphs are clipped.
- The land: `--contour` (an SVG data URI), `--contour-ink`, `--lamp-falloff`,
  `--lamp-wash`, `--horizon`, `--band-height`, drawn by `.shell::before` and
  `::after` and the masthead `::after`.
- High contrast sets every elevation, glow, halo, wash and falloff to
  `none`; `tokens-contrast.spec.ts` checks it, the depth luminance order,
  and that the system-light block matches light. Token names in that file
  must not contain digits, and values must not contain `;` or `}`.
- The validation board shows the four depths and the lamps.

## S311: Lamplight on every page

- Every page section is `<section class="field-panel" aria-labelledby="…">`
  with its `h2` as the first child; a page without h2s puts its content in
  one field. The catalog's Manage panel (`.catalog-manage`, S321) opens
  inside its app's card and its `h2` counts as opening a field. Add `data-state="warn|fail|ok"` for a furrow and `needs-you`
  only for a section that needs the owner now.
- On the ground, below the masthead, only fields, `app-permit`, dialogs,
  `app-operation-progress`, page-level `app-state-message`s and a line of
  prose. `kind="empty"` messages are wells and belong inside a field.
- Claim and login use `<section class="gate">`, a centred lifted plate.
- `src/app/theme/lamplight.spec.ts` enforces this for every route in
  `app.routes.ts` (its `PAGES` map must list each route and template), and
  rejects colour literals and raw shadows in every stylesheet except
  `tokens.css` and `fonts.css`, including component `styles`.
