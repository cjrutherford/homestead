// S437 real-browser check of Tasks sharing against the dev bridge started with
// sharing on (ADR-058). Three people, each in their own browser context:
//   alice shares a list with bob; bob sees it and, live, her new task (events);
//   a read-only link opens in the bridge viewer in a context with no session;
//   carol (signed in, no grant) and a never-signed-in name are denied;
//   alice revokes; bob loses the shared list.
// TEST ONLY (dev identity is not authentication). Zero dependencies: drives
// headless Chrome/Chromium over the DevTools protocol with Node's WebSocket.
// usage: node browser-check-tasks-sharing.mjs <bridge-bin> <dist-dir> <tmp-dir>
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import assert from 'node:assert/strict';

const [bin, dist, tmp] = process.argv.slice(2);
const BASE = '/apps/tasks';
let chrome = null;
for (const c of [process.env.CHROME, 'google-chrome', 'chromium', 'chromium-browser'].filter(Boolean)) {
  try { chrome = execFileSync('which', [c]).toString().trim(); break; } catch {}
}
if (!chrome) { console.log('SKIP: no Chrome/Chromium on PATH (set CHROME to run the real-browser check)'); process.exit(0); }

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const freePort = () => new Promise((res, rej) => {
  const s = net.createServer(); s.once('error', rej);
  s.listen(0, '127.0.0.1', () => { const { port } = s.address(); s.close(() => res(port)); });
});
const port = await freePort();
const origin = `http://127.0.0.1:${port}`;
let bridge = null;
const profile = path.join(tmp, 'profile');
fs.mkdirSync(profile, { recursive: true });
let browser = null;
const cleanup = async () => { browser?.kill('SIGKILL'); if (bridge && bridge.exitCode === null) bridge.kill('SIGTERM'); };
const hard = setTimeout(async () => { console.error('FAIL: timeout'); await cleanup(); process.exit(1); }, 240000);

async function wsUrl() {
  const f = path.join(profile, 'DevToolsActivePort');
  for (let i = 0; i < 100; i++) {
    if (fs.existsSync(f)) {
      const [p] = fs.readFileSync(f, 'utf8').split('\n');
      return (await (await fetch(`http://127.0.0.1:${p}/json/version`)).json()).webSocketDebuggerUrl;
    }
    await sleep(100);
  }
  throw new Error('browser did not start');
}

