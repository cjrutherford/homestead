import { Component, computed, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { ApiService, AuditEntry } from '../core/api.service';
import { StateMessage } from '../state/state-message';
import { Loading } from '../ui/loading';

/** Outcomes the daemon records (ok/error/skipped) plus the legacy "warning". */
export const KNOWN_OUTCOMES = ['ok', 'error', 'skipped', 'warning'] as const;
/** Badge class per outcome: colour, glyph and the outcome word. */
const BADGE_FOR_OUTCOME: Record<string, string> = {
  ok: 'badge-ok',
  error: 'badge-fail',
  warning: 'badge-warn',
  skipped: 'badge',
  other: 'badge',
};
export const LIMITS = [100, 250, 500, 1000] as const;
export const PAGE_SIZE = 25;

/** An ADR-020 aggregated rejection, parsed from its detail text. */
export interface Rejection {
  route: string;
  reason: string;
  count: number;
  since: string;
}

const REJECTION = /^rejected (.+) \((.+)\) x(\d+) since (\S+)$/;

/** An entry with its derived display data, computed once per load. */
export interface AuditRow {
  entry: AuditEntry;
  time: number;
  rejection: Rejection | null;
  plan: string;
}

/** Parses YYYY-MM-DD as a local-timezone midnight; NaN when empty/invalid. */
function localDay(value: string): number {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!m) return NaN;
  return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])).getTime();
}

/** Local midnight after the given day (DST-safe), NaN when invalid. */
function localDayEnd(value: string): number {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!m) return NaN;
  return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]) + 1).getTime();
}

type FilterField = 'actor' | 'action' | 'outcome' | 'since' | 'until';

/**
 * Audit log (S269): the append-only trail from GET /api/v1/audit.
 * The API accepts only `limit`, so actor/action/outcome/time filters and
 * paging run client-side over the loaded window. Every field renders as
 * text (never HTML). Entries are not guaranteed secret-free by the API:
 * callers record names rather than values and the daemon redacts known
 * credential shapes (internal/audit/redact.go) on a best-effort basis,
 * so the page treats every field as untrusted text and shows nothing
 * beyond what the entry holds (no tokens or cookies are ever fetched).
 */
@Component({
  selector: 'app-audit',
  imports: [Loading, StateMessage],
  templateUrl: './audit.html',
  styles: [
    '.audit-text { overflow-wrap: anywhere; white-space: pre-wrap; }',
  ],
})
export class AuditLog implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly limits = LIMITS;
  protected readonly outcomes = KNOWN_OUTCOMES;
  protected readonly rows = signal<AuditRow[]>([]);
  private requestId = 0;
  protected readonly loading = signal(true);
  protected readonly error = signal<string | null>(null);
  protected readonly limit = signal<number>(100);
  protected readonly actor = signal('');
  protected readonly action = signal('');
  protected readonly outcome = signal('');
  protected readonly since = signal('');
  protected readonly until = signal('');
  protected readonly page = signal(0);

  protected readonly filtered = computed(() => {
    const actor = this.actor().trim().toLowerCase();
    const action = this.action().trim().toLowerCase();
    const outcome = this.outcome();
    const since = localDay(this.since());
    const until = localDayEnd(this.until());
    return this.rows().filter(({ entry: e, time }) => {
      if (actor && !e.actor.toLowerCase().includes(actor)) return false;
      if (action && !e.action.toLowerCase().includes(action)) return false;
      if (outcome && e.outcome !== outcome) return false;
      if (!Number.isNaN(since) && time < since) return false;
      if (!Number.isNaN(until) && time >= until) return false;
      return true;
    });
  });
  protected readonly pageCount = computed(() =>
    Math.max(1, Math.ceil(this.filtered().length / PAGE_SIZE)),
  );
  protected readonly visible = computed(() => {
    const start = Math.min(this.page(), this.pageCount() - 1) * PAGE_SIZE;
    return this.filtered().slice(start, start + PAGE_SIZE);
  });

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    // Responses from superseded requests are ignored.
    const id = ++this.requestId;
    this.loading.set(true);
    this.error.set(null);
    this.api.auditList(this.limit()).subscribe({
      next: (entries) => {
        if (id !== this.requestId) return;
        this.loading.set(false);
        this.rows.set(
          entries.map((entry) => ({
            entry,
            time: new Date(entry.created_at).getTime(),
            rejection: this.parseRejection(entry),
            plan: entry.plan ? this.formatPlan(entry.plan) : '',
          })),
        );
        this.page.set(0);
      },
      error: (failure: HttpErrorResponse) => {
        if (id !== this.requestId) return;
        this.loading.set(false);
        this.error.set(this.describe(failure));
      },
    });
  }

  setLimit(value: string): void {
    this.limit.set(Number(value) || 100);
    this.load();
  }

  setFilter(field: FilterField, value: string): void {
    this[field].set(value);
    this.page.set(0);
  }

  clearFilters(): void {
    for (const f of ['actor', 'action', 'outcome', 'since', 'until'] as const) {
      this[f].set('');
    }
    this.page.set(0);
  }

  go(delta: number): void {
    this.page.set(
      Math.max(0, Math.min(this.pageCount() - 1, this.page() + delta)),
    );
  }

  protected describe(failure: HttpErrorResponse): string {
    if (failure.status === 0) {
      return "Can't reach the appliance. Check that homestead-server is running.";
    }
    const body = failure.error;
    if (typeof body === 'string' && body.trim()) return body;
    if (body && typeof body.error === 'string') return body.error;
    return failure.message;
  }

  /** CSS class for an outcome; unknown values stay visible with text. */
  outcomeClass(outcome: string): string {
    const known = (KNOWN_OUTCOMES as readonly string[]).includes(outcome);
    const kind = known ? outcome : 'other';
    return `outcome-${kind} ${BADGE_FOR_OUTCOME[kind]}`;
  }

  /** Parses an aggregated session-boundary rejection (ADR-020). */
  protected parseRejection(entry: AuditEntry): Rejection | null {
    if (entry.action !== 'session.boundary.rejected') return null;
    const m = REJECTION.exec(entry.detail);
    if (!m) return null;
    return { route: m[1], reason: m[2], count: Number(m[3]), since: m[4] };
  }

  /** Plan JSON pretty-printed when valid; otherwise the raw text. */
  protected formatPlan(plan: string): string {
    try {
      return JSON.stringify(JSON.parse(plan), null, 2);
    } catch {
      return plan;
    }
  }
}
