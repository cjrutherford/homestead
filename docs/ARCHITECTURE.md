# Architecture

## Core components
- `homestead-server`: native static Go daemon, systemd-managed, root-owned.
- `homesteadctl`: standalone Go CLI using the same API.
- Angular dashboard: compiled static assets served by the daemon in production.
- SQLite: operations, sessions, onboarding, audits, wizard state.
- Scheduler (S260, ADR-023): one in-process scheduler in `homestead-server`
  (`internal/scheduler`). It only enqueues due work through the operation
  runner, so scheduled runs appear in operations and audit as actor
  `scheduler`. Last-run state is in SQLite (`scheduler_jobs`). A window
  missed while the daemon was down runs once at startup (no burst
  catch-up); a job still queued or running is skipped and audited
  (`scheduler.skip`: job, due time, blocking operation), never run
  concurrently. Domains register a `scheduler.Source` (`Register`) whose
  jobs are re-read each tick; backup schedules are the first source, and
  drift scans and certificate checks register later. State is visible via
  `GET /api/v1/scheduler/jobs` and `homesteadctl scheduler jobs`.
  Schedules run only while the daemon runs; there are no systemd timers.
  Drift scan (S261): job `drift:scan` runs the file-drift scan over
  installed packages on an owner-set interval (default daily, min 5m;
  `drift_scan_schedule`, latest result in `drift_scan_latest`) as a
  `catalog.drift-scan` operation plus a `drift.scan` audit entry. It only
  detects, never accepts or restores. API: `GET|POST /api/v1/drift/schedule`.
  The `cert` source (S263, ADR-024) runs `cert:renewal-check` daily as a
  `cert.renewal-check` operation: it grades stored issuances (fingerprint
  of normalized names, DNS provider id, challenge method, recorded in
  `cert_issuances` by `DNS01.IssueFor`) as ok / renewal-due /
  plan-required / expired / error (corrupt chain), or no-recorded-issuance
  (no daemon path issues certificates yet; docs/DEFERRED.md). It reports
  only; clean checks write no audit entry. Status: `GET /api/v1/tls/renewal`, `homesteadctl tls renewal`,
  doctor checks `tls-renewal:<name>`, and the dashboard Exposure page.
- YAML: customer-readable desired configuration and generated artifacts.
- Docker Engine + Compose v2: application runtime.
- Caddy: application reverse proxy / HTTP edge.
- AdGuard Home: initial local DNS provider.
- Headscale/Tailscale: private overlay providers.
- Dockge: expert Compose interface.

## Control boundary
The browser never receives Docker socket access. The Go daemon owns host-level mutation.

The TCP listener is wrapped by the operator session boundary (ADR-020):
every route is classified public, claim-window, or session in code and the
middleware enforces it (since S257): unauthenticated requests to non-public
routes get 401, pre-claim access gets 403, and an unclassified route is
refused. The root-only Unix socket is exempt; its file mode is its boundary.
Sessions share one store with two transports (S256): an HttpOnly,
SameSite=Strict cookie plus `X-CSRF-Token` for the browser, and
`Authorization: Bearer` for the CLI; 12h idle / 7d absolute lifetime.

## Desired/generated/observed
Homestead separates:
- desired state: what the owner requested;
- generated state: concrete config Homestead produced;
- observed state: what is actually running.

## Paths
- `/usr/local/bin/homestead-server`
- `/usr/local/bin/homesteadctl`
- `/etc/homestead/`
- `/var/lib/homestead/`
- `/var/log/homestead/`
- `/run/homestead/`
- `/opt/homestead/stacks/`

## Application data
Catalog apps keep persistent data in bind mounts under assigned mounted
storage: `<class mount>/<package>/<subpath>`, declared per service in
`catalog/*.yaml` (S276). Generated Compose files are self-contained with
absolute host paths, so the stack stays usable without Homestead.
Backups scan `<mount>/<package>` per assigned mount. Existing installs
that predate this layout are migrated by S277.

## Secret-backed container variables
Values the secret store holds that containers need (database passwords,
the Authentik secret key) are delivered as mode 0600 files under the
stack directory and referenced from the generated Compose through
top-level `secrets:` with `file:` sources (S266, ADR-021). They are never
`environment:` entries and never enter YAML, drift output, logs, audit,
operation payloads, configuration export, or plaintext backups.
Manifests declare them (`secrets:`); install generates missing values;
configure rotates them; backups read the database password from the
store. See `docs/DEVELOPMENT.md`.
