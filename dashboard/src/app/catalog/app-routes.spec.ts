import { describe, expect, it } from 'vitest';
import { appRoutes, mergeRoutes, routeFromManifest } from './app-routes';

const route = (Host: string, target: string, extra: Record<string, unknown> = {}) =>
  ({ Host, Target: { Host: target, Port: 80 }, Exposure: 'local', TLS: 'internal', Health: { Path: '', IntervalSeconds: 0 }, ...extra }) as never;

const immich = { Name: 'immich', Services: [{ Name: 'server', Exposes: 'web' }, { Name: 'machine-learning', Exposes: '' }] } as never;
const gridcraft = { Name: 'gridcraft', Services: [{ Name: 'gridcraft', Exposes: 'web' }] } as never;

describe('appRoutes', () => {
  it('finds a craft app by its CraftApp mark and a package by served service and host name', () => {
    const table = [route('grid.example', 'gridcraft', { CraftApp: 'gridcraft' }), route('immich.lan', 'server'), route('authentik.lan', 'server')];
    expect(appRoutes(gridcraft, table).map((r) => r.Host)).toEqual(['grid.example']);
    expect(appRoutes(immich, table).map((r) => r.Host)).toEqual(['immich.lan']);
  });
});

describe('mergeRoutes', () => {
  it('adds a route, replaces one on the same host and never drops another', () => {
    const table = [route('a.lan', 'a'), route('b.lan', 'b')];
    expect(mergeRoutes(table, [route('c.lan', 'c')]).map((r) => r.Host)).toEqual(['a.lan', 'b.lan', 'c.lan']);
    const merged = mergeRoutes(table, [route('a.lan', 'a2')]);
    expect(merged.map((r) => r.Host)).toEqual(['b.lan', 'a.lan']);
    expect(merged[1].Target.Host).toBe('a2');
  });
});

// S326: the lightcraft regression. "Give it an address" sent a craft app's
// route on its own name; a package served by path gets its path.
describe('routeFromManifest', () => {
  const craft = (extra: Record<string, unknown> = {}) =>
    ({
      Name: 'lightcraft',
      Services: [{ Name: 'lightcraft', Exposes: 'web', Image: 'homestead/craft-lightcraft:1' }],
      Exposure: { Endpoints: [{ Endpoint: 'web', Recommended: 'local' }] },
      ...extra,
    }) as never;

  it('gives a craft app its path with the prefix stripped, never a name of its own', () => {
    const route = routeFromManifest(craft({ Serve: 'path', StripPrefix: true }), 'lightcraft.lan', 8080);
    expect(route).toMatchObject({ Host: 'lightcraft.lan', CraftApp: 'lightcraft', Path: '/apps/lightcraft/', StripPrefix: true });
  });

  it('keeps the package path and redirects', () => {
    const route = routeFromManifest(
      craft({ Name: 'nextcloud', Serve: 'path', Path: '/apps/nextcloud/', Redirects: [{ From: '/.well-known/carddav', To: '/apps/nextcloud/remote.php/dav' }] }),
      'nextcloud.lan',
      80,
    );
    expect(route?.Path).toBe('/apps/nextcloud/');
    expect(route?.Redirects).toHaveLength(1);
  });

  it('leaves a host-served package on its name', () => {
    expect(routeFromManifest(craft({ Name: 'immich', Services: [{ Name: 'server', Exposes: 'web', Image: 'immich' }] }), 'immich.lan', 2283)?.Path).toBeUndefined();
  });
});
