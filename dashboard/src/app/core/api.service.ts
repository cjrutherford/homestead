import { inject, Injectable } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { Observable, Subscription } from 'rxjs';
import { ConfigField, FieldError } from '../schema/schema';

/** GET /healthz — always available, even UNCLAIMED. */
export interface Health {
  status: string;
  version: string;
}

/** GET /api/v1/identity — claim state plus daemon version. */
export interface Identity {
  state: string;
  version: string;
}

/** GET /api/v1/doctor — diagnostic report check entry. */
export interface DoctorCheck {
  name: string;
  status: string;
  detail: string;
}

/** GET /api/v1/doctor — diagnostic report. */
export interface DoctorReport {
  overall: string;
  checks: DoctorCheck[];
}

/** One operation step (GET /api/v1/operations/:id). */
export interface OperationStep {
  id: number;
  operation_id: number;
  position: number;
  name: string;
  status: string;
  started_at: string;
  finished_at: string;
  error: string;
}

/** One operation record. */
export interface OperationState {
  id: number;
  type: string;
  status: string;
  payload: string;
  result: string;
  error: string;
  created_at: string;
  updated_at: string;
  steps: OperationStep[];
}

const TERMINAL_STATUSES = new Set(['succeeded', 'failed', 'cancelled']);

/** Minimal EventSource surface (injectable for tests; jsdom lacks it). */
export interface EventSourceLike {
  addEventListener(type: string, listener: (event: { data: string }) => void): void;
  close(): void;
}

/** GET /api/v1/firewall/status — setup state plus LAN identification. */
export interface FirewallStatus {
  nft_available: boolean;
  ruleset_accessible: boolean;
  managed: boolean;
  rule_count: number;
  daemon_port: number;
  local_subnets: string[];
  mode: string;
  external_managers: string[];
}

/** One reviewed firewall change. */
export interface FirewallChange {
  action: string;
  rule: string;
  detail: string;
}

/** GET /api/v1/firewall/plan — reviewed diff with the script to apply. */
export interface FirewallPlan {
  in_sync: boolean;
  changes: FirewallChange[];
  script: string;
}

/** GET /api/v1/config — current desired state. */
export interface ConfigState {
  version: number;
  settings: Record<string, unknown>;
}

/** POST /api/v1/config/plan and PUT /api/v1/config outcomes. */
export interface PlanResult {
  valid: boolean;
  effective?: Record<string, unknown>;
  errors?: FieldError[];
}

/** GET /api/v1/catalog — browsable package summaries. */
export interface PackageSummary {
  name: string;
  version: string;
  title: string;
  blurb: string;
  category: string;
  icon: string;
  /** True when a stack already exists; Install is refused (S276). */
  installed?: boolean;
}

/** GET /api/v1/catalog/:name — curated manifest detail. */
export interface CatalogService {
  name: string;
  image: string;
  exposes: string;
}

export interface CatalogManifest {
  Name: string;
  Version: string;
  Title: string;
  Blurb: string;
  Description: string;
  Category: string;
  Services: CatalogService[];
  Requirements: {
    MemoryMB: number;
    Storage: { Class: string; MinBytes: number }[];
    Ports: number[];
  };
  Exposure: {
    Package: string;
    Endpoints: {
      Endpoint: string;
      Recommended: string;
      AllowWider: boolean;
      Warnings: string[];
    }[];
  };
  BackupPaths: string[];
  /** Secret-backed container variables the package declares (S266). */
  Secrets?: { Name: string; Immutable: boolean }[];
}

/** GET /api/v1/catalog/:name/secrets — presence only, never values (S266). */
export interface AppSecret {
  name: string;
  store_name: string;
  present: boolean;
  immutable: boolean;
  services: string[];
  database_password: boolean;
  wired: boolean;
  installed: boolean;
}

/** POST /api/v1/catalog/:name/plan — reviewed install change set. */
export interface InstallPlan {
  Package: string;
  Requirements: { Passed: boolean };
  Routes: { Host: string; Exposure: string }[];
  Compose: { ComposeYAML: string };
  DNS: { Name: string; Type: string; Value: string }[];
  TLS: { Host: string; Issuer: string }[];
  Firewall: { Service: { Name: string } }[];
  Directories: { Mountpoint: string }[];
  Mounts?: PlannedMount[] | null;
  Notes?: string[] | null;
  Actions: string[];
}

