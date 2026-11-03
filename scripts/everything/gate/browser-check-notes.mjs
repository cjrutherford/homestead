// S371 real-browser check of the Notes app against the dev bridge: the built
// app boots under the CSP, creates a base and a record through its UI, keeps
// them across a reload, isolates a second person, and reports the bridge
// going away as network_error.
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
const BASE = '/apps/notes';
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
  bridge = spawn(bin, ['-dev-identity', '-db', path.join(tmp, 'bridge.db'), '-app', 'notes', '-listen', `127.0.0.1:${port}`,
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
  let id = 0; const pending = new Map(); const waiters = []; const exceptions = []; const requests = [];
  ws.onmessage = (m) => {
    const msg = JSON.parse(m.data);
    if (msg.id && pending.has(msg.id)) { const { res, rej } = pending.get(msg.id); pending.delete(msg.id); msg.error ? rej(new Error(JSON.stringify(msg.error))) : res(msg.result); }
    else {
      if (msg.method === 'Network.requestWillBeSent') requests.push(msg.params.request.url);
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
  const until = async (expr, what) => { for (let i = 0; i < 100; i++) { if (await evalJs(expr)) return; await sleep(100); } throw new Error('timeout waiting for ' + what + ' :: ' + (await evalJs('document.body.innerText.slice(0,300)')) + ' :: ' + JSON.stringify(exceptions) + ' :: csp ' + JSON.stringify(await csp())); };
  const clickText = (sel, text) => evalJs(`(()=>{const b=[...document.querySelectorAll(${JSON.stringify(sel)})].find(x=>x.textContent.includes(${JSON.stringify(text)}));if(!b)return false;b.click();return true})()`);
  const setValue = (sel, v) => evalJs(`(()=>{const i=document.querySelector(${JSON.stringify(sel)});i.value=${JSON.stringify(v)};i.dispatchEvent(new Event('input',{bubbles:true}));return true})()`);
  const csp = () => evalJs(`window.__csp||[]`);

  // 1. Sign in as alice; the app boots from the built bundle under the CSP.
  await go(`${origin}${BASE}/_homestead/login?subject=alice`);
  await until(`!!window.notesApp && document.body.textContent.includes("alice's Notebook")`, 'the app to boot');
  assert.deepEqual(await csp(), [], 'no CSP violation while booting');
  assert.equal(await evalJs(`(async()=>'serviceWorker' in navigator ? (await navigator.serviceWorker.getRegistrations()).length : 0)()`), 0, 'Notes registers no service worker');
  assert.ok(!(await evalJs(`document.documentElement.outerHTML`)).includes('js.puter.com'), 'no hosted SDK reference');
  assert.equal(await evalJs(`puter.env`), 'app');

  // 2. Create a page through the app's own commands, title and text it, and wait for the bridge to hold it.
  await evalJs(`window.notesApp.runCommand('newPage')`);
  await until(`!!window.notesApp.editor.page`, 'the new page');
  await evalJs(`(()=>{const e=window.notesApp.editor;e.titleEl.textContent='Garden plan';e.titleEl.dispatchEvent(new Event('input',{bubbles:true}));e.commit();
    const el=e.createOutlineAt(48,140,'');el.querySelector('.outline-body').innerHTML='<p>Plant the tomatoes in May</p>';e.markActiveDirty();return true})()`);
  const pageId = await evalJs(`window.notesApp.state.pageId`);
  await until(`puter.fs.read('notes/pages/${pageId}.json').then(b=>b.text()).then(t=>t.includes('tomatoes')&&t.includes('Garden plan'),()=>false)`, 'the page on the bridge');
  const listing = await evalJs(`puter.fs.readdir('notes/pages').then(l=>JSON.stringify(l.map(f=>f.name)))`);
  console.log('pages on the bridge:', listing);
  const st = await evalJs(`puter.fs.stat('notes/pages/${pageId}.json')`);
  assert.ok(st.size > 0 && st.modified > 0, 'stat reports size and stamp');
  // An image attachment is stored as an asset file.
  const asset = await evalJs(`(async()=>{const png=Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=='),c=>c.charCodeAt(0));
    const id=await window.notesApp.store.saveAsset(new Blob([png],{type:'image/png'}),'dot.png');return id})()`);
  await until(`puter.fs.readdir('notes/assets').then(l=>l.length>0,()=>false)`, 'an asset on the bridge');
  console.log('asset saved:', asset);

  // 3. A reload (a new page load, new bundle execution) keeps the data.
  await go(`${origin}${BASE}/`);
  await until(`!!window.notesApp && !!window.notesApp.store.findPage(${JSON.stringify(pageId)})`, 'the page to reappear after a reload');
  await until(`!!window.notesApp.editor.page && window.notesApp.editor.page.id===${JSON.stringify(pageId)}`, 'the page to open');
  await until(`document.body.textContent.includes('Plant the tomatoes in May')`, 'the text after reload');
  assert.deepEqual(await csp(), [], 'no CSP violation after reload');

  // 3b. AI assist: a clear message, and no request of any kind.
  requests.length = 0;
  await evalJs(`window.notesApp.runCommand('assist')`);
  await until(`document.body.textContent.includes('AI assist is not available on Homestead')`, 'the assist message');
  const body = await evalJs(`document.body.textContent`);
  assert.ok(!/Summarize this page|Find action items|Ask a question/.test(body), 'no assist actions are offered');
  assert.deepEqual(await evalJs(`window.notesApp.ai.ask('hi').then(()=>'answered',e=>e.message)`), 'AI assist is not available on Homestead.');
  await sleep(500);
  assert.deepEqual(requests, [], 'assist made no network request');
  assert.ok(!(await evalJs(`puter.ai`)), 'no puter.ai');

  // 3c. Sharing (S438): the Create Link controls are present; the full read-link flow is run by browser-check-notes-sharing.
  await evalJs(`window.notesApp.runCommand('share')`);
  await sleep(500);
  const dialog = await evalJs(`document.body.textContent`);
  assert.ok(dialog.includes('Other ways to share'), 'share dialog opened');
  assert.ok(/Create Link/.test(dialog) && /Read-only link/.test(dialog), 'read-only link controls shown');
  assert.ok(!/Puter/.test(dialog), 'no Puter wording in the share dialog');
  await evalJs(`document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}))`);

  // 3d. Restoring a notebook backup decodes its embedded files without a request
  // (fetch(data:) is refused by connect-src 'self'; a pinned patch decodes locally).
  const assetsBefore = await evalJs(`puter.fs.readdir('notes/assets').then(l=>l.length,()=>0)`);
  requests.length = 0;
  await evalJs(`(async()=>{
    const png='data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==';
    const backup={format:'notes-backup',v:1,notebook:{name:'Restored book',sections:[{type:'section',id:'s1',name:'Sec',pages:[{id:'p1',title:'Restored page'}]}]},
      pages:{p1:{id:'p1',title:'Restored page',outlines:[],ink:[]}},assets:{'pic.png':png}};
    window.notesApp.importFiles();
    const input=[...document.querySelectorAll('input[type=file]')].pop();
    const dt=new DataTransfer();dt.items.add(new File([JSON.stringify(backup)],'book.json',{type:'application/json'}));
    input.files=dt.files;input.dispatchEvent(new Event('change'));return true})()`);
  await until(`window.notesApp.store.notebooks.some(n=>n.name==='Restored book')`, 'the restored notebook');
  await until(`puter.fs.readdir('notes/assets').then(l=>l.length===${assetsBefore + 1},()=>false)`, 'the restored picture on the bridge');
  assert.deepEqual(requests.filter((u) => !u.startsWith(origin + BASE + '/_homestead/')), [], 'restore made no unexpected request');

  // 4. Offline: with the bridge gone a write rejects network_error. Notes
  // has no service worker, so there is no offline page to check.
  await stopBridge();
  assert.equal(await evalJs(`puter.fs.write('notes/x.json','1').then(()=>'ok',e=>e.code)`), 'network_error');

  // 5. Back online: bob (same browser) sees none of alice's notes.
  await startBridge();
  await go(`${origin}${BASE}/_homestead/login?subject=bob`);
  await until(`!!window.notesApp && document.body.textContent.includes("bob's Notebook")`, 'the app to boot for bob');
  await sleep(500);
  assert.ok(!(await evalJs(`document.body.textContent`)).includes('Garden plan'), 'bob sees alice page');
  assert.equal(await evalJs(`puter.fs.read('notes/pages/${pageId}.json').then(()=>'found',e=>e.code)`), 'subject_does_not_exist');
  assert.ok(!(await evalJs(`puter.fs.readdir('notes/pages').then(l=>l.map(f=>f.name).join(','),()=>'')`)).includes(pageId), "bob's folder lists none of alice's pages");
  assert.deepEqual(await csp(), [], 'no CSP violation for bob');
  assert.deepEqual(exceptions, [], 'no uncaught exceptions');

  console.log('real-browser Notes check passed (Chrome', (await send('Browser.getVersion')).product + ')');
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
