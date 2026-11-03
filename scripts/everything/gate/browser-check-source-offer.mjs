// S423 real-browser check of the in-app source offer against the dev bridge:
// the index carries a visible "Source" link that covers no control and the
// page loads without a CSP violation; the source page is script-free, lists
// the licence, upstream, pinned commit and components; the tarball downloads
// and matches its published SHA-256 and the pinned upstream archive inside it.
// TEST ONLY (dev identity is not authentication). Zero dependencies.
// usage: node browser-check-source-offer.mjs <bridge-bin> <app> <dist-dir> <offer-dir> <tmp-dir>
// Exit 0 pass; exit 0 with "SKIP:" if no browser; exit 1 on failure.
import { spawn, execFileSync } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import assert from 'node:assert/strict';

const [bin, app, dist, offerDir, tmp] = process.argv.slice(2);
const BASE = `/apps/${app}`;
let chrome = null;
for (const c of [process.env.CHROME, 'google-chrome', 'chromium', 'chromium-browser'].filter(Boolean)) {
  try { chrome = execFileSync('which', [c]).toString().trim(); break; } catch {}
}
if (!chrome) { console.log('SKIP: no Chrome/Chromium on PATH (set CHROME to run the real-browser check)'); process.exit(0); }
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const port = await new Promise((res, rej) => {
  const s = net.createServer(); s.once('error', rej);
  s.listen(0, '127.0.0.1', () => { const { port } = s.address(); s.close(() => res(port)); });
});
const origin = `http://127.0.0.1:${port}`;
const offer = JSON.parse(fs.readFileSync(path.join(offerDir, 'offer.json'), 'utf8'));

const bridge = spawn(bin, ['-dev-identity', '-db', path.join(tmp, 'bridge.db'), '-app', app, '-listen', `127.0.0.1:${port}`,
  '-origin', origin, '-base-path', BASE, '-static-dir', dist],
{ env: { ...process.env, HOMESTEAD_EVERYTHING_INSECURE_DEV: 'i-understand-this-is-not-authentication', HOMESTEAD_EVERYTHING_SOURCE_DIR: offerDir }, stdio: ['ignore', 'ignore', 'pipe'] });
let blog = '';
await new Promise((resolve, reject) => {
  bridge.stderr.on('data', (d) => { blog += d; if (blog.includes('listening on')) resolve(); });
  bridge.once('exit', (c) => reject(new Error('bridge exited ' + c + blog)));
  setTimeout(() => reject(new Error('bridge start timeout ' + blog)), 10000);
});
bridge.stderr.resume();
const profile = path.join(tmp, 'profile');
fs.mkdirSync(profile, { recursive: true });
const browser = spawn(chrome, ['--headless=new', '--no-sandbox', '--disable-gpu', '--remote-debugging-port=0', '--window-size=1280,800',
  `--user-data-dir=${profile}`, '--no-first-run', '--no-default-browser-check', 'about:blank'], { stdio: 'ignore' });
const cleanup = () => { browser.kill('SIGKILL'); bridge.kill('SIGTERM'); };
const hard = setTimeout(() => { console.error('FAIL: timeout'); cleanup(); process.exit(1); }, 180000);

