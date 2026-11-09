import { WhenPipe } from '../core/format-time';
import { Component, computed, effect, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { ApiService, OperationState } from '../core/api.service';
import { httpErrorMessage } from '../core/http-error';
import { labelForType } from '../core/operation-labels';
import { WorkOrders } from '../core/work-orders.service';
import { StateMessage } from '../state/state-message';
import { ariaSort, nextSort, SortState, sortRows } from '../core/sort';
import { Loading } from '../ui/loading';
const BADGES: Record<string, string> = {
  succeeded: 'badge-ok',
  failed: 'badge-fail',
  running: 'badge-running',
  pending: 'badge-pending',
};
const WORDS: Record<string, string> = {
  succeeded: 'Done',
  failed: 'Failed',
  running: 'Running',
  pending: 'Waiting',
  cancelled: 'Cancelled',
};

/** Operations list page (S064, S283): newest-first records linking to detail. */
@Component({
  imports: [StateMessage, RouterLink, Loading, WhenPipe],
  selector: 'app-operations',
  templateUrl: './operations.html',
})
export class Operations implements OnInit {
  private readonly api = inject(ApiService);
  private readonly workOrders = inject(WorkOrders);

  protected readonly operations = signal<OperationState[] | null>(null);
  protected readonly error = signal<string | null>(null);

  protected readonly sort = signal<SortState | null>(null);
  protected readonly rows = computed(() =>
    sortRows(this.operations() ?? [], this.sort(), (op, key) =>
      key === 'type' ? this.labelOf(op.type) : key === 'status' ? this.wordOf(op.status) : (op.created_at ?? ''),
    ),
  );

  /** Order ids and whether each has settled; a change means the list is stale. */
  private seen: string | null = null;

  constructor() {
    // An operation that starts or finishes while this page is open updates
    // its row, so the list never says "Running" for work the rail shows done.
    effect(() => {
      const key = this.workOrders
        .orders()
        .map((order) => `${order.id}:${order.settled}`)
        .join(',');
      const first = this.seen === null;
      this.seen = key;
      if (!first) {
        this.load();
      }
    });
  }

  ngOnInit(): void {
    this.load();
  }

  protected load(): void {
    this.error.set(null);
    this.api.operations().subscribe({
      next: (operations) => this.operations.set(operations),
      error: (err: HttpErrorResponse) =>
        this.error.set(httpErrorMessage(err, 'Loading operations')),
    });
  }

  protected sortBy(key: string): void {
    this.sort.set(nextSort(this.sort(), key));
  }

  protected ariaFor(key: string): 'ascending' | 'descending' | 'none' {
    return ariaSort(this.sort(), key);
  }

  protected dirFor(key: string): string {
    return this.sort()?.key === key ? this.sort()!.dir : '';
  }

  protected stepsOf(op: OperationState): string {
    const steps = op.steps ?? [];
    const done = steps.filter((step) => step.status === 'succeeded').length;
    return steps.length ? `${done} of ${steps.length}` : '';
  }

  protected labelOf(type: string): string {
    return labelForType(type).title;
  }

  protected badgeOf(status: string): string {
    return BADGES[status] ?? '';
  }

  protected wordOf(status: string): string {
    return WORDS[status] ?? status;
  }
}
