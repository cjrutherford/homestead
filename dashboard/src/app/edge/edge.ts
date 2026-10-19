import { plural } from '../core/plural';
import { Component, computed, inject, OnDestroy, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { Subscription } from 'rxjs';
import { RouterLink } from '@angular/router';
import {
  ApiService,
  EdgeRoute,
  OperationStarted,
  OperationState,
} from '../core/api.service';
import { StateMessage } from '../state/state-message';
import { Dialog } from '../ui/dialog';
import { Loading } from '../ui/loading';
import { OperationProgress } from '../ui/operation-progress';

export const EDGE_EXPOSURES = ['local', 'overlay', 'public'] as const;
export const EDGE_TLS = ['off', 'internal', 'auto'] as const;

/** A route row as typed into the form; numbers stay text until validated. */
interface Draft {
  host: string;
  targetHost: string;
  port: string;
  exposure: EdgeRoute['Exposure'];
  tls: EdgeRoute['TLS'];
  healthPath: string;
  interval: string;
  policy?: unknown;
}

const HOST_LABEL = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
/** Cap on pasted route documents. */
export const MAX_DOCUMENT_BYTES = 256 * 1024;
const MAX_ROUTES = 1000;
const PRIVATE_SUFFIXES = [
  'lan', 'local', 'localhost', 'internal', 'home.arpa', 'test', 'example', 'invalid',
];

/** Mirrors docker.IsPrivateName: names public ACME can never issue for. */
export function isPrivateName(host: string): boolean {
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(host) || host.includes(':')) {
    return true;
  }
  if (!host.includes('.')) {
    return true;
  }
  return PRIVATE_SUFFIXES.some((s) => host === s || host.endsWith('.' + s));
}

const TERMINAL = new Set(['succeeded', 'failed', 'cancelled']);

/**
 * Edge surfaces (S270): Caddy deployment and route-table reconciliation,
 * matching `homesteadctl edge deploy` and `edge routes set`. The daemon
 * has no read route for the table, so the editor submits the complete
 * desired table (declarative replace) and cannot show what is live.
 * Both actions return operation ids; progress streams on this page and
 * links to the operation detail. Public exposure is planned, previewed
 * and approved on the Exposure page (ADR-006), not here.
 */
@Component({
  imports: [OperationProgress, Loading, StateMessage, Dialog, RouterLink],
  selector: 'app-edge',
  templateUrl: './edge.html',
  styles: ['textarea { font-family: var(--font-mono); }'],
})
export class Edge implements OnInit, OnDestroy {
  protected readonly plural = plural;
  private readonly api = inject(ApiService);
  private stream: Subscription | null = null;

  protected readonly exposures = EDGE_EXPOSURES;
  protected readonly tlsModes = EDGE_TLS;
  protected readonly drafts = signal<Draft[]>([]);
  protected readonly busy = signal(false);
  protected readonly loading = signal(true);
  protected readonly loaded = signal(false);
  protected readonly loadError = signal<string | null>(null);
  protected readonly error = signal<string | null>(null);
  protected readonly notice = signal<string | null>(null);
  protected readonly confirming = signal<'deploy' | 'routes' | null>(null);
  /** Operation being followed and its latest streamed state. */
  protected readonly operationId = signal<number | null>(null);
  protected readonly operation = signal<OperationState | null>(null);
  protected readonly streamError = signal<string | null>(null);

  /** Validation messages per route index from the last submit attempt. */
  protected readonly problems = signal<string[]>([]);
  /** Prepared table awaiting confirmation (exactly what will be sent). */
  private pending: EdgeRoute[] = [];

  /** Controls lock while a call or an operation is in flight. */
  protected readonly locked = computed(() => this.busy() || this.running());

  ngOnInit(): void {
    this.loadRoutes();
  }

  ngOnDestroy(): void {
    this.stream?.unsubscribe();
  }

  /** Loads the recorded table; reconcile stays disabled until it arrives. */
  loadRoutes(): void {
    this.loading.set(true);
    this.loadError.set(null);
    this.api.edgeRoutes().subscribe({
      next: ({ routes }) => {
        this.loading.set(false);
        this.loaded.set(true);
        this.drafts.set((routes ?? []).map((r) => this.toDraft(r)));
        this.problems.set([]);
      },
      error: (failure: HttpErrorResponse) => {
        this.loading.set(false);
        this.loadError.set(
          `Could not load the route table: ${this.describe(failure)} Reconciling stays disabled so a partial table is never submitted.`,
        );
      },
    });
  }

