/**
 * Two Inbox cards that quoted less than they had (Q-12, D-03).
 *
 * Q-12: "Show full reason" expanded to a sentence that still ended in an
 * ellipsis, because the reason is shortened before the card ever sees it. The
 * card now reads the whole finding back from the run it already links to, and
 * "Show less" restores the clip.
 *
 * D-03: the learned-method card offered `phase: passed` — a checkpoint field
 * name and its value — as the evidence for a drafting method. When that is all
 * the extraction pinned, the card shows what the run actually delivered and
 * says plainly that it is the delivery, not pinned evidence.
 */

import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import type { Journey, Skill } from '../src/api';

const sha = (c: string) => c.repeat(64);

const WHOLE = 'The note skipped every check it declared, and its impact claim is unsupported by any dated source in the research step, so the brief cannot be forwarded as it stands today.';
// What the API delivers: the same sentence, cut to 200 characters with an
// ellipsis. The card cannot lengthen this on its own.
const CLIPPED = `${WHOLE.slice(0, 120)}…`;

const nodes = [
  { id: 'draft', kind: 'document', title: 'Write the Monday brief', instruction: 'Write it.' },
  { id: 'review', kind: 'check', title: 'Independent review of the brief', instruction: 'Review it.', dependsOn: ['draft'] },
];

const updatedAt = '2026-09-12T13:04:43Z';
const journey: Journey = {
  key: 'j-brief', name: 'Weekly competitor brief', dag: 'node-workflow', root: 'run-9',
  stage: 'needs_decision', maturity: 'unproven', attemptCount: 1, criteria: 1, proven: 0,
  needsDecision: true, updatedAt, timeline: [],
  attempts: [{ runId: 'run-9', number: 1, dag: 'node-workflow', state: 'failed', superseded: false, criteria: 1, proven: 0, failingNode: 'review', updatedAt }],
} as unknown as Journey;

async function mockInbox(page: Page, opts: { runDoc?: unknown; proposals?: Skill[] } = {}) {
  await page.route('**/file/**', route => route.fulfill({
    body: '# Monday competitor brief\n\nLinear shipped a redesigned inbox on 3 September.\n\nNotion changed nothing this week.',
    contentType: 'text/markdown',
  }));
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/auth/check') return route.fulfill({ json: { authRequired: false, authed: true } });
    if (path === '/api/health') return route.fulfill({ json: { ok: true } });
    if (path === '/api/inbox') return route.fulfill({ json: {
      approvals: [], selections: [], proposals: opts.proposals ?? [],
      outcomes: opts.proposals ? [] : [{ ...journey, failureReason: CLIPPED, workflowName: 'Weekly competitor brief', runNumber: 9 }],
    } });
    if (path === '/api/runs/run-9') return route.fulfill({ json: { run: {
      runId: 'run-9', dag: 'node-workflow', state: 'failed', goal: 'Produce a competitor brief',
      startedAt: '2026-09-12T13:02:00Z', updatedAt: '2026-09-12T13:04:43Z',
      failingStep: { id: 'review', title: 'Independent review of the brief', kind: 'check' },
      options: { runtime: 'codex-sdk', request: { workflow: 'node-workflow', nodes } },
      doc: { sdk: opts.runDoc ?? { phase: 'failed', currentNode: 'review', nodes: [
        { id: 'draft', phase: 'passed', document: { path: 'draft.md', bytes: 1800, sha256: sha('b') } },
        { id: 'review', phase: 'failed', findings: [WHOLE] },
      ] } },
    }, nodes: [], outcome: null, journey: null } });
    return route.fulfill({ json: [] });
  });
}

test('“Show full reason” ends with the full reason, and “Show less” restores the clip', async ({ page }) => {
  await mockInbox(page);
  await page.goto('/inbox');

  const reason = page.locator('.inbox-signoff-reason');
  await expect(reason).toContainText(CLIPPED.slice(0, 60));
  await expect(reason).not.toContainText('cannot be forwarded as it stands today.');

  await reason.getByRole('button', { name: 'Show full reason' }).click();
  await expect(reason).toContainText(WHOLE);
  // The whole sentence, ending in a full stop rather than in an ellipsis.
  await expect(reason).not.toContainText('by any dated source in the research step, so the brief…');

  await reason.getByRole('button', { name: 'Show less' }).click();
  await expect(reason).not.toContainText('cannot be forwarded as it stands today.');
});

test('a reason the run cannot lengthen says so instead of expanding to the same words', async ({ page }) => {
  // The run's record carries nothing more than the card was already given.
  await mockInbox(page, { runDoc: { phase: 'failed', currentNode: 'review', nodes: [{ id: 'review', phase: 'failed' }] } });
  await page.goto('/inbox');

  const reason = page.locator('.inbox-signoff-reason');
  await reason.getByRole('button', { name: 'Show full reason' }).click();
  await expect(reason).toContainText('This is as much of the finding as the delivery recorded.');
  await expect(reason.getByRole('button', { name: 'Open the run' })).toBeVisible();
});

test('a draft whose only pinned evidence is a status word shows what the run delivered', async ({ page }) => {
  const draft: Skill = {
    id: 'constrained-short-form-drafting',
    name: 'Constrained short-form drafting',
    description: 'Draft under a hard word limit, then verify the limit before delivering.',
    category: 'writing',
    metadata: {
      status: 'draft', sourceRun: 'run-9', supportRuns: 1,
      // The quote the audited card printed inside quotation marks as grounds.
      evidence: [{ runId: 'run-9', nodeId: 'draft', quote: 'phase: passed' }],
    },
  };
  await mockInbox(page, { proposals: [draft] });
  await page.goto('/inbox');

  // Still named for what it is — a recorded status, not delivered text.
  await expect(page.getByText('the extraction pinned only a recorded step status', { exact: false })).toBeVisible();

  const delivered = page.locator('.kn-delivered');
  await expect(delivered).toContainText('What the run delivered · Write the Monday brief');
  await expect(delivered).toContainText('Linear shipped a redesigned inbox on 3 September.');
  await expect(delivered).toContainText('draft.md');
  // And it does not claim to be the extraction's evidence.
  await expect(delivered).toContainText('not a quote the extraction pinned');
});
