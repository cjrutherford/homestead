# Configuration Parity Matrix (S037; ADR-004)

Every daemon API route must be reachable from both `homesteadctl` and the
dashboard, or carry an explicit `n/a` with a reason. `tests/test_s037_parity.py`
parses the table below and fails the build on drift: a route without a row,
a row without a route, or a claimed command/method that no longer exists.

Cell syntax (machine-checked): comma-separated `` `tokens` `` naming the
CLI subcommand (`"status"`) or dashboard symbol (`health`); exactly `n/a`
for an intentional gap. The Notes column is free text.

| Method | Path | CLI | Dashboard | Notes |
|--------|------|-----|-----------|-------|
| GET | /healthz | `"status"` | `health` |  |
| GET | /version | `"version"` | n/a | Dashboard shows the version via health/identity without fetching it |
| GET | /api/v1/config | `"get"` | `config` |  |
| GET | /api/v1/config/schema | n/a | `configSchema` | CLI takes typed files; schema-driven UI is dashboard-only |
| POST | /api/v1/config/plan | `"plan"` | `planConfig` |  |
| PUT | /api/v1/config | `"apply"` | `applyConfig` |  |
| GET | /api/v1/identity | n/a | `identity` | CLI status reports health only |
| POST | /api/v1/claim | `"claim"` | `claim` |  |
| POST | /api/v1/admins/setup | `"setup"` | `setupAdmin` |  |
| POST | /api/v1/admins/recover | `"recover"` | n/a | Root-only Unix socket has no browser surface by design |
| POST | /api/v1/sessions | `"login"` | `login` |  |
| DELETE | /api/v1/sessions | `"logout"` | `logout` |  |
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
| GET | /api/v1/doctor | `"doctor"` | `doctor` | Diagnostic report on all three surfaces |
| GET | /api/v1/catalog | n/a | `catalogPackages` | Package summaries; CLI install arrives in S168 |
| GET | /api/v1/catalog/{name} | n/a | `catalogPackage` | Manifest detail; CLI install arrives in S168 |
| POST | /api/v1/catalog/{name}/plan | n/a | `planInstall` | Reviewed install plan; CLI install arrives in S168 |
| POST | /api/v1/catalog/{name}/install | n/a | `installPackage` | Approved installation; CLI install arrives in S168 |
| POST | /api/v1/catalog/{name}/configure | n/a | `configurePackage` | Configuration operation; CLI install arrives in S168 |
| GET | / | n/a | `app-root` | The UI itself; the CLI is the other interface. Catch-all also answers HEAD; mux method patterns cannot express both without conflicting, so both rows stand for one Handle |
| HEAD | / | n/a | `app-root` | Same catch-all as GET / |
