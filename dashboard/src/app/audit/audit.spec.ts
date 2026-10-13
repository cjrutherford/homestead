import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { AuditLog, PAGE_SIZE } from './audit';
import { AuditEntry } from '../core/api.service';

function entry(id: number, over: Partial<AuditEntry> = {}): AuditEntry {
  return {
    id,
    actor: 'admin',
    action: 'backup.run',
    plan: '',
    outcome: 'ok',
    detail: `detail ${id}`,
    override: false,
    override_reason: '',
    created_at: '2026-10-10T12:00:00Z',
    ...over,
  };
}

describe('AuditLog', () => {
  let fixture: ComponentFixture<AuditLog>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [AuditLog],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(AuditLog);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  const el = () => fixture.nativeElement as HTMLElement;
  const text = () => el().textContent as string;
  const items = () => el().querySelectorAll('li.audit-entry');

  function load(rows: AuditEntry[]): void {
    fixture.detectChanges();
    http.expectOne('/api/v1/audit?limit=100').flush(rows);
    fixture.detectChanges();
  }

  function type(name: string, value: string): void {
    const input = el().querySelector(`[name="${name}"]`) as HTMLInputElement;
    input.value = value;
    input.dispatchEvent(new Event(input.tagName === 'SELECT' ? 'change' : 'input'));
    fixture.detectChanges();
  }

  it('shows loading, then entries', () => {
    fixture.detectChanges();
    expect(text()).toContain('Loading audit log');
    http.expectOne('/api/v1/audit?limit=100').flush([entry(1)]);
    fixture.detectChanges();
    expect(items().length).toBe(1);
    expect(text()).toContain('backup.run');
  });

  it('shows an empty state', () => {
    load([]);
    expect(text()).toContain('No audit entries yet.');
  });

  it('maps a network failure to readable text', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/audit?limit=100').error(new ProgressEvent('error'));
    fixture.detectChanges();
    expect(text()).toContain('Cannot reach the Homestead daemon');
  });

  it('shows server error text', () => {
    fixture.detectChanges();
    http
      .expectOne('/api/v1/audit?limit=100')
      .flush('failed to read audit trail', { status: 500, statusText: 'x' });
    fixture.detectChanges();
    expect(text()).toContain('failed to read audit trail');
  });

  it('renders a badge for every outcome', () => {
    load([
      entry(1, { outcome: 'ok' }),
      entry(2, { outcome: 'error' }),
      entry(3, { outcome: 'skipped' }),
      entry(4, { outcome: 'warning' }),
      entry(5, { outcome: 'mystery' }),
    ]);
    const badges = Array.from(el().querySelectorAll('.outcome')).map((b) => [
      b.textContent?.trim(),
      b.className,
    ]);
    expect(badges.map((b) => b[0])).toEqual(['ok', 'error', 'skipped', 'warning', 'mystery']);
    expect(badges[0][1]).toContain('outcome-ok');
    expect(badges[1][1]).toContain('outcome-error');
    expect(badges[4][1]).toContain('outcome-other');
  });

  it('filters by actor, action, outcome and time range', () => {
    load([
      entry(1, { actor: 'alice', action: 'secrets.rotate', outcome: 'ok', created_at: '2026-10-01T10:00:00Z' }),
      entry(2, { actor: 'bob', action: 'backup.run', outcome: 'error', created_at: '2026-10-05T10:00:00Z' }),
      entry(3, { actor: 'bob', action: 'backup.run', outcome: 'ok', created_at: '2026-10-09T10:00:00Z' }),
    ]);
    type('actor', 'BOB');
    expect(items().length).toBe(2);
    type('outcome', 'error');
    expect(items().length).toBe(1);
    type('outcome', '');
    type('action', 'secrets');
    type('actor', '');
    expect(items().length).toBe(1);
    type('action', '');
    type('since', '2026-10-05');
    expect(items().length).toBe(2);
    type('until', '2026-10-05');
    expect(items().length).toBe(1);
    type('actor', 'nobody');
    expect(text()).toContain('No entries match these filters.');
  });

  it('paginates the filtered set', () => {
    const rows = Array.from({ length: PAGE_SIZE + 5 }, (_, i) => entry(i + 1));
    load(rows);
    expect(items().length).toBe(PAGE_SIZE);
    expect(text()).toContain('Page 1 of 2');
    fixture.componentInstance.go(1);
    fixture.detectChanges();
    expect(items().length).toBe(5);
    expect(text()).toContain('Page 2 of 2');
    fixture.componentInstance.go(1);
    fixture.detectChanges();
    expect(text()).toContain('Page 2 of 2');
  });

  it('reloads with another limit', () => {
    load([entry(1)]);
    type('limit', '500');
    http.expectOne('/api/v1/audit?limit=500').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('No audit entries yet.');
  });

  it('shows counts for aggregated rejection entries', () => {
    load([
      entry(1, {
        actor: 'anonymous',
        action: 'session.boundary.rejected',
        outcome: 'error',
        detail: 'rejected GET /api/v1/status (no session) x42 since 2026-10-10T11:00:00Z',
      }),
      entry(2, {
        action: 'session.boundary.rejected',
        detail: 'rejected POST /api/v1/login (csrf) x1 since 2026-10-10T11:00:00Z',
      }),
    ]);
    expect(text()).toContain('Rejected 42 requests to GET /api/v1/status (no session)');
    expect(text()).toContain('Rejected 1 request to POST /api/v1/login (csrf)');
  });

  it('renders details and plans as text, never HTML', () => {
    load([
      entry(1, {
        detail: '<img src=x onerror=alert(1)>',
        plan: '{"a":"<b>bold</b>"}',
        override: true,
        override_reason: '<i>why</i>',
      }),
    ]);
    expect(el().querySelector('li img')).toBeNull();
    expect(el().querySelector('li b')).toBeNull();
    expect(el().querySelector('li i')).toBeNull();
    expect(text()).toContain('<img src=x onerror=alert(1)>');
    expect(text()).toContain('Owner override: <i>why</i>');
  });

  it('interprets date filters in the local timezone, until inclusive', () => {
    const late = new Date(2026, 9, 5, 23, 30).toISOString();
    const early = new Date(2026, 9, 6, 0, 30).toISOString();
    load([entry(1, { created_at: late }), entry(2, { created_at: early })]);
    type('until', '2026-10-05');
    expect(items().length).toBe(1);
    expect(text()).toContain('detail 1');
    type('until', '');
    type('since', '2026-10-06');
    expect(items().length).toBe(1);
    expect(text()).toContain('detail 2');
  });

  it('ignores an out-of-order response after a limit change', () => {
    fixture.detectChanges();
    const first = http.expectOne('/api/v1/audit?limit=100');
    type('limit', '500');
    const second = http.expectOne('/api/v1/audit?limit=500');
    second.flush([entry(2)]);
    first.flush([entry(1)]);
    fixture.detectChanges();
    expect(items().length).toBe(1);
    expect(text()).toContain('detail 2');
    expect(text()).not.toContain('detail 1');
  });

  it('resets to the first page when a filter changes', () => {
    load(Array.from({ length: PAGE_SIZE + 5 }, (_, i) => entry(i + 1)));
    fixture.componentInstance.go(1);
    fixture.detectChanges();
    expect(text()).toContain('Page 2 of 2');
    type('actor', 'admin');
    expect(text()).toContain('Page 1 of 2');
  });

  it('keeps filters when the limit changes', () => {
    load([entry(1, { actor: 'bob' }), entry(2, { actor: 'alice' })]);
    type('actor', 'bob');
    type('limit', '250');
    http.expectOne('/api/v1/audit?limit=250').flush([
      entry(3, { actor: 'bob' }),
      entry(4, { actor: 'alice' }),
    ]);
    fixture.detectChanges();
    expect(items().length).toBe(1);
    expect(text()).toContain('detail 3');
  });

  it('shows invalid-JSON plans as raw text', () => {
    load([entry(1, { plan: 'not json {' })]);
    expect(text()).toContain('not json {');
  });

  it('falls back to raw detail for a malformed rejection', () => {
    load([
      entry(1, {
        action: 'session.boundary.rejected',
        detail: 'rejected somewhere weird',
      }),
    ]);
    expect(text()).toContain('rejected somewhere weird');
    expect(text()).not.toContain('Rejected 0');
  });
});
