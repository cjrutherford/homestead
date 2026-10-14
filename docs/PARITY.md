# Configuration Parity Matrix (S037; ADR-004)

Every daemon API route must be reachable from both `homesteadctl` and the
dashboard, or carry an explicit `n/a` with a reason. `tests/test_s037_parity.py`
parses the table below and fails the build on drift: a route without a row,
a row without a route, or a claimed command/method that no longer exists.

Cell syntax (machine-checked): comma-separated `` `tokens` `` naming the
CLI subcommand (`"status"`) or dashboard symbol (`health`); exactly `n/a`
for an intentional gap. The Notes column is free text.

Session requirement (ADR-020, S256): the CLI sends `Authorization: Bearer`
from the session `homesteadctl login` stores (`~/.config/homestead/session`),
and the dashboard uses the HttpOnly session cookie plus `X-CSRF-Token` on
mutating calls. Both resolve to one session store. Every route except the
public allowlist and the claim-window wizard routes requires such a session
(S257 enforces this: 401 without a session, 403 before claim). First-admin
setup (`POST /api/v1/admins/setup`) also logs the owner in, returning the
session fields login returns and setting the cookies; the CLI stores that
session. S257 added or renamed no routes or commands, so the table below is
unchanged.

| Method | Path | CLI | Dashboard | Notes |
|--------|------|-----|-----------|-------|
| GET | /healthz | `"status"` | `health` |  |
| GET | /version | `"version"` | n/a | Dashboard shows the version via health/identity without fetching it |
| GET | /api/v1/config | `"get"` | `config` |  |
| GET | /api/v1/config/schema | n/a | `configSchema` | CLI takes typed files; schema-driven UI is dashboard-only |
| POST | /api/v1/config/plan | `"plan"` | `planConfig` |  |
| PUT | /api/v1/config | `"apply"` | `applyConfig` |  |
| GET | /api/v1/config/export | `"export"` | n/a | Portable document for disaster recovery; dashboard surface belongs to later slices |
| POST | /api/v1/config/import | `"import"` | n/a | Validated restore with per-call secrets; dashboard surface belongs to later slices |
| GET | /api/v1/identity | n/a | `identity` | CLI status reports health only |
| POST | /api/v1/claim | `"claim"` | `claim` |  |
| POST | /api/v1/admins/setup | `"setup"` | `setupAdmin` |  |
| POST | /api/v1/admins/recover | `"recover"` | n/a | Root-only Unix socket has no browser surface by design |
| POST | /api/v1/sessions | `"login"` | `login` |  |
| DELETE | /api/v1/sessions | `"logout"` | `logout` |  |
| POST | /api/v1/admins/password | `"passwd"` | `changePassword` | Account page (`/account`); S274. Same 12-character rule as the daemon; a wrong current password is a 401 with `X-Homestead-Error: invalid-current-password`, which the interceptor does not treat as a dead session; other sessions are signed out, the caller's stays |
| GET | /api/v1/admins/lockout | `"lockout"` | `lockoutStatus` | Account page (`/account`); S274. Reports `locked`, `failures` (current window) and `locked_until` (only while locked); CLI `admin lockout` prints both; Account page shows a relative unlock time |
| GET | /api/v1/wizards/{id} | n/a | `WizardService` | Resume machinery is dashboard-only |
| PUT | /api/v1/wizards/{id} | n/a | `WizardService` | Resume machinery is dashboard-only |
| DELETE | /api/v1/wizards/{id} | n/a | `WizardService` | Resume machinery is dashboard-only |
| GET | /api/v1/firewall/status | `runFirewallStatus` | `firewallStatus` | Firewall setup state plus LAN identification |
| GET | /api/v1/firewall/plan | `runFirewallPlan` | `firewallPlan` | Reviewed diff for the daemon port |
| POST | /api/v1/firewall/apply | `runFirewallApply` | `firewallApply` | Guarded, snapshot-backed commit |
| GET | /api/v1/operations/{id} | `runOperationsGet` | `operation` | Single operation record |
| GET | /api/v1/operations | `runOperationsList` | `operations` | Newest-first records |
| GET | /api/v1/operations/{id}/events | `runOperationsFollow` | `operationEvents` | Poll-backed SSE state stream |
| GET | /api/v1/audit | `runAuditList` | `auditList` | Audit page (`/audit`); S269. API supports only `limit`; actor/action/outcome/time filters and paging are client-side over the loaded window |
| GET | /api/v1/docker/health | `runDockerHealth` | n/a | Engine inventory; stack UI arrives later |
| GET | /api/v1/storage/mounts | `runStorageMounts` | `storageMounts` | Storage page (`/storage`); S268. Entries add `registrable` and `registrable_reason` |
| GET | /api/v1/storage/registrations | `runStorageRegistrations` | `storageRegistrations` | Storage page (`/storage`); S268 |
| POST | /api/v1/storage/registrations | `runStorageRegister` | `storageRegister` | Storage page (`/storage`); S268 |
| GET | /api/v1/storage/assignments | `runStorageAssignments` | `storageAssignments` | Storage page (`/storage`); S268 |
| POST | /api/v1/storage/assignments | `runStorageAssign` | `storageAssign` | Storage page (`/storage`); S268. Moving a class off a mount holding installed packages' data answers 409 with `affected_packages` unless `acknowledge_impact: true` (audited); CLI `--acknowledge-impact` |
| GET | /api/v1/doctor | `"doctor"` | `doctor` | Diagnostic report on all three surfaces |
| GET | /api/v1/catalog | `"apps"` | `catalogPackages` | Package summaries on both surfaces |
| GET | /api/v1/catalog/{name} | `"apps"` | `catalogPackage` | Manifest detail on both surfaces |
| POST | /api/v1/catalog/{name}/plan | `"apps"` | `planInstall` | Reviewed install plan on both surfaces |
| POST | /api/v1/catalog/{name}/install | `"apps"` | `installPackage` | Approved installation on both surfaces |
| POST | /api/v1/catalog/{name}/configure | `"apps"` | `configurePackage` | Configuration operation on both surfaces; also secret rotation (one per operation) and adoption of pre-S266 secret wiring (S266) |
| GET | /api/v1/catalog/{name}/secrets | `"apps"` | `appSecrets` | Declared secret-backed variables, presence only (S266) |
| POST | /api/v1/catalog/{name}/secrets/{secret} | `"apps"` | `registerAppSecret` | Register an existing secret for pre-S266 installs; rotation rides configure (S266) |
| POST | /api/v1/catalog/{name}/uninstall | `"apps"` | n/a | Approved removal on CLI (refuses volume-held data unless `--abandon-volumes`); dashboard surface belongs to later slices |
| POST | /api/v1/edge/deploy | `"edge"` | `edgeDeploy` | Edge page (`/edge`); S270. Confirmed deploy with live operation progress; CLI `edge deploy [--follow]` |
| GET | /api/v1/edge/routes | `"edge"` | `edgeRoutes` | Table the reconciler last wrote; Edge page loads it on open and keeps reconcile disabled until it arrives; CLI `edge routes get` (S270 review) |
| POST | /api/v1/edge/routes | `"edge"` | `edgeReconcile` | Edge page (`/edge`); S270. Confirmed replace of the reconciler's table with the same validation and route-document shape as CLI `edge routes set -f`. Public routes are refused (422) unless they match a current exposure approval; automatic TLS on a private name is refused up front; only hosts the previous reconcile wrote are removed |
| GET | /api/v1/exposure/review | `"exposure"` | `exposureReview` | Reviewed plan on CLI and dashboard |
| GET | /api/v1/exposure/approvals | `"exposure"` | `exposureApprovals` | Approval records on CLI and dashboard |
| POST | /api/v1/exposure/approvals | `"exposure"` | `exposureApprove` | Ownership approval on CLI and dashboard |
| POST | /api/v1/exposure/apply | `"exposure"` | `exposureApply` | Firewall and edge application on CLI and dashboard |
| GET | /api/v1/tunnel/status | `"tunnel"` | `tunnelStatus` | Discovery and health on CLI and dashboard |
| POST | /api/v1/tunnel/setup | `"tunnel"` | `tunnelSetup` | Tunnel setup on CLI and dashboard |
| DELETE | /api/v1/tunnel/tunnels/{id} | `"tunnel"` | `tunnelTeardown` | Tunnel teardown on CLI and dashboard |
| GET | /api/v1/scheduler/jobs | `"scheduler"` | `schedulerJobs` | Scheduled jobs with last run, next due, last error and skip on CLI and the read-only dashboard Backups panel |
| GET | /api/v1/tls/renewal | `"tls"` | `tlsRenewal` | Certificate renewal report with explicit no-recorded-issuance state (S263, reporting only) on CLI (`tls renewal`) and the dashboard Exposure page; also surfaced as doctor checks |
| GET | /api/v1/backups/destination | `"backup"` | `backupDestination` | Resolved backup root plus `encrypted` and `format_version` (S259) on CLI and dashboard |
| GET | /api/v1/backups/schedules | `"backup"` | `backupSchedules` | Schedule records on CLI and dashboard |
| POST | /api/v1/backups/schedules | `"backup"` | `backupScheduleSave` | Schedule save on CLI and dashboard |
| GET | /api/v1/backups/schedules/{package} | `"backup"` | `backupScheduleGet` | Visible through schedules listing on CLI; per-package inspect on dashboard |
| DELETE | /api/v1/backups/schedules/{package} | `"backup"` | `backupScheduleDelete` | Schedule removal on CLI and dashboard |
| GET | /api/v1/backups/recipients | `"backup"` | `backupRecipients` | Appliance and owner age recipients (public keys only) on CLI (`backup recipients list`) and the dashboard Backups page (S271) |
| POST | /api/v1/backups/recipients | `"backup"` | `backupRecipientAdd` | Add an owner X25519 recipient on CLI and dashboard; applies to new runs only (S271) |
| DELETE | /api/v1/backups/recipients/{fingerprint} | `"backup"` | `backupRecipientRemove` | Remove an owner recipient on CLI and dashboard; the appliance identity is refused (S271) |
| GET | /api/v1/backups/identity | `"backup"` | `backupIdentity` | Appliance recipient and export record, never the key, on CLI (`backup identity status`) and dashboard (S271) |
| POST | /api/v1/backups/identity/export | `"backup"` | `backupIdentityExport` | Explicit, audited, rate-limited recovery identity export, shown once: CLI `--out FILE` (0600, fsync'd) or `--stdout`; dashboard confirmation then copy/download (S271) |
| POST | /api/v1/backups/identity/ack | `"backup"` | `backupIdentityAck` | Owner confirms the exported key is stored offline (clears the doctor warning) on CLI (`backup identity ack`, or `export --ack`) and dashboard (S271) |
| GET | /api/v1/updates | `"updates"` | `updates` | Review rows on both surfaces |
| POST | /api/v1/updates/{name}/apply | `"updates"` | `applyUpdate` | Approved updates on both surfaces |
| GET | /api/v1/data-migrations | `runDataStatus` | `dataMigrations` | App data placement (placed, needs-migration, unknown) plus old volumes pending removal (S277); also surfaced as doctor checks |
| POST | /api/v1/data-migrations/{name}/plan | `runDataPlan` | `planDataMigration` | Reviewed migration plan with digest, targets, sizes, space, downtime, backup verdict (S277) |
| POST | /api/v1/data-migrations/{name}/approve | `runDataApprove` | `approveDataMigration` | Approval of the quoted plan digest starts the migration operation; audited (S277) |
| POST | /api/v1/data-migrations/{name}/remove-volumes | `runDataRemoveVolumes` | `removeOldVolumes` | Explicit, audited removal of old volumes, refused while referenced (S277) |
| GET | /api/v1/platform/updates | `"platform"` | `platformUpdates` | Platform plan with per-release verification state on both surfaces (S264) |
| POST | /api/v1/platform/updates/verify | `"verify"` | `platformVerify` | Local artifact verification, `--allow-older` or confirmed rollback override on both surfaces (S264) |
| POST | /api/v1/platform/updates/apply | `"apply-platform"` | `platformApply` | Apply a verified platform release as an operation: the daemon verifies and stages the artifact, checksums and signature, then the privileged apply unit re-verifies and swaps (ADR-027); `updates apply-platform --component --artifact --checksums --signature [--allow-older] [--follow]` and the Updates page apply action behind a confirmation, with a separate confirmation for an older release (S265). Plan view also reports the kept previous binaries and any pending apply |
| POST | /api/v1/platform/updates/clear-apply | `"platform"` | `platformClearApply` | Owner clears a stale apply marker (the apply unit died), audited; `updates platform clear-apply` and the Updates page Clear stale apply button (S265) |
| POST | /api/v1/platform/updates/rollback | `"rollback-platform"` | `platformRollback` | Manual platform rollback to the kept previous binary through the same apply unit, which re-verifies its recorded digest; an older counter needs `--allow-older` or a separate confirmation (audited with both versions, ADR-025); `updates rollback-platform [--component] [--allow-older] [--follow]` and the Updates page rollback action behind a confirmation (S273). A failed post-restart health check also rolls back automatically inside the apply unit. The plan view reports each component's own running version ("unrecorded" for a CLI with no record) |
| GET | /api/v1/platform/trust-keys | `"trust"` | `trustKeys` | Trusted release keys on both surfaces (S264) |
| POST | /api/v1/platform/trust-keys | `"trust"` | `trustKeyAdd` | Add a trusted key on both surfaces (S264) |
| PUT | /api/v1/platform/trust-keys/{id} | `"trust"` | `trustKeyReplace` | Replace a trusted key on both surfaces (S264) |
| DELETE | /api/v1/platform/trust-keys/{id} | `"trust"` | `trustKeyRemove` | Remove a trusted key on both surfaces (S264) |
| GET | /api/v1/secrets/key | `"secrets"` | `secretKeyStatus` | Key version on both surfaces (S258) |
| POST | /api/v1/secrets/rekey | `"secrets"` | `secretRekey` | Confirmed re-key operation on both surfaces (S258) |
| GET | /api/v1/dockge | n/a | `dockge` | Expert-interface link with assessed policy on dashboard; no command surface yet |
| GET | /api/v1/catalog/{name}/drift | `"drift"` | `driftReview` | Managed-state review on both surfaces |
| GET | /api/v1/catalog/{name}/drift/live | `"drift"` | `driftLive` | Read-only running-container comparison on CLI (`drift live`) and the dashboard drift page (S262) |
| POST | /api/v1/catalog/{name}/drift | `"drift"` | `driftResolve` | Accept or restore choice on both surfaces |
| GET | /api/v1/drift/schedule | `"drift"` | `driftSchedule` | Scan cadence and last scheduled scan, including live container findings and skipped-comparison note, on CLI (`drift schedule`) and the dashboard drift page |
| POST | /api/v1/drift/schedule | `"drift"` | `driftScheduleSave` | Scan interval and enable on CLI (`drift schedule --interval/--enabled`) and the dashboard drift page |
| GET | / | n/a | `app-root` | The UI itself; the CLI is the other interface. Catch-all also answers HEAD; mux method patterns cannot express both without conflicting, so both rows stand for one Handle |
| HEAD | / | n/a | `app-root` | Same catch-all as GET / |
