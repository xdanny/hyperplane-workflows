/**
 * Supervising a run: what it produced, what failed, and whose turn it is.
 *
 * Every assertion here is a sentence a person could not get from the audited
 * build without opening three surfaces or reading 600 words (Q-01, Q-02, Q-03,
 * P-04, P-06, M-03, D-08). Fixtures carry the round-11 contract shapes.
 */

import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import type { SDKWorkflowNode } from '../src/api';

const sha = (c: string) => c.repeat(64);

const nodes: SDKWorkflowNode[] = [
  { id: 'research', kind: 'research', title: 'Research last week’s announcements', instruction: 'Research the week.' },
  { id: 'draft', kind: 'document', title: 'Write the Monday brief', instruction: 'Write the brief.', dependsOn: ['research'] },
  { id: 'review', kind: 'check', title: 'Independent review of the brief', instruction: 'Check every claim.', dependsOn: ['draft'] },
];

const BRIEF = [
  '# Monday competitor brief',
  '',
  'Coverage: 6–12 September. 13 September is not covered.',
  '',
  '| Company | What was announced |',
  '| --- | --- |',
  '| Linear | A redesigned inbox |',
  '',
  'See the [Notion release index](https://www.notion.com/releases).',
].join('\n');

function passedRun() {
  return {
    runId: 'passed-run', dag: 'node-workflow', state: 'passed',
    goal: 'Produce this Monday’s one-page competitor brief',
    project: 'weekly-brief',
    startedAt: '2026-09-12T13:02:00Z', endedAt: '2026-09-12T13:04:43Z', updatedAt: '2026-09-12T13:04:43Z',
    // Round-11 contract fields.
    workflowName: 'Weekly competitor brief', runNumber: 3, durationMs: 163_200,
    slots: { week: '31 Aug–6 Sep', competitors: 'Linear, Notion' },
    // Round-12: the labels the saved workflow asked for these by, frozen at
    // launch, so no value is ever shown under a machine id (P-18).
    slotLabels: { week: 'Week to cover', competitors: 'Competitors' },
    signOff: 'required',
    failingStep: null, waitingForPerson: false,
    options: {
      runtime: 'codex-sdk', model: 'gpt-6-astra', autoRepair: true,
      projectWorkflow: { key: 'saved:wf-brief', name: 'Weekly competitor brief', sourceWorkflowId: 'wf-brief', sourceRevision: 4 },
      request: { workflow: 'node-workflow', instruction: 'Produce the brief', nodes },
    },
    doc: { sdk: {
      phase: 'passed',
      usage: { input_tokens: 428_982, cached_input_tokens: 300_000, output_tokens: 3_600 },
      attempts: 3,
      nodes: [
        { id: 'research', phase: 'passed', attempts: 1, document: { path: 'research.md', bytes: 900, sha256: sha('a') } },
        { id: 'draft', phase: 'passed', attempts: 1, document: { path: 'draft.md', bytes: 1800, sha256: sha('b') } },
        { id: 'review', phase: 'passed', attempts: 1, document: { path: 'review.md', bytes: 700, sha256: sha('c') },
          findings: ['Non-failing coverage concern: 13 September is not covered.'] },
      ],
    } },
  };
}

function failedRun() {
  return {
    ...passedRun(), runId: 'failed-run', state: 'failed', runNumber: 4,
    failingStep: { id: 'review', title: 'Independent review of the brief', kind: 'check' },
    doc: { sdk: {
      phase: 'failed',
      findings: ['FAIL — Unsupported impact claim about onboarding time. Rephrase it or add supporting research.'],
      nodes: [
        { id: 'research', phase: 'passed', attempts: 1 },
        { id: 'draft', phase: 'passed', attempts: 1, document: { path: 'draft.md', bytes: 1800, sha256: sha('b') } },
        { id: 'review', phase: 'failed', attempts: 1,
          findings: ['FAIL — Unsupported impact claim about onboarding time. Rephrase it or add supporting research.'] },
      ],
    } },
  };
}

