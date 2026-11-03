/*
 * S380: Homestead's service worker for Expenses. It replaces upstream's
 * public/sw.js, which cached index.html (and so the per-session CSRF token the
 * bridge injects into it) and the hosted Puter SDK.
 *
 * Rules: navigations are network-only (offline they get a fixed page with no
 * token and no user data); bridge paths (data, session, login, callback,
 * logout) bypass the worker; the only things ever stored are same-origin,
 * in-scope, content-hashed build assets (assets/<name>-<hash>.js|css), which
 * never change under their name. Expense data and receipts are never cached
 * here: they are read from the appliance on every visit. The version below is filled in by prepare_app.py from the pinned source
 * digest, so a new pin installs a new worker and drops the old cache.
 */
const VERSION = '__VERSION__';
const PREFIX = 'homestead-everything-expense-';
const CACHE = PREFIX + VERSION;
const HASHED_ASSET = /^\.\/assets\/[^/?]+-[A-Za-z0-9_-]{8,}\.(?:js|css|woff2?)$/;
const OFFLINE_PAGE = '<!doctype html><html lang="en"><head><meta charset="utf-8">' +
  '<meta name="viewport" content="width=device-width,initial-scale=1"><title>Offline</title></head>' +
  '<body><h1>You are offline</h1><p>Expenses could not reach your Homestead. ' +
  'Reconnect and reload this page to sign in and continue.</p></body></html>';
self.addEventListener('install', () => { self.skipWaiting(); });
self.addEventListener('activate', (event) => {
  event.waitUntil((async () => {
    const keys = await caches.keys();
    await Promise.all(keys.filter((key) => key.startsWith(PREFIX) && key !== CACHE)
      .map((key) => caches.delete(key)));
    await self.clients.claim();
  })());
});
self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  const scope = new URL(self.registration.scope);
  if (url.origin !== scope.origin || !url.pathname.startsWith(scope.pathname)) return;
  const relative = './' + url.pathname.slice(scope.pathname.length);
  if (relative.startsWith('./_homestead/')) return;
  if (req.mode === 'navigate' || relative === './' || relative === './index.html') {
    event.respondWith(fetch(req).catch(() => new Response(OFFLINE_PAGE, {
      status: 503,
      headers: { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' },
    })));
    return;
  }
  if (url.search || !HASHED_ASSET.test(relative)) return;
  event.respondWith((async () => {
    const cache = await caches.open(CACHE);
    const cached = await cache.match(relative, { ignoreVary: true });
    if (cached) return cached;
    const res = await fetch(req);
    if (res.status === 200 && res.type === 'basic' && !res.redirected) {
      await cache.put(relative, res.clone());
    }
    return res;
  })());
});
