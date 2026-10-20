import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { provideRouter } from '@angular/router';
import { Subject } from 'rxjs';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { ApiService, DashboardAddress, LocalCaReport, OperationState } from '../core/api.service';
import { Trust, TRUST_GUIDES } from './trust';

const FP = 'AA:BB:CC:DD';

const report = (over: Partial<LocalCaReport> = {}): LocalCaReport => ({
  ca: {
    present: true,
    subject: 'Homestead Local CA',
    serial: '01',
    fingerprint_sha256: FP,
    not_before: '2026-01-01T00:00:00Z',
    not_after: '2036-01-01T00:00:00Z',
    permitted_dns: ['.home.arpa'],
    generation: 1,
    key_present: true,
  },
  leaves: [{ host: 'app.home.arpa', cert_name: 'app', status: 'ok' }],
  eligible_routes: 1,
  ...over,
});

const address = (over: Partial<DashboardAddress> = {}): DashboardAddress => ({
  name: 'homestead.lan',
  default: 'homestead.lan',
  configured: true,
  exposure: 'local',
  mdns_name: 'homestead.local',
  names: ['homestead.lan', 'homestead.local'],
  urls: ['https://homestead.local', 'https://homestead.lan'],
  root_path: '/api/v1/tls/local-ca/root.pem',
  ...over,
});

