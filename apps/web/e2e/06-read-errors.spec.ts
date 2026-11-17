import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';

const project = { project: 'cycling', name: 'Cycling', registered: true, status: 'active', count: 1, runs: 1, lanes: 1, decisions: 1, workflowCount: 1 };
const run = { runId: 'recent-run', dag: 'node-workflow', goal: 'Inspect cycling sources', state: 'passed', startedAt: '2026-09-10T12:00:00Z', updatedAt: '2026-09-10T12:00:00Z', options: { runtime: 'codex-sdk' } };
const cases = [
  { label: 'runs', path: '/runs', endpoint: '/api/runs', empty: 'No runs match this filter.', populated: [run], result: 'Inspect cycling sources' },
  { label: 'projects', path: '/projects', endpoint: '/api/projects', empty: 'Group your work into projects — start one from any prompt or recipe.', populated: [project], result: 'Open project Cycling' },
  { label: 'workflows', path: '/projects/cycling', endpoint: '/api/projects/cycling', empty: 'No workflows are saved or have run under this project yet.', populated: { ...project, workflows: [{ key: 'saved:one', name: 'Source review', dag: 'node-workflow', runs: 1, lastRunId: 'recent-run' }] }, result: 'Source review' },
  { label: 'recent runs', path: '/projects/cycling', endpoint: '/api/runs', limit: '8', empty: 'No runs yet.', populated: [run], result: 'recent-run' },
  { label: 'lanes', path: '/projects/cycling', endpoint: '/api/lanes', empty: 'No lanes yet.', populated: [{ id: 'lane-1', name: 'Review sources', status: 'active' }], result: 'Review sources' },
  { label: 'decisions', path: '/projects/cycling', endpoint: '/api/decisions', empty: 'No decisions yet.', populated: [{ id: 'decision-1', title: 'Approve source review', status: 'pending' }], result: 'Approve source review' },
];
type Case = typeof cases[number];
async function fixture(page: Page, scenario: Case, state: { value: 'failed' | 'empty' | 'populated'; hold?: Promise<void> }) {
  const requests: string[] = [];
  await page.route('**/api/**', async route => {
    const url = new URL(route.request().url());
    if (url.pathname === '/api/auth/check') return route.fulfill({ json: { authRequired: false, authed: true } });
    if (url.pathname === '/api/health') return route.fulfill({ json: { ok: true } });
    if (url.pathname === scenario.endpoint && (!scenario.limit || url.searchParams.get('limit') === scenario.limit)) {
      requests.push(state.value);
      if (state.hold) await state.hold;
      if (state.value === 'failed') return route.fulfill({ status: 500, json: { error: 'Fixture read failure' } });
      return route.fulfill({ json: state.value === 'populated' ? scenario.populated : scenario.label === 'workflows' ? { ...project, workflows: [] } : [] });
    }
    return route.fulfill({ json: url.pathname === '/api/projects' ? [project] : url.pathname === '/api/projects/cycling' ? { ...project, workflows: [] } : [] });
  });
  return requests;
}
function result(page: Page, scenario: Case) {
  return scenario.label === 'projects' ? page.getByRole('button', { name: scenario.result, exact: true }) : page.getByText(scenario.result, { exact: true }).first();
}

