import { Location } from '@angular/common';
import { TestBed } from '@angular/core/testing';
import { provideRouter, Route, Router } from '@angular/router';
import { describe, expect, it } from 'vitest';
import { Account } from './account/account';
import { Claim } from './claim/claim';
import { Config } from './config/config';
import { Home } from './home/home';
import { Login } from './login/login';
import { NotFound } from './not-found/not-found';
import { routes } from './app.routes';
import { Status } from './status/status';

const admin = routes.find((route) => route.path === 'admin') as Route;
const adminChild = (path: string) => admin.children?.find((route) => route.path === path);

/** Every page that moved under /admin (ADR-050 §4, S339). */
const ADMIN_PATHS = [
  'status',
  'config',
  'firewall',
  'catalog',
  'catalog/:name/install',
  'updates',
  'data',
  'advanced',
  'doctor',
  'people',
  'drift',
  'exposure',
  'tunnel',
  'names',
  'backups',
  'storage',
  'edge',
  'tls',
  'trust',
  'craft-apps',
  'craft-files',
  'audit',
  'secrets',
  'operations',
  'operations/:id',
  'validation-board',
];

describe('routes', () => {
  it('serves the apps page at the root', () => {
    const root = routes.find((route) => route.path === '');
    expect(root?.component).toBe(Home);
    expect(root?.canActivate?.length).toBe(1);
  });

  it('keeps setup, claim, login, sign-in and account at the root', () => {
    expect(routes.find((route) => route.path === 'claim')?.component).toBe(Claim);
    expect(routes.find((route) => route.path === 'login')?.component).toBe(Login);
    expect(routes.find((route) => route.path === 'account')?.component).toBe(Account);
    expect(routes.find((route) => route.path === 'setup')?.loadComponent).toBeTypeOf('function');
    expect(routes.find((route) => route.path === 'sign-in')?.loadComponent).toBeTypeOf('function');
  });

  it('puts the person guard on /admin and on the owner pages at the root', () => {
    expect(admin.canActivate?.length).toBe(1);
    for (const path of ['account', 'setup', 'sign-in']) {
      expect(routes.find((route) => route.path === path)?.canActivate?.length, path).toBe(1);
    }
  });

  it('shows status at /admin', () => {
    expect(adminChild('')?.redirectTo).toBe('status');
    expect(adminChild('status')?.component).toBe(Status);
  });

  it('serves every administration page under /admin', () => {
    for (const path of ADMIN_PATHS) {
      const route = adminChild(path);
      expect(route, path).toBeDefined();
      expect(route?.component ?? route?.loadComponent, path).toBeTruthy();
    }
    expect(adminChild('config')?.component).toBe(Config);
  });

  it('no longer serves an administration page at the root', () => {
    for (const path of ADMIN_PATHS) {
      const route = routes.find((r) => r.path === path);
      expect(route?.component, path).toBeUndefined();
      expect(route?.loadComponent, path).toBeUndefined();
      expect(route?.redirectTo, path).toBe(`admin/${path}`);
    }
  });

  it('falls back to not-found last', () => {
    const last = routes[routes.length - 1];
    expect(last.path).toBe('**');
    expect(last.component).toBe(NotFound);
  });
});

describe('legacy redirects', () => {
  async function land(url: string): Promise<string> {
    TestBed.configureTestingModule({
      providers: [
        provideRouter([
          ...routes.filter((route) => route.redirectTo !== undefined),
          // The target pages are not under test: a stub in place of /admin.
          { path: 'admin', children: [{ path: '**', children: [] }] },
          { path: '**', children: [] },
        ]),
      ],
    });
    await TestBed.inject(Router).navigateByUrl(url);
    return TestBed.inject(Location).path(true);
  }

  it('sends each old path to its /admin path', async () => {
    for (const path of ADMIN_PATHS.filter((p) => !p.includes(':'))) {
      TestBed.resetTestingModule();
      expect(await land(`/${path}`), path).toBe(`/admin/${path}`);
    }
  });

  it('keeps route params', async () => {
    expect(await land('/operations/42')).toBe('/admin/operations/42');
    TestBed.resetTestingModule();
    expect(await land('/catalog/jellyfin/install')).toBe('/admin/catalog/jellyfin/install');
  });

  it('keeps the query and fragment', async () => {
    expect(await land('/catalog?x=1#manage-jellyfin')).toBe('/admin/catalog?x=1#manage-jellyfin');
  });
});
