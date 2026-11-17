/**
 * Drop-in replacement for upstream tests/helpers/mock-puter.js that keeps the
 * same exports but backs `puter` with the Homestead shim talking to a REAL
 * bridge process (no in-memory cloud). Upstream tests also peek into
 * kvMap/fileMap synchronously, so those are write-through mirrors: writes the
 * app makes through the shim land in the mirror, and tests' direct mirror
 * writes are forwarded to the bridge in order before the next shim call.
 *
 * Each reset() switches to a fresh bridge subject, so tests do not see each
 * other's data (this is also what per-person isolation looks like to Tasks).
 */
import { installMockPuter as installOrig } from './mock-puter-orig.js';
import '../../../../scripts/everything/puter-bridge.js';

const realFetch = globalThis.fetch;
let seq = 0;

async function login(subject) {
  const res = await realFetch(`${process.env.HS_BRIDGE_URL}/_dev/login`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ subject }),
  });
  const s = await res.json();
  return { csrf: s.csrf, headers: { Cookie: `hs_dev_session=${s.cookie}` } };
}

class Mirror extends Map {
  constructor(forward) { super(); this.forward = forward; }
  _set(k, v) { super.set(k, v); }
  _del(k) { super.delete(k); }
  set(k, v) { super.set(k, v); this.forward('put', k, v); return this; }
  delete(k) { const had = super.delete(k); this.forward('del', k); return had; }
}

export function createMockPuter({ username = 'testuser' } = {}) {
  const url = process.env.HS_BRIDGE_URL;
  const config = { endpoint: `${url}/_homestead/data`, origin: url, csrf: '', headers: {} };
  const shim = globalThis.createHomesteadPuter(config);
  let subject;
  let ready;
  let pending = Promise.resolve();
  const rotate = () => {
    subject = `${username}-${process.pid}-${++seq}`;
    ready = login(subject).then((s) => { config.csrf = s.csrf; config.headers = s.headers; });
  };
  rotate();
  const gate = async () => { await ready; await pending; };
  const queue = (op) => { pending = pending.then(op).catch(() => {}); };

  const kvMap = new Mirror((op, k, v) => queue(async () => {
    await ready;
    if (op === 'put') await shim.kv.set(k, v); else await shim.kv.del(k);
  }));
  const fileMap = new Mirror((op, k, v) => queue(async () => {
    await ready;
    if (op === 'put') await shim.fs.write(k, v); else await shim.fs.delete(k);
  }));

  const puter = {
    env: shim.env,
    kv: {
      async get(...a) { await gate(); return shim.kv.get(...a); },
      async set(key, value, opts) {
        await gate();
        const r = await shim.kv.set(key, value, opts);
        if (Array.isArray(key)) key.forEach((e) => kvMap._set(e.key, structuredClone(e.value)));
        else kvMap._set(key, structuredClone(value));
        return r;
      },
      async del(key, opts) { await gate(); const r = await shim.kv.del(key, opts); kvMap._del(key); return r; },
      async list(...a) { await gate(); return shim.kv.list(...a); },
    },
    fs: {
      async write(path, data, opts) {
        await gate();
        const r = await shim.fs.write(path, data, opts);
        fileMap._set(path, typeof data === 'string' ? data : String(data));
        return r;
      },
      async read(path) { await gate(); return shim.fs.read(path); },
      async delete(path) { await gate(); await shim.fs.delete(path); fileMap._del(path); },
      getReadURL: (...a) => shim.fs.getReadURL(...a),
    },
    auth: {
      async isSignedIn() { await gate(); return shim.auth.isSignedIn(); },
      async getUser() { await gate(); const u = await shim.auth.getUser(); return { username, uuid: u.uuid }; },
    },
  };
  return {
    puter, kvMap, fileMap, accountId: seq, shares: new Map(),
    rotate() { Map.prototype.clear.call(kvMap); Map.prototype.clear.call(fileMap); rotate(); },
  };
}

export function installMockPuter() {
  const orig = installOrig(); // browser globals + fetch mock (for share URLs)
  // Only the bridge is reachable for real; everything else stays mocked.
  const mockedFetch = globalThis.fetch;
  globalThis.fetch = (u, init) => (String(u).startsWith(process.env.HS_BRIDGE_URL)
    ? realFetch(u, init) : mockedFetch(u, init));
  const m = createMockPuter();
  globalThis.puter = m.puter;
  return {
    puter: m.puter, kvMap: m.kvMap, fileMap: m.fileMap,
    reset() { m.rotate(); orig.reset(); },
  };
}
