/*
 * S367: cache only versioned static assets, never the app root/index, bridge
 * data or sign-in paths. The bridge injects a per-session CSRF token into
 * index.html and redirects signed-out loads to sign-in, so a stored copy could
 * leak one person's token to another on a shared browser. Navigations are
 * network-only; offline they get a fixed page with no token and no user data.
 */
const VERSION = '__VERSION__';
const ASSETS = __ASSETS__;
// Never precache or store these, whatever the build lists.
const NEVER = (relative) => relative === './' || relative === './index.html' ||
  relative.startsWith('./_homestead/');
const PRECACHE = ASSETS.filter((url) => !NEVER(url));
// S468: with the app container stopped the edge (Caddy) still answers, with a
// 502/503/504 instead of a network error. For a page navigation that counts as
// offline too, so the person sees the offline page, not a bare gateway error.
const edgeDown = (res) => {
  if (res.status === 502 || res.status === 503 || res.status === 504) throw new Error('edge unavailable');
  return res;
};
const OFFLINE_PAGE = '<!doctype html><html lang="en"><head><meta charset="utf-8">' +
  '<meta name="viewport" content="width=device-width,initial-scale=1"><title>Offline</title></head>' +
  '<body><h1>You are offline</h1><p>Tasks could not reach your Homestead. ' +
  'Reconnect and reload this page to sign in and continue.</p></body></html>';
const CACHE = `homestead-everything-tasks-${VERSION}`;
self.addEventListener('install', (event) => {
  event.waitUntil(caches.open(CACHE).then((cache) =>
    cache.addAll(PRECACHE.map((url) => new Request(url, { cache: 'reload' })))));
});
self.addEventListener('activate', (event) => {
  event.waitUntil((async () => {
    const keys = await caches.keys();
    await Promise.all(keys.filter((key) => key.startsWith('homestead-everything-tasks-') && key !== CACHE)
      .map((key) => caches.delete(key)));
    await self.clients.claim();
  })());
});
self.addEventListener('message', (event) => {
  if (event.data === 'skip-waiting') self.skipWaiting();
});
self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  event.waitUntil(self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((list) => {
    const client = list.find((item) => 'focus' in item);
    return client ? client.focus() : self.clients.openWindow('./');
  }));
});
self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  const scope = new URL(self.registration.scope);
  if (url.origin !== scope.origin || !url.pathname.startsWith(scope.pathname)) return;
  const relative = './' + url.pathname.slice(scope.pathname.length);
  // Bridge paths (data, session, login, callback, logout) bypass the worker.
  if (relative.startsWith('./_homestead/')) return;
  if (req.mode === 'navigate' || relative === './' || relative === './index.html') {
    // Network-only. The response (page, sign-in redirect, error) is passed
    // through and never stored.
    event.respondWith(fetch(req).then(edgeDown).catch(() => new Response(OFFLINE_PAGE, {
      status: 503,
      headers: { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' },
    })));
    return;
  }
  // Only a named static precache asset may be cached.
  if (url.search || !PRECACHE.includes(relative)) return;
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
