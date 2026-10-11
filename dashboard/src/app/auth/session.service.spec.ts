import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { SessionService, SESSION_STORAGE_KEY } from './session.service';

describe('SessionService', () => {
  let service: SessionService;
  let http: HttpTestingController;

  beforeEach(() => {
    localStorage.clear();
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    service = TestBed.inject(SessionService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    http.verify();
    localStorage.clear();
  });

  it('starts logged out with no stored session', () => {
    expect(service.isLoggedIn()).toBe(false);
  });

  it('stores the session and persists it on login', () => {
    let done = false;
    service.login('owner', 'long-enough-password').subscribe(() => {
      done = true;
    });
    const req = http.expectOne('/api/v1/sessions');
    expect(req.request.method).toBe('POST');
    req.flush({ token: 'session-token', expires_at: '2026-01-01T00:00:00Z' });
    expect(done).toBe(true);
    expect(service.isLoggedIn()).toBe(true);
    expect(service.username()).toBe('owner');
    const stored = JSON.parse(localStorage.getItem(SESSION_STORAGE_KEY) ?? '{}');
    expect(stored.token).toBeUndefined(); // credential is the HttpOnly cookie
    expect(stored.username).toBe('owner');
  });

  it('restores a stored session without logging in', () => {
    localStorage.setItem(
      SESSION_STORAGE_KEY,
      JSON.stringify({ username: 'owner', expiresAt: '' }),
    );
    TestBed.resetTestingModule();
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    http = TestBed.inject(HttpTestingController);
    const fresh = TestBed.inject(SessionService);
    expect(fresh.isLoggedIn()).toBe(true);
    expect(fresh.username()).toBe('owner');
  });

  it('drops pre-S256 stored entries that held a token', () => {
    localStorage.setItem(
      SESSION_STORAGE_KEY,
      JSON.stringify({ token: 'legacy', expiresAt: '' }),
    );
    TestBed.resetTestingModule();
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    expect(TestBed.inject(SessionService).isLoggedIn()).toBe(false);
  });

  it('clears locally and destroys server-side on logout', () => {
    service.login('owner', 'long-enough-password').subscribe();
    http.expectOne('/api/v1/sessions').flush({
      token: 'session-token',
      expires_at: '2026-01-01T00:00:00Z',
    });
    expect(service.isLoggedIn()).toBe(true);
    service.logout().subscribe();
    const req = http.expectOne('/api/v1/sessions');
    expect(req.request.method).toBe('DELETE');
    expect(req.request.headers.has('Authorization')).toBe(false);
    req.flush(null);
    expect(service.isLoggedIn()).toBe(false);
    expect(localStorage.getItem(SESSION_STORAGE_KEY)).toBeNull();
  });

  it('clears locally even when the server call fails', () => {
    service.login('owner', 'long-enough-password').subscribe();
    http.expectOne('/api/v1/sessions').flush({
      token: 'session-token',
      expires_at: '2026-01-01T00:00:00Z',
    });
    let done = false;
    service.logout().subscribe(() => {
      done = true;
    });
    http.expectOne('/api/v1/sessions').flush('gone', {
      status: 401,
      statusText: 'Unauthorized',
    });
    expect(done).toBe(true);
    expect(service.isLoggedIn()).toBe(false);
  });
});
