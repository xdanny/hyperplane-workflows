import { expect, test } from '@playwright/test';
import { writeFileSync } from 'node:fs';
import type { SDKWorkflow } from '../src/api';

// Real handlers, database and SDK transport. Only the model executable is a fixture.
test('repair unavailable source and connection settings, then run the saved connected step', async ({ page, request }, testInfo) => {
  const source = process.env.SDK_FIXTURE_BROWSER_SOURCE!;
  const missingSource = `${source}.missing`;
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto('/workflows');
  await page.getByLabel('What do you want to achieve?').fill('Summarize the connected sales source');
  await page.getByRole('button', { name: /^Create files/ }).click();
  await page.getByLabel('Step 1 name').fill('Summarize source');
  await page.getByLabel('Step 1 instructions').fill('Read the declared CSV source and create summary.json with its row count and amount total. Use the configured connection reference without displaying its value.');
  await page.getByText('Files and connections · Off', { exact: true }).click();
  await page.getByLabel('Read local files or folders in step 1', { exact: true }).check();
  await page.getByLabel('Step 1 readable inputs, one path per line').fill(missingSource);
  await page.getByLabel('Allow network hosts for step 1', { exact: true }).check();
  // A reserved .invalid hostname exercises the declaration without contacting a service.
  await page.getByLabel('Step 1 allowed hosts, one per line').fill('source.example.invalid');
  await page.getByLabel('Step 1 connection names, one per line').fill('WORKFLOW_BROWSER_SOURCE_TOKEN');
  await expect(page.getByLabel('Allow step 1 to install temporary tools')).not.toBeChecked();
  await page.getByLabel('Workflow name').fill('Connected source summary');

  async function save(name: string): Promise<SDKWorkflow> {
    const response = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/api/workflows') || r.request().method() === 'PUT' && new URL(r.url()).pathname.startsWith('/api/workflows/'));
    await page.getByRole('button', { name, exact: true }).click();
    const saved = await response;
    expect(saved.ok()).toBe(true);
    await expect(page.getByText('Workflow saved', { exact: true })).toBeVisible();
    return saved.json();
  }
  async function launch(saved: SDKWorkflow, state: 'failed' | 'passed') {
    const response = page.waitForResponse(r => r.url().endsWith('/api/run') && r.request().method() === 'POST');
    await page.getByRole('button', { name: 'Run workflow', exact: true }).click();
    const launched = await response;
    expect(launched.status()).toBe(202);
    expect(launched.request().postDataJSON().nodes).toEqual(saved.nodes);
    const { runId } = await launched.json();
    await expect.poll(async () => (await (await request.get(`/api/runs/${runId}`)).json()).run.state).toBe(state);
    const detail = await (await request.get(`/api/runs/${runId}`)).json();
    expect(detail.run.options.request.nodes).toEqual(saved.nodes);
    return { runId, detail };
  }
  async function reopen(saved: SDKWorkflow) {
    await page.goto('/workflows');
    await page.getByRole('button', { name: /^Connected source summary/ }).click();
    await page.getByText('Files and connections · 1 readable path · 1 allowed host', { exact: true }).click();
    await expect(page.getByLabel('Step 1 allowed hosts, one per line')).toHaveValue('source.example.invalid');
    await expect(page.getByLabel('Step 1 readable inputs, one path per line')).toHaveValue(saved.nodes![0].access!.readPaths!.join('\n'));
    await expect(page.getByLabel('Step 1 connection names, one per line')).toHaveValue(saved.nodes![0].access!.environment!.join('\n'));
    await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeDisabled();
  }

  const first = await save('Save workflow');
  const missingPath = await launch(first, 'failed');
  await expect(page.getByRole('alert')).toContainText(`Readable input unavailable: ${missingSource}. Check that it exists and the run host can read it.`);
  await expect(page.getByRole('link', { name: 'Use this setup again', exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('missing-source.png'), fullPage: true });
  await reopen(first);
  await page.getByLabel('Step 1 readable inputs, one path per line').fill(source);
  await page.getByLabel('Step 1 connection names, one per line').fill('WORKFLOW_BROWSER_MISSING');
  const second = await save('Save changes');
  const missingConnection = await launch(second, 'failed');
  await expect(page.getByRole('alert')).toContainText('Configure the data connection environment variable WORKFLOW_BROWSER_MISSING on the run host');
  await reopen(second);
  await page.getByLabel('Step 1 connection names, one per line').fill('WORKFLOW_BROWSER_SOURCE_TOKEN');
  const saved = await save('Save changes');
  expect(saved.id).toBe(first.id);
  expect(saved.revision).toBe(3);
  await page.reload();
  await page.getByRole('button', { name: /^Connected source summary/ }).click();
  await page.getByText('Files and connections · 1 readable path · 1 allowed host', { exact: true }).click();
  await expect(page.getByLabel('Step 1 readable inputs, one path per line')).toHaveValue(source);
  await expect(page.getByLabel('Step 1 connection names, one per line')).toHaveValue('WORKFLOW_BROWSER_SOURCE_TOKEN');
  await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeDisabled();
  await page.screenshot({ path: testInfo.outputPath('connected-settings.png'), fullPage: true });
  const { runId, detail } = await launch(saved, 'passed');
  expect(detail.run.doc.sdk.nodes[0].verification).toBe('not_requested');
  const output = await request.get(`/file/${runId}/nodes/delivery/OUTPUTS/${saved.nodes![0].id}/summary.json`);
  expect(output.ok()).toBe(true);
  expect(await output.json()).toEqual({ rows: 2, total: 18 });
  await expect(page.getByRole('button', { name: 'Summarize source · summary.json', exact: true }).first()).toBeVisible();
  expect(errors).toEqual([]);
  writeFileSync(process.env.SDK_BROWSER_CONNECTED_RECEIPT!, JSON.stringify({ workflowId: saved.id, revision: saved.revision, nodes: saved.nodes, runId, failedRunIds: [missingPath.runId, missingConnection.runId], result: { rows: 2, total: 18 }, verification: detail.run.doc.sdk.nodes[0].verification }));
});
