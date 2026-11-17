import { fulfillWorkflowValidation, validationIdentity } from './workflowValidationFixture';
import { readFileSync } from 'node:fs';
const checkCatalog = JSON.parse(readFileSync(new URL('../../../packages/agents/checks.json', import.meta.url), 'utf8'));
import { test, expect } from '@playwright/test';
import type { Page, Locator } from '@playwright/test';
import type { SDKWorkflow } from '../src/api';

// Start the native gesture before scrolling its destination into view. A single
// dragTo can scroll away the source before Chromium dispatches dragstart.
async function dragNode(page: Page, source: Locator, target: Locator) {
  await source.hover();
  const box = await source.boundingBox();
  if (!box) throw new Error('Drag source is not visible');
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2 + 12, box.y + box.height / 2 + 12, { steps: 4 });
  await target.scrollIntoViewIfNeeded();
  await target.hover();
  await target.hover();
  await page.mouse.up();
}

// These are browser/API fixtures. SDK transport and database proof are separate.
test.beforeEach(async ({ page }) => {
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url());
    if (url.pathname === '/api/workflows/validate') return fulfillWorkflowValidation(route);
    return route.fulfill({ json: url.pathname === '/api/auth/check' ? { authRequired: false, authed: true } : url.pathname === '/api/health' ? { ok: true } : url.pathname === '/api/workflow-checks' ? checkCatalog : [] });
  });
  await page.goto('/workflows');
});

test('descriptions become editable nodes; saved order is the launched SDK order', async ({ page }, testInfo) => {
  let saved: SDKWorkflow | undefined;
  let launched: unknown;
  await page.route('**/api/workflows', route => {
    if (route.request().method() === 'POST') {
      saved = { ...route.request().postDataJSON(), id: 'saved-nodes', revision: 1 };
      return route.fulfill({ status: 201, json: saved });
    }
    return route.fulfill({ json: saved ? [saved] : [] });
  });
  await page.route('**/api/run', route => { launched = route.request().postDataJSON(); return route.fulfill({ status: 202, json: { runId: 'node-fixture' } }); });
  await page.route('**/api/runs/node-fixture', route => route.fulfill({ json: { run: {
    runId: 'node-fixture', dag: 'node-workflow', goal: 'Choose a cycling app idea', state: 'passed',
    options: { runtime: 'codex-sdk', request: { instruction: saved?.instruction, nodes: saved?.nodes } },
    doc: { sdk: { phase: 'passed', nodes: saved?.nodes?.map(n => ({ id: n.id, title: n.title, phase: 'passed', document: { path: `${n.id}.md`, bytes: 30, sha256: 'a'.repeat(64) } })), attempts: 3 } },
  }, nodes: [], outcome: null, journey: null } }));
  await expect(page.getByRole('button', { name: 'Build with steps' })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByRole('button', { name: 'Run workflow' })).toBeDisabled();
  await page.getByLabel('What do you want to achieve?').fill('Choose a cycling app idea');
  await page.getByText('Describe your steps', { exact: true }).click();
  await page.getByLabel('What should each step do? One step per line.').fill('Explore three cycling app ideas\nCompare their usefulness\nWrite a first-version plan');
  await page.getByRole('button', { name: 'Add described steps' }).click();
  await expect(page.getByLabel('Step 2 instructions')).toHaveValue('Compare their usefulness');
  await page.getByLabel('Step 2 instructions').fill('Compare usefulness, effort and offline support.');
  await page.getByLabel('Step 1 name').fill('Explore');
  await page.getByLabel('Step 2 name').fill('Compare');
  await page.getByLabel('Step 3 name').fill('Plan');
  await page.getByLabel('Workflow name').fill('Cycling app decision');
  await page.getByRole('button', { name: 'Save workflow', exact: true }).click();
  await expect(page.getByText('Workflow saved', { exact: true })).toBeVisible();
  expect(launched).toBeUndefined();
  await page.reload();
  await page.getByRole('button', { name: /^Cycling app decision/ }).click();
  await expect(page.getByLabel('Step 2 instructions')).toHaveValue('Compare usefulness, effort and offline support.');
  await expect(page.getByRole('button', { name: 'Save changes' })).toBeDisabled();
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: testInfo.outputPath('node-workflow-desktop.png'), fullPage: true });
  await page.getByRole('button', { name: 'Run workflow' }).click();
  expect(launched).toEqual({ sourceWorkflowId: saved?.id, workflowName: saved?.name, goal: saved?.instruction, nodes: saved?.nodes, project: '', autoRepair: false });
  await expect(page.getByText('All step documents produced')).toBeVisible();
  // Step documents open in the in-app reader rather than a bare browser tab.
  await expect(page.getByRole('button', { name: 'Read document' })).toHaveCount(3);
  await expect(page.getByRole('region', { name: 'Step progress', exact: true }).getByText('Document saved', { exact: true })).toHaveCount(3);
  await page.screenshot({ path: testInfo.outputPath('node-workflow-results.png'), fullPage: true });
  await page.getByRole('link', { name: 'Use this setup again' }).click();
  await expect(page.getByLabel('Step 2 instructions')).toHaveValue('Compare usefulness, effort and offline support.');
});

// Visibility alone permits off-screen elements; click() scrolls automatically.
async function expectInsideViewport(locator: Locator) {
  await expect.poll(() => locator.evaluate(element => {
    const box = element.getBoundingClientRect();
    return box.width > 0 && box.height > 0 && box.top >= 0 && box.left >= 0 && box.bottom <= innerHeight && box.right <= innerWidth;
  })).toBe(true);
}

for (const viewport of [{ name: 'desktop', width: 1280, height: 900 }, { name: 'mobile', width: 390, height: 844 }]) {
  test(`workflow actions stay reachable while editing a long workflow on ${viewport.name}`, async ({ page }, testInfo) => {
    await page.setViewportSize(viewport);
    let saved: SDKWorkflow | undefined;
    let submitted: SDKWorkflow | undefined;
    let launched: { goal: string; nodes: SDKWorkflow['nodes'] } | undefined;
    let finishSave: (() => Promise<void>) | undefined;
    let finishValidation: (() => Promise<void>) | undefined;
    let holdValidation = false;
    await page.route('**/api/workflows/validate', async route => {
      if (!holdValidation) return fulfillWorkflowValidation(route);
      await new Promise<void>(resolve => {
        finishValidation = async () => { await fulfillWorkflowValidation(route); resolve(); };
      });
    });
    await page.route('**/api/workflows', async route => {
      if (route.request().method() !== 'POST') return route.fulfill({ json: saved ? [saved] : [] });
      submitted = route.request().postDataJSON();
      await new Promise<void>(resolve => {
        finishSave = async () => {
          saved = { ...submitted!, id: 'long-workflow', revision: 1 };
          await route.fulfill({ status: 201, json: saved });
          resolve();
        };
      });
    });
    await page.route('**/api/run', route => {
      launched = route.request().postDataJSON();
      return route.fulfill({ status: 400, json: { error: 'The start failed. Your edited steps are still here.' } });
    });
    await page.getByLabel('What do you want to achieve?').fill('Prepare an accessible cycling guide');
    await page.getByText('Describe your steps', { exact: true }).click();
    await page.getByLabel('What should each step do? One step per line.').fill(Array.from({ length: 8 }, (_, index) => `Write guide section ${index + 1}`).join('\n'));
    await page.getByRole('button', { name: 'Add described steps' }).click();
    const actions = page.getByRole('region', { name: 'Workflow actions' });
    const run = actions.getByRole('button', { name: 'Run workflow', exact: true });
    await actions.getByRole('button', { name: 'Add a workflow name to save' }).click();
    await expect(page.getByLabel('Workflow name')).toBeFocused();
    await page.getByLabel('Workflow name').fill('Cycling guide');
    const middle = page.getByLabel('Step 4 instructions');
    await middle.fill('Include accessible rest stops in section four.');
    await middle.evaluate(element => element.scrollIntoView({ block: 'center' }));
    const save = actions.getByRole('button', { name: 'Save workflow', exact: true });
    await expect(save).toBeEnabled();
    await expectInsideViewport(middle);
    await expectInsideViewport(save);
    await expectInsideViewport(run);
    await page.screenshot({ path: testInfo.outputPath(`workflow-actions-${viewport.name}-middle.png`) });
    await save.click();
    await expect.poll(() => Boolean(finishSave)).toBe(true);
    await expect(actions.getByRole('button', { name: 'Saving…', exact: true })).toBeDisabled();
    await expect(run).toBeDisabled();
    await expect(middle).toBeDisabled();
    expect(launched).toBeUndefined();
    expect(submitted?.nodes?.[3].instruction).toBe('Include accessible rest stops in section four.');
    await finishSave!();
    await expect(actions.getByText('Workflow saved', { exact: true })).toBeVisible();
    await expect(actions.getByRole('button', { name: 'Save changes', exact: true })).toBeDisabled();

    holdValidation = true;
    await middle.fill('Add water access to the rest-stop guidance.');
    await middle.evaluate(element => element.scrollIntoView({ block: 'center' }));
    await expect.poll(() => Boolean(finishValidation)).toBe(true);
    await expect(actions.getByRole('button', { name: 'Save changes', exact: true })).toBeDisabled();
    await expect(run).toBeDisabled();
    holdValidation = false;
    await finishValidation!();
    await expect(run).toBeEnabled();
    await expect(actions.getByText('Unsaved changes', { exact: true })).toBeVisible();
    await expectInsideViewport(run);
    await run.focus();
    await page.keyboard.press('Enter');
    await expect.poll(() => launched?.nodes?.[3].instruction).toBe('Add water access to the rest-stop guidance.');
    expect(launched?.goal).toBe('Prepare an accessible cycling guide');
    expect(saved?.nodes?.[3].instruction).toBe('Include accessible rest stops in section four.');
    await actions.getByRole('button', { name: 'Review start error', exact: true }).click();
    await expect(page.getByRole('alert')).toBeFocused();
    await expectInsideViewport(page.getByRole('alert'));
    await expectInsideViewport(run);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(`workflow-actions-${viewport.name}-end.png`) });
  });
}

test('a short narrow viewport keeps editing space and keyboard access to workflow actions', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 320, height: 400 });
  await page.getByLabel('What do you want to achieve?').fill('Write a cycling guide');
  await page.getByRole('button', { name: /^Draft/ }).click();
  const instructions = page.getByLabel('Step 1 instructions');
  await instructions.focus();
  await page.keyboard.press('ControlOrMeta+A');
  await page.keyboard.type('Explain the route.');
  await instructions.evaluate(element => element.scrollIntoView({ block: 'center' }));
  await expectInsideViewport(instructions);
  // A short screen must retain its editing space rather than pin a large footer.
  const actions = page.getByRole('region', { name: 'Workflow actions' });
  expect(await actions.evaluate(element => element.getBoundingClientRect().top >= innerHeight)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath('workflow-actions-short-editor.png') });
  await actions.getByRole('button', { name: 'Add a workflow name to save' }).focus();
  await page.keyboard.press('Enter');
  await expect(page.getByLabel('Workflow name')).toBeFocused();
  await page.keyboard.type('Short viewport guide');
  const save = actions.getByRole('button', { name: 'Save workflow', exact: true });
  await expect(save).toBeEnabled();
  await save.focus();
  await expectInsideViewport(save);
  await page.keyboard.press('Tab');
  const run = actions.getByRole('button', { name: 'Run workflow', exact: true });
  await expect(run).toBeFocused();
  await expectInsideViewport(run);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath('workflow-actions-short-actions.png') });
});

test('drag a library node into the sequence and reorder it without losing descriptions', async ({ page }) => {
  await dragNode(page, page.getByRole('button', { name: /^Explore/ }), page.getByTestId('drop-0'));
  await expect(page.getByLabel('Step 1 name')).toHaveValue('Explore');
  await page.getByRole('button', { name: /^Draft/ }).click();
  await page.getByLabel('Step 1 instructions').fill('Explore this exact question.');
  await dragNode(page, page.getByLabel('Drag Draft', { exact: true }), page.getByTestId('drop-0'));
  await expect(page.getByLabel('Step 1 name')).toHaveValue('Draft');
  await expect(page.getByLabel('Step 2 instructions')).toHaveValue('Explore this exact question.');
  await dragNode(page, page.getByLabel('Drag Draft', { exact: true }), page.getByTestId('drop-2'));
  await expect(page.getByLabel('Step 1 name')).toHaveValue('Explore');
  await expect(page.getByLabel('Step 2 name')).toHaveValue('Draft');
});

