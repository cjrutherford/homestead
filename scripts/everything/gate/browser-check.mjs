// S367 real-browser check of the Tasks service worker against the dev bridge.
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
const BASE = '/apps/tasks';
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
  bridge = spawn(bin, ['-dev-identity', '-db', path.join(tmp, 'bridge.db'), '-app', 'tasks', '-listen', `127.0.0.1:${port}`,
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
  let id = 0; const pending = new Map(); const waiters = [];
  ws.onmessage = (m) => {
    const msg = JSON.parse(m.data);
    if (msg.id && pending.has(msg.id)) { const { res, rej } = pending.get(msg.id); pending.delete(msg.id); msg.error ? rej(new Error(JSON.stringify(msg.error))) : res(msg.result); }
    else for (const w of waiters.slice()) if (w.method === msg.method) { waiters.splice(waiters.indexOf(w), 1); w.res(msg.params); }
  };
  const send = (method, params = {}, sessionId) => new Promise((res, rej) => {
    const i = ++id; pending.set(i, { res, rej }); ws.send(JSON.stringify({ id: i, method, params, sessionId }));
  });
  const event = (method) => new Promise((res) => waiters.push({ method, res }));
  const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
  const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
  const cmd = (m, p) => send(m, p, sessionId);
  await cmd('Page.enable');
  const evalJs = async (expression) => {
    const r = await cmd('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error('eval: ' + JSON.stringify(r.exceptionDetails).slice(0, 400));
    return r.result.value;
  };
  const go = async (url) => { const loaded = event('Page.loadEventFired'); await cmd('Page.navigate', { url }); await Promise.race([loaded, sleep(15000)]); await sleep(300); };
  const meta = () => evalJs(`document.querySelector('meta[name="homestead-csrf"]')?.content || ''`);
  // Every cached request URL and body in Cache Storage, from the page.
  const cacheDump = () => evalJs(`(async()=>{const out=[];for(const k of await caches.keys()){const c=await caches.open(k);for(const r of await c.keys()){out.push({url:r.url,body:await (await c.match(r)).text()});}}return out;})()`);

  // 1. Sign in as alice; the page carries her token; the worker installs.
  await go(`${origin}${BASE}/_homestead/login?subject=alice`);
  const aliceToken = await meta();
  assert.ok(aliceToken.length >= 32, 'index carries a CSRF token after sign-in');
  await evalJs(`navigator.serviceWorker.ready.then(()=>true)`);
  await go(`${origin}${BASE}/`); // reload under the worker's control
  assert.equal(await evalJs(`!!navigator.serviceWorker.controller`), true, 'page is controlled by the worker');
  assert.equal(await meta(), aliceToken, 'controlled reload still has alice token (from the network)');
  assert.equal(await evalJs(`puter.auth.getUser().then(u=>u.username)`), 'alice');
  assert.equal(await evalJs(`puter.kv.set('rb:k','v1')`), true);

  // 2. Cache Storage never holds the index, bridge paths or any token.
  let dump = await cacheDump();
  assert.ok(dump.length > 0, 'static assets are cached');
  for (const e of dump) {
    const p = new URL(e.url).pathname;
    assert.ok(p !== `${BASE}/` && !p.endsWith('index.html') && !p.includes('/_homestead/'), 'cached forbidden path ' + p);
    assert.ok(!e.body.includes(aliceToken) && !e.body.includes('homestead-csrf'), 'token in cache: ' + p);
  }
  assert.ok(dump.some((e) => /\/assets\//.test(e.url)), 'a hashed asset is cached');

  // 3. Offline (bridge down): writes reject network_error so Tasks queues
  // them; navigation gets the token-free fallback, not a cached page.
  await stopBridge();
  assert.equal(await evalJs(`puter.kv.set('rb:k','v2').then(()=>'ok',e=>e.code)`), 'network_error');
  await go(`${origin}${BASE}/`);
  const body = await evalJs(`document.documentElement.outerHTML`);
  assert.match(body, /You are offline/);
  assert.ok(!body.includes(aliceToken) && !body.includes('homestead-csrf'), 'offline page has no token');

  // 4. Back online: bridge restarted (dev sessions are in memory, so alice's
  // cookie is dead). The worker must not serve a stale page; signed out
  // loads go to sign-in. Bob signing in on the same browser gets his own token.
  await startBridge();
  await go(`${origin}${BASE}/_homestead/login?subject=bob`);
  const bobToken = await meta();
  assert.ok(bobToken && bobToken !== aliceToken, 'bob gets a fresh token, not the old page');
  assert.equal(await evalJs(`puter.auth.getUser().then(u=>u.username)`), 'bob');
  assert.equal(await evalJs(`puter.kv.get('rb:k')`), null, 'bob cannot see alice data');
  dump = await cacheDump();
  for (const e of dump) assert.ok(!e.body.includes(aliceToken) && !e.body.includes(bobToken), 'token in cache after second sign-in');
  // A token refresh recovers from a page whose meta is stale.
  assert.equal(await evalJs(`(()=>{document.querySelector('meta[name=homestead-csrf]').content='stale';return puter.kv.set('rb:k2','x')})()`), true);

  console.log('real-browser check passed (Chrome', (await send('Browser.getVersion')).product + ')');
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
