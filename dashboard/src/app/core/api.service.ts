import { inject, Injectable } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { map, Observable, Subscription } from 'rxjs';
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
  Name: string;
  Image: string;
  Exposes: string;
  /** Container paths the package keeps (S276); empty for stateless apps. */
  Data?: CatalogDataPath[] | null;
}

/** One declared container path: persistent on a storage class, or ephemeral. */
export interface CatalogDataPath {
  Target: string;
  Class: string;
  Subpath: string;
  Database?: boolean;
  Ephemeral?: boolean;
}

/** How a package relates to single sign-on (S203). */
export interface CatalogSSO {
  Class: '' | 'unsupported' | 'optional' | 'recommended' | 'required';
  Protocols?: string[] | null;
  /** S298: the adapter Homestead signs the app in to Authentik through, absent when it has none. */
  oidc?: { adapter: string; service: string; plugin?: PinnedPlugin } | null;
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
  /** Single sign-on posture; an empty class reads as unsupported. */
  SSO?: CatalogSSO | null;
  /** Advisory browser capabilities the app needs (S252). */
  Browser?: BrowserAdvisory[] | null;
  /** Secret-backed container variables the package declares (S266). */
  Secrets?: { Name: string; Immutable: boolean }[];
}

/** The catalog detail exactly as the API sends it: empty lists may be null. */
export type RawCatalogManifest = Omit<CatalogManifest, 'Services' | 'Requirements'> & {
  Services: CatalogService[] | null;
  Requirements: Omit<CatalogManifest['Requirements'], 'Storage' | 'Ports'> & {
    Storage: CatalogManifest['Requirements']['Storage'] | null;
    Ports: number[] | null;
  };
};

/** One non-blocking browser capability warning with its reason (S252). */
export interface BrowserAdvisory {
  Capability: string;
  /** Plain-language name supplied by the API. */
  Label: string;
  Reason: string;
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
  /** A credential meant for the owner, readable with revealAppSecret (S312). */
  reveal?: boolean;
  note?: string;
}

/** POST /api/v1/catalog/:name/secrets/:secret/reveal — the value, audited (S312). */
export interface RevealedSecret {
  name: string;
  value: string;
  note?: string;
}

/** Whether Cloudflare's edge has a certificate for a public name (S312). */
export interface NameCoverage {
  hostname: string;
  covered: boolean;
  /** universal, certificate pack or total tls. */
  by?: string;
  /** False when the token could not read the zone's certificates. */
  checked: boolean;
}

/** POST /api/v1/catalog/:name/plan — reviewed install change set. */
export interface InstallPlan {
  Package: string;
  Requirements: { Passed: boolean };
  Routes: { Host: string; Exposure: string }[];
  Compose: { ComposeYAML: string };
  /** Sections are arrays from the API, but null-safe reads stay cheap. */
  DNS?: { Name: string; Type: string; Value: string }[] | null;
  TLS?: { Host: string; Issuer: string }[] | null;
  Firewall?: { Service: { Name: string } }[] | null;
  Directories?: { Mountpoint: string; Directory?: { Path: string } }[] | null;
  Mounts?: PlannedMount[] | null;
  BrowserAdvisories?: BrowserAdvisory[] | null;
  Notes?: string[] | null;
  Actions: string[];
  /** True when routed services join the shared edge network (install creates it). */
  EdgeNetwork?: boolean;
  /** S298: sign-in with Authentik offered for this install, absent when the app has no adapter. */
  sign_in?: SignInPreview;
  /** S313: binds the install to this plan; install with it answers 409 when the plan changed. */
  digest?: string;
}

/** The install plan's sign-in offer (S298). */
export interface SignInPreview {
  offered: boolean;
  reason?: string;
  plan?: AppSignInPlan;
  /** Apply needs no API token in the request: dashboard sign-in kept one. */
  token_kept: boolean;
}

/** The pinned third-party plugin an adapter installs (S298, Jellyfin only). */
export interface PinnedPlugin {
  name: string;
  version: string;
  url: string;
  sha256: string;
}

/** One app's recorded sign-in with Authentik (S298); never a secret. */
export interface AppSignInRecord {
  package: string;
  adapter: string;
  client_id: string;
  issuer: string;
  redirect_uris: string[];
  plugin_version?: string;
  actor?: string;
  updated_at: string;
}

/** GET /api/v1/catalog/:name/sign-in (S298). */
export interface AppSignInStatus {
  package: string;
  offered: boolean;
  reason?: string;
  enabled: boolean;
  applied?: AppSignInRecord;
  replan: boolean;
  adapter?: string;
  plugin?: PinnedPlugin;
  sign_in_url?: string;
  token_kept: boolean;
  /** A sign-in made before groups: every Authentik user can sign in (S314, ADR-043). */
  open_to_all?: boolean;
}

