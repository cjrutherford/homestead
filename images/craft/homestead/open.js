// Homestead wrapper for craft apps (S254, ADR-029, ADR-032).
//
// Frames the unmodified craft app on this origin and bridges it to the
// same-origin file path /_homestead/files/f/, which the edge forwards to the
// Homestead daemon's Nextcloud proxy. No Nextcloud credential is ever here:
// the browser attaches the HttpOnly craft cookie by itself, and the launch
// code in the URL fragment is spent once and removed from the address.
//
// Protocols (verified against the pinned upstream sources):
//  - gridcraft v0.7.0, cadcraft v0.7.0, pdfcraft v0.8.0 are framed at
//    ?host=parent and speak postMessage with a same-origin parent:
//      app -> parent  <app>:ready {version}, <app>:dirty {dirty},
//                     <app>:save {name, bytes: ArrayBuffer[, saveAs]},
//                     <app>:failed {error}
//      parent -> app  <app>:open {name, bytes: ArrayBuffer | Uint8Array}
//  - filmcraft v0.8.0 and effectcraft v0.10.0 have no bridge; they open files
//    through window.filmcraft.importUrl/openProject(url) and
//    window.effectcraft.addFile(url, name). Their saves stay browser-local.
(function () {
  'use strict';

  var FILES = '/_homestead/files';
  var TREE = FILES + '/f/';
  var BRIDGED = { gridcraft: 1, cadcraft: 1, pdfcraft: 1 };
  var URL_HOOK = { filmcraft: 1, effectcraft: 1 };

  var $ = function (id) { return document.getElementById(id); };
  var frame = $('app');
  var info = null;
  var dir = '';           // current folder, relative to the root folder
  var current = null;     // {path, etag} of the document open in the app
  var ready = false;
  var pendingOpen = null;
  var dirty = false;

  function say(text) { $('state').textContent = text; }
  function fail(text) {
    var m = $('message');
    m.textContent = text;
    m.hidden = false;
    say('Not connected');
  }

  function encodePath(p) {
    return p.split('/').filter(Boolean).map(encodeURIComponent).join('/');
  }
  function treeUrl(path) { return TREE + encodePath(path); }
  function baseName(p) { return p.split('/').filter(Boolean).pop() || ''; }
  function joinPath(a, b) { return a ? a + '/' + b : b; }

  function api(method, url, opts) {
    opts = opts || {};
    opts.method = method;
    opts.credentials = 'same-origin';
    opts.cache = 'no-store';
    return fetch(url, opts).then(function (res) {
      if (res.status === 401) {
        fail('The Nextcloud session ended. Open this app again from Homestead (Craft files, Open with Nextcloud files).');
        throw new Error('session ended');
      }
      return res;
    });
  }

  // ---- session -------------------------------------------------------

  function startSession() {
    var m = /^#code=([A-Za-z0-9_-]+)$/.exec(location.hash);
    if (m) {
      // Spend the one-time code and take it out of the address bar and
      // history before anything else runs.
      history.replaceState(null, '', location.pathname + location.search);
      return fetch(FILES + '/session', {
        method: 'POST', credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ code: m[1] })
      }).then(function (res) { return sessionReply(res, true); });
    }
    return fetch(FILES + '/session', { credentials: 'same-origin', cache: 'no-store' })
      .then(function (res) { return sessionReply(res, false); });
  }

  function sessionReply(res, launched) {
    if (res.status === 200) { return res.json(); }
    var why;
    if (res.status === 409) {
      why = 'Nextcloud file storage is not enabled for this app. It keeps documents in the browser.';
    } else if (launched) {
      why = 'This launch link was already used or has expired. Open the app again from Homestead.';
    } else {
      why = 'Open this app from Homestead (Craft files, Open with Nextcloud files) to use your Nextcloud.';
    }
    throw new Error(why);
  }

  // ---- file list -----------------------------------------------------

  var PROPFIND_BODY = '<?xml version="1.0"?><d:propfind xmlns:d="DAV:"><d:prop>' +
    '<d:resourcetype/><d:getcontentlength/><d:getlastmodified/></d:prop></d:propfind>';

  function list() {
    $('crumbs').textContent = '/' + dir;
    $('up').disabled = dir === '';
    return api('PROPFIND', treeUrl(dir), {
      headers: { Depth: '1', 'Content-Type': 'application/xml' }, body: PROPFIND_BODY
    }).then(function (res) {
      if (res.status !== 207) { throw new Error('Could not list the folder (' + res.status + ').'); }
      return res.text();
    }).then(function (xml) {
      var doc = new DOMParser().parseFromString(xml, 'application/xml');
      var rows = [];
      var responses = doc.getElementsByTagNameNS('DAV:', 'response');
      for (var i = 0; i < responses.length; i++) {
        var href = responses[i].getElementsByTagNameNS('DAV:', 'href')[0];
        if (!href) { continue; }
        var path = decodeURIComponent(href.textContent.replace(/^.*\/_homestead\/files\/f\/?/, '')).replace(/\/+$/, '');
        if (path === dir) { continue; }
        var isDir = responses[i].getElementsByTagNameNS('DAV:', 'collection').length > 0;
        rows.push({ path: path, name: baseName(path), dir: isDir });
      }
      rows.sort(function (a, b) { return (b.dir - a.dir) || a.name.localeCompare(b.name); });
      renderList(rows);
    }).catch(function (e) { if (e.message !== 'session ended') { say(e.message); } });
  }

  function renderList(rows) {
    var ul = $('list');
    ul.textContent = '';
    rows.forEach(function (row) {
      var li = document.createElement('li');
      var b = document.createElement('button');
      b.type = 'button';
      b.textContent = (row.dir ? '\u{1F4C1} ' : '') + row.name;
      b.addEventListener('click', function () {
        if (row.dir) { dir = row.path; list(); } else { openFile(row.path); }
      });
      li.appendChild(b);
      ul.appendChild(li);
    });
    if (!rows.length) {
      var li = document.createElement('li');
      li.textContent = 'This folder is empty.';
      ul.appendChild(li);
    }
  }

  // ---- open ----------------------------------------------------------

  function confirmDiscard() {
    return !dirty || window.confirm('The open document has unsaved changes. Open another file and discard them?');
  }

  function openFile(path) {
    if (!confirmDiscard()) { return; }
    var name = baseName(path);
    if (URL_HOOK[info.app]) { return openByUrl(path, name); }
    say('Opening ' + name);
    api('GET', treeUrl(path)).then(function (res) {
      if (!res.ok) { throw new Error('Could not open ' + name + ' (' + res.status + ').'); }
      var etag = res.headers.get('ETag');
      return res.arrayBuffer().then(function (buf) { return { buf: buf, etag: etag }; });
    }).then(function (got) {
      current = { path: path, etag: got.etag };
      dirty = false;
      sendOpen(name, got.buf);
      say('Opened ' + path);
    }).catch(function (e) { if (e.message !== 'session ended') { say(e.message); } });
  }

  function sendOpen(name, buf) {
    var msg = { type: info.app + ':open', name: name, bytes: buf };
    if (!ready) { pendingOpen = msg; return; }
    frame.contentWindow.postMessage(msg, location.origin, [buf]);
  }

  // filmcraft / effectcraft: the app fetches the same-origin URL itself.
  function openByUrl(path, name) {
    var url = treeUrl(path);
    var win = frame.contentWindow;
    var tries = 0;
    say('Opening ' + name);
    (function attempt() {
      var hook = win && win[info.app];
      if (!hook) {
        if (++tries > 240) { say('The app did not finish loading.'); return; }
        setTimeout(attempt, 500);
        return;
      }
      var done;
      try {
        if (info.app === 'filmcraft') {
          done = /\.fcproj$/i.test(name) ? hook.openProject(url) : hook.importUrl(url, name);
        } else {
          done = hook.addFile(url, name);
        }
      } catch (e) { say('Could not open ' + name + '.'); return; }
      Promise.resolve(done).then(
        function () { say('Opened ' + path + ' (saves stay in the browser; export from the app)'); },
        function () { say('Could not open ' + name + '.'); });
    })();
  }

  // ---- save ----------------------------------------------------------

  // A save name is one file name: never a path, dot segment or control
  // character (the daemon refuses these too; this keeps them from being sent).
  function validName(n) {
    return n !== '' && n !== '.' && n !== '..' && !/[\/\\\u0000-\u001f\u007f]/.test(n);
  }

  function saveFromApp(msg) {
    var name = typeof msg.name === 'string' && msg.name ? baseName(msg.name) : 'Document';
    if (!validName(name)) { say('Not saved: "' + name + '" is not a valid file name.'); return; }
    var buf = msg.bytes;
    if (!(buf instanceof ArrayBuffer) && !(buf instanceof Uint8Array)) { return; }
    var overwrite = current && !msg.saveAs && baseName(current.path) === name;
    var path = overwrite ? current.path : joinPath(dir, name);
    var headers = {};
    if (overwrite && current.etag) { headers['If-Match'] = current.etag; }
    else if (!overwrite) { headers['If-None-Match'] = '*'; }
    say('Saving ' + path);
    put(path, buf, headers).then(function (res) {
      if (res.status === 412) {
        if (!window.confirm(path + ' has changed in Nextcloud since it was opened. Replace it with this version?')) {
          say('Not saved: ' + path + ' changed in Nextcloud');
          return null;
        }
        return put(path, buf, {});
      }
      return res;
    }).then(function (res) {
      if (!res) { return; }
      if (res.status === 413) { throw new Error('Not saved: the file is larger than the upload limit set in Homestead.'); }
      if (!res.ok) { throw new Error('Not saved: Nextcloud answered ' + res.status + '.'); }
      current = { path: path, etag: res.headers.get('ETag') };
      dirty = false;
      say('Saved ' + path);
      if (dir === path.split('/').slice(0, -1).join('/')) { list(); }
    }).catch(function (e) { if (e.message !== 'session ended') { say(e.message); } });
  }

  function put(path, buf, headers) {
    return api('PUT', treeUrl(path), { headers: headers, body: buf });
  }

  // ---- bridge --------------------------------------------------------

  // Only the framed app, on this origin, is heard.
  window.addEventListener('message', function (e) {
    if (!info || e.source !== frame.contentWindow || e.origin !== location.origin) { return; }
    var d = e.data;
    if (!d || typeof d.type !== 'string' || d.type.indexOf(info.app + ':') !== 0) { return; }
    switch (d.type.slice(info.app.length + 1)) {
      case 'ready':
        ready = true;
        say('Ready');
        if (pendingOpen) {
          var m = pendingOpen;
          pendingOpen = null;
          frame.contentWindow.postMessage(m, location.origin, [m.bytes]);
        }
        break;
      case 'dirty': dirty = !!d.dirty; $('title').textContent = (dirty ? '• ' : '') + info.app; break;
      case 'save': saveFromApp(d); break;
      case 'failed': fail('The app could not start: ' + String(d.error || 'unknown error')); break;
    }
  });

  window.addEventListener('beforeunload', function (e) {
    if (dirty) { e.preventDefault(); e.returnValue = ''; }
  });

  // ---- start ---------------------------------------------------------

  $('up').addEventListener('click', function () { dir = dir.split('/').slice(0, -1).join('/'); list(); });
  $('refresh').addEventListener('click', list);
  $('toggle-files').addEventListener('click', function () { $('files').hidden = !$('files').hidden; });

  startSession().then(function (s) {
    info = s;
    document.title = s.app + ' with Nextcloud files';
    $('title').textContent = s.app;
    if (!BRIDGED[s.app] && !URL_HOOK[s.app]) {
      throw new Error('This app has no file hook; it keeps documents in the browser.');
    }
    $('files').hidden = false;
    $('toggle-files').hidden = false;
    $('note').textContent = BRIDGED[s.app]
      ? 'Pick a file to open. Save in the app writes to the folder shown.'
      : 'Pick a file to import. Saving stays in the browser: export from the app.';
    frame.src = BRIDGED[s.app] ? '/?host=parent' : '/';
    frame.hidden = false;
    say(BRIDGED[s.app] ? 'Loading the app' : 'Ready');
    return list();
  }).catch(function (e) { if (e.message !== 'session ended') { fail(e.message); } });
})();
