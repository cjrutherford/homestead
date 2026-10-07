import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute, provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Subject } from 'rxjs';
import { OperationDetail } from './operation-detail';
import { ApiService, OperationState } from '../core/api.service';

describe('OperationDetail', () => {
  let fixture: ComponentFixture<OperationDetail>;
  let http: HttpTestingController;
  let routeId: string | null;

  function setup(id: string | null): void {
    routeId = id;
    fixture = TestBed.createComponent(OperationDetail);
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  }

  beforeEach(async () => {
    routeId = null;
    await TestBed.configureTestingModule({
      imports: [OperationDetail],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { paramMap: { get: () => routeId } } },
        },
      ],
    }).compileComponents();
  });

  afterEach(() => {
    http.verify();
  });

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  it('shows steps of a finished operation without streaming', () => {
    setup('7');
    http.expectOne('/api/v1/operations/7').flush({
      id: 7,
      type: 'test.op',
      status: 'succeeded',
      steps: [{ id: 1, name: 'download', status: 'succeeded' }],
    });
    fixture.detectChanges();
    expect(text()).toContain('test.op');
    expect(text()).toContain('download: succeeded');
  });

  it('follows live updates for running operations', () => {
    const states = new Subject<OperationState>();
    vi.spyOn(TestBed.inject(ApiService), 'operationEvents').mockReturnValue(states);
    setup('8');
    http.expectOne('/api/v1/operations/8').flush({
      id: 8,
      type: 'test.op',
      status: 'running',
      steps: [],
    });
    fixture.detectChanges();
    expect(text()).toContain('running');
    states.next({
      id: 8,
      type: 'test.op',
      status: 'succeeded',
      payload: '',
      result: '',
      error: '',
      created_at: '',
      updated_at: '',
      steps: [{ id: 1, operation_id: 8, position: 0, name: 'install', status: 'succeeded', started_at: '', finished_at: '', error: '' }],
    });
    fixture.detectChanges();
    expect(text()).toContain('succeeded');
    expect(text()).toContain('install: succeeded');
  });

  it('reports unknown operations', () => {
    setup('999');
    http.expectOne('/api/v1/operations/999').flush('gone', {
      status: 404,
      statusText: 'Not Found',
    });
    fixture.detectChanges();
    expect(text()).toContain('not found');
  });

  it('rejects malformed ids without calling the API', () => {
    setup('bogus');
    http.expectNone('/api/v1/operations/bogus');
    fixture.detectChanges();
    expect(text()).toContain('not found');
  });
});
