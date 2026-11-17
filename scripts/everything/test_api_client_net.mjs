// S436: the API Client relay (api-client-net.js, added to the prepared tree as
// src/core/homestead-net.js) sends through puter.net.fetch and maps its
// answers. usage: node test_api_client_net.mjs <prepared src/api-client dir>
import assert from 'node:assert/strict';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const root = process.argv[2];
const { sendViaNet } = await import(pathToFileURL(path.join(root, 'src/core/homestead-net.js')).href);
const { send, SendError } = await import(pathToFileURL(path.join(root, 'src/core/send.js')).href);
const resolved = (over = {}) => ({ method: 'POST', url: 'https://api.example.com/items?q=1', headers: [{ key: 'X-Key', value: 'k' }, { key: 'Accept', value: 'a' }, { key: 'accept', value: 'b' }],
  body: { mode: 'raw', text: '{"a":1}' }, ...over });

// 1. A request goes out as bytes with its headers; the answer keeps status, safe headers and body.
let seen;
const ok = async (url, init) => {
  seen = { url, init };
  return new Response('{"ok":true}', { status: 201, statusText: 'Created', headers: { 'content-type': 'application/json', etag: '"x"' } });
};
const res = await sendViaNet(resolved(), { netFetch: ok });
assert.equal(seen.url, 'https://api.example.com/items?q=1');
assert.equal(seen.init.method, 'POST');
assert.equal(new TextDecoder().decode(seen.init.body), '{"a":1}');
assert.equal(seen.init.headers['X-Key'], 'k');
assert.equal(seen.init.headers.Accept, 'a, b');
assert.match(seen.init.headers['Content-Type'], /text\/plain/);
assert.match(seen.init.headers['User-Agent'], /^HomesteadAPIClient/);
assert.equal(res.status, 201);
assert.equal(res.transport, 'relay');
assert.equal(new TextDecoder().decode(res.body), '{"ok":true}');
assert.ok(res.headers.some(([k, v]) => k === 'content-type' && v === 'application/json'));
assert.ok(res.warnings.some((w) => /safe response headers/.test(w)));
assert.ok(res.warnings.every((w) => !/redirects/.test(w)), 'no redirect warning when following');
assert.ok((await sendViaNet(resolved({ method: 'GET', body: null }), { netFetch: ok, followRedirects: false })).warnings.some((w) => /always follows redirects/.test(w)));

// 2. The bridge's refusals become plain errors, with no raw sockets anywhere.
for (const [code, expect] of [['net_failed', /public internet addresses/], ['net_invalid', /not accepted/], ['net_too_large', /8 MB/], ['net_timeout', /in time/], ['net_rate_limited', /Too many/], ['net_disabled', /not turned on/]]) {
  const failing = async () => { const e = new Error('x'); e.code = code; throw e; };
  await assert.rejects(sendViaNet(resolved(), { netFetch: failing }), (e) => e instanceof SendError && expect.test(e.message) && e.transport === 'relay', code);
}
await assert.rejects(sendViaNet(resolved({ method: 'GET' }), { netFetch: ok }), /cannot have a body/);
await assert.rejects(sendViaNet(resolved({ body: { mode: 'raw', text: 'x'.repeat(2 * 1024 * 1024 + 1) } }), { netFetch: ok }), /larger than 2 MB/);

// 3. A timeout and a cancel stop waiting.
const hang = () => new Promise(() => {});
await assert.rejects(sendViaNet(resolved({ method: 'GET', body: null }), { netFetch: hang, timeoutMs: 30 }), (e) => e.code === 'timeout');
const ctl = new AbortController();
setTimeout(() => ctl.abort(), 20);
await assert.rejects(sendViaNet(resolved({ method: 'GET', body: null }), { netFetch: hang, signal: ctl.signal }), (e) => e.code === 'aborted');

// 4. send() on Homestead always takes the relay; the opener has no sockets.
globalThis.puter = { env: 'app' };
let called = 0;
const opener = Object.assign(() => { throw new Error('no sockets'); }, { netFetch: async (...a) => { called++; return ok(...a); } });
const viaSend = await send(resolved(), { mode: 'browser', relay: { openSocket: opener }, relayHosts: new Set() });
assert.equal(viaSend.transport, 'relay');
assert.equal(called, 1);
const { puterSockets } = await import(pathToFileURL(path.join(root, 'src/core/send.js')).href);
assert.throws(() => puterSockets({ net: { fetch() {} } })('h', 1, false), /Raw sockets/);
console.log('api-client net relay ok');
