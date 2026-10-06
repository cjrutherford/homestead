import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { ApiService } from './api.service';

describe('ApiService', () => {
  let service: ApiService;
  let http: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    service = TestBed.inject(ApiService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    http.verify();
  });

  it('fetches daemon health', () => {
    service.health().subscribe((health) => {
      expect(health.status).toBe('ok');
      expect(health.version).toBe('0.0.0-s002');
    });
    const req = http.expectOne('/healthz');
    expect(req.request.method).toBe('GET');
    req.flush({ status: 'ok', version: '0.0.0-s002' });
  });

  it('fetches appliance identity', () => {
    service.identity().subscribe((identity) => {
      expect(identity.state).toBe('UNCLAIMED');
    });
    const req = http.expectOne('/api/v1/identity');
    expect(req.request.method).toBe('GET');
    req.flush({ state: 'UNCLAIMED', version: '0.0.0-s002' });
  });

  it('fetches the diagnostic report', () => {
    service.doctor().subscribe((report) => {
      expect(report.overall).toBe('ok');
      expect(report.checks.length).toBe(1);
    });
    const req = http.expectOne('/api/v1/doctor');
    expect(req.request.method).toBe('GET');
    req.flush({ overall: 'ok', checks: [{ name: 'memory', status: 'ok', detail: 'x' }] });
  });

  it('fetches desired configuration', () => {
    service.config().subscribe((config) => {
      expect(config.version).toBe(1);
      expect(config.settings).toEqual({});
    });
    const req = http.expectOne('/api/v1/config');
    expect(req.request.method).toBe('GET');
    req.flush({ version: 1, settings: {} });
  });

  it('fetches field metadata for the renderer', () => {
    service.configSchema().subscribe((schema) => {
      expect(schema.fields.length).toBe(1);
      expect(schema.fields[0].name).toBe('hostname');
    });
    const req = http.expectOne('/api/v1/config/schema');
    expect(req.request.method).toBe('GET');
    req.flush({
      fields: [{ name: 'hostname', type: 'string', description: 'Name.', required: true }],
    });
  });

  it('redeems a claim token', () => {
    service.claim('console-token').subscribe((result) => {
      expect(result.state).toBe('CLAIMED');
    });
    const req = http.expectOne('/api/v1/claim');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({ token: 'console-token' });
    req.flush({ state: 'CLAIMED' });
  });

  it('creates the first administrator', () => {
    service.setupAdmin('owner', 'long-enough-password').subscribe((admin) => {
      expect(admin.username).toBe('owner');
    });
    const req = http.expectOne('/api/v1/admins/setup');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({
      username: 'owner',
      password: 'long-enough-password',
    });
    req.flush({ id: 1, username: 'owner' });
  });

  it('logs in for a session token', () => {
    service.login('owner', 'long-enough-password').subscribe((session) => {
      expect(session.token).toBe('session-token');
    });
    const req = http.expectOne('/api/v1/sessions');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({
      username: 'owner',
      password: 'long-enough-password',
    });
    req.flush({ token: 'session-token', expires_at: '2026-01-01T00:00:00Z' });
  });

  it('logs out with the bearer token', () => {
    service.logout('session-token').subscribe();
    const req = http.expectOne('/api/v1/sessions');
    expect(req.request.method).toBe('DELETE');
    expect(req.request.headers.get('Authorization')).toBe('Bearer session-token');
    req.flush(null);
  });

  it('plans configuration without persisting', () => {    service.planConfig({ hostname: 'hs' }).subscribe((plan) => {
      expect(plan.valid).toBe(true);
      expect(plan.effective).toEqual({ hostname: 'hs' });
    });
    const req = http.expectOne('/api/v1/config/plan');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({ settings: { hostname: 'hs' } });
    req.flush({ valid: true, effective: { hostname: 'hs' } });
  });

  it('applies configuration', () => {
    service.applyConfig({ hostname: 'hs' }).subscribe((state) => {
      expect(state.version).toBe(1);
    });
    const req = http.expectOne('/api/v1/config');
    expect(req.request.method).toBe('PUT');
    expect(req.request.body).toEqual({ settings: { hostname: 'hs' } });
    req.flush({ version: 1, settings: { hostname: 'hs' } });
  });
  it('loads wizard progress', () => {
    service.wizardState('claim').subscribe((state) => {
      expect(state.step).toBe('admin');
    });
    const req = http.expectOne('/api/v1/wizards/claim');
    expect(req.request.method).toBe('GET');
    req.flush({ wizard: 'claim', step: 'admin', payload: {}, updated_at: '' });
  });
  it('saves wizard progress', () => {
    service.saveWizardState('claim', 'done', {}).subscribe((state) => {
      expect(state.step).toBe('done');
    });
    const req = http.expectOne('/api/v1/wizards/claim');
    expect(req.request.method).toBe('PUT');
    expect(req.request.body).toEqual({ step: 'done', payload: {} });
    req.flush({ wizard: 'claim', step: 'done', payload: {}, updated_at: '' });
  });

  it('clears wizard progress', () => {
    service.clearWizardState('claim').subscribe();
    const req = http.expectOne('/api/v1/wizards/claim');
    expect(req.request.method).toBe('DELETE');
    req.flush(null);
  });

  it('fetches firewall status', () => {
    service.firewallStatus().subscribe((status) => {
      expect(status.daemon_port).toBe(8080);
      expect(status.local_subnets).toEqual(['192.168.1.0/24']);
    });
    const req = http.expectOne('/api/v1/firewall/status');
    expect(req.request.method).toBe('GET');
    req.flush({
      nft_available: true,
      ruleset_accessible: true,
      managed: false,
      rule_count: 0,
      daemon_port: 8080,
      local_subnets: ['192.168.1.0/24'],
    });
  });

  it('fetches the firewall plan and applies it', () => {
    service.firewallPlan().subscribe((plan) => {
      expect(plan.in_sync).toBe(false);
    });
    http.expectOne('/api/v1/firewall/plan').flush({
      in_sync: false,
      changes: [],
      script: 'table',
    });
    service.firewallApply().subscribe((result) => {
      expect(result.applied).toBe(true);
    });
    const req = http.expectOne('/api/v1/firewall/apply');
    expect(req.request.method).toBe('POST');
    req.flush({ applied: true });
  });
});
