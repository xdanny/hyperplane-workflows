import { defineConfig, devices } from '@playwright/test';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

// API-fixture tests own a production preview. Never reuse the user's live app.
const baseURL = 'http://127.0.0.1:5209';
export default defineConfig({
  testDir: './e2e',
  testMatch: '0[3-6]-*.spec.ts',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: [['list']],
  outputDir: process.env.PLAYWRIGHT_OUTPUT_DIR || join(tmpdir(), 'hyperplane-browser-fixtures'),
  webServer: {
    command: 'npm run build && node node_modules/vite/bin/vite.js preview --config playwright.preview.config.ts --host 127.0.0.1 --port 5209 --strictPort',
    url: baseURL,
    reuseExistingServer: false,
    timeout: 60_000,
    gracefulShutdown: { signal: 'SIGTERM', timeout: 5_000 },
  },
  use: {
    baseURL,
    actionTimeout: 10_000,
    navigationTimeout: 15_000,
    headless: true,
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