describe('Trust', () => {
  let fixture: ComponentFixture<Trust>;
  let http: HttpTestingController;
  let events: Subject<OperationState>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Trust],
      providers: [provideHttpClient(), provideHttpClientTesting(), provideRouter([])],
    }).compileComponents();
    events = new Subject();
    vi.spyOn(TestBed.inject(ApiService), 'operationEvents').mockReturnValue(events);
    fixture = TestBed.createComponent(Trust);
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
    http.expectOne('/api/v1/dashboard/address').flush(address());
  });
  afterEach(() => http.verify());

  const text = () => fixture.nativeElement.textContent as string;
  const load = (r: LocalCaReport) => {
    http.expectOne('/api/v1/tls/local-ca').flush(r);
    fixture.detectChanges();
  };
  const button = (label: string) =>
    Array.from(fixture.nativeElement.querySelectorAll('button')).find((b) =>
      (b as HTMLElement).textContent?.includes(label),
    ) as HTMLButtonElement;

  it('shows a loading state, then the fingerprint and downloads', () => {
    expect(fixture.nativeElement.querySelector('app-loading[aria-busy="true"]')).not.toBeNull();
    load(report());
    expect(fixture.nativeElement.querySelector('[data-testid="fingerprint"]').textContent).toContain(FP);
    const links = Array.from(fixture.nativeElement.querySelectorAll('a.button')) as HTMLAnchorElement[];
    expect(links.map((a) => a.getAttribute('href'))).toEqual([
      '/api/v1/tls/local-ca/root.pem',
      '/api/v1/tls/local-ca/root.der',
    ]);
    expect(text()).toContain('openssl x509');
  });

  it('lists every OS guide', () => {
    load(report());
    for (const guide of TRUST_GUIDES) {
      expect(text()).toContain(guide.title);
    }
    expect(text()).toContain('Certificate Trust Settings');
  });

  it('offers to create the CA when none exists', () => {
    load(report({ ca: { present: false, key_present: false }, leaves: [] }));
    expect(text()).toContain('has not been created yet');
    button('Create the internal CA').click();
    const req = http.expectOne('/api/v1/tls/local-ca');
    expect(req.request.method).toBe('POST');
    req.flush({ ca: report().ca, created: true });
    http.expectOne('/api/v1/tls/local-ca').flush(report());
    fixture.detectChanges();
    expect(text()).toContain('Internal CA created');
  });

  it('shows an error with retry when the load fails', () => {
    http.expectOne('/api/v1/tls/local-ca').flush('boom', { status: 500, statusText: 'x' });
    fixture.detectChanges();
    expect(text()).toContain('boom');
    expect(button('Retry')).toBeTruthy();
  });

  it('warns when the private key is missing', () => {
    load(report({ ca: { ...report().ca, key_present: false } }));
    expect(text()).toContain('private key is missing');
  });

  it('re-issues one leaf as an operation', () => {
    load(report());
    button('Re-issue').click();
    const req = http.expectOne('/api/v1/tls/local-ca/leaves/reissue');
    expect(req.request.body).toEqual({ host: 'app.home.arpa' });
    req.flush({ id: 5 });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('app-operation-progress a[href="/operations/5"]')).not.toBeNull();
  });

  it('requires confirmation before approving a rotation by digest', () => {
    load(report());
    button('Preview rotation').click();
    http.expectOne('/api/v1/tls/local-ca/rotation').flush({
      current_fingerprint_sha256: FP,
      current_not_after: '2036-01-01T00:00:00Z',
      generation: 1,
      leaves_to_reissue: ['app.home.arpa'],
      devices_must_retrust: 'Every device must trust the new root.',
      steps: ['mint a new root'],
      digest: 'd1',
    });
    fixture.detectChanges();
    expect(button('Approve rotation').disabled).toBe(true);
    const box = fixture.nativeElement.querySelector('input[type="checkbox"]') as HTMLInputElement;
    box.checked = true;
    box.dispatchEvent(new Event('change'));
    fixture.detectChanges();
    button('Approve rotation').click();
    const req = http.expectOne('/api/v1/tls/local-ca/rotation');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({ digest: 'd1' });
    req.flush({ id: 9 });
  });

  it('asks before re-issuing every certificate', () => {
    HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) { this.setAttribute('open', ''); };
    load(report());
    button('Re-issue all').click();
    fixture.detectChanges();
    http.expectNone('/api/v1/tls/local-ca/leaves/reissue');
    expect(fixture.nativeElement.querySelector('dialog')).not.toBeNull();
    expect(text()).toContain('Your devices keep trusting the CA');
    const confirm = Array.from(fixture.nativeElement.querySelectorAll('dialog button') as NodeListOf<HTMLButtonElement>).find((b) => b.textContent?.trim() === 'Re-issue all')!;
    confirm.click();
    const req = http.expectOne('/api/v1/tls/local-ca/leaves/reissue');
    expect(req.request.body).toEqual({ host: '' });
    req.flush({ id: 6 });
  });

  it('cancelling the re-issue dialog sends nothing', () => {
    HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) { this.setAttribute('open', ''); };
    load(report());
    button('Re-issue all').click();
    fixture.detectChanges();
    (Array.from(fixture.nativeElement.querySelectorAll('dialog button') as NodeListOf<HTMLButtonElement>).find((b) => b.textContent?.trim() === 'Cancel')!).click();
    fixture.detectChanges();
    http.expectNone('/api/v1/tls/local-ca/leaves/reissue');
    expect(fixture.nativeElement.querySelector('dialog')).toBeNull();
  });
  it('lists the dashboard URLs with the .local one first', () => {
    load(report());
    const urls = Array.from(
      fixture.nativeElement.querySelectorAll('[data-testid="dashboard-urls"] code'),
    ).map((c) => (c as HTMLElement).textContent?.trim());
    expect(urls).toEqual(['https://homestead.local', 'https://homestead.lan']);
    expect(fixture.nativeElement.querySelector('[data-testid="hosts-line"]').textContent).toContain(
      'homestead.lan',
    );
    expect(text()).toContain('Cloudflare tunnel is the public path');
  });

  it('saves the local name and follows the operation', async () => {
    load(report());
    await fixture.whenStable();
    const input = fixture.nativeElement.querySelector('input[name="dashboard-name"]') as HTMLInputElement;
    input.value = 'box.lan';
    input.dispatchEvent(new Event('input'));
    await fixture.whenStable();
    fixture.detectChanges();
    button('Save the name').click();
    const req = http.expectOne('/api/v1/dashboard/address');
    expect(req.request.method).toBe('PUT');
    expect(req.request.body).toEqual({ name: 'box.lan', exposure: 'local' });
    req.flush({ id: 12 });
    http.expectOne('/api/v1/dashboard/address').flush(address({ name: 'box.lan' }));
    fixture.detectChanges();
    expect(text()).toContain('Setting the dashboard address to box.lan');
  });

  it('shows the daemon refusal for a name it cannot vouch for', () => {
    load(report());
    button('Save the name').click();
    http
      .expectOne('/api/v1/dashboard/address')
      .flush('name is outside the suffixes', { status: 422, statusText: 'Unprocessable Entity' });
    fixture.detectChanges();
    expect(text()).toContain('outside the suffixes');
  });

  it('offers only local and overlay exposure', () => {
    load(report());
    const options = Array.from(
      fixture.nativeElement.querySelectorAll('select[name="dashboard-exposure"] option'),
    ).map((o) => (o as HTMLOptionElement).value);
    expect(options).toEqual(['local', 'overlay']);
  });
});
