// vitest globalSetup: one real bridge process (temp SQLite) for the adapted
// upstream suite. Connection details reach workers through env vars.
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { startBridge } from './bridge-harness.mjs';

export default async function setup() {
  const dir = mkdtempSync(path.join(tmpdir(), 's367-gate-'));
  const bridge = await startBridge({ bin: process.env.HS_BRIDGE_BIN, db: path.join(dir, 'suite.db') });
  process.env.HS_BRIDGE_URL = bridge.url;
  return async () => {
    await bridge.stop();
    rmSync(dir, { recursive: true, force: true });
  };
}
