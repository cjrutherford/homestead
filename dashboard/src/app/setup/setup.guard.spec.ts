import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { Router, UrlTree, provideRouter } from '@angular/router';
import { Observable, firstValueFrom, isObservable } from 'rxjs';
import { SESSION_STORAGE_KEY } from '../auth/session.service';
import { routes } from '../app.routes';
import { SETUP_EXEMPT_PATHS, setupGuard } from './setup.guard';
import { setupStatus } from './setup.testing';

async function runGuard(http: HttpTestingController, body?: object, fail = false): Promise<boolean | UrlTree> {
  const result = TestBed.runInInjectionContext(() => setupGuard({} as never, {} as never));
  if (!isObservable(result)) {
    return result as boolean | UrlTree;
  }
  const pending = firstValueFrom(result as Observable<boolean | UrlTree>);
  const req = http.expectOne('/api/v1/setup');
  if (fail) {
    req.flush('boom', { status: 500, statusText: 'Server Error' });
  } else {
    req.flush(body ?? null);
  }
  return pending;
}

describe('setup guard (S287)', () => {
  let http: HttpTestingController;

  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify({ username: 'owner', expiresAt: '' }));
    TestBed.configureTestingModule({
      providers: [provideRouter(routes), provideHttpClient(), provideHttpClientTesting()],
    });
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => localStorage.clear());

  it('sends a gated owner to /setup', async () => {
    const result = await runGuard(http, setupStatus());
    expect(result instanceof UrlTree).toBe(true);
    expect(TestBed.inject(Router).serializeUrl(result as UrlTree)).toBe('/setup');
  });

  it('lets a legacy appliance through, whatever is unfinished', async () => {
    const result = await runGuard(http, setupStatus({ legacy: true, origin: 'legacy', gated: false }));
    expect(result).toBe(true);
  });

  it('lets an owner through once every step is done or skipped', async () => {
    expect(await runGuard(http, setupStatus({ complete: true, gated: false, current: '' }))).toBe(true);
  });

  it('never locks the owner out when the status cannot be read', async () => {
    expect(await runGuard(http, undefined, true)).toBe(true);
  });

  it('asks nothing of a visitor who is not logged in', async () => {
    localStorage.clear();
    TestBed.resetTestingModule();
    TestBed.configureTestingModule({
      providers: [provideRouter(routes), provideHttpClient(), provideHttpClientTesting()],
    });
    const result = TestBed.runInInjectionContext(() => setupGuard({} as never, {} as never));
    expect(result).toBe(true);
    TestBed.inject(HttpTestingController).expectNone('/api/v1/setup');
  });
});

describe('setup exemptions (S287)', () => {
  it('leaves exactly the walkthrough pages, the way out and the claim page open', () => {
    expect([...SETUP_EXEMPT_PATHS].sort()).toEqual(
      ['account', 'backups', 'catalog/:name/install', 'claim', 'exposure', 'login', 'operations', 'operations/:id', 'setup', 'sign-in', 'storage', 'edge', 'trust', 'tls', 'tunnel'].sort(),
    );
  });

  it('guards every other page, and not redirects or the wildcard', () => {
    for (const route of routes) {
      const path = route.path ?? '';
      const guarded = route.canActivate?.includes(setupGuard) ?? false;
      if (route.redirectTo !== undefined || path === '**' || SETUP_EXEMPT_PATHS.includes(path)) {
        expect(guarded, path).toBe(false);
      } else {
        expect(guarded, path).toBe(true);
      }
    }
  });

  it('keeps every exempt path a real route', () => {
    const paths = routes.map((route) => route.path);
    for (const path of SETUP_EXEMPT_PATHS) {
      expect(paths, path).toContain(path);
    }
  });
});

describe('setup gate in navigation (S287)', () => {
  let http: HttpTestingController;
  let router: Router;

  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify({ username: 'owner', expiresAt: '' }));
    TestBed.configureTestingModule({
      providers: [provideRouter(routes), provideHttpClient(), provideHttpClientTesting()],
    });
    http = TestBed.inject(HttpTestingController);
    router = TestBed.inject(Router);
  });

  afterEach(() => localStorage.clear());

  async function go(url: string, status: object): Promise<string> {
    const navigation = router.navigateByUrl(url);
    await new Promise((resolve) => setTimeout(resolve));
    http.match('/api/v1/setup').forEach((req) => req.flush(status));
    await navigation;
    return router.url;
  }

  it('redirects a gated owner from a gated page to /setup', async () => {
    expect(await go('/firewall', setupStatus())).toBe('/setup');
  });

  it('lets a gated owner reach an exempt page the walkthrough links to', async () => {
    const navigation = router.navigateByUrl('/sign-in');
    await new Promise((resolve) => setTimeout(resolve));
    http.match('/api/v1/setup').forEach((req) => req.flush(setupStatus()));
    await navigation;
    expect(router.url).toBe('/sign-in');
  });

  it('does not hold a legacy appliance', async () => {
    expect(await go('/firewall', setupStatus({ legacy: true, origin: 'legacy', gated: false }))).toBe('/firewall');
  });
});
