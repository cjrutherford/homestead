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
  detects, never accepts or restores; it also compares running containers (S262,
  `docs/DEVELOPMENT.md`). API: `GET|POST /api/v1/drift/schedule`, `GET /api/v1/catalog/{name}/drift/live`.
  The `cert` source (S263, ADR-024) runs `cert:renewal-check` daily as a
  `cert.renewal-check` operation: it grades stored issuances (fingerprint
  of normalized names, DNS provider id, challenge method, recorded in
  `cert_issuances` by `DNS01.IssueFor`) as ok / renewal-due /
  plan-required / expired / error (corrupt chain), or no-recorded-issuance
  (none until an issuance is applied, S278). It reports
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
operation payloads, configuration export, or plaintext backups: the
stack `secrets/` directory is a sensitive backup source that only runs
encrypted (S259).
Manifests declare them (`secrets:`); install generates missing values;
configure rotates them; backups read the database password from the
store. See `docs/DEVELOPMENT.md`.

## Backup encryption (S259, ADR-022)
Every backup run is age-encrypted (`filippo.io/age`, X25519). Format 2:
each source tree is one streamed `tar | age` file, `<run>/<entry>.tar.age`
(directories, modes, ownership, in-tree relative symlinks, per
S276/870ce9b semantics), and each database export is one encrypted file
`<run>/<name>.age`; no plaintext of the data is written to disk (the SQLite
snapshot, which SQLite requires as a file, lives briefly in private tmpfs
scratch). `backup.json` stays readable plaintext with non-secret
metadata: package, timestamp, entry names, counts, `format_version`,
`encrypted`, and the entry-to-archive map. Runs without those fields are
format 1 (plaintext) and restore unchanged. The appliance identity is
generated on first use and held in the secret store as `sys_backup_identity`
(reserved `sys_` namespace: neither owner routes nor the catalog can write
it, absent from configuration export, never logged or returned; an identity
stored by the first build under `app_backup_identity` migrates on startup). Stack secret files (S266) join each run as a sensitive source
(`stack-secrets/<pkg>/secrets`) and restore 0600 with recorded ownership.
Restore decrypts with the appliance identity (`backup.Identities`) into a
hidden sibling staging directory, drains the stream to EOF, then swaps it
in by rename (old tree kept aside until the swap succeeds), so a wrong key
or damaged stream leaves live data untouched. Runs are complete only with a
manifest (written atomically); interrupted runs are cleaned up, and
retention prunes after the manifest and counts complete runs only. Pre-update, scheduled, and
pre-migration backups all encrypt. The plain-`age` restore runbook is S275.

### Owner recipients and recovery export (S271)
Owners add native age X25519 public keys (`age1...`; SSH and plugin
recipients are refused) with `homesteadctl backup recipients
list|add|remove`, the Backups dashboard page, or `/api/v1/backups/recipients`.
Every new run encrypts to the appliance identity plus all owner recipients
(`backup.RecipientsWithOwner`; pre-update, scheduled, and pre-migration
runs alike). Existing runs are never re-encrypted: a recipient added later
cannot read older runs, and a removed recipient keeps the runs written
while it was configured. The appliance identity always remains a recipient
and its removal is refused (409), so a run never has zero recipients.
Recipients live in SQLite (`backup_recipients`, migration 30) and render to
`backup-recipients.yaml` beside `desired.yaml` (public keys only, ADR-003).
Audit entries carry fingerprints and public keys only.

Audit trail safety (S269): callers record names and ids, never values.
As a best-effort net, `audit.Service.Record` also redacts known credential
shapes (`key=value` pairs for password/secret/token/api-key/private-key/
credential/authorization/cookie names, Bearer/Basic values, `AGE-SECRET-KEY-`
identities, JWT-like tunnel tokens) from Actor (the attempted username of a failed login), Detail, and OverrideReason, and
sensitive-named keys in Plan JSON (`internal/audit/redact.go`). It does not
catch arbitrary secrets, so the dashboard Audit page (`/audit`) renders
every field as plain text. `GET /api/v1/audit` takes only `limit`; the page
filters and pages client-side over the loaded window. Aggregated
`session.boundary.rejected` entries (ADR-020) show their request counts.
The Advanced page (`/advanced`) shows the Dockge link with its assessed
policy (exposure, TLS, alignment, findings) and warns when it is not
local-only.

The recovery identity export (`POST /api/v1/backups/identity/export`,
body `{"confirm": true}`) is explicit, audited, and rate limited (5 per
hour, 429 beyond). It fails closed: the export is recorded and audited
before the key is returned, and if either write fails the key is
withheld (500). The response carries the secret key with
`Cache-Control: no-store`; the daemon stores only fingerprint, actor, and
times (`backup_identity_exports`, migration 31 adds `acknowledged_at`),
never the value, and never logs it. The CLI needs `--out FILE` (new 0600
file, O_EXCL so existing files and symlinks are refused, fsync'd) or
`--stdout`; the dashboard asks for confirmation, then shows the key with
copy and download until acknowledged or dismissed (held only in component
state, never in storage). An export alone does not prove the owner holds
the key: the owner then acknowledges storing it offline
(`POST /api/v1/backups/identity/ack`, `homesteadctl backup identity ack`
or `export --ack`, the dashboard button). Doctor `backup-encryption`
warns until an owner recipient exists or an export was acknowledged, warns
again if the last owner recipient is removed with no acknowledged export,
and reports an unreadable export record as its own warning. Read routes
never create the appliance identity (`appliance_created`/`created` report
false until the first backup or export). Removing the last owner
recipient is allowed; removing the appliance identity is refused (409).
Add and remove run in one transaction, so the 16-recipient cap holds under
concurrency and a double removal returns 404. A recipient/YAML render
failure after a committed change is audited as its own
`backup.recipient.render` error entry, not as a failure of the action.
All JSON request bodies decoded through `decodeBody` are capped at 128 KiB. `filippo.io/age` is BSD-3-Clause (plus
`filippo.io/hpke`, BSD-3-Clause), licenses recorded in
`docs/THIRD_PARTY.md`.