/** A read-back of the groups and bindings Homestead made in Authentik (S314). */
export interface AccessReport {
  groups: { name: string; exists: boolean; members: number; owner_member: boolean }[];
  bindings: { group: string; bound: boolean }[];
  claim_mapping?: 'ok' | 'missing' | 'differs';
  open: boolean;
  drift: string[];
}

/** POST /api/v1/catalog/:name/sign-in/plan (S298): what approving does, with its digest. */
export interface AppSignInPlan {
  package: string;
  title: string;
  enabled: boolean;
  adapter: string;
  provider: string;
  application: string;
  client_id: string;
  issuer: string;
  redirect_uris: string[];
  app_settings: string[];
  plugin?: PinnedPlugin;
  sign_in_url?: string;
  changes: string[];
  warnings: string[];
  digest: string;
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

/**
 * One desired edge route (S270), PascalCase as the Go docker.Route has
 * no json tags. `Policy` is passed through untouched when present.
 */
export interface EdgeRoute {
  Host: string;
  Target: { Host: string; Port: number };
  Exposure: 'local' | 'overlay' | 'public';
  TLS: 'off' | 'internal' | 'auto';
  Health: { Path: string; IntervalSeconds: number };
  Policy?: unknown;
  /** Craft app this host serves (S254); install plans carry it, pass it through. */
  CraftApp?: string;
  /** The one dashboard route (S289); managed on the Trust page, not the Edge editor. */
  Dashboard?: boolean;
  /** Extra names the dashboard route answers on (the .local name). */
  Aliases?: string[];
}

/** GET /api/v1/dashboard/address (S289, ADR-035): the dashboard's local network address. */
export interface DashboardAddress {
  name: string;
  default: string;
  configured: boolean;
  exposure: 'local' | 'overlay';
  mdns_name: string;
  names: string[];
  /** Full URLs, the .local one first (it needs no DNS setup). */
  urls: string[];
  root_path: string;
}

/** GET /api/v1/craft/files — Nextcloud link state (S254); never the app password. */
export interface CraftFilesApp {
  name: string;
  title: string;
  mode: 'open-save' | 'open-only';
  local_only: boolean;
  host?: string;
  ready: boolean;
}

export interface CraftFilesStatus {
  linked: boolean;
  base_url?: string;
  account?: string;
  root?: string;
  credential_stored: boolean;
  daemon_created: boolean;
  apps: CraftFilesApp[];
}

export interface CraftFilesPlanRequest {
  action: 'link' | 'rotate' | 'unlink';
  base_url?: string;
  account?: string;
  root?: string;
  create_app_password?: boolean;
  allow_insecure_http?: boolean;
}

export interface CraftFilesPlan {
  id: string;
  action: 'link' | 'rotate' | 'unlink';
  base_url: string;
  account: string;
  root: string;
  summary: string[];
  warnings: string[];
  expires_at: string;
}

export interface CraftFilesLaunch {
  url: string;
  app: string;
  expires_at: string;
}

/** GET /api/v1/docker/health — the parts the install wizard reads. */
export interface DockerHealth {
  reachable: boolean;
  version?: string;
  containers?: { names: string[]; state: string }[] | null;
  networks?: { name: string }[] | null;
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
  /** Build state of locally built craft images the plan moves (S279). */
  craft?: CraftUpdate[];
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
  /** Binaries kept before the last replacement (manual restore). */
  previous?: Record<
    string,
    { component: string; version: string; counter: number; sha256: string; path: string }
  >;
  /** An apply awaiting finalization after the daemon restart. */
  pending?: {
    op_id: number;
    component: string;
    from_version: string;
    to_version: string;
  } | null;
}

/** POST /api/v1/platform/updates/apply — the files a release was verified from. */
export interface PlatformApplyBody {
  component: string;
  artifact: string;
  checksums: string;
  signature: string;
  allow_older: boolean;
}

/** POST /api/v1/platform/updates/rollback — restore the kept previous binary. */
export interface PlatformRollbackBody {
  component: string;
  allow_older: boolean;
}

/** POST /api/v1/platform/updates/verify — owner-supplied local files. */
export interface PlatformVerifyBody {
  component: string;
  artifact: string;
  checksums: string;
  signature: string;
  allow_older: boolean;
}

/** GET /api/v1/craft/apps — one installed craft app (S281). */
export interface CraftApp {
  name: string;
  title: string;
  /** One-line purpose from the catalog manifest. */
  blurb?: string;
  version: string;
  status: string;
  /** Empty when no edge route serves the app; url_reason says why. */
  url: string;
  host?: string;
  tls?: string;
  exposure?: string;
  url_reason?: string;
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

/** GET /api/v1/craft — a locally built craft image with its live comparison (S279). */
export interface CraftImage {
  app: string;
  version: string;
  tag: string;
  image_id: string;
  counter: number;
  key_id: string;
  artifact_sha256: string;
  context_digest: string;
  built_at: string;
  present: boolean;
  live_id: string;
  match: boolean;
}

/** Latest verification or build outcome for one craft app. */
export interface CraftVerdict {
  app: string;
  version: string;
  counter: number;
  state: 'verified' | 'built' | 'failed';
  /** The failing check (signature, counter, artifact-digest, ...); empty when none. */
  refusal: string;
  detail: string;
  key_id: string;
  override: boolean;
}

/** GET /api/v1/craft. */
export interface CraftStatus {
  images: CraftImage[];
  verdicts: CraftVerdict[];
  baselines: Record<string, { Version: string; Counter: number }>;
  manifest_path: string;
  signature_path: string;
  context_dir: string;
}

/** POST /api/v1/craft/plan and /build body. */
export interface CraftRequest {
  app: string;
  version?: string;
  manifest?: string;
  signature?: string;
  artifact?: string;
  allow_older?: boolean;
}

/** What a craft plan checked. */
export interface CraftPlan {
  app: string;
  version: string;
  counter: number;
  tag: string;
  key_id: string;
  artifact_name: string;
  artifact_sha256: string;
  artifact_size: number;
  source: 'local' | 'download';
  context_digest: string;
  override: boolean;
  disk_required: number;
  baseline: string;
}

/** POST /api/v1/craft/plan response. */
export interface CraftPlanResult {
  plan: CraftPlan;
  verdict: CraftVerdict;
}

/** An update-plan row's craft build state. */
export interface CraftUpdate {
  service: string;
  app: string;
  version: string;
  tag: string;
  built: boolean;
  verdict?: CraftVerdict;
}

/** GET /api/v1/audit — one immutable audit entry (snake_case JSON). */
export interface AuditEntry {
  id: number;
  actor: string;
  action: string;
  plan: string;
  outcome: string;
  detail: string;
  override: boolean;
  override_reason: string;
  created_at: string;
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
  drifted: {
    package: string;
    files: { path: string; kind: string }[];
    live?: LiveFinding[];
    live_error?: string;
  }[];
  errors: string[];
  live_skipped?: string;
  stopped?: string[];
  live_notes?: string[];
}

/** GET /api/v1/catalog/:name/drift/live — running-container finding (S262). */
export interface LiveFinding {
  service: string;
  container?: string;
  kind: string;
  detail: string;
  resolution: 'restore' | 'recreate' | 'manual';
  hint?: string;
}

export interface LiveDrift {
  checked: boolean;
  findings: LiveFinding[];
  stack_state?: string;
  unsupported?: string[];
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
  /** Added by S293: data disk, system disk path, or never registrable. */
  kind?: 'data' | 'system' | 'unusable';
  /** Added by S293: unassigned classes the daemon proposes for this mount. */
  suggested?: string[];
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
  /** True for a package that keeps no data: a no-op, never scheduled (ADR-028). */
  stateless?: boolean;
}

/** The nightly backup policy (S297, ADR-039 §2). */
export interface BackupPolicy {
  /** HH:MM, appliance local time. */
  time_of_day: string;
  retain: number;
  enabled: boolean;
  actor?: string;
  updated_at?: string;
}

/** An installed app the nightly policy leaves out, and why. */
export interface BackupPolicySkip {
  package: string;
  reason: string;
}

/** GET /api/v1/backups/policy. `policy` is absent until the first approval. */
export interface BackupPolicyStatus {
  policy?: BackupPolicy;
  proposed: BackupPolicy;
  covered: string[];
  skipped: BackupPolicySkip[];
  time_zone: string;
  backup_storage: boolean;
}

/** POST /api/v1/backups/policy/plan: what approving would store. */
export interface BackupPolicyPlan {
  policy: BackupPolicy;
  covered: string[];
  skipped: BackupPolicySkip[];
  time_zone: string;
  changes: string[];
  digest: string;
}

export interface BackupPolicyRequest {
  time_of_day: string;
  retain: number;
  enabled: boolean;
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
  /** Unattended re-issue (S272, ADR-024): consecutive failures and retry. */
  attempts?: number;
  last_attempt_at?: string;
  next_attempt_at?: string;
  last_error?: string;
  /** The approval's renewal choice: auto or manual. */
  renewal_mode?: string;
  /** Renewed certificate placed, edge reload refused. */
  pending_reload?: boolean;
  /** The CA forgot the ACME account; only the owner can re-register it. */
  account_recovery_required?: boolean;
}

/** GET /api/v1/tls/local-ca — the internal CA (public material only). */
export interface LocalCaInfo {
  present: boolean;
  subject?: string;
  serial?: string;
  fingerprint_sha256?: string;
  not_before?: string;
  not_after?: string;
  permitted_dns?: string[];
  generation?: number;
  previous_fingerprint_sha256?: string;
  key_present: boolean;
}

export interface LocalLeaf {
  host: string;
  cert_name: string;
  fingerprint_sha256?: string;
  not_after?: string;
  status: string;
  detail?: string;
}

export interface LocalCaReport {
  ca: LocalCaInfo;
  leaves: LocalLeaf[];
  eligible_routes: number;
}

export interface LocalCaRotationPlan {
  current_fingerprint_sha256: string;
  current_not_after: string;
  generation: number;
  leaves_to_reissue: string[];
  devices_must_retrust: string;
  steps: string[];
  digest: string;
}

/** GET /api/v1/tls/renewal — statuses plus the explicit recorded count. */
export interface TlsRenewalReport {
  recorded_issuances: number;
  /** Says "no recorded issuances ..." explicitly when none are recorded. */
  summary: string;
  certificates: TlsRenewalStatus[];
}

/** GET /api/v1/tls/issuance/plans — one previewed first issuance (S278). */
export interface TlsIssuancePlan {
  certificate: string;
  names: string[];
  provider: string;
  zone: string;
  challenge: string;
  directory: string;
  /** staging | test | production (production needs explicit confirmation). */
  directory_class: string;
  account: { state: string; detail: string; secret: string };
  /** DNS provider credential fields: presence only, never values. */
  credentials: { name: string; label: string; present: boolean }[];
  terms: string;
  placement: { directory: string; chain_file: string; key_file: string; key_mode: string };
  served_routes: string[];
  ready: boolean;
  blockers: string[];
  digest: string;
  approved: boolean;
}

/** GET /api/v1/tls/issuance/account/terms — terms to agree to (S272). */
export interface TlsAccountTerms {
  directory: string;
  directory_class: string;
  current_terms_url: string;
  recorded_terms_url: string;
  changed: boolean;
  /** Production directories need a fresh agreement to re-register. */
  agreement_required: boolean;
}

/** GET /api/v1/tls/issuance/approvals — one recorded approval. */
export interface TlsIssuanceApproval {
  certificate: string;
  digest: string;
  actor: string;
  agreed_terms: boolean;
  confirmed_production: boolean;
  renewal_mode?: string;
  approved_at: string;
  plan: TlsIssuancePlan;
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
  /** The names a tunnel plan would propose (S296); the owner may rename them. */
  proposed: { dashboard_hostname: string; auth_hostname: string; sign_in_linked: boolean };
  discovery_error?: string;
  /** The certificate lamp for each served name (S312). */
  coverage?: NameCoverage[];
}

/** One DNS change in a tunnel plan (ADR-037). */
export interface TunnelPlanChange {
  name: string;
  type: string;
  content: string;
  proxied: boolean;
  /** create, or replace the listed records; keep (adoption), delete and restore (S290). */
  action: 'create' | 'replace' | 'keep' | 'delete' | 'restore';
  replaces: { type: string; name: string; content: string; proxied: boolean }[];
}

/** POST /api/v1/tunnel/plan — what setup would change; never the account or zone IDs. */
export interface TunnelPlan {
  name: string;
  dashboard_hostname: string;
  auth_hostname: string;
  zone: string;
  /** Every public name reaches the edge, which routes it (S296). */
  ingress: { hostname: string; role: string; route: string; service: string }[];
  dns: TunnelPlanChange[];
  /** Names left out of this plan, each with the reason. */
  omitted: { hostname: string; role: string; reason: string }[];
  /** What approving means for Sign-in, when the dashboard is published. */
  sign_in?: string;
  /** Present when the plan adopts an existing tunnel (S290). */
  adopt?: TunnelAdoption;
  /** Whether Cloudflare has a certificate for each public name (S312). */
  coverage?: NameCoverage[];
  warnings: string[];
  digest: string;
}

/** Adopting a tunnel this appliance's connector already serves (S290, ADR-041 §3). */
export interface TunnelAdoption {
  id: string;
  status: string;
  connections: number;
  /** Each entry the connector serves now, and whether it is kept or dropped. */
  existing: { hostname: string; service: string; outcome: string }[];
}

/** The names a tunnel plan is asked for; empty names take the proposal. `adopt` replaces `name`. */
export interface TunnelRequest {
  name?: string;
  adopt?: string;
  dashboard_hostname?: string;
  auth_hostname?: string;
}

/** GET /api/v1/catalog/{name}/public (S290, ADR-041 §1). */
export interface AppPublicStatus {
  app: string;
  available: boolean;
  reason?: string;
  tunnel_id?: string;
  tunnel?: string;
  routes: { route: string; public: string[] }[];
  proposed?: string;
  sign_in: boolean;
  /** The certificate lamp for each public name (S312). */
  coverage?: NameCoverage[];
}

/** One public name added to or removed from an app. */
export interface AppPublicPlan {
  app: string;
  action: 'add' | 'remove';
  hostname: string;
  route: string;
  tunnel: string;
  tunnel_id: string;
  zone: string;
  dns: TunnelPlanChange[];
  connector_before: string[];
  connector_after: string[];
  sign_in?: string;
  coverage?: NameCoverage[];
  warnings: string[];
  /** The app's own host settings the plan changes (S313), such as Nextcloud's trusted domains. */
  app_settings?: string[];
  digest: string;
}

export interface AppPublicRequest {
  enabled: boolean;
  hostname?: string;
  route?: string;
  /** S313: plan a name for the route the install creates; applied without it once installed. */
  before_install?: boolean;
}

/** One address of an app and whether it answers from the appliance (S313). */
export interface ReachAddress {
  address: string;
  host: string;
  kind: 'local' | 'public';
  answers: boolean;
  reason?: string;
  fix?: string;
}

/** GET /api/v1/catalog/{name}/reach: advisory, nothing changes. */
export interface AppReach {
  app: string;
  resolver: boolean;
  addresses: ReachAddress[];
}

/** One app route's local names (S295): `<app>.lan` and its `<app>.local`. */
export interface AppNames {
  host: string;
  names: string[];
}

/** The optional `.lan` resolver (ADR-039). */
export interface ResolverStatus {
  enabled: boolean;
  running: boolean;
  listening: string[];
  error?: string;
  names: string[];
  router_hint: string;
}

/** GET /api/v1/names — app names and the resolver. */
export interface NamesStatus {
  resolver: ResolverStatus;
  apps: AppNames[];
}

/** POST /api/v1/names/resolver/plan — what turning the resolver on or off changes. */
export interface ResolverPlan {
  enabled: boolean;
  changes: string[];
  listen: string[];
  names: string[];
  firewall: { name: string; detail: string }[];
  warnings: string[];
  router_hint: string;
  digest: string;
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
    return this.http.get<RawCatalogManifest>(`/api/v1/catalog/${name}`).pipe(
      // The manifest is serialised straight from Go, so empty lists arrive
      // as null (a stateless craft has Storage and Ports null).
      map((raw) => ({
        ...raw,
        Services: raw.Services ?? [],
        Requirements: {
          ...raw.Requirements,
          Storage: raw.Requirements.Storage ?? [],
          Ports: raw.Requirements.Ports ?? [],
        },
      })),
    );
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

