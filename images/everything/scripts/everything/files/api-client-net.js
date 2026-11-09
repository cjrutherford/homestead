// Homestead (S436, ADR-058 section 5): the "relay" transport of API Client.
//
// Upstream's relay speaks raw HTTP/1.1 over Puter's socket relay. The bridge
// has no sockets; it has one network operation, puter.net.fetch, which makes
// the request from the appliance with the public-internet fence (private,
// loopback, link-local and metadata addresses refused), limits and a log.
// This module sends a resolved request through it and returns the shape the
// app's relay code returns. What the bridge does not carry is said plainly in
// the response warnings: redirects are always followed, cookies are neither
// sent nor kept, and only a safe subset of response headers comes back.
import { buildBody, USER_AGENT } from './prepare.js';
import { toBytes, MAX_TIMEOUT_MS } from './http1.js';
import { SendError } from './send.js';

const MAX_REQUEST_BYTES = 2 * 1024 * 1024;
const WARNING_SUBSET = 'Homestead shows only the safe response headers (content type, caching, rate limits and similar); cookies and other headers are not passed on.';

const FAILURES = {
  net_failed: ['That address could not be reached from your Homestead. Only public internet addresses are allowed: your local network, this server and link-local addresses are refused.', 'network'],
  net_invalid: ['The request was not accepted: check the URL (http or https, no user name or password), the method and the headers.', 'bad-url'],
  net_too_large: ['The response is larger than 8 MB.', 'too-large'],
  net_timeout: ['No response from the server in time.', 'timeout'],
  net_rate_limited: ['Too many requests through the relay. Wait a minute and try again.', 'network'],
  net_disabled: ['Sending to other servers is not turned on for this app.', 'relay-unavailable'],
  net_unavailable: ['Sending is unavailable right now. Try again later.', 'network'],
  bridge_unsupported: ['This kind of connection is not supported on Homestead.', 'relay-unavailable'],
};

function classify(err) {
  const code = err?.code || err?.error || '';
  const f = FAILURES[code];
  if (f) return new SendError(f[0], { code: f[1], transport: 'relay', cause: err });
  return new SendError(err?.message || 'Could not get a response through your Homestead.', { code: 'network', transport: 'relay', cause: err });
}

export async function sendViaNet(resolved, { files, signal, timeoutMs = 0, followRedirects: follow = true, netFetch, maxBytes = 100 * 1048576 } = {}) {
  let payload = null;
  let contentType = null;
  try {
    const built = buildBody(resolved.body, files);
    if (built != null) ({ bytes: payload, contentType } = await toBytes(built));
  } catch (err) {
    throw new SendError(err.message, { code: 'body', transport: 'relay' });
  }
  if (payload && payload.length > MAX_REQUEST_BYTES) throw new SendError('The request body is larger than 2 MB.', { code: 'body', transport: 'relay' });
  const headers = {};
  const has = (name) => Object.keys(headers).some((k) => k.toLowerCase() === name);
  for (const h of resolved.headers) {
    if (resolved.body?.mode === 'form' && h.key.toLowerCase() === 'content-type' && /multipart\/form-data/i.test(h.value)) continue;
    const have = Object.keys(headers).find((k) => k.toLowerCase() === h.key.toLowerCase());
    if (have === undefined) headers[h.key] = h.value;
    else headers[have] = `${headers[have]}, ${h.value}`;
  }
  if (contentType && !has('content-type')) headers['Content-Type'] = contentType;
  if (!has('user-agent')) headers['User-Agent'] = USER_AGENT;
  if (payload && ['GET', 'HEAD'].includes(resolved.method)) throw new SendError('GET and HEAD requests cannot have a body here.', { code: 'body', transport: 'relay' });

  const start = performance.now();
  const ctl = new AbortController();
  let timer = 0;
  if (timeoutMs) {
    timer = setTimeout(() => {
      const e = new Error(`No response after ${timeoutMs / 1000} s.`);
      e.name = 'TimeoutError';
      ctl.abort(e);
    }, Math.min(timeoutMs, MAX_TIMEOUT_MS));
  }
  const onAbort = () => ctl.abort(signal.reason);
  if (signal?.aborted) onAbort();
  else signal?.addEventListener('abort', onAbort, { once: true });
  const aborted = new Promise((_, reject) => {
    const fire = () => {
      const r = ctl.signal.reason;
      reject(r?.name === 'TimeoutError' ? new SendError(r.message, { code: 'timeout', transport: 'relay' }) : new SendError('The request was cancelled.', { code: 'aborted', transport: 'relay' }));
    };
    if (ctl.signal.aborted) fire();
    else ctl.signal.addEventListener('abort', fire, { once: true });
  });
  try {
    const res = await Promise.race([
      (async () => {
        const r = await netFetch(resolved.url, { method: resolved.method, headers, body: payload && payload.length ? payload : undefined });
        const first = performance.now() - start;
        const body = new Uint8Array(await r.arrayBuffer());
        return { r, body, first };
      })(),
      aborted,
    ]);
    if (res.body.length > maxBytes) throw new SendError(`The response is larger than ${Math.round(maxBytes / 1048576)} MB.`, { code: 'too-large', transport: 'relay' });
    const list = [...res.r.headers];
    const total = performance.now() - start;
    const warnings = [WARNING_SUBSET];
    if (!follow) warnings.push('Homestead always follows redirects; the final response is shown.');
    return {
      status: res.r.status,
      statusText: res.r.statusText,
      headers: list,
      body: res.body,
      size: { body: res.body.length, headers: list.reduce((n, [k, v]) => n + k.length + v.length + 4, 0) },
      timings: { total, firstByte: res.first, download: total - res.first },
      transport: 'relay',
      url: resolved.url,
      redirects: [],
      sentHeaders: Object.entries(headers),
      warnings,
    };
  } catch (err) {
    if (err instanceof SendError) throw err;
    throw classify(err);
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener('abort', onAbort);
  }
}
