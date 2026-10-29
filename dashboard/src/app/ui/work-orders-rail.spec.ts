import { describe, expect, it } from 'vitest';
import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { Subject } from 'rxjs';
import { ApiService, OperationState } from '../core/api.service';
import { WorkOrders } from '../core/work-orders.service';
import { WorkOrdersRail } from './work-orders-rail';
import { StepList } from './step-list';

function running(id: number): OperationState {
  const step = (n: number, name: string, status: string) => ({
    id: n, operation_id: id, position: n - 1, name, status, started_at: '', finished_at: '', error: '',
  });
  return {
    id, type: 'x', status: 'running', payload: '', result: '', error: '',
    created_at: new Date().toISOString(), updated_at: '',
    steps: [step(1, 'pull', 'succeeded'), step(2, 'up', 'running'), step(3, 'check', 'pending')],
  };
}

describe('WorkOrdersRail', () => {
  function setup(variant: 'rail' | 'sheet' = 'rail') {
    TestBed.configureTestingModule({
      providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()],
    });
    const stream = new Subject<OperationState>();
    TestBed.inject(ApiService).operationEvents = () => stream;
    const fixture = TestBed.createComponent(WorkOrdersRail);
    fixture.componentRef.setInput('variant', variant);
    fixture.detectChanges();
    return { fixture, stream, orders: TestBed.inject(WorkOrders), root: fixture.nativeElement as HTMLElement };
  }

  it('renders nothing while there are no orders', () => {
    const { root } = setup();
    expect(root.querySelector('.rail')).toBeNull();
  });

  it('shows title, current step, count, elapsed time, and links to the operation', () => {
    const { fixture, stream, orders, root } = setup();
    orders.track(4, { title: 'Reconcile routes', done: 'Routes reconciled' });
    stream.next(running(4));
    fixture.detectChanges();
    expect(root.querySelector('.rail-title')?.textContent).toContain('Reconcile routes');
    expect(root.querySelector('.rail-title')?.getAttribute('href')).toBe('/admin/operations/4');
    expect(root.querySelector('.rail-step')?.textContent).toContain('Step 2 of 3: Start the containers');
    expect(root.querySelector('.rail-time')?.textContent).toMatch(/\d+:\d\d/);
    const bar = root.querySelector('[role="progressbar"]')!;
    expect(bar.getAttribute('aria-valuenow')).toBe('1');
    expect(bar.getAttribute('aria-valuemax')).toBe('3');
  });

  it('shows the result when an order finishes', () => {
    const { fixture, stream, orders, root } = setup();
    orders.track(4, { title: 'Reconcile routes', done: 'Routes reconciled' });
    stream.next({ ...running(4), status: 'succeeded', steps: [] });
    fixture.detectChanges();
    expect(root.querySelector('.rail-order')?.getAttribute('data-outcome')).toBe('done');
    expect(root.querySelector('.rail-step')?.textContent).toContain('Done');
  });

  it('lists a handful and links to Operations for the rest', () => {
    const { fixture, orders, root } = setup();
    for (let id = 1; id <= 5; id++) {
      orders.track(id, { title: `Order ${id}`, done: 'done' });
    }
    fixture.detectChanges();
    expect(root.querySelectorAll('.rail-order').length).toBe(3);
    expect(root.querySelector('.rail-more')?.textContent).toContain('2 more');
  });

  it('is a collapsed bottom sheet with a summary toggle on small screens', () => {
    const { fixture, orders, root } = setup('sheet');
    orders.track(4, { title: 'Deploy Caddy', done: 'Caddy deployed' });
    fixture.detectChanges();
    const toggle = root.querySelector<HTMLButtonElement>('.rail-summary')!;
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect((root.querySelector('.rail-orders') as HTMLElement).hidden).toBe(true);
    toggle.click();
    fixture.detectChanges();
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect((root.querySelector('.rail-orders') as HTMLElement).hidden).toBe(false);
  });

  it('closes the open bottom sheet on Escape', () => {
    const { fixture, orders, root } = setup('sheet');
    orders.track(4, { title: 'Reconcile routes', done: 'Routes reconciled' });
    fixture.detectChanges();
    const summary = root.querySelector<HTMLButtonElement>('.rail-summary')!;
    summary.click();
    fixture.detectChanges();
    expect(summary.getAttribute('aria-expanded')).toBe('true');
    summary.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    fixture.detectChanges();
    expect(summary.getAttribute('aria-expanded')).toBe('false');
  });
});

describe('StepList', () => {
  it('shows a word beside every step glyph', () => {
    const fixture = TestBed.createComponent(StepList);
    fixture.componentRef.setInput('steps', running(1).steps);
    fixture.detectChanges();
    const text = (fixture.nativeElement as HTMLElement).textContent!;
    expect(text).toContain('Done');
    expect(text).toContain('Running');
    expect(text).toContain('Waiting');
  });
});
