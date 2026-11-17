/**
 * The structured check contract (D-06).
 *
 * "Recompute prediction metrics" described itself in one ~450-word block. The
 * sentence that mattered — "it does not establish label/holdout custody,
 * train/holdout isolation, split correctness or frozen-model provenance" — was
 * exactly right and nobody was going to find it. The catalog now carries four
 * short arrays; these fixtures pin that all four are labelled where a check is
 * chosen, and that the two claims a recorded result turns on are beside that
 * result in the inspector.
 */

import { test, expect } from '@playwright/test';
import { validationIdentity } from './workflowValidationFixture';

const check = {
  id: 'prediction-metrics',
  title: 'Recompute prediction metrics',
  summary: 'Recomputes the submitted labels and scores against the holdout you declared.',
  // The prescriptive text the SDK hands the step — the ~450-word block D-06
  // named. It is still reachable, one disclosure in, exactly as written.
  description: 'Write predictions.csv and metrics.json. Do not round scores. Do not re-fit after scoring the holdout. Report the operating point you claim.',
  produces: [
    'predictions.csv with one row per holdout record.',
    'metrics.json naming the operating point you claim.',
  ],
  recomputes: ['PR-AUC and precision at the declared operating point, from your submitted file.'],
  proves: ['The reported metrics match what the submitted predictions actually score.'],
  doesNotProve: [
    'Label or holdout custody.',
    'Train/holdout isolation.',
    'That the model was frozen before the holdout was scored.',
  ],
};

const HEADINGS = [
  'Files you must produce',
  'What the host recomputes',
  'What a pass proves',
  'What it does not prove',
];

test('choosing a check shows all four contract lists, not one paragraph', async ({ page }) => {
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/workflow-checks') return route.fulfill({ json: { checks: [check] } });
    if (path === '/api/workflows/validate') return route.fulfill({ json: { ...validationIdentity, valid: true } });
    if (path === '/api/compose') return route.fulfill({ json: { workflow: {
      name: 'Baseline', instruction: 'Build and evaluate a baseline', kind: 'nodes',
      nodes: [{ id: 'baseline', title: 'Score the holdout', kind: 'task', instruction: 'Score it.', acceptance: 'prediction-metrics' }],
    }, unsupported: [] } });
    return route.fulfill({ json: path === '/api/auth/check' ? { authRequired: false, authed: true } : path === '/api/health' ? { ok: true } : [] });
  });
  await page.goto('/workflows');
  await page.getByLabel('What do you want to achieve?').fill('Predict which trips are low-tip');
  await page.getByRole('button', { name: 'Suggest steps from goal' }).click();
  await page.getByText(`Extra check requirements · ${check.title}`, { exact: true }).click();

  // The intro stays one short paragraph; the methodology is the four lists,
  // and the agent's own instructions are one disclosure further in.
  await expect(page.getByText(check.summary, { exact: true })).toBeVisible();
  // Present in the DOM but closed away: reachable, not in the way.
  await expect(page.getByText(check.description, { exact: true })).not.toBeVisible();
  for (const heading of HEADINGS) {
    await expect(page.getByRole('heading', { name: heading, exact: true })).toBeVisible();
  }
  await expect(page.getByText('predictions.csv with one row per holdout record.', { exact: true })).toBeVisible();
  // The boundary is a list a reader can scan, not a clause in a wall of prose.
  const boundary = page.locator('.check-claim--doesNotProve');
  await expect(boundary.getByRole('listitem')).toHaveCount(3);
  await expect(boundary).toContainText('Train/holdout isolation.');

  await page.getByText('The exact instructions this check gives the step', { exact: true }).click();
  await expect(page.getByText(check.description, { exact: true })).toBeVisible();
});

test('a catalog entry with no contract arrays renders no empty headings', async ({ page }) => {
  const bare = { id: 'schema-match', title: 'Match a schema', description: 'Require the supplied schema.' };
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/workflow-checks') return route.fulfill({ json: { checks: [bare] } });
    if (path === '/api/workflows/validate') return route.fulfill({ json: { ...validationIdentity, valid: true } });
    if (path === '/api/compose') return route.fulfill({ json: { workflow: {
      name: 'Inventory', instruction: 'Make an inventory', kind: 'nodes',
      nodes: [{ id: 'match', title: 'Create inventory', kind: 'task', instruction: 'List it.', acceptance: 'schema-match' }],
    }, unsupported: [] } });
    return route.fulfill({ json: path === '/api/auth/check' ? { authRequired: false, authed: true } : path === '/api/health' ? { ok: true } : [] });
  });
  await page.goto('/workflows');
  await page.getByLabel('What do you want to achieve?').fill('Make an inventory');
  await page.getByRole('button', { name: 'Suggest steps from goal' }).click();
  await page.getByText(`Extra check requirements · ${bare.title}`, { exact: true }).click();
  await expect(page.getByText(bare.description, { exact: true })).toBeVisible();
  await expect(page.getByText('The exact instructions this check gives the step', { exact: true })).toHaveCount(0);
  for (const heading of HEADINGS) {
    await expect(page.getByRole('heading', { name: heading, exact: true })).toHaveCount(0);
  }
});

test('the inspector puts what a pass proves — and does not — beside the recorded result', async ({ page }) => {
  const nodes = [
    { id: 'baseline', title: 'Score the holdout', kind: 'task', instruction: 'Score it.', acceptance: 'prediction-metrics' },
  ];
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/workflow-checks') return route.fulfill({ json: { checks: [check] } });
    if (path === '/api/runs/scored-run') return route.fulfill({ json: { run: {
      runId: 'scored-run', dag: 'node-workflow', state: 'passed', goal: 'Score the holdout',
      options: { runtime: 'codex-sdk', request: { workflow: 'node-workflow', nodes } },
      doc: { sdk: { phase: 'passed', nodes: [{ id: 'baseline', phase: 'passed', verification: 'passed' }] } },
    }, nodes: [], outcome: null, journey: null } });
    return route.fulfill({ json: path === '/api/auth/check' ? { authRequired: false, authed: true } : path === '/api/health' ? { ok: true } : [] });
  });
  await page.goto('/runs/scored-run#sdk-step-baseline');
  const verification = page.locator('#sdk-step-baseline').getByRole('region', { name: 'Step verification' });

  await expect(verification.getByRole('heading', { name: 'What a pass proves', exact: true })).toBeVisible();
  await expect(verification.getByRole('heading', { name: 'What it does not prove', exact: true })).toBeVisible();
  await expect(verification).toContainText('That the model was frozen before the holdout was scored.');
  // The setup half belongs where a check is chosen, not on a finished receipt.
  await expect(verification.getByRole('heading', { name: 'Files you must produce', exact: true })).toHaveCount(0);
  // And the receipt keeps saying what it does and does not store itself.
  await expect(verification).toContainText('The check catalog description is not stored in this receipt.');
});
