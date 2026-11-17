// S432 real-browser check of the read-link viewer against the dev bridge:
// hostile Tasks and Notes snapshots render with zero CSP violations, no script
// runs or exists, no sub-request is made, and a script injected by hand is
// refused by the policy (the positive control).
// TEST ONLY (dev identity is not authentication). Zero dependencies: drives
// headless Chrome/Chromium over the DevTools protocol with Node's WebSocket.
// usage: node browser-check-viewer.mjs <bridge-bin> <dist-dir> <tmp-dir>
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import assert from 'node:assert/strict';

const [bin, dist, tmp] = process.argv.slice(2);
const BASE = '/apps/tasks';
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
const cleanup = async () => { browser?.kill('SIGKILL'); if (bridge && bridge.exitCode === null) { bridge.kill('SIGTERM'); } };
const profile = path.join(tmp, 'profile');
fs.mkdirSync(profile, { recursive: true });
let browser = null;
const hard = setTimeout(async () => { console.error('FAIL: timeout'); await cleanup(); process.exit(1); }, 90000);

try {
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

  // Mint two links as alice through the real data route.
  const login = await (await fetch(`${origin}/_dev/login`, { method: 'POST', body: JSON.stringify({ subject: 'alice' }) })).json();
  const call = async (body) => {
    const r = await fetch(`${origin}${BASE}/_homestead/data`, { method: 'POST', body: JSON.stringify(body),
      headers: { 'Content-Type': 'application/json', Origin: origin, Cookie: `hs_dev_session=${login.cookie}`, 'X-Homestead-CSRF': login.csrf } });
    const t = await r.text();
    assert.equal(r.status, 200, `${body.operation}: ${r.status} ${t}`);
    return t ? JSON.parse(t) : {};
  };
  const put = (key, text) => call({ operation: 'put', kind: 'file', key, value: Buffer.from(text).toString('base64') });
  const evil = '<script>window.__pwned=1</script><img src=x onerror="window.__pwned=2"><a href="javascript:window.__pwned=3">x</a>';
  await put('lists/evil.json', JSON.stringify({ kind: 'tasks.shared-list', v: 1, name: evil, owner: evil, updatedAt: 1793700000000,
    tasks: [{ title: evil, notes: evil + '\nsecond line', due: '2026-11-05', priority: 3, tags: [evil], order: 1, subtasks: [{ title: evil, done: true }] }] }));
  await put('published/p.json', JSON.stringify({ format: 'notes-shared-page', v: 2, title: evil, published: '2026-10-20T10:00:00.000Z', ink: [{ d: 'M0 0' }],
    outlines: [{ x: 1, y: 1, w: 100, html: '<h1>Head</h1><p onclick="window.__pwned=4">' + evil + '</p><ul><li>one</li></ul><a href="https://example.com/x" onclick="window.__pwned=5">ok</a><svg onload="window.__pwned=6"></svg>' }] }));
  const urls = {};
  for (const [name, key] of [['tasks', 'lists/evil.json'], ['notes', 'published/p.json']]) {
    const u = (await call({ operation: 'fs.getReadURL', key })).url;
    urls[name] = u.replace('/_homestead/read/', '/_homestead/view/');
  }

  browser = spawn(chrome, ['--headless=new', '--no-sandbox', '--disable-gpu', '--remote-debugging-port=0',
    `--user-data-dir=${profile}`, '--no-first-run', '--no-default-browser-check', 'about:blank'], { stdio: 'ignore' });
  const f = path.join(profile, 'DevToolsActivePort');
  for (let i = 0; i < 100 && !fs.existsSync(f); i++) await sleep(100);
  const [p] = fs.readFileSync(f, 'utf8').split('\n');
  const ws = new WebSocket((await (await fetch(`http://127.0.0.1:${p}/json/version`)).json()).webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let id = 0; const pending = new Map(); const waiters = []; const exceptions = []; const requests = []; const securityLogs = [];
  ws.onmessage = (m) => {
    const msg = JSON.parse(m.data);
    if (msg.id && pending.has(msg.id)) { const { res, rej } = pending.get(msg.id); pending.delete(msg.id); msg.error ? rej(new Error(JSON.stringify(msg.error))) : res(msg.result); return; }
    if (msg.method === 'Network.requestWillBeSent') requests.push(msg.params.request.url);
    if (msg.method === 'Runtime.exceptionThrown') exceptions.push(JSON.stringify(msg.params.exceptionDetails).slice(0, 300));
    if (msg.method === 'Log.entryAdded' && /content security policy/i.test(msg.params.entry.text)) securityLogs.push(msg.params.entry.text);
    for (const w of waiters.slice()) if (w.method === msg.method) { waiters.splice(waiters.indexOf(w), 1); w.res(msg.params); }
  };
  const send = (method, params = {}, sessionId) => new Promise((res, rej) => { const i = ++id; pending.set(i, { res, rej }); ws.send(JSON.stringify({ id: i, method, params, sessionId })); });
  const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
  const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
  const cmd = (m, pr) => send(m, pr, sessionId);
  for (const d of ['Page.enable', 'Runtime.enable', 'Network.enable', 'Log.enable']) await cmd(d);
  await cmd('Page.addScriptToEvaluateOnNewDocument', { source: `window.__csp=[];document.addEventListener('securitypolicyviolation',e=>window.__csp.push(e.violatedDirective+' '+e.blockedURI));` });
  const evalJs = async (expression) => {
    const r = await cmd('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error('eval: ' + JSON.stringify(r.exceptionDetails).slice(0, 400));
    return r.result.value;
  };
  const go = async (url) => { const loaded = new Promise((res) => waiters.push({ method: 'Page.loadEventFired', res })); await cmd('Page.navigate', { url }); await Promise.race([loaded, sleep(15000)]); await sleep(500); };

  for (const [name, url] of Object.entries(urls)) {
    requests.length = 0; securityLogs.length = 0;
    await go(url);
    assert.equal(await evalJs('location.href'), url, `${name}: the viewer page loaded`);
    assert.deepEqual(await evalJs('window.__csp'), [], `${name}: zero CSP violations`);
    assert.deepEqual(securityLogs, [], `${name}: no CSP console message`);
    assert.equal(await evalJs('window.__pwned === undefined'), true, `${name}: no script ran`);
    assert.equal(await evalJs('document.scripts.length'), 0, `${name}: no script element`);
    assert.equal(await evalJs('document.querySelectorAll("img,iframe,object,embed,svg,form,input,[onclick],[onload],[onerror],[style]").length'), 0, `${name}: no active or embedded element`);
    assert.equal(await evalJs('[...document.querySelectorAll("a")].filter(a=>!/^https?:/.test(a.href)).length'), 0, `${name}: only http(s) links`);
    assert.deepEqual(requests, [url], `${name}: no sub-request`);
    const text = await evalJs('document.body.innerText');
    assert.ok(text.includes('<script>window.__pwned=1</script>'), `${name}: hostile text is shown as text`);
    if (name === 'notes') assert.ok(text.includes('not shown in this viewer') && text.includes('Head'), 'notes: marker and heading');
    if (name === 'tasks') assert.ok(text.includes('High priority') && text.includes('second line'), 'tasks: fields');
    // Positive control: the policy really is enforced here.
    await evalJs(`(()=>{const s=document.createElement('script');s.textContent='window.__pwned=9';document.head.appendChild(s);return 1})()`);
    await sleep(300);
    assert.equal(await evalJs('window.__pwned === undefined'), true, `${name}: an injected script is refused`);
    assert.ok((await evalJs('window.__csp')).some((v) => v.startsWith('script-src')), `${name}: the policy reported the injected script`);
  }
  assert.deepEqual(exceptions, [], 'no uncaught exceptions');
  console.log('real-browser viewer check passed (Chrome', (await send('Browser.getVersion')).product + ')');
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
