const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(__dirname + '/tasks-sw.template.js', 'utf8')
  .replaceAll('__VERSION__', 'test')
  .replace('__ASSETS__', JSON.stringify(['./', './assets/app.js', './_homestead/puter-bridge.js']));
const handlers = {};
const opened = [];
const cache = { match: async () => 'static-cache', addAll: async () => {} };
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
vm.runInNewContext(source, { self, caches, URL, Request, fetch: async () => 'network' });
function dispatch(url, mode = 'cors', method = 'GET') {
  let promise;
  handlers.fetch({ request: { url, mode, method }, respondWith: (value) => { promise = value; } });
  return promise;
}
(async () => {
  assert.equal(dispatch('https://home.example/apps/tasks/_homestead/kv?key=private'), undefined);
  assert.equal(dispatch('https://home.example/apps/tasks/_homestead/kv'), undefined);
  assert.equal(dispatch('https://home.example/apps/tasks/assets/app.js?private=1'), undefined);
  assert.equal(dispatch('https://foreign.example/apps/tasks/assets/app.js'), undefined);
  assert.equal(dispatch('https://home.example/apps/tasks/assets/app.js', 'cors', 'POST'), undefined);
  assert.equal(await dispatch('https://home.example/apps/tasks/', 'navigate'), 'static-cache');
  assert.equal(await dispatch('https://home.example/apps/tasks/assets/app.js'), 'static-cache');
  let activate;
  handlers.activate({ waitUntil: (promise) => { activate = promise; } });
  await activate;
  assert.ok(opened.includes('delete:homestead-everything-tasks-old'));
  assert.ok(!opened.includes('delete:other-app'));
  assert.ok(!opened.includes('delete:tasks-old'));
})().catch((error) => { console.error(error); process.exitCode = 1; });
