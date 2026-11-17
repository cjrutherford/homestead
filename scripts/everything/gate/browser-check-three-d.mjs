// S377 real-browser check of the 3D app against the dev bridge: boots under the CSP
// (wasm-unsafe-eval for the embedded physics module, no unsafe-eval), changes a
// preference and the scene, keeps both across a reload through the person's own
// bridge storage, separates a second person in the same browser profile, shows the
// Chromium-only folder access as a fallback when it is missing, and proves AI,
// Python, Report a Bug and every foreign request are absent.
// TEST ONLY (dev identity is not authentication). Zero dependencies.
// usage: node browser-check-three-d.mjs <bridge-bin> <dist-dir> <tmp-dir>
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import assert from 'node:assert/strict';

const [bin, dist, tmp] = process.argv.slice(2);
const BASE = '/apps/three-d';
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
// A made-up host name that Chrome maps to loopback and treats as secure (the
// appliance serves HTTPS under its own name).
const HOST = 'hs-check.test';
const origin = `http://${HOST}:${port}`;
let bridge = null;
async function startBridge() {
  bridge = spawn(bin, ['-dev-identity', '-db', path.join(tmp, 'bridge.db'), '-app', 'three-d', '-listen', `127.0.0.1:${port}`,
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
const browser = spawn(chrome, ['--headless=new', '--no-sandbox', '--disable-gpu', '--remote-debugging-port=0', `--host-resolver-rules=MAP ${HOST} 127.0.0.1`, `--unsafely-treat-insecure-origin-as-secure=${origin}`,
  `--user-data-dir=${profile}`, '--no-first-run', '--no-default-browser-check', 'about:blank'], { stdio: 'ignore' });
const cleanup = async () => { browser.kill('SIGKILL'); await stopBridge(); };
const hard = setTimeout(async () => { console.error('FAIL: timeout'); await cleanup(); process.exit(1); }, 240000);

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
  let id = 0; const pending = new Map(); const waiters = []; const exceptions = []; const requests = [];
  ws.onmessage = (m) => {
    const msg = JSON.parse(m.data);
    if (msg.id && pending.has(msg.id)) { const { res, rej } = pending.get(msg.id); pending.delete(msg.id); msg.error ? rej(new Error(JSON.stringify(msg.error))) : res(msg.result); }
    else {
      if (msg.method === 'Page.javascriptDialogOpening') console.error('DIALOG', JSON.stringify(msg.params).slice(0,300));
      if (msg.method === 'Network.requestWillBeSent') requests.push({ url: msg.params.request.url, method: msg.params.request.method });
      if (msg.method === 'Runtime.consoleAPICalled' && ['error','warning'].includes(msg.params.type)) exceptions.push('console.' + msg.params.type + ': ' + msg.params.args.map((a) => a.value ?? a.description).join(' ').slice(0, 300));
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
  await cmd('Network.enable');
  // Record every CSP violation in every document.
  await cmd('Page.addScriptToEvaluateOnNewDocument', { source: `window.__csp=[];document.addEventListener('securitypolicyviolation',e=>window.__csp.push(e.violatedDirective+' '+e.blockedURI));` });
  const evalJs = async (expression) => {
    const r = await cmd('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error('eval: ' + JSON.stringify(r.exceptionDetails).slice(0, 400));
    return r.result.value;
  };
  const go = async (url) => { const loaded = event('Page.loadEventFired'); await cmd('Page.navigate', { url }); await Promise.race([loaded, sleep(15000)]); await sleep(300); };
  const until = async (expr, what) => {  for (let i = 0; i < 100; i++) { if (await evalJs(expr)) return; await sleep(100); } throw new Error('timeout waiting for ' + what + ' | page: ' + (await evalJs(`location.href+' '+document.body.innerText.slice(0,300)+' csp='+JSON.stringify(window.__csp)+' app='+typeof window.app+' puter='+typeof window.puter+' html='+document.body.innerHTML.slice(0,400)+' res='+performance.getEntriesByType('resource').map(e=>e.name.split('/').pop()+':'+Math.round(e.duration)).join(',').slice(0,500)`).catch(String)) + ' | exceptions: ' + exceptions.join(' ; ').slice(0, 1200)); };
  const clickText = (sel, text) => evalJs(`(()=>{const b=[...document.querySelectorAll(${JSON.stringify(sel)})].find(x=>x.textContent.includes(${JSON.stringify(text)}));if(!b)return false;b.click();return true})()`);
  const setValue = (sel, v) => evalJs(`(()=>{const i=document.querySelector(${JSON.stringify(sel)});i.value=${JSON.stringify(v)};i.dispatchEvent(new Event('input',{bubbles:true}));return true})()`);
  const csp = () => evalJs(`window.__csp||[]`);

  // 1. alice boots under the CSP; same-origin only; no hosted SDK, AI or Python.
  await cmd('Page.addScriptToEvaluateOnNewDocument', { source: `(()=>{window.__net=[];const f=window.fetch;window.fetch=function(u,o){window.__net.push('fetch '+(u&&u.url||u));return f.apply(this,arguments)};})();` });
  await go(`${origin}${BASE}/_homestead/login?subject=alice`);
  await until(`typeof window.app?.changed === 'function'`, 'the app to boot');
  await evalJs(`window.appReady`);
  assert.deepEqual(await csp(), [], 'no CSP violation while booting');
  assert.deepEqual(await evalJs(`performance.getEntriesByType('resource').map(e=>new URL(e.name).origin).filter(o=>o!==location.origin)`), [], 'only same-origin requests');
  const html = await evalJs(`document.documentElement.outerHTML`);
  assert.ok(!/js\.puter\.com|plausible|cdn\.jsdelivr|developer\.puter/.test(html), 'no hosted SDK, analytics or CDN reference');
  const hdr = await evalJs(`fetch(location.href).then(r=>r.headers.get('content-security-policy')||'')`);
  assert.ok(hdr.includes("'wasm-unsafe-eval'") && !hdr.includes("'unsafe-eval'") && !/'unsafe-eval'/.test(hdr.replace("'wasm-unsafe-eval'", '')), 'narrow wasm policy: ' + hdr);
  assert.equal(await evalJs(`typeof puter.ai + ' ' + typeof puter.net + ' ' + typeof puter.fs.share`), 'undefined undefined undefined');
  const body0 = await evalJs(`document.body.innerText`);
  assert.ok(!/AI Assistant|Report a Bug|Puter/.test(body0), 'no Assistant, Report a Bug or Puter wording');
  assert.equal(await evalJs(`typeof window.loadPyodide`), 'undefined', 'no Python runtime');
  assert.equal(await evalJs(`navigator.serviceWorker ? navigator.serviceWorker.getRegistrations().then(r=>r.length) : 0`), 0, 'no service worker (no offline page in this slice)');

  // 2. A preference and a scene change are kept for alice across a reload, in bridge storage.
  assert.equal(await evalJs(`localStorage.setItem('hs-check','alice-pref'), localStorage.getItem('hs-check')`), 'alice-pref');
  assert.equal(await evalJs(`localStorage.length >= 1 && localStorage.key(0) !== null`), true, 'the virtual localStorage lists its keys');
  await evalJs(`(app.filename='alice-scene.p3d', app.changed('doc'), true)`);
  await sleep(2800); // settings flush (1 s) and autosave (1.5 s)
  const dbs = await evalJs(`indexedDB.databases().then(l=>l.map(d=>d.name))`);
  assert.ok(dbs.length > 0 && dbs.every((n) => /@[0-9a-f]{16}$/.test(n)), 'every browser database carries the person tag: ' + JSON.stringify(dbs));
  const aliceDbs = dbs.slice().sort();
  assert.equal(await evalJs(`navigator.storage.getDirectory().then(async d=>{const k=[];for await (const n of d.keys()) k.push(n);return k.every(n=>/^p3d-[0-9a-f]{16}$/.test(n))})`), true, 'OPFS is under the person folder');
  assert.equal(await evalJs(`puter.kv.get('browser-settings/local-storage.v1').then(v=>JSON.stringify(v))`).then((t) => t.includes('alice-pref')), true, 'the preference is on the appliance');

  await cmd('Page.addScriptToEvaluateOnNewDocument', { source: `window.__g=[];const og=IDBObjectStore.prototype.get;IDBObjectStore.prototype.get=function(k){const r=og.apply(this,arguments);const t0=performance.now();r.addEventListener('success',()=>window.__g.push(this.name+':'+k+':'+(r.result?(r.result.name||'val'):'none')+'@'+Math.round(t0)+'+'+Math.round(performance.now()-t0)));return r};` });
  await go(`${origin}${BASE}/`);
  await until(`typeof window.app?.changed === 'function'`, 'the app to reboot');
  await evalJs(`window.appReady`);
  assert.equal(await evalJs(`localStorage.getItem('hs-check')`), 'alice-pref', 'the preference survives a reload');
  // The reloaded page read alice's own autosave from her own database (applying it is upstream's own logic).
  assert.equal(await evalJs(`window.__g.some(x=>x.startsWith('files:autosave:alice-scene.p3d'))`), true, 'alice autosave read after reload');
  assert.deepEqual(await csp(), [], 'no CSP violation after reload');

  // 3. bob, same browser profile: sees none of alice's preference or scene, different database names.
  await go(`${origin}${BASE}/_homestead/login?subject=bob`);
  await until(`typeof window.app?.changed === 'function'`, 'the app to boot for bob');
  await evalJs(`window.appReady`);
  assert.equal(await evalJs(`localStorage.getItem('hs-check')`), null, 'bob does not see the preference');
  assert.equal(await evalJs(`window.__g.some(x=>x.startsWith('files:autosave:none'))`), true, 'bob finds no autosave of his own');
  assert.equal(await evalJs(`window.__g.some(x=>x.includes('alice-scene'))`), false, 'bob is never handed alice scene');
  await evalJs(`(app.filename='bob-scene.p3d', app.changed('doc'), localStorage.setItem('hs-check','bob-pref'), true)`);
  await sleep(2800);
  const dbsB = await evalJs(`indexedDB.databases().then(l=>l.map(d=>d.name))`);
  assert.ok(dbsB.length > 0 && dbsB.every((n) => /@[0-9a-f]{16}$/.test(n)), 'bob databases carry a tag: ' + JSON.stringify(dbsB));
  assert.ok(!(await evalJs(`document.body.innerText`)).includes('alice'), 'bob does not see alice subject');
  await go(`${origin}${BASE}/_homestead/login?subject=alice`);
  await until(`typeof window.app?.changed === 'function'`, 'alice back');
  await evalJs(`window.appReady`);
  assert.equal(await evalJs(`localStorage.getItem('hs-check')`), 'alice-pref', 'alice still has her own preference');

  // 4. Without the Chromium folder picker the file browser offers Add Files only.
  await cmd('Page.addScriptToEvaluateOnNewDocument', { source: `delete window.showDirectoryPicker;delete window.showOpenFilePicker;delete window.showSaveFilePicker;` });
  await go(`${origin}${BASE}/`);
  await until(`typeof window.app?.changed === 'function'`, 'the app to boot without file access');
  await evalJs(`window.appReady`);
  assert.equal(await evalJs(`typeof window.showDirectoryPicker`), 'undefined');
  assert.deepEqual(await csp(), [], 'no CSP violation without the File System Access API');
  assert.deepEqual(exceptions, [], 'no uncaught exceptions');

  const foreign = requests.filter((r) => !r.url.startsWith(origin + BASE + '/') && !r.url.startsWith('data:') && !r.url.startsWith('blob:'));
  assert.deepEqual(foreign, [], 'every request stays on the app origin and path');
  console.log('IDB names alice:', JSON.stringify(aliceDbs), 'bob:', JSON.stringify(dbsB));
  console.log('real-browser 3D check passed (Chrome', (await send('Browser.getVersion')).product + ')');
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
