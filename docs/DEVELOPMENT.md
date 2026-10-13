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
go run ./cmd/homestead-server --data-dir /tmp/hs-data
# ... logs the single-use console token (30min TTL) on fresh start
curl localhost:8080/api/v1/identity              # {"state":"UNCLAIMED",...}
go run ./cmd/homesteadctl --server http://127.0.0.1:8080 claim <TOKEN>
curl localhost:8080/api/v1/identity              # {"state":"CLAIMED",...}
```

- Tokens are 24-byte crypto/rand, base64url on the console, SHA-256 hashes
  in SQLite, constant-time compared, expired ones pruned on mint.
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
export HOMESTEAD_TOKEN="$(homesteadctl login --username owner --password-file ./owner.pass | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')"
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
  HOMESTEAD_CLAIM_TOKEN=<console-token> ./scripts/acceptance.sh
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
  `/api/v1/backups/*`) and a dashboard Backups page since S267; the
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
  HOMESTEAD_CLAIM_TOKEN=<console-token> \
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
homesteadctl storage register --mountpoint /mnt/app --label app --actor owner
homesteadctl storage assign --class application --mountpoint /mnt/app --actor owner
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
  CLI; due and plan-required warn in doctor. Nothing re-issues (S272).
- `GET /api/v1/tls/renewal` returns `{recorded_issuances, summary,
  certificates}`. A desired certificate with no recorded issuance reports
  `no-recorded-issuance`; with none recorded at all, `summary` states
  "no recorded issuances" (also an ok `tls-renewal` doctor check, the CLI
  first line, and the dashboard). Today no daemon path issues
  certificates, so this is the normal state (docs/DEFERRED.md).
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

`/claim`: token step (single-use console token, 401 invalid/expired, 409
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
endpoints serve nothing routable by construction. The base suite (S172), also in
`internal/catalog`, resolves the curated bundle in install order with joint
grading: per-package verdicts plus combined memory footprints and cross-member
port collisions naming both owners. The Authentik package (S201) in
`catalog/` is the fourth curated application following the official
compose pins (server, worker, postgres) with internal services
carrying no endpoint: the first manifest declaring a Postgres
database export for backup, with credentials wiring post-install
through configuration. The backup contract (S180),
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
outages error instead of reading as current, and non-Hub images
refuse loudly. Update planning (S191), also in `internal/update`,
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
