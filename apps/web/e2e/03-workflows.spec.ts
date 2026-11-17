import { fulfillWorkflowValidation } from './workflowValidationFixture';
import { readFileSync } from 'node:fs';
const checkCatalog = JSON.parse(readFileSync(new URL('../../../packages/agents/checks.json', import.meta.url), 'utf8'));
import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import type { ProjectCount, ProjectDetail, SDKWorkflow } from '../src/api';

async function mockSession(page: Page, id: string) {
  let state = 'running';
  await page.route(`**/api/runs/${id}`, (route) => route.fulfill({ json: {
    run: { runId: id, dag: 'checked-task', goal: 'Fix keyboard activation', state,
      startedAt: '2026-09-05T12:00:00Z', updatedAt: '2026-09-05T12:00:00Z',
      options: { runtime: 'codex-sdk', autoRepair: true, request: { workspace: '/tmp/project', check: ['node', '/checks/acceptance.mjs'] } },
      doc: { sdk: { phase: state === 'running' ? 'correction_pending' : state, threadId: 'sdk-thread-fixture', attempts: 1, findings: ['Enter must add exactly one session'], usage: { input_tokens: 10, cached_input_tokens: 2, output_tokens: 3 } } } },
    nodes: [], outcome: null, journey: null,
  } }));
  await page.route(`**/api/runs/${id}/cancel`, (route) => {
    state = 'interrupted';
    return route.fulfill({ json: { cancelled: id } });
  });
}

// Browser behavior uses API fixtures; actual SDK/PostgreSQL execution is covered
// by TestSDKDirectTaskLifecycle. No browser test spends model calls.
test.beforeEach(async ({ page }) => {
  await page.route('**/api/**', async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === '/api/workflows/validate') return fulfillWorkflowValidation(route);
    await route.fulfill({ json: url.pathname === '/api/auth/check' ? { authRequired: false, authed: true } : url.pathname === '/api/health' ? { ok: true } : url.pathname === '/api/workflow-checks' ? checkCatalog : [] });
  });
  await page.goto('/workflows');
  await page.getByRole('button', { name: 'Project task', exact: true }).click();
});

test('a task needs intent and a folder; an extra check is optional', async ({ page }, testInfo) => {
  await expect(page.getByRole('heading', { name: 'Workflows', exact: true })).toBeVisible();
  const start = page.getByRole('button', { name: 'Start task' });
  await expect(start).toBeDisabled();
  await page.getByLabel('What should change?').fill('Fix keyboard activation');
  await page.getByLabel('Working folder', { exact: true }).fill('/tmp/project');
  await expect(start).toBeEnabled();
  await page.getByLabel('Add a check program', { exact: true }).check();
  await expect(start).toBeDisabled();
  await page.getByLabel('Add a check program', { exact: true }).check();
  await page.getByLabel('Program', { exact: true }).fill('node');
  await expect(start).toBeEnabled();
  await page.screenshot({ path: testInfo.outputPath('task-desktop.png'), fullPage: true });
});

test('launch sends exact arguments directly and opens the persisted run', async ({ page }) => {
  let payload: unknown;
  await mockSession(page, 'sdk-task-fixture');
  await page.route('**/api/run', async (route) => { payload = route.request().postDataJSON(); await route.fulfill({ status: 202, json: { runId: 'sdk-task-fixture' } }); });
  await page.getByLabel('What should change?').fill('Fix keyboard activation');
  await page.getByLabel('Working folder', { exact: true }).fill('/tmp/project');
  await page.getByLabel('Add a check program', { exact: true }).check();
  await page.getByLabel('Program', { exact: true }).fill('/path with spaces/check');
  await page.getByLabel('Arguments, one per line').fill('argument with spaces\n--strict');
  await page.getByLabel('Allow one correction if the check fails').check();
  await page.getByRole('button', { name: 'Start task' }).click();
  await expect(page).toHaveURL(/\/runs\/sdk-task-fixture$/);
  await expect(page.getByRole('heading', { name: 'Fix keyboard activation' })).toBeVisible();
  await expect(page.getByLabel('Session status').getByRole('status')).toContainText('Preparing one correction');
  await expect(page.getByRole('tab', { name: 'Graph' })).toHaveCount(0);
  expect(payload).toEqual({ workflowName: '', task: 'Fix keyboard activation', targetRoot: '/tmp/project', check: ['/path with spaces/check', 'argument with spaces', '--strict'], project: '', autoRepair: true });
});