  private toDraft(r: EdgeRoute): Draft {
    return {
      host: r.Host ?? '',
      targetHost: r.Target?.Host ?? '',
      port: String(r.Target?.Port ?? ''),
      exposure: r.Exposure ?? 'local',
      tls: r.TLS ?? 'internal',
      healthPath: r.Health?.Path ?? '',
      interval: String(r.Health?.IntervalSeconds ?? 0),
      policy: r.Policy ?? undefined,
    };
  }

  protected describe(failure: HttpErrorResponse): string {
    if (failure.status === 0) {
      return "Can't reach the appliance. Check that homestead-server is running.";
    }
    const body = failure.error;
    if (typeof body === 'string' && body.trim()) {
      return body.trim();
    }
    if (body && typeof body.error === 'string') {
      return body.error;
    }
    return failure.message;
  }

  addRoute(): void {
    this.drafts.update((d) => [
      ...d,
      {
        host: '',
        targetHost: '',
        port: '',
        exposure: 'local',
        tls: 'internal',
        healthPath: '',
        interval: '0',
      },
    ]);
  }

  removeRoute(index: number): void {
    this.drafts.update((d) => d.filter((_, i) => i !== index));
    this.problems.set([]);
  }

  setField(index: number, field: keyof Draft, value: string): void {
    this.drafts.update((d) =>
      d.map((row, i) => (i === index ? { ...row, [field]: value } : row)),
    );
  }

  /** Fills the table from a JSON document shaped like the CLI route file. */
  importJson(text: string): void {
    this.error.set(null);
    this.notice.set(null);
    if (new Blob([text]).size > MAX_DOCUMENT_BYTES) {
      this.error.set('That document is larger than 256 KiB.');
      return;
    }
    let doc: { routes?: unknown };
    try {
      doc = JSON.parse(text);
    } catch {
      this.error.set('That is not valid JSON.');
      return;
    }
    if (!doc || typeof doc !== 'object' || !Array.isArray(doc.routes)) {
      this.error.set('The document needs a "routes" list.');
      return;
    }
    if (doc.routes.length > MAX_ROUTES) {
      this.error.set(`The document has more than ${MAX_ROUTES} routes.`);
      return;
    }
    const bad = doc.routes
      .map((r, i) => this.schemaProblem(r, i))
      .find((message) => message !== null);
    if (bad) {
      this.error.set(bad);
      return;
    }
    this.drafts.set((doc.routes as EdgeRoute[]).map((r) => this.toDraft(r)));
    this.notice.set(`Loaded ${plural(doc.routes.length, 'route')}. Review them, then reconcile.`);
  }

  /** Shape check for one imported item; value rules run at submit. */
  private schemaProblem(r: unknown, i: number): string | null {
    const who = `Route ${i + 1}`;
    const o = r as Record<string, any> | null;
    if (!o || typeof o !== 'object' || Array.isArray(o)) {
      return `${who} must be an object.`;
    }
    if (typeof o['Host'] !== 'string') {
      return `${who}: Host must be a string.`;
    }
    const t = o['Target'];
    if (!t || typeof t !== 'object' || typeof t.Host !== 'string' || typeof t.Port !== 'number') {
      return `${who}: Target needs a string Host and a numeric Port.`;
    }
    if (!(EDGE_EXPOSURES as readonly string[]).includes(o['Exposure'])) {
      return `${who}: Exposure must be local, overlay or public.`;
    }
    if (!(EDGE_TLS as readonly string[]).includes(o['TLS'])) {
      return `${who}: TLS must be off, internal or auto.`;
    }
    const h = o['Health'];
    if (h !== undefined && h !== null) {
      if (typeof h !== 'object' || (h.Path !== undefined && typeof h.Path !== 'string') ||
          (h.IntervalSeconds !== undefined && typeof h.IntervalSeconds !== 'number')) {
        return `${who}: Health needs a string Path and a numeric IntervalSeconds.`;
      }
    }
    return null;
  }