for (const scenario of cases) {
  test(`${scenario.label} distinguishes loading and read failure from emptiness, then retries successfully`, async ({ page }, testInfo) => {
    let release!: () => void;
    const state: Parameters<typeof fixture>[2] = { value: 'failed', hold: new Promise<void>(resolve => { release = resolve; }) };
    await fixture(page, scenario, state);
    await page.goto(scenario.path);
    await expect(page.getByText(`Loading ${scenario.label}…`, { exact: true })).toBeVisible();
    await expect(page.getByText(scenario.empty, { exact: true })).toHaveCount(0);
    release();
    const error = page.getByRole('alert').filter({ hasText: `Could not load ${scenario.label}.` });
    await expect(error).toBeVisible();
    await expect(page.getByText(scenario.empty, { exact: true })).toHaveCount(0);
    if (scenario.label === 'projects') {
      await expect(page.locator('.proj-toggle-count')).toHaveCount(0);
      await expect(page.getByRole('region', { name: 'Projects', exact: true }).locator('.projpanel-head .mono')).toHaveCount(0);
      await expect(page.getByRole('alert')).toHaveCount(1);
    }
    if (scenario.label === 'workflows') await page.screenshot({ path: testInfo.outputPath('project-workflows-unavailable.png'), fullPage: true });
    state.value = 'populated';
    await error.getByRole('button', { name: `Retry ${scenario.label}`, exact: true }).click();
    await expect(result(page, scenario)).toBeVisible();
    await expect(error).toHaveCount(0);
  });
}

test('successful empty reads retain the real empty states', async ({ page }) => {
  for (const scenario of cases) {
    await page.unrouteAll({ behavior: 'wait' });
    await fixture(page, scenario, { value: 'empty' });
    await page.goto(scenario.path);
    await expect(page.getByText(scenario.empty, { exact: true })).toBeVisible();
    await expect(page.getByRole('alert')).toHaveCount(0);
    await expect(page.getByText(`Loading ${scenario.label}…`, { exact: true })).toHaveCount(0);
  }
});

for (const scenario of cases) {
  test(`${scenario.label} keeps cached results visibly stale after refresh failure and recovers`, async ({ page }, testInfo) => {
    await page.clock.install();
    const state: Parameters<typeof fixture>[2] = { value: 'populated' };
    await fixture(page, scenario, state);
    await page.goto(scenario.path);
    await expect(result(page, scenario)).toBeVisible();
    state.value = 'failed';
    // Query data becomes stale after one second; visibility asks the shared
    // query cache to refresh without replacing the current view or its data.
    await page.clock.runFor(1100);
    await page.evaluate(() => window.dispatchEvent(new Event('visibilitychange')));
    await page.clock.runFor(1100);
    const notice = page.getByRole('alert').filter({ hasText: `Could not refresh ${scenario.label}.` });
    await expect(notice).toBeVisible();
    await expect(notice).toContainText(`Showing previously loaded ${scenario.label}; they may be out of date.`);
    await expect(result(page, scenario)).toBeVisible();
    await expect(page.getByText(scenario.empty, { exact: true })).toHaveCount(0);
    if (scenario.label === 'projects') await expect(page.getByRole('alert')).toHaveCount(1);
    if (scenario.label === 'workflows') await page.screenshot({ path: testInfo.outputPath('project-workflows-stale.png'), fullPage: true });
    state.value = 'populated';
    await notice.getByRole('button', { name: `Retry ${scenario.label}`, exact: true }).click();
    await expect(notice).toHaveCount(0);
    await expect(result(page, scenario)).toBeVisible();
  });
}

test('collapsed project selector owns the sole read error and retries without inventing a missing project', async ({ page }) => {
  const state: Parameters<typeof fixture>[2] = { value: 'failed' };
  await fixture(page, cases[1], state);
  await page.goto('/runs?project=cycling');
  await expect(page.getByRole('alert').filter({ hasText: 'Could not load projects.' })).toHaveCount(1);
  await expect(page.getByText(/No project called/)).toHaveCount(0);
  await page.goto('/runs');
  const error = page.getByRole('alert').filter({ hasText: 'Could not load projects.' });
  await expect(error).toHaveCount(1);
  await expect(page.getByText(cases[1].empty, { exact: true })).toHaveCount(0);
  state.value = 'populated';
  await error.getByRole('button', { name: 'Retry projects', exact: true }).click();
  await expect(error).toHaveCount(0);
  await expect(page.locator('.proj-toggle-count')).toHaveText('1');
});
