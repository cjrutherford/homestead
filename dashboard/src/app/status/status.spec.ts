import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Status } from './status';

describe('Status', () => {
  let fixture: ComponentFixture<Status>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Status],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(Status);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    http.verify();
  });

  function flushAll(): void {
    const pending = http.match(() => true);
    for (const req of pending) {
      if (req.request.url === '/healthz') {
        req.flush({ status: 'ok', version: '0.0.0-s002' });
      } else if (req.request.url === '/api/v1/doctor') {
        req.flush({
          overall: 'warn',
          checks: [{ name: 'memory', status: 'warn', detail: 'low' }],
        });
      } else {
        req.flush({ state: 'CLAIMED', version: '0.0.0-s002' });
      }
    }
    fixture.detectChanges();
  }

  it('renders daemon health and claim state', () => {
    fixture.detectChanges();
    flushAll();
    const text = fixture.nativeElement.textContent as string;
    expect(text).toContain('ok');
    expect(text).toContain('CLAIMED');
    expect(text).toContain('0.0.0-s002');
  });

  it('renders an error when the daemon is unreachable', () => {
    fixture.detectChanges();
    for (const req of http.match(() => true)) {
      req.error(new ProgressEvent('error'));
    }
    fixture.detectChanges();
    const alert = fixture.nativeElement.querySelector('[role="alert"]');
    expect(alert?.textContent).toContain('unreachable');
  });

  it('renders diagnostics from the doctor', () => {
    fixture.detectChanges();
    flushAll();
    const text = fixture.nativeElement.textContent as string;
    expect(text).toContain('Diagnostics: warn');
    expect(text).toContain('memory: warn');
    expect(text).toContain('low');
  });
});
