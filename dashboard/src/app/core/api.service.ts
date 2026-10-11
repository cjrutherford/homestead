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
  Actions: string[];
}

/** POST /api/v1/catalog/:name/install — started operation. */
export interface OperationStarted {
  id: number;
  detail?: string;
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

/** GET /api/v1/backups/schedules — one persisted schedule record. */
export interface BackupSchedule {
  Package: string;
  IntervalSeconds: number;
  Retain: number;
  Enabled: boolean;
  Actor: string;
  UpdatedAt: string;
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
  ): Observable<OperationStarted> {
    return this.http.post<OperationStarted>(
      `/api/v1/catalog/${name}/configure`,
      { changes },
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

  backupDestination(): Observable<{ destination: string }> {
    return this.http.get<{ destination: string }>('/api/v1/backups/destination');
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
}
