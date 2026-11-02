/* S367 prototype: cache only the versioned static shell, never bridge data. */
const VERSION = '__VERSION__';
const ASSETS = __ASSETS__;
const CACHE = `homestead-everything-tasks-${VERSION}`;
self.addEventListener('install', (event) => {
  event.waitUntil(caches.open(CACHE).then((cache) =>
    cache.addAll(ASSETS.map((url) => new Request(url, { cache: 'reload' })))));
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
  // Only a navigation to the app root, or a named static precache asset,
  // may be cached. Bridge responses and arbitrary URLs bypass the worker.
  const isRoot = req.mode === 'navigate' && (relative === './' || relative === './index.html');
  if (!isRoot && (url.search || !ASSETS.includes(relative) || relative === './')) return;
  event.respondWith((async () => {
    const cache = await caches.open(CACHE);
    const cached = await cache.match(isRoot ? './' : relative, { ignoreVary: true });
    return cached || fetch(req);
  })());
});
