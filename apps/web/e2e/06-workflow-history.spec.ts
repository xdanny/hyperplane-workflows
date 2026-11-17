/**
 * A saved workflow's own history, and comparing two of its runs (F-C).
 *
 * "How does this month differ from last month?" had no screen at all: Sofia
 * answered it by reading two raw Markdown files side by side for five minutes
 * (S-03, D-13, P-10, R-04).
 */

import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import type { WorkflowRunsResult } from '../src/api';

const RUN_2 = [
  '# Monday competitor brief',
  '',
  'Linear shipped a new inbox on 2 September.',
].join('\n');

const RUN_3 = [
  '# Monday competitor brief',
  '',
  'Linear shipped a redesigned inbox on 3 September.',
].join('\n');

const history: WorkflowRunsResult = {
  workflow: {
    id: 'wf-brief', name: 'Weekly competitor brief', kind: 'nodes',
    instruction: 'Produce a one-page competitor brief. Keep it under 400 words.',
    targetRoot: '', check: [], project: 'weekly-brief', autoRepair: true, revision: 4,
    signOff: 'auto_when_checks_pass',
    inputs: [
      { id: 'week', label: 'Week to cover', hint: 'The Monday–Sunday week the brief reports on.', required: true, example: '31 Aug–6 Sep' },
      { id: 'competitors', label: 'Competitors', hint: 'Who to watch this week.', example: 'Linear, Notion' },
    ],
    nodes: [
      { id: 'research', kind: 'research', title: 'Research last week’s announcements', instruction: 'Research.' },
      { id: 'draft', kind: 'document', title: 'Write the Monday brief', instruction: 'Write.' },
      { id: 'review', kind: 'check', title: 'Independent review of the brief', instruction: 'Check.' },
    ],
  },
  total: 2,
  // An earlier run of this same definition that never named the saved
  // workflow, because the workflow was saved after it ran (P-16).
  attachable: [
    { runId: 'run-0', startedAt: '2026-08-29T13:02:00Z', state: 'passed', reason: 'same steps, no saved workflow recorded' },
  ],
  runs: [
    {
      runId: 'run-3', runNumber: 3, startedAt: '2026-09-12T13:02:00Z', endedAt: '2026-09-12T13:04:43Z',
      durationMs: 163_200, state: 'passed', waitingForPerson: false,
      slots: { week: '7–13 Sep', competitors: 'Linear, Notion' },
      slotLabels: { week: 'Week to cover', competitors: 'Competitors' },
      checksDeclared: 1, checksPassed: 1, failingStep: null,
      signOff: 'auto_when_checks_pass',
      outcome: { verdict: 'accepted', decidedAt: '2026-09-12T13:10:00Z', decidedBy: 'Sofia', note: 'Good enough to send.' },
      usage: { inputTokens: 428_982, cachedInputTokens: 300_000, outputTokens: 3_600 },
      documents: [{ stepId: 'draft', title: 'Monday brief', path: 'draft.md', bytes: 1800, sha256: 'b'.repeat(64) }],
      files: [
        { stepId: 'draft', stepTitle: 'Write the Monday brief', path: 'draft.md', bytes: 1800, sha256: 'b'.repeat(64) },
        { stepId: 'draft', stepTitle: 'Write the Monday brief', path: 'build/totals.csv', bytes: 900, sha256: 'd'.repeat(64) },
        { stepId: 'review', stepTitle: 'Independent review of the brief', path: 'review.md', bytes: 700, sha256: 'e'.repeat(64) },
      ],
    },
    {
      runId: 'run-2', runNumber: 2, startedAt: '2026-09-05T13:02:00Z', endedAt: '2026-09-05T13:04:41Z',
      durationMs: 161_000, state: 'failed', waitingForPerson: false,
      slots: { week: '31 Aug–6 Sep', competitors: 'Linear, Notion' },
      slotLabels: { week: 'Week to cover', competitors: 'Competitors' },
      checksDeclared: 1, checksPassed: 0,
      failingStep: { id: 'review', title: 'Independent review of the brief', kind: 'check' },
      outcome: null,
      signOff: 'auto_when_checks_pass',
      usage: { inputTokens: 419_728, cachedInputTokens: 290_000, outputTokens: 3_400 },
      documents: [{ stepId: 'draft', title: 'Monday brief', path: 'draft.md', bytes: 1700, sha256: 'c'.repeat(64) }],
      files: [
        { stepId: 'draft', stepTitle: 'Write the Monday brief', path: 'draft.md', bytes: 1700, sha256: 'c'.repeat(64) },
        { stepId: 'draft', stepTitle: 'Write the Monday brief', path: 'build/totals.csv', bytes: 900, sha256: 'd'.repeat(64) },
      ],
    },
  ],
};

