import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import type { SDKWorkflowNode } from '../src/api';
import { fulfillWorkflowValidation } from './workflowValidationFixture';

const nodes: SDKWorkflowNode[] = [
  {id: 'brief', title: 'Plan', kind: 'document', instruction: 'Preserve the original owner requirements.', dependsOn: []},
  {id: 'users', title: 'Research users', kind: 'research', instruction: 'Find evidence from users.', dependsOn: ['brief']},
  {id: 'options', title: 'Explore alternatives', kind: 'document', instruction: 'Compare possible approaches.', dependsOn: ['brief']},
  {id: 'join', title: 'Combine both branches', kind: 'document', instruction: 'Use both sources without losing their caveats.', dependsOn: ['users', 'options']},
  ...Array.from({length: 8}, (_, index): SDKWorkflowNode => ({id: `detail-${index}`, title: `Develop detail ${index + 1}`, kind: 'document', instruction: `Keep the agreed context and develop section ${index + 1}.`, dependsOn: [index ? `detail-${index - 1}` : 'join']})),
];
const graph = (page: Page) => page.getByRole('region', {name: 'Workflow graph', exact: true});
const inspector = (page: Page) => graph(page).getByRole('article');
// A straight chain of five steps or fewer keeps its diagram behind a toggle.
async function showDiagram(page: Page) {
  const toggle = graph(page).getByRole('button', {name: 'Show diagram', exact: true});
  if (await toggle.count()) await toggle.click();
}
async function fixture(page: Page, mutable = {polls: 0, removed: false}) {
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/workflows/validate') return fulfillWorkflowValidation(route);
    if (path === '/api/runs/navigation-run') {
      mutable.polls++;
      const current = mutable.removed ? nodes.filter(node => node.id !== 'detail-7') : nodes;
      return route.fulfill({json: {run: {runId: 'navigation-run', state: 'running', goal: 'A long branched workflow', dag: 'node-workflow',
        options: {runtime: 'codex-sdk', request: {workflow: 'node-workflow', instruction: 'A long branched workflow', nodes: current}},
        doc: {sdk: {phase: 'running', nodes: current.map(node => ({id: node.id, phase: node.id === 'brief' ? 'passed' : 'pending', attempts: mutable.polls}))}},
      }, nodes: [], outcome: null, journey: null}});
    }
    return route.fulfill({json: path === '/api/auth/check' ? {authRequired: false, authed: true} : path === '/api/health' ? {ok: true} : []});
  });
  return mutable;
}

