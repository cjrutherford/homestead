// Homestead (S464): the body of API Client's script sandbox loader, served as
// a file next to homestead-sandbox.html instead of inlined in a srcdoc.
// A srcdoc frame inherits the app page's Content-Security-Policy, which
// refuses inline script, Blob workers and eval; this page is its own
// response with the sandbox policy (see HOMESTEAD_EVERYTHING_SANDBOX_PAGE).
// It is a copy of sandboxLoader() in src/core/scripts/sandbox.js at the
// pinned version and must be reviewed against it on a version bump.
(function sandboxLoader() {
  let port = null;
  let blobUrl = null;
  let inline = null;
  let spare = null;
  const workers = new Map();
  const inlineSends = new Map();
  let inlineSeq = 0;

  function spawn() {
    return new Worker(blobUrl, { name: 'user-script' });
  }

  function verify(worker) {
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => fail(new Error('worker did not start')), 3000);
      function fail(err) {
        clearTimeout(timer);
        worker.terminate();
        reject(err);
      }
      worker.onmessage = (e) => {
        if (!e.data || e.data.type !== 'hello') return;
        clearTimeout(timer);
        worker.onmessage = null;
        worker.onerror = null;
        resolve(worker);
      };
      worker.onerror = (e) => {
        e.preventDefault();
        fail(new Error(e.message || 'worker failed to start'));
      };
      worker.postMessage({ type: 'hello' });
    });
  }

  function takeWorker() {
    const w = spare || spawn();
    try {
      spare = spawn();
    } catch {
      spare = null;
    }
    return w;
  }

  function finishWorker(id) {
    const w = workers.get(id);
    if (!w) return;
    workers.delete(id);
    w.terminate();
  }

  function runInWorker(id, message) {
    let w;
    try {
      w = takeWorker();
    } catch (err) {
      port.postMessage({ type: 'result', id, result: { ok: false, error: { message: `Could not start a script worker: ${err.message}` } } });
      return;
    }
    workers.set(id, w);
    w.onmessage = (e) => {
      const d = e.data;
      if (!d || typeof d !== 'object' || !workers.has(id)) return;
      if (d.type === 'send') port.postMessage({ type: 'send', id, sendId: d.sendId, request: d.request });
      else if (d.type === 'result') {
        finishWorker(id);
        port.postMessage({ type: 'result', id, result: d.result });
      }
    };
    w.onerror = (e) => {
      e.preventDefault();
      if (!workers.has(id)) return;
      finishWorker(id);
      const message = `Script worker failed: ${e.message || 'unknown error'}`;
      port.postMessage({ type: 'result', id, result: { ok: false, error: { message } } });
    };
    w.postMessage({ type: 'run', message });
  }

  function runInline(id, message) {
    const host = {
      send: (request) =>
        new Promise((resolve, reject) => {
          const sendId = ++inlineSeq;
          inlineSends.set(`${id}:${sendId}`, { resolve, reject });
          port.postMessage({ type: 'send', id, sendId, request });
        }),
    };
    inline.runScript(message, host).then(
      (result) => port.postMessage({ type: 'result', id, result }),
      (err) => port.postMessage({ type: 'result', id, result: { ok: false, error: { message: String(err && err.message) } } }),
    );
  }

  function onHostMessage(e) {
    const d = e.data;
    if (!d || typeof d !== 'object') return;
    if (d.type === 'ping') port.postMessage({ type: 'pong', nonce: d.nonce });
    else if (d.type === 'run') (inline ? runInline : runInWorker)(d.id, d.message);
    else if (d.type === 'terminate') finishWorker(d.id);
    else if (d.type === 'sendResult') {
      const payload = { type: 'sendResult', sendId: d.sendId, response: d.response, error: d.error };
      if (inline) {
        const key = `${d.id}:${d.sendId}`;
        const s = inlineSends.get(key);
        if (!s) return;
        inlineSends.delete(key);
        if (d.error != null) s.reject(new Error(String(d.error)));
        else s.resolve(d.response);
      } else if (workers.has(d.id)) workers.get(d.id).postMessage(payload);
    }
  }

  async function boot(source, forceInline) {
    let mode = 'worker';
    let reason;
    try {
      if (forceInline) throw new Error('an earlier iframe could not start a worker');
      blobUrl = URL.createObjectURL(new Blob([source], { type: 'text/javascript' }));
      spare = await verify(spawn());
    } catch (err) {
      mode = 'inline';
      reason = String((err && err.message) || err);
      (0, eval)(source);
      inline = self.__scriptRuntime;
      delete self.__scriptRuntime;
    }
    port.postMessage({ type: 'ready', mode, reason });
  }

  addEventListener('message', function onInit(e) {
    if (port || e.source !== parent || !e.data || e.data.type !== 'init' || !e.ports || !e.ports[0]) return;
    removeEventListener('message', onInit);
    port = e.ports[0];
    port.onmessage = onHostMessage;
    boot(String(e.data.source), e.data.inline === true);
  });
})();
