import { Component, computed, inject, input, linkedSignal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { OperationState } from '../core/api.service';
import { progressOf } from '../core/operation-view';
import { WorkOrders } from '../core/work-orders.service';
import { StateMessage } from '../state/state-message';
import { StepList } from './step-list';

/**
 * In-page progress of one operation (S283), replacing the old
 * "Operation N: running" text. It reads the operation from the work-orders
 * store, or from `operation` when the page follows the stream itself.
 * The summary line is a polite live region.
 */
@Component({
  selector: 'app-operation-progress',
  imports: [RouterLink, StateMessage, StepList],
  template: `
    <section class="operation-progress" [attr.aria-label]="'Progress'">
      <p class="operation-progress-line">
        <span role="status" aria-live="polite">{{ progress().text }}</span>
        <a [routerLink]="['/admin/operations', id()]">Open operation</a>
      </p>
      @if (state()?.error; as failure) {
        <app-state-message kind="error" [headline]="failure" />
      }
      <app-step-list [steps]="state()?.steps ?? []" />
    </section>
  `,
})
export class OperationProgress {
  private readonly orders = inject(WorkOrders);
  readonly id = input.required<number>();
  /** The page's own followed state, when it follows the stream itself. */
  readonly operation = input<OperationState | null>(null);

  /**
   * The rail drops a finished order after a few seconds; the box keeps the
   * last state it saw for this id rather than falling back to "Starting".
   */
  protected readonly state = linkedSignal<{ id: number; state: OperationState | null }, OperationState | null>({
    source: () => ({ id: this.id(), state: this.operation() ?? this.orders.stateOf(this.id()) }),
    computation: (source, previous) =>
      source.state ?? (previous && previous.source.id === source.id ? previous.value : null),
  });
  protected readonly progress = computed(() => progressOf(this.state()));
}
