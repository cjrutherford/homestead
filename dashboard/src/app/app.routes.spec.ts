import { describe, expect, it } from 'vitest';
import { routes } from './app.routes';
import { Status } from './status/status';
import { Claim } from './claim/claim';
import { Login } from './login/login';
import { Config } from './config/config';
import { Account } from './account/account';
import { NotFound } from './not-found/not-found';

describe('routes', () => {
  it('loads the install wizard on demand under the catalog', () => {
    const wizard = routes.find((route) => route.path === 'catalog/:name/install');
    expect(wizard?.loadComponent).toBeTypeOf('function');
  });

  it('loads the sign-in settings page on demand', () => {
    const page = routes.find((route) => route.path === 'sign-in');
    expect(page?.loadComponent).toBeTypeOf('function');
  });

  it('loads the setup walkthrough on demand (S287)', () => {
    const page = routes.find((route) => route.path === 'setup');
    expect(page?.loadComponent).toBeTypeOf('function');
  });

  it('redirects the root to status', () => {
    const root = routes.find((route) => route.path === '');
    expect(root?.redirectTo).toBe('status');
  });

  it('serves the status page', () => {
    const status = routes.find((route) => route.path === 'status');
    expect(status?.component).toBe(Status);
  });

  it('serves the login page', () => {
    const login = routes.find((route) => route.path === 'login');
    expect(login?.component).toBe(Login);
  });

  it('serves the claim page', () => {
    const claim = routes.find((route) => route.path === 'claim');
    expect(claim?.component).toBe(Claim);
  });

  it('serves the configuration page', () => {
    const config = routes.find((route) => route.path === 'config');
    expect(config?.component).toBe(Config);
  });

  it('serves the firewall page', () => {
    const firewall = routes.find((route) => route.path === 'firewall');
    expect(firewall?.loadComponent).toBeTypeOf('function');
  });

  it('serves operations list and detail pages', () => {
    expect(routes.find((route) => route.path === 'operations')?.loadComponent).toBeTypeOf('function');
    expect(routes.find((route) => route.path === 'operations/:id')?.loadComponent).toBeTypeOf('function');
  });

  it('serves the updates page', () => {
    expect(routes.find((route) => route.path === 'updates')?.loadComponent).toBeTypeOf('function');
  });

  it('serves the advanced page', () => {
    expect(routes.find((route) => route.path === 'advanced')?.loadComponent).toBeTypeOf('function');
  });

  it('serves the drift page', () => {
    expect(routes.find((route) => route.path === 'drift')?.loadComponent).toBeTypeOf('function');
  });

  it('serves the validation board', () => {
    expect(routes.find((route) => route.path === 'validation-board')?.loadComponent).toBeTypeOf('function');
  });

  it('serves the audit log', () => {
    expect(routes.find((route) => route.path === 'audit')?.loadComponent).toBeTypeOf('function');
  });

  it('falls back to not-found last', () => {
    const last = routes[routes.length - 1];
    expect(last.path).toBe('**');
    expect(last.component).toBe(NotFound);
  });

  it('serves the edge page', () => {
    const edge = routes.find((route) => route.path === 'edge');
    expect(edge?.loadComponent).toBeTypeOf('function');
  });

  it('serves the certificates page', () => {
    expect(routes.find((route) => route.path === 'tls')?.loadComponent).toBeTypeOf('function');
  });

  it('serves the account page', () => {
    expect(routes.find((route) => route.path === 'account')?.component).toBe(Account);
  });

  it('serves the craft files page', () => {
    const page = routes.find((route) => route.path === 'craft-files');
    expect(page?.loadComponent).toBeTypeOf('function');
  });

  it('serves the craft apps page', () => {
    const page = routes.find((route) => route.path === 'craft-apps');
    expect(page?.loadComponent).toBeTypeOf('function');
  });
});
