import { Injectable, computed, inject, signal } from '@angular/core';
import { Subscription } from 'rxjs';
import { ApiService, OperationState } from './api.service';
import { OrderLabel, labelForType } from './operation-labels';
import { isTerminal, progressOf } from './operation-view';
import { ToastService } from './toast.service';

/** How many orders the rail lists before it says "more". */
export const RAIL_MAX = 3;
/** Finished orders leave the rail after this long; the outcome stays in Operations. */
export const SETTLE_MS = 8000;
export const SETTLE_FAILED_MS = 20000;
/** Running operations are re-read at most this often when the owner navigates. */
const RESTORE_THROTTLE_MS = 10000;

export interface WorkOrder {
  id: number;
  title: string;
  done: string;
  startedAt: number;
  state: OperationState | null;
  /** The live stream broke; the order stays until it settles. */
  lost: boolean;
  settled: boolean;
}

/**
 * Work orders (S283): the operations the owner started from the dashboard,
 * plus any found running on load. Each is followed over the operation event
 * stream (the same one the detail page uses). Finished orders announce their
 * result, then settle out of the rail after a short time. The record stays
 * in Operations. No daemon addition: running operations are found through
 * the existing operations list.
 */
@Injectable({ providedIn: 'root' })
export class WorkOrders {
  private readonly api = inject(ApiService);
  private readonly toast = inject(ToastService);

  readonly orders = signal<WorkOrder[]>([]);
  /** Ticks while any order is open so elapsed time moves. */
  readonly now = signal(Date.now());

  readonly visible = computed(() => this.orders().slice(0, RAIL_MAX));
  readonly hiddenCount = computed(() => Math.max(0, this.orders().length - RAIL_MAX));
  readonly active = computed(() => this.orders().filter((order) => !order.settled).length);

  private readonly streams = new Map<number, Subscription>();
  private readonly timers = new Map<number, ReturnType<typeof setTimeout>>();
  private tick: ReturnType<typeof setInterval> | null = null;
  private lastRestore = 0;

  /** The followed state of an operation, if the rail knows it. */
  stateOf(id: number): OperationState | null {
    return this.orders().find((order) => order.id === id)?.state ?? null;
  }

  /** Follows an operation the dashboard just started. Safe to call twice. */
  track(id: number, label: OrderLabel, state: OperationState | null = null): void {
    if (this.orders().some((order) => order.id === id)) {
      return;
    }
    const created = state ? Date.parse(state.created_at) : NaN;
    const order: WorkOrder = {
      id,
      title: label.title,
      done: label.done,
      startedAt: Number.isFinite(created) ? created : Date.now(),
      state,
      lost: false,
      settled: false,
    };
    this.orders.update((list) => [order, ...list]);
    this.toast.announce(`${label.title} started`);
    this.startTick();
    if (state && isTerminal(state.status)) {
      this.finish(order, state);
      return;
    }
    this.streams.set(
      id,
      this.api.operationEvents(id).subscribe({
        next: (update) => this.update(id, update),
        error: () => this.lose(id),
      }),
    );
  }

  /** Re-reads running operations so orders survive a reload or a CLI start. */
  restore(force = false): void {
    const now = Date.now();
    if (!force && now - this.lastRestore < RESTORE_THROTTLE_MS) {
      return;
    }
    this.lastRestore = now;
    this.api.operations(20).subscribe({
      next: (list) => {
        for (const op of Array.isArray(list) ? list : []) {
          if (op.status === 'running' || op.status === 'pending') {
            this.track(op.id, labelForType(op.type), op);
          }
        }
      },
      // Not logged in or the appliance is down: the rail simply stays empty.
      error: () => undefined,
    });
  }

  private update(id: number, state: OperationState): void {
    const order = this.orders().find((o) => o.id === id);
    if (!order) {
      return;
    }
    const before = progressOf(order.state);
    this.replace(id, { state });
    if (isTerminal(state.status)) {
      this.finish({ ...order, state }, state);
      return;
    }
    const after = progressOf(state);
    if (after.text !== before.text && after.currentIndex !== null) {
      this.toast.announce(`${order.title}: ${after.text}`);
    }
  }

  private finish(order: WorkOrder, state: OperationState): void {
    this.streams.get(order.id)?.unsubscribe();
    this.streams.delete(order.id);
    this.replace(order.id, { state, settled: true });
    if (state.status === 'succeeded') {
      this.toast.show(order.done, 'status');
    } else {
      const reason = state.error ? ` ${state.error}` : '';
      this.toast.show(
        state.status === 'cancelled'
          ? `${order.title} was cancelled.`
          : `${order.title} failed.${reason} Open the operation for the steps.`,
        'error',
      );
    }
    this.settleLater(order.id, state.status === 'succeeded' ? SETTLE_MS : SETTLE_FAILED_MS);
  }

  private lose(id: number): void {
    this.streams.delete(id);
    this.replace(id, { lost: true, settled: true });
    this.toast.show('Lost the live progress stream. Open the operation to see its state.', 'notice');
    this.settleLater(id, SETTLE_FAILED_MS);
  }

  private settleLater(id: number, ms: number): void {
    clearTimeout(this.timers.get(id));
    this.timers.set(
      id,
      setTimeout(() => {
        this.timers.delete(id);
        this.orders.update((list) => list.filter((order) => order.id !== id));
        if (this.orders().length === 0) {
          this.stopTick();
        }
      }, ms),
    );
  }

  private replace(id: number, patch: Partial<WorkOrder>): void {
    this.orders.update((list) => list.map((order) => (order.id === id ? { ...order, ...patch } : order)));
  }

  private startTick(): void {
    if (this.tick === null) {
      this.now.set(Date.now());
      this.tick = setInterval(() => this.now.set(Date.now()), 1000);
    }
  }

  private stopTick(): void {
    if (this.tick !== null) {
      clearInterval(this.tick);
      this.tick = null;
    }
  }
}
