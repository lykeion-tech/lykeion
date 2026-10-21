import type { Environment } from 'vitest/environments';

// Node with components compiled for the browser, so a test can mount them
// with its own renderer and click through them.
export default {
  name: 'client-node',
  viteEnvironment: 'client',
  setup: () => ({ teardown() {} }),
} satisfies Environment;