test('mobile and keyboard users can add, move and remove nodes; launch errors keep the draft', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByLabel('What do you want to achieve?').fill('Make a practical plan');
  await page.getByRole('button', { name: /^Draft/ }).focus();
  await page.keyboard.press('Enter');
  await page.getByRole('button', { name: /^Review/ }).click();
  await page.getByRole('button', { name: 'Move step 2 up' }).focus();
  await page.keyboard.press('Enter');
  await expect(page.getByLabel('Step 1 name')).toHaveValue('Review');
  await page.getByRole('button', { name: 'Remove step 1' }).click();
  await expect(page.getByLabel('Step 1 name')).toHaveValue('Draft');
  await expect(page.getByLabel('Step 2 name')).toHaveCount(0);
  await page.route('**/api/run', route => route.fulfill({ status: 400, json: { error: 'Workflow could not start. Try again.' } }));
  await page.getByRole('button', { name: 'Run workflow' }).click();
  await expect(page.getByRole('alert')).toContainText('Try again');
  await expect(page.getByLabel('Step 1 name')).toHaveValue('Draft');
  await expect(page.getByRole('button', { name: 'Run workflow' })).toBeEnabled();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: testInfo.outputPath('node-workflow-mobile.png'), fullPage: true });
});

test('SDK suggestions stay separate until explicitly applied, then use the same node editor', async ({ page }, testInfo) => {
  let calls = 0;
  await page.route('**/api/compose', route => {
    calls++;
    expect(route.request().postDataJSON()).toEqual({ brief: 'Choose a cycling app', project: '' });
    return route.fulfill({ json: { workflow: { id: '', revision: 0, name: 'Cycling app options', kind: 'nodes', instruction: 'Choose a cycling app', nodes: [
      { id: 'explore', title: 'Explore options', instruction: 'Propose two ideas.' },
      { id: 'compare', title: 'Compare options', instruction: 'Compare explore.md and choose a first experiment.' },
    ], targetRoot: '', check: [], project: '', autoRepair: false }, unsupported: [], project: '' } });
  });
  await page.getByLabel('What do you want to achieve?').fill('Choose a cycling app');
  await page.getByRole('button', { name: /^Draft/ }).click();
  await page.getByLabel('Step 1 instructions').fill('My original instructions');
  await page.getByRole('button', { name: 'Suggest steps from goal' }).click();
  await expect(page.getByRole('heading', { name: 'Cycling app options' })).toBeVisible();
  await expect(page.getByLabel('Step 1 instructions')).toHaveValue('My original instructions');
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: testInfo.outputPath('sdk-proposal-review.png'), fullPage: true });
  await page.getByRole('button', { name: 'Replace 1 step with these steps', exact: true }).click();
  await expect(page.getByLabel('Step 1 instructions')).toHaveValue('Propose two ideas.');
  await expect(page.getByLabel('Step 2 instructions')).toHaveValue('Compare explore.md and choose a first experiment.');
  await expect(page.getByLabel('Workflow name')).toHaveValue('');
  await expect(page.getByLabel('Workflow name')).toHaveAttribute('placeholder', 'Cycling app options');
  await page.getByRole('button', { name: 'Use this name', exact: true }).click();
  await expect(page.getByLabel('Workflow name')).toHaveValue('Cycling app options');
  await page.getByLabel('Step 2 instructions').fill('Compare effort, then choose a first experiment.');
  expect(calls).toBe(1);
  await expect(page.getByRole('button', { name: 'Run workflow' })).toBeEnabled();
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: testInfo.outputPath('sdk-proposed-workflow.png'), fullPage: true });
});

test('a proposal with unsupported findings is usable anyway, and a changed goal drops it', async ({ page }) => {
  // P-01: the findings are what Hyperplane cannot do (schedule a run), not a
  // fault in the steps. Hiding the only way forward made the first proposal a
  // dead end, so the steps stay offered with the findings above them.
  await page.route('**/api/compose', route => route.fulfill({ json: {
    workflow: { name: 'Email draft', instruction: 'Write an email draft', kind: 'nodes', nodes: [{ id: 'draft', title: 'Draft', instruction: 'Write an email.' }] },
    unsupported: ['Hyperplane cannot schedule runs yet — you start the workflow yourself.'], project: '',
  } }));
  await page.getByLabel('What do you want to achieve?').fill('Write and send an email every Monday');
  await page.getByRole('button', { name: /^Draft/ }).click();
  await page.getByLabel('Step 1 name').fill('My own step');
  await page.getByRole('button', { name: 'Suggest steps from goal' }).click();
  await expect(page.getByText('Hyperplane cannot schedule runs yet — you start the workflow yourself.')).toBeVisible();
  await expect(page.getByText('The steps below are still usable', { exact: false })).toBeVisible();
  await expect(page.getByLabel('Step 1 name')).toHaveValue('My own step');
  const anyway = page.getByRole('button', { name: 'Replace 1 step with these steps anyway', exact: true });
  await expect(anyway).toBeEnabled();
  await page.getByLabel('What do you want to achieve?').fill('Write an email draft');
  await expect(page.getByText('Hyperplane cannot schedule runs yet — you start the workflow yourself.')).toHaveCount(0);
  await expect(page.getByLabel('Step 1 name')).toHaveValue('My own step');
  await page.getByRole('button', { name: 'Suggest steps from goal' }).click();
  await page.getByRole('button', { name: 'Replace 1 step with these steps anyway', exact: true }).click();
  await expect(page.getByLabel('Step 1 name')).toHaveValue('Draft');
  await expect(page.getByLabel('Workflow name', { exact: true })).toHaveValue('');
  await expect(page.getByLabel('Workflow name', { exact: true })).toHaveAttribute('placeholder', 'Email draft');
});

test('suggestion failure leaves manual authoring available', async ({ page }) => {
  await page.route('**/api/compose', route => route.fulfill({ status: 502, json: { error: 'SDK could not propose steps' } }));
  await page.getByLabel('What do you want to achieve?').fill('Choose an idea');
  await page.getByRole('button', { name: 'Suggest steps from goal' }).click();
  await expect(page.getByRole('alert')).toContainText('SDK could not propose steps');
  await page.getByRole('button', { name: /^Explore/ }).click();
  await expect(page.getByRole('button', { name: 'Run workflow' })).toBeEnabled();
});

test('stopping an in-flight suggestion prevents its late response from replacing steps', async ({ page }) => {
  let finish: (() => Promise<void>) | undefined;
  await page.route('**/api/compose', async route => {
    await new Promise<void>(resolve => {
      finish = async () => {
        await route.fulfill({ json: { workflow: { name: 'Late proposal', nodes: [{ id: 'late', title: 'Late', instruction: 'Too late' }] }, unsupported: [], project: '' } }).catch(() => {});
        resolve();
      };
    });
  });
  await page.getByLabel('What do you want to achieve?').fill('Choose an idea');
  await page.getByRole('button', { name: /^Draft/ }).click();
  await page.getByRole('button', { name: 'Suggest steps from goal' }).click();
  await expect.poll(() => Boolean(finish)).toBe(true);
  await page.getByRole('button', { name: 'Stop suggesting' }).click();
  await expect(page.getByRole('alert')).toContainText('Suggestion stopped');
  await finish!();
  await expect(page.getByRole('heading', { name: 'Late proposal' })).toHaveCount(0);
  await expect(page.getByLabel('Step 1 name')).toHaveValue('Draft');
});

test('approval nodes can be added and moved, and need a preceding document', async ({ page }, testInfo) => {
  let payload: { nodes: { kind?: string }[] } | undefined;
  await page.route('**/api/run', route => { payload = route.request().postDataJSON(); return route.fulfill({ status: 202, json: { runId: 'approval-editor' } }); });
  await page.getByLabel('What do you want to achieve?').fill('Review a draft before continuing');
  await page.getByRole('button', { name: /^Approval/ }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'Add a document before' })).toBeVisible();
  await expect(page.getByText('No step waits for this approval — the workflow will finish without your decision.', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Run workflow' })).toBeDisabled();
  await page.getByRole('button', { name: /^Draft/ }).click();
  await page.getByRole('button', { name: 'Move step 1 down' }).click();
  await expect(page.getByLabel('Step 2 type')).toHaveValue('approval');
  await expect(page.getByRole('button', { name: 'Run workflow' })).toBeEnabled();
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: testInfo.outputPath('sdk-approval-editor.png'), fullPage: true });
  await page.getByRole('button', { name: 'Run workflow' }).click();
  expect(payload?.nodes[1].kind).toBe('approval');
});

test('a described step can become a human approval without changing its instructions', async ({ page }) => {
  let payload: { nodes: { kind?: string; instruction: string }[] } | undefined;
  await page.route('**/api/run', route => { payload = route.request().postDataJSON(); return route.fulfill({ status: 202, json: { runId: 'described-approval' } }); });
  await page.getByLabel('What do you want to achieve?').fill('Choose a plan after reviewing it');
  await page.getByText('Describe your steps', { exact: true }).click();
  await page.getByLabel('What should each step do? One step per line.').fill('Draft the plan\nWait for me to approve the plan');
  await page.getByRole('button', { name: 'Add described steps' }).click();
  await page.getByLabel('Step 2 type').selectOption('approval');
  await expect(page.getByLabel('Step 2 instructions')).toHaveValue('Wait for me to approve the plan');
  await page.getByRole('button', { name: 'Run workflow' }).click();
  expect(payload?.nodes[1]).toMatchObject({ kind: 'approval', instruction: 'Wait for me to approve the plan' });
});

