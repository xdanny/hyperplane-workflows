import { test, expect } from '@playwright/test';

const brief = 'Discover practical app ideas using dated research and independent reviews. '.repeat(12);
const response = 'Preserve this full response and its evidence. '.repeat(80);

test('a named run keeps its complete brief and response without burying the graph', async ({ page }) => {
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/health') return route.fulfill({ json: { ok: true } });
    if (path === '/api/auth/check') return route.fulfill({ json: { authRequired: false, authed: true } });
    if (path === '/api/runs/structured-run') return route.fulfill({ json: { run: {
      runId: 'structured-run', dag: 'node-workflow', goal: brief, state: 'passed',
      options: { runtime: 'codex-sdk', projectWorkflow: { name: 'Ironman app discovery' }, request: { workflow: 'node-workflow', instruction: brief, nodes: [{ id: 'research', title: 'Dated trend research', kind: 'research', instruction: 'Find recent sources and report uncertainty.' }] } },
      doc: { sdk: { phase: 'passed', text: response, nodes: [{ id: 'research', phase: 'passed', document: { path: 'research.md', bytes: 99, sha256: 'a'.repeat(64) } }] } },
    }, nodes: [], outcome: null, journey: null } });
    return route.fulfill({ json: [] });
  });
  await page.goto('/runs/structured-run');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Ironman app discovery');
  const briefDisclosure = page.locator('details').filter({ has: page.getByText('Workflow brief', { exact: true }) });
  await expect(briefDisclosure).not.toHaveAttribute('open', '');
  await briefDisclosure.getByText('Workflow brief', { exact: true }).click();
  await expect(briefDisclosure.locator('p')).toHaveText(brief);
  // S-12: this run recorded a document, so the result card is what leads the
  // page and "Latest step result" does not repeat underneath it. The page says
  // what the run produced once.
  const card = page.getByRole('region', { name: 'Your result' });
  await expect(card.getByText('Dated trend research', { exact: false })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Worker response' })).toHaveCount(0);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test('a run with no recorded document still shows its full worker response (S-12)', async ({ page }) => {
  // The other half of the same rule: suppressing the response is only honest
  // while the card above is carrying the deliverable. A node workflow that
  // recorded no file has no card, so the response is the only account there is.
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/runs/no-document') return route.fulfill({ json: { run: {
      runId: 'no-document', dag: 'node-workflow', goal: 'Draft the brief', state: 'passed',
      options: { runtime: 'codex-sdk', request: { workflow: 'node-workflow', nodes: [{ id: 'draft', title: 'Draft', kind: 'document', instruction: 'Write a draft.' }] } },
      doc: { sdk: { phase: 'passed', text: response, nodes: [{ id: 'draft', phase: 'passed' }] } },
    }, nodes: [], outcome: null, journey: null } });
    return route.fulfill({ json: path === '/api/auth/check' ? { authRequired: false, authed: true } : path === '/api/health' ? { ok: true } : [] });
  });
  await page.goto('/runs/no-document');
  const result = page.getByRole('region', { name: 'Worker response' });
  await expect(result.getByRole('heading', { name: 'Latest step result' })).toBeVisible();
  await expect(result.locator('details')).not.toHaveAttribute('open', '');
  await result.getByText('Read full step response', { exact: true }).click();
  await expect(result.locator('p')).toHaveText(response);
});

test('an unnamed long request uses a compact heading and retains the exact brief', async ({ page }) => {
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/runs/unnamed') return route.fulfill({ json: { run: {
      runId: 'unnamed', dag: 'node-workflow', goal: brief, state: 'running',
      options: { runtime: 'codex-sdk', request: { workflow: 'node-workflow', nodes: [{ id: 'draft', title: 'Draft', instruction: 'Write a draft.' }] } },
      doc: { sdk: { phase: 'running', nodes: [{ id: 'draft', phase: 'running' }] } },
    }, nodes: [], outcome: null, journey: null } });
    return route.fulfill({ json: path === '/api/auth/check' ? { authRequired: false, authed: true } : path === '/api/health' ? { ok: true } : [] });
  });
  await page.goto('/runs/unnamed');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Workflow run');
  await page.getByText('Workflow brief', { exact: true }).click();
  await expect(page.locator('.sdk-run-brief p')).toHaveText(brief);
});

test('completion alone does not invent file links or an acceptance verdict', async ({ page }) => {
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/runs/missing-evidence') return route.fulfill({ json: { run: {
      runId: 'missing-evidence', dag: 'node-workflow', goal: 'Build a report', state: 'passed',
      options: { runtime: 'codex-sdk', request: { workflow: 'node-workflow', nodes: [{ id: 'build', title: 'Build report', kind: 'task', instruction: 'Create report files.', check: ['python3', '/checks/report.py'] }] } },
      doc: { sdk: { phase: 'passed', nodes: [{ id: 'build', phase: 'passed' }] } },
    }, nodes: [], outcome: null, journey: null } });
    return route.fulfill({ json: path === '/api/auth/check' ? { authRequired: false, authed: true } : path === '/api/health' ? { ok: true } : [] });
  });
  await page.goto('/runs/missing-evidence');
  const progress = page.getByRole('region', { name: 'Step progress', exact: true });
  await expect(progress).toContainText('Files produced');
  await expect(progress).not.toContainText('Acceptance check passed');
  await expect(progress.getByRole('link')).toHaveCount(0);
});
