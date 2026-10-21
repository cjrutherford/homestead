import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute, convertToParamMap, provideRouter, Router } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Login } from './login';
import { SESSION_STORAGE_KEY } from '../auth/session.service';

describe('Login', () => {
  let fixture: ComponentFixture<Login>;
  let http: HttpTestingController;

  beforeEach(async () => {
    localStorage.clear();
    await TestBed.configureTestingModule({
      imports: [Login],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(Login);
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
    http.expectOne('/api/v1/sso/options').flush({ enabled: false });
  });

  afterEach(() => {
    http.verify();
    localStorage.clear();
  });

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  it('logs in and navigates to status', () => {
    const navigate = vi
      .spyOn(TestBed.inject(Router), 'navigateByUrl')
      .mockResolvedValue(true);
    fixture.componentInstance['submit']('owner', 'long-enough-password');
    const req = http.expectOne('/api/v1/sessions');
    expect(req.request.method).toBe('POST');
    req.flush({ token: 'session-token', expires_at: '2026-01-01T00:00:00Z' });
    expect(navigate).toHaveBeenCalledWith('/status');
    fixture.detectChanges();
    expect(localStorage.getItem(SESSION_STORAGE_KEY)).not.toContain('session-token');
    expect(localStorage.getItem(SESSION_STORAGE_KEY)).toContain('owner');
    expect(text()).toContain('Already logged in as');
  });

  it('reports bad credentials', () => {
    fixture.componentInstance['submit']('owner', 'wrong-password-here');
    const req = http.expectOne('/api/v1/sessions');
    req.flush('nope', { status: 401, statusText: 'Unauthorized' });
    fixture.detectChanges();
    expect(text()).toContain('Invalid username or password');
  });

  it('explains a lockout on 429', () => {
    fixture.componentInstance['submit']('owner', 'wrong-password-here');
    http
      .expectOne('/api/v1/sessions')
      .flush('account temporarily locked after repeated failures', {
        status: 429,
        statusText: 'Too Many Requests',
      });
    fixture.detectChanges();
    expect(text()).toContain('temporarily locked');
    expect(text()).not.toContain('reachable');
  });

  it('says the appliance is unreachable on status 0', () => {
    fixture.componentInstance['submit']('owner', 'wrong-password-here');
    http.expectOne('/api/v1/sessions').error(new ProgressEvent('error'), { status: 0 });
    fixture.detectChanges();
    expect(text()).toContain("Can't reach the appliance");
  });

  it('requires both fields without calling the API', () => {
    fixture.componentInstance['submit']('', '');
    http.expectNone('/api/v1/sessions');
    fixture.detectChanges();
    expect(text()).toContain('required');
  });
});

describe('Login with Authentik', () => {
  let http: HttpTestingController;

  async function open(query: Record<string, string> = {}): Promise<ComponentFixture<Login>> {
    localStorage.clear();
    await TestBed.configureTestingModule({
      imports: [Login],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: ActivatedRoute, useValue: { snapshot: { queryParamMap: convertToParamMap(query) } } },
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(Login);
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
    return fixture;
  }

  afterEach(() => {
    http.verify();
    localStorage.clear();
  });

  const text = (f: ComponentFixture<Login>) => f.nativeElement.textContent as string;

  it('shows Sign in with Authentik beside the password form when the daemon offers it', async () => {
    const fixture = await open();
    http.expectOne('/api/v1/sso/options').flush({ enabled: true });
    fixture.detectChanges();
    const link = fixture.nativeElement.querySelector('a[href="/api/v1/sso/login"]') as HTMLAnchorElement;
    expect(link.textContent).toContain('Sign in with Authentik');
    expect(fixture.nativeElement.querySelector('form[aria-label="Login"]')).not.toBeNull();
    expect(text(fixture)).toContain('local password keeps working');
  });

  it('offers only Authentik on the public dashboard name', async () => {
    const fixture = await open();
    http.expectOne('/api/v1/sso/options').flush({ enabled: true, password: false });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('a[href="/api/v1/sso/login"]')).not.toBeNull();
    expect(fixture.nativeElement.querySelector('form[aria-label="Login"]')).toBeNull();
    expect(text(fixture)).toContain('only Authentik signs you in here');
  });

  it('leaves the button out when the daemon says no', async () => {
    const fixture = await open();
    http.expectOne('/api/v1/sso/options').flush({ enabled: false });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('a[href="/api/v1/sso/login"]')).toBeNull();
  });

  it('leaves the button out on 404 (not set up) and on an unreachable appliance, form intact', async () => {
    const fixture = await open();
    http.expectOne('/api/v1/sso/options').flush('not found', { status: 404, statusText: 'Not Found' });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('a[href="/api/v1/sso/login"]')).toBeNull();
    expect(fixture.nativeElement.querySelector('form[aria-label="Login"]')).not.toBeNull();
    expect(text(fixture)).not.toContain('reachable');
  });

  it('says the fixed sentence when Authentik did not answer, and still offers both ways in', async () => {
    const fixture = await open({ sso_error: 'unavailable' });
    http.expectOne('/api/v1/sso/options').flush({ enabled: true });
    fixture.detectChanges();
    expect(text(fixture)).toContain('Authentik did not answer. Use your local password.');
    expect(fixture.nativeElement.querySelector('form[aria-label="Login"]')).not.toBeNull();
    expect(fixture.nativeElement.querySelector('a[href="/api/v1/sso/login"]')).not.toBeNull();
  });

  it.each([
    ['unlinked', 'not linked to this appliance'],
    ['state', 'expired or was already used'],
    ['origin', 'not set up for this address'],
    ['rate-limited', 'Too many sign-in attempts'],
    ['token', 'did not finish'],
  ])('explains the %s code', async (code, words) => {
    const fixture = await open({ sso_error: code });
    http.expectOne('/api/v1/sso/options').flush({ enabled: true });
    fixture.detectChanges();
    expect(text(fixture)).toContain(words);
    expect(text(fixture)).toContain('local password');
  });

  it('learns who signed in after the callback and continues to status', async () => {
    const fixture = await open({ sso: 'done' });
    const navigate = vi.spyOn(TestBed.inject(Router), 'navigateByUrl').mockResolvedValue(true);
    http.expectOne('/api/v1/admins/lockout').flush({ username: 'owner', locked: false, failures: 0 });
    expect(navigate).toHaveBeenCalledWith('/status');
    expect(localStorage.getItem(SESSION_STORAGE_KEY)).toContain('owner');
    // No options probe once the callback finished.
    http.expectNone('/api/v1/sso/options');
  });

  it('says so when the session cannot be confirmed', async () => {
    const fixture = await open({ sso: 'done' });
    http.expectOne('/api/v1/admins/lockout').flush('no', { status: 401, statusText: 'Unauthorized' });
    fixture.detectChanges();
    expect(text(fixture)).toContain('could not confirm the session');
  });
});