  /** Mirrors the daemon's route validation so mistakes show before a call. */
  private validate(row: Draft, n: number): { route?: EdgeRoute; errors: string[] } {
    const errors: string[] = [];
    const who = `Route ${n + 1}`;
    const host = row.host.trim();
    if (!host || host.length > 253 || host !== host.toLowerCase()) {
      errors.push(`${who}: host must be 1-253 lowercase characters.`);
    } else if (
      host.startsWith('.') ||
      host.endsWith('.') ||
      host.includes('..') ||
      host.split('.').some((l) => !HOST_LABEL.test(l))
    ) {
      errors.push(`${who}: host has an invalid label.`);
    }
    const target = row.targetHost.trim();
    if (!target || target.length > 253 || /[\s/]/.test(target) || target.includes('://')) {
      errors.push(`${who}: target must be a host name or address, not a URL.`);
    }
    const port = Number(row.port);
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      errors.push(`${who}: port must be 1-65535.`);
    }
    const path = row.healthPath.trim();
    if (path && !path.startsWith('/')) {
      errors.push(`${who}: health path must start with /.`);
    }
    const interval = Number(row.interval === '' ? 0 : row.interval);
    if (!Number.isInteger(interval) || (interval !== 0 && (interval < 5 || interval > 300))) {
      errors.push(`${who}: health interval must be 0 or 5-300 seconds.`);
    }
    if (!errors.length && row.tls === 'auto' && isPrivateName(host)) {
      errors.push(
        `${who}: automatic TLS needs a public name, ${host} is private; use internal.`,
      );
    }
    if (errors.length) {
      return { errors };
    }
    const route: EdgeRoute = {
      Host: host,
      Target: { Host: target, Port: port },
      Exposure: row.exposure,
      TLS: row.tls,
      Health: { Path: path, IntervalSeconds: interval },
    };
    if (row.policy !== undefined) {
      route.Policy = row.policy;
    }
    return { route, errors };
  }

  /** Public routes in the prepared table (drives the extra warning). */
  protected publicHosts(): string[] {
    return this.pending.filter((r) => r.Exposure === 'public').map((r) => r.Host);
  }

  /** Public hosts in the table as it stands now, before validation (the preview beside the button). */
  protected draftPublicHosts(): string[] {
    return this.drafts()
      .filter((row) => row.exposure === 'public')
      .map((row) => row.host.trim())
      .filter((host) => host !== '');
  }

  protected pendingCount(): number {
    return this.pending.length;
  }

  askDeploy(): void {
    this.error.set(null);
    this.confirming.set('deploy');
  }

  askRoutes(): void {
    this.error.set(null);
    this.notice.set(null);
    const errors: string[] = [];
    const routes: EdgeRoute[] = [];
    this.drafts().forEach((row, i) => {
      const result = this.validate(row, i);
      errors.push(...result.errors);
      if (result.route) {
        routes.push(result.route);
      }
    });
    const hosts = routes.map((r) => r.Host);
    for (const h of new Set(hosts.filter((x, i) => hosts.indexOf(x) !== i))) {
      errors.push(`Host ${h} appears more than once.`);
    }
    this.problems.set(errors);
    if (errors.length) {
      return;
    }
    this.pending = routes;
    this.confirming.set('routes');
  }

  cancel(): void {
    this.confirming.set(null);
  }

  confirm(): void {
    const which = this.confirming();
    if (!which) {
      return;
    }
    this.confirming.set(null);
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.operationId.set(null);
    this.operation.set(null);
    this.streamError.set(null);
    const call =
      which === 'deploy' ? this.api.edgeDeploy() : this.api.edgeReconcile(this.pending);
    call.subscribe({
      next: (started: OperationStarted) => {
        this.busy.set(false);
        this.follow(started.id);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(this.describe(failure));
      },
    });
  }

  private follow(id: number): void {
    this.stream?.unsubscribe();
    this.operationId.set(id);
    this.stream = this.api.operationEvents(id).subscribe({
      next: (state) => {
        this.operation.set(state);
        if (TERMINAL.has(state.status)) {
          // The table on record changed (or rolled back): show it.
          this.loadRoutes();
        }
      },
      error: () =>
        this.streamError.set(
          'Lost the live progress stream. Open the operation to see its state.',
        ),
    });
  }

  protected running(): boolean {
    const op = this.operation();
    return this.operationId() !== null && !this.streamError() && !(op && TERMINAL.has(op.status));
  }
}
