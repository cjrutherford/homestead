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
| GET | /api/v1/docker/health | `runDockerHealth` | `dockerHealth` | Engine inventory; the install wizard's requirements step (S285) reads Docker reachability, the `homestead-caddy` container and the `homestead-edge` network from it |
| GET | /api/v1/storage/mounts | `runStorageMounts` | `storageMounts` | Storage page (`/storage`); S268. Entries add `registrable` and `registrable_reason` |
| GET | /api/v1/storage/registrations | `runStorageRegistrations` | `storageRegistrations` | Storage page (`/storage`); S268 |
| POST | /api/v1/storage/registrations | `runStorageRegister` | `storageRegister` | Storage page (`/storage`); S268 |
| GET | /api/v1/storage/assignments | `runStorageAssignments` | `storageAssignments` | Storage page (`/storage`); S268 |
| POST | /api/v1/storage/assignments | `runStorageAssign` | `storageAssign` | Storage page (`/storage`); S268. Moving a class off a mount holding installed packages' data answers 409 with `affected_packages` unless `acknowledge_impact: true` (audited); CLI `--acknowledge-impact` |
| GET | /api/v1/doctor | `"doctor"` | `doctor` | Diagnostic report on all three surfaces |
| GET | /api/v1/catalog | `"apps"` | `catalogPackages` | Package summaries on both surfaces |
| GET | /api/v1/catalog/{name} | `"apps"` | `catalogPackage` | Manifest detail on both surfaces; the optional `Browser` list (advisory capabilities with reasons, S252) shows on the dashboard package page |
| POST | /api/v1/catalog/{name}/plan | `"apps"` | `planInstall` | Reviewed install plan on both surfaces; `BrowserAdvisories` (non-blocking, S252) print as `browser:` lines on the CLI and as the advisory panel on the dashboard |
| POST | /api/v1/catalog/{name}/install | `"apps"` | `installPackage` | Approved installation on both surfaces |
| POST | /api/v1/catalog/{name}/configure | `"apps"` | `configurePackage` | Configuration operation on both surfaces; also secret rotation (one per operation) and adoption of pre-S266 secret wiring (S266) |
| GET | /api/v1/catalog/{name}/secrets | `"apps"` | `appSecrets` | Declared secret-backed variables, presence only (S266) |
| POST | /api/v1/catalog/{name}/secrets/{secret} | `"apps"` | `registerAppSecret` | Register an existing secret for pre-S266 installs; rotation rides configure (S266) |
| POST | /api/v1/catalog/{name}/uninstall | `"apps"` | `uninstallPackage` | Approved removal on both surfaces (S285). CLI `apps uninstall` (`--remove-data --data-dir MOUNT:PATH`, `--abandon-volumes`); the dashboard asks in a danger dialog on the catalog list and the wizard's done page, names every data root in full before "Also delete its data", offers the abandon-volumes acknowledgement only after a 409 or 502, and follows the operation in the work-orders rail |
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
| GET | /api/v1/scheduler/jobs | `"scheduler"` | `schedulerJobs` | Scheduled jobs with last run, next due, last error and skip on CLI and the read-only dashboard Backups panel. A stateless package (no persistent data, ADR-028) never has a job: its absence is the no-op (S249) |
| GET | /api/v1/tls/renewal | `"tls"` | `tlsRenewal` | Certificate renewal report with explicit no-recorded-issuance state (S263, reporting only) on CLI (`tls renewal`) and the dashboard Exposure page; also surfaced as doctor checks |
| GET | /api/v1/tls/issuance/plans | `"tls"` | `tlsIssuancePlans` | Previewed first issuance per ACME dns-01 certificate (S278): names, provider, zone, CA directory and class, account state, terms, chain/key placement, routes that will serve it, blockers, digest. Pure preview, no CA contact; CLI `tls issuance plan [CERT]`, dashboard Certificates page (`/tls`) |
| GET | /api/v1/tls/issuance/approvals | `"tls"` | `tlsIssuanceApprovals` | Recorded issuance approvals (S278); CLI `tls issuance approvals` |
| POST | /api/v1/tls/issuance/approvals | `"tls"` | `tlsIssuanceApprove` | Owner approval of one reviewed plan digest (S278, ADR-006): needs terms agreement, and explicit production confirmation for production CAs; audited; CLI `tls issuance approve CERT --digest D --agree-terms [--confirm-production] [--renewal auto or manual]` (renewal mode: auto default, the owner's recorded choice for unattended re-issue, S272; dashboard checkbox) |
| POST | /api/v1/tls/issuance/apply | `"tls"` | `tlsIssuanceApply` | Runs the approved issuance as an operation (S278): issue through DNS-01, place chain and key (0600), refresh covered route snippets, reload Caddy; refused with 409 without a current approval; CLI `tls issuance apply CERT [--follow]` |
| POST | /api/v1/tls/issuance/account/reregister | `"tls"` | `tlsReregisterAccount` | Owner action (S272, ADR-024) when the CA forgot the ACME account: generates a new account key, registers it with the approved directory, then replaces the stored one; required for production directories (test and staging recover automatically), which also need `agree_terms` in the request (fresh terms acknowledgement); audited; CLI `tls issuance reregister-account CERT [--agree-terms]`, dashboard "Re-register ACME account" button with terms checkbox on the Certificates page |
| GET | /api/v1/tls/issuance/account/terms | `"tls"` | `tlsAccountTerms` | The approved CA's current terms-of-service URL (one directory GET) beside the one recorded at registration, and whether a production re-registration needs agreement (S272); shown by the CLI before re-registering and on the Certificates page |
| POST | /api/v1/tls/issuance/credentials | `"tls"` | `tlsIssuanceCredentials` | Write-only storage of a DNS provider's credentials in the secret store (S278; no earlier surface stored them). Values never echoed, logged, or audited (field names only); CLI `tls issuance credentials PROVIDER NAME=@FILE` (or `NAME=-` for stdin), dashboard password fields on the Certificates page |
| GET | /api/v1/tls/local-ca | `"local"` | `localCaStatus` | Internal CA status and graded local leaves with SHA-256 fingerprint, serial, validity (S280, ADR-030); CLI `tls local status` and `tls root show`, dashboard Trust page (`/trust`); also surfaced as doctor checks (`local-ca`, `local-leaf:HOST`) |
| POST | /api/v1/tls/local-ca | `"local"` | `localCaCreate` | Idempotent creation of the persisted internal CA (S280); key custody-sealed in the secret store, never in YAML, audit, API or export; audited; CLI `tls local create`, Trust page button |
| GET | /api/v1/tls/local-ca/root.pem | `"root"` | `localCaRootUrl` | PUBLIC root certificate, PEM (S280). The one read-only unauthenticated exception (ADR-030) so a device can fetch it; local/overlay listener only, absent from public exposure; CLI `tls root export`, Trust page download |
| GET | /api/v1/tls/local-ca/root.der | `"root"` | `localCaRootUrl` | PUBLIC root certificate, DER (S280); same exception and surfaces as root.pem |
| GET | /api/v1/tls/local-ca/leaves | `"local"` | `localCaLeaves` | Local leaf certificates and their status (S280); CLI `tls local leaves`, Trust page table |
| POST | /api/v1/tls/local-ca/leaves/reissue | `"local"` | `localCaReissue` | Forced re-issue of one host or all leaves as an operation (S280); audited; CLI `tls local reissue [HOST]`, Trust page buttons |
| GET | /api/v1/tls/local-ca/rotation | `"local"` | `localCaRotationPlan` | Root rotation plan with digest, leaves re-issued and the devices that must re-trust (S280); CLI `tls local rotation`, Trust page |
| POST | /api/v1/tls/local-ca/rotation | `"local"` | `localCaRotate` | Approve a reviewed rotation digest (S280); 409 when the digest is stale; audited; CLI `tls local rotate --digest D`, Trust page |
| GET | /api/v1/backups/destination | `"backup"` | `backupDestination` | Resolved backup root plus `encrypted` and `format_version` (S259) on CLI and dashboard |
| GET | /api/v1/backups/schedules | `"backup"` | `backupSchedules` | Schedule records on CLI and dashboard. A schedule for a stateless package is flagged `stateless` (a no-op) on both (S249) |
| POST | /api/v1/backups/schedules | `"backup"` | `backupScheduleSave` | Schedule save on CLI and dashboard. A stateless package (ADR-028, for example a craft app) is refused with 422 naming it, shown as the error on both (S249) |
| GET | /api/v1/backups/schedules/{package} | `"backup"` | `backupScheduleGet` | Visible through schedules listing on CLI; per-package inspect on dashboard. A stateless package answers 200 with `stateless: true` instead of 404, shown as a no-op on the dashboard (S249) |
| DELETE | /api/v1/backups/schedules/{package} | `"backup"` | `backupScheduleDelete` | Schedule removal on CLI and dashboard |
| GET | /api/v1/backups/recipients | `"backup"` | `backupRecipients` | Appliance and owner age recipients (public keys only) on CLI (`backup recipients list`) and the dashboard Backups page (S271) |
| POST | /api/v1/backups/recipients | `"backup"` | `backupRecipientAdd` | Add an owner X25519 recipient on CLI and dashboard; applies to new runs only (S271) |
| DELETE | /api/v1/backups/recipients/{fingerprint} | `"backup"` | `backupRecipientRemove` | Remove an owner recipient on CLI and dashboard; the appliance identity is refused (S271) |
| GET | /api/v1/backups/identity | `"backup"` | `backupIdentity` | Appliance recipient and export record, never the key, on CLI (`backup identity status`) and dashboard (S271) |
| POST | /api/v1/backups/identity/export | `"backup"` | `backupIdentityExport` | Explicit, audited, rate-limited recovery identity export, shown once: CLI `--out FILE` (0600, fsync'd) or `--stdout`; dashboard confirmation then copy/download (S271) |
| POST | /api/v1/backups/identity/ack | `"backup"` | `backupIdentityAck` | Owner confirms the exported key is stored offline (clears the doctor warning) on CLI (`backup identity ack`, or `export --ack`) and dashboard (S271) |
| GET | /api/v1/updates | `"updates"` | `updates` | Review rows on both surfaces; rows moving a locally built craft image also report its build state and last verdict (S279) |
| POST | /api/v1/updates/{name}/apply | `"updates"` | `applyUpdate` | Approved updates on both surfaces. A pre-update backup of a stateless package is skipped with a recorded note (the reply's `detail`, printed by the CLI as `note:` and shown as the Updates page notice, and an `update.backup.skipped` audit entry; S249). A craft image move verifies the manifest and rebuilds first (S279); `--craft-manifest/--craft-signature/--craft-artifact/--craft-allow-older` and the Updates page craft inputs carry the optional manifest, offline artifact and audited override |
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
| GET | /api/v1/craft/apps | `"craft"` | `craftApps` | Craft apps start page (S281, ADR-031): installed craft packages (catalog craft packages with a stack) with title, version, status and local URL taken from catalog and the recorded edge route table; an app with no route has no URL and a reason; `https` unless the route's TLS is off. Read-only, session class, no external call; `craft apps` and the Craft apps page (`/craft-apps`) |
| GET | /api/v1/craft | `"craft"` | `craftStatus` | Recorded local craft builds with the live image compared to the recorded digest, latest verdicts per app, counter baselines; `craft status` and the Updates page Craft images panel (S279) |
| POST | /api/v1/craft/plan | `"craft"` | `craftPlan` | Verify-only: signature, counter, local artifact, build-context digest, disk space, nothing fetched or built; `craft plan APP [--manifest --signature --artifact --allow-older]` and the panel Verify action (S279) |
| POST | /api/v1/craft/build | `"craft"` | `craftBuild` | Verify-then-build as an operation; refusals name the failing check, only the counter has an audited override (`--allow-older`, a confirmation); `craft build APP [... --follow]` and the panel Verify and build action (S279) |
| GET | /api/v1/craft/trust-keys | `"craft"` | `craftTrustKeys` | Trusted craft signing keys, a class separate from the platform release keys (S279) |
| POST | /api/v1/craft/trust-keys | `"craft"` | `craftTrustKeyAdd` | Add a craft signing key on both surfaces (S279) |
| PUT | /api/v1/craft/trust-keys/{id} | `"craft"` | `craftTrustKeyReplace` | Replace a craft signing key on both surfaces (S279) |
| DELETE | /api/v1/craft/trust-keys/{id} | `"craft"` | `craftTrustKeyRemove` | Remove a craft signing key on both surfaces (S279) |
| GET | /api/v1/craft/files | `"files"` | `craftFilesStatus` | Nextcloud link (never the app password) and per-app file-storage state; `craft files status` and the Craft files page (S254). Session class |
| POST | /api/v1/craft/files/plan | `"files"` | `craftFilesPlan` | Plan a link, rotate or unlink; `craft files link`, `rotate` and `unlink` and the page's guided form (S254). Session class |
| POST | /api/v1/craft/files/approve | `"files"` | `craftFilesApprove` | Approve a plan; the app password (or the one-time account password for daemon-created app passwords) is request-only, sealed in the secret store, never returned, logged, audited or exported; `--yes` and the page's Approve (S254). Session class |
| POST | /api/v1/craft/files/local-only | `"files"` | `craftFilesLocalOnly` | Per-app browser-local opt-out stored as desired config (`craft-files.yaml`); `craft files local-only APP on` or `off` and the page toggle (S254). Session class |
| POST | /api/v1/craft/files/launch | `"files"` | `craftFilesLaunch` | Mint a one-time launch code (returned in a URL fragment) for the craft host's wrapper page; `craft files launch APP` prints the address, the dashboard's Open with Nextcloud files opens it (S254). Session class with CSRF |
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
| GET | /api/v1/sso/options | n/a | `ssoOptions` | PUBLIC browser-protocol route (S286, ADR-033): `{"enabled":bool}` for the login page, 404 before claim and unless sign-in is configured and linked, and `enabled:false` at an address the plan does not cover; read from local state, never probes Authentik. CLI equivalent: `sso status` (session class) |
| GET | /api/v1/sso/login | n/a | `ssoLogin` | PUBLIC browser-protocol route (S286): starts the authorization code flow with PKCE, state and nonce; a browser redirect, so the CLI has no equivalent (CLI login stays local, ADR-033). The login page's "Sign in with Authentik" link |
| GET | /api/v1/sso/callback | n/a | n/a | PUBLIC browser-protocol route (S286): Authentik's return address, 302 to the login page with a session or a fixed error code; no UI surface of its own. `sso link` prints the URL that ends here |
| GET | /api/v1/sso | `"sso"` | `ssoStatus` | Sign-in state, never a secret or token; `sso status` and the Sign-in page (`/sign-in`, S286). Session class |
| POST | /api/v1/sso/plan | `"sso"` | `ssoPlan` | Plan the Authentik provider and application with exact redirect URIs; `sso plan` and the Sign-in page. Step-up for oidc sessions (S286) |
| POST | /api/v1/sso/approve | `"sso"` | `ssoApprove` | Approve the plan by digest; the API token is request-only, sealed, forgotten after apply unless `--keep-token` / the checkbox; `sso approve` and the Sign-in page. Step-up for oidc sessions |
| POST | /api/v1/sso/apply | `"sso"` | `ssoApply` | Apply as an operation (`sso apply [--follow]`; the page follows it in the rail). Step-up for oidc sessions |
| POST | /api/v1/sso/link | `"sso"` | `ssoLink` | Mint the single-use 10 minute link intent after a fresh local password (`X-Homestead-Reauth`); `sso link` prints the URL, the page opens it. Account and Sign-in pages |
| DELETE | /api/v1/sso/link | `"sso"` | `ssoUnlink` | Remove the link and end oidc sessions, after a fresh local password; `sso unlink` and the Sign-in/Account pages |
| DELETE | /api/v1/sso | `"sso"` | `ssoDisable` | Turn sign-in with Authentik off (recovery path): removes configuration, link and sealed secrets, leaves the local owner; `sso disable` and the Sign-in page. Step-up for oidc sessions |

Craft file proxy (S254, ADR-029, ADR-032). The proxy itself is not on the
TCP mux: `POST|GET|DELETE /_homestead/files/session` and the WebDAV subset
under `/_homestead/files/f/` are served only on the dedicated craft socket
behind the edge, and are classified **craft-session** in
`internal/server/boundary.go` (a craft cookie bound to the operator
session). They are browser-protocol routes used by the craft wrapper page,
not configuration routes, so they carry no CLI or dashboard row; every
setting they depend on (link, rotate, unlink, opt-out, limits via the
`craft_files_*` settings on `/api/v1/config`) is in the rows above.