function waitingRun() {
  return {
    ...passedRun(), runId: 'waiting-run', state: 'running', runNumber: 5,
    endedAt: undefined, durationMs: null, waitingForPerson: true, failingStep: null,
    doc: { sdk: {
      phase: 'approval_pending',
      approval: {
        id: 'waiting-run:approve:approval', title: 'Approve the announcement',
        prompt: 'Read the draft announcement and approve it before it is finalized.',
        documents: [{ name: 'draft.md', url: '/file/waiting-run/nodes/delivery/OUTPUTS/draft.md', sha256: sha('b') }],
      },
      nodes: [{ id: 'draft', phase: 'passed', document: { path: 'draft.md', bytes: 400, sha256: sha('b') } }],
    } },
  };
}

async function mock(page: Page, run: ReturnType<typeof passedRun>, extra: { journeyOutcome?: unknown; personas?: unknown[] } = {}) {
  await page.route('**/file/**', route => route.fulfill({ body: BRIEF, contentType: 'text/markdown' }));
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/auth/check') return route.fulfill({ json: { authRequired: false, authed: true } });
    if (path === '/api/health') return route.fulfill({ json: { ok: true } });
    if (path === '/api/personas') return route.fulfill({ json: extra.personas ?? [] });
    if (path === `/api/runs/${run.runId}`) return route.fulfill({ json: {
      run, nodes: [],
      outcome: null,
      journey: extra.journeyOutcome !== undefined
        ? { key: 'j-brief', name: 'Weekly competitor brief', stage: 'accepted', maturity: 'accepted', attemptCount: 3, attempt: 3, criteria: 1, proven: 1, superseded: false, outcome: extra.journeyOutcome }
        : { key: 'j-brief', name: 'Weekly competitor brief', stage: 'needs_decision', maturity: 'proven_once', attemptCount: 3, attempt: 3, criteria: 1, proven: 1, superseded: false, outcome: null },
    } });
    if (path === '/api/runs') return route.fulfill({ json: [run] });
    return route.fulfill({ json: [] });
  });
}

