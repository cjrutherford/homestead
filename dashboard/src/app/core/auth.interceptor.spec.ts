import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { HttpClient, provideHttpClient, withInterceptors } from '@angular/common/http';
import {
  HttpTestingController,
  provideHttpClientTesting,
} from '@angular/common/http/testing';
import { authInterceptor, readCookie } from './auth.interceptor';
import { SESSION_STORAGE_KEY, SessionService } from '../auth/session.service';

describe('authInterceptor', () => {
  let http: HttpClient;
  let ctrl: HttpTestingController;
  let router: Router;

  beforeEach(() => {
    localStorage.clear();
    document.cookie = 'homestead_csrf=csrf-abc; path=/';
    TestBed.configureTestingModule({
      providers: [
        provideRouter([]),
        provideHttpClient(withInterceptors([authInterceptor])),
        provideHttpClientTesting(),
      ],
    });
    http = TestBed.inject(HttpClient);
    ctrl = TestBed.inject(HttpTestingController);
    router = TestBed.inject(Router);
  });

  afterEach(() => {
    ctrl.verify();
    document.cookie = 'homestead_csrf=; path=/; max-age=0';
    localStorage.clear();
  });

  it('echoes the CSRF cookie on every mutating API call', () => {
    for (const method of ['POST', 'PUT', 'PATCH', 'DELETE']) {
      http.request(method, '/api/v1/config').subscribe();
      const req = ctrl.expectOne('/api/v1/config');
      expect(req.request.headers.get('X-CSRF-Token')).toBe('csrf-abc');
      req.flush(null);
    }
  });

  it('does not send CSRF on reads or non-API URLs and never an Authorization header', () => {
    http.get('/api/v1/config').subscribe();
    const read = ctrl.expectOne('/api/v1/config');
    expect(read.request.headers.has('X-CSRF-Token')).toBe(false);
    expect(read.request.headers.has('Authorization')).toBe(false);
    read.flush({});
    http.post('/elsewhere', {}).subscribe();
    const other = ctrl.expectOne('/elsewhere');
    expect(other.request.headers.has('X-CSRF-Token')).toBe(false);
    other.flush({});
  });

  it('expires the session and routes to login on a 401', () => {
    const sessions = TestBed.inject(SessionService);
    localStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify({ username: 'o', expiresAt: '' }));
    const nav = vi.spyOn(router, 'navigateByUrl').mockResolvedValue(true);
    const expire = vi.spyOn(sessions, 'expire');
    http.get('/api/v1/config').subscribe({ error: () => undefined });
    ctrl.expectOne('/api/v1/config').flush('no', { status: 401, statusText: 'Unauthorized' });
    expect(expire).toHaveBeenCalled();
    expect(nav).toHaveBeenCalledWith('/login');
  });

  it('leaves failed logins to the login page', () => {
    const nav = vi.spyOn(router, 'navigateByUrl').mockResolvedValue(true);
    http.post('/api/v1/sessions', {}).subscribe({ error: () => undefined });
    ctrl.expectOne('/api/v1/sessions').flush('no', { status: 401, statusText: 'Unauthorized' });
    expect(nav).not.toHaveBeenCalled();
  });

  it('keeps the session on a wrong-current-password 401', () => {
    const nav = vi.spyOn(router, 'navigateByUrl').mockResolvedValue(true);
    const expire = vi.spyOn(TestBed.inject(SessionService), 'expire');
    http.post('/api/v1/admins/password', {}).subscribe({ error: () => undefined });
    ctrl.expectOne('/api/v1/admins/password').flush('no', {
      status: 401,
      statusText: 'Unauthorized',
      headers: { 'X-Homestead-Error': 'invalid-current-password' },
    });
    expect(expire).not.toHaveBeenCalled();
    expect(nav).not.toHaveBeenCalled();
  });

  it('still expires the session when the coded header rides another route', () => {
    localStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify({ username: 'o', expiresAt: '' }));
    const nav = vi.spyOn(router, 'navigateByUrl').mockResolvedValue(true);
    http.get('/api/v1/config').subscribe({ error: () => undefined });
    ctrl.expectOne('/api/v1/config').flush('no', {
      status: 401,
      statusText: 'Unauthorized',
      headers: { 'X-Homestead-Error': 'invalid-current-password' },
    });
    expect(nav).toHaveBeenCalledWith('/login');
  });

  it('parses cookies', () => {
    expect(readCookie('b', 'a=1; b=two%20words; c=3')).toBe('two words');
    expect(readCookie('x', 'a=1')).toBeNull();
  });
});
