import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting, HttpTestingController } from '@angular/common/http/testing';
import { provideRouter } from '@angular/router';
import { Doctor } from './doctor';

const FINE = {
  overall: 'ok',
  live: false,
  checks: [
    { name: 'memory', status: 'ok', detail: '8 GiB', group: 'System', title: 'Memory' },
    { name: 'root-disk', status: 'ok', detail: '40% used', group: 'Storage', title: 'System disk space' },
  ],
};

const PROBLEMS = {
  overall: 'fail',
  live: true,
  checked_at: '2026-01-01T10:00:00Z',
  checks: [
    { name: 'memory', status: 'ok', detail: '8 GiB', group: 'System', title: 'Memory' },
    {
      name: 'tunnel-connector',
      status: 'fail',
      detail: 'no connections',
      group: 'Tunnel',
      title: 'Tunnel connector',
      why: 'Without it the public names do not reach you.',
      live: true,
      fix: { text: 'Restart the connector.', link: '/admin/tunnel', command: 'sudo systemctl restart homestead-cloudflared' },
    },
    { name: 'app-access:immich', status: 'warn', detail: 'open to every user', group: 'Apps', title: 'Immich access', fix: { text: 'Restrict it.' } },
    { name: 'authentik', status: 'unknown', detail: 'timed out', group: 'Sign-in', title: 'Authentik' },
  ],
};

describe('Doctor', () => {
  let fixture: ComponentFixture<Doctor>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Doctor],
      providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(Doctor);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  const el = () => fixture.nativeElement as HTMLElement;

  function open(body: object): void {
    fixture.detectChanges();
    http.expectOne('/api/v1/doctor').flush(body);
    fixture.detectChanges();
  }

  it('says everything is fine and keeps ok rows collapsed', () => {
    open(FINE);
    expect(el().querySelector('.masthead-fact')?.textContent).toContain('Everything checked is fine');
    expect(el().querySelector('.needs-you')).toBeNull();
    const details = el().querySelectorAll('details');
    expect(details.length).toBe(2);
    expect(details[0].hasAttribute('open')).toBe(false);
    expect(el().textContent).toContain('Live checks have not run');
  });

  it('puts problems first with fix, link and a copyable command', () => {
    open(PROBLEMS);
    expect(el().querySelector('.masthead-fact')?.textContent).toContain('2 problems need you');
    expect(el().querySelector('h2')?.textContent).toContain('Needs you (2)');
    const items = el().querySelectorAll('.needs-you-list > li');
    expect(items[0].getAttribute('data-status')).toBe('fail');
    expect(items[0].textContent).toContain('Restart the connector.');
    expect(items[0].textContent).toContain('Without it the public names');
    expect(items[0].querySelector('a[href="/admin/tunnel"]')).not.toBeNull();
    expect(items[0].querySelector('code')?.textContent).toBe('sudo systemctl restart homestead-cloudflared');
    // Groups follow in order, and unknown is not counted as a problem.
    const headings = Array.from(el().querySelectorAll('h2')).map((h) => h.textContent?.trim());
    expect(headings.slice(1, 5)).toEqual(['System', 'Tunnel', 'Sign-in', 'Apps']);
    expect(el().textContent).toContain('1 check could not be answered');
    expect(el().textContent).toContain('Live checks ran at');
  });

  it('shows a skeleton while the live probes run, then the new report', () => {
    open(FINE);
    (el().querySelector('.masthead-actions button') as HTMLButtonElement).click();
    fixture.detectChanges();
    const request = http.expectOne('/api/v1/doctor?live=1');
    expect(el().querySelector('.masthead-actions button')?.hasAttribute('disabled')).toBe(true);
    expect(el().querySelector('app-loading')?.getAttribute('aria-busy')).toBe('true');
    expect(el().textContent).toContain('Checking');
    request.flush(PROBLEMS);
    fixture.detectChanges();
    expect(el().querySelector('.masthead-fact')?.textContent).toContain('2 problems');
    expect(el().querySelector('.masthead-actions button')?.hasAttribute('disabled')).toBe(false);
  });

  it('keeps the last report and says so when the live probes fail', () => {
    open(FINE);
    (el().querySelector('.masthead-actions button') as HTMLButtonElement).click();
    fixture.detectChanges();
    http.expectOne('/api/v1/doctor?live=1').flush('boom', { status: 500, statusText: 'Server Error' });
    fixture.detectChanges();
    expect(el().querySelector('[role="alert"]')?.textContent).toContain('Live checks failed');
    expect(el().querySelectorAll('details').length).toBe(2);
  });

  it('explains when the diagnostics cannot be read', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/doctor').flush('no', { status: 500, statusText: 'Server Error' });
    fixture.detectChanges();
    expect(el().querySelector('[role="alert"]')?.textContent).toContain("Can't read the diagnostics");
    expect(el().querySelector('.masthead-actions button')?.hasAttribute('disabled')).toBe(true);
  });
});
