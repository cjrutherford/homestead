// S374 real-browser check of the Time tracking app (Hourly upstream) against the
// dev bridge: the built app boots under the CSP, loads sample clients and
// projects, logs time through its form, writes a backup, keeps all of it across
// a reload, shows the offline page and isolates a second person (data, files and
// the browser-side localStorage mirror).
// TEST ONLY (dev identity is not authentication). Zero dependencies: drives
// headless Chrome/Chromium over the DevTools protocol with Node's WebSocket.
// usage: node browser-check-time-tracking.mjs <bridge-bin> <dist-dir> <tmp-dir>
// Exit 0 pass; exit 0 with "SKIP:" if no browser; exit 1 on failure.
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import assert from 'node:assert/strict';

const [bin, dist, tmp] = process.argv.slice(2);
const BASE = '/apps/time-tracking';
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
// Upstream skips its own service worker on localhost and 127.0.0.1, so the page is
// served from a made-up host name that Chrome maps to loopback and treats as secure
// (the appliance serves HTTPS under its own name).
const HOST = 'hs-check.test';
const origin = `http://${HOST}:${port}`;
let bridge = null;
async function startBridge() {
  bridge = spawn(bin, ['-dev-identity', '-db', path.join(tmp, 'bridge.db'), '-app', 'time-tracking', '-listen', `127.0.0.1:${port}`,
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
      if (msg.method === 'Page.javascriptDialogOpening') console.error('DIALOG', JSON.stringify(msg.params).slice(0,300));
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
  const until = async (expr, what) => {  for (let i = 0; i < 100; i++) { if (await evalJs(expr)) return; await sleep(100); } throw new Error('timeout waiting for ' + what + ' | page: ' + (await evalJs(`location.href+' '+document.body.innerText.slice(0,300)+' csp='+JSON.stringify(window.__csp)`).catch(String))); };
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
  assert.ok(!/js\.puter\.com|plausible|developer\.puter/.test(html), 'no hosted SDK or analytics reference');
  assert.equal(await evalJs(`fetch(document.querySelector('link[rel=manifest]').href).then(r=>r.json()).then(m=>[m.start_url,m.icons[0].src].join())`), './,./icons/icon-192.png');
  assert.equal(await evalJs(`typeof puter.ui + ' ' + typeof puter.ai`), 'undefined undefined');
  // The sidebar never shows the opaque sign-in subject, and has no Sign out.
  const side = await evalJs(`document.querySelector('.sidebar').textContent`);
  assert.ok(!side.includes('alice') && !/Sign out/.test(side), 'sidebar: ' + side);
  assert.ok(side.includes('Homestead user'), 'sidebar names a Homestead user: ' + side);

  // 2. First run offers onboarding; load the sample clients and projects, then log time.
  await until(`!![...document.querySelectorAll('button')].find(b=>b.textContent.includes('Explore with sample data'))`, 'onboarding');
  assert.ok(!(await evalJs(`document.body.textContent`)).match(/Puter/), 'no Puter wording on the onboarding screen');
  await clickText('button', 'Explore with sample data');
  await until(`!document.querySelector('.modal-panel')`, 'onboarding to close');
  await until(`puter.kv.get('hourly:projects:v1').then(v=>(typeof v==='string'?v:JSON.stringify(v)||'').length>20)`, 'sample projects on the bridge');
  await evalJs(`location.hash='#/'`);
  await evalJs(`document.querySelector('.sidebar-add').click()`);
  await until(`!!document.querySelector('.modal-panel input[placeholder="e.g. Design review"]')`, 'the entry form');
  assert.ok(!(await evalJs(`document.body.textContent`)).match(/Puter/), 'no Puter wording in the entry form');
  await evalJs(`(()=>{const s=document.querySelector('.modal-panel select');const o=[...s.options].find(x=>x.value);s.value=o.value;s.dispatchEvent(new Event('change',{bubbles:true}));return true})()`);
  await setValue('.modal-panel input[placeholder="e.g. Design review"]', 'Drill test');
  await setValue('.modal-panel input[placeholder="e.g. 1:30, 90m, or 1.5h"]', '1:30');
  await evalJs(`document.querySelector('.modal-panel form button[type=submit]').click()`);
  await until(`!document.querySelector('.modal-panel input[placeholder="e.g. Design review"]')`, 'the form to close');
  const month = await evalJs(`(()=>{const d=new Date();return d.getFullYear()+'-'+String(d.getMonth()+1).padStart(2,'0')})()`);
  // The entry is a month file on the bridge for alice; settings and projects are KV records.
  await until(`puter.fs.read('hourly-data/entries-${month}.json').then(b=>b.text()).then(t=>t.includes('Drill test'),()=>false)`, 'the entry file on the bridge');
  // A backup is written as a file (Settings > Back up now).
  await evalJs(`location.hash='#/settings'`);
  await until(`!![...document.querySelectorAll('.settings-row')].find(b=>b.textContent.includes('Back up now'))`, 'the settings page');
  const settings = await evalJs(`document.body.textContent`);
  assert.ok(!/Puter|Documentation|Sign out/.test(settings), 'settings: no Puter wording, docs link or Sign out');
  assert.ok(settings.includes('Homestead user') && !settings.includes('alice'), 'settings names a Homestead user, not the subject');
  assert.deepEqual(await evalJs(`[...document.querySelectorAll('a[href^="http"]')].map(a=>a.href)`), [], 'no outbound links');
  await clickText('.settings-row', 'Back up now');
  await until(`puter.fs.readdir('hourly-data/backups').then(l=>l.length===1,()=>false)`, 'the backup file');

  // 3. A reload (a new page load) keeps the entry.
  await go(`${origin}${BASE}/`);
  await until(`!!document.querySelector('.sidebar')`, 'the app to reboot');
  await until(`document.body.textContent.includes('Drill test')`, 'the entry after a reload');
  assert.deepEqual(await csp(), [], 'no CSP violation after reload');

  // 4. Offline: with the bridge gone a write rejects and navigation gets the token-free page.
  await stopBridge();
  assert.equal(await evalJs(`puter.kv.set('hourly:x',{}).then(()=>'ok',e=>e.code)`), 'network_error');
  await go(`${origin}${BASE}/`);
  const offline = await evalJs(`document.documentElement.outerHTML`);
  assert.match(offline, /You are offline/);
  assert.ok(!offline.includes('homestead-csrf') && !offline.includes('Drill'), 'offline page has no token or data');

  // 5. Back online: bob (same browser) sees none of alice's data, files or local mirror.
  await startBridge();
  await go(`${origin}${BASE}/_homestead/login?subject=bob`);
  await until(`!!document.querySelector('.sidebar')`, 'the app to boot for bob');
  await sleep(800);
  assert.ok(!(await evalJs(`document.body.textContent`)).includes('Drill'), 'bob sees alice entry');
  assert.equal(await evalJs(`puter.kv.get('hourly:projects:v1').then(v=>v==null||JSON.stringify(v)==='[]'||v==='[]')`), true, 'bob has no projects');
  assert.equal(await evalJs(`puter.fs.read('hourly-data/entries-${month}.json').then(()=>'read',e=>e.code||'err')`) !== 'read', true, 'bob cannot read alice entries');
  // The app may write bob's own snapshot of his empty data on open; none of it is alice's.
  assert.equal(await evalJs(`puter.fs.readdir('hourly-data/backups').then(async(l)=>{let all='';for(const e of l)all+=await (await puter.fs.read('hourly-data/backups/'+e.name)).text();return all.includes('Drill')||all.includes('Website redesign')},()=>false)`), false, 'bob backups hold nothing of alice');
  // The browser-side mirror of alice was wiped when bob adopted the browser.
  const mirror = await evalJs(`JSON.stringify(Object.entries(localStorage))`);
  assert.ok(!mirror.includes('Drill') && !mirror.includes('alice'), 'alice data is gone from the local mirror');
  assert.deepEqual(await csp(), [], 'no CSP violation for bob');
  assert.deepEqual(exceptions, [], 'no uncaught exceptions');

  console.log('real-browser Time tracking check passed (Chrome', (await send('Browser.getVersion')).product + ')');
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
