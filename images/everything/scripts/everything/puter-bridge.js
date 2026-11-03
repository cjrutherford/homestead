/*
 * Homestead replacement for the Puter browser SDK (S367 spike, ADR-053).
 *
 * Exposes only the `puter` surface the pinned Tasks revision uses, backed by
 * same-origin HTTP calls to the Homestead Everything bridge. It never talks to
 * Puter cloud. KV sharing (kv.share/unshare/listShared/listSharedByMe, the
 * {handle} option, perms.request, appID) and file sharing (fs.share,
 * fs.unshare, fs.getShares, fs.listShared, ~shared/<id>/... paths) follow
 * ADR-058. Read links (fs.getReadURL) mint an expiring, read-only URL on the
 * page's own origin. Events (events.onLocal over a Server-Sent Events stream,
 * ADR-058 section 6) need EventSource. net.fetch (ADR-058 section 5) goes
 * through the bridge's fenced client (public internet only, limited, logged)
 * and answers net_disabled unless the bridge runs with -net. Anything outside ADR-053 and ADR-058
 * is either absent or rejects
 * with { code: 'bridge_unsupported' }; it never silently succeeds.
 *
 * Config (all optional) from globalThis.__HOMESTEAD_BRIDGE__:
 *   endpoint  bridge URL (default ./_homestead/data resolved against the page)
 *   csrf      CSRF token (default: <meta name="homestead-csrf">). The token is
 *             refreshed from GET ./_homestead/session (no-store) when the
 *             bridge refuses a request, then the request is retried once.
 *   sessionEndpoint  override for that refresh URL
 *   origin    Origin header to send (non-browser harnesses only)
 *   headers   extra request headers (non-browser harnesses only, e.g. Cookie)
 *   signInUrl / signOutUrl  where the Homestead edge sign-in lives
 */
