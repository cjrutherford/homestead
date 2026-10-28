import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { provideRouter } from '@angular/router';
import { Status } from './status';

describe('Status', () => {
  let fixture: ComponentFixture<Status>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Status],
      providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()],
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
    expect(text).toContain('Claimed');
    expect(text).toContain('0.0.0-s002');
  });

  it('points an unclaimed appliance at the claim page, not at an outage', () => {
    fixture.detectChanges();
    for (const req of http.match(() => true)) {
      if (req.request.url === '/healthz') {
        req.flush({ status: 'ok', version: '1.0.0' });
      } else if (req.request.url === '/api/v1/doctor') {
        req.flush('appliance is not claimed', { status: 403, statusText: 'Forbidden' });
      } else {
        req.flush({ state: 'UNCLAIMED', version: '1.0.0' });
      }
    }
    fixture.detectChanges();
    const text = fixture.nativeElement.textContent as string;
    expect(text).not.toContain("Can't reach the appliance");
    expect(text).toContain('This appliance is not claimed yet.');
    expect(fixture.nativeElement.querySelector('a[href="/claim"]')).not.toBeNull();
  });

  it('renders an error when the daemon is unreachable', () => {
    fixture.detectChanges();
    for (const req of http.match(() => true)) {
      req.error(new ProgressEvent('error'));
    }
    fixture.detectChanges();
    const alert = fixture.nativeElement.querySelector('[role="alert"]');
    expect(alert?.textContent).toContain("Can't reach the appliance");
  });

  it('leads with problems, their fix, and collapses the good checks', () => {
    fixture.detectChanges();
    for (const req of http.match(() => true)) {
      if (req.request.url === '/api/v1/doctor') {
        req.flush({
          overall: 'fail',
          checks: [
            { name: 'cpu', status: 'ok', detail: '4 cores' },
            { name: 'memory', status: 'warn', detail: '1.5 GiB (below 2 GiB comfort)' },
            { name: 'state-dirs', status: 'fail', detail: 'missing /var/lib/homestead' },
          ],
        });
      } else if (req.request.url === '/healthz') {
        req.flush({ status: 'ok', version: '1.0.0' });
      } else {
        req.flush({ state: 'CLAIMED', version: '1.0.0' });
      }
    }
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    // Needs you comes first, with the worst item's furrow, and the masthead says so.
    expect(el.querySelector('h2')?.textContent).toContain('Needs you (2)');
    expect(el.querySelector('.needs-you')?.getAttribute('data-state')).toBe('fail');
    expect(el.querySelector('.masthead-fact')?.textContent).toContain('Homestead is running. 2 checks need you.');
    const items = el.querySelectorAll('.needs-you-list > li');
    expect(items.length).toBe(2);
    expect(items[0].getAttribute('data-status')).toBe('fail');
    expect(items[0].textContent).toContain('Homestead folders');
    expect(items[0].querySelector('code')?.textContent).toBe('sudo make install');
    expect(items[1].textContent).toContain('1.5 GiB');
    // A fix without a command still offers an action.
    expect(items[1].querySelector('.needs-you-action button')?.textContent).toContain('Check again');
    const good = el.querySelector('details');
    expect(good?.hasAttribute('open')).toBe(false);
    expect(good?.querySelector('summary')?.textContent).toContain('All good (1)');
    expect(el.textContent).toContain('homesteadctl doctor');
  });

  it('says so when nothing needs attention', () => {
    fixture.detectChanges();
    for (const req of http.match(() => true)) {
      if (req.request.url === '/api/v1/doctor') {
        req.flush({ overall: 'ok', checks: [{ name: 'cpu', status: 'ok', detail: '4 cores' }] });
      } else if (req.request.url === '/healthz') {
        req.flush({ status: 'ok', version: '1.0.0' });
      } else {
        req.flush({ state: 'CLAIMED', version: '1.0.0' });
      }
    }
    fixture.detectChanges();
    expect(fixture.nativeElement.textContent).toContain('Nothing needs attention');
    expect(fixture.nativeElement.querySelector('.needs-you')).toBeNull();
    expect(fixture.nativeElement.querySelector('.masthead-fact')?.textContent).toContain('Homestead is running. Nothing needs you.');
  });

  it('links Needs you and unexplained checks to Doctor (S316)', () => {
    fixture.detectChanges();
    for (const req of http.match(() => true)) {
      if (req.request.url === '/api/v1/doctor') {
        req.flush({
          overall: 'warn',
          checks: [
            { name: 'app-reach:immich', status: 'warn', detail: 'slow' },
            { name: 'authentik', status: 'unknown', detail: 'timed out' },
          ],
        });
      } else if (req.request.url === '/healthz') {
        req.flush({ status: 'ok', version: '1.0.0' });
      } else {
        req.flush({ state: 'CLAIMED', version: '1.0.0' });
      }
    }
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('h2')?.textContent).toContain('Needs you (1)');
    expect(el.querySelectorAll('.needs-you a[href="/doctor"]').length).toBe(2);
    expect(el.querySelector('.needs-you-list code')).toBeNull();
  });

  it('shows an unfinished app setup with its own title and a link to the catalog (S324)', () => {
    fixture.detectChanges();
    for (const req of http.match(() => true)) {
      if (req.request.url === '/api/v1/doctor') {
        req.flush({
          overall: 'warn',
          checks: [
            {
              name: 'app-setup:gridcraft',
              status: 'warn',
              group: 'Apps',
              title: "GridCraft's setup is unfinished",
              detail: 'Stopped at the public address: the plan changed after it was approved',
              fix: { text: 'Review the setup on the GridCraft card, then resume or skip the rest.', link: '/catalog', command: 'homesteadctl apps setup status gridcraft' },
            },
          ],
        });
      } else if (req.request.url === '/healthz') {
        req.flush({ status: 'ok', version: '1.0.0' });
      } else {
        req.flush({ state: 'CLAIMED', version: '1.0.0' });
      }
    }
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('.needs-you-list h3')?.textContent).toContain("GridCraft's setup is unfinished");
    expect(el.textContent).toContain('Stopped at the public address');
    expect(el.textContent).toContain('Review the setup on the GridCraft card');
    const link = el.querySelector('.needs-you a[href="/catalog"]');
    expect(link?.textContent?.trim()).toBe('Open the catalog');
  });
});
