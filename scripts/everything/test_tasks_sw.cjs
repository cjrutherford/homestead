const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(__dirname + '/tasks-sw.template.js', 'utf8')
  .replaceAll('__VERSION__', 'test')
  .replace('__ASSETS__', JSON.stringify(['./', './index.html', './assets/app-AbCd1234.js', './_homestead/puter-bridge.js']));
const handlers = {};
const opened = [];
const stored = new Map();
const added = [];
const cache = {
  match: async (key) => stored.get(key),
  put: async (key, res) => { stored.set(key, res); },
  addAll: async (requests) => { added.push(...requests.map((r) => r.url || r)); },
};
const self = {
  registration: { scope: 'https://home.example/apps/tasks/' },
  clients: { claim: async () => {} },
  addEventListener: (name, handler) => { handlers[name] = handler; },
};
const caches = {
  open: async (name) => { opened.push(name); return cache; },
  keys: async () => ['homestead-everything-tasks-old', 'tasks-old', 'other-app', 'homestead-everything-tasks-test'],
  delete: async (name) => { opened.push('delete:' + name); },
};
let online = true;
let network = async () => ({ status: 200, type: 'basic', redirected: false, body: 'network', clone() { return this; } });
class FakeResponse {
  constructor(body, init = {}) { this.body = body; this.status = init.status || 200; this.headers = init.headers || {}; }
}
class FakeRequest { constructor(url) { this.url = url; } }
const fetchStub = async (req) => { if (!online) throw new TypeError('offline'); return network(req); };
vm.runInNewContext(source, { self, caches, URL, Request: FakeRequest, Response: FakeResponse, fetch: fetchStub });
function dispatch(url, mode = 'cors', method = 'GET') {
  let promise;
  handlers.fetch({ request: { url, mode, method }, respondWith: (value) => { promise = value; } });
  return promise;
}
const base = 'https://home.example/apps/tasks/';
(async () => {
  // Install never precaches the root, index or any _homestead/ path.
  let install;
  handlers.install({ waitUntil: (p) => { install = p; } });
  await install;
  assert.deepEqual(added.map((u) => u), ['./assets/app-AbCd1234.js']);

  // Bridge paths and other non-asset requests bypass the worker entirely.
  for (const path of ['_homestead/kv?key=private', '_homestead/data', '_homestead/session', '_homestead/login',
    '_homestead/callback?code=x', '_homestead/logout', '_homestead/puter-bridge.js']) {
    assert.equal(dispatch(base + path), undefined, path);
    assert.equal(dispatch(base + path, 'navigate'), undefined, path);
  }
  assert.equal(dispatch(base + 'assets/app-AbCd1234.js?private=1'), undefined);
  assert.equal(dispatch('https://foreign.example/apps/tasks/assets/app-AbCd1234.js'), undefined);
  assert.equal(dispatch(base + 'assets/app-AbCd1234.js', 'cors', 'POST'), undefined);
  assert.equal(dispatch(base + 'assets/other.js'), undefined);

  // Online navigation goes to the network, never the cache, and is not stored.
  for (const nav of [base, base + 'index.html', base + 'some/deep/link']) {
    // Poison the cache: a stored index must never be served.
    stored.set('./', 'STALE-INDEX-WITH-TOKEN');
    stored.set('./index.html', 'STALE-INDEX-WITH-TOKEN');
    const res = await dispatch(nav, 'navigate');
    assert.equal(res.body, 'network', nav);
  }
  stored.clear();
  // Redirects (sign-in), errors and opaque responses are passed through, not stored.
  for (const odd of [{ status: 302, type: 'opaqueredirect' }, { status: 500, type: 'basic' }, { status: 200, type: 'opaque' }]) {
    network = async () => ({ ...odd, redirected: false, body: 'odd', clone() { return this; } });
    const res = await dispatch(base, 'navigate');
    assert.equal(res.status, odd.status);
  }
  assert.equal(stored.size, 0);
  network = async () => ({ status: 200, type: 'basic', redirected: false, body: 'network', clone() { return this; } });

  // Offline navigation: token-free fallback, 503, not cacheable.
  online = false;
  for (const nav of [base, base + 'index.html', base + 'x']) {
    stored.set('./', 'STALE-INDEX-WITH-TOKEN');
    const res = await dispatch(nav, 'navigate');
    assert.equal(res.status, 503);
    assert.match(res.body, /You are offline/);
    assert.doesNotMatch(res.body, /homestead-csrf|STALE|token|csrf/i);
    assert.equal(res.headers['Cache-Control'], 'no-store');
  }
  stored.clear();
  online = true;

  // Hashed static asset: network once, then cache-first; only 200 basic responses are stored.
  let calls = 0;
  network = async () => { calls++; return { status: 200, type: 'basic', redirected: false, body: 'asset', clone() { return this; } }; };
  assert.equal((await dispatch(base + 'assets/app-AbCd1234.js')).body, 'asset');
  assert.equal((await dispatch(base + 'assets/app-AbCd1234.js')).body, 'asset');
  assert.equal(calls, 1);
  assert.deepEqual([...stored.keys()], ['./assets/app-AbCd1234.js']);
  stored.clear();
  for (const odd of [{ status: 404, type: 'basic', redirected: false }, { status: 200, type: 'basic', redirected: true },
    { status: 200, type: 'opaque', redirected: false }, { status: 302, type: 'basic', redirected: false }]) {
    network = async () => ({ ...odd, body: 'odd', clone() { return this; } });
    await dispatch(base + 'assets/app-AbCd1234.js');
  }
  assert.equal(stored.size, 0);

  let activate;
  handlers.activate({ waitUntil: (promise) => { activate = promise; } });
  await activate;
  assert.ok(opened.includes('delete:homestead-everything-tasks-old'));
  assert.ok(!opened.includes('delete:other-app'));
  assert.ok(!opened.includes('delete:tasks-old'));
  console.log('sw tests ok');
})().catch((error) => { console.error(error); process.exitCode = 1; });
