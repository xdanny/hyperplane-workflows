/**
 * Typed review findings on the run page (S-17, S-12).
 *
 * The audited line read "Independent review: passed · 3 caveats" for three
 * findings that all CONFIRMED something — a reader was sent looking for three
 * problems the reviewer had signed off. The reviewer now labels each finding
 * and the checkpoint records the level beside the text, so the line counts
 * caveats and nothing else, and the findings are grouped by what they oblige
 * the reader to do.
 */

import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';

const sha = (c: string) => c.repeat(64);

const nodes = [
  { id: 'draft', kind: 'document', title: 'Write the Monday brief', instruction: 'Write it.' },
  { id: 'review', kind: 'check', title: 'Independent review of the brief', instruction: 'Review it.', dependsOn: ['draft'] },
];

type Checkpoint = Record<string, unknown>;

async function runPage(page: Page, runId: string, reviewCheckpoint: Checkpoint, state = 'passed') {
  await page.route('**/file/**', route => route.fulfill({ body: '# Monday brief\n\nLinear shipped a new inbox.', contentType: 'text/markdown' }));
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === `/api/runs/${runId}`) return route.fulfill({ json: { run: {
      runId, dag: 'node-workflow', goal: 'Produce a competitor brief', state,
      startedAt: '2026-09-12T13:02:00Z', updatedAt: '2026-09-12T13:04:43Z',
      options: { runtime: 'codex-sdk', projectWorkflow: { name: 'Weekly competitor brief' }, request: { workflow: 'node-workflow', nodes } },
      doc: { sdk: { phase: state, nodes: [
        { id: 'draft', phase: 'passed', document: { path: 'draft.md', bytes: 1800, sha256: sha('b') } },
        reviewCheckpoint,
      ] } },
    }, nodes: [], outcome: null, journey: null } });
    return route.fulfill({ json: path === '/api/auth/check' ? { authRequired: false, authed: true } : path === '/api/health' ? { ok: true } : [] });
  });
  await page.goto(`/runs/${runId}`);
}

test('the review line counts caveats only, and confirmations collapse on their own', async ({ page }) => {
  await runPage(page, 'typed-review', {
    id: 'review', phase: 'passed',
    document: { path: 'review.md', bytes: 700, sha256: sha('c') },
    findings: [
      'CONFIRMED — Every figure traces to the cited source.',
      'CAVEAT — 13 September is not covered.',
      'CONFIRMED — The brief is within the 400-word limit.',
    ],
    findingDetails: [
      { level: 'confirmation', text: 'CONFIRMED — Every figure traces to the cited source.' },
      { level: 'caveat', text: 'CAVEAT — 13 September is not covered.' },
      { level: 'confirmation', text: 'CONFIRMED — The brief is within the 400-word limit.' },
    ],
  });

  const card = page.getByRole('region', { name: 'Your result' });
  // The line sits beside its own buttons, so it is read as the line element.
  await expect(card.locator('.run-result-review-line')).toContainText('Independent review: passed · 1 caveat');
  // Not "3 caveats", which is what the same three findings used to read as.
  await expect(card.getByText('3 caveats', { exact: false })).toHaveCount(0);

  await card.getByRole('button', { name: /Show 3 findings/ }).click();
  await expect(card.getByRole('heading', { name: 'Caveats', exact: true })).toBeVisible();
  await expect(card.getByRole('heading', { name: 'Failed', exact: true })).toHaveCount(0);

  // The confirmations are present and out of the way — evidence the review was
  // substantive, not three things to worry about.
  const confirmed = card.locator('.run-result-confirmed');
  await expect(confirmed.getByText('What the review confirmed (2)')).toBeVisible();
  await expect(confirmed).not.toHaveAttribute('open', '');
  await confirmed.getByText('What the review confirmed (2)').click();
  await expect(confirmed.getByText('Every figure traces to the cited source.', { exact: false })).toBeVisible();
});

test('a review with nothing to add says so rather than leaving a bare "passed"', async ({ page }) => {
  await runPage(page, 'clean-review', {
    id: 'review', phase: 'passed',
    document: { path: 'review.md', bytes: 300, sha256: sha('c') },
    findings: [], findingDetails: [],
  });
  await expect(page.getByRole('region', { name: 'Your result' }).locator('.run-result-review-line'))
    .toContainText('Independent review: passed, no caveats');
});

test('a failed review leads with the finding levelled as the failure, under "Failed"', async ({ page }) => {
  await runPage(page, 'failed-review', {
    id: 'review', phase: 'failed',
    document: { path: 'review.md', bytes: 700, sha256: sha('c') },
    findings: ['A note about coverage.', 'The impact claim is unsupported.'],
    findingDetails: [
      { level: 'caveat', text: 'A note about coverage.' },
      { level: 'fail', text: 'The impact claim is unsupported.' },
    ],
  }, 'failed');

  const card = page.getByRole('region', { name: 'Your result' });
  await expect(card.locator('.run-result-review-line'))
    .toContainText('Independent review: failed — The impact claim is unsupported.');
  await expect(card.getByRole('heading', { name: /failed review/ })).toBeVisible();
  await card.getByRole('button', { name: /Show 2 findings/ }).click();
  // `exact` matters: the card's own h2 is "Draft that failed review — …".
  await expect(card.getByRole('heading', { name: 'Failed', exact: true })).toBeVisible();
  await expect(card.getByRole('heading', { name: 'Caveats', exact: true })).toBeVisible();
});

test('the findings appear once on the page, in the result card (S-12)', async ({ page }) => {
  await runPage(page, 'once-only', {
    id: 'review', phase: 'passed',
    document: { path: 'review.md', bytes: 700, sha256: sha('c') },
    findings: ['CAVEAT — 13 September is not covered.'],
    findingDetails: [{ level: 'caveat', text: 'CAVEAT — 13 September is not covered.' }],
  });
  // The run-level "Check findings" block is the second copy Sofia counted.
  await expect(page.getByRole('heading', { name: 'Check findings' })).toHaveCount(0);
  await expect(page.getByText('13 September is not covered.', { exact: false })).toHaveCount(0);
  await page.getByRole('button', { name: /Show 1 finding/ }).click();
  await expect(page.getByText('13 September is not covered.', { exact: false })).toHaveCount(1);
});

test('a run recorded before levels existed keeps the old count', async ({ page }) => {
  await runPage(page, 'old-review', {
    id: 'review', phase: 'passed',
    document: { path: 'review.md', bytes: 700, sha256: sha('c') },
    findings: ['Confirmed: every figure traces to its source.', 'Coverage of 13 September is thin.'],
  });
  // Two caveats, exactly as this run has always read. Guessing that the first
  // one was a confirmation would rewrite a record nobody labelled.
  await expect(page.getByRole('region', { name: 'Your result' }).locator('.run-result-review-line'))
    .toContainText('Independent review: passed · 2 caveats');
});
