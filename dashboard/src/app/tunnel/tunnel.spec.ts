import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Tunnel } from './tunnel';
import { WorkOrders } from '../core/work-orders.service';
import { OperationState } from '../core/api.service';

const STATUS = {
  preferred: 'tunnel',
  gateway: '',
  interface: '',
  protocols: [],
  tunnels: [
    { id: 'tunnel-id', name: 'homestead', status: 'healthy', connections: [] },
  ],
  routes: [
    {
      Hostname: 'app.example.com',
      TunnelID: 'tunnel-id',
      Service: 'http://127.0.0.1:8080',
    },
  ],
  connector: { installed: true, active: true },
  accounts: 1,
  proposed: { dashboard_hostname: 'homestead.example.com', auth_hostname: 'auth.example.com', sign_in_linked: true },
};

describe('Tunnel', () => {
  let fixture: ComponentFixture<Tunnel>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Tunnel],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(Tunnel);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    http.verify();
  });

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  it('leads with the tunnel when the router is silent', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    fixture.detectChanges();
    expect(text()).toContain('Preferred path: tunnel');
    expect(text()).toContain('Nothing on the router needs changing');
    expect(text()).toContain('homestead: healthy');
    expect(text()).toContain('app.example.com');
    expect(text()).toContain('http://127.0.0.1:8080');
  });

  const PLAN = {
    name: 'homestead',
    dashboard_hostname: 'homestead.example.com',
    auth_hostname: 'auth.example.com',
    zone: 'example.com',
    ingress: [
      { hostname: 'homestead.example.com', role: 'dashboard', route: 'homestead.lan', service: 'http://127.0.0.1:8480' },
      { hostname: 'auth.example.com', role: 'authentik', route: 'authentik.lan', service: 'http://127.0.0.1:8480' },
    ],
    dns: [
      {
        name: 'homestead.example.com',
        type: 'CNAME',
        content: '<new tunnel id>.cfargotunnel.com',
        proxied: true,
        action: 'replace',
        replaces: [{ type: 'A', name: 'homestead.example.com', content: '203.0.113.7', proxied: false }],
      },
      { name: 'auth.example.com', type: 'CNAME', content: '<new tunnel id>.cfargotunnel.com', proxied: true, action: 'create', replaces: [] },
    ],
    omitted: [],
    sign_in: 'After setup, re-plan Sign-in and approve it: it adds https://homestead.example.com.',
    warnings: ['Existing records for homestead.example.com are deleted and are not restored.'],
    digest: 'd1g',
  };

  function reviewPlan(plan: object = PLAN): void {
    fixture.detectChanges();
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    fixture.detectChanges();
    fixture.componentInstance['review']('homestead', '', 'login.example.com');
    const req = http.expectOne('/api/v1/tunnel/plan');
    expect(req.request.body).toEqual({ name: 'homestead', auth_hostname: 'login.example.com' });
    req.flush(plan);
    fixture.detectChanges();
  }

  it('never asks for a token or an account ID', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('input[type="password"]')).toBeNull();
    expect(el.querySelector('input[name="account-id"]')).toBeNull();
    expect(el.querySelector('#tunnel-service')).toBeNull();
    expect(el.querySelector('#tunnel-hostname')).toBeNull();
    expect(text()).toContain('Domain step');
  });

  it('proposes the daemon\'s names, and names under the setup domain before it answers', () => {
    fixture.componentRef.setInput('domain', 'example.net');
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('#tunnel-dashboard')?.getAttribute('placeholder')).toBe('homestead.example.net');
    expect(el.querySelector('#tunnel-auth')?.getAttribute('placeholder')).toBe('auth.example.net');
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    fixture.detectChanges();
    expect(el.querySelector('#tunnel-dashboard')?.getAttribute('placeholder')).toBe('homestead.example.com');
    expect(el.querySelector('#tunnel-auth')?.getAttribute('placeholder')).toBe('auth.example.com');
  });

  it('shows each public name with its edge route and the sign-in step', () => {
    reviewPlan();
    const el = fixture.nativeElement as HTMLElement;
    const names = el.querySelector('table[aria-label="Public hostnames"]')?.textContent ?? '';
    expect(names).toContain('homestead.example.com');
    expect(names).toContain('dashboard');
    expect(names).toContain('homestead.lan');
    expect(names).toContain('authentik.lan');
    expect(text()).toContain('re-plan Sign-in');
    expect(el.querySelector('a[href="/sign-in"]')).not.toBeNull();
  });

  it('says why the dashboard is left out until sign-in is linked', () => {
    reviewPlan({
      ...PLAN,
      ingress: [PLAN.ingress[1]],
      dns: [PLAN.dns[1]],
      omitted: [{ hostname: 'homestead.example.com', role: 'dashboard', reason: 'Sign-in with Authentik is not linked yet.' }],
      sign_in: undefined,
    });
    expect(text()).toContain('Left out: homestead.example.com (dashboard)');
    expect(text()).toContain('Sign-in with Authentik is not linked yet.');
    expect((fixture.nativeElement as HTMLElement).querySelector('a[href="/sign-in"]')).toBeNull();
  });

  it('shows the plan and changes nothing until approved', () => {
    reviewPlan();
    http.expectNone('/api/v1/tunnel/setup');
    const el = fixture.nativeElement as HTMLElement;
    const dns = el.querySelector('table[aria-label="DNS changes"]')?.textContent ?? '';
    expect(dns).toContain('Replace');
    expect(dns).toContain('CNAME homestead.example.com');
    expect(dns).toContain('CNAME auth.example.com');
    expect(dns).toContain('(proxied)');
    expect(dns).toContain('Delete');
    expect(dns).toContain('203.0.113.7');
    expect(text()).toContain('not restored');
    expect(text()).toContain('zone example.com');
  });

  it('approves the reviewed plan by its digest', () => {
    reviewPlan();
    const el = fixture.nativeElement as HTMLElement;
    const approve = Array.from(el.querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === 'Approve and set up',
    ) as HTMLButtonElement;
    approve.click();
    const setup = http.expectOne('/api/v1/tunnel/setup');
    expect(setup.request.body).toEqual({ name: 'homestead', auth_hostname: 'login.example.com', digest: 'd1g' });
    setup.flush({ id: 23 });
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    fixture.detectChanges();
    expect(el.querySelector('app-operation-progress a[href="/operations/23"]')).not.toBeNull();
    expect(el.querySelector('table[aria-label="DNS changes"]')).toBeNull();
  });

  it('re-reads the tunnels when setup ends and keeps saying Done after the rail drops it', () => {
    reviewPlan();
    fixture.componentInstance['approve']();
    http.expectOne('/api/v1/tunnel/setup').flush({ id: 23 });
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    const orders = TestBed.inject(WorkOrders);
    const finished: OperationState = {
      id: 23, type: 'tunnel.setup', status: 'succeeded', payload: '', result: '', error: '',
      created_at: '', updated_at: '', steps: [],
    };
    orders.orders.set([{ id: 23, title: 'Set up tunnel', done: 'Tunnel set up', startedAt: 0, state: finished, lost: false, settled: true }]);
    fixture.detectChanges();
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    fixture.detectChanges();
    expect(text()).toContain('Done');
    orders.orders.set([]);
    fixture.detectChanges();
    expect(text()).toContain('Done');
    expect(text()).not.toContain('Starting');
    http.expectNone('/api/v1/tunnel/status');
  });

  it('asks for a fresh review when the plan changed', () => {
    reviewPlan();
    fixture.componentInstance['approve']();
    http
      .expectOne('/api/v1/tunnel/setup')
      .flush('the plan changed since it was reviewed', { status: 409, statusText: 'Conflict' });
    fixture.detectChanges();
    expect(text()).toContain('Review it again');
    expect((fixture.nativeElement as HTMLElement).querySelector('table[aria-label="DNS changes"]')).toBeNull();
  });

  it('asks before tearing down and sends no account', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    (el.querySelector('.risky button.danger') as HTMLButtonElement).click();
    fixture.detectChanges();
    http.expectNone((req) => req.method === 'DELETE');
    const dialog = el.querySelector('app-dialog')?.textContent ?? '';
    expect(dialog).toContain('Tear down this tunnel?');
    expect(dialog).toContain('DNS records Homestead created');
    const confirm = Array.from(el.querySelectorAll('app-dialog button')).find(
      (b) => b.textContent?.trim() === 'Confirm tear down',
    ) as HTMLButtonElement;
    confirm.click();
    const teardown = http.expectOne('/api/v1/tunnel/tunnels/tunnel-id');
    expect(teardown.request.method).toBe('DELETE');
    teardown.flush({ id: 24 });
    http.expectOne('/api/v1/tunnel/status').flush({ ...STATUS, tunnels: [] });
    fixture.detectChanges();
    expect(el.querySelector('app-operation-progress a[href="/operations/24"]')).not.toBeNull();
  });

  it('shows missing setup fields next to the field', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    const forms = el.querySelectorAll('form');
    forms[forms.length - 1].dispatchEvent(new Event('submit'));
    fixture.detectChanges();
    const name = el.querySelector('#tunnel-name') as HTMLInputElement;
    expect(name.getAttribute('aria-invalid')).toBe('true');
    expect(el.querySelector('#tunnel-name-error')?.textContent).toContain('Enter a name');
    expect(el.querySelectorAll('.field-error').length).toBe(1);
    http.expectNone('/api/v1/tunnel/plan');
  });
});
