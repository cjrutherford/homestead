import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { TestBed } from '@angular/core/testing';
import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { Subject, of } from 'rxjs';
import { ApiService, OperationState } from './api.service';
import { ToastService } from './toast.service';
import { workOrderInterceptor } from './work-order.interceptor';
import { RAIL_MAX, SETTLE_MS, WorkOrders } from './work-orders.service';

function state(id: number, status: string, steps: string[] = ['one', 'two'], error = ''): OperationState {
  return {
    id,
    type: 'caddy.deploy',
    status,
    payload: '',
    result: '',
    error,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    steps: steps.map((name, i) => ({
      id: i + 1,
      operation_id: id,
      position: i,
      name,
      status: status === 'succeeded' ? 'succeeded' : i === 0 ? 'running' : 'pending',
      started_at: '',
      finished_at: '',
      error: '',
    })),
  };
}

describe('WorkOrders', () => {
  let streams: Map<number, Subject<OperationState>>;
  let orders: WorkOrders;
  let toast: ToastService;

  beforeEach(() => {
    vi.useFakeTimers();
    streams = new Map();
    TestBed.configureTestingModule({
      providers: [provideHttpClient(withInterceptors([workOrderInterceptor])), provideHttpClientTesting()],
    });
    const api = TestBed.inject(ApiService);
    vi.spyOn(api, 'operationEvents').mockImplementation((id: number) => {
      const subject = new Subject<OperationState>();
      streams.set(id, subject);
      return subject;
    });
    orders = TestBed.inject(WorkOrders);
    toast = TestBed.inject(ToastService);
  });
  afterEach(() => vi.useRealTimers());

  it('adds an order, follows its steps, and finishes with a toast', () => {
    orders.track(7, { title: 'Reconcile routes', done: 'Routes reconciled' });
    expect(orders.orders().map((o) => o.title)).toEqual(['Reconcile routes']);
    streams.get(7)!.next(state(7, 'running'));
    expect(orders.stateOf(7)?.status).toBe('running');
    expect(toast.announcement()).toContain('Reconcile routes: Step 1 of 2: one');
    streams.get(7)!.next(state(7, 'succeeded'));
    expect(toast.toasts().map((t) => t.message)).toEqual(['Routes reconciled']);
    expect(orders.active()).toBe(0);
  });

  it('settles a finished order after a short time', () => {
    orders.track(7, { title: 'Deploy Caddy', done: 'Caddy deployed' });
    streams.get(7)!.next(state(7, 'succeeded'));
    expect(orders.orders().length).toBe(1);
    vi.advanceTimersByTime(SETTLE_MS + 1);
    expect(orders.orders().length).toBe(0);
  });

  it('reports a failure with the reason as an error toast', () => {
    orders.track(8, { title: 'Deploy Caddy', done: 'Caddy deployed' });
    streams.get(8)!.next(state(8, 'failed', ['one'], 'port 80 busy'));
    const message = toast.toasts()[0];
    expect(message.kind).toBe('error');
    expect(message.message).toContain('Deploy Caddy failed.');
    expect(message.message).toContain('port 80 busy');
  });

  it('shows a handful and counts the rest', () => {
    for (let id = 1; id <= RAIL_MAX + 2; id++) {
      orders.track(id, { title: `Order ${id}`, done: 'done' });
    }
    expect(orders.visible().length).toBe(RAIL_MAX);
    expect(orders.hiddenCount()).toBe(2);
  });

  it('does not add the same operation twice', () => {
    orders.track(3, { title: 'A', done: 'a' });
    orders.track(3, { title: 'A', done: 'a' });
    expect(orders.orders().length).toBe(1);
  });

  it('finds running operations after a reload', () => {
    vi.spyOn(TestBed.inject(ApiService), 'operations').mockReturnValue(
      of([state(11, 'running'), state(10, 'succeeded'), state(9, 'pending')]),
    );
    orders.restore(true);
    expect(orders.orders().map((o) => o.id).sort()).toEqual([11, 9]);
    expect(orders.orders()[0].title).toBe('Deploy Caddy');
    // Throttled on navigation.
    orders.restore();
    expect(TestBed.inject(ApiService).operations).toHaveBeenCalledTimes(1);
  });

  it('adds an order when an operation-starting request succeeds', () => {
    const http = TestBed.inject(HttpTestingController);
    TestBed.inject(ApiService).edgeReconcile([]).subscribe();
    http.expectOne('/api/v1/edge/routes').flush({ id: 21 });
    expect(orders.orders()[0]).toMatchObject({ id: 21, title: 'Reconcile routes', done: 'Routes reconciled' });
  });

  it('ignores other requests that return an id', () => {
    const http = TestBed.inject(HttpTestingController);
    TestBed.inject(ApiService).edgeRoutes().subscribe();
    http.expectOne('/api/v1/edge/routes').flush({ id: 5, routes: [] });
    expect(orders.orders()).toEqual([]);
  });
});
