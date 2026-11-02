// S369 real-browser check of the Projects app against the dev bridge: the built
// app boots under the CSP, creates a project and a task through its UI, keeps
// them across a reload, snapshots, isolates a second person, and survives
// the bridge going away.
// TEST ONLY (dev identity is not authentication). Zero dependencies: drives
// headless Chrome/Chromium over the DevTools protocol with Node's WebSocket.
// usage: node browser-check.mjs <bridge-bin> <dist-dir> <tmp-dir>
// Exit 0 pass; exit 0 with "SKIP:" if no browser; exit 1 on failure.
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import assert from 'node:assert/strict';

const [bin, dist, tmp] = process.argv.slice(2);
const BASE = '/apps/projects';
const candidates = [process.env.CHROME, 'google-chrome', 'chromium', 'chromium-browser'].filter(Boolean);
let chrome = null;
for (const c of candidates) {
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
async function startBridge() {
  bridge = spawn(bin, ['-dev-identity', '-db', path.join(tmp, 'bridge.db'), '-app', 'projects', '-listen', `127.0.0.1:${port}`,
    '-origin', origin, '-base-path', BASE, '-static-dir', dist],
  { env: { ...process.env, HOMESTEAD_EVERYTHING_INSECURE_DEV: 'i-understand-this-is-not-authentication' }, stdio: ['ignore', 'ignore', 'pipe'] });
  let log = '';
  await new Promise((resolve, reject) => {
    bridge.stderr.on('data', (d) => { log += d; if (log.includes('listening on')) resolve(); });
    bridge.once('exit', (c) => reject(new Error('bridge exited ' + c + log)));
    setTimeout(() => reject(new Error('bridge start timeout ' + log)), 10000);
  });
  bridge.stderr.resume();
}
async function stopBridge() {
  if (!bridge || bridge.exitCode !== null) return;
  await new Promise((r) => { bridge.once('exit', r); bridge.kill('SIGTERM'); });
}

const profile = path.join(tmp, 'profile');
fs.mkdirSync(profile, { recursive: true });
const browser = spawn(chrome, ['--headless=new', '--no-sandbox', '--disable-gpu', '--remote-debugging-port=0',
  `--user-data-dir=${profile}`, '--no-first-run', '--no-default-browser-check', 'about:blank'], { stdio: 'ignore' });
const cleanup = async () => { browser.kill('SIGKILL'); await stopBridge(); };
const hard = setTimeout(async () => { console.error('FAIL: timeout'); await cleanup(); process.exit(1); }, 120000);

async function wsUrl() {
  const f = path.join(profile, 'DevToolsActivePort');
  for (let i = 0; i < 100; i++) {
    if (fs.existsSync(f)) {
      const [p] = fs.readFileSync(f, 'utf8').split('\n');
      const v = await (await fetch(`http://127.0.0.1:${p}/json/version`)).json();
      return v.webSocketDebuggerUrl;
    }
    await sleep(100);
  }
  throw new Error('browser did not start');
}

try {
  await startBridge();
  const ws = new WebSocket(await wsUrl());
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let id = 0; const pending = new Map(); const waiters = []; const exceptions = [];
  ws.onmessage = (m) => {
    const msg = JSON.parse(m.data);
    if (msg.id && pending.has(msg.id)) { const { res, rej } = pending.get(msg.id); pending.delete(msg.id); msg.error ? rej(new Error(JSON.stringify(msg.error))) : res(msg.result); }
    else {
      if (msg.method === 'Runtime.exceptionThrown') exceptions.push(JSON.stringify(msg.params.exceptionDetails).slice(0, 300));
      for (const w of waiters.slice()) if (w.method === msg.method) { waiters.splice(waiters.indexOf(w), 1); w.res(msg.params); }
    }
  };
  const send = (method, params = {}, sessionId) => new Promise((res, rej) => {
    const i = ++id; pending.set(i, { res, rej }); ws.send(JSON.stringify({ id: i, method, params, sessionId }));
  });
  const event = (method) => new Promise((res) => waiters.push({ method, res }));
  const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
  const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
  const cmd = (m, p) => send(m, p, sessionId);
  await cmd('Page.enable');
  await cmd('Runtime.enable');
  // Record every CSP violation in every document.
  await cmd('Page.addScriptToEvaluateOnNewDocument', { source: `window.__csp=[];document.addEventListener('securitypolicyviolation',e=>window.__csp.push(e.violatedDirective+' '+e.blockedURI));` });
  const evalJs = async (expression) => {
    const r = await cmd('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error('eval: ' + JSON.stringify(r.exceptionDetails).slice(0, 400));
    return r.result.value;
  };
  const go = async (url) => { const loaded = event('Page.loadEventFired'); await cmd('Page.navigate', { url }); await Promise.race([loaded, sleep(15000)]); await sleep(300); };
  const until = async (expr, what) => { for (let i = 0; i < 100; i++) { if (await evalJs(expr)) return; await sleep(100); } throw new Error('timeout waiting for ' + what); };
  const clickText = (sel, text) => evalJs(`(()=>{const b=[...document.querySelectorAll(${JSON.stringify(sel)})].find(x=>x.textContent.includes(${JSON.stringify(text)}));if(!b)return false;b.click();return true})()`);
  const setValue = (sel, v) => evalJs(`(()=>{const i=document.querySelector(${JSON.stringify(sel)});i.value=${JSON.stringify(v)};i.dispatchEvent(new Event('input',{bubbles:true}));return true})()`);
  const csp = () => evalJs(`window.__csp||[]`);

  // 1. Sign in as alice; the app boots from the built bundle under the CSP.
  await go(`${origin}${BASE}/_homestead/login?subject=alice`);
  await until(`!!document.querySelector('.home-view') || !!document.querySelector('.view')`, 'the app to boot');
  assert.deepEqual(await csp(), [], 'no CSP violation while booting');
  await evalJs(`navigator.serviceWorker.ready.then(()=>true)`);
  // Close the first-run onboarding if it is open.
  await evalJs(`(()=>{for (const t of ['Skip','Close','Got it','Not now']) {const b=[...document.querySelectorAll('button')].find(x=>x.textContent.trim()===t); if(b){b.click();return true}} return false})()`);
  await sleep(200);

  // 2. Create a project and a task through the real UI.
  assert.ok(await clickText('.home-view button', 'New project'), 'New project button');
  await until(`!!document.querySelector('input[placeholder="e.g. Website redesign"]')`, 'the project form');
  await setValue('input[placeholder="e.g. Website redesign"]', 'Kitchen rebuild');
  await evalJs(`document.querySelector('input[placeholder="e.g. Website redesign"]').form.requestSubmit()`);
  await until(`location.hash.startsWith('#/p/')`, 'the new project board');
  const projectId = (await evalJs(`location.hash`)).split('/')[2];
  await until(`!!document.querySelector('.add-card-btn')`, 'the board');
  await evalJs(`document.querySelector('.add-card-btn').click()`);
  await until(`!!document.querySelector('.composer-input')`, 'the composer');
  await setValue('.composer-input', 'Order the cabinets');
  await evalJs(`(()=>{const i=document.querySelector('.composer-input');i.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true,cancelable:true}));return true})()`);
  await until(`document.body.textContent.includes('Order the cabinets')`, 'the task card');
  // The save is debounced; wait until the document and a restore point are on the bridge.
  await until(`puter.fs.readdir('projects-data/snapshots/project-${projectId}').then(l=>l.length>0,()=>false)`, 'a restore point');
  const doc = await evalJs(`puter.fs.read('projects-data/project-${projectId}.json').then(b=>b.text())`);
  assert.ok(doc.includes('Order the cabinets') && doc.includes('Kitchen rebuild'), 'the document holds the task');
  const st = await evalJs(`puter.fs.stat('projects-data/project-${projectId}.json')`);
  assert.ok(st.size > 0 && st.modified > 0, 'stat reports size and stamp');

  // 3. A reload (a new page load, new bundle execution) keeps the data.
  await go(`${origin}${BASE}/`);
  await until(`document.body.textContent.includes('Kitchen rebuild')`, 'the project to reappear after a reload');
  assert.deepEqual(await csp(), [], 'no CSP violation after reload');

  // 4. Offline: with the bridge gone a write rejects network_error (the app
  // keeps its own offline copy); navigation gets the token-free page.
  await stopBridge();
  assert.equal(await evalJs(`puter.fs.write('projects-data/x.json','1').then(()=>'ok',e=>e.code)`), 'network_error');
  await go(`${origin}${BASE}/`);
  const offline = await evalJs(`document.documentElement.outerHTML`);
  assert.match(offline, /You are offline/);
  assert.ok(!offline.includes('homestead-csrf'), 'offline page has no token');

  // 5. Back online: bob (same browser) sees none of alice's projects.
  await startBridge();
  await go(`${origin}${BASE}/_homestead/login?subject=bob`);
  await until(`!!document.querySelector('.view')`, 'the app to boot for bob');
  await sleep(500);
  assert.ok(!(await evalJs(`document.body.textContent`)).includes('Kitchen rebuild'), 'bob sees alice project');
  assert.equal(await evalJs(`puter.fs.readdir('projects-data').then(()=>'found',e=>e.code)`), 'subject_does_not_exist');
  assert.deepEqual(await csp(), [], 'no CSP violation for bob');
  assert.deepEqual(exceptions, [], 'no uncaught exceptions');

  console.log('real-browser Projects check passed (Chrome', (await send('Browser.getVersion')).product + ')');
  ws.close();
  clearTimeout(hard);
  await cleanup();
  process.exit(0);
} catch (e) {
  console.error('FAIL:', e.stack || e);
  clearTimeout(hard);
  await cleanup();
  process.exit(1);
}
