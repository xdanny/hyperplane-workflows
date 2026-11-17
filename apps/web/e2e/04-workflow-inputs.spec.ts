import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import type { SDKWorkflow } from '../src/api';
import { fulfillWorkflowValidation } from './workflowValidationFixture';
import { readFileSync } from 'node:fs';
const checkCatalog = JSON.parse(readFileSync(new URL('../../../packages/agents/checks.json', import.meta.url), 'utf8'));

// Browser/API fixtures only. Filling and launching a saved workflow against a
// real database is proved by the Go tests; no model call happens here.
test.beforeEach(async ({ page }) => {
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url());
    if (url.pathname === '/api/workflows/validate') return fulfillWorkflowValidation(route);
    return route.fulfill({ json: url.pathname === '/api/auth/check' ? { authRequired: false, authed: true } : url.pathname === '/api/health' ? { ok: true } : url.pathname === '/api/workflow-checks' ? checkCatalog : [] });
  });
  await page.goto('/workflows');
});

const graph = (page: Page) => page.getByRole('region', { name: 'Workflow graph', exact: true });
async function describeSteps(page: Page, lines: string[]) {
  await page.getByText('Describe your steps', { exact: true }).click();
  await page.getByLabel('What should each step do? One step per line.').fill(lines.join('\n'));
  await page.getByRole('button', { name: 'Add described steps' }).click();
}

test('a workflow declares its run inputs, references them by token and launches from the saved definition', async ({ page }, testInfo) => {
  let stored: SDKWorkflow | undefined;
  const launches: unknown[] = [];
  await page.route('**/api/workflows**', route => {
    const method = route.request().method();
    if (new URL(route.request().url()).pathname.endsWith('/validate')) return route.fallback();
    if (method === 'GET') return route.fulfill({ json: stored ? [stored] : [] });
    stored = { ...route.request().postDataJSON(), id: 'weekly-brief', revision: (stored?.revision ?? 0) + 1 };
    return route.fulfill({ status: method === 'POST' ? 201 : 200, json: stored });
  });
  await page.route('**/api/run', route => { launches.push(route.request().postDataJSON()); return route.fulfill({ status: 202, json: { runId: 'weekly-run' } }); });
  await page.route('**/api/runs/weekly-run', route => route.fulfill({ json: { run: {
    runId: 'weekly-run', dag: 'node-workflow', goal: 'Brief the team', state: 'passed',
    options: { runtime: 'codex-sdk', request: { workflow: 'node-workflow', instruction: 'Brief the team', nodes: stored?.nodes, slots: { week: '31 August – 6 September 2026' } } },
    doc: { sdk: { phase: 'passed', nodes: stored?.nodes?.map(node => ({ id: node.id, phase: 'passed' })) } },
  }, nodes: [], outcome: null, journey: null } }));

  await page.getByLabel('What do you want to achieve?').fill('Brief the team on what changed');
  await describeSteps(page, ['Summarise what changed']);

  // Declare the one thing that differs every week.
  await page.getByRole('button', { name: 'Add an input', exact: true }).click();
  await page.getByLabel('Input 1 label', { exact: true }).fill('Week to cover');
  await expect(page.getByLabel('Input 1 id', { exact: true })).toHaveValue('weekToCover');
  await page.getByLabel('Input 1 id', { exact: true }).fill('week');
  await page.getByLabel('Input 1 hint', { exact: true }).fill('The Monday–Sunday week the brief covers');
  await page.getByLabel('Input 1 example', { exact: true }).fill('31 August – 6 September 2026');

  // The editor writes the token; nobody has to spell {slot:week} correctly.
  await page.getByLabel('Step 1 instructions', { exact: true }).click();
  await page.getByLabel('Insert an input into step 1 instructions', { exact: true }).selectOption('week');
  await expect(page.getByLabel('Step 1 instructions', { exact: true })).toHaveValue(/\{slot:week\}/);
  await expect(page.getByText('Uses inputs:', { exact: false }).first()).toBeVisible();

  // An unsaved definition cannot be filled in the browser at all.
  await expect(page.getByRole('button', { name: 'Save to run with inputs', exact: true })).toBeDisabled();
  await page.getByLabel('Workflow name', { exact: true }).fill('Weekly brief');
  await page.getByRole('button', { name: 'Save workflow', exact: true }).click();
  await expect(page.getByText('Workflow saved', { exact: true })).toBeVisible();
  expect(launches).toEqual([]);
  expect(stored?.inputs).toEqual([{ id: 'week', label: 'Week to cover', hint: 'The Monday–Sunday week the brief covers', example: '31 August – 6 September 2026', required: true, type: 'text' }]);

  // Saved and unchanged: the values are asked for at run time.
  const fill = page.getByRole('button', { name: 'Fill inputs and run', exact: true });
  await expect(fill).toBeEnabled();
  await fill.click();
  const panel = page.getByRole('region', { name: 'Fill inputs for this run', exact: true });
  await expect(panel.getByLabel('Week to cover', { exact: true })).toHaveAttribute('placeholder', '31 August – 6 September 2026');
  await expect(page.getByRole('button', { name: 'Run workflow', exact: true })).toBeDisabled();
  await expect(page.getByText('Fill Week to cover to run', { exact: true })).toBeVisible();
  await panel.getByLabel('Week to cover', { exact: true }).fill('31 August – 6 September 2026');
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: testInfo.outputPath('workflow-run-inputs.png'), fullPage: true });
  await page.getByRole('button', { name: 'Run workflow', exact: true }).click();
  await expect(page).toHaveURL(/\/runs\/weekly-run$/);
  expect(launches).toEqual([{ sourceWorkflowId: 'weekly-brief', revision: 1, slots: { week: '31 August – 6 September 2026' } }]);
});

