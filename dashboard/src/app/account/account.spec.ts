import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { provideRouter, Router } from '@angular/router';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Account } from './account';
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
    expect(text()).toContain('at least 12 characters');
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
    const navigate = vi_spy(router);
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
    expect(navigate.calls).toBe(0);
    expect(localStorage.getItem(SESSION_STORAGE_KEY)).not.toBeNull();
    expect(TestBed.inject(SessionService).isLoggedIn()).toBe(true);
  });

  it('redirects to login on a boundary 401', () => {
    localStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify({ username: 'owner', expiresAt: '2099-01-01T00:00:00Z' }));
    const navigate = vi_spy(router);
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
    expect(navigate.calls).toBeGreaterThan(0);
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
});

/** Counts navigations without performing them. */
function vi_spy(router: Router): { calls: number } {
  const counter = { calls: 0 };
  router.navigateByUrl = (() => {
    counter.calls++;
    return Promise.resolve(true);
  }) as Router['navigateByUrl'];
  return counter;
}
