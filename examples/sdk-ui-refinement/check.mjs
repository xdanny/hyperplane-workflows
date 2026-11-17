import { chromium } from '../../apps/web/node_modules/playwright/index.mjs';
import assert from 'node:assert/strict';
import { lstat } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';
import path from 'node:path';
import { randomUUID } from 'node:crypto';

// Trusted acceptance runs outside the generated project. These checks establish
// counter behavior and keyboard access, not subjective visual quality.
const [workspace, evidence] = process.argv.slice(2);
const index = path.join(workspace, 'index.html');
const stat = await lstat(index);
assert.ok(stat.isFile() && !stat.isSymbolicLink(), 'index.html must be a regular file');
const browser = await chromium.launch({ headless: true, chromiumSandbox: true });
try {
  const context = await browser.newContext({ viewport: { width: 900, height: 650 }, serviceWorkers: 'block' });
  const url = pathToFileURL(index).href;
  await context.route('**/*', (route) => route.request().url() === url ? route.continue() : route.abort());
  const page = await context.newPage();
  page.setDefaultTimeout(5000);
  try {
    await page.goto(url);
    assert.equal(await page.getByRole('heading', { name: 'Focus counter', exact: true }).count(), 1, 'Show the Focus counter heading');
    const count = page.getByRole('status');
    assert.equal((await count.innerText()).trim(), '0', 'Counter starts at 0');
    const button = page.getByRole('button', { name: 'Add session', exact: true });
    await button.click();
    assert.equal((await count.innerText()).trim(), '1', 'One click must increment the counter by exactly one');
    await button.focus();
    await page.keyboard.press('Enter');
    assert.equal((await count.innerText()).trim(), '2', 'Enter must add exactly one more session');
    console.log('Passed: initial count, click behavior and keyboard activation.');
  } finally {
    await page.screenshot({ path: path.join(evidence, `browser-${randomUUID()}.png`), fullPage: true });
  }
} finally {
  await browser.close();
}