test('jump, prerequisites and dependents navigate the same long branched run with back and forward', async ({page}) => {
  await fixture(page);
  await page.goto('/runs/navigation-run?view=inspect');
  await graph(page).getByLabel('Jump to step', {exact: true}).selectOption('detail-7');
  await expect(inspector(page)).toBeFocused();
  await expect(page).toHaveURL('/runs/navigation-run?view=inspect#sdk-step-detail-7');
  await inspector(page).getByLabel('Inspect another step', {exact: true}).selectOption('join');
  const inputs = inspector(page).getByRole('region', {name: 'Step inputs'});
  await expect(inputs.getByRole('button')).toHaveText(['Research users', 'Explore alternatives']);
  await inputs.getByRole('button', {name: 'Research users', exact: true}).click();
  await expect(inspector(page)).toBeFocused();
  await inspector(page).getByRole('region', {name: 'Dependent steps'}).getByRole('button', {name: 'Combine both branches', exact: true}).click();
  await expect(page).toHaveURL(/#sdk-step-join$/);
  await page.goBack();
  await expect(inspector(page).getByRole('heading', {level: 3})).toHaveText('Research users');
  await expect(inspector(page)).toBeFocused();
  await page.goBack();
  await expect(inspector(page).getByRole('heading', {level: 3})).toHaveText('Combine both branches');
  await page.goForward();
  await expect(graph(page).getByLabel('Jump to step', {exact: true})).toHaveValue('users');
  await page.goBack();
  await page.goBack();
  await expect(inspector(page).getByRole('heading', {level: 3})).toHaveText('Develop detail 8');
  await page.goBack();
  await expect(inspector(page)).toHaveCount(0);
  await expect(graph(page).locator('[data-node-id="detail-7"]').getByRole('link')).toBeFocused();
});

test('keyboard return reveals the selected deep card, closes details and keeps browser history usable', async ({page}, testInfo) => {
  await fixture(page);
  await page.setViewportSize({width: 390, height: 844});
  await page.goto('/runs/navigation-run#sdk-step-detail-7');
  await expect(inspector(page)).toBeFocused();
  await inspector(page).getByRole('button', {name: 'Return to diagram', exact: true}).focus();
  await page.keyboard.press('Enter');
  await expect(inspector(page)).toHaveCount(0);
  await expect(page).toHaveURL('/runs/navigation-run');
  const card = graph(page).locator('[data-node-id="detail-7"]').getByRole('link');
  await expect(card).toBeFocused();
  await expect(card).toBeInViewport();
  const canvas = graph(page).getByRole('region', {name: 'Dependency diagram. Scroll to see all branches.'});
  expect(await canvas.evaluate(element => element.scrollTop)).toBeGreaterThan(900);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({path: testInfo.outputPath('return-to-deep-step-mobile.png')});
  await page.goBack();
  await expect(inspector(page)).toBeFocused();
  await expect(inspector(page).getByRole('heading', {level: 3})).toHaveText('Develop detail 8');
  await page.goForward();
  await expect(inspector(page)).toHaveCount(0);
});

test('hash changes reject missing and malformed IDs; polling preserves focus and deleted selection clears', async ({page}) => {
  const live = await fixture(page);
  await page.goto('/runs/navigation-run#sdk-step-join');
  await expect(inspector(page)).toBeFocused();
  const chooser = inspector(page).getByLabel('Inspect another step', {exact: true});
  await chooser.focus();
  const previous = live.polls;
  await expect.poll(() => live.polls, {timeout: 8000}).toBeGreaterThan(previous);
  await expect(inspector(page)).toContainText('Combine both branches');
  await expect(chooser).toBeFocused();
  for (const hash of ['#sdk-step-does-not-exist', '#sdk-step-%E0%A4%A', '', '#unrelated-heading']) {
    await page.evaluate(value => { window.location.hash = value; }, hash);
    await expect(inspector(page)).toHaveCount(0);
    await page.evaluate(() => { window.location.hash = '#sdk-step-detail-7'; });
    await expect(inspector(page)).toBeFocused();
  }
  live.removed = true;
  await expect(inspector(page)).toHaveCount(0, {timeout: 8000});
  await expect(graph(page).getByLabel('Jump to step', {exact: true})).toHaveValue('');
});

test('modified run links keep native new-tab navigation and the original inspector selection', async ({page, context}) => {
  await fixture(page);
  await page.goto('/runs/navigation-run#sdk-step-join');
  const link = graph(page).locator('[data-node-id="brief"]').getByRole('link');
  // Playwright maps this to Command on macOS and Control elsewhere.
  const popupPromise = context.waitForEvent('page');
  await link.click({modifiers: ['ControlOrMeta']});
  const popup = await popupPromise;
  await expect(popup).toHaveURL(/#sdk-step-brief$/);
  await expect(page).toHaveURL(/#sdk-step-join$/);
  await expect(inspector(page).getByRole('heading', {level: 3})).toHaveText('Combine both branches');
  await popup.close();
});

test('editor inspection and dependency hops preserve every draft field and return to Inspect instead of Edit', async ({page}) => {
  await fixture(page);
  let saved: {nodes: SDKWorkflowNode[]} | undefined;
  await page.route('**/api/workflows', route => {
    if (route.request().method() === 'POST') {
      saved = route.request().postDataJSON();
      return route.fulfill({status: 201, json: {...saved, id: 'navigation-workflow', revision: 1}});
    }
    return route.fulfill({json: []});
  });
  await page.goto('/workflows');
  await page.getByLabel('What do you want to achieve?').fill('Keep my exact draft.');
  await page.getByText('Describe your steps', {exact: true}).click();
  await page.getByLabel('What should each step do? One step per line.').fill('Plan\nResearch users\nWrite report');
  await page.getByRole('button', {name: 'Add described steps'}).click();
  await page.getByLabel('Step 2 instructions').fill('Keep literal instructions.\nDo not reinterpret these criteria.');
  await page.getByLabel('Step 2 type').selectOption('research');
  await page.getByLabel('Step 1 starts after').selectOption('custom');
  await page.getByLabel('Workflow name').fill('Exact draft');
  const fields = () => page.locator('input, textarea, select').evaluateAll(elements => elements.filter(element => !element.closest('.workflow-graph')).map(element => ({tag: element.tagName, value: (element as HTMLInputElement).value, checked: (element as HTMLInputElement).checked})));
  const before = await fields();
  await showDiagram(page);
  const ids = await graph(page).locator('[data-node-id]').evaluateAll(elements => elements.map(element => element.getAttribute('data-node-id')!));
  await graph(page).getByLabel('Jump to step', {exact: true}).selectOption(ids[2]);
  await inspector(page).getByRole('region', {name: 'Step inputs'}).getByRole('button', {name: 'Research users', exact: true}).click();
  await inspector(page).getByRole('region', {name: 'Step inputs'}).getByRole('button', {name: 'Plan', exact: true}).click();
  await inspector(page).getByRole('region', {name: 'Dependent steps'}).getByRole('button', {name: 'Research users', exact: true}).click();
  await inspector(page).getByRole('button', {name: 'Return to diagram', exact: true}).click();
  await expect(graph(page).getByRole('button', {name: 'Inspect Research users', exact: true})).toBeFocused();
  await expect(page).toHaveURL('/workflows');
  expect(await fields()).toEqual(before);
  expect(await page.locator('[id]').evaluateAll(elements => { const ids = elements.map(element => element.id); return ids.length === new Set(ids).size; })).toBe(true);
  await page.getByRole('button', {name: 'Save workflow', exact: true}).click();
  await expect(page.getByText('Workflow saved', {exact: true})).toBeVisible();
  expect(saved!.nodes[0].dependsOn).toEqual([]);
  expect(saved!.nodes[1].instruction).toBe('Keep literal instructions.\nDo not reinterpret these criteria.');
  expect(saved!.nodes[1].kind).toBe('research');
  await graph(page).getByRole('button', {name: /^Edit Research users\./}).click();
  await expect(page.getByLabel('Step 2 name')).toBeFocused();
});