/** One resolved container mount in an install plan (S276). */
export interface PlannedMount {
  Service: string;
  Target: string;
  Source: string;
  Class: string;
  Database: boolean;
  Ephemeral: boolean;
  ReadOnly: boolean;
  Declared: boolean;
}

/** GET /api/v1/secrets/key — secret-store key custody status (S258). */
export interface SecretKeyStatus {
  version: number;
  in_progress: boolean;
}

/** POST /api/v1/catalog/:name/install — started operation. */
export interface OperationStarted {
  id: number;
  detail?: string;
}

/** GET /api/v1/data-migrations — one declared path's observed placement. */
export interface DataFinding {
  service: string;
  target: string;
  state: 'placed' | 'needs-migration' | 'unknown';
  kind: string;
  volume?: string;
  reason?: string;
}

/** GET /api/v1/data-migrations — one installed app's placement verdict. */
export interface AppDataStatus {
  package: string;
  state: 'placed' | 'needs-migration' | 'unknown';
  reason?: string;
  findings: DataFinding[];
}

/** GET /api/v1/data-migrations — a volume left behind by a migration. */
export interface OldVolumeRecord {
  volume: string;
  package: string;
  service: string;
  target: string;
  size_bytes: number;
  state: 'pending-removal' | 'removed';
}

/** GET /api/v1/data-migrations — detection plus old volumes. */
export interface DataMigrations {
  apps: AppDataStatus[];
  old_volumes: OldVolumeRecord[];
}

/** POST /api/v1/data-migrations/:name/plan — reviewable migration plan. */
export interface DataMigrationPlan {
  package: string;
  digest: string;
  items: {
    service: string;
    target: string;
    source: string;
    dest: string;
    size_bytes: number;
  }[];
  old_volumes: { name: string; size_bytes: number }[];
  space: {
    mountpoint: string;
    needed_bytes: number;
    free_bytes: number;
    ok: boolean;
  }[];
  estimated_downtime_seconds: number;
  notes: string[];
  backup: { possible: boolean; destination?: string; reason?: string };
}

/** GET /api/v1/updates — per-package review rows. */
export interface ServiceUpdate {
  Service: string;
  Current: string;
  Target: string;
  Source: string;
}

/** GET /api/v1/updates — reviewable change set for one package. */
export interface UpdatePlan {
  Package: string;
  Services: ServiceUpdate[];
  Backup: boolean;
  BackupWhy: string;
  Affected: string[];
  Warnings: string[];
}

/** GET /api/v1/updates — one row of the review list. */
export interface PackageUpdate {
  package: string;
  installed: boolean;
  plan: UpdatePlan;
}

/** GET /api/v1/platform/updates — latest verification record per component. */
export interface PlatformRelease {
  component: string;
  version: string;
  counter: number;
  state: 'verified' | 'unverified' | 'failed';
  refusal: string;
  detail: string;
  key_id: string;
  sha256: string;
  override: boolean;
  plan_error?: string;
}

/** GET /api/v1/platform/updates — platform plan with verification state. */
export interface PlatformUpdates {
  running: Record<string, string>;
  plan: {
    Components: {
      Component: string;
      Current: string;
      Target: string;
      Verification: string;
    }[];
    Backup: boolean;
    BackupWhy: string;
    Warnings: string[];
  };
  releases: PlatformRelease[];
  /** Failed or unverified releases: shown, never planned. */
  refused?: PlatformRelease[];
}

/** POST /api/v1/platform/updates/verify — owner-supplied local files. */
export interface PlatformVerifyBody {
  component: string;
  artifact: string;
  checksums: string;
  signature: string;
  allow_older: boolean;
}

/** GET /api/v1/platform/trust-keys — one trusted release-signing key. */
export interface TrustKey {
  key_id: string;
  public_key: string;
  label: string;
  source: 'embedded' | 'owner';
  /** The embedded key's private half was discarded; it verifies nothing real. */
  placeholder?: boolean;
}

/** GET /api/v1/dockge — expert-interface link with access policy. */
export interface DockgeLink {
  host: string;
  url: string;
  exposure: string;
  tls: string;
  aligned: boolean;
  findings: string[];
  installed: boolean;
}
/** GET /api/v1/catalog/:name/drift — one viewed file with both sides. */
export interface FileDrift {
  path: string;
  kind: string;
  recorded: string;
  current: string;
}

