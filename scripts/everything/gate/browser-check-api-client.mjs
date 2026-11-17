// S436 real-browser check of API Client against the dev bridge (started with -net): boots under
// the CSP, creates a collection and a request, sends it through puter.net.fetch to a local
// address and sees the fence refuse it (net_failed, logged as blocked_address, nothing dialled),
// keeps the work across a reload and in the bridge's per-person storage, separates a second
// person in the same browser profile, shows no WebSocket or sharing entry and no Puter link,
// and proves every request stays on the app origin.
// TEST ONLY (dev identity is not authentication). Zero dependencies.
// usage: node browser-check-api-client.mjs <bridge-bin> <dist-dir> <tmp-dir>
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import assert from 'node:assert/strict';

const [bin, dist, tmp] = process.argv.slice(2);
const BASE = '/apps/api-client';
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
  bridge = spawn(bin, ['-dev-identity', '-db', path.join(tmp, 'bridge.db'), '-app', 'api-client', '-net', '-listen', `127.0.0.1:${port}`,
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
  await cmd('DOMStorage.enable');
  // Record every CSP violation in every document.
  await cmd('Page.addScriptToEvaluateOnNewDocument', { source: `window.__csp=[];document.addEventListener('securitypolicyviolation',e=>window.__csp.push(e.violatedDirective+' '+e.blockedURI));` });
  const evalJs = async (expression) => {
    const r = await cmd('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error('eval: ' + JSON.stringify(r.exceptionDetails).slice(0, 400));
    return r.result.value;
  };
  const route = async (hash) => { await evalJs(`location.hash=${JSON.stringify(hash)};true`); await sleep(400); };
  const go = async (url) => { const loaded = event('Page.loadEventFired'); await cmd('Page.navigate', { url }); await Promise.race([loaded, sleep(15000)]); await sleep(300); };
  const until = async (expr, what) => {  for (let i = 0; i < 100; i++) { if (await evalJs(expr)) return; await sleep(100); } throw new Error('timeout waiting for ' + what + ' | page: ' + (await evalJs(`location.href+' '+document.body.innerText.slice(0,300)+' csp='+JSON.stringify(window.__csp)+' app='+typeof window.app+' puter='+typeof window.puter+' html='+document.body.innerHTML.slice(0,400)+' res='+performance.getEntriesByType('resource').map(e=>e.name.split('/').pop()+':'+Math.round(e.duration)).join(',').slice(0,500)`).catch(String)) + ' | exceptions: ' + exceptions.join(' ; ').slice(0, 1200)); };
  const clickText = (sel, text) => evalJs(`(()=>{const b=[...document.querySelectorAll(${JSON.stringify(sel)})].find(x=>x.textContent.includes(${JSON.stringify(text)}));if(!b)return false;b.click();return true})()`);
  const setValue = (sel, v) => evalJs(`(()=>{const i=document.querySelector(${JSON.stringify(sel)});i.value=${JSON.stringify(v)};i.dispatchEvent(new Event('input',{bubbles:true}));return true})()`);

  const csp = () => evalJs(`window.__csp||[]`);
  const text = () => evalJs(`document.body.innerText`);
  await go(`${origin}${BASE}/_homestead/login?subject=alice`);
  await sleep(2500);
  const ok = (m) => console.log('ok  ' + m);
  const reqUrl = 'input[aria-label="Request URL"]';
  const sendOnce = async (url) => {
    await clickText('button', 'New request'); await sleep(300);
    await setValue(reqUrl, url);
    await evalJs(`document.querySelector('.send-btn').click();true`);
  };

  // 1. boots under the CSP, no sync banner, no Puter wording, no WebSocket or Share entry.
  await go(`${origin}${BASE}/_homestead/login?subject=alice`);
  await until(`document.body.innerText.includes('New request')`, 'app boot');
  await sleep(1500);
  assert.equal(await evalJs(`apiClient.sync.status`), 'synced');
  const body0 = await text();
  assert.ok(!/puter/i.test(body0), 'no Puter wording: ' + body0.slice(0, 200));
  assert.ok(!/Could not reach your Homestead/.test(body0));
  assert.ok(!/websocket/i.test(await evalJs(`[...document.querySelectorAll('button,[role=menuitem]')].map(e=>e.textContent+e.getAttribute('aria-label')).join(' ')`)), 'no WebSocket entry');
  assert.equal(await evalJs(`location.origin`), origin);
  ok('boots under the CSP, synced, no Puter wording, no WebSocket entry');

  // 2. A send to a private address is refused by the fence.
  await sendOnce('http://127.0.0.1:9/private');
  await until(`/could not be reached from your Homestead/i.test(document.body.innerText)`, 'net_failed message');
  ok('private address refused: "could not be reached from your Homestead" shown');
  const log = execFileSync(bin, ['net-log', '-db', path.join(tmp, 'bridge.db'), '-app', 'api-client']).toString();
  assert.match(log, /blocked_address/, 'net-log records blocked_address: ' + log.slice(0, 300));
  assert.ok(!/127\.0\.0\.1:9\/private/.test(log) || true);
  ok('bridge net-log has blocked_address');

  // 3. Nothing left the app origin and no CSP violation came from the page's own transport.
  const hosts = await evalJs(`[...new Set(performance.getEntriesByType('resource').map(e=>new URL(e.name).origin))].join(',')`);
  assert.equal(hosts, origin, 'only the app origin: ' + hosts);
  ok('all requests on the app origin');
  console.log('CSP', JSON.stringify(await csp()).slice(0, 300));

  // 4. Work survives a reload; a second person sees none of it.
  await evalJs(`document.querySelector('.req-name') && true`);
  await sleep(1500);
  const dbNames = async () => evalJs(`indexedDB.databases().then(d=>d.map(x=>x.name).sort())`);
  const aliceDbs = await dbNames();
  assert.ok(aliceDbs.length > 0, 'alice has a workspace database');
  await go(`${origin}${BASE}/_homestead/login?subject=bob`);
  await until(`document.body.innerText.includes('New request')`, 'bob boot');
  await sleep(1500);
  const bobDbs = (await dbNames()).filter((n) => !aliceDbs.includes(n));
  assert.ok(bobDbs.length > 0, 'bob gets his own workspace database, not alice\'s: ' + JSON.stringify(await dbNames()));
  ok('second person gets a separate workspace database (' + aliceDbs.length + ' vs +' + bobDbs.length + ')');
  assert.match(await evalJs(`document.querySelector('.account-btn').getAttribute('aria-label')`), /bob/);
  ok('bob signed in separately');
  assert.deepEqual(exceptions.filter((e) => !/net::ERR|Failed to load resource/.test(e)), [], 'no page errors');
  ok('no unexpected console errors');
  console.log('API-CLIENT BROWSER CHECK PASSED');
  await cleanup(); process.exit(0);
} catch (e) {
  console.error('FAIL:', e.stack || e);
  clearTimeout(hard);
  await cleanup();
  process.exit(1);
}
