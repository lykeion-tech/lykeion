import { defineConfig } from '@playwright/test';

const lab = '127.0.0.1:18080';

export default defineConfig({
  testDir: './tests/e2e',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 15_000 },
  reporter: 'list',
  projects: [
    // Signs in once through the dashboard; every spec reuses the session.
    { name: 'setup', testMatch: 'signin.setup.ts' },
    {
      name: 'lab',
      testMatch: '*.spec.ts',
      testIgnore: 'lab.spec.ts',
      dependencies: ['setup'],
      use: { storageState: 'test-results/ui-lab/session.json' },
    },
    {
      name: 'lab-contract',
      testMatch: 'lab.spec.ts',
      dependencies: ['setup'],
      use: { storageState: { cookies: [], origins: [] } },
    },
  ],
  use: {
    baseURL: 'http://127.0.0.1:1431',
    viewport: { width: 1512, height: 982 },
    browserName: 'chromium',
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE
      ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE }
      : undefined,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  webServer: [
    {
      command:
        'mkdir -p ../../../test-results/ui-lab && go test -p 1 -c -o ../../../test-results/ui-lab/e2e.test . && exec ../../../test-results/ui-lab/e2e.test -test.run "^TestUILab$" -test.timeout 0 -test.v',
      cwd: 'server/internal/e2e',
      env: { LYK_UI_LAB: lab, GOMAXPROCS: '2', GOFLAGS: '-p=1' },
      url: `http://${lab}/health`,
      timeout: 300_000,
      reuseExistingServer: false,
      gracefulShutdown: { signal: 'SIGTERM', timeout: 30_000 },
      stdout: 'pipe',
    },
    {
      command: 'npx vite --config apps/dashboard/vite.config.ts --host 127.0.0.1 --port 1431',
      env: { LYK_API_URL: `http://${lab}` },
      url: 'http://127.0.0.1:1431',
      timeout: 60_000,
      reuseExistingServer: false,
      gracefulShutdown: { signal: 'SIGTERM', timeout: 5_000 },
    },
  ],
});
