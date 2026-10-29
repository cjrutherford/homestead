import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { provideRouter } from '@angular/router';
import { Subject } from 'rxjs';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { ApiService, OperationState, TlsIssuancePlan } from '../core/api.service';
import { Tls } from './tls';

const plan = (over: Partial<TlsIssuancePlan> = {}): TlsIssuancePlan => ({
  certificate: 'www',
  names: ['www.example.com'],
  provider: 'cloudflare',
  zone: 'example.com',
  challenge: 'dns-01',
  directory: 'https://acme-staging-v02.api.letsencrypt.org/directory',
  directory_class: 'staging',
  account: { state: 'new', detail: 'A new account will be registered.', secret: 'sys_acme_account_ab' },
  credentials: [{ name: 'api_token', label: 'API token', present: false }],
  terms: 'Approving records your agreement.',
  placement: {
    directory: '/d',
    chain_file: '/d/fullchain.pem',
    key_file: '/d/privkey.pem',
    key_mode: '0600',
  },
  served_routes: ['www.example.com'],
  ready: true,
  blockers: [],
  digest: 'abc123',
  approved: false,
  ...over,
});

describe('Tls', () => {
  let fixture: ComponentFixture<Tls>;
  let http: HttpTestingController;
  let events: Subject<OperationState>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Tls],
      providers: [provideHttpClient(), provideHttpClientTesting(), provideRouter([])],
    }).compileComponents();
    events = new Subject();
    vi.spyOn(TestBed.inject(ApiService), 'operationEvents').mockReturnValue(events);
    fixture = TestBed.createComponent(Tls);
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  });
  afterEach(() => {
    // Renewal status loads with the plans; tests that do not care leave it unanswered.
    http.match('/api/v1/tls/renewal').forEach((r) => r.flush({ recorded_issuances: 0, summary: '', certificates: [] }));
    http.verify();
  });

  const text = () => fixture.nativeElement.textContent as string;
  const load = (plans: TlsIssuancePlan[]) => {
    http.expectOne('/api/v1/tls/issuance/plans').flush({ plans });
    fixture.detectChanges();
  };
  const button = (label: string) =>
    Array.from(
      fixture.nativeElement.querySelectorAll('button') as NodeListOf<HTMLButtonElement>,
    ).find((b) => b.textContent?.trim() === label)!;
  const check = (label: string) => {
    const box = Array.from(
      fixture.nativeElement.querySelectorAll('label') as NodeListOf<HTMLLabelElement>,
    ).find((l) => l.textContent?.includes(label))!.querySelector('input')!;
    box.checked = true;
    box.dispatchEvent(new Event('change'));
    fixture.detectChanges();
  };

  it('shows the plan preview with placement and the key mode', () => {
    load([plan()]);
    expect(text()).toContain('www.example.com');
    expect(text()).toContain('staging');
    expect(text()).toContain('/d/privkey.pem');
    expect(text()).toContain('0600');
    expect(text()).toContain('A new account will be registered.');
    expect(button('Issue certificate').disabled).toBe(true);
  });

  it('says so when the strategy is missing', () => {
    http
      .expectOne('/api/v1/tls/issuance/plans')
      .flush('no strategy', { status: 404, statusText: 'Not Found' });
    fixture.detectChanges();
    expect(text()).toContain('No TLS strategy is saved yet');
    // A state with a next action, not a red error.
    expect(fixture.nativeElement.querySelector('[role="alert"]')).toBeNull();
    expect(fixture.nativeElement.querySelector('a[href="/admin/config"]')).not.toBeNull();
  });

  it('approval needs the terms and posts the shown digest', () => {
    load([plan()]);
    expect(button('Approve plan').disabled).toBe(true);
    check('terms of service');
    expect(button('Approve plan').disabled).toBe(false);
    button('Approve plan').click();
    const req = http.expectOne('/api/v1/tls/issuance/approvals');
    expect(req.request.body).toEqual({
      certificate: 'www',
      digest: 'abc123',
      agree_terms: true,
      confirm_production: false,
      renewal: 'auto',
    });
    req.flush({});
    http.expectOne('/api/v1/tls/issuance/plans').flush({ plans: [plan({ approved: true })] });
    fixture.detectChanges();
    expect(text()).toContain('Nothing has been issued yet');
    expect(button('Issue certificate').disabled).toBe(false);
  });

  it('production needs its own explicit confirmation', () => {
    load([plan({ directory_class: 'production', directory: 'https://acme-v02.api.letsencrypt.org/directory' })]);
    expect(text()).toContain('production certificate authority');
    check('terms of service');
    expect(button('Approve plan').disabled).toBe(true);
    check('production certificate authority');
    expect(button('Approve plan').disabled).toBe(false);
    button('Approve plan').click();
    const req = http.expectOne('/api/v1/tls/issuance/approvals');
    expect(req.request.body.confirm_production).toBe(true);
    req.flush({});
    http.expectOne('/api/v1/tls/issuance/plans').flush({ plans: [] });
  });

  it('a blocked plan cannot be approved and says why', () => {
    load([plan({ ready: false, blockers: ['missing DNS credential "api_token"'] })]);
    check('terms of service');
    expect(text()).toContain('missing DNS credential');
    expect(button('Approve plan').disabled).toBe(true);
  });

  it('applies an approved plan and streams progress', () => {
    load([plan({ approved: true })]);
    button('Issue certificate').click();
    const req = http.expectOne('/api/v1/tls/issuance/apply');
    expect(req.request.body).toEqual({ certificate: 'www' });
    req.flush({ id: 9 });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('app-operation-progress a[href="/admin/operations/9"]')).not.toBeNull();
    events.next({ id: 9, status: 'running', error: '', steps: [{ id: 1, name: 'issue-and-place', status: 'running' }] } as unknown as OperationState);
    fixture.detectChanges();
    expect(text()).toContain('Issue and place');
    expect(text()).toContain('Running');
    events.next({ id: 9, status: 'failed', error: 'ca said no', steps: [] } as unknown as OperationState);
    fixture.detectChanges();
    expect(text()).toContain('ca said no');
    http.expectOne('/api/v1/tls/issuance/plans').flush({ plans: [plan({ approved: true })] });
  });

  it('shows the daemon refusal when apply is rejected', () => {
    load([plan({ approved: true })]);
    button('Issue certificate').click();
    http
      .expectOne('/api/v1/tls/issuance/apply')
      .flush('cert: plan changed', { status: 409, statusText: 'Conflict' });
    fixture.detectChanges();
    expect(text()).toContain('cert: plan changed');
    http.expectOne('/api/v1/tls/issuance/plans').flush({ plans: [plan()] });
  });

  it('stores credentials write-only and clears the form', () => {
    load([plan()]);
    const form = fixture.nativeElement.querySelector('form') as HTMLFormElement;
    const input = form.querySelector('input[type="password"]') as HTMLInputElement;
    expect(input).toBeTruthy();
    input.value = 'hunter2';
    form.dispatchEvent(new Event('submit'));
    const req = http.expectOne('/api/v1/tls/issuance/credentials');
    expect(req.request.body).toEqual({ provider: 'cloudflare', values: { api_token: 'hunter2' } });
    req.flush({ provider: 'cloudflare', stored: ['api_token'] });
    expect(input.value).toBe('');
    http.expectOne('/api/v1/tls/issuance/plans').flush({
      plans: [plan({ credentials: [{ name: 'api_token', label: 'API token', present: true }] })],
    });
    fixture.detectChanges();
    expect(text()).toContain('API token stored');
    expect(text()).not.toContain('hunter2');
  });

  it('refuses empty credential fields without calling the daemon', () => {
    load([plan()]);
    const form = fixture.nativeElement.querySelector('form') as HTMLFormElement;
    form.dispatchEvent(new Event('submit'));
    fixture.detectChanges();
    http.expectNone('/api/v1/tls/issuance/credentials');
    expect(text()).toContain('Fill in every credential field.');
  });

  it('shows a failing unattended re-issue with its backoff', () => {
    load([plan()]);
    http.match('/api/v1/tls/renewal')[0].flush({
      recorded_issuances: 1,
      summary: '1 recorded issuance(s)',
      certificates: [{
        certificate: 'www', status: 'renewal-due', days_remaining: 9, not_after: '2026-11-01T00:00:00Z',
        detail: 'www expires soon', attempts: 3, last_error: 'rate limited',
        next_attempt_at: '2026-10-15T04:00:00Z',
      }],
    });
    fixture.detectChanges();
    expect(text()).toContain('Unattended re-issue failed 3 times: rate limited');
    expect(text()).toContain('2026-10-15T04:00:00Z');
    expect(button('Re-register ACME account')).toBeUndefined();
  });

  const forgotten = () => ({
    recorded_issuances: 1,
    summary: '',
    certificates: [{
      certificate: 'www', status: 'renewal-due', days_remaining: 9, not_after: '2026-11-01T00:00:00Z',
      detail: 'www expires soon', account_recovery_required: true, last_error: 'accountDoesNotExist',
      renewal_mode: 'auto',
    }],
  });
  const terms = (over: Record<string, unknown>) => ({
    directory: 'https://ca.example/dir', directory_class: 'staging',
    current_terms_url: 'https://ca.example/terms-v2', recorded_terms_url: 'https://ca.example/terms-v1',
    changed: true, agreement_required: false, ...over,
  });

  it('offers the owner re-registration when the CA forgot the account', () => {
    load([plan()]);
    http.match('/api/v1/tls/renewal')[0].flush(forgotten());
    http.expectOne((r) => r.url === '/api/v1/tls/issuance/account/terms').flush(terms({}));
    fixture.detectChanges();
    expect(text()).toContain('https://ca.example/terms-v2');
    expect(text()).toContain('changed since the account was registered');
    button('Re-register ACME account').click();
    const req = http.expectOne('/api/v1/tls/issuance/account/reregister');
    expect(req.request.body).toEqual({ certificate: 'www', agree_terms: false });
    req.flush({ certificate: 'www', reregistered: true });
    http.expectOne('/api/v1/tls/issuance/plans').flush({ plans: [plan()] });
    fixture.detectChanges();
    expect(text()).toContain('Registered a new ACME account for www');
  });

  it('production re-registration needs a fresh terms agreement', () => {
    load([plan()]);
    http.match('/api/v1/tls/renewal')[0].flush(forgotten());
    http.expectOne((r) => r.url === '/api/v1/tls/issuance/account/terms')
      .flush(terms({ directory_class: 'production', agreement_required: true }));
    fixture.detectChanges();
    expect(button('Re-register ACME account').disabled).toBe(true);
    check('I agree to these terms of service');
    expect(button('Re-register ACME account').disabled).toBe(false);
    button('Re-register ACME account').click();
    const req = http.expectOne('/api/v1/tls/issuance/account/reregister');
    expect(req.request.body).toEqual({ certificate: 'www', agree_terms: true });
    req.flush({ certificate: 'www', reregistered: true });
    http.expectOne('/api/v1/tls/issuance/plans').flush({ plans: [] });
  });

  it('approval defaults to automatic renewal and can opt out', () => {
    load([plan()]);
    check('terms of service');
    check('I will renew this certificate myself');
    button('Approve plan').click();
    const req = http.expectOne('/api/v1/tls/issuance/approvals');
    expect(req.request.body.renewal).toBe('manual');
    req.flush({});
    http.expectOne('/api/v1/tls/issuance/plans').flush({ plans: [] });
  });
  it('lists the apps whose sign-in moves with a certificate for the public dashboard name (ADR-048 §6)', () => {
    load([
      plan({
        certificate: 'dash',
        names: ['homestead.example.com'],
        issuer_moves: {
          apps: [
            {
              app: 'immich',
              title: 'Immich',
              from: 'https://homestead.lan/apps/auth/application/o/homestead-immich/',
              to: 'https://homestead.example.com/apps/auth/application/o/homestead-immich/',
              host_entry: 'homestead.example.com:host-gateway',
              digest: 'm1',
            },
          ],
          token_kept: true,
          note: 'Once the certificate is placed, each app\'s sign-in is applied again with the Authentik API token you kept.',
        },
      }),
    ]);
    const moves = fixture.nativeElement.querySelector(
      'section[aria-label="Apps moving to Authentik\'s public address"]',
    ) as HTMLElement;
    expect(moves.textContent).toContain('applied again');
    expect(moves.textContent).toContain('Immich');
    expect(moves.textContent).toContain('homestead.example.com:host-gateway');
    // With the kept token the move is applied, so there is no re-plan link.
    expect(moves.querySelector('a')).toBeNull();
  });
});