try {
  bridge = spawn(bin, ['-dev-identity', '-sharing', '-db', path.join(tmp, 'bridge.db'), '-app', 'tasks', '-listen', `127.0.0.1:${port}`,
    '-origin', origin, '-base-path', BASE, '-static-dir', dist],
  { env: { ...process.env, HOMESTEAD_EVERYTHING_INSECURE_DEV: 'i-understand-this-is-not-authentication' }, stdio: ['ignore', 'ignore', 'pipe'] });
  let log = '';
  await new Promise((resolve, reject) => {
    bridge.stderr.on('data', (d) => { log += d; if (log.includes('listening on')) resolve(); });
    bridge.once('exit', (c) => reject(new Error('bridge exited ' + c + log)));
    setTimeout(() => reject(new Error('bridge start timeout ' + log)), 10000);
  });
  bridge.stderr.resume();

  browser = spawn(chrome, ['--headless=new', '--no-sandbox', '--disable-gpu', '--remote-debugging-port=0',
    `--user-data-dir=${profile}`, '--no-first-run', '--no-default-browser-check', 'about:blank'], { stdio: 'ignore' });
  const ws = new WebSocket(await wsUrl());
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let id = 0; const pending = new Map(); const waiters = []; const exceptions = [];
  ws.onmessage = (m) => {
    const msg = JSON.parse(m.data);
    if (msg.id && pending.has(msg.id)) { const { res, rej } = pending.get(msg.id); pending.delete(msg.id); msg.error ? rej(new Error(JSON.stringify(msg.error))) : res(msg.result); return; }
    if (msg.method === 'Runtime.exceptionThrown') exceptions.push(`${msg.sessionId?.slice(0, 4)} ${JSON.stringify(msg.params.exceptionDetails).slice(0, 300)}`);
    for (const w of waiters.slice()) if (w.method === msg.method && w.sessionId === msg.sessionId) { waiters.splice(waiters.indexOf(w), 1); w.res(msg.params); }
  };
  const send = (method, params = {}, sessionId) => new Promise((res, rej) => {
    const i = ++id; pending.set(i, { res, rej }); ws.send(JSON.stringify({ id: i, method, params, sessionId }));
  });

  // One page per person, each in its own browser context (own cookies and storage).
  async function person(name) {
    const { browserContextId } = await send('Target.createBrowserContext');
    const { targetId } = await send('Target.createTarget', { url: 'about:blank', browserContextId });
    const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
    const cmd = (m, p) => send(m, p, sessionId);
    await cmd('Page.enable'); await cmd('Runtime.enable');
    await cmd('Page.addScriptToEvaluateOnNewDocument', { source: `window.__csp=[];document.addEventListener('securitypolicyviolation',e=>window.__csp.push(e.violatedDirective+' '+e.blockedURI));` });
    // Count event streams and the `msg` frames they deliver (proof that updates arrive by events).
    await cmd('Page.addScriptToEvaluateOnNewDocument', { source: `window.__es={urls:[],msgs:0};(function(){var E=window.EventSource;if(!E)return;window.EventSource=function(u,o){var s=new E(u,o);window.__es.urls.push(String(u));s.addEventListener('msg',function(){window.__es.msgs++});return s};window.EventSource.prototype=E.prototype;window.EventSource.CONNECTING=0;window.EventSource.OPEN=1;window.EventSource.CLOSED=2})();` });
    const p = { name };
    p.js = async (expression) => {
      const r = await cmd('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
      if (r.exceptionDetails) throw new Error(`${name} eval: ` + JSON.stringify(r.exceptionDetails).slice(0, 400));
      return r.result.value;
    };
    p.go = async (url) => {
      const loaded = new Promise((res) => waiters.push({ method: 'Page.loadEventFired', sessionId, res }));
      await cmd('Page.navigate', { url }); await Promise.race([loaded, sleep(15000)]); await sleep(300);
    };
    p.until = async (expr, what, tries = 150) => {
      for (let i = 0; i < tries; i++) { if (await p.js(expr)) return; await sleep(100); }
      throw new Error(`${name}: timeout waiting for ${what}`);
    };
    p.click = (sel, text) => p.js(`(()=>{const b=[...document.querySelectorAll(${JSON.stringify(sel)})].find(x=>x.textContent.includes(${JSON.stringify(text)}));if(!b)return false;b.click();return true})()`);
    p.clickSel = (sel) => p.js(`(()=>{const b=document.querySelector(${JSON.stringify(sel)});if(!b)return false;b.click();return true})()`);
    p.type = (sel, v) => p.js(`(()=>{const i=document.querySelector(${JSON.stringify(sel)});i.value=${JSON.stringify(v)};i.dispatchEvent(new Event('input',{bubbles:true}));return true})()`);
    p.text = () => p.js(`document.body.textContent`);
    p.csp = () => p.js(`window.__csp||[]`);
    p.boot = async () => {
      await p.go(`${origin}${BASE}/_homestead/login?subject=${name}`);
      await p.until(`!!document.querySelector('.view, .view-head, .modal, [role=dialog]')`, 'the app to boot');
      await p.js(`(()=>{const b=[...document.querySelectorAll('button')].find(x=>x.textContent.trim()==='Skip for now');if(b)b.click();return true})()`);
      await sleep(200);
    };
    p.addTask = async (title) => {
      await p.until(`!!document.querySelector('input[aria-label="Add a task"]')`, 'the quick-add field');
      await p.type('input[aria-label="Add a task"]', title);
      await p.js(`document.querySelector('input[aria-label="Add a task"]').dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true,cancelable:true}))`);
      await p.until(`document.body.textContent.includes(${JSON.stringify(title)})`, 'the task ' + title);
    };
    return p;
  }
  const alice = await person('alice');
  const bob = await person('bob');
  const carol = await person('carol');
  for (const p of [alice, bob, carol]) await p.boot();
  // The shim learns the app (appID) when the app asks who is signed in.
  assert.equal(await alice.js(`puter.appID`), 'app-tasks', 'appID is known after sign-in');
  assert.equal(await alice.js(`puter.auth.getUser().then(u=>u.username)`), 'alice');

  // 1. Alice makes a list with a task through the UI.
  await alice.go(`${origin}${BASE}/#/lists`);
  await alice.until(`!!document.querySelector('.view-head')`, 'lists view');
  assert.ok(await alice.click('button', 'New list'), 'New list button');
  await alice.until(`!!document.querySelector('input[aria-label="List name"]')`, 'the list form');
  await alice.type('input[aria-label="List name"]', 'Groceries');
  assert.ok(await alice.click('button', 'Create list'));
  await alice.until(`location.hash.startsWith('#/list/')`, 'the new list');
  await alice.addTask('Buy milk');

  // 2. The sharing UI is present and works: collaborate, invite bob.
  assert.ok(await alice.clickSel('button[aria-label="Share list"]'), 'Share button is shown');
  await alice.until(`document.body.textContent.includes('Invite people to edit')`, 'the share sheet');
  assert.ok(await alice.click('button', 'Invite people to edit'));
  await alice.until(`!!document.querySelector('#invite-name')`, 'the invite field (consent granted, KV mode)');
  assert.ok(!(await alice.text()).includes('Puter'), 'no Puter wording in the share sheet');
  // A name nobody has (never signed in) is refused with the plain message; so is yourself.
  await alice.type('#invite-name', 'dave');
  await alice.js(`document.querySelector('#invite-name').form.requestSubmit()`);
  await alice.until(`document.body.textContent.includes('No one with access to Tasks is named')`, 'refusal for an unknown name');
  // Invite bob.
  await alice.type('#invite-name', 'bob');
  await alice.js(`document.querySelector('#invite-name').form.requestSubmit()`);
  await alice.until(`document.body.textContent.includes('bob can now edit')`, 'bob invited');
  const mine = await alice.js(`puter.kv.listSharedByMe({limit:50}).then(r=>r.items)`);
  assert.equal(mine.length, 1);
  assert.equal(mine[0].holder, 'bob');
  assert.equal(mine[0].access, 'write');
  assert.equal(mine[0].appUid, 'app-tasks');
  const handle = mine[0].handle;

  // 3. Bob sees the list (share notice -> discovery) and its task, without a reload.
  await bob.go(`${origin}${BASE}/#/lists`);
  await bob.until(`document.body.textContent.includes('Groceries')`, 'the shared list on bob\'s lists', 200);
  const bobShares = await bob.js(`puter.kv.listShared({limit:50}).then(r=>r.items.map(i=>({h:i.handle,o:i.owner,a:i.access})))`);
  assert.deepEqual(bobShares, [{ h: handle, o: 'alice', a: 'write' }]);
  await bob.clickSel('button[aria-label="Open list: Groceries"]');
  await bob.until(`document.body.textContent.includes('Buy milk')`, 'alice\'s task on bob', 200);

  // 4. Live update through events: alice adds, bob sees it with no reload and no navigation.
  assert.ok(await bob.js(`window.__es.urls.length>=1 && window.__es.urls.every(u=>u.includes('/_homestead/events'))`), 'bob has an events stream open');
  const before = await bob.js(`window.__es.msgs`);
  await alice.addTask('Call plumber');
  await bob.until(`document.body.textContent.includes('Call plumber')`, 'alice\'s new task on bob (live)', 200);
  assert.ok(await bob.js(`window.__es.msgs`) > before, 'the update arrived as an event frame, not only by polling');
  await bob.addTask('Pick up bread');
  await alice.until(`document.body.textContent.includes('Pick up bread')`, 'bob\'s task on alice (live)', 200);

  // 5. Carol (signed in, no grant) cannot reach the data, and sees no list.
  assert.equal(await carol.js(`puter.kv.get('meta',{handle:${JSON.stringify(handle)}}).then(()=>'read',e=>e.code)`), 'kv_handle_not_found');
  assert.equal(await carol.js(`puter.kv.set('t:x','1',{handle:${JSON.stringify(handle)}}).then(()=>'wrote',e=>e.code)`), 'kv_handle_not_found');
  assert.deepEqual(await carol.js(`puter.kv.listShared({limit:50}).then(r=>r.items)`), []);
  assert.ok(!(await carol.text()).includes('Groceries'), 'carol sees no shared list');

  // 6. A read-only link opens in the bridge viewer in a context with no session.
  await alice.go(`${origin}${BASE}/#/lists`);
  await alice.until(`!!document.querySelector('.view-head')`, 'lists view');
  await alice.clickSel('button[aria-label="Open list: Groceries"]');
  await alice.until(`!!document.querySelector('button[aria-label="Sharing options"]')`, 'the sharing options button');
  await alice.clickSel('button[aria-label="Sharing options"]');
  await alice.until(`document.body.textContent.includes('Create view-only link')`, 'the read-only section');
  assert.ok(await alice.click('button', 'Create view-only link'));
  await alice.until(`!!document.querySelector('#share-link')`, 'the link');
  const link = await alice.js(`document.querySelector('#share-link').value`);
  assert.match(link, new RegExp(`^${origin}${BASE}/_homestead/view/[A-Za-z0-9_-]{43}$`), 'the link is the viewer');
  const visitor = await person('visitor'); // no sign-in
  await visitor.go(link);
  const viewer = await visitor.js(`document.documentElement.outerHTML`);
  assert.match(viewer, /Groceries/); assert.match(viewer, /Buy milk/); assert.match(viewer, /Call plumber/);
  assert.ok(!/<script/i.test(viewer), 'the viewer has no script');
  assert.deepEqual(await visitor.csp(), [], 'no CSP violation in the viewer');
  // Stop sharing the link: the viewer then answers the uniform 404.
  const dir404 = await fetch(link);
  assert.equal(dir404.status, 200);
  await alice.click('button', 'Stop sharing');
  await alice.until(`!!document.querySelector('[role=dialog] button, .modal button') && [...document.querySelectorAll('button')].some(b=>/Stop sharing/.test(b.textContent))`, 'confirm');
  await alice.js(`(()=>{const bs=[...document.querySelectorAll('button')].filter(b=>/Stop sharing/.test(b.textContent));bs[bs.length-1].click();return true})()`);
  await alice.until(`!document.querySelector('#share-link')`, 'the link gone');
  assert.equal((await fetch(link)).status, 404, 'the stopped link is a 404');

  // 7. Alice revokes bob; bob loses the share and can no longer read or write it.
  await alice.until(`document.body.textContent.includes('Remove bob') || !!document.querySelector('button[aria-label="Remove bob"]')`, 'the member row');
  await alice.clickSel('button[aria-label="Remove bob"]');
  await alice.until(`[...document.querySelectorAll('button')].some(b=>b.textContent.trim()==='Remove')`, 'confirm remove');
  await alice.click('button', 'Remove');
  await alice.until(`puter.kv.listSharedByMe({limit:50}).then(r=>r.items.filter(i=>!i.revokedAt).length===0)`, 'revoked on the bridge');
  assert.equal(await bob.js(`puter.kv.get('meta',{handle:${JSON.stringify(handle)}}).then(()=>'read',e=>e.code)`), 'kv_handle_not_found');
  assert.equal(await bob.js(`puter.kv.set('t:y','1',{handle:${JSON.stringify(handle)}}).then(()=>'wrote',e=>e.code)`), 'kv_handle_not_found');
  assert.deepEqual(await bob.js(`puter.kv.listShared({limit:50}).then(r=>r.items)`), []);
  // Alice's next task never reaches bob (he keeps a private copy by design).
  await alice.go(`${origin}${BASE}/#/lists`);
  await alice.clickSel('button[aria-label="Open list: Groceries"]');
  await alice.addTask('After revoke');
  await sleep(2500);
  assert.ok(!(await bob.text()).includes('After revoke'), 'bob does not receive tasks after the revoke');
  assert.equal(await bob.js(`puter.kv.list('tasks:collab:*',false).then(k=>k.length)`), 0, 'bob\'s own KV holds no copy of alice\'s shared keys');

  // 8. Everything stayed under the policy.
  for (const p of [alice, bob, carol]) assert.deepEqual(await p.csp(), [], `${p.name}: no CSP violation`);
  assert.deepEqual(exceptions, [], 'no uncaught exceptions');

  console.log('real-browser Tasks sharing check passed (Chrome', (await send('Browser.getVersion')).product + ')');
  ws.close(); clearTimeout(hard); await cleanup(); process.exit(0);
} catch (e) {
  console.error('FAIL:', e.stack || e);
  clearTimeout(hard); await cleanup(); process.exit(1);
}