/** POST /api/v1/catalog/:name/drift — accept or restore outcome. */
export interface DriftResolution {
  id?: number;
  accepted?: string[];
  detail?: string;
}

/** GET/POST /api/v1/drift/schedule — periodic scan cadence and latest scan (S261). */
export interface DriftScanResult {
  scanned_at: string;
  scanned: number;
  drifted: { package: string; files: { path: string; kind: string }[] }[];
  errors: string[];
}

export interface DriftSchedule {
  interval_seconds: number;
  enabled: boolean;
  updated_at: string;
  last_scan: DriftScanResult | null;
}

/** GET /api/v1/exposure/review — recomputed plan for the saved selection. */
export interface ExposurePlan {
  Host: string;
  From: string;
  To: string;
  Assessment: {
    Aligned: boolean;
    Recommended: string;
    Findings: { Severity: string; Message: string }[];
  };
  DNS: { Name: string; Type: string; Value: string }[];
  TLS: { Action: string; Name: string; Detail: string };
  Firewall: {
    Service: { Name: string; Protocol: string; Port: number };
    Sources: { Zone: string; CIDR: string }[];
  }[];
  Edge: { Host: string; Exposure: string };
}

/** Exposure approval record. */
export interface ExposureApproval {
  host: string;
  actor: string;
}

/** GET /api/v1/backups/recipients (S271): public data only. */
export interface BackupRecipient {
  fingerprint: string;
  public_key: string;
  label: string;
  kind: 'appliance' | 'owner';
  actor: string;
}

export interface BackupRecipients {
  recipients: BackupRecipient[];
  owner_held: boolean;
  appliance_created: boolean;
  note: string;
}

export interface BackupIdentityStatus {
  created: boolean;
  recipient?: string;
  fingerprint?: string;
  exported: boolean;
  exported_at?: string;
  exported_by?: string;
  acknowledged: boolean;
  acknowledged_at?: string;
}

/** POST /api/v1/backups/identity/export: the only response with the key. */
export interface BackupIdentityExport {
  identity: string;
  recipient: string;
  fingerprint: string;
  exported_at: string;
  actor: string;
}

/**
 * GET /api/v1/storage/mounts — the Go structs carry no json tags, so
 * fields arrive PascalCase.
 */
export interface StorageMount {
  ID: number;
  Device: string;
  Mountpoint: string;
  FSType: string;
  Options: string[] | null;
  ReadOnly: boolean;
  UUID: string;
  Label: string;
  Accessible: boolean;
  TotalBytes: number;
  FreeBytes: number;
  AvailBytes: number;
  /** Added by S268; absent from older daemons. */
  registrable?: boolean;
  registrable_reason?: string;
}

/** GET/POST /api/v1/storage/registrations (PascalCase). */
export interface StorageRegistration {
  Mountpoint: string;
  Label: string;
  Actor: string;
  AddedAt: string;
}

/** GET/POST /api/v1/storage/assignments (PascalCase). */
export interface StorageAssignment {
  Class: string;
  Mountpoint: string;
  Actor: string;
  CreatedAt: string;
}

/** GET /api/v1/backups/schedules — one persisted schedule record. */
export interface BackupSchedule {
  package: string;
  interval_seconds: number;
  /** Runs to keep; 0 keeps every run. */
  retain: number;
  enabled: boolean;
  actor: string;
  updated_at: string;
}

/** GET /api/v1/scheduler/jobs — one declared job with its run state. */
export interface SchedulerJob {
  id: string;
  interval_seconds: number;
  enabled: boolean;
  last_run_at: string;
  last_operation_id: number;
  last_error: string;
  last_skip_at: string;
  last_skip_due: string;
  last_skip_blocking: number;
  next_due: string;
  running: boolean;
  /** Ongoing outage: builds unavailable, window not spent (ADR-026). */
  unavailable_attempts?: number;
  unavailable_since?: string;
}

/** GET /api/v1/tls/renewal — one certificate's renewal status (S263). */
export interface TlsRenewalStatus {
  certificate: string;
  /** ok | renewal-due | plan-required | expired | error | no-recorded-issuance */
  status: string;
  days_remaining: number;
  not_after: string;
  detail: string;
}

