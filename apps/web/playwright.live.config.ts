import { defineConfig, devices } from '@playwright/test';

// Started by TestSDKBrowserJourney, which owns a disposable DB, real API and preview.
const api = process.env.SDK_BROWSER_API;
const baseURL = process.env.SDK_BROWSER_URL;
const port = baseURL && new URL(baseURL).port;
if (!api || !baseURL || !port || !process.env.SDK_BROWSER_RECEIPT) {
  throw new Error('Run the opt-in TestSDKBrowserJourney harness; it supplies the isolated API, preview and receipt.');
}

export default defineConfig({
  testDir: './e2e-live',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 60_000,
  reporter: [['list']],
  outputDir: process.env.SDK_BROWSER_OUTPUT,
  use: { baseURL, headless: true, actionTimeout: 10_000, screenshot: 'only-on-failure', ...devices['Desktop Chrome'] },
});
