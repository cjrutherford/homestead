import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { provideRouter, Router } from '@angular/router';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Account, byteLength, relativeUntil } from './account';
import { authInterceptor } from '../core/auth.interceptor';
import { SESSION_STORAGE_KEY, SessionService } from '../auth/session.service';

describe('Account', () => {
  let fixture: ComponentFixture<Account>;
  let http: HttpTestingController;
  let router: Router;

  const start = (lockout: unknown = { username: 'owner', locked: false }) => {
    fixture = TestBed.createComponent(Account);
    fixture.detectChanges();
    if (lockout !== undefined) {
      http.expectOne('/api/v1/admins/lockout').flush(lockout);
    }
    fixture.detectChanges();
  };

  beforeEach(() => {
    localStorage.clear();
    TestBed.configureTestingModule({
      imports: [Account],
      providers: [
        provideRouter([]),
        provideHttpClient(withInterceptors([authInterceptor])),
        provideHttpClientTesting(),
      ],
    });
    http = TestBed.inject(HttpTestingController);
    router = TestBed.inject(Router);
  });
  afterEach(() => {
    vi.restoreAllMocks();
    http.verify();
    localStorage.clear();
  });

  const text = () => fixture.nativeElement.textContent as string;
  const fill = (cur: string, nw: string, cf: string) => {
    const set = (id: string, v: string) => {
      (fixture.nativeElement.querySelector('#' + id) as HTMLInputElement).value = v;
    };
    set('account-current', cur);
    set('account-new', nw);
    set('account-confirm', cf);
    (fixture.nativeElement.querySelector('form') as HTMLFormElement).dispatchEvent(
      new Event('submit'),
    );
    fixture.detectChanges();
  };

  it('shows loading, then an unlocked account', () => {
    fixture = TestBed.createComponent(Account);
    fixture.detectChanges();
    expect(text()).toContain('Loading lockout status');
    http.expectOne('/api/v1/admins/lockout').flush({ username: 'owner', locked: false });
    fixture.detectChanges();
    expect(text()).toContain('owner');
    expect(text()).toContain('not locked');
  });

  it('shows a locked account', () => {
    start({ username: 'owner', locked: true });
    expect(text()).toContain('temporarily locked');
  });

  it('shows an error with retry when lockout fails', () => {
    fixture = TestBed.createComponent(Account);
    fixture.detectChanges();
    http.expectOne('/api/v1/admins/lockout').error(new ProgressEvent('error'));
    fixture.detectChanges();
    expect(text()).toContain('Cannot reach the Homestead daemon');
    expect(text()).toContain('Retry');
  });

  it('shows empty state for a null body', () => {
    start(null);
    expect(text()).toContain('No lockout information');
  });

  it('validates length, match and presence without calling the API', () => {
    start();
    fill('', 'short', 'other');
    expect(text()).toContain('Enter your current password.');
    expect(text()).toContain('at least 12 bytes');
    expect(text()).toContain('do not match');
  });

  it('changes the password, clears the form and explains other sessions', () => {
    localStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify({ username: 'owner', expiresAt: '2099-01-01T00:00:00Z' }));
    start();
    fill('old-password-1', 'new-password-12', 'new-password-12');
    const req = http.expectOne('/api/v1/admins/password');
    expect(req.request.body).toEqual({
      current_password: 'old-password-1',
      new_password: 'new-password-12',
    });
    req.flush({ id: 1, username: 'owner' });
    http.expectOne('/api/v1/admins/lockout').flush({ username: 'owner', locked: false });
    fixture.detectChanges();
    expect(text()).toContain('other sessions were signed out');
    for (const id of ['account-current', 'account-new', 'account-confirm']) {
      expect((fixture.nativeElement.querySelector('#' + id) as HTMLInputElement).value).toBe('');
    }
    expect(text()).not.toContain('new-password-12');
  });

  it('shows a wrong current password inline and stays signed in', () => {
    localStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify({ username: 'owner', expiresAt: '2099-01-01T00:00:00Z' }));
    const navigate = vi.spyOn(router, 'navigateByUrl').mockResolvedValue(true);
    start();
    fill('wrong', 'new-password-12', 'new-password-12');
    http.expectOne('/api/v1/admins/password').flush('invalid username or password', {
      status: 401,
      statusText: 'Unauthorized',
      headers: { 'X-Homestead-Error': 'invalid-current-password' },
    });
    http.expectOne('/api/v1/admins/lockout').flush({ username: 'owner', locked: false });
    fixture.detectChanges();
    expect(text()).toContain('current password is incorrect');
    expect(navigate).not.toHaveBeenCalled();
    expect(localStorage.getItem(SESSION_STORAGE_KEY)).not.toBeNull();
    expect(TestBed.inject(SessionService).isLoggedIn()).toBe(true);
  });

  it('redirects to login on a boundary 401', () => {
    localStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify({ username: 'owner', expiresAt: '2099-01-01T00:00:00Z' }));
    const navigate = vi.spyOn(router, 'navigateByUrl').mockResolvedValue(true);
    start();
    fill('old-password-1', 'new-password-12', 'new-password-12');
    http.expectOne('/api/v1/admins/password').flush('unauthorized', {
      status: 401,
      statusText: 'Unauthorized',
    });
    http.expectOne('/api/v1/admins/lockout').flush('unauthorized', {
      status: 401,
      statusText: 'Unauthorized',
    });
    expect(navigate).toHaveBeenCalledWith('/login');
    expect(localStorage.getItem(SESSION_STORAGE_KEY)).toBeNull();
  });

  it('explains a 429 lockout', () => {
    start();
    fill('old-password-1', 'new-password-12', 'new-password-12');
    http.expectOne('/api/v1/admins/password').flush('locked', {
      status: 429,
      statusText: 'Too Many Requests',
    });
    http.expectOne('/api/v1/admins/lockout').flush({ username: 'owner', locked: true });
    fixture.detectChanges();
    expect(text()).toContain('temporarily locked after repeated failures');
  });

  const expectCleared = () => {
    for (const id of ['account-current', 'account-new', 'account-confirm']) {
      expect((fixture.nativeElement.querySelector('#' + id) as HTMLInputElement).value).toBe('');
    }
  };

  it('clears the fields after a validation failure', () => {
    start();
    fill('x', 'short', 'short');
    expectCleared();
  });

  it('clears the fields after 401, 429 and 422', () => {
    for (const [status, headers] of [
      [401, { 'X-Homestead-Error': 'invalid-current-password' }],
      [429, {}],
      [422, {}],
    ] as const) {
      start();
      fill('old-password-1', 'new-password-12', 'new-password-12');
      http.expectOne('/api/v1/admins/password').flush('refused', { status, statusText: 'x', headers });
      http.expectOne('/api/v1/admins/lockout').flush({ username: 'owner', locked: false });
      fixture.detectChanges();
      expectCleared();
      fixture.destroy();
    }
  });

  it('counts UTF-8 bytes against the 12 to 72 byte window', () => {
    expect(byteLength('é')).toBe(2);
    start();
    // 6 two-byte characters: 6 characters but 12 bytes, so it passes.
    fill('old-password-1', 'éééééé', 'éééééé');
    http.expectOne('/api/v1/admins/password').flush({ id: 1, username: 'owner' });
    http.expectOne('/api/v1/admins/lockout').flush({ username: 'owner', locked: false });
    fixture.detectChanges();
    // 5 two-byte characters is only 10 bytes.
    fill('old-password-1', 'ééééé', 'ééééé');
    expect(text()).toContain('at least 12 bytes');
  });

  it('rejects more than 72 bytes without calling the API', () => {
    start();
    fill('old-password-1', 'é'.repeat(37), 'é'.repeat(37));
    expect(text()).toContain('at most 72 bytes');
    fill('old-password-1', 'a'.repeat(73), 'a'.repeat(73));
    expect(text()).toContain('at most 72 bytes');
  });

  it('shows a relative unlock time and failure count', () => {
    const until = new Date(Date.now() + 4 * 60000 + 1000).toISOString();
    start({ username: 'owner', locked: true, failures: 5, locked_until: until });
    expect(text()).toContain('unlocks in 5 minutes');
    expect(text()).toContain('failed attempts: 5');
    expect(relativeUntil(new Date(Date.now() + 3600001).toISOString())).toBe('in 2 hours');
  });

  it('pairs the credential with a readonly username field', () => {
    start();
    const user = fixture.nativeElement.querySelector('input[autocomplete="username"]');
    expect(user.readOnly).toBe(true);
    expect(fixture.nativeElement.querySelector('#account-current').autocomplete).toBe('current-password');
    expect(fixture.nativeElement.querySelector('#account-new').autocomplete).toBe('new-password');
  });
});