/** GET /api/v1/tls/renewal — statuses plus the explicit recorded count. */
export interface TlsRenewalReport {
  recorded_issuances: number;
  /** Says "no recorded issuances ..." explicitly when none are recorded. */
  summary: string;
  certificates: TlsRenewalStatus[];
}

/** GET /api/v1/tunnel/status — discovery, tunnels, and connector. */
export interface TunnelStatus {
  preferred: string;
  gateway: string;
  interface: string;
  protocols: string[];
  tunnels: {
    id: string;
    name: string;
    status: string;
    connections: { Colo: string; Opened: string }[];
  }[];
  routes: { Hostname: string; TunnelID: string; Service: string }[];
  connector: { installed: boolean; active: boolean };
  accounts: number;
  discovery_error?: string;
}

/** GET /api/v1/wizards/:id — saved wizard progress. */
export interface WizardState {
  wizard: string;
  step: string;
  payload: Record<string, unknown>;
  updated_at: string;
}

/** GET /api/v1/config/schema — field metadata for the renderer. */
export interface ConfigSchema {
  fields: ConfigField[];
}

/** POST /api/v1/claim — token redemption result. */
export interface ClaimResult {
  state: string;
}

/** POST /api/v1/admins/setup — created administrator. */
export interface AdminCreated {
  id: number;
  username: string;
  /**
   * Setup also logs the owner in: the response sets the session cookie
   * and carries the session fields login returns (ADR-020, S257).
   */
  expires_at?: string;
}

/**
 * POST /api/v1/sessions — minted session. The browser authenticates with
 * the HttpOnly session cookie the same response sets; `token` is the CLI
 * bearer and is deliberately never kept by the dashboard (ADR-020).
 */
export interface LoginSession {
  token: string;
  csrf_token?: string;
  expires_at: string;
}

/**
 * Typed access to the homestead-server API (S016/S019/S020).
 * Relative URLs: production serves the dashboard from the daemon (S031),
 * development proxies /healthz, /version and /api to it (proxy.conf.json).
 */
@Injectable({ providedIn: 'root' })
export class ApiService {
  private readonly http = inject(HttpClient);

  health(): Observable<Health> {
    return this.http.get<Health>('/healthz');
  }

  identity(): Observable<Identity> {
    return this.http.get<Identity>('/api/v1/identity');
  }

  doctor(): Observable<DoctorReport> {
    return this.http.get<DoctorReport>('/api/v1/doctor');
  }

  firewallStatus(): Observable<FirewallStatus> {
    return this.http.get<FirewallStatus>('/api/v1/firewall/status');
  }

  firewallPlan(): Observable<FirewallPlan> {
    return this.http.get<FirewallPlan>('/api/v1/firewall/plan');
  }

  firewallApply(): Observable<{ applied: boolean }> {
    return this.http.post<{ applied: boolean }>('/api/v1/firewall/apply', {});
  }

  catalogPackages(): Observable<PackageSummary[]> {
    return this.http.get<PackageSummary[]>('/api/v1/catalog');
  }

  catalogPackage(name: string): Observable<CatalogManifest> {
    return this.http.get<CatalogManifest>(`/api/v1/catalog/${name}`);
  }

  planInstall(
    name: string,
    body: Record<string, unknown>,
  ): Observable<InstallPlan> {
    return this.http.post<InstallPlan>(`/api/v1/catalog/${name}/plan`, body);
  }

  installPackage(
    name: string,
    body: Record<string, unknown>,
  ): Observable<OperationStarted> {
    return this.http.post<OperationStarted>(
      `/api/v1/catalog/${name}/install`,
      body,
    );
  }

  configurePackage(
    name: string,
    changes: Record<string, unknown>[],
    rotateSecrets: string[] = [],
  ): Observable<OperationStarted> {
    return this.http.post<OperationStarted>(
      `/api/v1/catalog/${name}/configure`,
      rotateSecrets.length > 0
        ? { changes, rotate_secrets: rotateSecrets }
        : { changes },
    );
  }

  /** Regenerates only the secret wiring of a pre-S266 stack (S266). */
  adoptSecrets(name: string): Observable<OperationStarted> {
    return this.http.post<OperationStarted>(
      `/api/v1/catalog/${name}/configure`,
      { changes: [], adopt_secrets: true },
    );
  }

