import { test, expect } from '@playwright/test';

test.beforeEach(async ({ page }) => {
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    return route.fulfill({ json: path === '/api/auth/check' ? { authRequired: false, authed: true } : path === '/api/health' ? { ok: true } : [] });
  });
});

test('knowledge map offers current kinds and retains its controls after failures and empty filters', async ({ page }) => {
  const queries: string[][] = [];
  let fail = true;
  await page.route('**/api/graph/knowledge?**', route => {
    const kinds = new URL(route.request().url()).searchParams.get('kinds')!.split(',');
    queries.push(kinds);
    if (fail) return route.fulfill({ status: 503, json: { error: 'Temporarily unavailable' } });
    const nodes = kinds.includes('skill') ? [{ id: 'skill:write', kind: 'skill', label: 'Write clearly', degree: 0 }] : [];
    return route.fulfill({ json: { nodes, edges: [], communities: nodes.length } });
  });
  await page.goto('/knowledge');
  await page.getByRole('tab', { name: 'Knowledge map' }).click();
  await expect(page.getByRole('alert')).toContainText('Could not load the knowledge map');
  await expect(page.getByText('No skills or decisions match these filters.', { exact: false })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'toggle process' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'toggle skill' })).toBeVisible();
  fail = false;
  await page.getByRole('button', { name: 'Retry knowledge map' }).click();
  await expect(page.getByRole('img', { name: 'Knowledge map' })).toContainText('Write clearly');
  await page.getByRole('button', { name: 'toggle skill' }).click();
  await expect(page.getByText('No skills or decisions match these filters.', { exact: false })).toBeVisible();
  await page.getByRole('button', { name: 'toggle skill' }).click();
  await expect(page.getByRole('img', { name: 'Knowledge map' })).toContainText('Write clearly');
  expect(queries.every(kinds => kinds.length > 0 && kinds.every(kind => kind === 'decision' || kind === 'skill'))).toBe(true);
});

test('home distinguishes new execution usage from a continued run budget', async ({ page }) => {
  await page.route('**/api/stats/tokens', route => route.fulfill({ json: {
    totalCalls: 3, inputTokens: 140, cachedInputTokens: 25, outputTokens: 30, reasoningTokens: 0, cacheHitRate: 25 / 140,
    byRun: [{ runId: 'source', dag: 'node-workflow', totalCalls: 2, inputTokens: 100, cachedInputTokens: 20, outputTokens: 20 },
      { runId: 'continued', dag: 'node-workflow', totalCalls: 1, inputTokens: 40, cachedInputTokens: 5, outputTokens: 10 }],
  } }));
  await page.goto('/');
  await expect(page.getByText('Recorded token usage', { exact: true })).toBeVisible();
  await expect(page.getByText(/Reused usage from a continuation is counted once here/)).toBeVisible();
  const chart = page.getByRole('img', { name: 'New token usage per execution — input and output bars' });
  await expect(chart).toBeVisible();
  await expect(chart.locator('g[aria-label="node-workflow: in 40 out 10"]')).toHaveCount(1);
});

test('delivery decisions show SDK continuation lineage and distinguish checks from unchecked completion', async ({ page }) => {
  const updatedAt = '2026-09-07T10:00:00Z';
  const plain = { key: 'plain', name: 'Unchecked draft', dag: 'node-workflow', root: 'plain', stage: 'needs_decision', maturity: 'unproven',
    criteria: 0, proven: 0, needsDecision: true, attemptCount: 1, updatedAt, timeline: [],
    attempts: [{ runId: 'plain', number: 1, dag: 'node-workflow', state: 'passed', criteria: 0, proven: 0, superseded: false, updatedAt }] };
  const continued = { key: 'continued', name: 'Continued delivery', dag: 'node-workflow', root: 'source', stage: 'needs_decision', maturity: 'proven_once',
    criteria: 2, proven: 2, provenAttempt: 2, needsDecision: true, attemptCount: 2, updatedAt, timeline: [{ kind: 'continued', runId: 'continued', at: updatedAt, text: 'Continued from source with reviewed completed work.' }],
    attempts: [{ runId: 'continued', number: 2, continuedFrom: 'source', dag: 'node-workflow', state: 'passed', criteria: 2, proven: 2, superseded: false, updatedAt },
      { runId: 'source', number: 1, dag: 'node-workflow', state: 'interrupted', criteria: 2, proven: 2, failingNode: 'publish', superseded: true, updatedAt }] };
  await page.route('**/api/inbox', route => route.fulfill({ json: { approvals: [], selections: [], proposals: [], outcomes: [plain, continued] } }));
  const decisions: unknown[] = [];
  await page.route('**/api/journeys/plain/outcome', route => {
    decisions.push(route.request().postDataJSON());
    return route.fulfill({ json: { key: 'plain', verdict: 'accepted', scope: 'journey' } });
  });
  await page.goto('/inbox');
  const card = page.locator('.journey-card').filter({ has: page.locator('.journey-name', { hasText: 'Unchecked draft' }) });
  await expect(card.getByText('No extra checks', { exact: true })).toBeVisible();
  await expect(card.getByText('completed', { exact: true })).toBeVisible();
  await card.getByRole('button', { name: 'Accept', exact: true }).click();
  await expect.poll(() => decisions.length).toBe(1);
  expect(decisions[0]).toEqual({ runId: 'plain', verdict: 'accepted', note: '' });
  const continuedCard = page.locator('.journey-card').filter({ has: page.locator('.journey-name', { hasText: 'Continued delivery' }) });
  await expect(continuedCard.getByText('2/2 checks passed · attempt 2', { exact: true })).toBeVisible();
  await expect(continuedCard.getByText('2/2 checks passed · stopped at publish', { exact: true })).toBeVisible();
  await expect(continuedCard.getByText('repair', { exact: false })).toHaveCount(0);
  await continuedCard.getByRole('button', { name: 'Open source run source' }).click();
  await expect(page).toHaveURL(/\/runs\/source$/);
});

