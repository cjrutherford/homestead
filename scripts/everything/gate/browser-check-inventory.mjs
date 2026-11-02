// S373 real-browser check of the Inventory app against the dev bridge: the built
// app boots under the CSP, creates a product with a photo through its UI, keeps
// both across a reload, shows the offline page and isolates a second person
// (data, photos and the browser-side mirror).
// TEST ONLY (dev identity is not authentication). Zero dependencies: drives
// headless Chrome/Chromium over the DevTools protocol with Node's WebSocket.
// usage: node browser-check-inventory.mjs <bridge-bin> <dist-dir> <tmp-dir>
// Exit 0 pass; exit 0 with "SKIP:" if no browser; exit 1 on failure.
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import assert from 'node:assert/strict';

const [bin, dist, tmp] = process.argv.slice(2);
const BASE = '/apps/inventory';
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
  bridge = spawn(bin, ['-dev-identity', '-db', path.join(tmp, 'bridge.db'), '-app', 'inventory', '-listen', `127.0.0.1:${port}`,
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
  await until(`!!document.querySelector('.sidebar')`, 'the app to boot');
  assert.deepEqual(await csp(), [], 'no CSP violation while booting');
  await evalJs(`navigator.serviceWorker.ready.then((r)=>r.scope)`).then((scope) => assert.equal(scope, `${origin}${BASE}/`, 'worker scope'));
  assert.deepEqual(await evalJs(`performance.getEntriesByType('resource').map(e=>new URL(e.name).origin).filter(o=>o!==location.origin)`), [], 'only same-origin requests');
  const html = await evalJs(`document.documentElement.outerHTML`);
  assert.ok(!/js\.puter\.com|plausible/.test(html), 'no hosted SDK or analytics reference');
  assert.equal(await evalJs(`fetch(document.querySelector('link[rel=manifest]').href).then(r=>r.json()).then(m=>[m.start_url,m.icons[0].src].join())`), './,./icon-192.png');
  assert.equal(await evalJs(`typeof puter.ui + ' ' + typeof puter.ai`), 'undefined undefined');
  // The sidebar never shows the opaque sign-in subject, and has no Sign out.
  const side = await evalJs(`document.querySelector('.sidebar').textContent`);
  assert.ok(!side.includes('alice') && !/Sign out/.test(side), 'sidebar: ' + side);
  assert.ok(!(await evalJs(`document.querySelector('.sidebar-user').getAttribute('aria-label')||''`)).includes('alice'));

  // 2. First run offers onboarding; skip it, then add a product with a photo.
  await until(`!![...document.querySelectorAll('button')].find(b=>b.textContent.includes('Skip for now'))`, 'onboarding');
  assert.ok(!(await evalJs(`document.body.textContent`)).match(/Puter/), 'no Puter wording on the onboarding screen');
  await clickText('button', 'Skip for now');
  await until(`!document.querySelector('.modal, [role=dialog]')`, 'onboarding to close');
  await evalJs(`document.querySelector('.sidebar-add').click()`);
  await until(`!!document.querySelector('input[placeholder="e.g. Wireless Mouse M310"]')`, 'the product form');
  assert.ok(!(await evalJs(`document.body.textContent`)).match(/Puter/), 'no Puter wording in the product form');
  await setValue('input[placeholder="e.g. Wireless Mouse M310"]', 'Cordless Drill');
  await setValue('input[placeholder="e.g. WM-M310"]', 'DRILL-18V');
  await setValue('input[type=number]', '4');
  // A real image file through the form's own file input and its canvas re-encode.
  await evalJs(`(async()=>{const c=document.createElement('canvas');c.width=1600;c.height=1200;const x=c.getContext('2d');x.fillStyle='#c33';x.fillRect(0,0,1600,1200);
    const blob=await new Promise(r=>c.toBlob(r,'image/png'));const dt=new DataTransfer();dt.items.add(new File([blob],'drill.png',{type:'image/png'}));
    const i=document.querySelector('input[type=file][accept="image/*"]');i.files=dt.files;i.dispatchEvent(new Event('change',{bubbles:true}));return true})()`);
  await until(`!!document.querySelector('.photo-preview img')`, 'the photo preview');
  await evalJs(`document.querySelector('form button[type=submit]').click()`);
  await until(`!document.querySelector('input[placeholder="e.g. Wireless Mouse M310"]')`, 'the form to close');
  await until(`document.body.textContent.includes('Cordless Drill')`, 'the product in the list');
  // The data is on the bridge for alice: a record in KV and the photo as a file.
  await until(`puter.kv.list('inv:*', true).then(l=>JSON.stringify(l).includes('Cordless Drill'))`, 'the product on the bridge');
  const photos = await evalJs(`puter.fs.readdir('inventory/images').then(l=>l.map(e=>[e.name,e.size>0&&e.size<400000]))`);
  assert.equal(photos.length, 1, 'one photo on the bridge');
  assert.match(photos[0][0], /\.(webp|jpg|jpeg)$/);
  assert.ok(photos[0][1], 'photo is re-encoded and small');
  assert.equal(await evalJs(`puter.fs.read('inventory/images/' + ${JSON.stringify(photos[0][0])}).then(b=>b.size>0)`), true, 'photo reads back');

  // 3. A reload (a new page load) keeps the product and shows its photo.
  await go(`${origin}${BASE}/`);
  await until(`!!document.querySelector('.sidebar')`, 'the app to reboot');
  await until(`document.body.textContent.includes('Cordless Drill')`, 'the product after a reload');
  await until(`!!document.querySelector('img[src^="blob:"]:not([src$="icon-192.png"])') || [...document.querySelectorAll('img')].some(i=>i.src.startsWith('blob:'))`, 'the photo thumbnail');
  assert.deepEqual(await csp(), [], 'no CSP violation after reload');

  // 4. Offline: with the bridge gone a write rejects and navigation gets the token-free page.
  await stopBridge();
  assert.equal(await evalJs(`puter.kv.set('inv:x',{}).then(()=>'ok',e=>e.code)`), 'network_error');
  await go(`${origin}${BASE}/`);
  const offline = await evalJs(`document.documentElement.outerHTML`);
  assert.match(offline, /You are offline/);
  assert.ok(!offline.includes('homestead-csrf') && !offline.includes('Cordless'), 'offline page has no token or data');

  // 5. Back online: bob (same browser) sees none of alice's data, photos or offline mirror.
  await startBridge();
  await go(`${origin}${BASE}/_homestead/login?subject=bob`);
  await until(`!!document.querySelector('.sidebar')`, 'the app to boot for bob');
  await sleep(800);
  assert.ok(!(await evalJs(`document.body.textContent`)).includes('Cordless'), 'bob sees alice product');
  assert.deepEqual(await evalJs(`puter.kv.list('inv:*', true).then(l=>l.filter(x=>JSON.stringify(x.value).includes('Cordless')))`), [], 'bob has no such record');
  assert.deepEqual(await evalJs(`puter.fs.readdir('inventory/images').then(l=>l.length,()=>0)`), 0, 'bob has no photos');
  assert.equal(await evalJs(`puter.fs.read('inventory/images/' + ${JSON.stringify(photos[0][0])}).then(()=>'read',e=>e.code||'err')`) !== 'read', true, 'bob cannot read alice photo');
  // The browser-side mirror of alice was wiped when bob adopted the browser.
  const mirror = await evalJs(`new Promise((res)=>{const r=indexedDB.open('inv-offline');r.onsuccess=()=>{const db=r.result;const names=[...db.objectStoreNames];let all='';let n=names.length;if(!n)return res('');names.forEach(s=>{const q=db.transaction(s).objectStore(s).getAll();q.onsuccess=()=>{all+=JSON.stringify(q.result);if(--n===0)res(all)}})};r.onerror=()=>res('err')})`);
  assert.ok(!mirror.includes('Cordless'), 'alice data is gone from the browser mirror');
  assert.deepEqual(await csp(), [], 'no CSP violation for bob');
  assert.deepEqual(exceptions, [], 'no uncaught exceptions');

  console.log('real-browser Inventory check passed (Chrome', (await send('Browser.getVersion')).product + ')');
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