  appSecrets(name: string): Observable<AppSecret[]> {
    return this.http.get<AppSecret[]>(`/api/v1/catalog/${name}/secrets`);
  }

  /** Registers an existing value for installs that predate S266. */
  registerAppSecret(
    name: string,
    secret: string,
    value: string,
    skipVerify: boolean,
    force = false,
  ): Observable<{ store_name: string }> {
    return this.http.post<{ store_name: string }>(
      `/api/v1/catalog/${name}/secrets/${secret}`,
      { value, skip_verify: skipVerify, force },
    );
  }

  dataMigrations(): Observable<DataMigrations> {
    return this.http.get<DataMigrations>('/api/v1/data-migrations');
  }

  planDataMigration(name: string): Observable<DataMigrationPlan> {
    return this.http.post<DataMigrationPlan>(
      `/api/v1/data-migrations/${name}/plan`,
      {},
    );
  }

  approveDataMigration(
    name: string,
    body: { digest: string; acknowledge_no_backup: boolean },
  ): Observable<OperationStarted> {
    return this.http.post<OperationStarted>(
      `/api/v1/data-migrations/${name}/approve`,
      body,
    );
  }

  removeOldVolumes(
    name: string,
    volumes: string[],
  ): Observable<OperationStarted> {
    return this.http.post<OperationStarted>(
      `/api/v1/data-migrations/${name}/remove-volumes`,
      { volumes },
    );
  }

  updates(): Observable<PackageUpdate[]> {
    return this.http.get<PackageUpdate[]>('/api/v1/updates');
  }

  applyUpdate(
    name: string,
    body: Record<string, unknown>,
  ): Observable<OperationStarted> {
    return this.http.post<OperationStarted>(
      `/api/v1/updates/${name}/apply`,
      body,
    );
  }

  platformUpdates(): Observable<PlatformUpdates> {
    return this.http.get<PlatformUpdates>('/api/v1/platform/updates');
  }

  platformVerify(body: PlatformVerifyBody): Observable<PlatformRelease> {
    return this.http.post<PlatformRelease>(
      '/api/v1/platform/updates/verify',
      body,
    );
  }

  trustKeys(): Observable<TrustKey[]> {
    return this.http.get<TrustKey[]>('/api/v1/platform/trust-keys');
  }

  trustKeyAdd(publicKey: string, label: string): Observable<TrustKey> {
    return this.http.post<TrustKey>('/api/v1/platform/trust-keys', {
      public_key: publicKey,
      label,
    });
  }

  trustKeyReplace(
    keyId: string,
    publicKey: string,
    label: string,
  ): Observable<TrustKey> {
    return this.http.put<TrustKey>(
      `/api/v1/platform/trust-keys/${encodeURIComponent(keyId)}`,
      { public_key: publicKey, label },
    );
  }

  trustKeyRemove(keyId: string): Observable<void> {
    return this.http.delete<void>(
      `/api/v1/platform/trust-keys/${encodeURIComponent(keyId)}`,
    );
  }

  secretKeyStatus(): Observable<SecretKeyStatus> {
    return this.http.get<SecretKeyStatus>('/api/v1/secrets/key');
  }

  secretRekey(): Observable<OperationStarted> {
    return this.http.post<OperationStarted>('/api/v1/secrets/rekey', {
      confirm: true,
    });
  }

  dockge(domain: string): Observable<DockgeLink> {
    return this.http.get<DockgeLink>(
      `/api/v1/dockge?domain=${encodeURIComponent(domain)}`,
    );
  }

  driftReview(name: string): Observable<FileDrift[]> {
    return this.http.get<FileDrift[]>(`/api/v1/catalog/${name}/drift`);
  }

  driftResolve(name: string, action: string): Observable<DriftResolution> {
    return this.http.post<DriftResolution>(`/api/v1/catalog/${name}/drift`, {
      action,
    });
  }

  driftSchedule(): Observable<DriftSchedule> {
    return this.http.get<DriftSchedule>('/api/v1/drift/schedule');
  }

  driftScheduleSave(body: {
    interval_seconds: number;
    enabled: boolean;
  }): Observable<DriftSchedule> {
    return this.http.post<DriftSchedule>('/api/v1/drift/schedule', body);
  }

