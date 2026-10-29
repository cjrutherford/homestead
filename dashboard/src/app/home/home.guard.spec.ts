import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { Router, UrlTree, provideRouter } from '@angular/router';
import { Observable, firstValueFrom, isObservable } from 'rxjs';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { SessionService } from '../auth/session.service';
import { setupStatus } from '../setup/setup.testing';
import { homeGuard, ownerGuard } from './home.guard';

type Verdict = boolean | UrlTree;

describe('home and owner guards (S339)', () => {
  let http: HttpTestingController;

  beforeEach(() => {
    localStorage.clear();
    TestBed.configureTestingModule({ providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()] });
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => localStorage.clear());

  const signIn = () => TestBed.inject(SessionService).begin('sam', '');
  const url = (v: Verdict) => (v === true ? 'true' : TestBed.inject(Router).serializeUrl(v as UrlTree));

  async function run(guard: typeof homeGuard, answers: Record<string, object>): Promise<Verdict> {
    const result = TestBed.runInInjectionContext(() => guard({} as never, {} as never));
    if (!isObservable(result)) {
      return result as Verdict;
    }
    const pending = firstValueFrom(result as Observable<Verdict>);
    for (let i = 0; i < 3; i++) {
      await new Promise((resolve) => setTimeout(resolve));
      for (const [path, body] of Object.entries(answers)) {
        http.match(path).forEach((req) => req.flush(body));
      }
    }
    return pending;
  }

  it('sends a signed-out visitor at the root to sign-in', async () => {
    expect(url(await run(homeGuard, {}))).toBe('/login');
  });

  it('lets a person onto the apps page without asking the walkthrough', async () => {
    signIn();
    expect(await run(homeGuard, { '/api/v1/me': { role: 'person' } })).toBe(true);
    http.expectNone('/api/v1/setup');
  });

  it('holds a gated owner at /setup', async () => {
    signIn();
    const verdict = await run(homeGuard, { '/api/v1/me': { role: 'owner' }, '/api/v1/setup': setupStatus() });
    expect(url(verdict)).toBe('/setup');
  });

  it('sends a person away from /admin to the apps page', async () => {
    signIn();
    expect(url(await run(ownerGuard, { '/api/v1/me': { role: 'person' } }))).toBe('/');
  });

  it('lets the owner into /admin, and does not ask again once the role is known', async () => {
    signIn();
    expect(await run(ownerGuard, { '/api/v1/me': { role: 'owner' } })).toBe(true);
    expect(await run(ownerGuard, {})).toBe(true);
    http.verify();
  });

  it('lets a signed-out visitor reach the public status page', async () => {
    expect(await run(ownerGuard, {})).toBe(true);
  });
});
