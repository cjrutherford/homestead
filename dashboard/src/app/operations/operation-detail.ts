import { Component, DestroyRef, computed, inject, OnInit, signal } from '@angular/core';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ApiService, OperationState } from '../core/api.service';
import { labelForType } from '../core/operation-labels';
import { isTerminal, progressOf } from '../core/operation-view';
import { StateMessage } from '../state/state-message';
import { Loading } from '../ui/loading';
import { StepList } from '../ui/step-list';
/**
 * Operation detail page (S064): initial fetch plus live state events
 * until terminal. The subscription dies with the component; terminal
 * states stop the stream server-side.
 */
@Component({
  imports: [StateMessage, RouterLink, Loading, StepList],
  selector: 'app-operation-detail',
  templateUrl: './operation-detail.html',
})
export class OperationDetail implements OnInit {
  private readonly api = inject(ApiService);
  private readonly route = inject(ActivatedRoute);
  private readonly destroyRef = inject(DestroyRef);

  protected readonly operation = signal<OperationState | null>(null);
  protected readonly missing = signal(false);
  protected readonly title = computed(() => {
    const op = this.operation();
    return op ? labelForType(op.type).title : 'Operation';
  });
  protected readonly progress = computed(() => progressOf(this.operation()));
  protected readonly word = computed(() => {
    switch (this.operation()?.status) {
      case 'succeeded':
        return 'Done';
      case 'failed':
        return 'Failed';
      case 'running':
        return 'Running';
      case 'cancelled':
        return 'Cancelled';
      default:
        return 'Waiting';
    }
  });
  /** The masthead sentence: where the operation stands, from the record the page reads. */
  protected readonly fact = computed(() => {
    const op = this.operation();
    if (!op) {
      return 'Reading the operation record.';
    }
    const p = this.progress();
    const steps = p.total === 1 ? '1 step' : `${p.total} steps`;
    switch (op.status) {
      case 'succeeded':
        return `This operation finished all ${steps}.`;
      case 'failed':
        return `This operation stopped after ${p.done} of ${steps}.`;
      case 'running':
        return `This operation is running: ${p.done} of ${steps} done.`;
      case 'cancelled':
        return 'This operation was cancelled.';
      default:
        return 'This operation is waiting to start.';
    }
  });
  protected readonly badge = computed(() => {
    switch (this.operation()?.status) {
      case 'succeeded':
        return 'badge-ok';
      case 'failed':
        return 'badge-fail';
      case 'running':
        return 'badge-running';
      case 'cancelled':
        return '';
      default:
        return 'badge-pending';
    }
  });

  ngOnInit(): void {
    const id = Number(this.route.snapshot.paramMap.get('id'));
    if (!Number.isInteger(id) || id <= 0) {
      this.missing.set(true);
      return;
    }
    this.api.operation(id).subscribe({
      next: (op) => {
        this.operation.set(op);
        if (!isTerminal(op.status)) {
          this.api
            .operationEvents(id)
            .pipe(takeUntilDestroyed(this.destroyRef))
            .subscribe({
              next: (update) => this.operation.set(update),
              error: () => undefined,
            });
        }
      },
      error: () => this.missing.set(true),
    });
  }
}