test('run approval shows documents and a later approval remains actionable', async ({ page }, testInfo) => {
  const decisions: string[] = [];
  let current = 1;
  let state = 'running';
  const nodes = [
    { id: 'explore', title: 'Explore options', instruction: 'Explore' },
    { id: 'approval', kind: 'approval', title: 'Approve options', instruction: 'Review options' },
    { id: 'summary', title: 'Summary', instruction: 'Summarize' },
    { id: 'final-approval', kind: 'approval', title: 'Approve summary', instruction: 'Review summary' },
  ];
  await page.route('**/api/runs/approval-run', route => route.fulfill({ json: { run: {
    runId: 'approval-run', dag: 'node-workflow', goal: 'Choose with review', state,
    options: { runtime: 'codex-sdk', request: { nodes } },
    doc: { sdk: { phase: state === 'running' ? 'approval_pending' : state,
      approval: { id: `approval-${current}`, title: nodes[current].title, prompt: nodes[current].instruction,
        documents: [{ name: 'Explore options document', url: '/file/approval-run/nodes/delivery/OUTPUTS/explore.md', sha256: 'a'.repeat(64) }] },
      nodes: nodes.map((n, i) => ({ id: n.id, title: n.title, phase: i < current ? 'passed' : i === current ? state === 'running' ? 'approval_pending' : 'rejected' : 'pending' })),
    } },
  }, nodes: [], outcome: null, journey: null } }));
  await page.route('**/api/approvals/*/decision', route => {
    decisions.push(route.request().postDataJSON().decision);
    if (current === 1) current = 3; else state = 'failed';
    return route.fulfill({ json: { state: decisions.at(-1) } });
  });
  await page.goto('/runs/approval-run');
  await expect(page.getByRole('heading', { name: 'Approve options', exact: true })).toBeVisible();
  const approval = page.getByRole('region', {name:'Approval needed', exact:true});
  await expect(approval).toContainText('Waiting for approval counts toward the run’s overall time limit.');
  await expect(approval).not.toContainText('ten-minute');
  await page.getByText('Reviewed workflow settings', {exact:true}).click();
  await expect(page.getByText(/All steps and approval waits share the server’s run time limit/)).toBeVisible();
  // Approval documents open in the in-app reader rather than a bare browser
  // tab, so they are buttons now; the raw file stays one click away inside it.
  await expect(page.getByRole('button', { name: 'Explore options document' }).first()).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('sdk-approval-run.png'), fullPage: true });
  await page.getByRole('button', { name: 'Approve and continue' }).click();
  await expect(page.getByRole('heading', { name: 'Approve summary', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Reject and stop' })).toBeEnabled();
  await page.getByRole('button', { name: 'Reject and stop' }).click();
  await expect(page.getByText('Rejected by a person', { exact: true })).toBeVisible();
  expect(decisions).toEqual(['approved', 'rejected']);
});

test('the inbox links the same approval to its documents and exact run', async ({ page }) => {
  await page.route('**/api/inbox', route => route.fulfill({ json: {
    approvals: [{ type: 'approvals', id: 'sdk-review', state: 'pending', kind: 'sdk-approval', runId: 'approval-run', doc: {
      title: 'Approve the draft', prompt: 'Review before continuing', dag: 'node-workflow', gateNode: 'approval',
      documents: [{ name: 'Draft document', url: '/file/approval-run/nodes/delivery/OUTPUTS/draft.md', sha256: 'a'.repeat(64) }],
    } }], selections: [], outcomes: [], proposals: [],
  } }));
  await page.goto('/inbox');
  await expect(page.getByRole('link', { name: 'Draft document' })).toHaveAttribute('href', '/file/approval-run/nodes/delivery/OUTPUTS/draft.md');
  await expect(page.locator('a[href="/runs/approval-run"]')).toBeVisible();
});

test('market research templates prepare editable SDK steps without starting a run', async ({ page }, testInfo) => {
  const nodes = [{ id: 'sources', title: 'Research five perspectives', instruction: 'Find sources', kind: 'research' },
    { id: 'research', title: 'Rank problems', instruction: 'Rank sources' },
    { id: 'concepts', title: 'Propose concepts', instruction: 'Propose three' },
    { id: 'review', title: 'Independent review', instruction: 'Check the evidence', kind: 'check' }];
  let launched = false;
  await page.route('**/api/templates', route => route.fulfill({ json: [{ id: 'market-template', name: 'Market research', spec: { kind: 'nodes' },
    contextSlots: [{ id: 'market', label: 'Market', required: true }, { id: 'audience', label: 'Audience', required: true }],
  }] }));
  await page.route('**/api/templates/market-template/instantiate', route => {
    expect(route.request().postDataJSON()).toMatchObject({ run: false, slots: { market: 'Cycling tools', audience: 'Weekend riders' } });
    return route.fulfill({ status: 201, json: { workflow: { id: '', revision: 0, name: 'Market research', kind: 'nodes', nodes, instruction: 'Research cycling tools for weekend riders', targetRoot: '', check: [], project: '', autoRepair: false } } });
  });
  await page.route('**/api/run', route => { launched = true; expect(route.request().postDataJSON().nodes).toEqual(nodes); return route.fulfill({ status: 202, json: { runId: 'market-run' } }); });
  await page.goto('/workflows');
  await page.getByText('Start from a workflow template', { exact: true }).click();
  await page.getByLabel('Workflow template', { exact: true }).selectOption('market-template');
  await expect(page.getByRole('button', { name: 'Use template steps' })).toBeDisabled();
  await page.getByLabel('Market', { exact: true }).fill('Cycling tools');
  await page.getByLabel('Audience', { exact: true }).fill('Weekend riders');
  await page.getByRole('button', { name: 'Use template steps' }).click();
  await expect(page.getByLabel('Step 1 type')).toHaveValue('research');
  await expect(page.getByLabel('Step 4 type')).toHaveValue('check');
  await expect(page.getByLabel('Workflow name', { exact: true })).toHaveValue('');
  await expect(page.getByLabel('Workflow name', { exact: true })).toHaveAttribute('placeholder', 'Market research');
  await page.getByLabel('Workflow name', { exact: true }).fill('Cycling market');
  await expect(page.getByLabel('Workflow name', { exact: true })).toHaveValue('Cycling market');
  expect(launched).toBe(false);
  await page.getByText('Start from a workflow template', { exact: true }).click();
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: testInfo.outputPath('sdk-market-template.png'), fullPage: true });
  await page.getByRole('button', { name: 'Run workflow' }).click();
  expect(launched).toBe(true);
});

test('research and independent review nodes work through drag and described-step editing', async ({ page }) => {
  await page.getByLabel('What do you want to achieve?').fill('Find evidence before choosing');
  await dragNode(page, page.getByRole('button', { name: /^Research / }), page.getByTestId('drop-0'));
  await expect(page.getByLabel('Step 1 type')).toHaveValue('research');
  await page.getByText('Describe your steps', { exact: true }).click();
  await page.getByLabel('What should each step do? One step per line.').fill('Reject conclusions without evidence');
  await page.getByRole('button', { name: 'Add described steps' }).click();
  await page.getByLabel('Step 2 type').selectOption('check');
  await expect(page.getByLabel('Step 2 instructions')).toHaveValue('Reject conclusions without evidence');
  await page.getByRole('button', { name: 'Move step 2 up' }).click();
  await expect(page.getByRole('alert')).toContainText('Add a document before asking for approval or review');
  await expect(page.getByRole('button', { name: 'Run workflow' })).toBeDisabled();
});

