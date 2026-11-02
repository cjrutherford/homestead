// S378 real-browser check of Solitaire against the dev bridge: boots under the CSP,
// plays a move, keeps the saved game and statistics across a reload (in the browser,
// kept apart per person, and on the appliance through the person's key-value store),
// separates a second person in the same browser profile, serves the offline page, and
// proves Puter links, the hosted SDK and every foreign request are absent.
// TEST ONLY (dev identity is not authentication). Zero dependencies.
// usage: node browser-check-solitaire.mjs <bridge-bin> <dist-dir> <tmp-dir>
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import assert from 'node:assert/strict';

const [bin, dist, tmp] = process.argv.slice(2);
const BASE = '/apps/solitaire';
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
  bridge = spawn(bin, ['-dev-identity', '-db', path.join(tmp, 'bridge.db'), '-app', 'solitaire', '-listen', `127.0.0.1:${port}`,
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
  if (!bridge || bridge.exitCode !== null || bridge.signalCode !== null) return;
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

  // 1. alice boots under the CSP; same-origin only; no hosted SDK or Puter link.
  await cmd('Page.addScriptToEvaluateOnNewDocument', { source: `(()=>{const f=window.fetch;window.__net=[];window.fetch=function(u,o){window.__net.push('fetch '+(u&&u.url||u));return f.apply(this,arguments)}})()` });
  await go(`${origin}${BASE}/_homestead/login?subject=alice`);
  await until(`document.querySelectorAll('#board [data-kind="stock"] *').length > 0`, 'the board to deal');
  assert.deepEqual(await csp(), [], 'no CSP violation while booting');
  assert.deepEqual(await evalJs(`performance.getEntriesByType('resource').map(e=>new URL(e.name).origin).filter(o=>o!==location.origin)`), [], 'only same-origin requests');
  const html = await evalJs(`document.documentElement.outerHTML`);
  assert.ok(!/js\.puter\.com|plausible|jsdelivr|puter\.com|developer\.puter|Powered by/.test(html), 'no hosted SDK, analytics, CDN or Puter link');
  assert.equal(await evalJs(`typeof puter.ai + ' ' + typeof puter.net`), 'undefined undefined');
  assert.equal(await evalJs(`document.querySelectorAll('a[href^="http"]').length`), 0, 'no outbound links');

  // 2. Play a move; the saved game is kept per person and also written to the appliance.
  const play = () => evalJs(`(()=>{const s=document.querySelector('#board [data-kind="stock"]');const t=s;const r=t.getBoundingClientRect();const o={bubbles:true,button:0,pointerId:1,isPrimary:true,clientX:r.x+r.width/2,clientY:r.y+r.height/2};t.dispatchEvent(new PointerEvent('pointerdown',o));t.dispatchEvent(new PointerEvent('pointerup',o));return document.getElementById('moves').textContent})()`);
  await evalJs(`localStorage.setItem('alice-mark','1'),true`);
  await play(); await sleep(400);
  const moves1 = await evalJs(`document.getElementById('moves').textContent`);
  assert.notEqual(moves1.trim().replace(/\D/g, '') || '0', '0', 'a move was made: ' + moves1);
  await evalJs(`document.dispatchEvent(new Event('visibilitychange'));window.dispatchEvent(new Event('pagehide'));true`);
  await sleep(800);
  const keys = await evalJs(`JSON.stringify([...Array(localStorage.length).keys()].map(i=>localStorage.key(i)))`);
  assert.ok(keys.includes('solitaire.save.v1'), 'the saved game is in the person storage: ' + keys);
  assert.equal(await evalJs(`puter.kv.get('savedGame').then(v=>!!v)`), true, 'the saved game is on the appliance');
  assert.equal(await evalJs(`navigator.serviceWorker.ready.then(r=>r.scope)`), `${origin}${BASE}/`, 'the service worker serves the app path');

  // 3. A reload keeps alice's game.
  await go(`${origin}${BASE}/`);
  await until(`document.querySelectorAll('#board [data-kind="stock"]').length > 0`, 'the board to reload');
  assert.equal(await evalJs(`document.getElementById('moves').textContent`), moves1, 'the move count survives a reload');
  assert.deepEqual(await csp(), [], 'no CSP violation after reload');

  // 4. bob, same browser profile: a fresh game, none of alice's keys, nothing leaks into his appliance storage.
  await go(`${origin}${BASE}/_homestead/login?subject=bob`);
  await until(`document.querySelectorAll('#board [data-kind="stock"]').length > 0`, 'the board to deal for bob');
  assert.equal(await evalJs(`document.getElementById('moves').textContent.replace(/\\D/g,'')||'0'`), '0', 'bob starts a fresh game');
  assert.equal(await evalJs(`localStorage.getItem('solitaire.save.v1')`), null, 'bob does not see the saved game');
  assert.equal(await evalJs(`localStorage.getItem('alice-mark')`), null, 'bob does not see alice browser storage');
  assert.equal(await evalJs(`(JSON.parse(localStorage.getItem('solitaire.stats.v1')||'{"played":0}')).played`), 0, 'bob statistics are his own');
  assert.equal(await evalJs(`puter.kv.get('savedGame').then(v=>v===null)`), true, 'nothing of alice reaches bob appliance storage');
  assert.ok(!(await evalJs(`document.body.innerText`)).includes('alice'), 'bob does not see alice subject');
  await go(`${origin}${BASE}/_homestead/login?subject=alice`);
  await until(`document.querySelectorAll('#board [data-kind="stock"]').length > 0`, 'alice back');
  assert.equal(await evalJs(`document.getElementById('moves').textContent`), moves1, 'alice still has her game');
  assert.equal(await evalJs(`localStorage.getItem('alice-mark')`), '1', 'and her own browser storage');

  // 5. Offline: the service worker answers a navigation with the fixed offline page and caches no page or data.
  await stopBridge(); // the worker's own fetches are not covered by page throttling, so the server goes away
  await go(`${origin}${BASE}/`);
  assert.ok((await evalJs(`document.body.innerText`)).includes('You are offline'), 'the offline page is shown');
  const cached = await evalJs(`caches.keys().then(async ks=>{const out=[];for(const k of ks){const c=await caches.open(k);out.push(...(await c.keys()).map(r=>new URL(r.url).pathname))}return out})`);
  assert.ok(cached.every((p) => /\/assets\//.test(p)), 'only build assets are cached: ' + JSON.stringify(cached));
  assert.deepEqual(exceptions.filter((e) => !/Failed to fetch|ERR_INTERNET_DISCONNECTED/.test(e)), [], 'no uncaught exceptions');

  const foreign = requests.filter((r) => !r.url.startsWith(origin + BASE + '/') && r.url !== origin + '/favicon.ico' /* the fixed offline page names no icon, so Chrome asks the origin root */ && !r.url.startsWith('data:') && !r.url.startsWith('blob:'));
  assert.deepEqual(foreign, [], 'every request stays on the app origin and path');
  console.log('real-browser Solitaire check passed (Chrome', (await send('Browser.getVersion')).product + ')');
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
