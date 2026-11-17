import { defineConfig } from 'vitest/config';

// Mode "suite": upstream tests, with tests/helpers/mock-puter.js replaced by a
// bridge-backed helper. Mode "bridge": Homestead-specific bridge tests only.
const mode = process.env.HS_GATE_MODE || 'suite';
export default defineConfig({
  test: {
    include: mode === 'bridge' ? ['gate/*.test.js'] : ['tests/**/*.test.js'],
    globalSetup: mode === 'bridge' ? [] : ['./gate/global-setup.mjs'],
    testTimeout: 30000,
    fileParallelism: mode !== 'bridge',
  },
});
