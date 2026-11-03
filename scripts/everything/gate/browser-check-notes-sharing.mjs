// S438 real-browser check of Notes read links against the dev bridge (ADR-058).
// Notes has no person-to-person sharing (upstream offers read links only), so the
// three people are: alice (owner), bob (signed in, has Notes access, was given the
// link) and carol (signed in, was not given the link), plus a visitor with no session.
//   alice publishes a page (text, a picture and handwriting) and gets a viewer link;
//   bob opens it: text shown, picture and ink marked "not shown", no script;
//   alice edits and republishes: the same link shows the new text;
//   carol, without the link, cannot read or link alice's file (own namespace only);
//   alice stops sharing: bob and the visitor get the uniform 404.
// TEST ONLY (dev identity is not authentication). Zero dependencies.
// usage: node browser-check-notes-sharing.mjs <bridge-bin> <dist-dir> <tmp-dir>
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import assert from 'node:assert/strict';

const [bin, dist, tmp] = process.argv.slice(2);
const BASE = '/apps/notes';
let chrome = null;
for (const c of [process.env.CHROME, 'google-chrome', 'chromium', 'chromium-browser'].filter(Boolean)) {
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
const profile = path.join(tmp, 'profile');
fs.mkdirSync(profile, { recursive: true });
let browser = null;
const cleanup = async () => { browser?.kill('SIGKILL'); if (bridge && bridge.exitCode === null) bridge.kill('SIGTERM'); };
const hard = setTimeout(async () => { console.error('FAIL: timeout'); await cleanup(); process.exit(1); }, 240000);

async function wsUrl() {
  const f = path.join(profile, 'DevToolsActivePort');
  for (let i = 0; i < 100; i++) {
    if (fs.existsSync(f)) {
      const [p] = fs.readFileSync(f, 'utf8').split('\n');
      return (await (await fetch(`http://127.0.0.1:${p}/json/version`)).json()).webSocketDebuggerUrl;
    }
    await sleep(100);
  }
  throw new Error('browser did not start');
}

try {
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

  browser = spawn(chrome, ['--headless=new', '--no-sandbox', '--disable-gpu', '--remote-debugging-port=0',
    `--user-data-dir=${profile}`, '--no-first-run', '--no-default-browser-check', 'about:blank'], { stdio: 'ignore' });
  const ws = new WebSocket(await wsUrl());
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let id = 0; const pending = new Map(); const waiters = []; const exceptions = [];
  ws.onmessage = (m) => {
    const msg = JSON.parse(m.data);
    if (msg.id && pending.has(msg.id)) { const { res, rej } = pending.get(msg.id); pending.delete(msg.id); msg.error ? rej(new Error(JSON.stringify(msg.error))) : res(msg.result); return; }
    if (msg.method === 'Runtime.exceptionThrown') exceptions.push(`${msg.sessionId?.slice(0, 4)} ${JSON.stringify(msg.params.exceptionDetails).slice(0, 300)}`);
    for (const w of waiters.slice()) if (w.method === msg.method && w.sessionId === msg.sessionId) { waiters.splice(waiters.indexOf(w), 1); w.res(msg.params); }
  };
  const send = (method, params = {}, sessionId) => new Promise((res, rej) => {
    const i = ++id; pending.set(i, { res, rej }); ws.send(JSON.stringify({ id: i, method, params, sessionId }));
  });

  // One page per person, each in its own browser context (own cookies and storage).
  async function person(name) {
    const { browserContextId } = await send('Target.createBrowserContext');
    const { targetId } = await send('Target.createTarget', { url: 'about:blank', browserContextId });
    const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
    const cmd = (m, p) => send(m, p, sessionId);
    await cmd('Page.enable'); await cmd('Runtime.enable');
    await cmd('Page.addScriptToEvaluateOnNewDocument', { source: `window.__csp=[];document.addEventListener('securitypolicyviolation',e=>window.__csp.push(e.violatedDirective+' '+e.blockedURI));` });
    await cmd('Page.addScriptToEvaluateOnNewDocument', { source: `window.__es={urls:[],msgs:0};(function(){var E=window.EventSource;if(!E)return;window.EventSource=function(u,o){var s=new E(u,o);window.__es.urls.push(String(u));s.addEventListener('msg',function(){window.__es.msgs++});return s};window.EventSource.prototype=E.prototype;window.EventSource.CONNECTING=0;window.EventSource.OPEN=1;window.EventSource.CLOSED=2})();` });
    const p = { name };
    p.js = async (expression) => {
      const r = await cmd('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
      if (r.exceptionDetails) throw new Error(`${name} eval: ` + JSON.stringify(r.exceptionDetails).slice(0, 400));
      return r.result.value;
    };
    p.go = async (url) => {
      const loaded = new Promise((res) => waiters.push({ method: 'Page.loadEventFired', sessionId, res }));
      await cmd('Page.navigate', { url }); await Promise.race([loaded, sleep(15000)]); await sleep(300);
    };
    p.until = async (expr, what, tries = 150) => {
      for (let i = 0; i < tries; i++) { if (await p.js(expr)) return; await sleep(100); }
      throw new Error(`${name}: timeout waiting for ${what}`);
    };
    p.click = (sel, text) => p.js(`(()=>{const b=[...document.querySelectorAll(${JSON.stringify(sel)})].find(x=>x.textContent.includes(${JSON.stringify(text)}));if(!b)return false;b.click();return true})()`);
    p.clickSel = (sel) => p.js(`(()=>{const b=document.querySelector(${JSON.stringify(sel)});if(!b)return false;b.click();return true})()`);
    p.type = (sel, v) => p.js(`(()=>{const i=document.querySelector(${JSON.stringify(sel)});i.value=${JSON.stringify(v)};i.dispatchEvent(new Event('input',{bubbles:true}));return true})()`);
    p.text = () => p.js(`document.body.textContent`);
    p.csp = () => p.js(`window.__csp||[]`);
    p.boot = async () => {
      await p.go(`${origin}${BASE}/_homestead/login?subject=${name}`);
      await p.until(`!!window.notesApp && document.body.textContent.includes(${JSON.stringify(name + "'s Notebook")})`, 'the app to boot', 200);
    };
    return p;
  }
  const alice = await person('alice');
  const bob = await person('bob');
  const carol = await person('carol');
  for (const p of [alice, bob, carol]) await p.boot();
  assert.equal(await alice.js(`puter.auth.getUser().then(u=>u.username)`), 'alice');
  // Sharing is not switched on for Notes: no person-to-person sharing exists.
  assert.equal(await alice.js(`puter.kv.share('k','bob').then(()=>'ok',e=>e.code)`), 'kv_handles_disabled');

  // 1. Alice writes a page: text, a picture and a handwriting stroke.
  await alice.js(`window.notesApp.runCommand('newPage')`);
  await alice.until(`!!window.notesApp.editor.page`, 'the new page');
  const png = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==';
  await alice.js(`(()=>{const e=window.notesApp.editor;e.titleEl.textContent='Garden plan';e.titleEl.dispatchEvent(new Event('input',{bubbles:true}));e.commit();
    const el=e.createOutlineAt(48,140,'');el.querySelector('.outline-body').innerHTML='<p>Plant the tomatoes in May</p><p><img src="${png}" alt="dot"></p>';e.markActiveDirty();
    e.page.ink=[{id:'i1',tool:'pen',color:'#000000',width:2,points:[[0,0,1],[10,10,1],[20,5,1]]}];return true})()`);
  const pageId = await alice.js(`window.notesApp.state.pageId`);
  await alice.until(`puter.fs.read('notes/pages/${pageId}.json').then(b=>b.text()).then(t=>t.includes('tomatoes'),()=>false)`, 'the page on the bridge');

  // 2. Share dialog: Create Link hands out the bridge viewer, not the app page.
  await alice.js(`window.notesApp.runCommand('share')`);
  await alice.until(`!!document.querySelector('input[aria-label="Read-only link"]')`, 'the read-only link field');
  const dlg = await alice.text();
  assert.ok(!dlg.includes('Puter'), 'no Puter wording in the share dialog');
  assert.ok(/Create a link anyone can open to read this page/.test(dlg));
  assert.ok(await alice.click('button', 'Create Link'));
  await alice.until(`document.querySelector('input[aria-label="Read-only link"]').value.length>0`, 'the link');
  const link = await alice.js(`document.querySelector('input[aria-label="Read-only link"]').value`);
  assert.match(link, new RegExp(`^${origin}${BASE}/_homestead/view/[A-Za-z0-9_-]{43}$`), 'the link is the viewer');
  assert.ok((await alice.text()).includes('Pictures and handwriting are not shown in the viewer'));
  const snap = JSON.parse(await alice.js(`puter.fs.read('notes/published/${pageId}.json').then(b=>b.text())`));
  assert.equal(snap.format, 'notes-shared-page');
  assert.ok(snap.ink.length > 0, 'the snapshot has ink');

  // 3. Bob (signed in, given the link) and a visitor (no session) read it in the viewer.
  const visitor = await person('visitor');
  for (const p of [bob, visitor]) {
    await p.go(link);
    const html = await p.js(`document.documentElement.outerHTML`);
    assert.match(html, /Garden plan/); assert.match(html, /Plant the tomatoes in May/);
    assert.ok(/not shown in this viewer/.test(html), `${p.name}: image/ink marker`);
    assert.ok(!/<img/i.test(html), `${p.name}: no image element`);
    assert.ok(!/<script/i.test(html), `${p.name}: no script`);
    assert.deepEqual(await p.csp(), [], `${p.name}: no CSP violation in the viewer`);
  }
  assert.equal((await fetch(link)).status, 200);

  // 4. Live update: edit and publish again; the link shows the page as it is now.
  await alice.js(`document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}))`);
  await alice.js(`(()=>{const e=window.notesApp.editor;const el=e.createOutlineAt(48,400,'');el.querySelector('.outline-body').innerHTML='<p>Repot the herbs in June</p>';e.markActiveDirty();return true})()`);
  await alice.js(`window.notesApp.runCommand('share')`);
  await alice.until(`!!document.querySelector('input[aria-label="Read-only link"]')`, 'the share dialog again');
  await alice.click('button', 'Create Link');
  await alice.until(`puter.fs.read('notes/published/${pageId}.json').then(b=>b.text()).then(t=>t.includes('Repot the herbs'),()=>false)`, 'the republished snapshot');
  await bob.go(link);
  assert.match(await bob.js(`document.body.textContent`), /Repot the herbs in June/, 'bob sees the update on the same link');

  // 5. Carol (signed in, no link, no grant) cannot reach alice's published file or link it.
  assert.notEqual(await carol.js(`puter.fs.read('notes/published/${pageId}.json').then(()=>'read',()=>'denied')`), 'read', 'carol cannot read alice\'s published page by path');
  assert.notEqual(await carol.js(`puter.fs.getReadURL('notes/published/${pageId}.json','1d').then(()=>'link',()=>'denied')`), 'link', 'carol cannot mint a link to alice\'s file');
  const token = link.split('/').pop();
  const wrong = token.slice(0, -1) + (token.endsWith('A') ? 'B' : 'A');
  const notFound = await fetch(`${origin}${BASE}/_homestead/view/${wrong}`);
  assert.equal(notFound.status, 404, 'a guessed token is a 404');
  const gotCarol = await carol.js(`fetch('${BASE}/_homestead/view/${wrong}').then(r=>r.status)`);
  assert.equal(gotCarol, 404);
  assert.equal(await carol.js(`window.notesApp.store.findPage(${JSON.stringify(pageId)})?'has':'none'`), 'none', 'carol has none of alice\'s pages');

  // 6. Alice stops sharing: bob and the visitor then get the uniform 404 page.
  await alice.js(`window.notesApp.runCommand('share')`);
  await alice.until(`[...document.querySelectorAll('button')].some(b=>b.textContent.includes('Stop Sharing') && !b.hidden)`, 'Stop Sharing');
  assert.ok(await alice.click('button', 'Stop Sharing'));
  await alice.until(`document.body.textContent.includes('Sharing stopped')`, 'sharing stopped');
  assert.equal((await fetch(link)).status, 404, 'the stopped link is a 404');
  for (const p of [bob, visitor]) {
    await p.go(link);
    assert.ok(!(await p.js(`document.body.textContent`)).includes('Plant the tomatoes'), `${p.name}: nothing of the page after stop`);
  }

  // 7. Everything stayed under the policy.
  for (const p of [alice, bob, carol, visitor]) assert.deepEqual(await p.csp(), [], `${p.name}: no CSP violation`);
  assert.deepEqual(exceptions, [], 'no uncaught exceptions');

  console.log('real-browser Notes read-link check passed (Chrome', (await send('Browser.getVersion')).product + ')');
  ws.close(); clearTimeout(hard); await cleanup(); process.exit(0);
} catch (e) {
  console.error('FAIL:', e.stack || e);
  clearTimeout(hard); await cleanup(); process.exit(1);
}
