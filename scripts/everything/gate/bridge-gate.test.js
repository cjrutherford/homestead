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
    // Wrong CSRF token with a valid session is forbidden on the wire...
    const raw = await fetch(bridge.endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Origin: bridge.origin, 'X-Homestead-CSRF': 'wrong', Cookie: good.headers.Cookie },
      body: JSON.stringify({ operation: 'whoami' }),
    });
    expect(raw.status).toBe(403);
    // ...but the shim recovers a stale page-load token from the session
    // endpoint (no-store) and retries once, so the write lands.
    const stale = shimFor({ ...good, csrf: 'wrong' });
    expect(await stale.kv.set('k', 2)).toBe(true);
    expect(await p.kv.get('k')).toBe(2);
    const noCache = await fetch(bridge.endpoint.replace(/data$/, 'session'), { headers: { Cookie: good.headers.Cookie } });
    expect(noCache.headers.get('cache-control')).toBe('no-store');
    expect((await noCache.json()).csrf).toBe(good.csrf);
    // A session endpoint without a session reveals nothing.
    const anon = await fetch(bridge.endpoint.replace(/data$/, 'session'));
    expect(anon.status).toBe(401);
    // Foreign origin is forbidden.
    const badOrigin = shimFor({ ...good, origin: 'http://evil.example' });
    expect((await rejects(badOrigin.kv.get('k'))).code).toBe('forbidden');
    await bridge.revoke(good.cookie);
    expect(await p.auth.isSignedIn()).toBe(false);
    expect((await rejects(p.kv.get('k'))).code).toBe('auth_required');
    expect((await rejects(stale.kv.set('k', 3))).code).toBe('auth_required');

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

  // S372: Contacts reads its whole address book with kv.list('contact:*', true).
  // More than the 1000-key list limit must still come back, in pages.
  it('lists key-value pairs past the 1000-key limit, per person', async () => {
    const p = shimFor(await bridge.login('book-person'));
    const q = shimFor(await bridge.login('book-other'));
    for (let from = 0; from < 1150; from += 100) {
      const batch = [];
      for (let i = from; i < Math.min(from + 100, 1150); i++) batch.push({ key: 'contact:' + String(i).padStart(5, '0'), value: { id: i, name: 'n' + i } });
      await p.kv.set(batch);
    }
    await p.kv.set('other:1', { x: 1 });
    await q.kv.set('contact:q', { id: 'q' });
    const all = await p.kv.list('contact:*', true);
    expect(all.length).toBe(1150);
    expect(all[0]).toEqual({ key: 'contact:00000', value: { id: 0, name: 'n0' } });
    expect(all[1149].value.id).toBe(1149);
    expect((await q.kv.list('contact:*', true)).map((e) => e.key)).toEqual(['contact:q']);
    expect(await p.kv.list('nothing:*', true)).toEqual([]);
    expect((await rejects(p.kv.list('*'))).code).toBeTruthy(); // key-only list keeps its 1000 limit
  });

  // S369: the folder operations Projects needs, through the shim to the real
  // bridge. Folders are implied by file paths; everything is per person.
  it('stat, readdir and recursive delete work on folders, per person', async () => {
    const p = shimFor(await bridge.login('fs-person'));
    const q = shimFor(await bridge.login('fs-other'));
    const w1 = await p.fs.write('pd/snapshots/p1/2026-01-01.json', '{"a":1}', { createMissingParents: true });
    expect(w1.size).toBe(7);
    expect(w1.modified).toBeGreaterThan(0);
    await p.fs.write('pd/snapshots/p1/2026-01-02.json', new Blob(['bb']));
    await p.fs.write('pd/snapshots/p10/keep.json', 'k');
    await q.fs.write('pd/snapshots/p1/theirs.json', 't');
    const names = (await p.fs.readdir('pd/snapshots/p1')).map((e) => e.name);
    expect(names).toEqual(['2026-01-01.json', '2026-01-02.json']);
    const top = await p.fs.readdir('pd/snapshots');
    expect(top.map((e) => [e.name, e.is_dir])).toEqual([['p1', true], ['p10', true]]);
    const st = await p.fs.stat('pd/snapshots/p1/2026-01-01.json');
    expect(st.size).toBe(7);
    // A rewrite changes the stamp even with identical bytes (conflict check).
    const w2 = await p.fs.write('pd/snapshots/p1/2026-01-01.json', '{"a":1}');
    expect(w2.modified).toBeGreaterThan(st.modified);
    expect((await p.fs.stat('pd/snapshots/p1/2026-01-01.json')).modified).toBe(w2.modified);
    expect((await p.fs.stat('pd/snapshots')).is_dir).toBe(true);
    expect((await rejects(p.fs.stat('pd/nothing'))).code).toBe('subject_does_not_exist');
    expect((await rejects(p.fs.readdir('pd/nothing'))).code).toBe('subject_does_not_exist');
    expect((await rejects(p.fs.read('pd/nothing'))).code).toBe('subject_does_not_exist');
    expect((await rejects(p.fs.stat('../escape'))).code).toBe('bridge_rejected');
    expect((await rejects(q.fs.stat('pd/snapshots/p1/2026-01-01.json'))).code).toBe('subject_does_not_exist');
    await p.fs.delete('pd/snapshots/p1', { recursive: true });
    expect((await rejects(p.fs.readdir('pd/snapshots/p1'))).code).toBe('subject_does_not_exist');
    expect((await p.fs.readdir('pd/snapshots/p10')).length).toBe(1);
    expect((await q.fs.readdir('pd/snapshots/p1')).map((e) => e.name)).toEqual(['theirs.json']);
    await p.fs.delete('pd/snapshots/p1', { recursive: true }); // already gone: no error
  });
});