test('an edited saved workflow cannot be filled in the browser until it is saved again', async ({ page }) => {
  const saved: SDKWorkflow = { id: 'brief', revision: 3, name: 'Weekly brief', kind: 'nodes',
    instruction: 'Brief the team on {slot:week}', targetRoot: '', check: [], project: '', autoRepair: false,
    inputs: [{ id: 'week', label: 'Week to cover', required: true, type: 'text', example: 'last week' }],
    nodes: [{ id: 'write', title: 'Write', kind: 'document', instruction: 'Summarise {slot:week}.' }] };
  await page.route('**/api/workflows**', route => new URL(route.request().url()).pathname.endsWith('/validate')
    ? route.fallback()
    : route.fulfill({ json: route.request().method() === 'GET' ? [saved] : { ...saved, revision: 4 } }));
  await page.reload();
  await page.getByRole('button', { name: /^Weekly brief/ }).click();
  await expect(page.getByRole('button', { name: 'Fill inputs and run', exact: true })).toBeEnabled();
  await page.getByLabel('Step 1 instructions', { exact: true }).fill('Summarise {slot:week} in one page.');
  await expect(page.getByRole('button', { name: 'Fill inputs and run', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Save to run with inputs', exact: true })).toBeDisabled();
  await expect(page.getByText('Save it first: a run with inputs uses the saved definition', { exact: false })).toBeVisible();
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(page.getByText('Workflow saved', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Fill inputs and run', exact: true })).toBeEnabled();
});

test('a token naming an undeclared input blocks saving until it is declared', async ({ page }) => {
  await page.getByLabel('What do you want to achieve?').fill('Brief the team on {slot:month}');
  await describeSteps(page, ['Summarise the month']);
  await page.getByLabel('Workflow name', { exact: true }).fill('Monthly brief');
  await expect(page.getByText('The token {slot:month} names an input this workflow does not declare.', { exact: false })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Save workflow', exact: true })).toBeDisabled();
  await page.getByRole('button', { name: 'Declare the inputs used by the steps', exact: true }).click();
  await page.getByRole('button', { name: 'Add an input', exact: true }).click();
  await page.getByLabel('Input 1 label', { exact: true }).fill('Month');
  await expect(page.getByLabel('Input 1 id', { exact: true })).toHaveValue('month');
  await expect(page.getByRole('button', { name: 'Save workflow', exact: true })).toBeEnabled();
  // A declared input nothing references is allowed: its value is carried as context.
  await page.getByLabel('Input 1 id', { exact: true }).fill('month-covered');
  await expect(page.getByRole('button', { name: 'Save workflow', exact: true })).toBeDisabled();
  await page.getByLabel('What do you want to achieve?').fill('Brief the team');
  await expect(page.getByText('Month is declared but never referenced by a step.', { exact: false })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Save workflow', exact: true })).toBeEnabled();
});

test('moving an approval rewires the steps that must wait for it, and says so', async ({ page }) => {
  await page.getByLabel('What do you want to achieve?').fill('Publish a reviewed brief');
  await describeSteps(page, ['Draft the brief', 'Prepare the final text']);
  await page.getByRole('button', { name: /^Approval / }).click();
  await expect(page.getByText('No step waits for this approval — the workflow will finish without your decision.', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Move step 3 up', exact: true }).click();
  await expect(page.locator('.workflow-feedback')).toContainText('moved to step 2');
  await expect(page.getByText('No step waits for this approval — the workflow will finish without your decision.', { exact: true })).toHaveCount(0);
  await expect(page.getByLabel('Step 2 type', { exact: true })).toHaveValue('approval');
});

test('the dependency picker offers only steps that cannot close a loop', async ({ page }) => {
  await page.getByLabel('What do you want to achieve?').fill('Prepare a proposal');
  await describeSteps(page, ['Research', 'Draft', 'Review']);
  await page.getByLabel('Step 1 starts after', { exact: true }).selectOption('custom');
  const first = page.getByRole('group', { name: 'Dependencies for Research', exact: true });
  await expect(first.getByRole('checkbox')).toHaveCount(0);
  await expect(first).toContainText('Steps that would then wait on this one are not listed');
  await page.getByLabel('Step 3 starts after', { exact: true }).selectOption('custom');
  const third = page.getByRole('group', { name: 'Dependencies for Review', exact: true });
  await expect(third.getByRole('checkbox')).toHaveCount(2);
  await expect(third.getByRole('checkbox', { name: 'Research', exact: true })).toBeVisible();
});

test('a straight chain hides its diagram until asked, then shows compact cards and inspects a step', async ({ page }, testInfo) => {
  await page.getByLabel('What do you want to achieve?').fill('Prepare a proposal');
  await describeSteps(page, ['Research the market', 'Draft the proposal', 'Review the draft']);
  await expect(graph(page)).toContainText('3 steps, each waiting for the one before it.');
  await expect(graph(page).locator('[data-node-id]')).toHaveCount(0);
  await graph(page).getByRole('button', { name: 'Show diagram', exact: true }).click();
  const cards = graph(page).locator('[data-node-id]');
  await expect(cards).toHaveCount(3);
  const boxes = await cards.evaluateAll(items => items.map(item => item.getBoundingClientRect()).map(box => ({ height: box.height, top: Math.round(box.top) })));
  expect(boxes.every(box => box.height <= 140)).toBe(true);
  expect(new Set(boxes.map(box => box.top)).size).toBe(1); // one row: a chain reads left to right
  await expect(cards.first()).toHaveAttribute('data-state', 'draft');
  // S-09: Inspect in the editor opens the panel it promises, not just focus.
  await graph(page).getByRole('button', { name: 'Inspect Research the market', exact: true }).click();
  const inspector = graph(page).getByRole('article');
  await expect(inspector).toContainText('Research the market');
  await expect(inspector.getByRole('button', { name: 'Return to diagram', exact: true })).toBeVisible();
  await graph(page).screenshot({ path: testInfo.outputPath('compact-chain-graph.png') });
  await graph(page).getByRole('button', { name: 'Hide diagram', exact: true }).click();
  await expect(graph(page).locator('[data-node-id]')).toHaveCount(0);
  // A branch is what a diagram is for, so it opens itself again.
  await page.getByLabel('Step 3 starts after', { exact: true }).selectOption('custom');
  await page.getByRole('group', { name: 'Dependencies for Review the draft', exact: true }).getByRole('checkbox', { name: 'Research the market', exact: true }).check();
  await page.getByRole('group', { name: 'Dependencies for Review the draft', exact: true }).getByRole('checkbox', { name: 'Draft the proposal', exact: true }).uncheck();
  await expect(graph(page).getByRole('button', { name: 'Show diagram', exact: true })).toHaveCount(0);
  await expect(graph(page).locator('[data-node-id]')).toHaveCount(3);
});

test('a saved card names what the workflow is made of and links to its runs', async ({ page }) => {
  const saved: SDKWorkflow = { id: 'brief-1', revision: 2, name: 'Weekly brief', kind: 'nodes',
    instruction: 'Brief the team on {slot:week}', targetRoot: '', check: [], project: '', autoRepair: false,
    inputs: [{ id: 'week', label: 'Week', required: true }],
    nodes: [{ id: 'a', title: 'Research', kind: 'research', instruction: 'Look' }, { id: 'b', title: 'Research more', kind: 'research', instruction: 'Look again' },
      { id: 'c', title: 'Write', kind: 'document', instruction: 'Write' }, { id: 'd', title: 'Review', kind: 'check', instruction: 'Check' }] };
  await page.route('**/api/workflows**', route => new URL(route.request().url()).pathname.endsWith('/validate') ? route.fallback() : route.fulfill({ json: [saved] }));
  await page.reload();
  await expect(page.getByRole('button', { name: /^Weekly brief/ })).toContainText('4 steps · 2 research steps · 1 document · 1 independent review · asks for 1 input');
  await expect(page.getByRole('link', { name: 'Runs of this workflow →', exact: true })).toHaveAttribute('href', '/workflows/brief-1');
});
