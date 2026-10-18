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
import { Drift } from './drift/drift';
import { ValidationBoard } from './validation/validation-board';
import { Account } from './account/account';
import { Edge } from './edge/edge';
import { Tls } from './tls/tls';
import { AuditLog } from './audit/audit';
import { CraftApps } from './craftapps/craft-apps';
import { CraftFiles } from './craftfiles/craft-files';
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
  });

  it('serves the drift page', () => {
    expect(routes.find((route) => route.path === 'drift')?.component).toBe(Drift);
  });

  it('serves the validation board', () => {
    expect(routes.find((route) => route.path === 'validation-board')?.component).toBe(ValidationBoard);
  });

  it('serves the audit log', () => {
    expect(routes.find((route) => route.path === 'audit')?.component).toBe(AuditLog);
  });

  it('falls back to not-found last', () => {
    const last = routes[routes.length - 1];
    expect(last.path).toBe('**');
    expect(last.component).toBe(NotFound);
  });

  it('serves the edge page', () => {
    const edge = routes.find((route) => route.path === 'edge');
    expect(edge?.component).toBe(Edge);
  });

  it('serves the certificates page', () => {
    expect(routes.find((route) => route.path === 'tls')?.component).toBe(Tls);
  });

  it('serves the account page', () => {
    expect(routes.find((route) => route.path === 'account')?.component).toBe(Account);
  });

  it('serves the craft files page', () => {
    const page = routes.find((route) => route.path === 'craft-files');
    expect(page?.component).toBe(CraftFiles);
  });

  it('serves the craft apps page', () => {
    const page = routes.find((route) => route.path === 'craft-apps');
    expect(page?.component).toBe(CraftApps);
  });
});