  /** Removes the app; data stays unless remove_data names explicit roots. */
  uninstallPackage(
    name: string,
    body: {
      remove_data?: boolean;
      abandon_volumes?: boolean;
      leave_sign_in?: boolean;
      data_dirs?: { mountpoint: string; path: string }[];
    } = {},
  ): Observable<OperationStarted> {
    return this.http.post<OperationStarted>(`/api/v1/catalog/${name}/uninstall`, body);
  }

  /** Docker engine health and inventory; the install wizard reads edge readiness from it. */
  dockerHealth(): Observable<DockerHealth> {
    return this.http.get<DockerHealth>('/api/v1/docker/health');
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

  /** Reads a credential the package marks readable (S312); audited. */
  revealAppSecret(name: string, secret: string): Observable<RevealedSecret> {
    return this.http.post<RevealedSecret>(`/api/v1/catalog/${name}/secrets/${secret}/reveal`, {
      confirm: true,
    });
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

  platformApply(body: PlatformApplyBody): Observable<OperationStarted> {
    return this.http.post<OperationStarted>(
      '/api/v1/platform/updates/apply',
      body,
    );
  }

  /** POST /api/v1/platform/updates/rollback — manual platform rollback (S273). */
  platformRollback(body: PlatformRollbackBody): Observable<OperationStarted> {
    return this.http.post<OperationStarted>(
      '/api/v1/platform/updates/rollback',
      body,
    );
  }

  /** POST /api/v1/platform/updates/clear-apply — clears a stale apply marker. */
  platformClearApply(): Observable<{
    op_id: number;
    component: string;
    from_version: string;
    to_version: string;
  }> {
    return this.http.post<{
      op_id: number;
      component: string;
      from_version: string;
      to_version: string;
    }>('/api/v1/platform/updates/clear-apply', {});
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

  craftFilesStatus(): Observable<CraftFilesStatus> {
    return this.http.get<CraftFilesStatus>('/api/v1/craft/files');
  }

  craftFilesPlan(body: CraftFilesPlanRequest): Observable<CraftFilesPlan> {
    return this.http.post<CraftFilesPlan>('/api/v1/craft/files/plan', body);
  }

  /** Secrets travel in this request only; the reply never carries them. */
  craftFilesApprove(body: {
    plan_id: string;
    app_password?: string;
    account_password?: string;
  }): Observable<CraftFilesStatus> {
    return this.http.post<CraftFilesStatus>('/api/v1/craft/files/approve', body);
  }

  craftFilesLocalOnly(app: string, localOnly: boolean): Observable<CraftFilesStatus> {
    return this.http.post<CraftFilesStatus>('/api/v1/craft/files/local-only', {
      app,
      local_only: localOnly,
    });
  }

  craftFilesLaunch(app: string): Observable<CraftFilesLaunch> {
    return this.http.post<CraftFilesLaunch>('/api/v1/craft/files/launch', { app });
  }

  /** S281 (ADR-031): installed craft apps with their local URLs. */
  craftApps(): Observable<{ apps: CraftApp[] }> {
    return this.http.get<{ apps: CraftApp[] }>('/api/v1/craft/apps');
  }

  craftStatus(): Observable<CraftStatus> {
    return this.http.get<CraftStatus>('/api/v1/craft');
  }

  craftPlan(body: CraftRequest): Observable<CraftPlanResult> {
    return this.http.post<CraftPlanResult>('/api/v1/craft/plan', body);
  }

  craftBuild(body: CraftRequest): Observable<OperationStarted> {
    return this.http.post<OperationStarted>('/api/v1/craft/build', body);
  }

  craftTrustKeys(): Observable<TrustKey[]> {
    return this.http.get<TrustKey[]>('/api/v1/craft/trust-keys');
  }

  craftTrustKeyAdd(publicKey: string, label: string): Observable<TrustKey> {
    return this.http.post<TrustKey>('/api/v1/craft/trust-keys', {
      public_key: publicKey,
      label,
    });
  }

  craftTrustKeyReplace(
    keyId: string,
    publicKey: string,
    label: string,
  ): Observable<TrustKey> {
    return this.http.put<TrustKey>(
      `/api/v1/craft/trust-keys/${encodeURIComponent(keyId)}`,
      { public_key: publicKey, label },
    );
  }

  craftTrustKeyRemove(keyId: string): Observable<void> {
    return this.http.delete<void>(
      `/api/v1/craft/trust-keys/${encodeURIComponent(keyId)}`,
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

  auditList(limit: number): Observable<AuditEntry[]> {
    return this.http.get<AuditEntry[]>(`/api/v1/audit?limit=${limit}`);
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

  driftLive(name: string): Observable<LiveDrift> {
    return this.http.get<LiveDrift>(`/api/v1/catalog/${name}/drift/live`);
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

  appPublic(name: string): Observable<AppPublicStatus> {
    return this.http.get<AppPublicStatus>(`/api/v1/catalog/${encodeURIComponent(name)}/public`);
  }

  appPublicPlan(name: string, request: AppPublicRequest): Observable<AppPublicPlan> {
    return this.http.post<AppPublicPlan>(`/api/v1/catalog/${encodeURIComponent(name)}/public/plan`, request);
  }

  appPublicApply(name: string, request: AppPublicRequest, digest: string): Observable<OperationStarted> {
    return this.http.post<OperationStarted>(`/api/v1/catalog/${encodeURIComponent(name)}/public/apply`, { ...request, digest });
  }

  /** Checks from the appliance whether each address of the app answers (S313). */
  appReach(name: string): Observable<AppReach> {
    return this.http.get<AppReach>(`/api/v1/catalog/${encodeURIComponent(name)}/reach`);
  }

  appSignIn(name: string): Observable<AppSignInStatus> {
    return this.http.get<AppSignInStatus>(`/api/v1/catalog/${encodeURIComponent(name)}/sign-in`);
  }

  appSignInPlan(name: string, enabled: boolean): Observable<AppSignInPlan> {
    return this.http.post<AppSignInPlan>(`/api/v1/catalog/${encodeURIComponent(name)}/sign-in/plan`, { enabled });
  }

  /** Starts the reviewed change. The token is for this apply only; leave it empty when one is kept. */
  appSignInApply(name: string, enabled: boolean, digest: string, token = ''): Observable<OperationStarted> {
    const body: { enabled: boolean; digest: string; token?: string } = { enabled, digest };
    if (token) {
      body.token = token;
    }
    return this.http.post<OperationStarted>(`/api/v1/catalog/${encodeURIComponent(name)}/sign-in/apply`, body);
  }

  /** Reads back, without changing anything, who can sign in to the app (S314). */
  appSignInAccess(name: string, token = ''): Observable<AccessReport> {
    return this.http.post<AccessReport>(
      `/api/v1/catalog/${encodeURIComponent(name)}/sign-in/access`,
      token ? { token } : {},
    );
  }

  backupPolicy(): Observable<BackupPolicyStatus> {
    return this.http.get<BackupPolicyStatus>('/api/v1/backups/policy');
  }

  backupPolicyPlan(request: BackupPolicyRequest): Observable<BackupPolicyPlan> {
    return this.http.post<BackupPolicyPlan>('/api/v1/backups/policy/plan', request);
  }

  backupPolicyApply(request: BackupPolicyRequest, digest: string): Observable<BackupPolicyStatus> {
    return this.http.post<BackupPolicyStatus>('/api/v1/backups/policy/apply', { ...request, digest });
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

  tlsIssuancePlans(): Observable<{ plans: TlsIssuancePlan[] }> {
    return this.http.get<{ plans: TlsIssuancePlan[] }>('/api/v1/tls/issuance/plans');
  }

  tlsIssuanceApprovals(): Observable<{ approvals: TlsIssuanceApproval[] }> {
    return this.http.get<{ approvals: TlsIssuanceApproval[] }>('/api/v1/tls/issuance/approvals');
  }

  tlsIssuanceApprove(body: {
    certificate: string;
    digest: string;
    agree_terms: boolean;
    confirm_production: boolean;
    /** auto (default) re-issues unattended; manual opts out (S272). */
    renewal?: 'auto' | 'manual';
  }): Observable<TlsIssuanceApproval> {
    return this.http.post<TlsIssuanceApproval>('/api/v1/tls/issuance/approvals', body);
  }

  tlsIssuanceCredentials(
    provider: string,
    values: Record<string, string>,
  ): Observable<{ provider: string; stored: string[]; warning?: string }> {
    return this.http.post<{ provider: string; stored: string[]; warning?: string }>(
      '/api/v1/tls/issuance/credentials',
      { provider, values },
    );
  }

  /** S280 (ADR-030): internal CA status with fingerprint and graded leaves. */
  dashboardAddress(): Observable<DashboardAddress> {
    return this.http.get<DashboardAddress>('/api/v1/dashboard/address');
  }

  /** Sets the local name; public exposure is refused by the daemon (the tunnel is the public path). */
  setDashboardAddress(name: string, exposure: 'local' | 'overlay'): Observable<OperationStarted> {
    return this.http.put<OperationStarted>('/api/v1/dashboard/address', { name, exposure });
  }

  localCaStatus(): Observable<LocalCaReport> {
    return this.http.get<LocalCaReport>('/api/v1/tls/local-ca');
  }

  localCaCreate(): Observable<{ ca: LocalCaInfo; created: boolean }> {
    return this.http.post<{ ca: LocalCaInfo; created: boolean }>('/api/v1/tls/local-ca', {});
  }

  localCaLeaves(): Observable<{ leaves: LocalLeaf[] }> {
    return this.http.get<{ leaves: LocalLeaf[] }>('/api/v1/tls/local-ca/leaves');
  }

  localCaReissue(host = ''): Observable<OperationStarted> {
    return this.http.post<OperationStarted>('/api/v1/tls/local-ca/leaves/reissue', { host });
  }

  localCaRotationPlan(): Observable<LocalCaRotationPlan> {
    return this.http.get<LocalCaRotationPlan>('/api/v1/tls/local-ca/rotation');
  }

  localCaRotate(digest: string): Observable<OperationStarted> {
    return this.http.post<OperationStarted>('/api/v1/tls/local-ca/rotation', { digest });
  }

  /** Public root certificate download URL (unauthenticated by design, ADR-030). */
  localCaRootUrl(format: 'pem' | 'der'): string {
    return `/api/v1/tls/local-ca/root.${format}`;
  }

  tlsReregisterAccount(
    certificate: string,
    agreeTerms = false,
  ): Observable<{ certificate: string; reregistered: boolean }> {
    return this.http.post<{ certificate: string; reregistered: boolean }>(
      '/api/v1/tls/issuance/account/reregister',
      { certificate, agree_terms: agreeTerms },
    );
  }

  tlsAccountTerms(certificate: string): Observable<TlsAccountTerms> {
    return this.http.get<TlsAccountTerms>('/api/v1/tls/issuance/account/terms', {
      params: { certificate },
    });
  }

  tlsIssuanceApply(certificate: string): Observable<OperationStarted> {
    return this.http.post<OperationStarted>('/api/v1/tls/issuance/apply', { certificate });
  }

  tunnelStatus(): Observable<TunnelStatus> {
    return this.http.get<TunnelStatus>('/api/v1/tunnel/status');
  }

  /** Reads Cloudflare and changes nothing (ADR-037). */
  tunnelPlan(request: TunnelRequest): Observable<TunnelPlan> {
    return this.http.post<TunnelPlan>('/api/v1/tunnel/plan', request);
  }

  /** Approves a reviewed plan by its digest; 409 when the plan changed since. */
  tunnelSetup(request: TunnelRequest, digest: string): Observable<OperationStarted> {
    return this.http.post<OperationStarted>('/api/v1/tunnel/setup', { ...request, digest });
  }

  /** The account is the one stored at setup: teardown never asks for it. */
  tunnelTeardown(id: string): Observable<OperationStarted> {
    return this.http.delete<OperationStarted>(`/api/v1/tunnel/tunnels/${encodeURIComponent(id)}`);
  }

  /** Each app's local names and the `.lan` resolver state (S295). */
  names(): Observable<NamesStatus> {
    return this.http.get<NamesStatus>('/api/v1/names');
  }

  /** Previews turning the resolver on or off; changes nothing. */
  resolverPlan(enabled: boolean): Observable<ResolverPlan> {
    return this.http.post<ResolverPlan>('/api/v1/names/resolver/plan', { enabled });
  }

  /** Approves a reviewed resolver plan by its digest; 409 when it changed. */
  resolverApply(enabled: boolean, digest: string): Observable<NamesStatus> {
    return this.http.post<NamesStatus>('/api/v1/names/resolver/apply', { enabled, digest });
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

  /**
   * Whether the login page offers Authentik (S286, ADR-033). Public; answers
   * 404 until sign-in is set up and linked, and `enabled: false` at an
   * address the plan does not cover. `password: false` on the public
   * dashboard name (S296), where only Authentik sign-in is offered.
   */
  ssoOptions(): Observable<{ enabled: boolean; password?: boolean }> {
    return this.http.get<{ enabled: boolean; password?: boolean }>('/api/v1/sso/options');
  }

  /** Own lockout status (S274). */
  lockoutStatus(): Observable<LockoutStatus> {
    return this.http.get<LockoutStatus>('/api/v1/admins/lockout');
  }

  /**
   * Self-service password change (S274). A wrong current password answers
   * 401 with `X-Homestead-Error: invalid-current-password`; the auth
   * interceptor leaves that one alone so the session survives.
   */
  changePassword(currentPassword: string, newPassword: string): Observable<AdminCreated> {
    return this.http.post<AdminCreated>('/api/v1/admins/password', {
      current_password: currentPassword,
      new_password: newPassword,
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
    /** Omit to record the signed-in caller (S293). */
    actor?: string;
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
    /** Omit to record the signed-in caller (S293). */
    actor?: string;
    acknowledge_impact?: boolean;
  }): Observable<StorageAssignment> {
    return this.http.post<StorageAssignment>(
      '/api/v1/storage/assignments',
      body,
    );
  }

  /** Drops a class binding only; the daemon answers 409 naming apps that still use the mount (S288). */
  storageUnassign(cls: string): Observable<void> {
    return this.http.delete<void>(
      `/api/v1/storage/assignments/${encodeURIComponent(cls)}`,
    );
  }

  /** Drops a registration only; never touches the disk (S288). */
  storageUnregister(mountpoint: string): Observable<void> {
    return this.http.delete<void>('/api/v1/storage/registrations', {
      params: { mountpoint },
    });
  }

  edgeDeploy(): Observable<OperationStarted> {
    return this.http.post<OperationStarted>('/api/v1/edge/deploy', {});
  }

  /** The table the reconciler last wrote (not routes placed by exposure). */
  edgeRoutes(): Observable<{ routes: EdgeRoute[] }> {
    return this.http.get<{ routes: EdgeRoute[] }>('/api/v1/edge/routes');
  }

  edgeReconcile(routes: EdgeRoute[]): Observable<OperationStarted> {
    return this.http.post<OperationStarted>('/api/v1/edge/routes', { routes });
  }
}

/** Own lockout status from GET /api/v1/admins/lockout. */
export interface LockoutStatus {
  username: string;
  locked: boolean;
  /** Failed attempts inside the current window. */
  failures?: number;
  /** RFC 3339 unlock time; present only while locked. */
  locked_until?: string;
}
