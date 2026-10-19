import { describe, expect, it } from 'vitest';
import { labelForRequest, labelForType, registerPackageTitles } from './operation-labels';

describe('operation labels', () => {
  it('keeps one name through the flow', () => {
    expect(labelForRequest('POST', '/api/v1/edge/routes')).toEqual({ title: 'Reconcile routes', done: 'Routes reconciled' });
    expect(labelForRequest('POST', '/api/v1/catalog/immich/install')).toEqual({ title: 'Install Immich', done: 'Immich installed' });
  });

  it('names uninstall like the button, dialog and toast do', () => {
    expect(labelForRequest('POST', '/api/v1/catalog/nextcloud/uninstall')).toEqual({
      title: 'Uninstall Nextcloud',
      done: 'Nextcloud uninstalled',
    });
  });

  it('uses the catalog title once the catalog is known', () => {
    registerPackageTitles([{ name: 'gridcraft', title: 'GridCraft' }]);
    expect(labelForRequest('POST', '/api/v1/catalog/gridcraft/install')).toEqual({
      title: 'Install GridCraft',
      done: 'GridCraft installed',
    });
  });

  it('only labels operation-starting requests', () => {
    expect(labelForRequest('GET', '/api/v1/edge/routes')).toBeNull();
    expect(labelForRequest('POST', '/api/v1/auth/login')).toBeNull();
  });

  it('labels unknown operation types from the type', () => {
    expect(labelForType('caddy.deploy').title).toBe('Deploy Caddy');
    expect(labelForType('catalog.remove')).toEqual({ title: 'Uninstall app', done: 'App uninstalled' });
    expect(labelForType('some.new-thing').title).toBe('Some new thing');
  });
});