  exposureReview(): Observable<ExposurePlan> {
    return this.http.get<ExposurePlan>('/api/v1/exposure/review');
  }

  exposureApprovals(): Observable<ExposureApproval[]> {
    return this.http.get<ExposureApproval[]>('/api/v1/exposure/approvals');
  }

  exposureApprove(
    actor: string,
    networks: string[],
  ): Observable<ExposureApproval> {
    return this.http.post<ExposureApproval>('/api/v1/exposure/approvals', {
      actor,
      networks,
    });
  }

  exposureApply(host: string): Observable<OperationStarted> {
    return this.http.post<OperationStarted>('/api/v1/exposure/apply', {
      host,
    });
  }

  backupDestination(): Observable<{
    destination: string;
    encrypted?: boolean;
    format_version?: number;
  }> {
    return this.http.get<{
      destination: string;
      encrypted?: boolean;
      format_version?: number;
    }>('/api/v1/backups/destination');
  }

  backupSchedules(): Observable<BackupSchedule[]> {
    return this.http.get<BackupSchedule[]>('/api/v1/backups/schedules');
  }

  backupScheduleSave(body: {
    package: string;
    interval_seconds: number;
    retain: number;
    enabled: boolean;
    actor: string;
  }): Observable<BackupSchedule> {
    return this.http.post<BackupSchedule>('/api/v1/backups/schedules', body);
  }

  backupScheduleGet(name: string): Observable<BackupSchedule> {
    return this.http.get<BackupSchedule>(
      `/api/v1/backups/schedules/${encodeURIComponent(name)}`,
    );
  }

  backupScheduleDelete(name: string): Observable<void> {
    return this.http.delete<void>(
      `/api/v1/backups/schedules/${encodeURIComponent(name)}`,
    );
  }

  backupRecipients(): Observable<BackupRecipients> {
    return this.http.get<BackupRecipients>('/api/v1/backups/recipients');
  }

  backupRecipientAdd(body: {
    public_key: string;
    label: string;
  }): Observable<BackupRecipient> {
    return this.http.post<BackupRecipient>('/api/v1/backups/recipients', body);
  }

  backupRecipientRemove(fingerprint: string): Observable<BackupRecipient> {
    return this.http.delete<BackupRecipient>(
      `/api/v1/backups/recipients/${encodeURIComponent(fingerprint)}`,
    );
  }

  backupIdentity(): Observable<BackupIdentityStatus> {
    return this.http.get<BackupIdentityStatus>('/api/v1/backups/identity');
  }

  backupIdentityExport(): Observable<BackupIdentityExport> {
    return this.http.post<BackupIdentityExport>(
      '/api/v1/backups/identity/export',
      { confirm: true },
    );
  }

  backupIdentityAck(): Observable<BackupIdentityStatus> {
    return this.http.post<BackupIdentityStatus>(
      '/api/v1/backups/identity/ack',
      {},
    );
  }

  schedulerJobs(): Observable<SchedulerJob[]> {
    return this.http.get<SchedulerJob[]>('/api/v1/scheduler/jobs');
  }

  tlsRenewal(): Observable<TlsRenewalReport> {
    return this.http.get<TlsRenewalReport>('/api/v1/tls/renewal');
  }

  tunnelStatus(): Observable<TunnelStatus> {
    return this.http.get<TunnelStatus>('/api/v1/tunnel/status');
  }

  tunnelSetup(
    name: string,
    hostname: string,
    service: string,
    apiToken: string,
    accountId: string,
  ): Observable<OperationStarted> {
    return this.http.post<OperationStarted>('/api/v1/tunnel/setup', {
      name,
      hostname,
      service,
      api_token: apiToken,
      account_id: accountId,
    });
  }

  tunnelTeardown(id: string, accountId: string): Observable<OperationStarted> {
    return this.http.delete<OperationStarted>(
      `/api/v1/tunnel/tunnels/${encodeURIComponent(id)}?account_id=${encodeURIComponent(accountId)}`,
    );
  }

  operation(id: number): Observable<OperationState> {
    return this.http.get<OperationState>(`/api/v1/operations/${id}`);
  }