/** The history payload this page serves — overridable per test, because a
 *  second `page.route` for the same API is matched by registration order
 *  rather than by how specific its pattern is. */
async function mock(page: Page, payload: WorkflowRunsResult = history) {
  await page.route('**/file/run-2/**', route => route.fulfill({ body: RUN_2, contentType: 'text/markdown' }));
  await page.route('**/file/run-3/**', route => route.fulfill({ body: RUN_3, contentType: 'text/markdown' }));
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/auth/check') return route.fulfill({ json: { authRequired: false, authed: true } });
    if (path === '/api/health') return route.fulfill({ json: { ok: true } });
    if (path === '/api/workflows/wf-brief/runs') return route.fulfill({ json: payload });
    if (path === '/api/workflows/validate') {
      return route.fulfill({ json: { valid: true, runtimeFingerprint: 'f'.repeat(64), runtimeFingerprintVersion: 1 } });
    }
    if (path === '/api/workflow-checks') return route.fulfill({ json: { checks: [] } });
    if (path === '/api/workflows') return route.fulfill({ json: [payload.workflow] });
    return route.fulfill({ json: [] });
  });
}

test('a workflow page lists what it asks for and what its runs cost and decided', async ({ page }) => {
  await mock(page);
  await page.goto('/workflows/wf-brief');

  await expect(page.getByRole('heading', { name: 'Weekly competitor brief' })).toBeVisible();
  // The inputs are named fields with hints, not prose buried in a goal (C1).
  await expect(page.getByText('Week to cover').first()).toBeVisible();
  await expect(page.getByText('The Monday–Sunday week the brief reports on.')).toBeVisible();
  await expect(page.getByText('For example: 31 Aug–6 Sep')).toBeVisible();

  // The runs table answers "which run, what did it cost, who decided".
  const table = page.getByRole('table').first();
  await expect(table.getByText('#3')).toBeVisible();
  await expect(table.getByText('2 min 43 s')).toBeVisible();
  await expect(table.getByText('1 of 1 passed')).toBeVisible();
  await expect(table.getByText('accepted 12 Sep 2026 by Sofia')).toBeVisible();
  await expect(table.getByText('“Good enough to send.”')).toBeVisible();
  await expect(table.getByText('0 of 1 passed')).toBeVisible();
  await expect(table.getByText('Independent review of the brief failed')).toBeVisible();

  // Re-running keeps the saved workflow; nothing launches from this page.
  await expect(page.getByRole('button', { name: 'Run again with the same inputs' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Run with new inputs' })).toBeVisible();
  await expect(page.getByText('Nothing starts until you press Run there.')).toBeVisible();
});

test('two runs compare side by side with a text difference that claims only that', async ({ page }) => {
  await mock(page);
  await page.goto('/workflows/wf-brief?compare=run-3,run-2');

  const compare = page.getByRole('region', { name: 'Compare two runs' });
  await expect(compare.getByRole('heading', { name: 'Comparing run #2 with run #3' })).toBeVisible();
  // Per-step state, duration, tokens, checks, findings and inputs, side by side.
  await expect(compare.getByText('2 min 41 s')).toBeVisible();
  await expect(compare.getByText('419.7k in / 3.4k out')).toBeVisible();
  await expect(compare.getByText('428.9k in / 3.6k out')).toBeVisible();
  await expect(compare.getByText('Week to cover: 31 Aug–6 Sep', { exact: false })).toBeVisible();
  await expect(compare.getByText('REVIEW FAILED')).toBeVisible();

  // The difference is word-level and is labelled as a text difference.
  await expect(compare.getByText(/2 words removed, 2 words added/)).toBeVisible();
  await expect(compare.getByText(/text difference, not a comparison of meaning/).first()).toBeVisible();
  const diff = compare.locator('.wfh-diff');
  await expect(diff.locator('.wfh-diff-remove').filter({ hasText: 'new' })).toBeVisible();
  await expect(diff.locator('.wfh-diff-add').filter({ hasText: 'redesigned' })).toBeVisible();
});

test('picking two runs writes the comparison into the URL so it can be sent on', async ({ page }) => {
  await mock(page);
  await page.goto('/workflows/wf-brief');
  await expect(page.getByRole('region', { name: 'Compare two runs' })).toHaveCount(0);
  await page.getByRole('checkbox', { name: 'Compare run #3' }).check();
  await page.getByRole('checkbox', { name: 'Compare run #2' }).check();
  await expect(page).toHaveURL(/compare=run-3%2Crun-2/);
  await expect(page.getByRole('region', { name: 'Compare two runs' })).toBeVisible();
});

test('the compare view answers which produced files changed, by checksum (M-18)', async ({ page }) => {
  await mock(page);
  await page.goto('/workflows/wf-brief?compare=run-3,run-2');

  const compare = page.getByRole('region', { name: 'Compare two runs' });
  const files = compare.getByRole('table', { name: 'Output files compared by checksum' });
  await expect(files).toBeVisible();
  // The delivered document changed; the recomputed totals did not; the review
  // document exists only in the later run. None of this was answerable before:
  // the comparison diffed one document and ignored every other file.
  await expect(files.getByRole('row', { name: /draft\.md/ })).toContainText('changed');
  await expect(files.getByRole('row', { name: /build\/totals\.csv/ })).toContainText('same');
  await expect(files.getByRole('row', { name: /review\.md/ })).toContainText('only in run #3');
  await expect(compare.getByText('1 file changed, 1 the same, 1 only in run #3.')).toBeVisible();
  // What the comparison claims, said out loud: hashes differ, not meanings.
  await expect(compare.getByText('compared by their recorded SHA-256 only', { exact: false })).toBeVisible();
});

test('earlier runs of the same definition can be attached to the saved workflow (P-16)', async ({ page }) => {
  let attached: string[] = [];
  await mock(page);
  await page.route('**/api/workflows/wf-brief/attach-runs', async route => {
    attached = (route.request().postDataJSON() as { runIds: string[] }).runIds;
    return route.fulfill({ json: { attached, skipped: [] } });
  });
  await page.goto('/workflows/wf-brief');

  const attach = page.getByRole('region', { name: 'Attach earlier runs' });
  await expect(attach.getByRole('heading', { name: 'Attach 1 earlier run' })).toBeVisible();
  // The started column is rendered in the reader's own zone, so the assertion
  // pins the day and the shape rather than one machine's clock.
  await expect(attach.getByText(/29 Aug \d{2}:\d{2}/)).toBeVisible();
  await expect(attach.getByText('same steps, no saved workflow recorded')).toBeVisible();
  // Attaching changes what a run is FILED under, never what it executed.
  await expect(attach.getByText('leaves what each run actually executed untouched', { exact: false })).toBeVisible();

  // It asks before it writes.
  await attach.getByRole('button', { name: /^Attach these run/ }).click();
  expect(attached).toEqual([]);
  await expect(attach.getByText('will be listed under', { exact: false })).toBeVisible();
  await attach.getByRole('button', { name: 'Attach 1 run' }).click();
  await expect.poll(() => attached).toEqual(['run-0']);
  await expect(attach.getByText('Attached 1 run — the table above now lists it.')).toBeVisible();
});

test('the workflow page states its sign-off setting and what it does (M-11)', async ({ page }) => {
  await mock(page);
  await page.goto('/workflows/wf-brief');

  const policy = page.getByRole('region', { name: 'Sign-off setting' });
  await expect(policy.getByText('Accept automatically when every check passes')).toBeVisible();
  await expect(policy.getByText('A run that passes every check it declared is accepted by Hyperplane itself', { exact: false })).toBeVisible();
  // The caveat is gone: every row now reads the policy frozen on its own run,
  // so the table cannot be wrong about a finished run and has no need to warn
  // that it might be (round-13 contract).
  await expect(policy.getByText('Runs already finished keep the setting they were started under', { exact: false })).toHaveCount(0);
});

test('each run row reads the sign-off policy frozen on that run, not the current one', async ({ page }) => {
  const row = history.runs[0];
  // The saved workflow now says "never ask". Three finished runs, launched
  // under three different settings — and one launched before the setting
  // existed, which is "required", the way every workflow behaved then.
  await mock(page, {
    ...history,
    workflow: { ...history.workflow, signOff: 'optional' },
    total: 3, attachable: [],
    runs: [
      { ...row, runId: 'run-7', runNumber: 7, outcome: null, signOff: 'required' },
      { ...row, runId: 'run-6', runNumber: 6, outcome: null, signOff: 'optional' },
      { ...row, runId: 'run-5', runNumber: 5, outcome: null, signOff: undefined },
    ],
  });
  await page.goto('/workflows/wf-brief');

  const runRow = (number: number) => page.getByRole('row').filter({ has: page.getByRole('link', { name: 'Open' }) })
    .filter({ hasText: `#${number}` });
  await expect(runRow(7)).toContainText('awaiting your sign-off');
  await expect(runRow(6)).toContainText('no sign-off asked');
  // Absent means "required": the default every workflow had before the setting
  // existed, never the setting somebody chose afterwards.
  await expect(runRow(5)).toContainText('awaiting your sign-off');
});


// D-11 REGRESSION: both buttons hand the editor the SAVED workflow — its id,
// revision, name, steps and declared inputs. Dropping the id and the name made
// the editor treat it as a fresh draft: "Add a workflow name to save" for a
// workflow that already had one, and "Save to run with inputs" instead of
// launching by reference.
for (const button of ['Run with new inputs', 'Run again with the same inputs']) {
  test(`“${button}” opens the editor on the saved workflow, not a nameless draft`, async ({ page }) => {
    await mock(page);
    await page.goto('/workflows/wf-brief');
    await page.getByRole('button', { name: button }).click();
    await expect(page).toHaveURL(/\/workflows$/);

    // The identity survived the handover.
    await expect(page.getByLabel('Workflow name')).toHaveValue('Weekly competitor brief');
    await expect(page.getByRole('button', { name: 'Add a workflow name to save' })).toHaveCount(0);

    // And because it is a SAVED workflow with declared inputs, the run is
    // offered by reference rather than blocked behind a save.
    await expect(page.getByRole('button', { name: 'Fill inputs and run' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Save to run with inputs' })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Save changes' })).toBeVisible();
  });
}

// "Run again with the same inputs" also carries the values the last run used,
// which is the whole difference between the two buttons.
test('“Run again with the same inputs” prefills the values the last run used', async ({ page }) => {
  await mock(page);
  await page.goto('/workflows/wf-brief');
  await page.getByRole('button', { name: 'Run again with the same inputs' }).click();
  const fill = page.getByRole('button', { name: 'Fill inputs and run' });
  await expect(fill).toBeEnabled();
  await fill.click();
  // Opening the panel must not BE the run: an unkeyed button swap turned this
  // very click into a form submit, launching with values nobody had confirmed.
  const panel = page.getByRole('region', { name: 'Fill inputs for this run' });
  await expect(panel).toBeVisible();
  await expect(page).toHaveURL(/\/workflows$/);
  await expect(page.locator('#run-input-week')).toHaveValue('7–13 Sep');
  await expect(page.locator('#run-input-competitors')).toHaveValue('Linear, Notion');
  await expect(page.locator('#run-input-competitors')).toHaveValue('Linear, Notion');
});

// S-16: the compare view diffed the LAST recorded document, and a workflow that
// ends with an independent review records the review as a document too — so the
// word difference was of "# Check the note", not the note (S-03, S-16).
test('the compare view diffs the delivered document, not the review of it', async ({ page }) => {
  await mock(page);
  await page.goto('/workflows/wf-brief?compare=run-3,run-2');
  const compare = page.getByRole('region', { name: 'Compare two runs' });
  await expect(compare.getByRole('heading', { name: 'Text difference of the delivered document' })).toBeVisible();
  // Each output-file row opens that run's own copy in the reader (S-16).
  const files = compare.getByRole('table', { name: 'Output files compared by checksum' });
  await files.getByRole('row', { name: /draft\.md/ })
    .getByRole('button', { name: /Read draft\.md from run #3/ }).click();
  await expect(page.getByText('Linear shipped a redesigned inbox on 3 September.')).toBeVisible();
});
