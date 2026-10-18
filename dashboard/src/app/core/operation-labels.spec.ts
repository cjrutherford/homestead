import { describe, expect, it } from 'vitest';
import { labelForRequest, labelForType } from './operation-labels';

describe('operation labels', () => {
  it('keeps one name through the flow', () => {
    expect(labelForRequest('POST', '/api/v1/edge/routes')).toEqual({ title: 'Reconcile routes', done: 'Routes reconciled' });
    expect(labelForRequest('POST', '/api/v1/catalog/immich/install')).toEqual({ title: 'Install Immich', done: 'Immich installed' });
  });

  it('only labels operation-starting requests', () => {
    expect(labelForRequest('GET', '/api/v1/edge/routes')).toBeNull();
    expect(labelForRequest('POST', '/api/v1/auth/login')).toBeNull();
  });

  it('labels unknown operation types from the type', () => {
    expect(labelForType('caddy.deploy').title).toBe('Deploy Caddy');
    expect(labelForType('catalog.remove')).toEqual({ title: 'Remove app', done: 'App removed' });
    expect(labelForType('some.new-thing').title).toBe('Some new thing');
  });
});
