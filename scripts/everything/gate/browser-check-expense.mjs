// S380 real-browser check of Expenses against the dev bridge: boots under the CSP,
// attaches a receipt and saves an expense (the receipt goes to per-person bridge storage),
// keeps it across a reload, proves that no AI request leaves the page and that no AI or
// receipt-reading wording is shown, separates a second person in the same browser profile,
// serves the offline page, and proves Puter links, the hosted SDK and every foreign request
// are absent.
// TEST ONLY (dev identity is not authentication). Zero dependencies.
// usage: node browser-check-expense.mjs <bridge-bin> <dist-dir> <tmp-dir>
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import assert from 'node:assert/strict';

const [bin, dist, tmp] = process.argv.slice(2);
const BASE = '/apps/expense';
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
  bridge = spawn(bin, ['-dev-identity', '-db', path.join(tmp, 'bridge.db'), '-app', 'expense', '-listen', `127.0.0.1:${port}`,
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
  const BAD_WORDING = /\bAI\b|OCR|vision model|Puter|read(ing)? (the )?receipts?/;
  const RECEIPT_PNG = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==';
  // Choose files in the capture dialog the way a person would (the hidden input, then its change event).
  const choose = (name, type, bytes) => evalJs(`(()=>{const i=[...document.querySelectorAll('input[type=file]')].find(x=>x.multiple);
    const b=${bytes === null ? `Uint8Array.from(atob('${RECEIPT_PNG}'),c=>c.charCodeAt(0))` : `new Uint8Array(${bytes})`};
    const dt=new DataTransfer();dt.items.add(new File([b],${JSON.stringify(name)},{type:${JSON.stringify(type)}}));i.files=dt.files;i.dispatchEvent(new Event('change',{bubbles:true}));return true})()`);
  const addExpense = async (merchant, amount, file) => {
    await evalJs(`document.querySelector('.sidebar-add').click(),true`);
    await until(`!!document.querySelector('.dropzone')`, 'the capture dialog');
    if (file) await choose(...file);
    else await clickText('button', 'Enter an expense without a receipt');
    await until(`!!document.querySelector('form.expense-form')`, 'the expense form');
    await setValue('form.expense-form input[placeholder="Where was this?"]', merchant);
    await setValue('form.expense-form input.amount', amount);
    await evalJs(`document.querySelector('form.expense-form button[type=submit]').click(),true`);
    await until(`!document.querySelector('form.expense-form')`, 'the expense to be saved');
  };
  const receipts = () => evalJs(`puter.fs.readdir('expenses-data/receipts').then(l=>l.map(e=>e.name+':'+e.size),()=>[])`);

  // 1. alice boots under the CSP; same-origin only; no hosted SDK, AI or Puter wording or link.
  await cmd('Page.addScriptToEvaluateOnNewDocument', { source: `(()=>{const f=window.fetch;window.__net=[];window.fetch=function(u,o){window.__net.push('fetch '+(u&&u.url||u));return f.apply(this,arguments)}})()` });
  await go(`${origin}${BASE}/_homestead/login?subject=alice`);
  await until(`!!document.querySelector('.modal, [role=dialog]') && document.body.innerText.includes('Welcome')`, 'the welcome dialog');
  assert.deepEqual(await csp(), [], 'no CSP violation while booting');
  assert.deepEqual(await evalJs(`performance.getEntriesByType('resource').map(e=>new URL(e.name).origin).filter(o=>o!==location.origin)`), [], 'only same-origin requests');
  const html = await evalJs(`document.documentElement.outerHTML`);
  assert.ok(!/js\.puter\.com|plausible|jsdelivr|googleapis|puter\.com|developer\.puter|Powered by/.test(html), 'no hosted SDK, analytics, CDN or Puter link');
  assert.equal(await evalJs(`typeof puter.ai + ' ' + typeof puter.net`), 'undefined undefined');
  assert.equal(await evalJs(`document.querySelectorAll('a[href^="http"]').length`), 0, 'no outbound links');
  assert.ok(!BAD_WORDING.test(await text()), 'no AI, OCR, receipt-reading or Puter wording on the welcome page: ' + (await text()).slice(0, 300));
  const manifest = await evalJs(`fetch('./manifest.webmanifest').then(r=>r.text())`);
  assert.ok(!/AI|OCR|puter/i.test(manifest), 'the manifest makes no AI claim');
  assert.ok(!/AI OCR|puter/i.test(await evalJs(`document.querySelector('meta[name=description]').content`)), 'the page description makes no AI claim');
  await clickText('button', 'Skip for now');
  await until(`!document.body.innerText.includes('Welcome')`, 'the welcome dialog to close');

  // 2. Create flow with a receipt. The saved setting still says "read receipts automatically"
  //    (upstream's default); the capture goes straight to the form and nothing leaves the page.
  const before = requests.length;
  await addExpense('Corner Cafe', '12.50', ['cafe.png', 'image/png', null]);
  assert.equal((await receipts()).length, 1, 'the receipt went to the appliance');
  assert.ok((await receipts())[0].endsWith(':' + atob(RECEIPT_PNG).length), 'the stored receipt has the bytes that were chosen');
  const during = requests.slice(before).filter((r) => !/^(data|blob):/.test(r.url)).map((r) => new URL(r.url));
  assert.ok(during.every((u) => u.origin === origin && u.pathname.startsWith(BASE + '/')), 'every request while capturing stays on the app: ' + during.map(String).join(' '));
  assert.ok(!during.some((u) => /\/(ai|chat|img2txt|drivers|llm)\b/i.test(u.pathname)), 'no AI request');
  assert.ok(!BAD_WORDING.test(await text()), 'no AI wording after a capture');

  // 2b. A file over 8 MB is refused up front; one just under the limit is stored.
  await evalJs(`document.querySelector('.sidebar-add').click(),true`);
  await until(`!!document.querySelector('.dropzone')`, 'the capture dialog');
  assert.ok((await text()).includes('up to 8 MB'), 'the dialog names the 8 MB limit');
  await choose('huge.png', 'image/png', 8 * 1024 * 1024 + 1);
  await until(`document.body.innerText.includes('over 8 MB and skipped')`, 'the too-large notice');
  assert.equal((await receipts()).length, 1, 'the too-large file was not stored');
  await evalJs(`document.querySelector('.modal-backdrop, [role=dialog] [aria-label=Close], .modal .icon-btn')?.click(),true`);
  await go(`${origin}${BASE}/`);
  await until(`!!document.querySelector('.sidebar-add')`, 'the app after reload');
  await addExpense('Big Scan', '99.00', ['big.png', 'image/png', 8 * 1024 * 1024 - 1024]);
  assert.equal((await receipts()).length, 2, 'a receipt just under 8 MB is stored: ' + JSON.stringify(await receipts()));

  // 3. Persistence across reload; the expense list shows it; the receipt reads back.
  await route('#/expenses');
  await until(`document.body.innerText.includes('Corner Cafe') && document.body.innerText.includes('Big Scan')`, 'both expenses after a reload');
  assert.deepEqual(await csp(), [], 'no CSP violation after reload');
  assert.equal(await evalJs(`puter.fs.read('expenses-data/receipts/'+${JSON.stringify((await receipts())[0].split(':')[0])}).then(b=>b.size>0)`), true, 'a receipt reads back');
  assert.equal(await evalJs(`navigator.serviceWorker.ready.then(r=>r.scope)`), `${origin}${BASE}/`, 'the service worker serves the app path');

  // 4. Settings: no AI section, "Homestead user", no sign-out, no link; a theme choice lands in person-tagged storage.
  await route('#/settings');
  await until(`/appearance/i.test(document.body.innerText)`, 'settings');
  const settings = await text();
  assert.ok(!BAD_WORDING.test(settings), 'no AI or Puter wording in settings: ' + settings.slice(0, 400));
  assert.ok(settings.includes('Homestead user'), 'settings show "Homestead user": ' + settings.slice(-600));
  assert.ok(!settings.includes('alice'), 'settings never show the subject');
  assert.ok(!/Sign out/.test(await evalJs(`document.body.innerHTML`)), 'no sign-out (the session belongs to the bridge)');
  assert.equal(await evalJs(`document.querySelectorAll('a[href^="http"]').length`), 0, 'no outbound links in settings');
  await clickText('button[role=radio]', 'Dark');
  await sleep(400);
  const keys = await evalJs(`JSON.stringify([...Array(localStorage.length).keys()].map(i=>localStorage.key(i)))`);
  assert.ok(keys.includes('expenses:theme'), 'the theme is in the person storage: ' + keys);
  const realKeys = async () => JSON.stringify((await cmd('DOMStorage.getDOMStorageItems', { storageId: { securityOrigin: origin, isLocalStorage: true } })).entries.map((e) => e[0]));
  const raw = await realKeys();
  assert.ok(/hs:[0-9a-f]{16}:expenses:theme/.test(raw) && !raw.includes('"expenses:theme"'), 'in the real browser storage the key carries the person tag: ' + raw);
  assert.ok(!raw.includes('alice'), 'the subject is not in the browser storage key');
  await evalJs(`localStorage.setItem('alice-mark','1'),true`);

  // 5. bob, same browser profile: nothing of alice's, in the browser or on the appliance.
  await go(`${origin}${BASE}/_homestead/login?subject=bob`);
  await until(`document.body.innerText.includes('Welcome')`, 'bob first-run welcome');
  assert.equal(await evalJs(`localStorage.getItem('alice-mark')`), null, 'bob does not see alice browser storage');
  assert.equal(await evalJs(`localStorage.getItem('expenses:theme')`), null, 'bob does not inherit alice theme');
  assert.equal(await evalJs(`document.documentElement.dataset.theme||''`), '', 'bob is not shown alice theme');
  await clickText('button', 'Skip for now');
  await route('#/expenses');
  await until(`document.body.innerText.includes('No expenses') || document.body.innerText.includes('Nothing') || !document.body.innerText.includes('Corner Cafe')`, 'bob list');
  const bobText = await text();
  assert.ok(!bobText.includes('Corner Cafe') && !bobText.includes('Big Scan') && !bobText.includes('alice'), 'bob sees none of alice expenses');
  assert.deepEqual(await receipts(), [], 'bob has no receipts');
  await go(`${origin}${BASE}/_homestead/login?subject=alice`);
  await until(`!!document.querySelector('.sidebar-add')`, 'alice back');
  await route('#/expenses');
  await until(`document.body.innerText.includes('Corner Cafe')`, 'alice expenses back');
  assert.equal(await evalJs(`localStorage.getItem('alice-mark')`), '1', 'alice still has her own browser storage');
  assert.equal((await receipts()).length, 2, 'alice still has both receipts');

  // 6. Offline: the service worker answers a navigation with the fixed offline page and caches no page or data.
  await stopBridge();
  await go(`${origin}${BASE}/`);
  assert.ok((await text()).includes('You are offline'), 'the offline page is shown');
  const cached = await evalJs(`caches.keys().then(async ks=>{const out=[];for(const k of ks){const c=await caches.open(k);out.push(...(await c.keys()).map(r=>new URL(r.url).pathname))}return out})`);
  assert.ok(cached.every((p) => /\/assets\//.test(p)), 'only build assets are cached: ' + JSON.stringify(cached));
  assert.deepEqual(exceptions.filter((e) => !/Failed to fetch|ERR_INTERNET_DISCONNECTED|Failed to load resource/.test(e)), [], 'no uncaught exceptions');

  const foreign = requests.filter((r) => !r.url.startsWith(origin + BASE + '/') && r.url !== origin + '/favicon.ico' && !r.url.startsWith('data:') && !r.url.startsWith('blob:'));
  assert.deepEqual(foreign, [], 'every request, for the whole run, stays on the app origin and path');
  assert.ok(!requests.some((r) => /\/(ai|chat|img2txt|llm)\b/i.test(new URL(r.url).pathname)), 'no AI request in the whole run');
  console.log('real-browser Expenses check passed (Chrome', (await send('Browser.getVersion')).product + ')');
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
