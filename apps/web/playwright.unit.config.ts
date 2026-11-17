import { defineConfig } from '@playwright/test';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

/**
 * Unit tests for the app's pure modules (no browser, no server).
 *
 * The runner is the one already in the repo — @playwright/test transpiles TS
 * and runs a plain test file with no page fixture, so pinning the row-state
 * machine behind the recipe form costs no new dependency. The browser flows
 * live in ./e2e and own their preview; these do not.
 */
export default defineConfig({
  testDir: './unit',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  reporter: [['list']],
  outputDir: process.env.PLAYWRIGHT_UNIT_OUTPUT_DIR || join(tmpdir(), 'hyperplane-web-unit'),
});
