import { describe, expect, it } from 'vitest';
import { appRoutes, mergeRoutes } from './app-routes';

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