test('a session shows findings and can be stopped from its result page', async ({ page }, testInfo) => {
  await mockSession(page, 'cancel-fixture');
  await page.goto('/runs/cancel-fixture');
  await expect(page.getByText('Enter must add exactly one session')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Stop task' })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('task-session.png'), fullPage: true });
  await page.getByRole('button', { name: 'Stop task' }).click();
  await expect(page.getByLabel('Session status').getByRole('status')).toContainText('Task stopped');
  await expect(page.getByRole('button', { name: 'Stop task' })).toHaveCount(0);
  await page.getByRole('link', { name: 'Use this setup again' }).click();
  await expect(page.getByLabel('What should change?')).toHaveValue('Fix keyboard activation');
  await expect(page.getByLabel('Working folder', { exact: true })).toHaveValue('/tmp/project');
  await expect(page.getByLabel('Arguments, one per line')).toHaveValue('/checks/acceptance.mjs');
  await expect(page.getByRole('button', { name: 'Start task' })).toBeEnabled();
});

test('startup failure stays editable and explains the rejection', async ({ page }) => {
  await page.route('**/api/run', (route) => route.fulfill({ status: 400, json: { error: 'task target must be disjoint from run records' } }));
  await page.getByLabel('What should change?').fill('Fix counter');
  await page.getByLabel('Working folder', { exact: true }).fill('/bad/target');
  await page.getByLabel('Add a check program', { exact: true }).check();
  await page.getByLabel('Program', { exact: true }).fill('node');
  await page.getByRole('button', { name: 'Start task' }).click();
  await expect(page.getByRole('alert')).toContainText('disjoint from run records');
  await expect(page.getByRole('button', { name: 'Start task' })).toBeEnabled();
});

test('built-in ideator uses its own checks and workspace', async ({ page }) => {
  let payload: unknown;
  await page.route('**/api/run', async (route) => { payload = route.request().postDataJSON(); await route.fulfill({ status: 202, json: { runId: 'ideator-fixture' } }); });
  await page.getByRole('button', { name: 'App ideas', exact: true }).click();
  await expect(page.getByLabel('Working folder', { exact: true })).toHaveCount(0);
  await page.getByLabel('Who are these ideas for?').fill('Weekend cyclists');
  await page.getByRole('button', { name: 'Create app ideas' }).click();
  await expect(page).toHaveURL(/\/runs\/ideator-fixture$/);
  expect(payload).toEqual({ workflowName: '', workflow: 'app-ideator', inputs: { domain: 'Weekend cyclists' }, project: '', autoRepair: false });
});

test('the task form fits a narrow screen', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole('button', { name: 'Start task' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath('task-mobile.png'), fullPage: true });
});

