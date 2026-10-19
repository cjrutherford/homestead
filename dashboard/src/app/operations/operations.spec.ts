import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Subject } from 'rxjs';
import { ApiService, OperationState } from '../core/api.service';
import { WorkOrders } from '../core/work-orders.service';
import { Operations } from './operations';

describe('Operations', () => {
  let fixture: ComponentFixture<Operations>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Operations],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(Operations);
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  });

  afterEach(() => {
    http.verify();
  });

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  it('sorts by a column when its header is pressed', () => {
    http.expectOne('/api/v1/operations').flush([
      { id: 2, type: 'b.two', status: 'running', steps: [{ status: 'succeeded' }, { status: 'running' }] },
      { id: 1, type: 'a.one', status: 'succeeded', steps: [] },
    ]);
    fixture.detectChanges();
    const names = () =>
      Array.from(fixture.nativeElement.querySelectorAll('tbody tr td:first-child')).map((c) =>
        (c as HTMLElement).textContent?.trim(),
      );
    expect(names()).toEqual(['B two', 'A one']);
    expect(text()).toContain('1 of 2');
    const header = fixture.nativeElement.querySelector('th button.sort') as HTMLButtonElement;
    header.click();
    fixture.detectChanges();
    expect(names()).toEqual(['A one', 'B two']);
    expect(header.closest('th')?.getAttribute('aria-sort')).toBe('ascending');
    header.click();
    fixture.detectChanges();
    expect(names()).toEqual(['B two', 'A one']);
  });

  it('lists operations newest first', () => {
    const req = http.expectOne('/api/v1/operations');
    expect(req.request.method).toBe('GET');
    req.flush([
      { id: 2, type: 'b.two', status: 'running', steps: [] },
      { id: 1, type: 'a.one', status: 'succeeded', steps: [] },
    ]);
    fixture.detectChanges();
    expect(text()).toContain('B two');
    expect(text()).toContain('Running');
    const link = fixture.nativeElement.querySelector('a[href="/operations/2"]');
    expect(link).not.toBeNull();
  });

  it('reports an empty trail', () => {
    http.expectOne('/api/v1/operations').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('No operations yet.');
  });

  it('reports unreachability', () => {
    http.expectOne('/api/v1/operations').flush('down', {
      status: 503,
      statusText: 'Unavailable',
    });
    fixture.detectChanges();
    expect(text()).toContain("Can't reach the appliance");
  });

  it('refreshes when a work order starts or settles while open', () => {
    const stream = new Subject<OperationState>();
    TestBed.inject(ApiService).operationEvents = () => stream;
    http.expectOne('/api/v1/operations').flush([]);
    fixture.detectChanges();

    TestBed.inject(WorkOrders).track(5, { title: 'Reconcile routes', done: 'Routes reconciled' });
    fixture.detectChanges();
    http.expectOne('/api/v1/operations').flush([{ id: 5, type: 'x', status: 'running', steps: [] }]);

    stream.next({ id: 5, type: 'x', status: 'succeeded', steps: [] } as unknown as OperationState);
    fixture.detectChanges();
    http.expectOne('/api/v1/operations').flush([{ id: 5, type: 'x', status: 'succeeded', steps: [] }]);
    fixture.detectChanges();
    expect(text()).toContain('Done');
  });
});
