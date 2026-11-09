// Headless Chrome smoke check of docs/reviews/acceptance-form.html loaded as file://.
// Sets one step FAIL, fills an issue, builds the Markdown export, checks the
// issue text appears, reloads and checks persistence. Zero dependencies: Chrome
// over the DevTools protocol with Node's WebSocket.
// usage: node browser-check-acceptance-form.mjs <path-to-acceptance-form.html>
// Exit 0 pass; exit 0 with "SKIP:" if no browser; exit 1 on failure.
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import net from 'node:net';
import assert from 'node:assert/strict';

const file = path.resolve(process.argv[2] || 'docs/reviews/acceptance-form.html');
const candidates = [process.env.CHROME, 'google-chrome', 'chromium', 'chromium-browser'].filter(Boolean);
let chrome = null;
for (const c of candidates) { try { chrome = execFileSync('which', [c]).toString().trim(); break; } catch {} }
if (!chrome) { console.log('SKIP: no Chrome/Chromium on PATH (set CHROME to run the acceptance form check)'); process.exit(0); }

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const port = await new Promise((res, rej) => { const s = net.createServer(); s.once('error', rej); s.listen(0, '127.0.0.1', () => { const { port } = s.address(); s.close(() => res(port)); }); });
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'acceptance-form-'));
const proc = spawn(chrome, ['--headless=new', '--no-sandbox', '--disable-gpu', `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`, 'about:blank'], { stdio: 'ignore' });
let ws, id = 0; const pending = new Map();
const cleanup = () => { try { ws?.close(); } catch {} proc.kill('SIGKILL'); fs.rmSync(profile, { recursive: true, force: true }); };
try {
  let target;
  for (let i = 0; i < 60 && !target; i++) {
    try { const list = await (await fetch(`http://127.0.0.1:${port}/json`)).json(); target = list.find((t) => t.type === 'page'); } catch {}
    if (!target) await sleep(250);
  }
  assert.ok(target, 'Chrome did not start');
  ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  ws.onmessage = (m) => { const d = JSON.parse(m.data); if (d.id && pending.has(d.id)) { pending.get(d.id)(d); pending.delete(d.id); } };
  const send = (method, params = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })); });
  const ev = async (expr) => {
    const r = await send('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true });
    if (r.result?.exceptionDetails) throw new Error(JSON.stringify(r.result.exceptionDetails).slice(0, 400));
    return r.result.result.value;
  };
  const load = async () => { await send('Page.navigate', { url: 'file://' + file }); await sleep(1200); };
  await send('Page.enable'); await send('Runtime.enable');
  await load();

  const count = await ev(`document.querySelectorAll('article.step').length`);
  assert.ok(count > 100, 'steps rendered: ' + count);
  const first = await ev(`document.querySelector('article.step .sid').textContent`);
  const typed = 'SMOKE-ISSUE-TEXT <b>not html</b>';
  await ev(`(() => {
    const art = document.getElementById('step-${first}');
    const set = (node, v, evn) => { node.value = v; node.dispatchEvent(new Event(evn, { bubbles: true })); };
    set(art.querySelector('[data-f=result]'), 'fail', 'change');
    const issue = art.querySelector('.issue');
    if (!issue) throw new Error('issue panel did not open');
    set(issue.querySelector('[data-f=severity]'), 'Major', 'change');
    set(issue.querySelector('[data-f=actual]'), ${JSON.stringify(typed)}, 'input');
    set(issue.querySelector('[data-f=url]'), 'https://example.test/x', 'input');
    set(art.querySelector('[data-f=notes]'), 'smoke note', 'input');
    document.querySelector('[data-run=tester]').value = 'Smoke Tester';
    document.querySelector('[data-run=tester]').dispatchEvent(new Event('input', { bubbles: true }));
    return true; })()`);
  assert.equal(await ev(`document.querySelectorAll('#step-${first} b').length`), 0, 'owner text must not become markup');
  let md = await ev(`window.acceptanceForm.markdown()`);
  assert.ok(md.includes(typed), 'issue text in export');
  assert.ok(md.includes('Major'), 'severity in export');
  assert.ok(md.includes('Smoke Tester'), 'run info in export');
  assert.ok(md.includes(first), 'step id in export');
  await ev(`window.acceptanceForm.saveNow()`);
  await load();
  md = await ev(`window.acceptanceForm.markdown()`);
  assert.ok(md.includes(typed), 'issue persisted across reload');
  assert.equal(await ev(`document.querySelector('#step-${first} [data-f=result]').value`), 'fail');
  assert.equal(await ev(`document.querySelector('[data-run=tester]').value`), 'Smoke Tester');
  const json = JSON.parse(await ev(`window.acceptanceForm.json()`));
  assert.equal(json.steps[first].result, 'fail');
  console.log('PASS: acceptance form smoke check (' + count + ' steps)');
  cleanup();
} catch (e) {
  console.error('FAIL:', e.message);
  cleanup();
  process.exit(1);
}