test('a passed run leads with its result, its review and whose turn it is', async ({ page }) => {
  await mock(page, passedRun());
  await page.goto('/runs/passed-run');

  // The heading names the workflow and the run, not the goal sentence.
  await expect(page.getByRole('heading', { name: 'Weekly competitor brief · run #3' })).toBeVisible();
  // Duration, tokens and model, which existed in the API and were rendered
  // nowhere (M-03).
  await expect(page.getByText('PASSED · 2 min 43 s · 428.9k in / 3.6k out · gpt-6-astra')).toBeVisible();
  // A PASSED run whose sign-off the Inbox still holds says so (P-07, M-11).
  await expect(page.getByText('· awaiting your sign-off')).toBeVisible();

  // "Your result" is the first card, with the brief rendered — not raw
  // Markdown, and not the third of three identical links (Q-02, P-06, S-04).
  const result = page.getByRole('region', { name: 'Your result' });
  await expect(result).toBeVisible();
  await expect(result.getByText('Write the Monday brief').first()).toBeVisible();
  await expect(result.getByRole('table')).toBeVisible();
  await expect(result.getByRole('link', { name: 'Notion release index' })).toBeVisible();

  // The review's verdict in one line, with its non-failing caveat kept.
  await expect(result.getByText('Independent review: passed · 1 caveat')).toBeVisible();
  // The findings are collapsed until asked for. (The same sentence appears in
  // the brief's own text above, so this asserts on the findings list itself.)
  await expect(result.locator('.run-result-findings')).toHaveCount(0);
  await result.getByRole('button', { name: 'Show 1 finding' }).click();
  await expect(result.locator('.run-result-findings')).toContainText('13 September is not covered');

  // Steps and corrections, never "3 attempts" (D-08, S-07, M-08).
  await expect(page.getByText('3 steps · 0 corrections used (1 allowed per step)')).toBeVisible();
  await expect(page.getByText(/\battempts\b/)).toHaveCount(0);

  // The reader opens in the app with a way back and the raw file still offered.
  await result.getByRole('button', { name: 'Open' }).first().click();
  const reader = page.getByRole('dialog', { name: /^Document:/ });
  await expect(reader.getByRole('button', { name: 'Back to run' })).toBeVisible();
  await expect(reader.getByRole('link', { name: 'Open raw file' })).toBeVisible();
  await reader.getByRole('button', { name: 'Back to run' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);

  // The ~170 words of boilerplate are one disclosure now (Q-11).
  await expect(page.getByText('What these statuses mean')).toBeVisible();
});

test('a failed review is attributed to its step, never to "delivery"', async ({ page }) => {
  await mock(page, failedRun());
  await page.goto('/runs/failed-run');

  await expect(page.getByText('Independent review failed at step 3 — Independent review of the brief')).toBeVisible();
  await expect(page.locator('header').getByText('REVIEW FAILED')).toBeVisible();
  await expect(page.getByText('delivery')).toHaveCount(0);
  await expect(page.getByText(/Unsupported impact claim/).first()).toBeVisible();
  // A failed run is nobody's sign-off: the suffix belongs only to a pass.
  await expect(page.getByText('· awaiting your sign-off')).toHaveCount(0);
});

test('a run parked on a person says WAITING FOR YOU and shows the draft above the buttons', async ({ page }) => {
  await mock(page, waitingRun());
  const decisions: string[] = [];
  await page.route('**/api/approvals/*/decision', route => {
    decisions.push(new URL(route.request().url()).pathname);
    return route.fulfill({ json: { state: 'approved' } });
  });
  await page.goto('/runs/waiting-run');

  // RUNNING beside "0 steps are active" was the most-reported contradiction.
  await expect(page.locator('header').getByText('WAITING FOR YOU')).toBeVisible();
  await expect(page.getByText('Waiting for your approval')).toBeVisible();

  // The draft is rendered inside the approval card, above the buttons (Q-13).
  const approval = page.getByRole('region', { name: 'Approval needed' });
  await expect(approval.getByText('What you are approving')).toBeVisible();
  await expect(approval.getByRole('table')).toBeVisible();
  const draftBox = approval.locator('.approval-draft');
  const buttons = approval.getByRole('button', { name: 'Approve and continue' });
  expect((await draftBox.boundingBox())!.y).toBeLessThan((await buttons.boundingBox())!.y);

  // Deciding says what was recorded rather than leaving both buttons live for
  // a minute (P-04).
  await buttons.click();
  await expect(approval.getByText('Decision recorded — continuing…')).toBeVisible();
  expect(decisions).toEqual(['/api/approvals/waiting-run%3Aapprove%3Aapproval/decision']);
});

test('a recorded acceptance is attributed on the run header, with its note', async ({ page }) => {
  await mock(page, passedRun(), {
    journeyOutcome: { verdict: 'accepted', decidedBy: 'Sofia', decidedAt: '2026-09-12T13:10:00Z', decisionId: 'd1', scope: 'journey', note: 'Good enough to send.' },
  });
  await page.goto('/runs/passed-run');
  // ONE acceptor: the decider, once. A reviewer who wrote the note is credited
  // for the note, never rendered as a second person who accepted (Q-18).
  await expect(page.getByText(/· accepted 12 Sep 2026 by Sofia/)).toBeVisible();
  await expect(page.getByText('“Good enough to send.”')).toBeVisible();
  await expect(page.getByText('· awaiting your sign-off')).toHaveCount(0);
  await expect(page.getByText(/accepted by Sofia 12 Sep/)).toHaveCount(0);
});

test('an automatic acceptance says it was automatic and names the checks it rests on', async ({ page }) => {
  await mock(page, passedRun(), {
    journeyOutcome: {
      verdict: 'accepted', decidedBy: 'hyperplane:auto', decidedAt: '2026-09-12T13:04:44Z',
      decisionId: 'd-auto', scope: 'journey',
      note: 'Accepted automatically: all 1 declared checks passed (workflow setting)',
    },
  });
  await page.goto('/runs/passed-run');

  await expect(page.getByText('· accepted automatically · 1/1 checks 12 Sep 2026')).toBeVisible();
  await expect(page.getByText('No person read this result.')).toBeVisible();
  // A policy is not a person: an automatic acceptance does not unlock skill
  // extraction, which still requires somebody to have accepted (M-11).
  await expect(page.getByRole('button', { name: 'Draft a reusable skill from this run' })).toHaveCount(0);
  await expect(page.getByText('accepted automatically by the checks policy, not by a person', { exact: false })).toBeVisible();
});

test('a reviewer can be asked to read a finished run, and the verdict stays advisory', async ({ page }) => {
  const persona = {
    id: 'marcus', name: 'Marcus', bar: 'Would I run this on production data?',
    competence: { bullets: ['Judges data pipelines and the claims made about their outputs.'] },
    reviews: 0,
  };
  const verdict = {
    id: 'pr-1', persona: 'marcus', personaName: 'Marcus', bar: 'Would I run this on production data?',
    target: 'run:passed-run', targetKind: 'run', targetId: 'passed-run', verdict: 'mixed',
    summary: 'The brief holds up, but its coverage gap is not stated where a reader will see it.',
    reasons: ['13 September is outside the window and the brief says so only in passing.'],
    advisory: true, reviewedAt: '2026-09-12T13:20:00Z',
  };
  let asked = 0;
  await mock(page, passedRun(), { personas: [persona] });
  await page.route('**/api/personas/marcus/review', route => { asked += 1; return route.fulfill({ json: verdict }); });
  await page.route('**/api/persona-reviews*', route => route.fulfill({ json: asked > 0 ? [verdict] : [] }));
  await page.goto('/runs/passed-run');

  const section = page.getByRole('region', { name: 'Ask a reviewer' });
  await expect(section).toBeVisible();
  await expect(section.getByText('Judges data pipelines and the claims made about their outputs.')).toBeVisible();
  await expect(section.getByText('No reviewer has recorded a verdict on this run.')).toBeVisible();
  await section.getByRole('button', { name: 'Review this run' }).click();
  await expect.poll(() => asked).toBe(1);

  // D-18: the chip used to read "MARCUS · MIXED", which beside a run chip is
  // read as the platform's own verdict on the run. It says who, what, and that
  // it decides nothing — and the reviewer's scope sits beside it unopened.
  const chip = section.getByRole('button', { name: /Reviewer Marcus: MIXED \(advisory\)/ });
  await expect(chip).toHaveText(/Reviewer Marcus: MIXED \(advisory\)/);
  await expect(section.getByText('The brief holds up, but its coverage gap is not stated where a reader will see it.')).toBeVisible();
  await chip.click();
  await expect(section.getByText('13 September is outside the window', { exact: false })).toBeVisible();
  // Advisory, never a gate: the sign-off controls stay exactly as they were.
  await expect(section.getByText('A persona review never gates Approve', { exact: false })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Accept', exact: true })).toBeEnabled();
});

test('the runs list names the workflow, the run number, the inputs and the failing step', async ({ page }) => {
  const run = failedRun();
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/auth/check') return route.fulfill({ json: { authRequired: false, authed: true } });
    if (path === '/api/health') return route.fulfill({ json: { ok: true } });
    if (path === '/api/runs') return route.fulfill({ json: [run, { ...passedRun(), runId: 'older' }] });
    return route.fulfill({ json: [] });
  });
  await page.goto('/runs');

  await expect(page.getByText('Weekly competitor brief · run #4')).toBeVisible();
  // Values under the labels the workflow asked for them by, never under ids (P-18).
  await expect(page.getByText('Week to cover: 31 Aug–6 Sep · Competitors: Linear, Notion').first()).toBeVisible();
  await expect(page.getByText(/Independent review of the brief failed — FAIL — Unsupported impact claim/)).toBeVisible();
  await expect(page.locator('.run-row-meta').first()).toHaveText(/^\d{1,2} \w{3} \d{2}:\d{2} · /);
  await expect(page.getByText('failed at delivery')).toHaveCount(0);

  // Grouped by workflow, with a comparison offered once there are two runs.
  await expect(page.getByRole('checkbox', { name: 'Group by workflow' })).toBeChecked();
  await expect(page.getByRole('link', { name: 'Compare latest two' })).toBeVisible();
});


