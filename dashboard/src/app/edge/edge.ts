import { Component, DestroyRef, inject, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import {
  ApiService,
  EdgeRoute,
  OperationStarted,
  OperationState,
} from '../core/api.service';
import { StateMessage } from '../state/state-message';
import { DialogFocus } from '../storage/dialog-focus';

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
  imports: [StateMessage, DialogFocus, RouterLink],
  selector: 'app-edge',
  templateUrl: './edge.html',
  styles: [
    'button, input, select, textarea { min-height: 44px; }',
    'textarea { width: 100%; box-sizing: border-box; font-family: monospace; }',
    'input, select { max-width: 100%; box-sizing: border-box; }',
    '.edge-route { overflow-wrap: anywhere; }',
  ],
})
export class Edge {
  private readonly api = inject(ApiService);
  private readonly destroyRef = inject(DestroyRef);

  protected readonly exposures = EDGE_EXPOSURES;
  protected readonly tlsModes = EDGE_TLS;
  protected readonly drafts = signal<Draft[]>([]);
  protected readonly busy = signal(false);
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

  protected describe(failure: HttpErrorResponse): string {
    if (failure.status === 0) {
      return 'Cannot reach the Homestead daemon. Check that it is running and try again.';
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
    let doc: { routes?: EdgeRoute[] };
    try {
      doc = JSON.parse(text);
    } catch {
      this.error.set('That is not valid JSON.');
      return;
    }
    if (!doc || !Array.isArray(doc.routes)) {
      this.error.set('The document needs a "routes" list.');
      return;
    }
    this.drafts.set(
      doc.routes.map((r) => ({
        host: r?.Host ?? '',
        targetHost: r?.Target?.Host ?? '',
        port: String(r?.Target?.Port ?? ''),
        exposure: r?.Exposure ?? 'local',
        tls: r?.TLS ?? 'internal',
        healthPath: r?.Health?.Path ?? '',
        interval: String(r?.Health?.IntervalSeconds ?? 0),
        policy: r?.Policy ?? undefined,
      })),
    );
    this.notice.set(`Loaded ${doc.routes.length} route(s). Review them, then reconcile.`);
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
    hosts.forEach((h, i) => {
      if (hosts.indexOf(h) !== i) {
        errors.push(`Host ${h} appears more than once.`);
      }
    });
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
        this.notice.set(
          which === 'deploy'
            ? 'Caddy deployment started.'
            : `Route reconciliation started for ${this.pending.length} route(s).`,
        );
        this.follow(started.id);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(this.describe(failure));
      },
    });
  }

  private follow(id: number): void {
    this.operationId.set(id);
    this.api
      .operationEvents(id)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (state) => this.operation.set(state),
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
