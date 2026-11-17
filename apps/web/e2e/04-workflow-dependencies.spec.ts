import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import type { SDKWorkflow, SDKWorkflowNode } from '../src/api';
import { fulfillWorkflowValidation } from './workflowValidationFixture';

test.beforeEach(async ({ page }) => {
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/workflows/validate') return fulfillWorkflowValidation(route);
    return route.fulfill({ json: path === '/api/auth/check' ? { authRequired: false, authed: true } : path === '/api/health' ? { ok: true } : [] });
  });
});
async function describe(page: Page, titles: string[]) {
  await page.goto('/workflows');
  await page.getByLabel('What do you want to achieve?').fill('Prepare a useful cycling app proposal');
  await page.getByText('Describe your steps', { exact: true }).click();
  await page.getByLabel('What should each step do? One step per line.').fill(titles.join('\n'));
  await page.getByRole('button', { name: 'Add described steps' }).click();
}
const graph = (page: Page) => page.getByRole('region', { name: 'Workflow graph', exact: true });
// A straight chain of five steps or fewer keeps its diagram behind a toggle.
async function showDiagram(page: Page) {
  const toggle = graph(page).getByRole('button', { name: 'Show diagram', exact: true });
  if (await toggle.count()) await toggle.click();
}
async function edges(page: Page) {
  return graph(page).locator('svg > path').evaluateAll(paths => paths.map(path => [path.getAttribute('data-from'), path.getAttribute('data-to')]));
}

test('describe, connect, reorder, save and reopen a fan-out/fan-in workflow using real SDK IDs', async ({ page }, testInfo) => {
  let saved: SDKWorkflow | undefined;
  let launched: { nodes: SDKWorkflowNode[] } | undefined;
  await page.route('**/api/workflows', route => {
    if (route.request().method() === 'POST') {
      saved = { ...route.request().postDataJSON(), id: 'parallel-workflow', revision: 1 };
      return route.fulfill({ status: 201, json: saved });
    }
    return route.fulfill({ json: saved ? [saved] : [] });
  });
  await page.route('**/api/run', route => { launched = route.request().postDataJSON(); return route.fulfill({ status: 202, json: { runId: 'parallel-run' } }); });
  await page.route('**/api/runs/parallel-run', route => route.fulfill({ json: { run: {
    runId: 'parallel-run', dag: 'node-workflow', goal: 'Cycling app proposal', state: 'passed',
    options: { runtime: 'codex-sdk', request: { workflow: 'node-workflow', instruction: saved?.instruction, nodes: launched?.nodes } },
    doc: { sdk: { phase: 'passed', nodes: launched?.nodes.map(node => ({ id: node.id, phase: 'passed' })) } },
  }, nodes: [], outcome: null, journey: null } }));
  await describe(page, ['Brief', 'Research users', 'Explore options', 'Compare']);
  await page.getByLabel('Step 1 starts after').selectOption('custom');
  await page.getByLabel('Step 2 starts after').selectOption('custom');
  await page.getByLabel('Step 2 type').selectOption('research');
  await page.getByLabel('Step 3 starts after').selectOption('custom');
  const explore = page.getByRole('group', { name: 'Dependencies for Explore options', exact: true });
  await explore.getByRole('checkbox', { name: 'Research users', exact: true }).uncheck();
  await explore.getByRole('checkbox', { name: 'Brief', exact: true }).check();
  await page.getByLabel('Step 4 starts after').selectOption('custom');
  await page.getByRole('group', { name: 'Dependencies for Compare', exact: true }).getByRole('checkbox', { name: 'Research users', exact: true }).check();
  // The join can appear before its dependencies in the editing list. IDs, not
  // positions, keep explicit links intact; the SDK accepts acyclic forward refs.
  await page.getByRole('button', { name: 'Move step 4 up', exact: true }).click();
  await page.getByRole('button', { name: 'Move step 3 up', exact: true }).click();
  await expect(page.getByLabel('Step 2 name')).toHaveValue('Compare');
  await graph(page).getByRole('button', { name: /^Edit Explore options\./ }).click();
  await expect(page.getByLabel('Step 4 name')).toBeFocused();
  await page.getByLabel('Workflow name').fill('Parallel proposal');
  await page.getByRole('button', { name: 'Save workflow', exact: true }).click();
  await expect(page.getByText('Workflow saved', { exact: true })).toBeVisible();
  const [brief, compare, research, options] = saved!.nodes!;
  expect(brief.dependsOn).toEqual([]);
  expect(research.dependsOn).toEqual([brief.id]);
  expect(options.dependsOn).toEqual([brief.id]);
  expect(compare.dependsOn).toEqual([options.id, research.id]);
  const expected = [[options.id, compare.id], [research.id, compare.id], [brief.id, research.id], [brief.id, options.id]];
  expect(await edges(page)).toEqual(expected);
  await page.reload();
  await page.getByRole('button', { name: /^Parallel proposal/ }).click();
  await expect(page.getByLabel('Step 1 starts after')).toHaveValue('custom');
  await expect(page.getByRole('group', { name: 'Dependencies for Brief', exact: true }).getByRole('checkbox', { checked: true })).toHaveCount(0);
  expect(await edges(page)).toEqual(expected);
  await graph(page).scrollIntoViewIfNeeded();
  await graph(page).screenshot({ path: testInfo.outputPath('workflow-dependencies-editor.png') });
  await page.getByRole('button', { name: 'Run workflow', exact: true }).click();
  await expect(graph(page).locator('[data-state="completed"]')).toHaveCount(4);
  expect(launched?.nodes).toEqual(saved!.nodes);
  expect(await edges(page)).toEqual(expected);
  await graph(page).getByRole('link', { name: /^Compare\. Completed/ }).click();
  await expect(page).toHaveURL(new RegExp(`#sdk-step-${compare.id}$`));
  await graph(page).screenshot({ path: testInfo.outputPath('workflow-dependencies-completed.png') });
});