// S-14 / D-16: a workflow whose sign-off is OPTIONAL is never queued — and was
// therefore undecidable anywhere, while its own page sent the reader to an
// Inbox that said "Nothing needs your attention". The policy governs the queue;
// it never governs whether a person may record their own judgment.
test('a finished run under an optional sign-off can still be decided, on its own page', async ({ page }) => {
  const run = { ...passedRun(), signOff: 'optional' as const };
  let recorded: unknown = null;
  await mock(page, run);
  await page.route('**/api/journeys/*/outcome', route => {
    recorded = route.request().postDataJSON();
    return route.fulfill({ status: 201, json: { verdict: 'rejected', decidedBy: 'local', decidedAt: '2026-09-12T13:20:00Z', decisionId: 'd-2', scope: 'journey' } });
  });
  await page.goto('/runs/passed-run');

  const result = page.getByRole('region', { name: 'Your result' });
  // The controls are here, and they say why they are here when nothing queued it.
  await expect(result.getByText('This workflow does not queue a sign-off, so nothing asked you for this.', { exact: false })).toBeVisible();
  // The Inbox is NOT claimed to be holding it — neither in the chip suffix nor
  // in the skill-extraction sentence.
  await expect(page.getByText('· awaiting your sign-off')).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Sign off in Inbox' })).toHaveCount(0);
  await expect(page.getByText('Record your decision in “Your result” above.')).toBeVisible();

  await result.getByRole('textbox', { name: 'Your note (optional)' }).fill('Rejected: the coverage gap is buried.');
  await result.getByRole('button', { name: 'Reject', exact: true }).click();
  await expect(result.getByText('Decision recorded.')).toBeVisible();
  expect(recorded).toMatchObject({ verdict: 'rejected', note: 'Rejected: the coverage gap is buried.' });
});

// A REQUIRED policy still says the Inbox is holding it — the queue claim is
// only made when the queue is actually making it.
test('a required sign-off still points at the Inbox that is holding it', async ({ page }) => {
  await mock(page, passedRun());
  await page.goto('/runs/passed-run');
  await expect(page.getByText('· awaiting your sign-off')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Sign off here' })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Your result' })
    .getByText('This workflow does not queue a sign-off', { exact: false })).toHaveCount(0);
});

// S-16: "Read the review" opened the check step's SESSION LOG — "Log: Check the
// note … no log output" — while the review document sat in the run's outputs.
test('“Read the review” opens the review document, not the reviewer’s empty log', async ({ page }) => {
  await mock(page, passedRun());
  await page.goto('/runs/passed-run');

  const result = page.getByRole('region', { name: 'Your result' });
  await result.getByRole('button', { name: 'Read the review' }).click();
  const reader = page.getByRole('dialog', { name: /^Document:/ });
  await expect(reader).toBeVisible();
  await expect(reader).toContainText('Independent review of the brief');
  await expect(page.getByText('no log output')).toHaveCount(0);
});
