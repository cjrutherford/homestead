// S440 real-browser check of Parametric sharing against the dev bridge started with
// sharing on (ADR-058). Three people, each in their own browser context:
//   alice shares a saved part with bob (view, then edit) through the app's
//   own Share dialog; bob finds it under "Shared with me", opens it, and his
//   saves land in alice's file (view only: they make his own copy); carol (signed in, no grant) and a never-signed-in
//   name are denied; alice removes bob and he loses access.
// TEST ONLY (dev identity is not authentication). Zero dependencies: drives
// headless Chrome/Chromium over the DevTools protocol with Node's WebSocket.
// usage: node browser-check-parametric-sharing.mjs <bridge-bin> <dist-dir> <tmp-dir>
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import assert from 'node:assert/strict';

const [bin, dist, tmp] = process.argv.slice(2);
const BASE = '/apps/parametric';
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
const hard = setTimeout(async () => { console.error('FAIL: timeout'); await cleanup(); process.exit(1); }, 420000);

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
      await p.until(`!!document.querySelector('.start-card') && !!window.puter`, 'the app to boot');
    };
    return p;
  }
  const alice = await person('alice');
  const bob = await person('bob');
  const carol = await person('carol');
  for (const p of [alice, bob, carol]) await p.boot();

  const code = (p, expr) => p.js(`(${expr}).then(()=>'ok',e=>e.code||e.message||'err')`);
  // The share dialog is the app's own; drive it with the keyboard-level controls.
  const dialogShare = async (p, who, access) => {
    await p.until(`!!document.querySelector('input[aria-label="Person to share with"]')`, 'the share dialog');
    await p.type('input[aria-label="Person to share with"]', who);
    await p.js(`(()=>{const s=document.querySelector('select[aria-label="Access"]');s.value=${JSON.stringify(access)};s.dispatchEvent(new Event('change',{bubbles:true}));return true})()`);
    assert.ok(await p.click('.dialog-body button', 'Share'), 'Share button');
  };

  const status = `document.querySelector('.dialog-body [role=status]').textContent`;
  const kernelReady = (p) => p.until(`!document.querySelector('.loading-kernel') && document.querySelectorAll('.tree [role=treeitem]').length > 3`, 'the part to build');
  const decimals = (p, file) => p.js(`puter.fs.read(${JSON.stringify(file)}).then(b=>b.text()).then(t=>JSON.parse(t).decimals)`);
  // Change the part through the app's own Part Properties dialog (decimal places), then Save.
  const editDecimals = async (p, n) => {
    assert.ok(await p.click('[role=menubar] [role=menuitem]', 'File'), 'File menu');
    await sleep(200);
    assert.ok(await p.click('[role=menu] [role=menuitem]', 'Part Properties'), 'Part Properties entry');
    await p.until(`!!document.querySelector('input[aria-label="Decimal places"]')`, 'the properties dialog');
    await p.type('input[aria-label="Decimal places"]', String(n));
    await p.js(`document.querySelector('.dialog-foot .primary').click()`);
    await sleep(300);
    await p.clickSel('[data-cmd=save]');
  };
  const openShared = async (p, name) => {
    await p.go(`${origin}${BASE}/`);
    await p.until(`!!document.querySelector('.start-card')`, 'the app to reload');
    assert.ok(await p.click('.start-action', 'Open'), 'Open on the start page');
    await p.until(`document.querySelector('.dialog-body')?.textContent.includes('Shared with me') && !document.querySelector('.dialog-body [aria-busy=true]')`, 'the Shared with me list');
    return p.js(`document.querySelector('.dialog-body').textContent`);
  };

  // 1. Alice opens the example part, saves it, and opens the app's own Share dialog.
  assert.equal(await alice.js(`typeof puter.fs.share+' '+typeof puter.fs.listShared+' '+typeof puter.ai+' '+typeof puter.net`), 'function function undefined object');
  assert.equal(await alice.js(`puter.net.fetch('https://example.com/').then(()=>'ok',e=>e.code||'err')`), 'net_disabled', 'net stays off');
  assert.ok(await alice.click('.start-action', 'Mounting Bracket'));
  await kernelReady(alice);
  await alice.clickSel('[data-cmd=save]');
  const path0 = 'Parts/Mounting Bracket.parametric';
  await alice.until(`puter.fs.readdir('Parts').then(l=>l.map(e=>e.name).join()==='Mounting Bracket.parametric',()=>false)`, 'the saved part');
  const dec0 = await decimals(alice, path0);
  assert.ok(await alice.clickSel('[data-cmd=share]'), 'the Share button is in the title bar');
  await alice.until(`document.body.textContent.includes('Not shared with anyone yet')`, 'the share dialog (empty list)');
  assert.ok(!/Puter/.test(await alice.js(`document.querySelector('.dialog').textContent`)), 'no Puter wording in the share dialog');
  assert.equal(await alice.js(`document.querySelector('input[aria-label="Person to share with"]').placeholder`), 'Homestead username');

  // 2. A name nobody has and yourself are refused with a message; nothing is shared.
  await dialogShare(alice, 'dave', 'read');
  await alice.until(`document.body.textContent.includes('Could not share') || document.body.textContent.includes('no Homestead user')`, 'refusal for an unknown name');
  console.log('unknown-name refusal text:', JSON.stringify(await alice.js(status)));
  await dialogShare(alice, 'alice', 'read');
  await alice.until(`/already own/.test(${status}) || /Could not share/.test(${status})`, 'refusal for herself');
  console.log('self refusal text:', JSON.stringify(await alice.js(status)));
  await dialogShare(alice, 'e@x.test', 'read');
  await sleep(600);
  console.log('email refusal text:', JSON.stringify(await alice.js(status)));
  assert.deepEqual(await alice.js(`puter.fs.getShares(${JSON.stringify(path0)})`), [], 'nothing was shared by the refusals');

  // 3. Share with bob, view only.
  await dialogShare(alice, 'bob', 'read');
  await alice.until(`document.body.textContent.includes('Shared with bob')`, 'bob invited');
  let shares = await alice.js(`puter.fs.getShares(${JSON.stringify(path0)})`);
  assert.equal(shares.length, 1); assert.equal(shares[0].holder, 'bob'); assert.equal(shares[0].mode, 'read');

  // 4. Bob finds it under "Shared with me" in the app's Open dialog and opens it view-only.
  const bobText = await openShared(bob, 'Mounting Bracket');
  assert.ok(bobText.includes('From alice') && bobText.includes('View only'), 'Shared with me: ' + bobText.slice(0, 200));
  const bobItems = await bob.js(`puter.fs.listShared({limit:50}).then(r=>r.items.map(i=>({p:i.path,n:i.name,o:i.owner,m:i.mode})))`);
  assert.equal(bobItems.length, 1); assert.equal(bobItems[0].n, 'Mounting Bracket.parametric'); assert.equal(bobItems[0].m, 'read');
  assert.match(bobItems[0].p, /^~shared\//);
  assert.ok(await bob.click('.dialog .file-item', 'From alice'));
  await kernelReady(bob);
  await bob.until(`document.body.textContent.includes('view only')`, 'the view-only notice');
  const before = await alice.js(`puter.fs.read(${JSON.stringify(path0)}).then(b=>b.text())`);
  assert.notEqual(await code(bob, `puter.fs.write(${JSON.stringify(bobItems[0].p)},'x',{overwrite:true})`), 'ok', 'a view-only write is refused');
  assert.notEqual(await code(bob, `puter.fs.delete(${JSON.stringify(bobItems[0].p)})`), 'ok', 'a recipient cannot delete');
  assert.notEqual(await code(bob, `puter.fs.rename(${JSON.stringify(bobItems[0].p)},'x.parametric')`), 'ok', 'a recipient cannot rename');
  assert.notEqual(await code(bob, `puter.fs.share(${JSON.stringify(bobItems[0].p)},{username:'carol'},'read')`), 'ok', 'a recipient cannot re-share');
  // The app opens a view-only share detached: Save makes bob's OWN copy.
  await editDecimals(bob, 5);
  await bob.until(`puter.fs.readdir('Parts').then(l=>l.length===1,()=>false)`, "bob's own copy");
  assert.equal(await bob.js(`puter.fs.read('Parts/Mounting Bracket.parametric').then(b=>b.text()).then(t=>JSON.parse(t).decimals)`), 5, 'the copy is bob\'s edit');
  assert.equal(await alice.js(`puter.fs.read(${JSON.stringify(path0)}).then(b=>b.text())`), before, "alice's file is unchanged by a view-only recipient");

  // 5. Alice upgrades bob to edit; his save lands in alice's file.
  await dialogShare(alice, 'bob', 'write');
  await alice.until(`document.body.textContent.includes('Updated access for bob')`, 'bob upgraded');
  shares = await alice.js(`puter.fs.getShares(${JSON.stringify(path0)})`);
  assert.equal(shares.length, 1); assert.equal(shares[0].mode, 'write');
  const bob2 = await openShared(bob, 'Mounting Bracket');
  assert.ok(bob2.includes('Can edit'), 'Shared with me now says Can edit: ' + bob2.slice(0, 200));
  assert.ok(await bob.click('.dialog .file-item', 'From alice'));
  await kernelReady(bob);
  assert.ok(!(await bob.text()).includes('view only'), 'no view-only notice with edit access');
  const target = dec0 === 3 ? 4 : 3;
  await editDecimals(bob, target);
  await bob.until(`document.body.textContent.includes('Saved to')`, 'the save to finish');
  assert.equal(await decimals(alice, path0), target, "the save landed in alice's file");
  assert.equal(await alice.js(`puter.fs.readdir('Parts').then(l=>l.map(e=>e.name).join())`), 'Mounting Bracket.parametric', 'alice still has exactly one part');
  assert.equal(await bob.js(`puter.fs.read('Parts/Mounting Bracket.parametric').then(b=>b.text()).then(t=>JSON.parse(t).decimals)`), 5, "bob's own copy is untouched");

  // 6. Carol (signed in, no grant) reaches nothing.
  assert.deepEqual(await carol.js(`puter.fs.listShared({limit:50}).then(r=>r.items)`), []);
  assert.notEqual(await code(carol, `puter.fs.read(${JSON.stringify(bobItems[0].p)})`), 'ok', "carol cannot read through bob's handle");
  assert.notEqual(await code(carol, `puter.fs.write(${JSON.stringify(bobItems[0].p)},'x',{overwrite:true})`), 'ok', 'carol cannot write through it');
  assert.notEqual(await code(carol, `puter.fs.read(${JSON.stringify(path0)})`), 'ok', "carol cannot read alice's path");
  assert.deepEqual(await carol.js(`puter.fs.getShares(${JSON.stringify(path0)}).catch(()=>[])`), [], "carol sees none of alice's shares");
  const carolText = await openShared(carol, 'Mounting Bracket');
  assert.ok(carolText.includes('Nothing has been shared with you yet') && !carolText.includes('Mounting Bracket') && !carolText.includes('alice'), 'carol sees nothing: ' + carolText.slice(0, 200));

  // 7. Alice removes bob in the dialog; bob loses access at once, and a save through his open handle fails visibly.
  const bobOpen = await openShared(bob, 'Mounting Bracket');
  assert.ok(await bob.click('.dialog .file-item', 'From alice'));
  await kernelReady(bob);
  assert.ok(await alice.click('.dialog-body button', 'Remove'), 'Remove button');
  await alice.until(`document.body.textContent.includes('Not shared with anyone yet')`, 'bob removed');
  assert.deepEqual(await alice.js(`puter.fs.getShares(${JSON.stringify(path0)})`), []);
  assert.deepEqual(await bob.js(`puter.fs.listShared({limit:50}).then(r=>r.items)`), [], "bob's shared list is empty");
  assert.notEqual(await code(bob, `puter.fs.read(${JSON.stringify(bobItems[0].p)})`), 'ok', 'bob can no longer read');
  assert.notEqual(await code(bob, `puter.fs.write(${JSON.stringify(bobItems[0].p)},'x',{overwrite:true})`), 'ok', 'bob can no longer write');
  await editDecimals(bob, 2);
  await bob.until(`document.body.textContent.includes('Could not save')`, 'a visible save failure after revocation');
  assert.notEqual(await decimals(alice, path0), 2, "bob's refused save did not reach alice's file");
  assert.equal(await decimals(alice, path0), target, 'alice keeps the part as bob last saved it');

  // 8. Everything stayed under the policy.
  for (const p of [alice, bob, carol]) assert.deepEqual(await p.csp(), [], `${p.name}: no CSP violation`);
  assert.deepEqual(exceptions, [], 'no uncaught exceptions');

  console.log('real-browser Parametric sharing check passed (Chrome', (await send('Browser.getVersion')).product + ')');
  ws.close(); clearTimeout(hard); await cleanup(); process.exit(0);
} catch (e) {
  console.error('FAIL:', e.stack || e);
  clearTimeout(hard); await cleanup(); process.exit(1);
}
