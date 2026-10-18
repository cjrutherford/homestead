import { Component, computed, inject, input, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { formatElapsed, progressOf } from '../core/operation-view';
import { WorkOrder, WorkOrders } from '../core/work-orders.service';

/**
 * The work-orders rail (S283): the one bold element of the dashboard. In the
 * sidebar slot it lists the orders in flight (title, step, count, elapsed
 * time, result); below md it is a bottom sheet that opens from a summary
 * bar. It renders nothing while there are no orders.
 */
@Component({
  selector: 'app-work-orders-rail',
  imports: [RouterLink],
  templateUrl: './work-orders-rail.html',
})
export class WorkOrdersRail {
  protected readonly store = inject(WorkOrders);
  readonly variant = input<'rail' | 'sheet'>('rail');
  protected readonly open = signal(false);

  protected readonly summary = computed(() => {
    const orders = this.store.orders();
    const running = orders.filter((order) => !order.settled).length;
    if (running > 0) {
      return running === 1 ? '1 work order running' : `${running} work orders running`;
    }
    return orders[0] ? `${orders[0].title}: ${progressOf(orders[0].state).text}` : '';
  });

  protected progress(order: WorkOrder) {
    return progressOf(order.state);
  }

  protected elapsed(order: WorkOrder): string {
    const state = order.state;
    const end = order.settled && state ? Date.parse(state.updated_at) : this.store.now();
    return formatElapsed((Number.isFinite(end) ? end : this.store.now()) - order.startedAt);
  }

  protected outcome(order: WorkOrder): 'running' | 'done' | 'failed' {
    if (!order.settled) {
      return 'running';
    }
    return order.state?.status === 'succeeded' ? 'done' : 'failed';
  }

  protected toggle(): void {
    this.open.update((value) => !value);
  }
}
