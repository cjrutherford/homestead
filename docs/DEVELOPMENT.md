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
cmd/homesteadctl/       homesteadctl binary (placeholder until S018; see ADR-004)
internal/server/        daemon HTTP surface: health/version + graceful shutdown (S010)
internal/layout/        authoritative filesystem layout + provisioner (S012)
internal/store/         versioned SQLite operational state (S013; ADR-003)
internal/desired/       versioned YAML desired-state storage (S014; ADR-003)
internal/schema/        typed configuration metadata + validation (S015; ADR-004)
internal/config/        validated configuration service: get/plan/apply (S016)
internal/apiclient/     reusable Go API client for local consumers (S017; ADR-004)
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
pnpm start              # ng serve, local dev server
pnpm build              # production build into dashboard/dist/ (gitignored)
```

Component unit tests (`ng test`) are intentionally not wired yet: the scaffold was
generated with `--skip-tests` and no spec files exist. Karma/browser-based testing
arrives with the first real UI slice (S030+), which will also decide the runner.

Adding a dashboard dependency that runs an install script requires explicit
approval first: `pnpm approve-builds <pkg>...` records it in
`dashboard/pnpm-workspace.yaml`. Unapproved scripts fail the install
(`ERR_PNPM_IGNORED_BUILDS`), including `--frozen-lockfile` in CI.

## Provisional decisions (revisit, do not work around silently)

- **Go module path is `homestead`** (bare). No VCS remote exists in the planning
  packet, so no org URL is claimed. When a remote is chosen, rename the module in
  `go.mod` plus the `homestead/internal/...` import prefixes (one mechanical change).
- **Lint baseline is `gofmt` + `go vet`.** Adopting `golangci-lint` (or another
  aggregator) belongs to a later slice; do not add ad-hoc linters per slice.
- **Direct Go dependencies need an owning slice** (ADR-001): `modernc.org/sqlite`
  (S013, pure Go — cgo drivers would break the static daemon) and
  `gopkg.in/yaml.v3` (S014, pure Go). Further deps arrive only with the
  slices that need them.