test('checked work can be dragged, configured, saved and launched with exact check arguments', async ({ page }, testInfo) => {
  let saved: SDKWorkflow | undefined;
  await page.route('**/api/workflows', route => {
    if (route.request().method() === 'POST') {
      saved = { ...route.request().postDataJSON(), id: 'checked-work', revision: 1 };
      return route.fulfill({ status: 201, json: saved });
    }
    return route.fulfill({ json: saved ? [saved] : [] });
  });
  await page.route('**/api/run', route => {
    expect(route.request().postDataJSON().nodes).toEqual(saved?.nodes);
    return route.fulfill({ status: 202, json: { runId: 'checked-work-run' } });
  });
  await page.route('**/api/runs/checked-work-run', route => route.fulfill({ json: { run: {
    runId: 'checked-work-run', dag: 'node-workflow', goal: 'Build a checked answer', state: 'passed',
    options: { runtime: 'codex-sdk', request: { nodes: saved?.nodes, instruction: saved?.instruction } },
    doc: { sdk: { phase: 'passed', nodes: saved?.nodes?.map(n => ({ id: n.id, title: n.title, phase: 'passed', verification: 'passed', document: { path: `${n.id}.md`, bytes: 30, sha256: 'a'.repeat(64) }, files: [{ path: `${n.id}/answer.txt`, bytes: 2, sha256: 'a'.repeat(64) }] })), attempts: 1 } },
  }, nodes: [], outcome: null, journey: null } }));
  await page.getByLabel('What do you want to achieve?').fill('Build a checked answer');
  await dragNode(page, page.getByRole('button', { name: /^Create files/ }), page.getByTestId('drop-0'));
  await expect(page.getByLabel('Step 1 type')).toHaveValue('task');
  await expect(page.getByRole('button', { name: 'Run workflow' })).toBeEnabled();
  await page.getByLabel('Step 1 extra check', { exact: true }).selectOption('custom');
  await page.getByLabel('Step 1 acceptance program').fill('python3');
  await page.getByLabel('Step 1 check arguments, one per line').fill('/checks/extra check.py\n--strict');
  await page.getByLabel('Workflow name', { exact: true }).fill('Checked answer');
  await page.getByRole('button', { name: 'Save workflow', exact: true }).click();
  await expect.poll(() => saved?.nodes?.[0].check).toEqual(['python3', '/checks/extra check.py', '--strict']);
  await page.screenshot({ path: testInfo.outputPath('sdk-checked-work-editor.png'), fullPage: true });
  await page.getByRole('button', { name: 'Run workflow' }).click();
  await expect(page.getByRole('region', { name: 'Step progress', exact: true }).getByText('Acceptance check passed', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Read work summary' })).toBeVisible();
  // Step files open in the in-app reader rather than a bare browser tab.
  await expect(page.getByRole('button', { name: `${saved!.nodes![0].title} · answer.txt`, exact: true })).toBeVisible();
});

test('described steps can become checked work and discard its command when changed back', async ({ page }) => {
  await page.getByLabel('What do you want to achieve?').fill('Build an output');
  await page.getByText('Describe your steps', { exact: true }).click();
  await page.getByLabel('What should each step do? One step per line.').fill('Create a CSV file');
  await page.getByRole('button', { name: 'Add described steps' }).click();
  await page.getByLabel('Step 1 type').selectOption('task');
  await expect(page.getByLabel('Step 1 instructions')).toHaveValue('Create a CSV file');
  await page.getByLabel('Step 1 extra check', { exact: true }).selectOption('custom');
  await page.getByLabel('Step 1 acceptance program').fill('python3');
  await page.getByLabel('Step 1 check arguments, one per line').fill('/checks/check.py');
  await page.getByLabel('Step 1 type').selectOption('document');
  await expect(page.getByLabel('Step 1 acceptance program')).toHaveCount(0);
  await page.getByLabel('Step 1 type').selectOption('task');
  await expect(page.getByLabel('Step 1 acceptance program')).toHaveCount(0);
  await expect(page.getByLabel('Step 1 extra check', { exact: true })).toHaveValue('none');
  await expect(page.getByRole('button', { name: 'Run workflow' })).toBeEnabled();
});

test('preparing a template protects current edits until its response arrives', async ({ page }) => {
  let release: (() => void) | undefined;
  await page.route('**/api/templates', route => route.fulfill({ json: [{ id: 'slow', name: 'Research', spec: { kind: 'nodes' }, contextSlots: [] }] }));
  await page.route('**/api/templates/slow/instantiate', async route => {
    await new Promise<void>(resolve => { release = resolve; });
    await route.fulfill({ status: 500, json: { error: 'Preparation failed' } });
  });
  await page.goto('/workflows');
  await page.getByLabel('What do you want to achieve?').fill('My existing goal');
  await page.getByRole('button', { name: /^Draft/ }).click();
  await page.getByText('Start from a workflow template', { exact: true }).click();
  await page.getByLabel('Workflow template', { exact: true }).selectOption('slow');
  await page.getByRole('button', { name: 'Replace current setup with template' }).click();
  await expect(page.getByLabel('What do you want to achieve?')).toBeDisabled();
  await expect(page.getByLabel('Step 1 instructions')).toBeDisabled();
  await expect.poll(() => Boolean(release)).toBe(true);
  release!();
  await expect(page.getByRole('alert')).toHaveText('Preparation failed');
  await expect(page.getByLabel('What do you want to achieve?')).toBeEnabled();
  await expect(page.getByLabel('What do you want to achieve?')).toHaveValue('My existing goal');
  await expect(page.getByLabel('Step 1 name')).toHaveValue('Draft');
});

test('data-quality templates use built-in checks and require approval before audit', async ({ page }, testInfo) => {
  const profile = { id: 'profile', kind: 'task', title: 'Measure data', instruction: 'Measure the real data', acceptance: 'data-quality-profile', inputs: { dataset: '/data/source.csv', keyColumns: 'id' } };
  const rules = { id: 'rules', kind: 'task', title: 'Propose rules', instruction: 'Cover every expectation', acceptance: 'data-quality-rules' };
  const approval = { id: 'approve', kind: 'approval', title: 'Approve rules', instruction: 'Review exact rules' };
  const audit = { id: 'audit', kind: 'task', title: 'Execute audit', instruction: 'Run every approved rule', acceptance: 'data-quality-audit' };
  let saved: SDKWorkflow | undefined;
  await page.route('**/api/templates', route => route.fulfill({ json: [{ id:'quality-template', name:'data-quality-audit', description:'Measure, approve rules, and execute the audit.', spec:{kind:'nodes'}, contextSlots:[{id:'dataset', label:'Data source',required:true},{id:'keyColumns',label:'Key columns',required:true}] }] }));
  await page.route('**/api/templates/quality-template/instantiate', route => {
    expect(route.request().postDataJSON().slots).toEqual({dataset:'/data/source.csv',keyColumns:'id'});
    return route.fulfill({status:201,json:{workflow:{name:'Data audit',kind:'nodes',instruction:'Audit the source data',nodes:[profile,rules,approval,audit],check:[],targetRoot:''}}});
  });
  await page.route('**/api/workflows', route => {
    if(route.request().method()==='POST'){saved={...route.request().postDataJSON(),id:'quality-saved',revision:1};return route.fulfill({status:201,json:saved});}
    return route.fulfill({json:saved?[saved]:[]});
  });
  await page.getByText('Start from a workflow template', {exact:true}).click();
  await page.getByLabel('Workflow template',{exact:true}).selectOption('quality-template');
  await page.getByLabel('Data source',{exact:true}).fill('/data/source.csv');
  await page.getByLabel('Key columns',{exact:true}).fill('id');
  await page.getByRole('button',{name:'Use template steps'}).click();
  await expect(page.getByLabel('Step 1 source data')).toHaveValue('/data/source.csv');
  await expect(page.getByLabel('Step 1 extra check',{exact:true})).toHaveValue('data-quality-profile');
  await expect(page.getByLabel('Step 1 acceptance program',{exact:true})).toHaveCount(0);
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeEnabled();
  await page.getByRole('button',{name:'Use this name',exact:true}).click();
  await page.getByRole('button',{name:'Save workflow',exact:true}).click();
  await expect.poll(()=>saved?.nodes).toEqual([profile,rules,approval,audit]);
  await page.getByRole('button',{name:'Remove step 3'}).click();
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
  await page.getByLabel('Step 1 extra check',{exact:true}).selectOption('custom');
  await expect(page.getByLabel('Step 1 acceptance program',{exact:true})).toHaveValue('');
  await expect(page.getByLabel('Step 1 source data')).toHaveCount(0);
  await page.getByLabel('Step 1 extra check',{exact:true}).selectOption('data-quality-profile');
  await page.getByLabel('Step 1 source data').fill('/data/source.csv');
  await page.getByLabel('Step 1 key columns').fill('id');
  await page.screenshot({path:testInfo.outputPath('sdk-data-quality-editor.png'),fullPage:true});
});

test('dataset-shift fields and controls survive save and launch; moving approval blocks audit', async ({page},testInfo)=>{
  const prepared:Pick<SDKWorkflow,'instruction'|'nodes'>={instruction:'Compare data windows',nodes:[
    {id:'profile',kind:'task',title:'Measure both windows',instruction:'Measure both sources.',acceptance:'dataset-shift-profile',inputs:{baselineData:'/data/baseline.csv',currentData:'/data/current.csv',keyColumns:'id',features:JSON.stringify([{feature:'category',type:'categorical',whyItMatters:'Report segmentation',statistic:'category-emergence',tolerance:'2',unit:'points_of_share'}])}},
    {id:'profile-review',kind:'check',title:'Review profiles',instruction:'Check comparability.'},
    {id:'rules',kind:'task',title:'Propose rules',instruction:'Preserve each declared tolerance.',acceptance:'dataset-shift-rules'},
    {id:'rules-review',kind:'check',title:'Review rules',instruction:'Check rule coverage.'},
    {id:'approve-rules',kind:'approval',title:'Approve the rules and fixed controls',instruction:'Review the exact files.'},
    {id:'audit',kind:'task',title:'Execute comparison',instruction:'Run approved rules.',acceptance:'dataset-shift-audit'},
    {id:'audit-review',kind:'check',title:'Review audit',instruction:'Check results and controls.'},
    {id:'owner-review',kind:'check',title:'Review impact',instruction:'Check consequences.'},
    {id:'summary',kind:'document',title:'Explain findings',instruction:'Write next actions.'},
    {id:'summary-review',kind:'check',title:'Review explanation',instruction:'Check clarity.'},
  ]};
  let saved:SDKWorkflow={...prepared,id:'shift-saved',revision:1,name:'Compare data windows',kind:'nodes',check:[],targetRoot:'',project:'',autoRepair:false};
  let launched:unknown;
  await page.route('**/api/workflows',route=>route.fulfill({json:[saved]}));
  await page.route('**/api/workflows/shift-saved',route=>{
    if(route.request().method()==='PUT')saved={...saved,...route.request().postDataJSON(),revision:saved.revision+1};
    return route.fulfill({json:saved});
  });
  await page.route('**/api/run',route=>{launched=route.request().postDataJSON();return route.fulfill({status:202,json:{runId:'shift-fixture'}});});
  await page.route('**/api/runs/shift-fixture',route=>route.fulfill({json:{run:{runId:'shift-fixture',dag:'node-workflow',goal:saved.instruction,state:'passed',options:{runtime:'codex-sdk',request:{nodes:saved.nodes}},doc:{sdk:{phase:'passed',nodes:[]}}},nodes:[]}}));
  await page.reload();await page.getByRole('button',{name:/^Compare data windows/}).click();
  await expect(page.getByLabel('Feature, row 1',{exact:true})).toHaveValue('category');
  await page.getByLabel('Step 1 baseline window').fill('/data/before.csv');
  await page.getByLabel('Step 1 current window').fill('/data/after.csv');
  await page.getByLabel('Tolerance, row 1',{exact:true}).fill('');
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
  await page.getByLabel('Tolerance, row 1',{exact:true}).fill('3');
  await page.getByText('Controls that test the detector',{exact:true}).click();
  await page.getByLabel('Control feature, row 1',{exact:true}).fill('category');
  await page.getByLabel('Multiple, row 1',{exact:true}).fill('0.5');
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeEnabled();
  await page.getByRole('button',{name:'Save changes',exact:true}).click();
  await expect(page.getByText('Workflow saved',{exact:true})).toBeVisible();
  expect(saved.nodes?.[0].inputs?.features).toContain('3');
  expect(saved.nodes?.[0].inputs?.seededControl).toContain('0.5');
  await page.reload();await page.getByRole('button',{name:/^Compare data windows/}).click();
  await expect(page.getByLabel('Tolerance, row 1',{exact:true})).toHaveValue('3');
  await expect(page.getByLabel('Step 1 baseline window')).toHaveValue('/data/before.csv');
  await dragNode(page,page.getByLabel('Drag Approve the rules and fixed controls',{exact:true}),page.getByTestId('drop-6'));
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
  await page.getByRole('button',{name:'Move step 6 up'}).click();
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeEnabled();
  await page.getByText('Controls that test the detector',{exact:true}).click();
  await expect(page.getByLabel('Multiple, row 1',{exact:true})).toHaveValue('0.5');
  await page.getByRole('article',{name:/^Step 1:/}).screenshot({path:testInfo.outputPath('sdk-shift-desktop.png')});
  await page.setViewportSize({width:390,height:844});
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.getByRole('article',{name:/^Step 1:/}).screenshot({path:testInfo.outputPath('sdk-shift-mobile.png')});
  await page.getByRole('button',{name:'Run workflow'}).click();
  expect(launched).toEqual({sourceWorkflowId:saved.id,workflowName:saved.name,goal:saved.instruction,nodes:saved.nodes,project:'',autoRepair:false});
});

test('connected checked work retains reviewed access through save and launch, and clears it when changing type', async ({page}, testInfo) => {
  let saved: SDKWorkflow | undefined;
  let launched: {nodes?: SDKWorkflow['nodes']} | undefined;
  await page.route('**/api/workflows', route => {
    if(route.request().method()==='POST') {
      saved={...route.request().postDataJSON(),id:'connected-work',revision:1};
      return route.fulfill({status:201,json:saved});
    }
    return route.fulfill({json:saved?[saved]:[]});
  });
  await page.route('**/api/run', route => {launched=route.request().postDataJSON();return route.fulfill({status:202,json:{runId:'access-fixture'}});});
  await page.route('**/api/runs/access-fixture',route=>route.fulfill({json:{run:{runId:'access-fixture',dag:'node-workflow',goal:'Measure sales',state:'passed',options:{runtime:'codex-sdk',request:{nodes:saved?.nodes,instruction:'Measure sales'}},doc:{sdk:{phase:'passed',nodes:[{id:saved?.nodes?.[0].id,phase:'passed'}]}}},nodes:[]}}));
  await page.getByLabel('What do you want to achieve?').fill('Measure sales');
  await page.getByRole('button',{name:/^Create files/}).click();
  await page.getByLabel('Step 1 extra check', { exact: true }).selectOption('custom');
  await page.getByLabel('Step 1 acceptance program').fill('python3');
  await page.getByLabel('Step 1 check arguments, one per line').fill('/checks/sales.py');
  await page.getByText('Files and connections · Off',{exact:true}).click();
  // A local read is its own grant: no host is invented to get the path field.
  await page.getByLabel('Read local files or folders in step 1',{exact:true}).check();
  await expect(page.getByLabel('Step 1 allowed hosts, one per line')).toHaveCount(0);
  await page.getByLabel('Step 1 readable inputs, one path per line').fill('/data/sales.csv');
  await page.getByLabel('Allow network hosts for step 1',{exact:true}).check();
  await page.getByLabel('Step 1 allowed hosts, one per line').fill('https://data.example.com');
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
  // M-02: the reason a step blocks the run is stated where the buttons are.
  await expect(page.getByRole('button',{name:'Fix the step marked above',exact:true})).toBeVisible();
  await page.getByLabel('Step 1 allowed hosts, one per line').fill('data.example.com\npypi.org\nfiles.pythonhosted.org');
  await page.getByLabel('Step 1 connection names, one per line').fill('DATABASE_URL');
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
  await page.getByLabel('Step 1 connection names, one per line').fill('WORKFLOW_SALES_CONNECTION');
  await page.getByLabel('Allow step 1 to install temporary tools',{exact:true}).check();
  await page.getByLabel('Workflow name').fill('Connected sales check');
  await page.getByRole('button',{name:'Save workflow',exact:true}).click();
  await expect(page.getByText('Workflow saved',{exact:true})).toBeVisible();
  expect(saved?.nodes?.[0]).toMatchObject({access:{hosts:['data.example.com','pypi.org','files.pythonhosted.org'],readPaths:['/data/sales.csv'],environment:['WORKFLOW_SALES_CONNECTION']},toolchain:true,sideEffect:true});
  await page.reload();await page.getByRole('button',{name:/^Connected sales check/}).click();
  await page.getByText('Files and connections · 1 readable path · 3 allowed hosts',{exact:true}).click();
  await expect(page.getByLabel('Allow step 1 to install temporary tools',{exact:true})).toBeChecked();
  await page.setViewportSize({width:390,height:844});
  await expect.poll(()=>page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBeTruthy();
  await page.screenshot({path:testInfo.outputPath('step-access-mobile.png'),fullPage:true});
  await page.getByRole('button',{name:'Run workflow'}).click();
  expect(launched?.nodes).toEqual(saved?.nodes);
  await page.getByText('Reviewed workflow settings',{exact:true}).click();
  await expect(page.getByText('Connection names: WORKFLOW_SALES_CONNECTION',{exact:true})).toBeVisible();
  await expect(page.getByText('Temporary tool setup: Allowed',{exact:true})).toBeVisible();
  await page.getByRole('link',{name:'Use this setup again'}).click();
  await page.getByLabel('Step 1 type').selectOption('document');
  await page.getByLabel('Step 1 type').selectOption('task');
  await page.getByText('Files and connections · Off',{exact:true}).click();
  await expect(page.getByLabel('Read local files or folders in step 1',{exact:true})).not.toBeChecked();
  await expect(page.getByLabel('Allow network hosts for step 1',{exact:true})).not.toBeChecked();
});

test('reconciliation preserves owner tolerances and source definitions through editing, approval order and launch', async ({page},testInfo)=>{
  let saved:SDKWorkflow={id:'recon-saved',revision:1,name:'Reconcile revenue',kind:'nodes',instruction:'Explain the revenue gap',check:[],targetRoot:'',project:'',autoRepair:false,nodes:[
    {id:'profile',kind:'task',title:'Measure both sources',instruction:'Read both sources independently.',acceptance:'reconciliation-profile',inputs:{sourceA:'/data/a.csv',sourceB:'/data/b.csv',sharedGrain:'one row per id',keyColumns:'id',measures:JSON.stringify([{measure:'revenue',definitionA:'Sum amount',definitionB:'Sum amount'}]),tolerances:JSON.stringify([{measure:'revenue',unit:'absolute',value:'10',segmentValue:'2',segmentUnit:'absolute',setBy:'Finance'}])}},
    {id:'rules',kind:'task',title:'Propose equations',instruction:'Use exact owner tolerances.',acceptance:'reconciliation-rules'},
    {id:'approve',kind:'approval',title:'Approve equations',instruction:'Review source definitions and rules.'},
    {id:'audit',kind:'task',title:'Reconcile',instruction:'Execute approved equations and controls.',acceptance:'reconciliation-audit'},
  ]};
  let launched:unknown;
  await page.route('**/api/workflows',route=>route.fulfill({json:[saved]}));
  await page.route('**/api/workflows/recon-saved',route=>{if(route.request().method()==='PUT')saved={...saved,...route.request().postDataJSON(),revision:saved.revision+1};return route.fulfill({json:saved});});
  await page.route('**/api/run',route=>{launched=route.request().postDataJSON();return route.fulfill({status:202,json:{runId:'recon-fixture'}});});
  await page.route('**/api/runs/recon-fixture',route=>route.fulfill({json:{run:{runId:'recon-fixture',dag:'node-workflow',goal:saved.instruction,state:'passed',options:{runtime:'codex-sdk',request:{nodes:saved.nodes}},doc:{sdk:{phase:'passed',nodes:[]}}},nodes:[]}}));
  await page.reload();await page.getByRole('button',{name:/^Reconcile revenue/}).click();
  await page.getByLabel('Step 1 source A').fill('/data/finance.csv');
  await page.getByLabel('Definition in B, row 1',{exact:true}).fill('Sum amount after refunds');
  await page.getByLabel('Per-segment unit, row 1',{exact:true}).selectOption('');
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
  await page.getByLabel('Per-segment unit, row 1',{exact:true}).selectOption('percent_of_b');
  await page.getByLabel('Per-segment value, row 1',{exact:true}).fill('0.5');
  await page.getByLabel('Difference, row 1',{exact:true}).fill('Test accounts');
  await page.getByLabel('Side, row 1',{exact:true}).selectOption('A');
  await page.getByLabel('Filter predicate, row 1',{exact:true}).fill("is_test = '1'");
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeEnabled();
  await page.getByRole('button',{name:'Save changes',exact:true}).click();
  await expect(page.getByText('Workflow saved',{exact:true})).toBeVisible();
  expect(saved.nodes?.[0].inputs?.tolerances).toContain('percent_of_b');
  await page.reload();await page.getByRole('button',{name:/^Reconcile revenue/}).click();
  await expect(page.getByLabel('Step 1 source A')).toHaveValue('/data/finance.csv');
  await expect(page.getByLabel('Definition in B, row 1',{exact:true})).toHaveValue('Sum amount after refunds');
  await expect(page.getByLabel('Per-segment value, row 1',{exact:true})).toHaveValue('0.5');
  await expect(page.getByLabel('Filter predicate, row 1',{exact:true})).toHaveValue("is_test = '1'");
  await dragNode(page,page.getByLabel('Drag Approve equations',{exact:true}),page.getByTestId('drop-4'));
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
  await page.getByRole('button',{name:'Move step 4 up'}).click();
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeEnabled();
  await page.getByRole('article',{name:/^Step 1:/}).screenshot({path:testInfo.outputPath('sdk-reconciliation-desktop.png')});
  await page.setViewportSize({width:390,height:844});
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.getByRole('article',{name:/^Step 1:/}).screenshot({path:testInfo.outputPath('sdk-reconciliation-mobile.png')});
  await page.getByRole('button',{name:'Run workflow'}).click();
  expect(launched).toEqual({sourceWorkflowId:saved.id,workflowName:saved.name,goal:saved.instruction,nodes:saved.nodes,project:'',autoRepair:false});
});

test('described pipeline changes preserve protected behavior, current inputs and approval order', async ({page},testInfo)=>{
 let saved:SDKWorkflow|undefined,launched:unknown;
 await page.route('**/api/workflows',route=>{if(route.request().method()==='POST'){saved={...route.request().postDataJSON(),id:'pipeline-change-saved',revision:1};return route.fulfill({status:201,json:saved});}return route.fulfill({json:saved?[saved]:[]});});
 await page.route('**/api/run',route=>{launched=route.request().postDataJSON();return route.fulfill({status:202,json:{runId:'pipeline-change-fixture'}});});
 await page.route('**/api/runs/pipeline-change-fixture',route=>route.fulfill({json:{run:{runId:'pipeline-change-fixture',dag:'node-workflow',goal:saved?.instruction,state:'passed',options:{runtime:'codex-sdk',request:{nodes:saved?.nodes}},doc:{sdk:{phase:'passed',nodes:[]}}},nodes:[]}}));
 await page.getByLabel('What do you want to achieve?').fill('Convert refunds while preserving US revenue');
 await page.getByText('Describe your steps',{exact:true}).click();
 await page.getByLabel('What should each step do? One step per line.').fill('Capture the current pipeline\nPlan the refund conversion\nApprove this change\nImplement and check the change');
 await page.getByRole('button',{name:'Add described steps'}).click();
 for(const [step,check] of [[1,'pipeline-change-profile'],[2,'pipeline-change-rules'],[4,'pipeline-change-audit']] as const){await page.getByLabel(`Step ${step} type`,{exact:true}).selectOption('task');await page.getByLabel(`Step ${step} extra check`,{exact:true}).selectOption(check);}
 await page.getByLabel('Step 3 type',{exact:true}).selectOption('approval');
 await page.getByLabel('Step 3 name',{exact:true}).fill('Approve this change');
 const fields={'original pipeline':'/data/pipeline.py; reads /data/orders.csv','baseline run command':'python3 pipeline.py','requested change':'Convert EU refunds with fx','what one row represents':'one row per order id','key columns':'id','confidentiality and use limits':'Synthetic fields only'};
 for(const [label,value] of Object.entries(fields))await page.getByLabel(`Step 1 ${label}`,{exact:true}).fill(value);
 await page.getByLabel('What must stay true, row 1',{exact:true}).fill('US revenue');
 await page.getByLabel('Measure or behavior, row 1',{exact:true}).fill('us_net');
 await page.getByLabel('Value type, row 1',{exact:true}).selectOption('number');
 await page.getByLabel('Tolerance unit, row 1',{exact:true}).selectOption('absolute');
 await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
 await page.getByLabel('Tolerance, row 1',{exact:true}).fill('0.01');
 await expect(page.getByRole('button',{name:'Run workflow'})).toBeEnabled();
 await page.getByRole('button',{name:'+ Add a row',exact:true}).click();
 await page.getByLabel('What must stay true, row 2',{exact:true}).fill('Contains US rows');
 await page.getByLabel('Measure or behavior, row 2',{exact:true}).fill('has_us');
 await page.getByLabel('Value type, row 2',{exact:true}).selectOption('boolean');
 await page.getByLabel('Tolerance unit, row 2',{exact:true}).selectOption('absolute');
 await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
 await page.getByLabel('Tolerance unit, row 2',{exact:true}).selectOption('exact');
 await page.getByLabel('Workflow name',{exact:true}).fill('Checked refund change');
 await page.getByRole('button',{name:'Save workflow',exact:true}).click();
 await expect(page.getByText('Workflow saved',{exact:true})).toBeVisible();
 await page.reload();await page.getByRole('button',{name:/^Checked refund change/}).click();
 await expect(page.getByLabel('Step 1 requested change',{exact:true})).toHaveValue('Convert EU refunds with fx');
 await expect(page.getByLabel('Tolerance, row 1',{exact:true})).toHaveValue('0.01');
 await expect(page.getByLabel('Value type, row 2',{exact:true})).toHaveValue('boolean');
 await dragNode(page,page.getByLabel('Drag Approve this change',{exact:true}),page.getByTestId('drop-4'));
 await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
 await page.getByRole('button',{name:'Move step 4 up'}).click();
 await expect(page.getByRole('button',{name:'Run workflow'})).toBeEnabled();
 await page.getByRole('article',{name:/^Step 1:/}).screenshot({path:testInfo.outputPath('sdk-pipeline-change-desktop.png')});
 await page.setViewportSize({width:390,height:844});
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.getByRole('article',{name:/^Step 1:/}).screenshot({path:testInfo.outputPath('sdk-pipeline-change-mobile.png')});
 await page.getByRole('button',{name:'Run workflow'}).click();
 expect(launched).toEqual({sourceWorkflowId:saved?.id,workflowName:saved?.name,goal:saved?.instruction,nodes:saved?.nodes,project:'',autoRepair:false});
});

test('described data pipelines preserve business measures, derived keys and approval when dragged',async({page},testInfo)=>{
 let saved:SDKWorkflow|undefined,launched:unknown;
 await page.route('**/api/workflows',route=>{if(route.request().method()==='POST'){saved={...route.request().postDataJSON(),id:'data-pipeline-saved',revision:1};return route.fulfill({status:201,json:saved});}return route.fulfill({json:saved?[saved]:[]});});
 await page.route('**/api/run',route=>{launched=route.request().postDataJSON();return route.fulfill({status:202,json:{runId:'data-pipeline-fixture'}});});
 await page.route('**/api/runs/data-pipeline-fixture',route=>route.fulfill({json:{run:{runId:'data-pipeline-fixture',dag:'node-workflow',goal:saved?.instruction,state:'passed',options:{runtime:'codex-sdk',request:{nodes:saved?.nodes}},doc:{sdk:{phase:'passed',nodes:[]}}},nodes:[]}}));
 await page.getByLabel('What do you want to achieve?').fill('Build daily sales tables from orders');
 await page.getByText('Describe your steps',{exact:true}).click();
 await page.getByLabel('What should each step do? One step per line.').fill('Measure orders and raw revenue\nPlan daily sales tables\nApprove the model plan\nBuild and verify daily sales');
 await page.getByRole('button',{name:'Add described steps'}).click();
 for(const [step,check] of [[1,'data-pipeline-profile'],[2,'data-pipeline-rules'],[4,'data-pipeline-audit']] as const){await page.getByLabel(`Step ${step} type`,{exact:true}).selectOption('task');await page.getByLabel(`Step ${step} extra check`,{exact:true}).selectOption(check);}
 await page.getByLabel('Step 3 type',{exact:true}).selectOption('approval');
 await page.getByLabel('Step 3 name',{exact:true}).fill('Approve the model plan');
 const fields={'source data':'/data/orders.csv','finished delivery':'SQLite database containing daily_sales','what one finished row represents':'one row per UTC order date','finished-table key columns':'order_day','maximum source lag':'0 days behind the static source','confidentiality and use limits':'No customer email in the finished tables'};
 for(const [label,value] of Object.entries(fields))await page.getByLabel(`Step 1 ${label}`,{exact:true}).fill(value);
 await page.getByLabel('Number to produce, row 1',{exact:true}).fill('net');
 await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
 await page.getByLabel('Business definition, row 1',{exact:true}).fill('Sum order amounts excluding is_test=1');
 await expect(page.getByRole('button',{name:'Run workflow'})).toBeEnabled();
 await page.getByLabel('Allowed difference unit, row 1',{exact:true}).selectOption('absolute');
 await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
 await page.getByLabel('Allowed difference, row 1',{exact:true}).fill('0.01');
 await expect(page.getByRole('button',{name:'Run workflow'})).toBeEnabled();
 await page.getByRole('button',{name:'Add a number',exact:true}).click();
 await expect(page.getByLabel('Business definition, row 2',{exact:true})).toBeVisible();
 await page.getByLabel('Number to produce, row 2',{exact:true}).fill('orders');
 await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
 await page.getByLabel('Business definition, row 2',{exact:true}).fill('Count non-test orders');
 await expect(page.getByRole('button',{name:'Run workflow'})).toBeEnabled();
 await page.getByRole('button',{name:'Remove number 2',exact:true}).click();
 await expect(page.getByLabel('Business definition, row 2',{exact:true})).toHaveCount(0);
 await page.getByText('Timing and handover options',{exact:true}).click();
 await page.getByLabel('Step 1 refresh deadline and trigger',{exact:true}).fill('06:00 UTC; external daily job');
 await page.getByLabel('Step 1 late-arriving rows',{exact:true}).fill('Up to 7 days late; restate original day');
 await page.getByLabel('Workflow name',{exact:true}).fill('Daily sales build');
 await page.getByRole('button',{name:'Save workflow',exact:true}).click();
 await expect(page.getByText('Workflow saved',{exact:true})).toBeVisible();
 expect(saved?.nodes?.[0].inputs?.refreshDeadline).toBe('06:00 UTC; external daily job');
 expect(saved?.nodes?.[0].inputs?.lateArrival).toBe('Up to 7 days late; restate original day');
 await page.reload();await page.getByRole('button',{name:/^Daily sales build/}).click();
 await expect(page.getByLabel('Step 1 finished-table key columns',{exact:true})).toHaveValue('order_day');
 await expect(page.getByLabel('Allowed difference, row 1',{exact:true})).toHaveValue('0.01');
 await dragNode(page,page.getByLabel('Drag Approve the model plan',{exact:true}),page.getByTestId('drop-4'));
 await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
 await page.getByRole('button',{name:'Move step 4 up'}).click();
 await expect(page.getByRole('button',{name:'Run workflow'})).toBeEnabled();
 await page.getByRole('article',{name:/^Step 1:/}).screenshot({path:testInfo.outputPath('sdk-data-pipeline-desktop.png')});
 await page.setViewportSize({width:390,height:844});
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.getByRole('article',{name:/^Step 1:/}).screenshot({path:testInfo.outputPath('sdk-data-pipeline-mobile.png')});
 await page.getByRole('button',{name:'Run workflow'}).click();
 expect(launched).toEqual({sourceWorkflowId:saved?.id,workflowName:saved?.name,goal:saved?.instruction,nodes:saved?.nodes,project:'',autoRepair:false});
});

test('data-pipeline catalog uses readable business fields and prepares all SDK reviews without running',async({page},testInfo)=>{
 const {readFileSync}=await import('node:fs');
 const template=JSON.parse(readFileSync(new URL('./fixtures/data-pipeline-template.json',import.meta.url),'utf8'));
 let prepared=false,launched=false;
 await page.route('**/api/templates',route=>route.fulfill({json:[{...template,id:'pipeline-template'}]}));
 await page.route('**/api/templates/pipeline-template/instantiate',route=>{
  const slots=route.request().postDataJSON().slots;
  expect(JSON.parse(slots.measures)).toEqual([{measure:'net',definition:'Sum order amounts excluding is_test=1',unit:'exact'}]);
  const nodes=structuredClone(template.spec.nodes);
  for(const node of nodes)if(node.inputs)for(const key of Object.keys(node.inputs))node.inputs[key]=slots[key]??'';
  prepared=true;
  return route.fulfill({status:201,json:{workflow:{...template.spec,name:'data-pipeline',instruction:'Build daily sales using the declared source and business definition',nodes,check:[],targetRoot:''}}});
 });
 await page.route('**/api/run',route=>{launched=true;return route.fulfill({status:500,json:{error:'This preparation test must not launch'}});});
 await page.reload();await page.getByText('Start from a workflow template',{exact:true}).click();
 await page.getByLabel('Workflow template',{exact:true}).selectOption('pipeline-template');
 for(const [label,value] of Object.entries({'Sources':'/data/orders.csv','Target store':'SQLite database under delivery/','Grain':'one row per UTC order date','Key columns':'order_day','How far behind the source the tables may fall':'0 days behind the static source','What is confidential here, and what these numbers must not be used for':'No customer email in the delivered tables'}))await page.getByLabel(label,{exact:true}).fill(value);
 await page.getByLabel('Number to produce, row 1',{exact:true}).fill('net');
 await expect(page.getByRole('button',{name:'Use template steps'})).toBeDisabled();
 await page.getByLabel('Business definition, row 1',{exact:true}).fill('Sum order amounts excluding is_test=1');
 await page.getByLabel('Allowed difference unit, row 1',{exact:true}).selectOption('exact');
 await page.setViewportSize({width:390,height:844});
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.getByLabel('Business definition, row 1',{exact:true}).scrollIntoViewIfNeeded();
 await page.screenshot({path:testInfo.outputPath('pipeline-template-measures-mobile.png')});
 await page.getByRole('button',{name:'Use template steps'}).click();
 expect(prepared).toBe(true);expect(launched).toBe(false);
 await page.getByText('Start from a workflow template',{exact:true}).click();
 await expect(page.getByLabel('Step 1 extra check',{exact:true})).toHaveValue('data-pipeline-profile');
 await expect(page.getByLabel('Step 4 type',{exact:true})).toHaveValue('approval');
 await expect(page.getByLabel('Step 5 extra check',{exact:true})).toHaveValue('data-pipeline-audit');
 for(const step of [3,7])await expect(page.getByLabel(`Step ${step} type`,{exact:true})).toHaveValue('check');
 await expect(page.getByRole('button',{name:'Run workflow'})).toBeEnabled();
 await page.getByRole('button',{name:'Remove step 4'}).click();
 await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
});

test('checked-work suggestions expose typed inputs before use and preserve approval through editing',async({page},testInfo)=>{
 const inputs={sources:'/data/orders.csv',targetStore:'SQLite database',grain:'one row per UTC date',keyColumns:'order_day',measures:JSON.stringify([{measure:'net',definition:'Sum amounts excluding test orders',unit:'exact',tolerance:'0'}]),maxLag:'0 days behind the source',safeUse:'No customer emails'};
 const reviewedInstruction = 'Build daily sales from /data/orders.csv\n\nREVIEWED RECIPE INPUTS:\nExclude test orders; preserve exact equality.';
 const nodes=[
  {id:'profile',title:'Measure sources',instruction:'Measure the declared source and independent net controls.',kind:'task',acceptance:'data-pipeline-profile',inputs},
  {id:'rules',title:'Plan tables',instruction:'Preserve the net definition and exact equality.',kind:'task',acceptance:'data-pipeline-rules'},
  {id:'plan-review',title:'Review plan',instruction:'Inspect controls and model plan.',kind:'check'},
  {id:'approve',title:'Approve the plan',instruction:'Approve before building.',kind:'approval'},
  {id:'audit',title:'Build tables',instruction:'Build and verify the approved tables.',kind:'task',acceptance:'data-pipeline-audit'},
  {id:'summary',title:'Explain results',instruction:'Preserve measured results and limits.',kind:'document'},
  {id:'review',title:'Review delivery',instruction:'Inspect actual output and controls.',kind:'check'},
 ];
 let saved:SDKWorkflow|undefined,launched=false;
 await page.route('**/api/compose',route=>route.fulfill({json:{workflow:{name:'Daily sales',kind:'nodes',instruction:reviewedInstruction,nodes},unsupported:[]}}));
 await page.route('**/api/workflows',route=>{if(route.request().method()==='POST'){saved={...route.request().postDataJSON(),id:'proposed-pipeline',revision:1};return route.fulfill({status:201,json:saved});}return route.fulfill({json:saved?[saved]:[]});});
 await page.route('**/api/run',route=>{launched=true;return route.fulfill({status:500,json:{error:'Must not run during preparation'}});});
 await page.getByLabel('What do you want to achieve?').fill('Build daily sales from /data/orders.csv');
 await page.getByRole('button',{name:'Suggest steps from goal'}).click();
 const proposed=page.getByRole('group',{name:'Proposed inputs for Measure sources'});
 await expect(proposed.getByLabel('Step 1 source data',{exact:true})).toHaveValue(inputs.sources);
 await expect(proposed.getByLabel('Business definition, row 1',{exact:true})).toHaveValue('Sum amounts excluding test orders');
 await expect(proposed.getByLabel('Business definition, row 1',{exact:true})).toBeDisabled();
 expect(saved).toBeUndefined();expect(launched).toBe(false);
 await page.setViewportSize({width:390,height:844});
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await proposed.screenshot({path:testInfo.outputPath('proposed-pipeline-inputs-mobile.png')});
 await page.getByRole('button',{name:'Use these steps',exact:true}).click();
 await expect(page.getByLabel('Step 1 extra check',{exact:true})).toHaveValue('data-pipeline-profile');
 await page.getByLabel('Business definition, row 1',{exact:true}).fill('Sum amounts excluding test orders and refunded orders');
 await page.getByRole('button',{name:'Use this name',exact:true}).click();
 await page.getByRole('button',{name:'Save workflow',exact:true}).click();
 await expect(page.getByText('Workflow saved',{exact:true})).toBeVisible();
 expect(saved!.instruction).toBe(reviewedInstruction);
 expect(JSON.parse(saved!.nodes![0].inputs!.measures)[0].definition).toContain('refunded orders');
 expect(saved!.nodes![3].kind).toBe('approval');expect(launched).toBe(false);
 await page.getByRole('button',{name:'Remove step 4'}).click();
 await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
});

test('malformed proposed business inputs stay visible but cannot be applied',async({page})=>{
 await page.route('**/api/compose',route=>route.fulfill({json:{workflow:{name:'Incomplete pipeline',instruction:'Review the current workflow',kind:'nodes',nodes:[{id:'profile',title:'Profile',instruction:'Measure',kind:'task',acceptance:'data-pipeline-profile',inputs:{sources:'/data/orders.csv',targetStore:'SQLite',grain:'day',keyColumns:'day',maxLag:'0 days',safeUse:'counts only',measures:JSON.stringify([{measure:'net',definition:'Sum',unit:'absolute',tolerance:'-1'}])}}]},unsupported:[]}}));
 await page.getByLabel('What do you want to achieve?').fill('Build a pipeline');
 await page.getByRole('button',{name:'Suggest steps from goal'}).click();
 await expect(page.getByRole('alert')).toContainText('nonnegative tolerance');
 await expect(page.getByRole('button',{name:'Use these steps',exact:true})).toHaveCount(0);
});


test('a proposal with an empty check or an approval before any work cannot replace the editor', async ({ page }) => {
  await page.route('**/api/compose', route => route.fulfill({ json: { workflow: { name: 'Invalid work', instruction: 'Review the current workflow', kind: 'nodes', nodes: [{ id: 'work', title: 'Work', instruction: 'Build a file.', kind: 'task', check: [''] }] }, unsupported: [] } }));
  await page.getByLabel('What do you want to achieve?').fill('Create a file');
  await page.getByRole('button', { name: 'Suggest steps from goal' }).click();
  await expect(page.getByRole('alert')).toContainText('program and literal arguments');
  await expect(page.getByRole('button', { name: 'Use these steps', exact: true })).toHaveCount(0);
  await page.route('**/api/compose', route => route.fulfill({ json: { workflow: { name: 'Invalid approval', instruction: 'Review the current workflow', kind: 'nodes', nodes: [{ id: 'approve', title: 'Approve', instruction: 'Review the work.', kind: 'approval' }] }, unsupported: [] } }));
  await page.getByRole('button', { name: 'Suggest steps from goal' }).click();
  await expect(page.getByRole('alert')).toContainText('Add a document before asking for approval or review');
  await expect(page.getByRole('button', { name: 'Use these steps', exact: true })).toHaveCount(0);
});

test('knowledge templates open the SDK editor even without input slots and never create a hidden draft', async ({ page }) => {
  const template = { id: 'no-input-template', name: 'Simple note', displayName: 'Simple note', description: 'Write a short note', spec: { kind: 'nodes' } };
  let prepared = false;
  await page.route('**/api/templates', route => route.fulfill({ json: [template] }));
  await page.route('**/api/templates/no-input-template/instantiate', route => {
    prepared = true;
    return route.fulfill({ json: { workflow: { id: '', name: 'Simple note', kind: 'nodes', instruction: 'Write a short note', nodes: [{ id: 'note', title: 'Note', instruction: 'Write a short note.', kind: 'document' }], check: [], targetRoot: '', project: '', autoRepair: false } } });
  });
  await page.goto('/knowledge');
  await page.getByRole('tab', { name: 'Templates', exact: true }).click();
  await page.getByRole('link', { name: 'Open workflow template', exact: true }).click();
  expect(prepared).toBe(false);
  await expect(page.getByLabel('Workflow template', { exact: true })).toHaveValue(template.id);
  await page.getByRole('button', { name: 'Use template steps', exact: true }).click();
  await expect(page.getByLabel('Step 1 instructions', { exact: true })).toHaveValue('Write a short note.');
  expect(prepared).toBe(true);
  await expect(page.getByLabel('Workflow name', { exact: true })).toHaveValue('');
  await page.getByRole('button', { name: 'Use this name', exact: true }).click();
  await expect(page.getByLabel('Workflow name', { exact: true })).toHaveValue('Simple note');
  await expect(page.getByRole('button', { name: 'Save workflow', exact: true })).toBeEnabled();
});

test('described file creation saves and launches without extra checks and reports completion honestly', async ({ page }) => {
  let saved: SDKWorkflow | undefined;
  await page.route('**/api/workflows', route => {
    if (route.request().method() === 'POST') { saved = { ...route.request().postDataJSON(), id: 'plain-files', revision: 1 }; return route.fulfill({ status: 201, json: saved }); }
    return route.fulfill({ json: saved ? [saved] : [] });
  });
  await page.route('**/api/run', route => { expect(route.request().postDataJSON().nodes).toEqual(saved?.nodes); return route.fulfill({ status: 202, json: { runId: 'plain-files-run' } }); });
  await page.route('**/api/runs/plain-files-run', route => route.fulfill({ json: { run: { runId: 'plain-files-run', dag: 'node-workflow', goal: 'Make an inventory', state: 'passed', options: { runtime: 'codex-sdk', request: { nodes: saved?.nodes } }, doc: { sdk: { phase: 'passed', nodes: [{ id: saved?.nodes?.[0].id, phase: 'passed', verification: 'not_requested' }] } } }, nodes: [] } }));
  await page.getByLabel('What do you want to achieve?').fill('Make an inventory');
  await page.getByText('Describe your steps', { exact: true }).click();
  await page.getByLabel('What should each step do? One step per line.').fill('Create an inventory CSV using my column names');
  await page.getByRole('button', { name: 'Add described steps' }).click();
  await page.getByLabel('Step 1 type', { exact: true }).selectOption('task');
  await expect(page.getByLabel('Step 1 extra check', { exact: true })).toHaveValue('none');
  await expect(page.getByLabel('Step 1 acceptance program')).toHaveCount(0);
  await page.getByLabel('Workflow name', { exact: true }).fill('Inventory files');
  await page.getByRole('button', { name: 'Save workflow', exact: true }).click();
  await expect(page.getByText('Workflow saved', { exact: true })).toBeVisible();
  expect(saved?.nodes?.[0].instruction).toBe('Create an inventory CSV using my column names');
  expect(saved?.nodes?.[0].check).toBeUndefined();
  expect(saved?.nodes?.[0].acceptance).toBeUndefined();
  await page.reload();
  await page.getByRole('button', { name: /^Inventory files/ }).click();
  await page.getByRole('button', { name: 'Run workflow' }).click();
  await expect(page.getByRole('region', { name: 'Step progress', exact: true }).getByText('Files produced', { exact: true })).toBeVisible();
  await expect(page.getByText('Acceptance check passed', { exact: true })).toHaveCount(0);
  await page.getByText('What these statuses mean', { exact: true }).click();
  await expect(page.getByText(/An attached program check tests exactly what its command asserts/)).toBeVisible();
  await expect(page.getByText(/A step that saved a document proves a response was saved, not that it is correct/)).toBeVisible();
});

test('catalog requirements are visible, preserve instructions, and initialize new check inputs without domain-specific UI', async ({ page }) => {
  const description = 'The output must name the supplied schema and count every record. Do not infer missing columns.';
  const checks = [{ id: 'schema-match', title: 'Match a schema', description, inputs: { required: ['schema'], optional: ['notes'] } }];
  await page.route('**/api/workflow-checks', route => route.fulfill({ json: { checks } }));
  await page.route('**/api/workflows/validate', route => route.fulfill({json:{...validationIdentity,valid:true}}));
  await page.route('**/api/compose', route => route.fulfill({ json: { workflow: { name: 'Schema task', instruction: 'Review the current workflow', kind: 'nodes', nodes: [{ id: 'match', title: 'Create inventory', kind: 'task', instruction: 'Keep my exact inventory instructions.', acceptance: 'schema-match', inputs: { schema: 'id, description' } }] }, unsupported: [] } }));
  await page.reload();
  await page.getByLabel('What do you want to achieve?').fill('Make an inventory');
  await page.getByRole('button', { name: 'Suggest steps from goal' }).click();
  await page.getByText('Extra check requirements · Match a schema', { exact: true }).click();
  await expect(page.getByText(description, { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Use these steps', exact: true }).click();
  await expect(page.getByLabel('Step 1 instructions', { exact: true })).toHaveValue('Keep my exact inventory instructions.');
  await expect(page.getByLabel('Step 1 schema', { exact: true })).toHaveValue('id, description');
  await page.getByText('Extra check requirements · Match a schema', { exact: true }).click();
  await expect(page.getByText(description, { exact: true })).toBeVisible();
  await page.getByLabel('Step 1 extra check', { exact: true }).selectOption('none');
  await expect(page.getByLabel('Step 1 schema', { exact: true })).toHaveCount(0);
  await expect(page.getByLabel('Step 1 instructions', { exact: true })).toHaveValue('Keep my exact inventory instructions.');
  await page.getByLabel('Step 1 extra check', { exact: true }).selectOption('schema-match');
  await expect(page.getByLabel('Step 1 schema', { exact: true })).toHaveValue('');
  await expect(page.getByRole('button', { name: 'Run workflow' })).toBeDisabled();
  await page.getByLabel('Step 1 schema', { exact: true }).fill('id, description');
  await expect(page.getByRole('button', { name: 'Run workflow' })).toBeEnabled();
});

test('catalog failure preserves attached checks and blocks use until retry succeeds', async ({ page }) => {
  const saved = { id: 'saved-check', revision: 1, name: 'Saved check', kind: 'nodes', instruction: 'Create inventory', check: [], targetRoot: '', project: '', autoRepair: false, nodes: [{ id: 'match', title: 'Inventory', instruction: 'Preserve these exact instructions.', kind: 'task', acceptance: 'schema-match', inputs: { schema: 'id' } }] };
  let ready = false;
  await page.route('**/api/workflow-checks', route => ready ? route.fulfill({ json: { checks: [{ id: 'schema-match', title: 'Match a schema', description: 'Require the supplied schema.', inputs: { required: ['schema'] } }] } }) : route.fulfill({ status: 503, json: { error: 'Catalog offline' } }));
  await page.route('**/api/workflows/validate', route => route.fulfill({json:{...validationIdentity,valid:true}}));
  await page.route('**/api/workflows', route => route.fulfill({ json: [saved] }));
  await page.reload();
  await page.getByRole('button', { name: /^Saved check/ }).click();
  await expect(page.getByLabel('Step 1 extra check', { exact: true })).toHaveValue('schema-match');
  await expect(page.getByLabel('Step 1 instructions', { exact: true })).toHaveValue('Preserve these exact instructions.');
  await page.getByLabel('Workflow name', { exact: true }).fill('Renamed check');
  await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeDisabled();
  await expect(page.getByRole('button', { name: 'Run workflow' })).toBeDisabled();
  ready = true;
  await page.getByRole('button', { name: 'Retry check catalog', exact: true }).click();
  await expect(page.getByLabel('Step 1 schema', { exact: true })).toHaveValue('id');
  await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeEnabled();
  await expect(page.getByRole('button', { name: 'Run workflow' })).toBeEnabled();
});

test('multibyte workflow text is preserved and byte limits block saves and launches before the API', async ({ page }) => {
  let saved: SDKWorkflow | undefined;
  await page.route('**/api/workflows', route => {
    if (route.request().method() === 'POST') { saved = {...route.request().postDataJSON(), id:'unicode-workflow', revision:1}; return route.fulfill({status:201,json:saved}); }
    return route.fulfill({json:saved ? [saved] : []});
  });
  const name = 'é'.repeat(80), goal = '😀'.repeat(4000);
  await page.getByLabel('What do you want to achieve?').fill('Create a note');
  await page.getByRole('button', {name:/^Draft/}).click();
  await page.getByLabel('Workflow name', {exact:true}).fill(name);
  const save = page.getByRole('button', {name:'Save workflow', exact:true}), run = page.getByRole('button', {name:'Run workflow', exact:true});
  await page.getByLabel('Step 1 name', {exact:true}).fill(name + 'é');
  await expect(page.getByRole('alert')).toContainText('step name to fit the 160-byte limit');
  await expect(save).toBeDisabled(); await expect(run).toBeDisabled();
  await expect(page.getByLabel('Step 1 name', {exact:true})).toHaveValue(name + 'é');
  await page.getByLabel('Step 1 name', {exact:true}).fill(name);
  await expect(save).toBeEnabled();
  await page.getByLabel('Step 1 instructions', {exact:true}).fill(goal + 'a');
  await expect(page.getByRole('alert')).toContainText('step instructions to fit the 16000-byte limit');
  await expect(run).toBeDisabled();
  await page.getByLabel('Step 1 instructions', {exact:true}).fill(goal);
  await page.getByLabel('What do you want to achieve?').fill(goal + 'a');
  await expect(page.getByRole('alert')).toContainText('goal to fit the 16000-byte limit');
  await expect(page.getByRole('button', {name:'Suggest steps from goal', exact:true})).toBeDisabled();
  await expect(save).toBeDisabled(); await expect(run).toBeDisabled();
  await page.getByLabel('What do you want to achieve?').fill(goal);
  await page.getByLabel('Workflow name', {exact:true}).fill(name + 'é');
  await expect(page.getByRole('alert')).toContainText('workflow name to fit the 160-byte limit');
  await expect(save).toBeDisabled(); await expect(run).toBeDisabled();
  await page.getByRole('button', {name:'Review workflow name', exact:true}).click();
  await expect(page.getByLabel('Workflow name', {exact:true})).toBeFocused();
  await page.getByLabel('Workflow name', {exact:true}).fill(name);
  await save.click();
  await expect(page.getByText('Workflow saved', {exact:true})).toBeVisible();
  expect(saved?.name).toBe(name); expect(saved?.instruction).toBe(goal);
  expect(saved?.nodes?.[0].title).toBe(name); expect(saved?.nodes?.[0].instruction).toBe(goal);
});

test('ML catalog prepares the common editable workflow and preserves its saved definition through launch and approval', async ({ page }, testInfo) => {
  const template = JSON.parse(readFileSync(new URL('./fixtures/ml-engineering-template.json', import.meta.url), 'utf8'));
  const predictionCheck = checkCatalog.checks.find((check: { id: string }) => check.id === 'prediction-metrics');
  expect(predictionCheck).toBeTruthy();
  let prepared: SDKWorkflow | undefined;
  let saved: SDKWorkflow | undefined;
  let launched: unknown;
  let decision: unknown;
  await page.route('**/api/templates', route => route.fulfill({ json: [{ ...template, id: 'ml-template' }] }));
  await page.route('**/api/templates/ml-template/instantiate', route => {
    const request = route.request().postDataJSON();
    expect(request.run).toBe(false);
    expect(JSON.parse(request.slots.successMetric)).toEqual([{ metric: 'PR-AUC', threshold: '>= 0.65', operatingPoint: 'top 2%' }]);
    const substitute = (value: unknown): unknown => {
      if (typeof value === 'string') return value.replace(/\{slot:([^}]+)\}/g, (_, key) => request.slots[key] ?? '');
      if (Array.isArray(value)) return value.map(substitute);
      if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([key, entry]) => [key, substitute(entry)]));
      return value;
    };
    prepared = { ...substitute(template.spec) as SDKWorkflow, id: '', revision: 0, name: 'Retention baseline', check: [], targetRoot: '', project: '', autoRepair: false };
    return route.fulfill({ status: 201, json: { workflow: prepared } });
  });
  await page.route('**/api/workflows', route => {
    if (route.request().method() === 'POST') {
      saved = { ...route.request().postDataJSON(), id: 'saved-ml', revision: 1 };
      return route.fulfill({ status: 201, json: saved });
    }
    return route.fulfill({ json: saved ? [saved] : [] });
  });
  await page.route('**/api/run', route => {
    launched = route.request().postDataJSON();
    return route.fulfill({ status: 202, json: { runId: 'ml-fixture' } });
  });
  await page.route('**/api/runs/ml-fixture', route => route.fulfill({ json: { run: {
    runId: 'ml-fixture', dag: 'node-workflow', goal: saved?.instruction, state: 'running',
    options: { runtime: 'codex-sdk', request: { instruction: saved?.instruction, nodes: saved?.nodes } },
    doc: { sdk: { phase: 'approval_pending',
      approval: { id: 'ml-plan-approval', title: saved?.nodes?.[2].title, prompt: saved?.nodes?.[2].instruction,
        documents: [{ name: 'Evaluation plan', url: '/file/ml-fixture/nodes/delivery/OUTPUTS/plan/plan.md', sha256: 'a'.repeat(64) }] },
      nodes: saved?.nodes?.map((node, index) => ({ id: node.id, title: node.title, phase: index < 2 ? 'passed' : index === 2 ? 'approval_pending' : 'pending', ...(index === 0 ? { verification: 'not_requested' } : {}) })),
    } },
  }, nodes: [], outcome: null, journey: null } }));
  await page.route('**/api/approvals/ml-plan-approval/decision', route => {
    decision = route.request().postDataJSON();
    return route.fulfill({ json: { state: 'approved' } });
  });
  await page.goto('/knowledge');
  await page.getByRole('tab', { name: 'Templates', exact: true }).click();
  await page.getByRole('link', { name: 'Open workflow template', exact: true }).click();
  await expect(page).toHaveURL(/\/workflows$/);
  await expect(page.getByLabel('Workflow template', { exact: true })).toHaveValue('ml-template');
  await expect(page.getByRole('button', { name: 'Build with steps', exact: true })).toHaveAttribute('aria-pressed', 'true');
  for (const [label, value] of Object.entries({
    'Task': 'Rank subscription accounts for weekly retention calls',
    'Prediction target': 'Cancellation within 30 days; labels mature after the window closes',
    'Data available': '/data/accounts.csv; monthly snapshots; holdout access isolation is unproven',
    'How training and evaluation are separated': 'Train on months 1–17; evaluate on month 18; account-disjoint split',
  })) await page.getByLabel(label, { exact: true }).fill(value);
  await page.getByLabel('Metric, row 1', { exact: true }).fill('PR-AUC');
  await expect(page.getByRole('button', { name: 'Use template steps', exact: true })).toBeDisabled();
  await page.getByLabel('Threshold, row 1', { exact: true }).fill('>= 0.65');
  await page.getByLabel('Operating point, row 1', { exact: true }).fill('top 2%');
  await page.getByRole('button', { name: 'Use template steps', exact: true }).click();
  await expect(page.getByLabel('Step 3 type', { exact: true })).toHaveValue('approval');
  expect(prepared?.nodes?.map(node => node.kind)).toEqual(['task', 'check', 'approval', 'task', 'check', 'document', 'check']);
  expect(launched).toBeUndefined();
  expect(saved).toBeUndefined();
  await page.getByText('Start from a workflow template', { exact: true }).click();
  const baseline = page.getByRole('article', { name: /^Step 4:/ });
  await expect(baseline.getByLabel('Step 4 extra check', { exact: true })).toHaveValue('prediction-metrics');
  const metric = baseline.getByLabel('Step 4 success metric', { exact: true });
  const source = baseline.getByLabel('Step 4 data description', { exact: true });
  await expect(metric).toHaveAttribute('aria-required', 'true');
  await expect(source).toHaveValue(prepared!.nodes![3].inputs!.dataDescription);
  await expect(baseline).toContainText('Naming a source does not grant access to it.');
  await baseline.getByText(`Extra check requirements · ${predictionCheck.title}`, { exact: true }).click();
  // A reader meets the short summary and the four contract lists; the long
  // prescriptive text the SDK hands the step is one disclosure further in and
  // is still exactly what the catalog says (D-06).
  await expect(baseline.getByText(predictionCheck.summary, { exact: true })).toBeVisible();
  await expect(baseline.getByRole('heading', { name: 'What it does not prove', exact: true })).toBeVisible();
  await baseline.getByText('The exact instructions this check gives the step', { exact: true }).click();
  await expect(baseline.getByText(predictionCheck.description, { exact: true })).toBeVisible();
  await expect(baseline).toContainText('This optional check adds requirements beyond your editable step instructions.');
  const originalMetric = await metric.inputValue();
  await metric.fill('');
  await expect(page.getByRole('button', { name: 'Save workflow', exact: true })).toBeDisabled();
  await expect(page.getByRole('button', { name: 'Run workflow', exact: true })).toBeDisabled();
  await metric.fill(originalMetric);
  const editedInstructions = `${prepared!.nodes![3].instruction}\nAlso include a concise explanation for the retention team.`;
  await page.getByLabel('Step 4 instructions', { exact: true }).fill(editedInstructions);
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await baseline.screenshot({ path: testInfo.outputPath('ml-common-editor-mobile.png') });
  // The template suggests its own title; this workflow is named for its subject.
  await page.getByLabel('Workflow name', { exact: true }).fill('Retention baseline');
  await page.getByRole('button', { name: 'Save workflow', exact: true }).click();
  await expect(page.getByText('Workflow saved', { exact: true })).toBeVisible();
  expect(saved!.nodes![3].instruction).toBe(editedInstructions);
  expect(saved!.nodes![3].inputs).toEqual(prepared!.nodes![3].inputs);
  expect(saved!.instruction).toBe(prepared!.instruction);
  for (const node of saved!.nodes!) {
    expect(node.access).toBeUndefined();
    expect(node.toolchain).toBeUndefined();
    expect(node.sideEffect).toBeUndefined();
  }
  await page.reload();
  await page.getByRole('button', { name: /^Retention baseline/ }).click();
  await expect(page.getByLabel('Step 4 instructions', { exact: true })).toHaveValue(editedInstructions);
  await expect(page.getByLabel('Step 3 type', { exact: true })).toHaveValue('approval');
  await page.getByRole('button', { name: 'Run workflow', exact: true }).click();
  expect(launched).toEqual({ sourceWorkflowId: saved!.id, workflowName: saved!.name, goal: saved!.instruction, nodes: saved!.nodes, project: '', autoRepair: false });
  const approval = page.getByRole('region', { name: 'Approval needed', exact: true });
  await expect(approval.getByRole('heading', { name: 'Approve the baseline and evaluation plan', exact: true })).toBeVisible();
  await expect(approval).toContainText('approval does not grant filesystem, network or deployment permissions');
  await expect(approval.getByRole('button', { name: 'Evaluation plan', exact: true }).first()).toBeVisible();
  await expect(page.getByRole('region', { name: 'Step progress', exact: true }).getByText('Files produced', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Approve and continue', exact: true }).click();
  expect(decision).toEqual({ decision: 'approved' });
});


test('catalog tables supply guidance while SDK validation decides whether inputs can run', async ({page}) => {
  const catalog=structuredClone(checkCatalog);
  const profile=catalog.checks.find((check:{id:string})=>check.id==='dataset-shift-profile');
  const table=profile.inputTables.features;
  table.maxRows=1;
  table.columns.find((column:{id:string})=>column.id==='type').options=['categorical'];
  const owner=table.columns.find((column:{id:string})=>column.id==='owner');
  owner.label='Responsible team';owner.hint='Name the team that can act on this result.';
  const saved:SDKWorkflow={id:'metadata-flow',revision:1,name:'Metadata guidance',kind:'nodes',instruction:'Compare windows',check:[],targetRoot:'',project:'',autoRepair:false,nodes:[
    {id:'measure',kind:'task',title:'Measure sources',instruction:'Preserve this instruction.',acceptance:'dataset-shift-profile',inputs:{baselineData:'/data/before.csv',currentData:'/data/after.csv',keyColumns:'id',features:JSON.stringify([{feature:'category',type:'categorical',whyItMatters:'Segmentation',statistic:'category-emergence',tolerance:'-1',unit:'points_of_share'}])}},
  ]};
  await page.route('**/api/workflow-checks',route=>route.fulfill({json:catalog}));
  await page.route('**/api/workflows',route=>route.fulfill({json:[saved]}));
  await page.reload();await page.getByRole('button',{name:/^Metadata guidance/}).click();
  await expect(page.getByText('Name the team that can act on this result.',{exact:true})).toBeVisible();
  await expect(page.getByLabel('Type, row 1',{exact:true}).locator('option')).toHaveText(['—','categorical']);
  await expect(page.getByRole('button',{name:'+ Add a row',exact:true}).first()).toBeDisabled();
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
  await page.getByLabel('Responsible team, row 1',{exact:true}).fill('Analytics');
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
  await page.getByLabel('Tolerance, row 1',{exact:true}).fill('2');
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeEnabled();
  await expect(page.getByRole('button',{name:'Save changes',exact:true})).toBeEnabled();
  await expect(page.getByLabel('Step 1 instructions')).toHaveValue('Preserve this instruction.');
});

test('validation follows the exact edited definition and late responses cannot enable save or run', async ({page}) => {
  const pending: Array<{definition: {instruction:string;nodes:SDKWorkflow['nodes']}; finish: (valid:boolean)=>Promise<void>}> = [];
  await page.route('**/api/workflows/validate', async route => {
    await new Promise<void>(resolve => {
      pending.push({definition:route.request().postDataJSON(),finish:async valid => {
        await route.fulfill({json:{...validationIdentity,valid,...(!valid?{issue:{message:'Revise this draft before running.'}}:{})}}).catch(()=>{});
        resolve();
      }});
    });
  });
  let saved:SDKWorkflow|undefined;let launched:{goal:string;nodes:SDKWorkflow['nodes']}|undefined;
  await page.route('**/api/workflows',route=>{
    if(route.request().method()==='POST') {saved={...route.request().postDataJSON(),id:'current-draft',revision:1};return route.fulfill({json:saved});}
    return route.fulfill({json:saved?[saved]:[]});
  });
  await page.route('**/api/run',route=>{launched=route.request().postDataJSON();return route.fulfill({status:202,json:{runId:'current-draft-run'}});});
  await page.getByLabel('What do you want to achieve?').fill('Create the reviewed report');
  await page.getByRole('button',{name:/^Create files/}).click();
  await page.getByLabel('Workflow name').fill('Current draft');
  await expect.poll(()=>pending.length).toBe(1);
  await expect(page.getByRole('button',{name:'Save workflow',exact:true})).toBeDisabled();
  await page.getByLabel('Step 1 instructions').fill('Use only the corrected source.');
  await expect.poll(()=>pending.length).toBe(2);
  await pending[0].finish(true);
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
  await expect(page.getByRole('button',{name:'Save workflow',exact:true})).toBeDisabled();
  await pending[1].finish(false);
  await expect(page.getByRole('alert')).toContainText('Revise this draft');
  await page.getByLabel('Step 1 instructions').fill('Use the corrected source and produce the report.');
  await expect.poll(()=>pending.length).toBe(3);
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
  await pending[2].finish(true);
  await page.getByRole('button',{name:'Save workflow',exact:true}).click();
  await expect(page.getByText('Workflow saved',{exact:true})).toBeVisible();
  expect({instruction:saved?.instruction,nodes:saved?.nodes}).toEqual(pending[2].definition);
  await page.getByRole('button',{name:'Run workflow'}).click();
  expect({instruction:launched?.goal,nodes:launched?.nodes}).toEqual(pending[2].definition);
});

test('validation failure keeps the draft editable and explicit retry checks current settings', async ({page}, testInfo) => {
  let available=false;const definitions:unknown[]=[];
  await page.route('**/api/workflows/validate',route=>{
    definitions.push(route.request().postDataJSON());
    return available?fulfillWorkflowValidation(route):route.fulfill({status:503,json:{error:'Workflow validation is temporarily unavailable.'}});
  });
  await page.getByLabel('What do you want to achieve?').fill('Write a report');
  await page.getByRole('button',{name:/^Create files/}).click();
  await expect(page.getByRole('alert')).toContainText('temporarily unavailable');
  await page.screenshot({path:testInfo.outputPath('workflow-validation-retry.png'),fullPage:true});
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeDisabled();
  await page.getByLabel('Step 1 instructions').fill('Preserve this corrected instruction.');
  await expect.poll(()=>definitions.length).toBe(2);
  await expect(page.getByRole('button',{name:'Retry workflow check'})).toBeVisible();
  available=true;
  await page.getByRole('button',{name:'Retry workflow check'}).click();
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeEnabled();
  expect(definitions[2]).toEqual(definitions[1]);
  await expect(page.getByLabel('Step 1 instructions')).toHaveValue('Preserve this corrected instruction.');
});

test('a valid SDK decimal is not rejected by browser-only tolerance rules', async ({page}) => {
  const workflow:SDKWorkflow={id:'zero-tolerance',revision:1,name:'Exact zero',kind:'nodes',instruction:'Produce exact totals',check:[],targetRoot:'',project:'',autoRepair:false,nodes:[
    {id:'profile',kind:'task',title:'Profile',instruction:'Measure sources.',acceptance:'data-pipeline-profile',inputs:{sources:'/source.csv',targetStore:'SQLite',grain:'one row per day',keyColumns:'day',maxLag:'one day',safeUse:'aggregates only',measures:JSON.stringify([{measure:'orders',definition:'Order count',unit:'exact',tolerance:'-0'}])}},
  ]};
  await page.route('**/api/workflows',route=>route.fulfill({json:[workflow]}));
  await page.reload();await page.getByRole('button',{name:/^Exact zero/}).click();
  await expect(page.getByRole('button',{name:'Run workflow'})).toBeEnabled();
  await expect(page.getByLabel('Allowed difference, row 1',{exact:true})).toHaveValue('-0');
});

test('saving adopts server-normalized node objects and protects the editor while the request is pending', async ({ page }) => {
  let finishSave: () => Promise<void> = async () => {};
  await page.route('**/api/workflows', async route => {
    if (route.request().method() !== 'POST') return route.fulfill({ json: [] });
    const submitted = route.request().postDataJSON();
    await new Promise<void>(resolve => {
      finishSave = async () => {
        // Go emits struct keys in this order, unlike browser-created objects.
        const nodes = submitted.nodes.map((node: {kind: string; id: string; title: string; instruction: string}) => ({ kind: node.kind, id: node.id, title: node.title, instruction: node.instruction }));
        await route.fulfill({ status: 201, json: { ...submitted, id: 'canonical', revision: 1, nodes } });
        resolve();
      };
    });
  });
  await page.getByLabel('What do you want to achieve?').fill('Preserve my reviewed document instructions');
  await page.getByRole('button', { name: /^Explore/ }).click();
  await page.getByLabel('Step 1 instructions').fill('Keep this exact instruction.');
  await page.getByLabel('Workflow name').fill('Canonical save');
  await page.getByRole('button', { name: 'Save workflow', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Saving…', exact: true })).toBeVisible();
  await expect(page.getByLabel('Step 1 instructions')).toBeDisabled();
  await expect(page.getByLabel('What do you want to achieve?')).toBeDisabled();
  await expect(page.getByLabel('Workflow name')).toBeDisabled();
  await expect(page.getByRole('button', { name: 'New workflow', exact: true })).toBeDisabled();
  await expect(page.getByRole('button', { name: 'Project task', exact: true })).toBeDisabled();
  await finishSave();
  await expect(page.getByText('Workflow saved', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeDisabled();
  await expect(page.getByLabel('Step 1 instructions')).toHaveValue('Keep this exact instruction.');
  await page.getByLabel('Step 1 instructions').fill('A deliberate later edit.');
  await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeEnabled();
});