test('backend SDK cycle and missing-reference findings block save/run until corrected', async ({ page }) => {
  // A cycle saved elsewhere (an older definition, the console, another client)
  // still reaches the editor, and the SDK validator remains what refuses it.
  // The picker no longer offers the choices that would build one, so the
  // editor's own route to a cycle is loading one.
  const cyclic: SDKWorkflow = { id: 'cyclic', revision: 1, name: 'Correct these connections', kind: 'nodes',
    instruction: 'Prepare a useful cycling app proposal', targetRoot: '', check: [], project: '', autoRepair: false,
    nodes: [node('brief', 'Brief', ['compare']), node('explore', 'Explore', ['brief']), node('compare', 'Compare', ['explore'])] };
  await page.route('**/api/workflows', route => route.fulfill({ json: [cyclic] }));
  await page.goto('/workflows');
  await page.getByRole('button', { name: /^Correct these connections/ }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'Workflow dependencies contain a cycle' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeDisabled();
  await expect(page.getByRole('button', { name: 'Run workflow', exact: true })).toBeDisabled();
  const first = page.getByRole('group', { name: 'Dependencies for Brief', exact: true });
  const cyclingLink = first.getByRole('checkbox', { name: 'Compare', exact: true });
  await expect(cyclingLink).toBeChecked();
  await cyclingLink.click();
  // Once the loop is broken the choice that would restore it is no longer offered.
  await expect(cyclingLink).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Run workflow', exact: true })).toBeEnabled();
  await page.getByLabel('Step 3 starts after').selectOption('custom');
  await page.getByRole('button', { name: 'Remove step 2', exact: true }).click();
  // Removing a step drops the connections that pointed at it, and says so, so
  // no raw-id ghost is left behind (P-14). The dangling case is covered below.
  await expect(page.getByRole('group', { name: 'Dependencies for Compare', exact: true }).getByRole('checkbox', { name: /^Missing step:/ })).toHaveCount(0);
  await expect(page.getByRole('status').filter({ hasText: 'no longer' })).toBeVisible();
  await expect(page.getByRole('alert').filter({ hasText: 'Unknown dependency' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Run workflow', exact: true })).toBeEnabled();
});

test('a dangling dependency saved elsewhere is shown, blocks run and can be removed', async ({ page }) => {
  const dangling: SDKWorkflow = { id: 'dangling', revision: 1, name: 'Points at a removed step', kind: 'nodes',
    instruction: 'Prepare a useful cycling app proposal', targetRoot: '', check: [], project: '', autoRepair: false,
    nodes: [node('brief', 'Brief', []), node('compare', 'Compare', ['brief', 'gone'])] };
  await page.route('**/api/workflows', route => route.fulfill({ json: [dangling] }));
  await page.goto('/workflows');
  await page.getByRole('button', { name: /^Points at a removed step/ }).click();
  const missing = page.getByRole('group', { name: 'Dependencies for Compare', exact: true }).getByRole('checkbox', { name: /^Missing step:/ });
  await expect(missing).toBeChecked();
  await expect(page.getByRole('alert').filter({ hasText: 'Unknown dependency' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Run workflow', exact: true })).toBeDisabled();
  await missing.click();
  await expect(missing).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Run workflow', exact: true })).toBeEnabled();
});

const node = (id: string, title: string, dependsOn?: string[]): SDKWorkflowNode => ({ id, title, instruction: `Write ${title}`, ...(dependsOn === undefined ? {} : { dependsOn }) });

test('run graph shows concurrent checkpoints, eligible branches and blocked descendants without inventing history', async ({ page }, testInfo) => {
  const nodes = [node('brief', 'Brief', []), node('left', 'Research users', ['brief']), node('right', 'Explore options', ['brief']), node('join', 'Compare', ['left', 'right'])];
  let state = 'running';
  let phases = ['passed', 'running', 'running', 'pending'];
  await page.route('**/api/runs/branch-status', route => route.fulfill({ json: { run: {
    runId: 'branch-status', dag: 'node-workflow', goal: 'Explore a proposal', state,
    options: { runtime: 'codex-sdk', request: { workflow: 'node-workflow', nodes } },
    doc: { sdk: { phase: state, nodes: nodes.map((node, index) => ({ id: node.id, phase: phases[index] })) } },
  }, nodes: [], outcome: null, journey: null } }));
  await page.goto('/runs/branch-status');
  await expect(graph(page).locator('[data-state="active"]')).toHaveCount(2);
  await expect(graph(page).locator('[data-node-id="join"]')).toContainText('Waiting for dependencies');
  await graph(page).screenshot({ path: testInfo.outputPath('workflow-dependencies-active.png') });
  phases = ['passed', 'passed', 'pending', 'pending'];
  await page.getByRole('button', { name: 'Refresh run status', exact: true }).click();
  await expect(graph(page).locator('[data-node-id="right"]')).toContainText('Ready');
  await expect(graph(page).locator('[data-state="active"]')).toHaveCount(0);
  // An aborted peer can retain its last running checkpoint. Overall terminal
  // state must prevent the diagram from claiming it is still working.
  state = 'failed'; phases = ['passed', 'failed', 'running', 'pending'];
  await page.getByRole('button', { name: 'Refresh run status', exact: true }).click();
  await expect(graph(page).locator('[data-node-id="left"]')).toContainText('Failed');
  await expect(graph(page).locator('[data-node-id="right"]')).toContainText('Stopped');
  await expect(graph(page).locator('[data-node-id="join"]')).toContainText('Blocked by a failed step');
  await graph(page).screenshot({ path: testInfo.outputPath('workflow-dependencies-blocked.png') });
  await page.setViewportSize({ width: 390, height: 844 });
  await graph(page).scrollIntoViewIfNeeded();
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await graph(page).screenshot({ path: testInfo.outputPath('workflow-dependencies-mobile.png') });
});

test('old omitted dependencies render and save as a sequential workflow; reviewed proposals retain explicit roots', async ({ page }) => {
  const legacy: SDKWorkflow = { id: 'legacy', revision: 1, name: 'Existing sequence', kind: 'nodes', instruction: 'Write a proposal', nodes: [node('first', 'Brief'), node('second', 'Explore'), node('third', 'Compare')], targetRoot: '', check: [], project: '', autoRepair: false };
  let saved: SDKWorkflow | undefined;
  await page.route('**/api/workflows', route => route.fulfill({ json: [legacy] }));
  await page.route('**/api/workflows/legacy', route => { saved = { ...route.request().postDataJSON(), revision: 2 }; return route.fulfill({ json: saved }); });
  await page.goto('/workflows');
  await page.getByRole('button', { name: /^Existing sequence/ }).click();
  await showDiagram(page);
  expect(await edges(page)).toEqual([['first', 'second'], ['second', 'third']]);
  await page.getByLabel('Step 2 instructions').fill('Explore usefulness and costs.');
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect.poll(() => saved?.revision).toBe(2);
  expect(saved!.nodes!.every(node => !Object.hasOwn(node, 'dependsOn'))).toBe(true);
  await page.route('**/api/compose', route => route.fulfill({ json: { workflow: { ...legacy, id: '', revision: 0, name: 'Independent exploration', nodes: [node('a', 'Users', []), node('b', 'Alternatives', [])] }, unsupported: [], project: '' } }));
  await page.getByRole('button', { name: 'New workflow', exact: true }).click();
  await page.getByLabel('What do you want to achieve?').fill('Independently explore users and alternatives');
  await page.getByRole('button', { name: 'Suggest steps from goal' }).click();
  await page.getByRole('button', { name: 'Use these steps', exact: true }).click();
  await expect(graph(page).getByText('Starts with your goal', { exact: true })).toHaveCount(2);
  expect(await edges(page)).toEqual([]);
  await expect(page.getByLabel('Step 1 starts after')).toHaveValue('custom');
  await expect(page.getByLabel('Step 2 starts after')).toHaveValue('custom');
});
