import { expect, test } from '@playwright/test';
import { writeFileSync } from 'node:fs';
import type { Locator, Page } from '@playwright/test';

// Begin the real mouse gesture before scrolling the destination into view.
// No synthesized DragEvent or injected DataTransfer substitutes for a user drag.
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

// No page.route or route.fulfill: every application endpoint reaches the real Go API.
test('describe, propose, drag, save, run, inspect, approve and accept one persisted SDK workflow', async ({ page, request }, testInfo) => {
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto('/workflows');
  await page.getByLabel('What do you want to achieve?').fill('Plan a simple weekend cycling guide');
  await page.getByText('Describe your steps', { exact: true }).click();
  await page.getByLabel('What should each step do? One step per line.').fill('Write the planning document\nCreate the cycling guide file');
  await page.getByRole('button', { name: 'Add described steps' }).click();
  await page.getByLabel('Step 1 name').fill('Plan');
  await page.getByLabel('Step 2 name').fill('Create guide');
  await page.getByLabel('Step 2 type').selectOption('task');
  await expect(page.getByLabel('Step 2 extra check')).toHaveValue('none');
  await page.getByLabel('Workflow name').fill('Browser cycling guide');
  const savedResponse = page.waitForResponse(response => response.url().endsWith('/api/workflows') && response.request().method() === 'POST');
  await page.getByRole('button', { name: 'Save workflow', exact: true }).click();
  const first = await (await savedResponse).json();
  expect(first.revision).toBe(1);
  await expect(page.getByText('Workflow saved', { exact: true })).toBeVisible();

  // A goal proposal is reviewed separately from the editor and cannot save or run it.
  const beforeRuns = await (await request.get('/api/runs')).json();
  const beforeWorkflows = await (await request.get('/api/workflows')).json();
  const composeResponse = page.waitForResponse(response => response.url().endsWith('/api/compose') && response.request().method() === 'POST');
  await page.getByRole('button', { name: 'Suggest steps from goal', exact: true }).click();
  const composed = await composeResponse;
  expect(composed.ok()).toBe(true);
  const proposal = await composed.json();
  expect(proposal.unsupported).toEqual([]);
  const suggested = page.getByRole('region', { name: 'Suggested steps' });
  await expect(suggested.getByRole('heading', { name: 'Proposed cycling guide' })).toBeVisible();
  await expect(suggested.getByText('Write the planning document', { exact: true })).toBeVisible();
  await expect(suggested.getByText('Create the cycling guide file', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeDisabled();
  await expect(page.getByLabel('Step 1 name')).toHaveAttribute('id', `title-${first.nodes[0].id}`);
  expect(await (await request.get('/api/workflows')).json()).toEqual(beforeWorkflows);
  expect(await (await request.get('/api/runs')).json()).toEqual(beforeRuns);
  await expect(suggested.getByRole('button', { name: 'Replace 2 steps with these steps', exact: true })).toBeEnabled();
  await page.screenshot({ path: testInfo.outputPath('real-api-proposal.png'), fullPage: true });
  await suggested.getByRole('button', { name: 'Replace 2 steps with these steps', exact: true }).click();
  await expect(page.getByLabel('Step 1 name')).toHaveAttribute('id', `title-${proposal.workflow.nodes[0].id}`);
  await expect(page.getByLabel('Step 2 instructions')).toHaveValue(proposal.workflow.nodes[1].instruction);
  expect(await (await request.get('/api/workflows')).json()).toEqual(beforeWorkflows);
  expect(await (await request.get('/api/runs')).json()).toEqual(beforeRuns);

  // Continue editing the applied proposal through visual controls.
  await page.getByLabel('Step 1 instructions').fill('Write the planning document with route, weather and rest-stop considerations.');
  await dragNode(page, page.getByRole('button', { name: /^Approval/ }), page.getByTestId('drop-2'));
  await expect(page.getByLabel('Step 3 type')).toHaveValue('approval');
  await page.getByRole('button', { name: /^Independent review/ }).click();
  await dragNode(page, page.getByLabel('Drag Independent review', { exact: true }), page.getByTestId('drop-2'));
  await expect(page.getByText('Independent review moved to step 3.', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Step 3 type')).toHaveValue('check');
  await expect(page.getByLabel('Step 4 type')).toHaveValue('approval');
  const changedResponse = page.waitForResponse(response => response.url().endsWith(`/api/workflows/${first.id}`) && response.request().method() === 'PUT');
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  const saved = await (await changedResponse).json();
  expect(saved.id).toBe(first.id);
  expect(saved.revision).toBe(2);
  expect(saved.nodes.map((node: {kind: string}) => node.kind)).toEqual(['document', 'task', 'check', 'approval']);
  await page.reload();
  await page.getByRole('button', { name: /^Browser cycling guide/ }).click();
  await expect(page.getByLabel('Step 1 instructions')).toHaveValue(saved.nodes[0].instruction);
  await expect(page.getByLabel('Step 2 extra check')).toHaveValue('none');
  await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeDisabled();

  const launchResponse = page.waitForResponse(response => response.url().endsWith('/api/run') && response.request().method() === 'POST');
  await page.getByRole('button', { name: 'Run workflow', exact: true }).click();
  const launched = await launchResponse;
  expect(launched.status()).toBe(202);
  expect(launched.request().postDataJSON().nodes).toEqual(saved.nodes);
  const { runId } = await launched.json();
  await expect(page).toHaveURL(new RegExp(`/runs/${runId}$`));
  await expect(page.getByRole('button', { name: 'Approve and continue', exact: true })).toBeVisible();
  await expect(page.getByLabel('Step progress').getByText('Files produced', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Step progress').getByText('Model review passed', { exact: true })).toBeVisible();
  const detailBefore = await (await request.get(`/api/runs/${runId}`)).json();
  expect(detailBefore.run.options.request.nodes).toEqual(saved.nodes);
  expect(detailBefore.run.doc.sdk.phase).toBe('approval_pending');
  expect(detailBefore.run.doc.sdk.nodes[1].verification).toBe('not_requested');
  const taskFile = `/file/${runId}/nodes/delivery/OUTPUTS/${saved.nodes[1].id}/guide.txt`;
  // Recorded files open in the in-app reader; the reader keeps a raw-file link
  // that carries the recorded path, so the evidence address is still asserted.
  const fileButtons = page.getByLabel('Step progress').getByRole('button', { name: 'Create guide · guide.txt', exact: true });
  await expect(fileButtons.first()).toHaveAttribute('title', `${saved.nodes[1].id}/guide.txt`);
  await fileButtons.first().click();
  await expect(page.getByRole('link', { name: 'Open raw file', exact: true })).toHaveAttribute('href', taskFile);
  await page.getByRole('button', { name: 'Back to run', exact: true }).click();
  await page.getByLabel('Step progress').getByRole('button', { name: 'Read document', exact: true }).first().click();
  await expect(page.getByRole('link', { name: 'Open raw file', exact: true })).toHaveAttribute('href', `/file/${runId}/nodes/delivery/OUTPUTS/${saved.nodes[0].id}.md`);
  await page.getByRole('button', { name: 'Back to run', exact: true }).click();
  const content = await request.get(taskFile);
  expect(content.ok()).toBe(true);
  expect(await content.text()).toContain('Cycling guide fixture');
  const planDocument = await request.get(`/file/${runId}/nodes/delivery/OUTPUTS/${saved.nodes[0].id}.md`);
  expect(await planDocument.text()).toContain('Planning document fixture');
  // Follow the persisted dependencies without saving, launching or approving work.
  const graph = page.getByRole('region', { name: 'Workflow graph', exact: true });
  // A straight chain of a few steps keeps its diagram behind a toggle by default.
  const showDiagram = graph.getByRole('button', { name: 'Show diagram', exact: true });
  if (await showDiagram.count()) await showDiagram.click();
  await graph.getByLabel('Jump to step', { exact: true }).selectOption(saved.nodes[2].id);
  const reviewInspector = graph.getByRole('article', { name: 'Independent review', exact: true });
  await expect(reviewInspector).toBeFocused();
  await reviewInspector.getByRole('region', { name: 'Step inputs' }).getByRole('button', { name: 'Create guide', exact: true }).click();
  const taskInspector = graph.getByRole('article', { name: 'Create guide', exact: true });
  await expect(taskInspector).toBeFocused();
  await expect(taskInspector.getByRole('region', { name: 'Step outputs' }).getByRole('link', { name: 'Create guide · guide.txt', exact: true })).toHaveAttribute('href', taskFile);
  await taskInspector.getByLabel('Inspect another step', { exact: true }).selectOption(saved.nodes[0].id);
  const planInspector = graph.getByRole('article', { name: 'Plan', exact: true });
  await expect(planInspector).toBeFocused();
  await planInspector.getByRole('button', { name: 'Return to diagram', exact: true }).click();
  await expect(graph.getByRole('article')).toHaveCount(0);
  await expect(graph.getByRole('link', { name: /^Plan\. Completed\./ })).toBeFocused();
  await expect(page).toHaveURL(new RegExp(`/runs/${runId}$`));
  expect((await (await request.get('/api/workflows')).json()).find((workflow: { id: string }) => workflow.id === saved.id)).toEqual(saved);
  expect((await (await request.get(`/api/runs/${runId}`)).json()).run.doc.sdk.phase).toBe('approval_pending');
  await page.screenshot({ path: testInfo.outputPath('real-api-approval.png'), fullPage: true });
  const approvalResponse = page.waitForResponse(response => response.url().includes('/api/approvals/') && response.request().method() === 'POST');
  await page.getByRole('button', { name: 'Approve and continue', exact: true }).click();
  expect((await approvalResponse).ok()).toBe(true);
  await expect.poll(async () => (await (await request.get(`/api/runs/${runId}`)).json()).run.state).toBe('passed');

  await page.goto('/inbox');
  const card = page.locator('.journey-card').filter({ has: page.locator('.journey-name', { hasText: 'Plan a simple weekend cycling guide' }) });
  await expect(card).toBeVisible();
  await expect(card.getByText('1/1 checks passed', { exact: true }).first()).toBeVisible();
  await card.getByRole('textbox', { name: 'Note (optional)' }).fill('Reviewed fixture outputs and their explicit limits.');
  const outcomeResponse = page.waitForResponse(response => response.url().includes('/api/journeys/') && response.url().endsWith('/outcome') && response.request().method() === 'POST');
  await card.getByRole('button', { name: 'Accept', exact: true }).click();
  const outcome = await outcomeResponse;
  expect(outcome.ok()).toBe(true);
  expect(outcome.request().postDataJSON()).toEqual({ runId, verdict: 'accepted', note: 'Reviewed fixture outputs and their explicit limits.' });
  const decision = await outcome.json();
  const detail = await (await request.get(`/api/runs/${runId}`)).json();
  expect(detail.journey.outcome.verdict).toBe('accepted');
  expect(detail.journey.criteria).toBe(1);
  expect(detail.journey.proven).toBe(1);
  expect(detail.run.doc.sdk.nodes.map((node: {phase: string}) => node.phase)).toEqual(['passed', 'passed', 'passed', 'passed']);
  expect(errors).toEqual([]);
  writeFileSync(process.env.SDK_BROWSER_RECEIPT!, JSON.stringify({ authoring: { descriptions: true, goalProposal: true, proposalReviewedBeforeApply: true, paletteDrag: true, reorderDrag: true, clickToAdd: true }, proposedNodes: proposal.workflow.nodes, workflowId: saved.id, revision: saved.revision, runId, nodes: saved.nodes, decisionId: decision.decisionId, runtimeFingerprint: detail.run.doc.sdk.runtimeFingerprint, taskVerification: detail.run.doc.sdk.nodes[1].verification, reviewPhase: detail.run.doc.sdk.nodes[2].phase, checkCounts: { total: detail.journey.criteria, passed: detail.journey.proven } }));
});