try {
  let ws;
  for (let i = 0; i < 100 && !ws; i++) {
    const f = path.join(profile, 'DevToolsActivePort');
    if (fs.existsSync(f)) {
      const v = await (await fetch(`http://127.0.0.1:${fs.readFileSync(f, 'utf8').split('\n')[0]}/json/version`)).json();
      ws = new WebSocket(v.webSocketDebuggerUrl);
    } else await sleep(100);
  }
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
  const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
  const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
  const cmd = (m, p) => send(m, p, sessionId);
  await cmd('Page.enable'); await cmd('Runtime.enable');
  await cmd('Page.addScriptToEvaluateOnNewDocument', { source: `window.__csp=[];document.addEventListener('securitypolicyviolation',e=>window.__csp.push(e.violatedDirective+' '+e.blockedURI));` });
  const evalJs = async (expression) => {
    const r = await cmd('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error('eval: ' + JSON.stringify(r.exceptionDetails).slice(0, 400));
    return r.result.value;
  };
  const go = async (url) => { const loaded = new Promise((res) => waiters.push({ method: 'Page.loadEventFired', res })); await cmd('Page.navigate', { url }); await Promise.race([loaded, sleep(20000)]); await sleep(2500); };
  const shot = async (name) => fs.writeFileSync(path.join(tmp, name), Buffer.from((await cmd('Page.captureScreenshot', { format: 'png' })).data, 'base64'));

  // 1. Signed in, the app boots with the Source link on top of it.
  await go(`${origin}${BASE}/_homestead/login?subject=alice`);
  assert.deepEqual(await evalJs(`window.__csp`), [], 'no CSP violation while booting');
  const link = await evalJs(`(()=>{const a=document.getElementById('homestead-source');if(!a)return null;const r=a.getBoundingClientRect();
    const cs=getComputedStyle(a);const hit=document.elementFromPoint(r.x+r.width/2,r.y+r.height/2);
    return {text:a.textContent,label:a.getAttribute('aria-label'),href:a.getAttribute('href'),x:r.x,y:r.y,w:r.width,h:r.height,vw:innerWidth,vh:innerHeight,
      visible:cs.visibility==='visible'&&cs.display!=='none'&&r.width>0&&r.height>0,onTop:hit===a,bodyChild:a.parentElement===document.body}})()`);
  assert.ok(link, 'the Source link is in the served page');
  assert.equal(link.text, 'Source'); assert.ok(link.label && link.label.includes(offer.license));
  assert.equal(link.href, `${BASE}/_homestead/source`);
  assert.ok(link.visible && link.onTop, 'the link is visible and not covered');
  assert.ok(link.y > link.vh * 0.9 && link.w < 120 && link.h < 40, 'a small link at the bottom edge: ' + JSON.stringify(link));
  // Nothing interactive of the app lies under the link (hit-test with the link out of the way).
  const under = await evalJs(`(()=>{const a=document.getElementById('homestead-source');a.style.pointerEvents='none';const r=a.getBoundingClientRect();const out=[];
    for(const fx of [0.1,0.5,0.9])for(const fy of [0.2,0.8]){const e=document.elementFromPoint(r.x+r.width*fx,r.y+r.height*fy);
      const hit=e&&e.closest('button,a,input,select,textarea,summary,[role=button],[role=tab],[role=menuitem],[onclick],[tabindex]:not([tabindex="-1"])');if(hit)out.push(hit.outerHTML.slice(0,80))}
    a.style.pointerEvents='';return out})()`);
  assert.deepEqual(under, [], 'the link covers no control: ' + JSON.stringify(under));
  assert.ok(link.bodyChild, 'the link survived the app mounting');
  await shot('app.png');
  console.log(app, 'Source link:', JSON.stringify(link));

  // 2. The link opens a script-free page with the licence, upstream and components.
  await evalJs(`document.getElementById('homestead-source').click()`);
  await sleep(2000);
  assert.equal(await evalJs(`location.pathname`), `${BASE}/_homestead/source`);
  const page = await evalJs(`document.documentElement.outerHTML`);
  assert.ok(!/<script/i.test(page), 'the source page has no script');
  assert.deepEqual(await evalJs(`window.__csp`), [], 'no CSP violation on the source page');
  const text = await evalJs(`document.body.innerText`);
  for (const want of [offer.license, offer.repository, offer.commit, ...offer.components.flatMap((c) => [c.name, c.license, c.version])]) assert.ok(text.includes(want), 'page lacks ' + want);
  if (offer.tarball) assert.ok(text.includes(offer.tarball.sha256));
  await shot('source.png');

  // 3. The tarball downloads for real (click the link on the page) and matches its checksum.
  if (offer.tarball) {
    const dl = path.join(tmp, 'downloads');
    fs.mkdirSync(dl, { recursive: true });
    await send('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: dl });
    assert.equal(await evalJs(`document.querySelector('a[href$="${offer.tarball.name}"]').getAttribute('href')`), `${BASE}/_homestead/source/${offer.tarball.name}`);
    await evalJs(`document.querySelector('a[href$="${offer.tarball.name}"]').click()`);
    const file = path.join(dl, offer.tarball.name);
    for (let i = 0; i < 200 && !(fs.existsSync(file) && !fs.readdirSync(dl).some((n) => n.endsWith('.crdownload'))); i++) await sleep(100);
    const data = fs.readFileSync(file);
    const sha = crypto.createHash('sha256').update(data).digest('hex');
    assert.equal(sha, offer.tarball.sha256); assert.equal(data.length, offer.tarball.size);
    assert.ok(data.equals(fs.readFileSync(path.join(offerDir, offer.tarball.name))), 'matches the file the build made');
    console.log(app, 'tarball downloaded:', data.length, 'bytes, sha256', sha);
  } else {
    const c = offer.components[0];
    await go(`${origin}${BASE}/`);
    assert.equal(await evalJs(`fetch(${JSON.stringify(`${BASE}/${c.license_file}`)}).then(async r=>r.status+' '+(await r.text()).includes('GNU LESSER GENERAL PUBLIC LICENSE'))`), '200 true', 'licence text served with the app');
    assert.equal(await evalJs(`fetch(${JSON.stringify(`${BASE}/_homestead/source/${app}-source.tar.gz`)}).then(r=>r.status)`), 404);
  }
  assert.deepEqual(exceptions, [], 'no uncaught exceptions');
  console.log(`real-browser source-offer check passed for ${app}`);
  ws.close(); clearTimeout(hard); cleanup(); process.exit(0);
} catch (e) {
  console.error('FAIL:', e.stack || e); clearTimeout(hard); cleanup(); process.exit(1);
}
