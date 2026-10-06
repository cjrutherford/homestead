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

  it('fetches desired configuration', () => {
    service.config().subscribe((config) => {
      expect(config.version).toBe(1);
      expect(config.settings).toEqual({});
    });
    const req = http.expectOne('/api/v1/config');
    expect(req.request.method).toBe('GET');
    req.flush({ version: 1, settings: {} });
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
});