test('save, reload, edit, launch and remove a reusable SDK workflow', async ({ page }, testInfo) => {
  let workflows: SDKWorkflow[] = [];
  let launches = 0;
  await page.route('**/api/workflows**', async (route) => {
    const method = route.request().method();
    if (method === 'GET') return route.fulfill({ json: workflows });
    if (method === 'DELETE') { workflows = []; return route.fulfill({ json: { removed: true } }); }
    const input = route.request().postDataJSON() as SDKWorkflow;
    const saved = { ...input, id: 'saved-fixture', revision: input.revision + 1 };
    workflows = [saved];
    return route.fulfill({ status: method === 'POST' ? 201 : 200, json: saved });
  });
  await page.route('**/api/run', async (route) => { launches++; await route.fulfill({ status: 202, json: { runId: 'saved-run' } }); });
  await mockSession(page, 'saved-run');
  await page.reload();
  await page.getByRole('button', { name: 'Project task', exact: true }).click();
  await page.getByLabel('What should change?').fill('Fix keyboard activation');
  await page.getByLabel('Working folder', { exact: true }).fill('/tmp/project');
  await page.getByLabel('Add a check program', { exact: true }).check();
  await page.getByLabel('Program', { exact: true }).fill('node');
  await page.getByLabel('Arguments, one per line').fill('/checks/acceptance.mjs');
  await page.getByLabel('Workflow name').fill('Keyboard check');
  await page.getByRole('button', { name: 'Save workflow', exact: true }).click();
  await expect(page.getByRole('status')).toHaveText('Workflow saved');
  expect(launches).toBe(0);
  await page.reload();
  await page.getByRole('button', { name: /^Keyboard check/ }).click();
  await expect(page.getByLabel('What should change?')).toHaveValue('Fix keyboard activation');
  await expect(page.getByRole('button', { name: 'Save changes' })).toBeDisabled();
  await page.getByLabel('What should change?').fill('Fix keyboard and pointer activation');
  await expect(page.getByText('Unsaved changes', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByRole('status')).toHaveText('Workflow saved');
  expect(workflows[0].revision).toBe(2);
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: testInfo.outputPath('saved-workflows.png'), fullPage: true });
  await page.getByRole('button', { name: 'Start task' }).click();
  await expect(page).toHaveURL(/\/runs\/saved-run$/);
  expect(launches).toBe(1);
  await page.goto('/workflows');
  await page.getByRole('button', { name: /^Keyboard check/ }).click();
  await page.getByRole('button', { name: 'Remove workflow', exact: true }).click();
  await page.getByRole('button', { name: 'Keep workflow' }).click();
  expect(workflows).toHaveLength(1);
  await page.getByRole('button', { name: 'Remove workflow', exact: true }).click();
  await page.getByRole('button', { name: 'Remove saved setup' }).click();
  await expect(page.getByRole('button', { name: /^Keyboard check/ })).toHaveCount(0);
});

test('stale saves keep the draft editable and loaded arguments remain exact', async ({ page }) => {
  const saved: SDKWorkflow = { id: 'existing', name: 'Exact arguments', revision: 2, kind: 'task', instruction: 'Check argument handling', targetRoot: '/tmp/project', check: ['node', '/checks/argv.mjs', '', 'two words'], project: '', autoRepair: false };
  await page.route('**/api/workflows**', route => route.request().method() === 'GET'
    ? route.fulfill({ json: [saved] })
    : route.fulfill({ status: 409, json: { error: 'Workflow changed in another tab. Reload it before saving.' } }));
  let check: unknown;
  await page.route('**/api/run', route => { check = route.request().postDataJSON().check; return route.fulfill({ status: 202, json: { runId: 'exact-run' } }); });
  await page.reload();
  await page.getByRole('button', { name: /^Exact arguments/ }).click();
  await page.getByLabel('Workflow name').fill('New name');
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByRole('alert')).toContainText('changed in another tab');
  await expect(page.getByLabel('Workflow name')).toHaveValue('New name');
  await page.getByRole('button', { name: 'Start task' }).click();
  await expect(page).toHaveURL(/\/runs\/exact-run$/);
  expect(check).toEqual(saved.check);
});

