// S468: every Everything service worker serves its offline page when a page
// navigation gets a 502/503/504 from the edge (container stopped), and leaves
// other responses alone. Run: node scripts/everything/test_edge_down_sw.cjs
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const dir = __dirname;
const workers = fs.readdirSync(dir).filter((f) => f.endsWith('-sw.template.js')).map((f) => dir + '/' + f)
  .concat([dir + '/files/sudoku-sw.js']);
assert.ok(workers.length >= 8, 'expected the eight workers');
class FakeResponse {
  constructor(body, init = {}) { this.body = body; this.status = init.status || 200; }
}
(async () => {
  for (const file of workers) {
    const source = fs.readFileSync(file, 'utf8').replaceAll('__VERSION__', 'test')
      .replace('__ASSETS__', JSON.stringify(['./', './index.html']));
    const handlers = {};
    let status = 200;
    const self = {
      registration: { scope: 'https://h.example/apps/x/' },
      clients: { claim: async () => {} },
      addEventListener: (n, h) => { handlers[n] = h; },
    };
    const caches = { open: async () => ({ match: async () => undefined, put: async () => {}, addAll: async () => {} }), keys: async () => [], delete: async () => {} };
    const fetchStub = async () => new FakeResponse('upstream', { status });
    vm.runInNewContext(source, { self, caches, URL, Response: FakeResponse, fetch: fetchStub });
    const nav = async () => {
      let p;
      handlers.fetch({ request: { url: 'https://h.example/apps/x/', mode: 'navigate', method: 'GET' }, respondWith: (v) => { p = v; } });
      return p;
    };
    for (const s of [502, 503, 504]) {
      status = s;
      const res = await nav();
      assert.equal(res.status, 503, file + ' ' + s);
      assert.match(res.body, /You are offline/, file);
    }
    status = 200;
    assert.equal((await nav()).body, 'upstream', file);
    status = 404;
    assert.equal((await nav()).status, 404, file);
    status = 500;
    assert.equal((await nav()).status, 500, file);
  }
  console.log('edge-down sw tests ok:', workers.length);
})().catch((e) => { console.error(e); process.exit(1); });
