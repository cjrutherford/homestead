/*
 * Homestead replacement for the Puter browser SDK (S367 spike, ADR-053).
 *
 * Exposes only the `puter` surface the pinned Tasks revision uses, backed by
 * same-origin HTTP calls to the Homestead Everything bridge. It never talks to
 * Puter cloud. Anything outside ADR-053 (sharing, collaboration, permissions,
 * events, signed read URLs) is either absent or rejects with
 * { code: 'bridge_unsupported' }; it never silently succeeds.
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
  function unsupported(what) {
    return function () {
      return Promise.reject({
        code: 'bridge_unsupported',
        message: what + ' is not supported by the Homestead bridge',
      });
    };
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

    async function send(body) {
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

    async function call(body) {
      var res = await send(body);
      if (res.status === 401 || res.status === 403) {
        // The page-load token may be stale (new session, restored tab).
        if (await refreshCsrf(body)) res = await send(body);
      }
      if (res.status === 404) return null;
      if (res.status === 401) fail('auth_required', 'Not signed in', 401);
      if (res.status === 403) fail('forbidden', 'Request refused by the bridge', 403);
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
    function noHandle(opts) {
      if (opts && opts.handle) fail('bridge_unsupported', 'Shared key-value handles are not supported by the Homestead bridge');
    }
    function fsKey(path) {
      var p = String(path).replace(/^\/+/, '');
      return p;
    }

    var kv = {
      async get(key, opts) {
        noHandle(opts);
        var r = await call({ operation: 'get', kind: 'kv', key: String(key) });
        return r === null ? null : JSON.parse(dec.decode(fromBase64(r.value)));
      },
      async set(key, value, opts) {
        if (Array.isArray(key)) {
          noHandle(value);
          if (!key.length) return true;
          await call({
            operation: 'putbatch', kind: 'kv',
            entries: key.map(function (e) { return { key: String(e.key), value: encodeValue(e.value) }; }),
          });
          return true;
        }
        noHandle(opts);
        await call({ operation: 'put', kind: 'kv', key: String(key), value: encodeValue(value) });
        return true;
      },
      async del(key, opts) {
        noHandle(opts);
        await call({ operation: 'delete', kind: 'kv', key: String(key) });
        return true;
      },
      async list(pattern, returnValues, opts) {
        if (typeof pattern === 'boolean') { opts = returnValues; returnValues = pattern; pattern = '*'; }
        pattern = pattern === undefined ? '*' : String(pattern);
        noHandle(opts);
        var star = pattern.indexOf('*');
        if (star !== -1 && star !== pattern.length - 1) fail('bridge_unsupported', 'Only trailing-* key patterns are supported');
        var prefix = star === -1 ? pattern : pattern.slice(0, -1);
        var r = await call({ operation: 'list', kind: 'kv', key: prefix });
        var keys = (r && r.keys) || [];
        if (star === -1) keys = keys.filter(function (k) { return k === prefix; });
        if (!returnValues) return keys;
        return Promise.all(keys.map(async function (k) { return { key: k, value: await kv.get(k) }; }));
      },
    };

    var fs = {
      async write(path, data) {
        var bytes;
        if (typeof data === 'string') bytes = enc.encode(data);
        else if (data && typeof data.arrayBuffer === 'function') bytes = new Uint8Array(await data.arrayBuffer());
        else bytes = enc.encode(String(data));
        if (bytes.length > MAX_VALUE) fail('storage_limit', 'storage limit exceeded');
        await call({ operation: 'put', kind: 'file', key: fsKey(path), value: toBase64(bytes) });
        return { path: path };
      },
      async read(path) {
        var r = await call({ operation: 'get', kind: 'file', key: fsKey(path) });
        if (r === null) fail('subject_does_not_exist', 'No such file');
        return new Blob([fromBase64(r.value)]);
      },
      async delete(path) {
        await call({ operation: 'delete', kind: 'file', key: fsKey(path) });
      },
      // Signed public read URLs are public exposure; ADR-053 requires an
      // explicit exposure plan first, so this is refused.
      getReadURL: unsupported('Public read URLs'),
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
        user = { username: r.subject, uuid: r.subject };
        return user;
      },
      async signIn() {
        if (config.signInUrl && root.location) { root.location.assign(config.signInUrl); return new Promise(function () {}); }
        fail('bridge_unsupported', 'Sign in through the Homestead sign-in page');
      },
      async signOut() {
        if (config.signOutUrl && root.location) { root.location.assign(config.signOutUrl); return; }
        fail('bridge_unsupported', 'Sign out through Homestead');
      },
    };

    // Deliberately absent: appID, perms, events, kv.share/unshare/listShared/
    // listSharedByMe. Tasks treats an SDK without kv.share as one that
    // predates KV shares and falls back to link sharing, which needs
    // fs.getReadURL and is refused above.
    return { env: 'app', kv: kv, fs: fs, auth: auth };
  }

  root.createHomesteadPuter = create;
  if (typeof root.puter === 'undefined' && !root.__HOMESTEAD_BRIDGE_NO_AUTOINSTALL__) {
    root.puter = create(root.__HOMESTEAD_BRIDGE__);
  }
})(globalThis);