(function (root) {
  'use strict';

  var MAX_VALUE = 8 * 1024 * 1024;
  var enc = new TextEncoder();
  var dec = new TextDecoder();

  function fail(code, message, status) {
    var err = { code: code, message: message || code };
    if (status) err.status = status;
    throw err;
  }
  // '30m', '12h', '7d', '2w', '1y' or a number of seconds; 0 means the default.
  function ttlSeconds(v) {
    if (v === undefined || v === null || v === '') return 0;
    if (typeof v === 'number') {
      if (!isFinite(v) || v < 0) fail('invalid_argument', 'Invalid expiry');
      return Math.floor(v);
    }
    var m = /^(\d{1,5})([smhdwy])$/.exec(String(v));
    if (!m) fail('invalid_argument', 'Invalid expiry');
    var unit = { s: 1, m: 60, h: 3600, d: 86400, w: 604800, y: 31536000 }[m[2]];
    return Number(m[1]) * unit;
  }

  function toBase64(bytes) {
    var s = '';
    for (var i = 0; i < bytes.length; i += 0x8000) {
      s += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
    }
    return btoa(s);
  }
  function fromBase64(b64) {
    var s = atob(b64 || '');
    var out = new Uint8Array(s.length);
    for (var i = 0; i < s.length; i++) out[i] = s.charCodeAt(i);
    return out;
  }

  function create(config) {
    config = config || {};
    function endpoint() {
      if (config.endpoint) return config.endpoint;
      var base = (root.document && root.document.baseURI) || (root.location && root.location.href);
      return new URL('./_homestead/data', base).href;
    }
    var freshCsrf = '';
    function csrf() {
      if (freshCsrf) return freshCsrf;
      if (config.csrf) return config.csrf;
      var m = root.document && root.document.querySelector('meta[name="homestead-csrf"]');
      return (m && m.content) || '';
    }
    function sessionEndpoint() {
      if (config.sessionEndpoint) return config.sessionEndpoint;
      if (config.endpoint) return config.endpoint.replace(/data$/, 'session');
      var base = (root.document && root.document.baseURI) || (root.location && root.location.href);
      return new URL('./_homestead/session', base).href;
    }
    function toSignIn() {
      if (!root.document || !root.location) return;
      var url = config.signInUrl || new URL('./_homestead/login', root.document.baseURI).href;
      root.location.assign(url);
    }
    // Fetch a fresh token from the authenticated session endpoint. Never
    // cached (cache: 'no-store'). Returns true if a token was obtained.
    async function refreshCsrf(body) {
      var headers = Object.assign({}, config.headers || {});
      if (config.origin) headers.Origin = config.origin;
      var res;
      try {
        res = await fetch(sessionEndpoint(), {
          method: 'GET', headers: headers, credentials: 'same-origin', redirect: 'error', cache: 'no-store',
        });
      } catch (e) {
        fail('network_error', 'Could not reach the Homestead bridge');
      }
      if (res.status === 401) {
        freshCsrf = '';
        if (body.operation !== 'whoami') toSignIn();
        fail('auth_required', 'Not signed in', 401);
      }
      if (!res.ok) return false;
      var info = await res.json();
      if (!info || !info.csrf) return false;
      freshCsrf = info.csrf;
      return true;
    }

    // A random id for this page instance. Writes carry it so the bridge can
    // tell the writer's own stream (`self`) from a second tab (ADR-058).
    var clientId = (function () {
      var b = new Uint8Array(18);
      if (root.crypto && root.crypto.getRandomValues) root.crypto.getRandomValues(b);
      else for (var i = 0; i < b.length; i++) b[i] = Math.floor(Math.random() * 256);
      return toBase64(b).replace(/\+/g, '-').replace(/\//g, '_');
    })();
    function eventsEndpoint() {
      if (config.eventsEndpoint) return config.eventsEndpoint;
      if (config.endpoint) return config.endpoint.replace(/data$/, 'events');
      var base = (root.document && root.document.baseURI) || (root.location && root.location.href);
      return new URL('./_homestead/events', base).href;
    }

    async function send(body) {
      if (body.kind === 'kv' && (body.operation === 'put' || body.operation === 'delete' || body.operation === 'putbatch')) {
        body.client = clientId;
      }
      var headers = Object.assign({}, config.headers || {}, {
        'Content-Type': 'application/json',
        'X-Homestead-CSRF': csrf(),
      });
      if (config.origin) headers.Origin = config.origin;
      try {
        return await fetch(endpoint(), {
          method: 'POST',
          headers: headers,
          body: JSON.stringify(body),
          credentials: 'same-origin',
          redirect: 'error',
          cache: 'no-store',
        });
      } catch (e) {
        // Offline or unreachable: Tasks' offline queue relies on this reject.
        fail('network_error', 'Could not reach the Homestead bridge');
      }
    }

    // The bridge answers sharing failures as JSON { error: code }; throw that
    // code. Anything else (plain-text refusals) is handled by call().
    async function jsonError(res) {
      if (res.status < 400 || res.status === 401) return;
      var ct = (res.headers && res.headers.get && res.headers.get('content-type')) || '';
      if (ct.indexOf('application/json') === -1) return;
      var j = null;
      try { j = await res.json(); } catch (e) { return; }
      if (j && j.error) fail(String(j.error), String(j.message || j.error), res.status);
    }

    async function call(body) {
      var res = await send(body);
      await jsonError(res);
      if (res.status === 401 || res.status === 403) {
        // The page-load token may be stale (new session, restored tab).
        if (await refreshCsrf(body)) {
          res = await send(body);
          await jsonError(res);
          // A session that is valid but too old for a shared operation
          // (ADR-058: 24 h): sign in again; silent if Authentik still allows.
          if (res.status === 401 && body.operation !== 'whoami') toSignIn();
        }
      }
      if (res.status === 404) return null;
      if (res.status === 401) fail('auth_required', 'Not signed in', 401);
      if (res.status === 403) fail('forbidden', 'Request refused by the bridge', 403);
      if (res.status === 409) fail('item_with_same_name_exists', 'A file with that name already exists', 409);
      if (res.status === 413) fail('quota_exceeded', 'Your storage quota for this app is full', 413);
      if (!res.ok) {
        fail(res.status === 400 ? 'bridge_rejected' : 'bridge_error',
          res.status === 400 ? 'Request rejected (invalid or storage limit exceeded)' : 'Bridge storage unavailable',
          res.status);
      }
      return res.json();
    }

    function encodeValue(v) {
      var bytes = enc.encode(JSON.stringify(v === undefined ? null : v));
      if (bytes.length > MAX_VALUE) fail('storage_limit', 'storage limit exceeded');
      return toBase64(bytes);
    }
    // The {handle} option of kv calls (ADR-058): keys are relative to the
    // shared prefix and the bridge proves the handle belongs to the caller.
    function handleOf(opts) {
      if (!opts || opts.handle === undefined || opts.handle === null) return '';
      if (typeof opts.handle !== 'string' || !opts.handle) fail('kv_handle_not_found', 'Unknown share handle');
      return opts.handle;
    }
    function withHandle(body, handle) {
      if (handle) body.handle = handle;
      return body;
    }
    var appId = config.appID || '';
    function learnApp(app) { if (!appId && app) appId = 'app-' + app; }
    function fsKey(path) {
      // Paths are relative to the person's own space for this app; leading and
      // trailing slashes are cosmetic. The bridge rejects anything that is
      // not a clean relative path (no .., no empty parts, no backslashes).
      return String(path).replace(/^\/+/, '').replace(/\/+$/, '');
    }
    function fileItem(dir, info) {
      var path = dir ? dir + '/' + info.name : info.name;
      return { path: path, name: info.name, size: info.size || 0, modified: info.modified || 0,
        is_dir: !!info.is_dir, isDirectory: !!info.is_dir };
    }
    function recipientName(r) {
      if (typeof r === 'string') return r;
      if (r && typeof r.username === 'string') return r.username;
      // The bridge holds no email addresses.
      fail('invalid_recipient', 'Share with a Homestead username, not an email address');
    }
    function noFile(what) {
      fail('subject_does_not_exist', 'No such ' + what);
    }

    var kv = {
      async get(key, opts) {
        var r = await call(withHandle({ operation: 'get', kind: 'kv', key: String(key) }, handleOf(opts)));
        return r === null ? null : JSON.parse(dec.decode(fromBase64(r.value)));
      },
      async set(key, value, opts) {
        if (Array.isArray(key)) {
          var batchHandle = handleOf(value);
          if (!key.length) return true;
          await call(withHandle({
            operation: 'putbatch', kind: 'kv',
            entries: key.map(function (e) { return { key: String(e.key), value: encodeValue(e.value) }; }),
          }, batchHandle));
          return true;
        }
        await call(withHandle({ operation: 'put', kind: 'kv', key: String(key), value: encodeValue(value) }, handleOf(opts)));
        return true;
      },
      async del(key, opts) {
        await call(withHandle({ operation: 'delete', kind: 'kv', key: String(key) }, handleOf(opts)));
        return true;
      },
      async list(pattern, returnValues, opts) {
        if (typeof pattern === 'boolean') { opts = returnValues; returnValues = pattern; pattern = '*'; }
        pattern = pattern === undefined ? '*' : String(pattern);
        var handle = handleOf(opts);
        var star = pattern.indexOf('*');
        if (star !== -1 && star !== pattern.length - 1) fail('bridge_unsupported', 'Only trailing-* key patterns are supported');
        var prefix = star === -1 ? pattern : pattern.slice(0, -1);
        if (returnValues && star !== -1) {
          // Key-value pairs in bounded pages (S372): works for address books
          // longer than the 1000-key list limit and needs one request per page,
          // not one per key. A page's "next" is the cursor for the following one.
          var out = [];
          var after = '';
          for (var pages = 0; pages < 100000; pages++) {
            var page = await call(withHandle({ operation: 'listvalues', kind: 'kv', key: prefix, after: after }, handle));
            var entries = (page && page.entries) || [];
            for (var i = 0; i < entries.length; i++) {
              out.push({ key: entries[i].key, value: JSON.parse(dec.decode(fromBase64(entries[i].value))) });
            }
            if (!page || !page.next) return out;
            after = page.next;
          }
          fail('bridge_error', 'Key list did not end');
        }
        var r = await call(withHandle({ operation: 'list', kind: 'kv', key: prefix }, handle));
        var keys = (r && r.keys) || [];
        if (star === -1) keys = keys.filter(function (k) { return k === prefix; });
        if (!returnValues) return keys;
        return Promise.all(keys.map(async function (k) { return { key: k, value: await kv.get(k, handle ? { handle: handle } : undefined) }; }));
      },
      // ADR-058 KV sharing. Recipients are Homestead people of this app who
      // have signed in; access is 'read' or 'write' ('manage' is refused).
      async share(args) {
        args = args || {};
        var r = await call({ operation: 'kv.share', prefix: String(args.prefix === undefined ? '' : args.prefix),
          recipient: String(args.recipient === undefined || args.recipient === null ? '' : args.recipient),
          access: String(args.access === undefined ? 'write' : args.access),
          label: args.label === undefined || args.label === null ? '' : String(args.label) });
        if (r && r.appUid && !appId) appId = r.appUid;
        return r;
      },
      async unshare(handle) {
        await call({ operation: 'kv.unshare', handle: String(handle === undefined || handle === null ? '' : handle) });
        return true;
      },
      async listShared(opts) { return sharedPage('kv.listShared', opts); },
      async listSharedByMe(opts) { return sharedPage('kv.listSharedByMe', opts); },
    };
    async function sharedPage(operation, opts) {
      opts = opts || {};
      var body = { operation: operation };
      if (opts.limit !== undefined && opts.limit !== null) body.limit = Number(opts.limit) || 0;
      if (opts.cursor) body.cursor = String(opts.cursor);
      var r = await call(body);
      var out = { items: (r && r.items) || [] };
      if (r && r.cursor) out.cursor = r.cursor;
      return out;
    }

    // perms.request maps to the sharing permission, shows no prompt and keeps
    // no state: the share click in the app is the consent (ADR-058).
    var perms = {
      Resource: { KV_SHARE: 'kvShare' },
      async request(resource, args) {
        args = args || {};
        var body;
        if (resource === 'kvShare') {
          body = { operation: 'perms.request', resource: 'kvShare', prefix: String(args.prefix === undefined ? '' : args.prefix),
            access: String(args.access === undefined ? '' : args.access) };
        } else if (resource === 'permission') {
          body = { operation: 'perms.request', resource: 'permission', permission: String(args.permission === undefined ? '' : args.permission) };
        } else {
          fail('invalid_argument', 'Unsupported permission request');
        }
        var r = await call(body);
        return !!(r && r.granted);
      },
    };

    var fs = {
      // Options such as createMissingParents are implied: folders exist while
      // a file is beneath them. Writes replace the file and are counted
      // against the person's and the app's storage quota.
      async write(path, data) {
        var bytes;
        if (typeof data === 'string') bytes = enc.encode(data);
        else if (data && typeof data.arrayBuffer === 'function') bytes = new Uint8Array(await data.arrayBuffer());
        else bytes = enc.encode(String(data));
        if (bytes.length > MAX_VALUE) fail('storage_limit', 'storage limit exceeded');
        var key = fsKey(path);
        var r = await call({ operation: 'put', kind: 'file', key: key, value: toBase64(bytes) });
        // A write through a share that was revoked (or never existed) answers
        // 404; do not report a save that did not happen.
        if (r === null) noFile('file');
        var name = key.split('/').pop();
        return { path: path, name: name, size: (r && r.size) || bytes.length, modified: (r && r.modified) || 0, is_dir: false };
      },
      async read(path) {
        var r = await call({ operation: 'get', kind: 'file', key: fsKey(path) });
        if (r === null) noFile('file');
        return new Blob([fromBase64(r.value)]);
      },
      async stat(path) {
        var r = await call({ operation: 'stat', kind: 'file', key: fsKey(path) });
        if (r === null) noFile('file or directory');
        var item = fileItem('', r);
        item.path = path;
        return item;
      },
      // Direct children of a folder: [{ name, path, size, modified, is_dir }].
      async readdir(path) {
        var dir = fsKey(path);
        var r = await call({ operation: 'readdir', kind: 'file', key: dir });
        if (r === null) noFile('directory');
        return ((r && r.entries) || []).map(function (e) { return fileItem(dir, e); });
      },
      // Renames a file within its folder (S375): newName is a bare name, never
      // a path, and an existing file is never replaced.
      async rename(path, newName) {
        var key = fsKey(path);
        var name = String(newName);
        if (!name || name.indexOf('/') !== -1 || name === '.' || name === '..') fail('bridge_rejected', 'Invalid file name', 400);
        var slash = key.lastIndexOf('/');
        var to = (slash === -1 ? '' : key.slice(0, slash + 1)) + name;
        var r = await call({ operation: 'rename', kind: 'file', key: key, to: to });
        if (r === null) noFile('file');
        return { path: to, name: name, size: (r && r.size) || 0, modified: (r && r.modified) || 0, is_dir: false };
      },
      // { recursive: true } removes a folder and everything under it. Missing
      // paths are not an error.
      async delete(path, opts) {
        var recursive = !!(opts && opts.recursive);
        await call({ operation: recursive ? 'deletetree' : 'delete', kind: 'file', key: fsKey(path) });
      },
      // ADR-058 file sharing. Recipients are Homestead people of this app who
      // have signed in, given as {username} or a bare name; {email} is refused.
      // mode is 'read' or 'write' ('manage' is refused). Resolves
      // [{ isNew, holder, mode }] as Circuits and Parametric expect.
      async share(path, recipient, mode) {
        var r = await call({ operation: 'fs.share', key: fsKey(path), recipient: recipientName(recipient),
          access: String(mode === undefined || mode === null ? 'read' : mode) });
        return [{ isNew: !!(r && r.isNew), holder: r && r.holder, mode: r && r.mode }];
      },
      async unshare(path, recipient) {
        var r = await call({ operation: 'fs.unshare', key: fsKey(path), recipient: recipientName(recipient) });
        return { revoked: (r && r.revoked) || 0 };
      },
      // [{ holder, mode, inheritedFrom }] for a path the caller owns.
      async getShares(path) {
        var r = await call({ operation: 'fs.getShares', key: fsKey(path) });
        return ((r && r.items) || []).map(function (i) {
          return { holder: i.holder, mode: i.mode, inheritedFrom: i.inheritedFrom || null };
        });
      },
      // { items: [{ path, name, owner, mode, modified, size, is_dir }], cursor }
      // Items' paths are ~shared/<id>[/<name>] and work with read/write/stat/
      // readdir/rename/delete.
      async listShared(opts) {
        var page = await sharedPage('fs.listShared', opts);
        page.items = page.items.map(function (i) {
          i.isDir = !!i.is_dir;
          return i;
        });
        return page;
      },
      // A read-only, expiring link to one of the caller's own files (ADR-058).
      // expiresIn is seconds or a string such as '1d', '7d', '30d', '365d',
      // '1y'; the bridge clamps it to its maximum. The URL is on the origin
      // the page is using and serves the file's current content until it
      // expires or the file is deleted.
      async getReadURL(path, expiresIn) {
        var r = await call({ operation: 'fs.getReadURL', key: fsKey(path), ttl: ttlSeconds(expiresIn) });
        if (!r || !r.url) fail('bridge_error', 'Bridge storage unavailable', 500);
        return r.url;
      },
    };

    var user = null;
    var auth = {
      async isSignedIn() {
        try { await auth.getUser(); return true; } catch (e) {
          if (e && e.code === 'auth_required') return false;
          throw e;
        }
      },
      async getUser() {
        var r = await call({ operation: 'whoami' });
        if (!r) fail('auth_required', 'Not signed in', 401);
        learnApp(r.app);
        user = { username: r.username || 'Homestead user', uuid: r.uuid };
        return user;
      },
      async signIn() {
        // The only sign-in is Homestead's (Authentik) page; leave for it. The
        // promise never settles because the page is going away.
        if (root.document && root.location) { toSignIn(); return new Promise(function () {}); }
        fail('bridge_unsupported', 'Sign in through the Homestead sign-in page');
      },
      async signOut() {
        // POST to the bridge's logout with a fresh CSRF token, then return
        // to the app (which sends a signed-out visitor to sign-in).
        if (!root.document || !root.location) fail('bridge_unsupported', 'Sign out through Homestead');
        var url = config.signOutUrl || new URL('./_homestead/logout', root.document.baseURI).href;
        if (await refreshCsrf({ operation: 'signOut' })) {
          var headers = Object.assign({}, config.headers || {}, { 'X-Homestead-CSRF': csrf() });
          if (config.origin) headers.Origin = config.origin;
          try {
            await fetch(url, { method: 'POST', headers: headers, credentials: 'same-origin', redirect: 'error', cache: 'no-store' });
          } catch (e) {
            fail('network_error', 'Could not reach the Homestead bridge');
          }
          freshCsrf = '';
        }
        root.location.assign(new URL('./', root.document.baseURI).href);
      },
    };

    // appID is the constant app-<app>; it is known once the bridge has said which app this
    // is (the first whoami or share reply, or config.appID).

    // ADR-058 events: one EventSource per page, a subscription per distinct
    // subject, callbacks fanned out locally. Values never arrive; `gap` means
    // "re-read" and is also delivered after every reconnect.
    var ev = { source: null, ready: false, everReady: false, waiters: [], subs: new Map() };
    function evFail(entry, err) {
      entry.cbs.forEach(function (c) { if (c.onError) { try { c.onError(err); } catch (e) { /* ignore */ } } });
    }
    function evGapAll() {
      ev.subs.forEach(function (entry, subject) {
        entry.cbs.forEach(function (c) { try { c.cb({ event: { op: 'gap', subject: subject } }); } catch (e) { /* ignore */ } });
      });
    }
    function evRemote(entry, subject) {
      return call({ operation: 'events.subscribe', client: clientId, topic: subject });
    }
    function evClose() {
      if (ev.source) { try { ev.source.close(); } catch (e) { /* ignore */ } }
      ev.source = null; ev.ready = false; ev.everReady = false;
    }
    function evOpen() {
      if (ev.source) return;
      var ES = root.EventSource;
      if (typeof ES !== 'function') fail('bridge_unsupported', 'Events need EventSource');
      var src = new ES(eventsEndpoint() + '?client=' + encodeURIComponent(clientId), { withCredentials: true });
      ev.source = src;
      src.addEventListener('ready', function () {
        if (ev.source !== src) return;
        var again = ev.everReady;
        ev.ready = true; ev.everReady = true;
        var w = ev.waiters; ev.waiters = [];
        w.forEach(function (f) { f(); });
        if (again) {
          // A resumed stream has no subscriptions server-side: renew, then re-read.
          ev.subs.forEach(function (entry, subject) {
            evRemote(entry, subject).catch(function (e) {
              if (ev.subs.get(subject) === entry) { ev.subs.delete(subject); evFail(entry, e); }
            });
          });
          evGapAll();
        }
      });
      src.addEventListener('gap', function () { if (ev.source === src) evGapAll(); });
      src.addEventListener('msg', function (m) {
        if (ev.source !== src) return;
        var d;
        try { d = JSON.parse(m.data); } catch (e) { return; }
        var entry = d && ev.subs.get(d.subject);
        if (!entry) return;
        entry.cbs.forEach(function (c) { try { c.cb({ event: d.event }); } catch (e) { /* ignore */ } });
      });
      src.onerror = function () {
        if (ev.source !== src) return;
        ev.ready = false;
        if (src.readyState === 2) { // the bridge refused the stream: give up, callers poll
          var all = Array.from(ev.subs.values());
          ev.subs.clear();
          evClose();
          var w = ev.waiters; ev.waiters = [];
          w.forEach(function (f) { f(new Error('closed')); });
          all.forEach(function (entry) { evFail(entry, { code: 'network_error', message: 'The event stream is closed' }); });
        }
      };
    }
    function evReady() {
      if (ev.ready) return Promise.resolve();
      return new Promise(function (resolve, reject) {
        var t = setTimeout(function () { reject({ code: 'network_error', message: 'The event stream did not start' }); }, 15000);
        ev.waiters.push(function (err) { clearTimeout(t); if (err) reject({ code: 'network_error', message: 'The event stream is closed' }); else resolve(); });
      });
    }
    var events = {
      async onLocal(subject, cb, opts) {
        if (typeof subject !== 'string' || !subject || typeof cb !== 'function') fail('invalid_argument', 'events.onLocal needs a subject and a callback');
        evOpen();
        var sub = { cb: cb, onError: opts && typeof opts.onError === 'function' ? opts.onError : null };
        var entry = ev.subs.get(subject);
        if (!entry) {
          entry = { cbs: new Set(), pending: null };
          ev.subs.set(subject, entry);
          entry.pending = evReady().then(function () { return evRemote(entry, subject); });
        }
        entry.cbs.add(sub);
        try {
          await entry.pending;
        } catch (e) {
          entry.cbs.delete(sub);
          if (!entry.cbs.size && ev.subs.get(subject) === entry) { ev.subs.delete(subject); if (!ev.subs.size) evClose(); }
          throw e;
        }
        return {
          off: function () {
            if (!entry.cbs.delete(sub) || entry.cbs.size || ev.subs.get(subject) !== entry) return;
            ev.subs.delete(subject);
            if (ev.ready) call({ operation: 'events.unsubscribe', client: clientId, topic: subject }).catch(function () {});
            if (!ev.subs.size) evClose();
          },
        };
      },
    };
    events.on = events.onLocal;

    // net.fetch (ADR-058 section 5): the bridge makes the request. Only
    // fetch exists; raw sockets, TLS sockets and WebSocket/wisp stay
    // bridge_unsupported. Bodies are bytes (base64 on the wire).
    var NET_MAX_REQUEST = 2 * 1024 * 1024;
    var NET_METHODS = { GET: 1, HEAD: 1, POST: 1, PUT: 1, PATCH: 1, DELETE: 1, OPTIONS: 1 };
    async function netBytes(body) {
      if (body === undefined || body === null) return null;
      if (typeof body === 'string') return enc.encode(body);
      if (body instanceof ArrayBuffer) return new Uint8Array(body);
      if (ArrayBuffer.isView(body)) return new Uint8Array(body.buffer, body.byteOffset, body.byteLength);
      if (typeof root.URLSearchParams === 'function' && body instanceof root.URLSearchParams) return enc.encode(body.toString());
      if (typeof root.Blob === 'function' && body instanceof root.Blob) return new Uint8Array(await body.arrayBuffer());
      return fail('bridge_unsupported', 'net.fetch bodies must be text or bytes');
    }
    function netHeaders(h) {
      var out = {};
      if (!h) return out;
      var add = function (k, v) { k = String(k); out[k] = Object.prototype.hasOwnProperty.call(out, k) ? out[k] + ', ' + String(v) : String(v); };
      if (typeof h.forEach === 'function' && !Array.isArray(h)) h.forEach(function (v, k) { add(k, v); });
      else if (Array.isArray(h)) h.forEach(function (kv) { add(kv[0], kv[1]); });
      else Object.keys(h).forEach(function (k) { add(k, h[k]); });
      return out;
    }
    var net = {
      async fetch(input, init) {
        init = init || {};
        var url = input && typeof input === 'object' && 'url' in input ? input.url : input;
        var src = input && typeof input === 'object' && 'url' in input ? input : {};
        url = String(url);
        if (/^(wss?|wisp):/i.test(url)) fail('bridge_unsupported', 'WebSocket is not supported');
        var method = String(init.method || src.method || 'GET').toUpperCase();
        if (!NET_METHODS[method]) fail('invalid_argument', 'Unsupported method');
        var headers = Object.assign(netHeaders(src.headers), netHeaders(init.headers));
        var bytes = await netBytes(init.body);
        if (bytes && bytes.length > NET_MAX_REQUEST) fail('net_invalid', 'Request body is too large');
        var req = { operation: 'net.fetch', url: url, method: method, headers: headers };
        if (bytes && bytes.length) req.body = toBase64(bytes);
        var r = await call(req);
        if (!r || typeof r.status !== 'number') fail('bridge_error', 'Bridge storage unavailable', 500);
        var data = fromBase64(r.body);
        var nullBody = method === 'HEAD' || r.status === 204 || r.status === 205 || r.status === 304;
        return new root.Response(nullBody ? null : data, { status: r.status, statusText: r.statusText || '', headers: r.headers || {} });
      },
      Socket: function () { fail('bridge_unsupported', 'Raw sockets are not supported'); },
      tls: { TLSSocket: function () { fail('bridge_unsupported', 'TLS sockets are not supported'); } },
    };
    var sdk = { env: 'app', kv: kv, fs: fs, auth: auth, perms: perms, events: events, net: net };
    Object.defineProperty(sdk, 'appID', { enumerable: true, get: function () { return appId || undefined; } });
    return sdk;
  }

  root.createHomesteadPuter = create;
  if (typeof root.puter === 'undefined' && !root.__HOMESTEAD_BRIDGE_NO_AUTOINSTALL__) {
    root.puter = create(root.__HOMESTEAD_BRIDGE__);
  }
})(globalThis);
