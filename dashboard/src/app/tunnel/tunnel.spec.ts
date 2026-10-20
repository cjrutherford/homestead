import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Tunnel } from './tunnel';

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
    hostname: 'photos.example.com',
    service: 'http://127.0.0.1:2283',
    zone: 'example.com',
    ingress: [{ hostname: 'photos.example.com', service: 'http://127.0.0.1:2283' }],
    dns: [
      {
        name: 'photos.example.com',
        type: 'CNAME',
        content: '<new tunnel id>.cfargotunnel.com',
        proxied: true,
        action: 'replace',
        replaces: [{ type: 'A', name: 'photos.example.com', content: '203.0.113.7', proxied: false }],
      },
    ],
    warnings: ['Existing records for photos.example.com are deleted and are not restored.'],
    digest: 'd1g',
  };

  function reviewPlan(): void {
    fixture.detectChanges();
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    fixture.detectChanges();
    fixture.componentInstance['review']('homestead', 'photos.example.com', 'http://127.0.0.1:2283');
    const plan = http.expectOne('/api/v1/tunnel/plan');
    expect(plan.request.body).toEqual({
      name: 'homestead',
      hostname: 'photos.example.com',
      service: 'http://127.0.0.1:2283',
    });
    plan.flush(PLAN);
    fixture.detectChanges();
  }

  it('never asks for a token or an account ID', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('input[type="password"]')).toBeNull();
    expect(el.querySelector('input[name="account-id"]')).toBeNull();
    expect(el.querySelector('#tunnel-service')?.getAttribute('placeholder')).toBeNull();
    expect(text()).toContain('Domain step');
  });

  it('suggests the hostname under the setup domain', () => {
    fixture.componentRef.setInput('domain', 'example.net');
    fixture.detectChanges();
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    fixture.detectChanges();
    expect((fixture.nativeElement as HTMLElement).querySelector('#tunnel-hostname')?.getAttribute('placeholder')).toBe(
      'photos.example.net',
    );
  });

  it('shows the plan and changes nothing until approved', () => {
    reviewPlan();
    http.expectNone('/api/v1/tunnel/setup');
    const el = fixture.nativeElement as HTMLElement;
    const dns = el.querySelector('table[aria-label="DNS changes"]')?.textContent ?? '';
    expect(dns).toContain('Replace');
    expect(dns).toContain('CNAME photos.example.com');
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
    expect(setup.request.body).toEqual({
      name: 'homestead',
      hostname: 'photos.example.com',
      service: 'http://127.0.0.1:2283',
      digest: 'd1g',
    });
    setup.flush({ id: 23 });
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    fixture.detectChanges();
    expect(el.querySelector('app-operation-progress a[href="/operations/23"]')).not.toBeNull();
    expect(el.querySelector('table[aria-label="DNS changes"]')).toBeNull();
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
    expect(el.querySelectorAll('.field-error').length).toBe(3);
    http.expectNone('/api/v1/tunnel/plan');
  });
});
