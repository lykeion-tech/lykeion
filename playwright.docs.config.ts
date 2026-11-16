import { defineConfig } from '@playwright/test';

const externalOrigin = process.env.LYK_DOCS_URL;
const origin = externalOrigin || 'http://127.0.0.1:1453';
process.env.LYK_DOCS_URL = origin;

export default defineConfig({
  testDir: './tests/e2e',
  projects: [{ name: 'docs', testMatch: 'docs.spec.ts' }],
  workers: 1,
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 15_000 },
  reporter: 'list',
  use: {
    baseURL: origin,
    viewport: { width: 1512, height: 982 },
    browserName: 'chromium',
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE
      ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE }
      : undefined,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  // An explicit URL targets an already-running site, including isolated Caddy.
  webServer: externalOrigin ? undefined : {
    command: 'npm --prefix apps/web run dev -- --hostname 127.0.0.1 --port 1453',
    url: `${origin}/docs/`,
    timeout: 120_000,
    reuseExistingServer: false,
    gracefulShutdown: { signal: 'SIGTERM', timeout: 5_000 },
  },
});
