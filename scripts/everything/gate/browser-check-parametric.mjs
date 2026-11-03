// S376 real-browser check of the Parametric app against the dev bridge: the
// built app boots under the CSP, the OpenCascade kernel (a 23 MB WebAssembly
// module in a worker) builds the example part, the part is saved, renamed and
// kept across a reload, a second person is isolated, and AI is proven off: no
// entry point, and no request to anything but the app's own address.
// TEST ONLY (dev identity is not authentication). Zero dependencies: drives
// headless Chrome/Chromium over the DevTools protocol with Node's WebSocket.
// usage: node browser-check-parametric.mjs <bridge-bin> <dist-dir> <tmp-dir>
// Exit 0 pass; exit 0 with "SKIP:" if no browser; exit 1 on failure.
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import assert from 'node:assert/strict';

const [bin, dist, tmp] = process.argv.slice(2);
const BASE = '/apps/parametric';
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
  bridge = spawn(bin, ['-dev-identity', '-sharing', '-db', path.join(tmp, 'bridge.db'), '-app', 'parametric', '-listen', `127.0.0.1:${port}`,
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
  const until = async (expr, what) => {  for (let i = 0; i < 100; i++) { if (await evalJs(expr)) return; await sleep(100); } throw new Error('timeout waiting for ' + what + ' | page: ' + (await evalJs(`location.href+' '+document.body.innerText.slice(0,300)+' csp='+JSON.stringify(window.__csp)`).catch(String))); };
  const clickText = (sel, text) => evalJs(`(()=>{const b=[...document.querySelectorAll(${JSON.stringify(sel)})].find(x=>x.textContent.includes(${JSON.stringify(text)}));if(!b)return false;b.click();return true})()`);
  const setValue = (sel, v) => evalJs(`(()=>{const i=document.querySelector(${JSON.stringify(sel)});i.value=${JSON.stringify(v)};i.dispatchEvent(new Event('input',{bubbles:true}));return true})()`);
  const csp = () => evalJs(`window.__csp||[]`);

  // 1. Sign in as alice; the app boots from the built bundle under the CSP.
  // Every call to fetch, XMLHttpRequest, sendBeacon, WebSocket and EventSource is also
  // recorded in the page, so a request that never reaches the network log still shows.
  await cmd('Page.addScriptToEvaluateOnNewDocument', { source: `(()=>{window.__net=[];const f=window.fetch;window.fetch=function(u,o){window.__net.push('fetch '+(u&&u.url||u));return f.apply(this,arguments)};const x=XMLHttpRequest.prototype.open;XMLHttpRequest.prototype.open=function(m,u){window.__net.push('xhr '+u);return x.apply(this,arguments)};const b=navigator.sendBeacon&&navigator.sendBeacon.bind(navigator);if(b)navigator.sendBeacon=function(u){window.__net.push('beacon '+u);return b.apply(null,arguments)};const W=window.WebSocket;window.WebSocket=function(u){window.__net.push('ws '+u);return new W(...arguments)};const E=window.EventSource;if(E)window.EventSource=function(u){window.__net.push('sse '+u);return new E(...arguments)};})();` });
  await go(`${origin}${BASE}/_homestead/login?subject=alice`);
  await until(`!!document.querySelector('.start-card')`, 'the app to boot');
  assert.deepEqual(await csp(), [], 'no CSP violation while booting');
  assert.deepEqual(await evalJs(`performance.getEntriesByType('resource').map(e=>new URL(e.name).origin).filter(o=>o!==location.origin)`), [], 'only same-origin requests');
  const html = await evalJs(`document.documentElement.outerHTML`);
  assert.ok(!/js\.puter\.com|plausible|puter\.com|developer\.puter/.test(html), 'no hosted SDK, analytics or puter.com reference');
  assert.equal(await evalJs(`typeof puter.ui + ' ' + typeof puter.ai + ' ' + typeof puter.net + ' ' + typeof puter.fs.share + ' ' + typeof puter.fs.rename`), 'undefined undefined object function function');
  assert.equal(await evalJs(`puter.net.fetch('https://example.com/').then(()=>'ok',e=>e.code||'err')`), 'net_disabled', 'net stays off');
  const text0 = await evalJs(`document.body.innerText`);
  assert.ok(!/Puter/.test(text0) && !text0.includes('alice'), 'start screen: no Puter wording or subject: ' + text0.slice(0, 300));
  assert.deepEqual(await evalJs(`[...document.querySelectorAll('a[href^="http"]')].map(a=>a.href)`), [], 'no outbound links on the start screen');
  assert.equal(await evalJs(`!!document.querySelector('[data-cmd=assistant],[data-pane=assistant],.assistant,a[aria-label*=GitHub]')`), false, 'no Assistant or GitHub control (the Share button is there since S440)');
  // The start screen offers no account chooser; start the work from the example.
  await clickText('.start-action', 'Mounting Bracket');
  // The kernel (OpenCascade, WebAssembly in a worker) builds the example.
  await until(`!document.querySelector('.loading-kernel') && document.querySelectorAll('.tree [role=treeitem]').length > 3`, 'the geometry engine and the design tree');
  await until(`!!document.querySelector('.tree')`, 'the tree');
  const feats = await evalJs(`[...document.querySelectorAll('.tree [role=treeitem]')].map(e=>e.textContent.trim()).join(' | ')`);
  assert.ok(/Extrude1/.test(feats) && /Hole1/.test(feats) && /Fillet1/.test(feats), 'design tree: ' + feats);
  await sleep(1500); // let the rebuild settle
  assert.equal(await evalJs(`document.querySelectorAll('.tree .badge:not(.warn)').length`), 0, 'no feature failed to build');
  // The worker owns the kernel fetch (workers are separate targets), so take the name from the build.
  const wasmFile = fs.readdirSync(path.join(dist, 'assets')).find((n) => /\.wasm$/.test(n));
  assert.ok(wasmFile, 'the build ships the kernel module');
  const wasmUrl = `${origin}${BASE}/assets/${wasmFile}`;
  const wasmHead = await evalJs(`fetch(${JSON.stringify(wasmUrl)}, { method: 'HEAD' }).then((r) => { const p = r.headers.get('content-security-policy') || ''; return [r.headers.get('content-type'), p.includes("'wasm-unsafe-eval'"), !p.includes("'unsafe-eval'")].join(' '); })`);
  assert.equal(wasmHead, 'application/wasm true true', 'wasm is served with its type and the narrow policy');
  assert.deepEqual(await csp(), [], 'no CSP violation while the kernel builds');
  const gl = await evalJs(`!!document.createElement('canvas').getContext('webgl2') || !!document.createElement('canvas').getContext('webgl')`);
  console.log('webgl available in this headless browser:', gl);
  const status = await evalJs(`document.querySelector('.statusbar, footer')?.textContent?.replace(/\\s+/g,' ').slice(0,160)`);
  console.log('status bar:', status);

  // 2. The account button names a Homestead user, never the subject; no Sign out; no GitHub or Assistant anywhere (Share is in File since S440).
  await evalJs(`document.querySelector('.avatar-btn').click()`);
  await until(`!!document.querySelector('[role=menu]')`, 'the account menu');
  const menu = await evalJs(`document.querySelector('[role=menu]').textContent`);
  assert.ok(!menu.includes('alice') && !/Sign out|GitHub|Puter/.test(menu), 'account menu: ' + menu);
  const label = await evalJs(`document.querySelector('.avatar-btn').getAttribute('aria-label')+'|'+document.querySelector('.avatar-btn').title+'|'+document.querySelector('.avatar-btn').textContent`);
  assert.ok(label.includes('Homestead user') && !label.includes('alice'), 'account button: ' + label);
  console.log('account menu:', menu.replace(/\s+/g, ' ').slice(0, 120));
  await evalJs(`document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}))`);
  for (const m of ['File', 'Help']) {
    await clickText('[role=menubar] [role=menuitem], .menubar button, .menubar [role=menuitem]', m);
    await sleep(200);
    const t = await evalJs(`[...document.querySelectorAll('[role=menu]')].map(x=>x.textContent).join(' ')`);
    assert.ok(!/GitHub|Puter|Assistant/.test(t) && (m !== 'File' || /Share/.test(t)), m + ' menu: ' + t.slice(0, 300));
    await evalJs(`document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}))`);
    await sleep(100);
  }

  // 3. AI is off. Ctrl+J (the old shortcut), the command search and the task-pane strip give no
  // Assistant; nothing in the page reaches for an AI endpoint; every request stays on this origin.
  const before = (await evalJs(`window.__net.length`));
  await evalJs(`document.body.dispatchEvent(new KeyboardEvent('keydown',{key:'j',ctrlKey:true,bubbles:true}))`);
  await sleep(300);
  assert.equal(await evalJs(`!!document.querySelector('.assistant, .as-input, textarea[aria-label*=ssistant]') || !!document.querySelector('.tp-panel:not([hidden])')`), false, 'Ctrl+J opens nothing');
  await setValue('input[type=search]', 'assistant');
  await sleep(300);
  assert.equal(await evalJs(`document.querySelector('#cmd-results').textContent.trim()`).then((t) => /Assistant|AI/.test(t)), false, 'command search offers no Assistant');
  await setValue('input[type=search]', 'share');
  await sleep(300);
  assert.equal(await evalJs(`/Share/.test(document.querySelector('#cmd-results').textContent)`), true, 'command search offers Share (S440)');
  await setValue('input[type=search]', '');
  assert.deepEqual(await evalJs(`[...document.querySelectorAll('.tp-strip [role=tab], [data-pane]')].map(b=>b.dataset.pane)`), ['files', 'appearance', 'help'], 'task pane tabs have no assistant');
  assert.equal(await evalJs(`window.__net.length`), before, 'Ctrl+J, search and tabs sent no request');

  // 4. Save the part (Parts/<name>.parametric on the bridge).
  await evalJs(`document.querySelector('[data-cmd=save]').click()`);
  await until(`puter.fs.readdir('Parts').then(l=>l.length===1,()=>false)`, 'the saved part');
  const name0 = await evalJs(`puter.fs.readdir('Parts').then(l=>l[0].name)`);
  assert.equal(name0, 'Mounting Bracket.parametric');
  assert.equal(await evalJs(`puter.fs.read('Parts/${name0}').then(b=>b.text()).then(t=>{const d=JSON.parse(t);return d.features.length>2 && t.length>500})`), true, 'the part file holds the design');

  // 5. Rename through the app's own dialog: the file moves (fs.rename), nothing is copied.
  await evalJs(`document.querySelector('.doc-title').click()`);
  await until(`!!document.querySelector('.dialog-body input')`, 'the rename dialog');
  await setValue('.dialog-body input', 'Renamed bracket');
  await evalJs(`document.querySelector('.dialog-foot .primary').click()`);
  await until(`puter.fs.readdir('Parts').then(l=>l.map(e=>e.name).join()==='Renamed bracket.parametric',()=>false)`, 'the renamed file (and no stale copy)');
  assert.equal(await evalJs(`puter.fs.rename('Parts/Renamed bracket.parametric','a/b').then(()=>'ok',e=>e.code||'err')`) !== 'ok', true, 'a path is not a name');

  // 6. A reload keeps the part: it is listed in Open and opens, the kernel builds it again.
  await evalJs(`document.querySelector('[data-cmd=save]').click()`);
  await sleep(800);
  await go(`${origin}${BASE}/`);
  await until(`!!document.querySelector('.start-card')`, 'the app to reboot');
  assert.equal(await evalJs(`puter.fs.readdir('Parts').then(l=>l.map(e=>e.name).join())`), 'Renamed bracket.parametric');
  await clickText('.start-action', 'Open');
  await until(`!!document.querySelector('.dialog .file-item')`, 'the Open dialog');
  const openText = await evalJs(`document.querySelector('.dialog-body').textContent`);
  assert.ok(openText.includes('Renamed bracket') && /Shared with me/.test(openText) && !/Puter/.test(openText), 'Open dialog: ' + openText.slice(0, 200));
  await clickText('.dialog .file-item', 'Renamed bracket');
  await until(`document.querySelector('.doc-title').textContent.includes('Renamed bracket') && /Extrude1/.test(document.querySelector('.tree').textContent) && !document.querySelector('.loading-kernel')`, 'the saved part to open');
  await sleep(1500);
  assert.equal(await evalJs(`document.querySelectorAll('.tree .badge:not(.warn)').length`), 0, 'the reopened part builds without a failed feature');
  const title1 = await evalJs(`document.querySelector('.doc-title').textContent + '|' + document.querySelector('.doc-state').textContent`);
  assert.ok(title1.includes('Renamed bracket'), 'the opened part is the renamed one: ' + title1);
  await sleep(1000);
  assert.deepEqual(await csp(), [], 'no CSP violation after reload');

  // An unsaved edit leaves a recovery snapshot in the person's own storage on the appliance after 4 s.
  await evalJs(`document.querySelector('.doc-title').click()`);
  await until(`!!document.querySelector('.dialog-body input')`, 'the rename dialog');
  await setValue('.dialog-body input', 'Alice draft');
  await evalJs(`document.querySelector('.dialog-foot .primary').click()`);
  await sleep(5500);
  assert.equal(await evalJs(`puter.fs.readdir('Recovery').then(l=>l.map(e=>e.name).join())`), 'recovery.json', 'the snapshot is on the appliance');
  assert.equal(await evalJs(`navigator.storage.getDirectory().then(async d=>{let n=0;for await (const k of d.keys()) n++;return n})`), 0, 'nothing is left in the browser profile storage');

  // 7. Second person (same browser) sees no part and cannot read alice's file.
  await go(`${origin}${BASE}/_homestead/login?subject=bob`);
  await until(`!!document.querySelector('.start-card, .dialog')`, 'the app to boot for bob');
  const recovery = await evalJs(`document.body.innerText.includes('Recover unsaved work')`);
  assert.equal(recovery, false, 'bob is not offered alice unsaved work (the snapshot is in her own storage, not the shared browser profile)');
  assert.equal(await evalJs(`puter.fs.readdir('Parts').then(l=>'listed '+l.length,e=>e.code)`), 'subject_does_not_exist', 'bob has no parts');
  assert.equal(await evalJs(`puter.fs.read('Parts/Alice draft.parametric').then(()=>'read',e=>e.code||'err')`) !== 'read', true, 'bob cannot read alice file');
  assert.equal(await evalJs(`puter.fs.rename('Parts/Alice draft.parametric','x.parametric').then(()=>'ok',e=>e.code||'err')`) !== 'ok', true, 'bob cannot rename alice file');
  assert.ok(!(await evalJs(`document.body.innerText`)).includes('alice'), 'bob does not see alice subject');
  assert.deepEqual(await csp(), [], 'no CSP violation for bob');
  // Alice comes back and is offered her own unsaved work; Discard clears it.
  await go(`${origin}${BASE}/_homestead/login?subject=alice`);
  await until(`document.body.innerText.includes('Recover unsaved work')`, 'alice recovery prompt');
  assert.ok((await evalJs(`document.body.innerText`)).includes('Alice draft'));
  await clickText('.dialog-foot button', 'Discard');
  await until(`puter.fs.readdir('Recovery').then(l=>l.length===0,e=>true)`, 'the snapshot to be cleared');

  assert.deepEqual(exceptions, [], 'no uncaught exceptions');
  // Known gap, reported not asserted: browser-side recents (localStorage).
  console.log('recents kept in the shared browser profile (localStorage):', JSON.stringify(await evalJs(`JSON.parse(localStorage.getItem('parametric:recent')||'[]').map(r=>r.name)`)));

  // 8. Everything the page asked for, from the network log and the in-page hooks.
  const foreign = requests.filter((r) => !r.url.startsWith(origin + BASE + '/') && !r.url.startsWith('data:') && !r.url.startsWith('blob:'));
  assert.deepEqual(foreign, [], 'every request stays on the app origin and path');
  assert.deepEqual(requests.filter((r) => /\/(ai|chat|completions?|llm|openai|anthropic)(\/|\?|$)|_homestead\/(ai|net)\b/i.test(r.url)), [], 'no AI request');
  const kinds = [...new Set(requests.map((r) => new URL(r.url).pathname.replace(BASE, '').replace(/\/assets\/.*/, '/assets/*').replace(/\?.*/, '')))].sort();
  console.log('distinct request paths:', JSON.stringify(kinds));

  console.log('real-browser Parametric check passed (Chrome', (await send('Browser.getVersion')).product + ')');
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
