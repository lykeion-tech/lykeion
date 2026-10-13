import { defineConfig } from '@playwright/test';

process.env.LYKEION_VISUAL_RUN_ID ||= new Date().toISOString();

export default defineConfig({
  testDir: './tests',
  testMatch: 'visual-capture.spec.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 120_000,
  reporter: 'list',
  outputDir: 'test-results/visual-capture',
  use: {
    baseURL: 'http://127.0.0.1:1430',
    viewport: { width: 1920, height: 959 },
    deviceScaleFactor: 1,
    browserName: 'chromium',
    colorScheme: 'light',
    reducedMotion: 'reduce',
    locale: 'en-US',
    timezoneId: 'Europe/Copenhagen',
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE
      ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE }
      : undefined,
    trace: 'retain-on-failure',
  },
  webServer: {
    command: 'npm run dev',
    url: 'http://127.0.0.1:1430',
    reuseExistingServer: true,
    timeout: 30_000,
  },
});
