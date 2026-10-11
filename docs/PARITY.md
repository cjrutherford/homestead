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
| POST | /api/v1/admins/password | `"passwd"` | n/a | Self-service rotation on CLI; dashboard surface belongs to later slices |
| GET | /api/v1/admins/lockout | `"lockout"` | n/a | Own lockout status on CLI; dashboard surface belongs to later slices |
| GET | /api/v1/wizards/{id} | n/a | `WizardService` | Resume machinery is dashboard-only |
| PUT | /api/v1/wizards/{id} | n/a | `WizardService` | Resume machinery is dashboard-only |
| DELETE | /api/v1/wizards/{id} | n/a | `WizardService` | Resume machinery is dashboard-only |
| GET | /api/v1/firewall/status | `runFirewallStatus` | `firewallStatus` | Firewall setup state plus LAN identification |
| GET | /api/v1/firewall/plan | `runFirewallPlan` | `firewallPlan` | Reviewed diff for the daemon port |
| POST | /api/v1/firewall/apply | `runFirewallApply` | `firewallApply` | Guarded, snapshot-backed commit |
| GET | /api/v1/operations/{id} | `runOperationsGet` | `operation` | Single operation record |
| GET | /api/v1/operations | `runOperationsList` | `operations` | Newest-first records |
| GET | /api/v1/operations/{id}/events | `runOperationsFollow` | `operationEvents` | Poll-backed SSE state stream |
| GET | /api/v1/audit | `runAuditList` | n/a | Append-only trail; no dashboard viewer yet |
| GET | /api/v1/docker/health | `runDockerHealth` | n/a | Engine inventory; stack UI arrives later |
| GET | /api/v1/storage/mounts | `runStorageMounts` | n/a | Discovered mounts for registration; dashboard surface belongs to later slices |
| GET | /api/v1/storage/registrations | `runStorageRegistrations` | n/a | Registered mounts; dashboard surface belongs to later slices |
| POST | /api/v1/storage/registrations | `runStorageRegister` | n/a | Mount approval; dashboard surface belongs to later slices |
| GET | /api/v1/storage/assignments | `runStorageAssignments` | n/a | Class bindings; dashboard surface belongs to later slices |
| POST | /api/v1/storage/assignments | `runStorageAssign` | n/a | Class binding; dashboard surface belongs to later slices |
| GET | /api/v1/doctor | `"doctor"` | `doctor` | Diagnostic report on all three surfaces |
| GET | /api/v1/catalog | `"apps"` | `catalogPackages` | Package summaries on both surfaces |
| GET | /api/v1/catalog/{name} | `"apps"` | `catalogPackage` | Manifest detail on both surfaces |
| POST | /api/v1/catalog/{name}/plan | `"apps"` | `planInstall` | Reviewed install plan on both surfaces |
| POST | /api/v1/catalog/{name}/install | `"apps"` | `installPackage` | Approved installation on both surfaces |
| POST | /api/v1/catalog/{name}/configure | `"apps"` | `configurePackage` | Configuration operation on both surfaces |
| POST | /api/v1/catalog/{name}/uninstall | `"apps"` | n/a | Approved removal on CLI; dashboard surface belongs to later slices |
| POST | /api/v1/edge/deploy | `"edge"` | n/a | Edge deployment on CLI; dashboard surface belongs to later slices |
| POST | /api/v1/edge/routes | `"edge"` | n/a | Route reconciliation on CLI; dashboard surface belongs to later slices |
| GET | /api/v1/exposure/review | `"exposure"` | `exposureReview` | Reviewed plan on CLI and dashboard |
| GET | /api/v1/exposure/approvals | `"exposure"` | `exposureApprovals` | Approval records on CLI and dashboard |
| POST | /api/v1/exposure/approvals | `"exposure"` | `exposureApprove` | Ownership approval on CLI and dashboard |
| POST | /api/v1/exposure/apply | `"exposure"` | `exposureApply` | Firewall and edge application on CLI and dashboard |
| GET | /api/v1/tunnel/status | `"tunnel"` | `tunnelStatus` | Discovery and health on CLI and dashboard |
| POST | /api/v1/tunnel/setup | `"tunnel"` | `tunnelSetup` | Tunnel setup on CLI and dashboard |
| DELETE | /api/v1/tunnel/tunnels/{id} | `"tunnel"` | `tunnelTeardown` | Tunnel teardown on CLI and dashboard |
| GET | /api/v1/backups/destination | `"backup"` | `backupDestination` | Resolved backup root on CLI and dashboard |
| GET | /api/v1/backups/schedules | `"backup"` | `backupSchedules` | Schedule records on CLI and dashboard |
| POST | /api/v1/backups/schedules | `"backup"` | `backupScheduleSave` | Schedule save on CLI and dashboard |
| GET | /api/v1/backups/schedules/{package} | `"backup"` | `backupScheduleGet` | Visible through schedules listing on CLI; per-package inspect on dashboard |
| DELETE | /api/v1/backups/schedules/{package} | `"backup"` | `backupScheduleDelete` | Schedule removal on CLI and dashboard |
| GET | /api/v1/updates | `"updates"` | `updates` | Review rows on both surfaces |
| POST | /api/v1/updates/{name}/apply | `"updates"` | `applyUpdate` | Approved updates on both surfaces |
| GET | /api/v1/dockge | n/a | `dockge` | Expert-interface link with assessed policy on dashboard; no command surface yet |
| GET | /api/v1/catalog/{name}/drift | `"drift"` | `driftReview` | Managed-state review on both surfaces |
| POST | /api/v1/catalog/{name}/drift | `"drift"` | `driftResolve` | Accept or restore choice on both surfaces |
| GET | / | n/a | `app-root` | The UI itself; the CLI is the other interface. Catch-all also answers HEAD; mux method patterns cannot express both without conflicting, so both rows stand for one Handle |
| HEAD | / | n/a | `app-root` | Same catch-all as GET / |
