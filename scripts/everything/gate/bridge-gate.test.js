// Homestead-specific S367 gate: the real Tasks Store talking through the real
// shim to a real bridge process (development identity, temp SQLite).
import { describe, it, expect, beforeAll, afterAll } from 'vitest';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { startBridge } from './bridge-harness.mjs';
import { installMockPuter } from '../tests/helpers/mock-puter-orig.js';
import '../../../scripts/everything/puter-bridge.js';

const BIN = process.env.HS_BRIDGE_BIN;
const realFetch = globalThis.fetch; // before the upstream helper mocks it
let dir, db, bridge, Store;

const shimFor = (cfg) => globalThis.createHomesteadPuter(cfg);
const rejects = async (p) => { try { await p; } catch (e) { return e; } throw new Error('expected rejection'); };
const newStore = (puter, id) => { const s = new Store(); s.cloud = puter; s.deviceId = id; return s; };

beforeAll(async () => {
  installMockPuter(); // browser globals only
  globalThis.fetch = realFetch;
  ({ Store } = await import('../src/lib/store.js'));
  dir = mkdtempSync(path.join(tmpdir(), 's367-bridge-'));
  db = path.join(dir, 'bridge.db');
  bridge = await startBridge({ bin: BIN, db });
});
afterAll(async () => { await bridge?.stop(); rmSync(dir, { recursive: true, force: true }); });

