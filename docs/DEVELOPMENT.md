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
homesteadctl login --username owner --password-file ./owner.pass   # prints session token
homesteadctl logout --token "$TOKEN"        # or HOMESTEAD_TOKEN env
```

- Setup succeeds exactly once: CLAIMED state, no administrator yet (403
  unclaimed, 409 exists, 422 weak input). Usernames match
  `[a-z][a-z0-9_-]{0,63}`; passwords minimum 12 characters.
- Passwords are bcrypt hashes; sessions are 30-day SHA-256-hashed tokens in
  SQLite (`Authorization: Bearer`). Login never distinguishes unknown users
  from wrong passwords. Password files should be 0600.
- Sessions are created here, not yet required: other endpoints stay open
  until a later slice enforces authentication. Recovery is S021.

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
`/login` plus `SessionService`: login stores token/username/expiry in
`localStorage` (`homestead.session`, matching the 30-day server sessions);
the shell nav shows the username with logout instead of the login link.
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
converge).

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
