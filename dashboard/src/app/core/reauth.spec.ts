import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { TestBed } from '@angular/core/testing';
import { HttpClient, provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { REAUTH_HEADER, Reauth, reauthInterceptor } from './reauth';

describe('reauthInterceptor', () => {
  let http: HttpClient;
  let backend: HttpTestingController;
  let reauth: Reauth;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [provideHttpClient(withInterceptors([reauthInterceptor])), provideHttpClientTesting()],
    });
    http = TestBed.inject(HttpClient);
    backend = TestBed.inject(HttpTestingController);
    reauth = TestBed.inject(Reauth);
  });

  afterEach(() => backend.verify());

  const refuse = (code: string) =>
    ({ status: 403, statusText: 'Forbidden', headers: { 'X-Homestead-Error': code } });

  it('asks for the local password and retries the same request with it', async () => {
    let result: unknown = null;
    http.post('/api/v1/sso/link', {}).subscribe((r) => (result = r));
    backend.expectOne('/api/v1/sso/link').flush('enter your local password', refuse('reauth-required'));
    await Promise.resolve();
    expect(reauth.request()?.invalid).toBe(false);
    reauth.request()?.answer('owner-password');
    const retry = backend.expectOne('/api/v1/sso/link');
    expect(retry.request.headers.get(REAUTH_HEADER)).toBe('owner-password');
    expect(retry.request.method).toBe('POST');
    retry.flush({ url: 'https://authentik.lan/x' });
    expect(result).toEqual({ url: 'https://authentik.lan/x' });
    expect(reauth.request()).toBeNull();
  });

  it('asks again, saying so, when the password was wrong', () => {
    http.delete('/api/v1/sso/link').subscribe();
    backend.expectOne('/api/v1/sso/link').flush('no', refuse('reauth-required'));
    reauth.request()?.answer('wrong');
    backend.expectOne('/api/v1/sso/link').flush('no', refuse('reauth-invalid'));
    expect(reauth.request()?.invalid).toBe(true);
    reauth.request()?.answer('right');
    const last = backend.expectOne('/api/v1/sso/link');
    expect(last.request.headers.get(REAUTH_HEADER)).toBe('right');
    last.flush({});
  });

  it('passes the original refusal on when the owner cancels', () => {
    let status = 0;
    http.post('/api/v1/sso/plan', {}).subscribe({ error: (e) => (status = e.status) });
    backend.expectOne('/api/v1/sso/plan').flush('no', refuse('reauth-required'));
    reauth.request()?.answer(null);
    backend.expectNone('/api/v1/sso/plan');
    expect(status).toBe(403);
    expect(reauth.request()).toBeNull();
  });

  it('leaves every other refusal alone', () => {
    let status = 0;
    http.get('/api/v1/audit').subscribe({ error: (e) => (status = e.status) });
    backend.expectOne('/api/v1/audit').flush('csrf', { status: 403, statusText: 'Forbidden' });
    expect(status).toBe(403);
    expect(reauth.request()).toBeNull();
  });

  it('does not retry a locked account', () => {
    let status = 0;
    http.post('/api/v1/sso/link', {}).subscribe({ error: (e) => (status = e.status) });
    backend
      .expectOne('/api/v1/sso/link')
      .flush('locked', { status: 429, statusText: 'Too Many', headers: { 'X-Homestead-Error': 'reauth-locked' } });
    expect(status).toBe(429);
    expect(reauth.request()).toBeNull();
  });

  it('shows the Authentik step-up, not the password, on stepup-required, and does not retry', () => {
    let status = 0;
    http.post('/api/v1/people/1/remove', {}).subscribe({ error: (e) => (status = e.status) });
    backend.expectOne('/api/v1/people/1/remove').flush('no', refuse('stepup-required'));
    expect(reauth.request()?.stepUp).toBe(true);
    reauth.request()?.answer(null);
    backend.expectNone('/api/v1/people/1/remove');
    expect(status).toBe(403);
    expect(reauth.request()).toBeNull();
  });

  it('shows the local-only notice, not the password, on local-only, and does not retry', () => {
    let status = 0;
    http.post('/api/v1/sso/plan', {}).subscribe({ error: (e) => (status = e.status) });
    backend.expectOne('/api/v1/sso/plan').flush('no', refuse('local-only'));
    expect(reauth.request()?.localOnly).toBe(true);
    expect(reauth.request()?.stepUp).toBe(false);
    reauth.request()?.answer(null);
    backend.expectNone('/api/v1/sso/plan');
    expect(status).toBe(403);
    expect(reauth.request()).toBeNull();
  });
});