test('a stale delivery decision preserves the note and requires manual review of the newer attempt', async ({ page }) => {
  const updatedAt = '2026-09-07T10:00:00Z';
  const previous = { verdict: 'accepted', note: 'Accepted the first attempt only.', scope: 'journey', decisionId: 'old-outcome', decidedAt: updatedAt };
  let latest = 2;
  let reads = 0;
  const attempts = () => Array.from({ length: latest }, (_, index) => ({ runId: `attempt-${latest-index}`, number: latest-index, dag: 'node-workflow',
    ...(latest-index > 1 ? { continuedFrom: `attempt-${latest-index-1}` } : {}), state: 'passed', criteria: 0, proven: 0, superseded: index > 0, updatedAt }));
  await page.route('**/api/inbox', route => {
    reads++;
    return route.fulfill({ json: { approvals: [], selections: [], proposals: [], outcomes: [{ key: 'lineage', name: 'Reviewed delivery', dag: 'node-workflow', root: 'attempt-1',
      stage: 'needs_decision', maturity: 'unproven', criteria: 0, proven: 0, needsDecision: true, attemptCount: latest, updatedAt, timeline: [], attempts: attempts(),
      outcome: null, recordedOutcome: previous, coverage: { attempts: [1], undecided: Array.from({length: latest-1}, (_, i) => i+2), coversNewest: false, basis: 'refs', decidedAt: updatedAt } }] } });
  });
  const decisions: { runId: string; verdict: string; note: string }[] = [];
  await page.route('**/api/journeys/lineage/outcome', route => {
    decisions.push(route.request().postDataJSON());
    if (decisions.length === 1) { latest = 3; return route.fulfill({ status: 409, json: { error: 'A newer attempt is now current. Review it before deciding.' } }); }
    return route.fulfill({ json: { key: 'lineage', runId: 'attempt-3', verdict: 'accepted', scope: 'journey' } });
  });
  await page.goto('/inbox');
  await expect(page.getByText('Accepted — earlier attempts', { exact: true })).toBeVisible();
  const note = page.getByRole('textbox', { name: 'Note (optional)' });
  await note.fill('I reviewed the outputs and their limits.');
  await page.getByRole('button', { name: 'Accept', exact: true }).click();
  await expect(page.getByText('A newer attempt is now current. Review it before deciding.', { exact: true })).toBeVisible();
  await expect(note).toHaveValue('I reviewed the outputs and their limits.');
  await expect(page.getByRole('button', { name: 'Accept', exact: true })).toBeDisabled();
  await expect(page.getByRole('button', { name: 'I reviewed attempt 2', exact: true })).toBeDisabled();
  expect(reads).toBe(1);
  expect(decisions).toEqual([{ runId: 'attempt-2', verdict: 'accepted', note: 'I reviewed the outputs and their limits.' }]);
  await page.getByRole('button', { name: 'Refresh delivery', exact: true }).click();
  await expect(page.getByRole('button', { name: 'I reviewed attempt 3', exact: true })).toBeVisible();
  await expect(note).toHaveValue('I reviewed the outputs and their limits.');
  await expect(page.getByRole('button', { name: 'Accept', exact: true })).toBeDisabled();
  expect(decisions).toHaveLength(1);
  await page.getByRole('button', { name: 'I reviewed attempt 3', exact: true }).click();
  await page.getByRole('button', { name: 'Accept', exact: true }).click();
  await expect.poll(() => decisions.length).toBe(2);
  expect(decisions[1]).toEqual({ runId: 'attempt-3', verdict: 'accepted', note: 'I reviewed the outputs and their limits.' });
});

test('an older real attempt cannot enable acceptance of the latest rehearsal', async ({ page }) => {
  const updatedAt = '2026-09-07T10:00:00Z';
  await page.route('**/api/inbox', route => route.fulfill({ json: { approvals: [], selections: [], proposals: [], outcomes: [{
    key: 'mixed-history', name: 'Mixed delivery history', dag: 'node-workflow', root: 'real-attempt',
    stage: 'needs_decision', maturity: 'unproven', criteria: 0, proven: 0, needsDecision: true,
    attemptCount: 2, rehearsal: false, updatedAt, timeline: [], attempts: [
      { runId: 'latest-rehearsal', number: 2, dag: 'node-workflow', state: 'passed', rehearsal: true, criteria: 0, proven: 0, superseded: false, updatedAt },
      { runId: 'real-attempt', number: 1, dag: 'node-workflow', state: 'passed', rehearsal: false, criteria: 0, proven: 0, superseded: true, updatedAt },
    ],
  }] } }));
  let submissions = 0;
  await page.route('**/api/journeys/mixed-history/outcome', route => { submissions++; return route.fulfill({ json: {} }); });
  await page.goto('/inbox');
  const accept = page.getByRole('button', { name: 'Accept', exact: true });
  await expect(accept).toBeDisabled();
  await expect(accept).toHaveAttribute('title', /This attempt was a rehearsal/);
  await expect(page.getByText('This attempt was a rehearsal; its results are stand-ins and cannot be accepted as a delivery. Run it for real first.', { exact: true })).toBeVisible();
  expect(submissions).toBe(0);
});
