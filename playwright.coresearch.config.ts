import { defineConfig } from '@playwright/test';

// Isolated HTTP-fixture checks: no Go lab, database or authenticated session required.
const port = process.env.LYK_UI_RENDERER_PORT || '1437';
export default defineConfig({
  testDir: './tests/e2e',
  testMatch: 'coresearch-markdown.spec.ts',
  workers: 1,
  retries: 0,
  reporter: 'list',
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    browserName: 'chromium',
    permissions: ['clipboard-read', 'clipboard-write'],
  },
  webServer: {
    command: `npx vite --config apps/dashboard/vite.config.ts --host 127.0.0.1 --port ${port}`,
    url: `http://127.0.0.1:${port}`,
    reuseExistingServer: true,
  },
});