test('a project task launches without an extra check and preserves its folder when reused', async ({ page }) => {
  let launched: unknown;
  let saved: unknown;
  await page.route('**/api/workflows', route => {
    if (route.request().method() === 'POST') { const input = route.request().postDataJSON(); expect(input.check).toBeUndefined(); saved = {...input, id:'plain-project-saved', revision:1, check:null}; return route.fulfill({status:201, json:saved}); }
    return route.fulfill({json:saved ? [saved] : []});
  });
  await page.route('**/api/run', route => { launched = route.request().postDataJSON(); return route.fulfill({ status: 202, json: { runId: 'plain-project' } }); });
  await page.route('**/api/runs/plain-project', route => route.fulfill({ json: { run: { runId: 'plain-project', dag: 'checked-task', goal: 'Create an inventory', state: 'passed', options: { runtime: 'codex-sdk', request: { workspace: '/tmp/inventory-project', instruction: 'Create an inventory', check: null } }, doc: { sdk: { phase: 'passed', verification: 'not_requested' } } }, nodes: [] } }));
  await page.getByLabel('What should change?').fill('Create an inventory');
  await page.getByLabel('Working folder', { exact: true }).fill('/tmp/inventory-project');
  await expect(page.getByLabel('Add a check program', { exact: true })).not.toBeChecked();
  await page.getByLabel('Workflow name', {exact:true}).fill('Inventory project');
  await page.getByRole('button', {name:'Save workflow', exact:true}).click();
  await expect(page.getByText('Workflow saved', {exact:true})).toBeVisible();
  await page.reload();
  await page.getByRole('button', {name:/^Inventory project/}).click();
  await expect(page.getByLabel('Add a check program', {exact:true})).not.toBeChecked();
  await page.getByRole('button', { name: 'Start task', exact: true }).click();
  expect(launched).toEqual({ sourceWorkflowId: 'plain-project-saved', workflowName: 'Inventory project', task: 'Create an inventory', targetRoot: '/tmp/inventory-project', project: '', autoRepair: false });
  await expect(page.getByLabel('Session status').getByRole('status')).toContainText('Files produced');
  await expect(page.getByRole('heading', { name: 'Acceptance command' })).toHaveCount(0);
  await expect(page.getByText(/No extra check was requested/)).toBeVisible();
  await page.getByRole('link', { name: 'Use this setup again' }).click();
  await expect(page.getByLabel('Working folder', { exact: true })).toHaveValue('/tmp/inventory-project');
  await expect(page.getByLabel('Add a check program', { exact: true })).not.toBeChecked();
  await expect(page.getByRole('button', { name: 'Start task', exact: true })).toBeEnabled();
});