  operations(limit = 0): Observable<OperationState[]> {
    const params = limit > 0 ? `?limit=${limit}` : '';
    return this.http.get<OperationState[]>(`/api/v1/operations${params}`);
  }

  operationEvents(
    id: number,
    open: (url: string) => EventSourceLike = (url) => new EventSource(url),
  ): Observable<OperationState> {
    return new Observable((subscriber) => {
      // EventSource cannot set headers; same-origin it sends the
      // HttpOnly session cookie by itself (ADR-020).
      const source = open(`/api/v1/operations/${id}/events`);
      let finished = false;
      let probe: Subscription | undefined;
      source.addEventListener('state', (event) => {
        const state = JSON.parse(event.data) as OperationState;
        subscriber.next(state);
        if (TERMINAL_STATUSES.has(state.status)) {
          // The server closes the stream after a terminal state; do not
          // let EventSource reconnect or report that close as a failure.
          finished = true;
          source.close();
          subscriber.complete();
        }
      });
      // EventSource hides HTTP status codes, so a failed stream could be
      // a revoked session or a network drop. Probe an authenticated
      // route: a 401 goes through the auth interceptor (local session
      // dropped, redirect to /login) and either way the failure is
      // surfaced instead of leaving the stream silently dead.
      source.addEventListener('error', () => {
        if (finished) {
          return;
        }
        finished = true;
        source.close();
        probe = this.http.get('/api/v1/admins/lockout').subscribe({
          next: () => subscriber.error(new Error('operation event stream interrupted')),
          error: (err: unknown) => subscriber.error(err),
        });
      });
      return () => {
        probe?.unsubscribe();
        source.close();
      };
    });
  }

  config(): Observable<ConfigState> {
    return this.http.get<ConfigState>('/api/v1/config');
  }

  configSchema(): Observable<ConfigSchema> {
    return this.http.get<ConfigSchema>('/api/v1/config/schema');
  }

  planConfig(settings: Record<string, unknown>): Observable<PlanResult> {
    return this.http.post<PlanResult>('/api/v1/config/plan', { settings });
  }

  applyConfig(settings: Record<string, unknown>): Observable<ConfigState> {
    return this.http.put<ConfigState>('/api/v1/config', { settings });
  }

  wizardState(id: string): Observable<WizardState> {
    return this.http.get<WizardState>(`/api/v1/wizards/${id}`);
  }

  saveWizardState(
    id: string,
    step: string,
    payload: Record<string, unknown> = {},
  ): Observable<WizardState> {
    return this.http.put<WizardState>(`/api/v1/wizards/${id}`, { step, payload });
  }

  clearWizardState(id: string): Observable<void> {
    return this.http.delete<void>(`/api/v1/wizards/${id}`);
  }

  claim(token: string): Observable<ClaimResult> {
    return this.http.post<ClaimResult>('/api/v1/claim', { token });
  }

  setupAdmin(username: string, password: string): Observable<AdminCreated> {
    return this.http.post<AdminCreated>('/api/v1/admins/setup', {
      username,
      password,
    });
  }

  login(username: string, password: string): Observable<LoginSession> {
    return this.http.post<LoginSession>('/api/v1/sessions', {
      username,
      password,
    });
  }

  /** Revokes the cookie session (the cookie rides along same-origin). */
  logout(): Observable<void> {
    return this.http.delete<void>('/api/v1/sessions');
  }

  storageMounts(): Observable<StorageMount[]> {
    return this.http.get<StorageMount[]>('/api/v1/storage/mounts');
  }

  storageRegistrations(): Observable<StorageRegistration[]> {
    return this.http.get<StorageRegistration[]>(
      '/api/v1/storage/registrations',
    );
  }

  storageRegister(body: {
    mountpoint: string;
    label: string;
    actor: string;
  }): Observable<StorageRegistration> {
    return this.http.post<StorageRegistration>(
      '/api/v1/storage/registrations',
      body,
    );
  }

  storageAssignments(): Observable<StorageAssignment[]> {
    return this.http.get<StorageAssignment[]>('/api/v1/storage/assignments');
  }

  storageAssign(body: {
    class: string;
    mountpoint: string;
    actor: string;
    acknowledge_impact?: boolean;
  }): Observable<StorageAssignment> {
    return this.http.post<StorageAssignment>(
      '/api/v1/storage/assignments',
      body,
    );
  }
}
