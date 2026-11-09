// S372 real-browser check of the Contacts app against the dev bridge: the built
// app boots under the CSP, creates a contact through its UI, imports and exports
// vCards, searches, keeps the book across a reload, shows the offline page,
// hides Smart add and isolates a second person.
// TEST ONLY (dev identity is not authentication). Zero dependencies: drives
// headless Chrome/Chromium over the DevTools protocol with Node's WebSocket.
// usage: node browser-check-contacts.mjs <bridge-bin> <dist-dir> <tmp-dir>
// Exit 0 pass; exit 0 with "SKIP:" if no browser; exit 1 on failure.
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import assert from 'node:assert/strict';

const [bin, dist, tmp] = process.argv.slice(2);
const BASE = '/apps/contacts';
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
  bridge = spawn(bin, ['-dev-identity', '-db', path.join(tmp, 'bridge.db'), '-app', 'contacts', '-listen', `127.0.0.1:${port}`,
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
  await until(`!!document.querySelector('[data-search]')`, 'the app to boot');
  assert.deepEqual(await csp(), [], 'no CSP violation while booting');
  await evalJs(`navigator.serviceWorker.ready.then((r)=>r.scope)`).then((scope) => assert.equal(scope, `${origin}${BASE}/`, 'worker scope'));
  // Nothing is requested from any other origin, and the page references no hosted SDK.
  assert.deepEqual(await evalJs(`performance.getEntriesByType('resource').map(e=>new URL(e.name).origin).filter(o=>o!==location.origin)`), [], 'only same-origin requests');
  assert.ok(!(await evalJs(`document.documentElement.outerHTML`)).includes('js.puter.com'), 'no hosted SDK reference');
  // The manifest and the icons load under the path (not at the server root).
  assert.equal(await evalJs(`fetch(document.querySelector('link[rel=manifest]').href).then(r=>r.json()).then(m=>[m.start_url,m.icons[0].src,'screenshots' in m].join())`), './,./icon-48.png,false');
  assert.equal(await evalJs(`fetch(document.querySelector('img[src$="icon.svg"]').src).then(r=>r.status)`), 200, 'top-bar logo loads');
  // ui.* is absent from the shim: harmless absence, with the app's own fallbacks.
  assert.equal(await evalJs(`typeof puter.ui + ' ' + typeof puter.ai + ' ' + typeof puter.exit`), 'undefined undefined undefined');
  // Smart add needs Puter AI: its button is gone, and nothing mentions Puter.
  assert.equal(await evalJs(`!!document.querySelector('[data-action="smart-add"]')`), false, 'Smart add is hidden');
  assert.ok(!(await evalJs(`document.body.textContent`)).match(/Puter|Smart add/), 'no Puter wording on screen');
  // Account menu falls back to the app's own DOM menu: theme, no sign-out, no opaque subject.
  await evalJs(`document.querySelector('[data-action="account"]').click()`);
  await until(`!!document.querySelector('.ctx-menu')`, 'the account menu');
  const menu = await evalJs(`document.querySelector('.ctx-menu').textContent`);
  assert.match(menu, /Theme/);
  assert.ok(!/Sign out|@alice/.test(menu), 'menu shows no sign-out or raw subject: ' + menu);
  await evalJs(`document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}))`);
  await evalJs(`document.querySelector('.ctx-menu')&&document.querySelector('.ctx-menu').remove()`);

  // 2. Create a contact through the real editor.
  assert.ok(await evalJs(`(()=>{const b=document.querySelector('[data-action="new"]');b.click();return !!b})()`), 'New button');
  await until(`!!document.querySelector('input[name="firstName"]')`, 'the editor');
  await setValue('input[name="firstName"]', 'Ada');
  await setValue('input[name="lastName"]', 'Lovelace');
  await setValue('input[name="company"]', 'Analytical Engines');
  await evalJs(`document.querySelector('[data-action="add-row"][data-group="emails"]')?.click()`);
  await evalJs(`(()=>{const i=document.querySelector('input[type=email].repeat-row__value');if(i){i.value='ada@example.org';i.dispatchEvent(new Event('input',{bubbles:true}));}return true})()`);
  await evalJs(`document.querySelector('[data-action="save"]').click()`);
  await until(`document.querySelector('.contact-item__name-text')?.textContent.includes('Ada')`, 'the new contact in the list');
  const stored = await evalJs(`puter.kv.list('contact:*', true)`);
  assert.equal(stored.length, 1, 'one contact on the bridge');
  assert.equal(stored[0].value.firstName, 'Ada');
  assert.equal(stored[0].value.company, 'Analytical Engines');
  assert.ok(JSON.stringify(stored[0].value).includes('ada@example.org'), 'email stored');

  // 3. Import a vCard file by dropping it on the window (the app's own browser path).
  const vcf = 'BEGIN:VCARD\r\nVERSION:3.0\r\nN:Hopper;Grace;;;\r\nFN:Grace Hopper\r\nORG:Navy\r\nEMAIL:grace@example.org\r\nEND:VCARD\r\nBEGIN:VCARD\r\nVERSION:3.0\r\nN:Turing;Alan;;;\r\nFN:Alan Turing\r\nTEL:+44 20 7946 0000\r\nEND:VCARD\r\n';
  await evalJs(`(()=>{const dt=new DataTransfer();dt.items.add(new File([${JSON.stringify(vcf)}],'people.vcf',{type:'text/vcard'}));
    for (const t of ['dragenter','dragover','drop']) document.dispatchEvent(new DragEvent(t,{dataTransfer:dt,bubbles:true,cancelable:true}));return true})()`);
  await until(`puter.kv.list('contact:*').then(k=>k.length===3)`, 'the import to reach the bridge');
  await until(`document.body.textContent.includes('Grace') && document.body.textContent.includes('Turing')`, 'imported contacts in the list');

  // 4. Search narrows the list.
  await setValue('[data-search]', 'Lovelace');
  await until(`document.querySelectorAll('.contact-item').length===1`, 'search to narrow the list');
  assert.ok((await evalJs(`document.querySelector('.contact-item').textContent`)).includes('Ada'));
  await setValue('[data-search]', '');
  await until(`document.querySelectorAll('.contact-item').length===3`, 'search to clear');

  // 5. Export all: the app builds a vCard download link (a browser download, no SDK).
  await evalJs(`window.__blobs=[];const co=URL.createObjectURL.bind(URL);URL.createObjectURL=(b)=>{window.__blobs.push(b);return co(b)};window.__dl=[];HTMLAnchorElement.prototype.click=function(){window.__dl.push({name:this.download,href:this.href});}`);
  await evalJs(`document.querySelector('[data-action="list-options"]').click()`);
  await until(`[...document.querySelectorAll('.ctx-menu__item')].some(x=>x.textContent.includes('Export all'))`, 'the list options menu');
  await evalJs(`[...document.querySelectorAll('.ctx-menu__item')].find(x=>x.textContent.includes('Export all')).click()`);
  await until(`window.__dl.length===1`, 'the export download');
  assert.match(await evalJs(`window.__dl[0].name`), /\.vcf$/);
  const exported = await evalJs(`window.__blobs[window.__blobs.length-1].text()`); // the CSP (rightly) refuses fetch() of blob: URLs
  assert.ok(exported.includes('Lovelace') && exported.includes('Hopper') && exported.includes('Turing'), 'export holds the contacts');

  // 6. A reload (a new page load) keeps the address book.
  await go(`${origin}${BASE}/`);
  await until(`document.querySelectorAll('.contact-item').length===3`, 'the book to reappear after a reload');
  assert.ok((await evalJs(`document.body.textContent`)).includes('Lovelace'));
  assert.deepEqual(await csp(), [], 'no CSP violation after reload');

  // 7. A longer book than the 1000-key list limit still loads in full.
  await evalJs(`(async()=>{for(let f=0;f<1100;f+=100){const b=[];for(let i=f;i<f+100;i++)b.push({key:'contact:bulk-'+String(i).padStart(5,'0'),value:{id:'bulk-'+i,firstName:'Bulk'+i,lastName:'Person',emails:[],phones:[]}});await puter.kv.set(b)}return true})()`);
  await go(`${origin}${BASE}/`);
  await until(`!!document.querySelector('[data-search]')`, 'the long book to load');
  await setValue('[data-search]', 'Bulk1099');
  await until(`document.querySelectorAll('.contact-item').length===1`, 'the last of 1103 contacts to be found');
  assert.equal(await evalJs(`puter.kv.list('contact:*',true).then(l=>l.length)`), 1103);
  await setValue('[data-search]', '');

  // 8. Offline: with the bridge gone a write rejects network_error and
  // navigation gets the token-free page.
  await stopBridge();
  assert.equal(await evalJs(`puter.kv.set('contact:x',{}).then(()=>'ok',e=>e.code)`), 'network_error');
  await go(`${origin}${BASE}/`);
  const offline = await evalJs(`document.documentElement.outerHTML`);
  assert.match(offline, /You are offline/);
  assert.ok(!offline.includes('homestead-csrf'), 'offline page has no token');

  // 9. Back online: bob (same browser) sees none of alice's contacts.
  await startBridge();
  await go(`${origin}${BASE}/_homestead/login?subject=bob`);
  await until(`!!document.querySelector('[data-search]')`, 'the app to boot for bob');
  await sleep(500);
  assert.ok(!(await evalJs(`document.body.textContent`)).includes('Lovelace'), 'bob sees alice contact');
  assert.deepEqual(await evalJs(`puter.kv.list('contact:*', true)`), [], 'bob has no contacts');
  assert.deepEqual(await csp(), [], 'no CSP violation for bob');
  assert.deepEqual(exceptions, [], 'no uncaught exceptions');

  console.log('real-browser Contacts check passed (Chrome', (await send('Browser.getVersion')).product + ')');
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
