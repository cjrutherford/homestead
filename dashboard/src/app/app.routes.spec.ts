import { describe, expect, it } from 'vitest';
import { routes } from './app.routes';
import { Status } from './status/status';
import { Claim } from './claim/claim';
import { Login } from './login/login';
import { Config } from './config/config';
import { Firewall } from './firewall/firewall';
import { Operations } from './operations/operations';
import { OperationDetail } from './operations/operation-detail';
import { Updates } from './updates/updates';
import { Advanced } from './advanced/advanced';
import { NotFound } from './not-found/not-found';

describe('routes', () => {
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
    expect(firewall?.component).toBe(Firewall);
  });

  it('serves operations list and detail pages', () => {
    expect(routes.find((route) => route.path === 'operations')?.component).toBe(Operations);
    expect(routes.find((route) => route.path === 'operations/:id')?.component).toBe(OperationDetail);
  });

  it('serves the updates page', () => {
    expect(routes.find((route) => route.path === 'updates')?.component).toBe(Updates);
  });

  it('serves the advanced page', () => {
    expect(routes.find((route) => route.path === 'advanced')?.component).toBe(Advanced);
  });  it('falls back to not-found last', () => {
    const last = routes[routes.length - 1];
    expect(last.path).toBe('**');
    expect(last.component).toBe(NotFound);
  });
});
