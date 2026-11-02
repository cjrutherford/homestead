// Starts the real Go everything-bridge (development-identity build) for the
// S367 behavioural gate. TEST ONLY: the dev identity is not authentication.
import { spawn } from 'node:child_process';
import net from 'node:net';

function freePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.once('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address();
      srv.close(() => resolve(port));
    });
  });
}

export async function startBridge({ bin, db, app = 'tasks' }) {
  const port = await freePort();
  const origin = `http://127.0.0.1:${port}`;
  const child = spawn(bin, [
    '-dev-identity', '-db', db, '-app', app, '-listen', `127.0.0.1:${port}`, '-origin', origin,
  ], {
    env: { ...process.env, HOMESTEAD_EVERYTHING_INSECURE_DEV: 'i-understand-this-is-not-authentication' },
    stdio: ['ignore', 'ignore', 'pipe'],
  });
  let log = '';
  await new Promise((resolve, reject) => {
    child.stderr.on('data', (d) => {
      log += d;
      if (log.includes('listening on')) resolve();
    });
    child.once('exit', (code) => reject(new Error(`bridge exited ${code}: ${log}`)));
    setTimeout(() => reject(new Error(`bridge start timeout: ${log}`)), 10000);
  });
  child.stderr.resume();
  const post = async (path, body) => {
    const res = await fetch(origin + path, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
    });
    return res.json();
  };
  return {
    url: origin,
    endpoint: `${origin}/_homestead/data`,
    origin,
    /** Mint a dev session; returns the config the shim needs. */
    async login(subject, ttlMs) {
      const s = await post('/_dev/login', { subject, ttl_ms: ttlMs });
      return { endpoint: `${origin}/_homestead/data`, origin, csrf: s.csrf, headers: { Cookie: `hs_dev_session=${s.cookie}` }, cookie: s.cookie };
    },
    revoke: (cookie) => post('/_dev/revoke', { cookie }),
    stop() {
      return new Promise((resolve) => {
        if (child.exitCode !== null) return resolve();
        child.once('exit', () => resolve());
        child.kill('SIGTERM');
      });
    },
  };
}
