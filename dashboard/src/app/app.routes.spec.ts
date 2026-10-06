import { describe, expect, it } from 'vitest';
import { routes } from './app.routes';
import { Status } from './status/status';
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

  it('falls back to not-found last', () => {
    const last = routes[routes.length - 1];
    expect(last.path).toBe('**');
    expect(last.component).toBe(NotFound);
  });
});