test('SDK run metadata drives ideator results and reuse without a synthetic DAG or legacy inspection requests', async ({ page }) => {
  const legacyRequests: string[] = [];
  page.on('request', request => {
    const path = new URL(request.url()).pathname;
    if (/\/api\/dags(?:\/|$)|\/api\/runs\/direct-ideator\/(?:graph|outputs|logs)$/.test(path)) legacyRequests.push(path);
  });
  await page.route('**/api/runs/direct-ideator', route => route.fulfill({ json: {
    run: { runId: 'direct-ideator', dag: 'display-label-only', state: 'passed',
      options: { runtime: 'codex-sdk', request: { workflow: 'app-ideator', domain: 'Accessible weekend cycling tools', workspace: '/managed/sdk-output' } },
      doc: { sdk: { phase: 'passed', verification: 'passed', attempts: 1 } } },
    nodes: [], outcome: null, journey: null,
  } }));
  await page.goto('/runs/direct-ideator');
  await expect(page.getByRole('heading', { name: 'Accessible weekend cycling tools', exact: true })).toBeVisible();
  await expect(page.getByText('Acceptance checks passed', { exact: true })).toBeVisible();
  await expect(page.getByText('Built-in checks verify the three ideas and their comparison brief.', { exact: true })).toBeVisible();
  expect(legacyRequests).toEqual([]);
  await page.getByRole('link', { name: 'Use this setup again', exact: true }).click();
  await expect(page.getByRole('button', { name: 'App ideas', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByLabel('Who are these ideas for?', { exact: true })).toHaveValue('Accessible weekend cycling tools');
  await expect(page.getByLabel('Working folder', { exact: true })).toHaveCount(0);
});

test('terminal SDK startup failures override stale starting checkpoints and do not offer an empty setup', async ({ page }) => {
  await page.route('**/api/runs/startup-failed', route => route.fulfill({ json: {
    run: { runId: 'startup-failed', dag: 'obsolete-display-label', state: 'failed',
      options: { runtime: 'codex-sdk' },
      doc: { sdk: { phase: 'starting', error: 'The SDK worker could not start: executable unavailable.' } } },
    nodes: [], outcome: null, journey: null,
  } }));
  await page.goto('/runs/startup-failed');
  await expect(page.getByRole('heading', { name: 'SDK workflow', exact: true })).toBeVisible();
  await expect(page.getByText('Task failed', { exact: true })).toBeVisible();
  await expect(page.getByRole('alert')).toContainText('executable unavailable');
  await expect(page.getByText('Starting the session', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Use this setup again', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Read session log', exact: true })).toBeEnabled();
});

test('SDK run list opens a searchable session log and preserves file links and draft skill creation', async ({ page }) => {
  const retiredRequests: string[] = [];
  const logRequests: (string | null)[] = [];
  let drafted = false;
  const run = { runId: 'sdk-reader', dag: 'checked-task', goal: 'Prepare a project summary', state: 'passed',
    startedAt: '2026-09-07T08:00:00Z', updatedAt: '2026-09-07T08:00:00Z',
    options: { runtime: 'codex-sdk', request: { workflow: 'checked-task', instruction: 'Prepare a project summary', workspace: '/tmp/project' } },
    doc: { sdk: { phase: 'passed', verification: 'not_requested', attempts: 1 } } };
  page.on('request', request => {
    const path = new URL(request.url()).pathname;
    if (/\/api\/(?:dags|processes|validate|preflight)(?:\/|$)|\/api\/runs\/sdk-reader\/(?:graph|outputs|failures|repair|events)(?:\/|$)|\/api\/runs\/sdk-reader\/logs$/.test(path)) retiredRequests.push(path);
  });
  await page.route('**/api/runs?*', route => route.fulfill({ json: [run, { ...run, runId: 'retired-graph', goal: 'Historical graph', options: {} }] }));
  await page.route('**/api/runs/sdk-reader', route => route.fulfill({ json: { run, nodes: [], outcome: { runId: run.runId, verdict: 'accepted', decisionId: 'human-accepted-sdk-reader' } } }));
  await page.route('**/api/runs/sdk-reader/artifacts', route => route.fulfill({ json: [{ name: 'summary.md', url: '/file/sdk-reader/summary.md', size: 200 }] }));
  await page.route('**/api/runs/sdk-reader/log?*', route => {
    const tail = new URL(route.request().url()).searchParams.get('tail');
    logRequests.push(tail);
    expect(new URL(route.request().url()).searchParams.get('nodeId')).toBe('delivery');
    return route.fulfill({ body: tail ? 'Summary saved.\nReview the summary before use.' : 'Session started.\nSummary saved.\nReview the summary before use.', headers: { 'Content-Type': 'text/plain', 'X-Log-Size': '30000', 'X-Log-Truncated': String(Boolean(tail)) } });
  });
  await page.route('**/api/runs/sdk-reader/extract-skill', route => {
    drafted = true;
    return route.fulfill({ json: { status: 'drafted', skill: { id: 'project-summary-method' } } });
  });
  await page.goto('/runs');
  await expect(page.getByRole('button', { name: /Historical graph/ })).toHaveCount(0);
  await page.getByRole('button', { name: 'Prepare a project summary — PASSED', exact: true }).focus();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/runs\/sdk-reader$/);
  await expect(page.getByText('Files produced', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'summary.md', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Read session log', exact: true }).click();
  const reader = page.getByRole('dialog', { name: 'Log: Session log', exact: true });
  await expect(reader).toBeVisible();
  await expect(reader).toContainText('last 500 lines');
  await reader.getByLabel('Search this log', { exact: true }).fill('summary');
  await expect(reader.locator('.log-search-count')).toHaveText('1/2');
  await reader.getByRole('button', { name: 'Next match', exact: true }).click();
  await expect(reader.locator('.log-search-count')).toHaveText('2/2');
  // "load full" became "Load earlier": the panel truncates at the tail, and
  // the earlier output is what a reader is missing (M-10).
  await reader.getByRole('button', { name: 'Load earlier lines of this log', exact: true }).click();
  await expect(reader).toContainText('Session started.');
  expect(logRequests).toEqual(['500', null]);
  await reader.getByRole('button', { name: 'Close the log reader', exact: true }).click();
  await expect(reader).toHaveCount(0);
  await page.getByRole('button', { name: 'Read session log', exact: true }).click();
  await expect(reader).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(reader).toHaveCount(0);
  await page.getByRole('button', { name: 'Draft a reusable skill from this run', exact: true }).click();
  await expect(page.getByText('draft created', { exact: true })).toBeVisible();
  await expect(page.getByText(/Completion alone does not prove correctness/)).toBeVisible();
  expect(drafted).toBe(true);
  expect(retiredRequests).toEqual([]);
});

test('completed SDK work needs an acceptance covering this run before skill drafting', async ({ page }) => {
  await page.route('**/api/runs/not-accepted', route => route.fulfill({ json: {
    run: { runId: 'not-accepted', dag: 'checked-task', state: 'passed', goal: 'Write the summary',
      options: { runtime: 'codex-sdk', request: { workflow: 'checked-task', instruction: 'Write the summary', workspace: '/tmp/project' } },
      doc: { sdk: { phase: 'passed', verification: 'not_requested' } } },
    nodes: [], outcome: null, journey: { outcome: null, recordedOutcome: { verdict: 'accepted' } },
  } }));
  await page.goto('/runs/not-accepted');
  await expect(page.getByRole('button', { name: 'Draft a reusable skill from this run', exact: true })).toHaveCount(0);
  await expect(page.getByText('A person must accept this result before a skill can be drafted.', { exact: false })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Sign off in Inbox', exact: true })).toHaveAttribute('href', '/inbox');
});


for (const kind of ['task', 'nodes', 'app-ideator'] as const) {
  test(`saved ${kind} runs the current draft and can detach an unavailable source`, async ({ page }) => {
    const original: SDKWorkflow = {
      id: `source-${kind}`, revision: 4, name: 'Saved setup', kind,
      instruction: 'Original instruction', targetRoot: kind === 'task' ? '/tmp/original' : '',
      check: kind === 'task' ? ['node', '/checks/original.mjs', '', 'two words'] : [],
      nodes: kind === 'nodes' ? [{ id: 'write', kind: 'document', title: 'Write', instruction: 'Original step' }] : undefined,
      project: '', autoRepair: false,
    };
    const launches: Record<string, unknown>[] = [];
    await page.route('**/api/workflows', route => route.fulfill({ json: [original] }));
    await page.route('**/api/projects', route => route.fulfill({ json: [{ project: 'cycling', name: 'Cycling' }] }));
    await page.route('**/api/run', route => {
      launches.push(route.request().postDataJSON());
      return route.fulfill({ status: 400, json: { error: launches.length === 1 ? 'Source workflow is no longer available.' : 'Fixture stopped before starting a worker.' } });
    });
    await page.reload();
    await page.getByRole('button', { name: /^Saved setup/ }).click();
    await page.getByLabel('Workflow name', { exact: true }).fill('Edited setup');
    await page.getByLabel('Project', { exact: true }).selectOption('cycling');
    await page.getByLabel(kind === 'nodes' ? 'Allow one correction per step if its check fails' : 'Allow one correction if the check fails').check();
    let expected: Record<string, unknown>;
    let runName: string;
    if (kind === 'task') {
      await page.getByLabel('What should change?').fill('Edited task');
      await page.getByLabel('Working folder', { exact: true }).fill('/tmp/edited');
      expected = { task: 'Edited task', targetRoot: '/tmp/edited', check: original.check };
      runName = 'Start task';
    } else if (kind === 'nodes') {
      await page.getByLabel('What do you want to achieve?').fill('Edited goal');
      await page.getByLabel('Step 1 instructions', { exact: true }).fill('Edited step');
      expected = { goal: 'Edited goal', nodes: [{ ...original.nodes![0], instruction: 'Edited step' }] };
      runName = 'Run workflow';
    } else {
      await page.getByLabel('Who are these ideas for?').fill('Edited audience');
      expected = { workflow: 'app-ideator', inputs: { domain: 'Edited audience' } };
      runName = 'Create app ideas';
    }
    expected = { workflowName: 'Edited setup', ...expected, project: 'cycling', autoRepair: true };
    await page.getByRole('button', { name: runName, exact: true }).click();
    await expect(page.getByRole('alert')).toContainText('Source workflow is no longer available');
    expect(launches).toEqual([{ sourceWorkflowId: original.id, ...expected }]);
    await page.getByRole('button', { name: 'Remove workflow', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Remove saved setup', exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Use as a new workflow', exact: true }).click();
    await expect(page.getByRole('alert')).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Remove saved setup', exact: true })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Use as a new workflow', exact: true })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Save workflow', exact: true })).toBeEnabled();
    await expect(page.getByRole('button', { name: /^Saved setup/ })).toHaveAttribute('aria-pressed', 'false');
    await page.getByRole('button', { name: runName, exact: true }).click();
    await expect(page.getByRole('alert')).toContainText('Fixture stopped');
    expect(launches).toEqual([{ sourceWorkflowId: original.id, ...expected }, expected]);
  });
}

test('detaching clears failed save and removal state and saves the intact task as a new workflow', async ({ page }) => {
  const original: SDKWorkflow = { id: 'old-source', revision: 8, name: 'Exact arguments', kind: 'task', instruction: 'Keep every field', targetRoot: '/tmp/project', check: ['node', '/checks/argv.mjs', '', 'two words'], project: '', autoRepair: true };
  let created: SDKWorkflow | undefined;
  await page.route('**/api/workflows**', route => {
    if (route.request().method() === 'GET') return route.fulfill({ json: [original] });
    if (route.request().method() === 'POST') {
      created = route.request().postDataJSON();
      return route.fulfill({ status: 201, json: { ...created, id: 'new-source', revision: 1 } });
    }
    return route.fulfill({ status: 409, json: { error: 'The original setup changed elsewhere.' } });
  });
  await page.reload();
  await page.getByRole('button', { name: /^Exact arguments/ }).click();
  await page.getByLabel('Workflow name', { exact: true }).fill('Independent copy');
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('changed elsewhere');
  await page.getByRole('button', { name: 'Remove workflow', exact: true }).click();
  await page.getByRole('button', { name: 'Remove saved setup', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Use as a new workflow', exact: true })).toBeEnabled();
  await page.getByRole('button', { name: 'Use as a new workflow', exact: true }).click();
  await expect(page.getByRole('alert')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Remove saved setup', exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Save workflow', exact: true }).click();
  await expect(page.getByText('Workflow saved', { exact: true })).toBeVisible();
  // signOff travels with every saved definition now; 'required' is what a
  // workflow that predates the setting keeps asking for (M-11).
  expect(created).toEqual({ ...original, id: '', revision: 0, name: 'Independent copy', signOff: 'required' });
});

test('project workflows use distinct backend identities, names, counts and latest run links', async ({ page }, testInfo) => {
  const entry: ProjectCount = { project: 'cycling', name: 'Cycling', registered: true, status: 'active', count: 3, runs: 3, lanes: 0, decisions: 0, workflowCount: 2 };
  const detail: ProjectDetail = { ...entry, workflows: [
    { key: 'saved:one', name: 'Explore routes', dag: 'node-workflow', runs: 2, lastRunId: 'route-latest', lastState: 'passed' },
    { key: 'saved:two', name: 'Prepare packing', dag: 'node-workflow', runs: 1, lastRunId: 'packing-latest', lastState: 'interrupted' },
  ] };
  const duplicateKeyErrors: string[] = [];
  page.on('console', message => { if (/same key/.test(message.text())) duplicateKeyErrors.push(message.text()); });
  await page.route('**/api/projects', route => route.fulfill({ json: [entry] }));
  await page.route('**/api/projects/cycling', route => route.fulfill({ json: detail }));
  await mockSession(page, 'route-latest');
  await mockSession(page, 'packing-latest');
  await page.goto('/projects/cycling');
  const projects = page.getByRole('region', { name: 'Projects', exact: true });
  const routes = projects.getByRole('button', { name: /^Explore routes/ });
  const packing = projects.getByRole('button', { name: /^Prepare packing/ });
  await expect(routes).toContainText('2 runs');
  await expect(packing).toContainText('1 run');
  await expect(projects.locator('dl').getByText('2', { exact: true })).toBeVisible();
  expect(duplicateKeyErrors).toEqual([]);
  await page.screenshot({ path: testInfo.outputPath('project-workflow-identities.png'), fullPage: true });
  await routes.click();
  await expect(page).toHaveURL(/\/runs\/route-latest$/);
  await page.goto('/projects/cycling');
  await packing.click();
  await expect(page).toHaveURL(/\/runs\/packing-latest$/);
});