describe('real Tasks store through the real bridge', () => {
  it('persists across a bridge restart', async () => {
    const cfg = await bridge.login('persist-person');
    const a = newStore(shimFor(cfg), 'dev-A');
    await a.init();
    const list = await a.addList({ name: 'Survives restart' });
    await a.addTask({ listId: list.id, title: 'still here' });
    await a.flushPending();
    await bridge.stop();
    bridge = await startBridge({ bin: BIN, db });
    const cfg2 = await bridge.login('persist-person');
    const b = newStore(shimFor(cfg2), 'dev-B');
    await b.init();
    expect(b.lists.map((l) => l.name)).toContain('Survives restart');
    expect(b.tasks.map((t) => t.title)).toContain('still here');
  });

  it('two concurrent clients of one person converge without losing writes', async () => {
    const cfgA = await bridge.login('concurrent-person');
    const cfgB = await bridge.login('concurrent-person');
    const a = newStore(shimFor(cfgA), 'dev-A');
    const b = newStore(shimFor(cfgB), 'dev-B');
    // The first device seeds the empty account; the second joins it. Booting
    // both at once on an empty account makes each mint its own dataset epoch
    // and the later mint replaces the other wholesale (see the next test).
    await a.init();
    const la = await a.addList({ name: 'Shared by devices' });
    await a.flushPending();
    await b.init();
    await b.refreshFromCloud({ force: true });
    await Promise.all([
      a.addTask({ listId: la.id, title: 'from A' }),
      b.addTask({ listId: la.id, title: 'from B' }),
    ]);
    await Promise.all([a.flushPending(), b.flushPending()]);
    // Like real Puter KV the bridge is last-write-wins per key; Tasks'
    // record-level merge converges after each side re-reads and pushes back.
    for (let round = 0; round < 3; round++) {
      for (const s of [a, b]) { await s.refreshFromCloud({ force: true }); await s.flushPending(); }
    }
    for (const s of [a, b]) {
      expect(s.tasks.map((t) => t.title).sort()).toEqual(['from A', 'from B']);
    }
  });

  it('first boot of two devices on an empty account: later-minted epoch replaces the other (upstream)', async () => {
    const a = newStore(shimFor(await bridge.login('race-person')), 'dev-A');
    const b = newStore(shimFor(await bridge.login('race-person')), 'dev-B');
    await Promise.all([a.init(), b.init()]);
    await a.addTask({ listId: a.lists[0].id, title: 'from A' });
    await a.flushPending();
    for (let round = 0; round < 3; round++) {
      for (const s of [a, b]) { await s.refreshFromCloud({ force: true }); await s.flushPending(); }
    }
    // Both devices converge on one dataset (no divergence), though one
    // device's pre-sync edits belong to the discarded epoch.
    expect(a.epoch).toBe(b.epoch);
    expect(a.tasks.map((t) => t.title)).toEqual(b.tasks.map((t) => t.title));
  });

  it('keeps one person private from another (kv and files)', async () => {
    const alice = shimFor(await bridge.login('iso-alice'));
    const bob = shimFor(await bridge.login('iso-bob'));
    await alice.kv.set('tasks:secret', { v: 'alice-only' });
    await alice.fs.write('tasks-data/secret.json', '"alice-file"');
    expect(await bob.kv.get('tasks:secret')).toBeNull();
    expect(await bob.kv.list('*')).toEqual([]);
    expect((await rejects(bob.fs.read('tasks-data/secret.json'))).code).toBe('subject_does_not_exist');
    expect(await alice.kv.get('tasks:secret')).toEqual({ v: 'alice-only' });
    // The subject is the verified one; a client-supplied one is not accepted.
    const res = await fetch(bridge.endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Origin: bridge.origin, 'X-Homestead-CSRF': (await bridge.login('x')).csrf },
      body: JSON.stringify({ operation: 'get', kind: 'kv', key: 'tasks:secret', subject: 'iso-alice' }),
    });
    expect(res.status).toBe(401);
  });

  it('refuses sharing, collaboration and signed URLs instead of succeeding', async () => {
    const p = shimFor(await bridge.login('share-person'));
    expect(p.kv.share).toBeUndefined();
    expect(p.perms).toBeUndefined();
    expect(p.events).toBeUndefined();
    expect((await rejects(p.fs.getReadURL('tasks-data/x', 60))).code).toBe('bridge_unsupported');
    expect((await rejects(p.kv.get('meta', { handle: 'kvh-1' }))).code).toBe('bridge_unsupported');
    const s = newStore(p, 'dev-S');
    await s.init();
    const list = await s.addList({ name: 'Try to share' });
    expect(s.collabMode()).toBe('link');
    const err = await rejects(s.enableShare(list.id));
    expect(err.code).toBe('bridge_unsupported');
    expect(await shimFor(await bridge.login('share-other')).kv.list('*')).toEqual([]);
  });

  it('refuses missing, forged, revoked and expired sessions', async () => {
    const none = shimFor({ endpoint: bridge.endpoint, origin: bridge.origin, csrf: 'x' });
    expect(await none.auth.isSignedIn()).toBe(false);
    expect((await rejects(none.kv.get('k'))).code).toBe('auth_required');
    const forged = shimFor({ endpoint: bridge.endpoint, origin: bridge.origin, csrf: 'x', headers: { Cookie: 'hs_dev_session=forged' } });
    expect(await forged.auth.isSignedIn()).toBe(false);

    const good = await bridge.login('revoked-person');
    const p = shimFor(good);
    await p.kv.set('k', 1);
    expect(await p.auth.isSignedIn()).toBe(true);
    // Wrong CSRF token with a valid session is forbidden.
    const badCsrf = shimFor({ ...good, csrf: 'wrong' });
    expect((await rejects(badCsrf.kv.set('k', 2))).code).toBe('forbidden');
    // Foreign origin is forbidden.
    const badOrigin = shimFor({ ...good, origin: 'http://evil.example' });
    expect((await rejects(badOrigin.kv.get('k'))).code).toBe('forbidden');
    await bridge.revoke(good.cookie);
    expect(await p.auth.isSignedIn()).toBe(false);
    expect((await rejects(p.kv.get('k'))).code).toBe('auth_required');

    const short = await bridge.login('expiring-person', 150);
    const q = shimFor(short);
    expect(await q.auth.isSignedIn()).toBe(true);
    await new Promise((r) => setTimeout(r, 400));
    expect(await q.auth.isSignedIn()).toBe(false);
  });

  it('bounds values and keeps batches atomic', async () => {
    const p = shimFor(await bridge.login('bounds-person'));
    expect((await rejects(p.kv.set('big', 'x'.repeat(9 * 1024 * 1024)))).code).toBe('storage_limit');
    const dup = await rejects(p.kv.set([{ key: 'a', value: 1 }, { key: 'a', value: 2 }]));
    expect(dup.code).toBe('bridge_rejected');
    expect(await p.kv.get('a')).toBeNull();
    await p.kv.set([{ key: 'a', value: 1 }, { key: 'b', value: 2 }]);
    expect(await p.kv.list('*')).toEqual(['a', 'b']);
  });
});
