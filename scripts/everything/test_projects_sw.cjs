// Simulates projects-sw.template.js (S369): navigations are network-only and
// never stored, bridge paths bypass the worker, only same-origin in-scope
// content-hashed assets are cached, and old caches are dropped on activate.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(__dirname + '/projects-sw.template.js', 'utf8').replaceAll('__VERSION__', 'abc123def456');
const handlers = {};
const stored = new Map();
const deleted = [];
const cache = { match: async (k) => stored.get(k), put: async (k, r) => { stored.set(k, r); } };
const self = {
  registration: { scope: 'https://home.example/apps/projects/' },
  clients: { claim: async () => {} },
  skipWaiting: () => {},
  addEventListener: (name, handler) => { handlers[name] = handler; },
};
const caches = {
  open: async () => cache,
  keys: async () => ['homestead-everything-projects-old', 'homestead-everything-projects-abc123def456', 'other-app'],
  delete: async (n) => { deleted.push(n); },
};
let online = true;
let network = async () => ({ status: 200, type: 'basic', redirected: false, body: 'network', clone() { return this; } });
class FakeResponse { constructor(body, init = {}) { this.body = body; this.status = init.status || 200; } }
const fetchStub = async (req) => { if (!online) throw new TypeError('offline'); return network(req); };
vm.runInNewContext(source, { self, caches, URL, Response: FakeResponse, fetch: fetchStub });
const dispatch = (url, mode = 'cors', method = 'GET') => {
  let p;
  handlers.fetch({ request: { url, mode, method }, respondWith: (v) => { p = v; } });
  return p;
};
const base = 'https://home.example/apps/projects/';
(async () => {
  let act;
  handlers.activate({ waitUntil: (p) => { act = p; } });
  await act;
  assert.deepEqual(deleted, ['homestead-everything-projects-old']);
  for (const path of ['_homestead/data', '_homestead/session', '_homestead/login', '_homestead/callback?code=x',
    '_homestead/logout', '_homestead/puter-bridge.js']) {
    assert.equal(dispatch(base + path), undefined, path);
    assert.equal(dispatch(base + path, 'navigate'), undefined, path);
  }
  assert.equal(dispatch(base + 'assets/index-AbCd1234.js?x=1'), undefined);
  assert.equal(dispatch('https://foreign.example/apps/projects/assets/index-AbCd1234.js'), undefined);
  assert.equal(dispatch('https://home.example/apps/tasks/assets/index-AbCd1234.js'), undefined);
  assert.equal(dispatch(base + 'assets/index-AbCd1234.js', 'cors', 'POST'), undefined);
  for (const other of ['assets/plain.js', 'manifest.webmanifest', 'icon-192.png', 'sw.js', 'favicon.ico']) {
    assert.equal(dispatch(base + other), undefined, other);
  }
  for (const nav of [base, base + 'index.html', base + 'deep/link']) {
    stored.set('./', 'STALE-INDEX-WITH-TOKEN');
    stored.set('./index.html', 'STALE-INDEX-WITH-TOKEN');
    assert.equal((await dispatch(nav, 'navigate')).body, 'network', nav);
  }
  stored.clear();
  for (const odd of [{ status: 302, type: 'opaqueredirect' }, { status: 500, type: 'basic' }, { status: 200, type: 'opaque' }]) {
    network = async () => ({ ...odd, redirected: false, body: 'odd', clone() { return this; } });
    assert.equal((await dispatch(base, 'navigate')).status, odd.status);
    assert.equal((await dispatch(base + 'assets/index-AbCd1234.js')).status, odd.status);
  }
  assert.equal(stored.size, 0);
  network = async () => ({ status: 200, type: 'basic', redirected: false, body: 'asset', clone() { return this; } });
  assert.equal((await dispatch(base + 'assets/index-AbCd1234.js')).body, 'asset');
  assert.ok(stored.has('./assets/index-AbCd1234.js'));
  online = false;
  assert.equal((await dispatch(base + 'assets/index-AbCd1234.js')).body, 'asset'); // cached hashed asset
  for (const nav of [base, base + 'index.html', base + 'x']) {
    stored.set('./', 'STALE-INDEX-WITH-TOKEN');
    const res = await dispatch(nav, 'navigate');
    assert.equal(res.status, 503);
    assert.ok(!String(res.body).includes('STALE') && !/csrf/i.test(String(res.body)));
  }
  console.log('projects service worker simulation passed');
})().catch((e) => { console.error(e); process.exit(1); });
