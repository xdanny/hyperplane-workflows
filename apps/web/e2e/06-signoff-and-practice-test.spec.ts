/**
 * Two remedies the audited build made unreachable from the app (M-11, D-04).
 *
 * M-11: twelve finished runs, twelve "awaiting your sign-off" cards, no way to
 * say that a workflow's runs do not need one. The policy is now a control in
 * the editor, saved with the workflow, and it states its consequence before it
 * is chosen.
 *
 * D-04: Approve on a learned draft was correctly blocked until the skill had a
 * practice test, and the only remedy printed on the card was a terminal
 * command — so thirteen of thirteen skills had none. The same generator is now
 * a button on the card that holds the blocked Approve.
 */

import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import type { SDKWorkflow, Skill } from '../src/api';

const draft: Skill = {
  id: 'constrained-short-form-drafting',
  name: 'Constrained short-form drafting',
  description: 'Draft under a hard word limit, then verify the limit before delivering.',
  category: 'writing',
  metadata: {
    status: 'draft',
    sourceRun: '20260912-aaaaaaaa',
    supportRuns: 1,
    evidence: [{ runId: '20260912-aaaaaaaa', nodeId: 'draft', quote: 'The note contains 121 words, within the 150-word limit.' }],
  },
};

async function mockInbox(page: Page, opts: { evalsets?: unknown[] } = {}) {
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/auth/check') return route.fulfill({ json: { authRequired: false, authed: true } });
    if (path === '/api/health') return route.fulfill({ json: { ok: true } });
    if (path === '/api/evals') return route.fulfill({ json: opts.evalsets ?? [] });
    if (path === '/api/inbox') return route.fulfill({ json: { approvals: [], selections: [], outcomes: [], proposals: [draft] } });
    return route.fulfill({ json: [] });
  });
}

test('the editor sets a sign-off policy, and each option says what it does', async ({ page }) => {
  let saved: SDKWorkflow | undefined;
  await page.route('**/api/**', async route => {
    const url = new URL(route.request().url());
    if (url.pathname === '/api/auth/check') return route.fulfill({ json: { authRequired: false, authed: true } });
    if (url.pathname === '/api/health') return route.fulfill({ json: { ok: true } });
    if (url.pathname === '/api/workflows' && route.request().method() === 'POST') {
      saved = route.request().postDataJSON() as SDKWorkflow;
      return route.fulfill({ status: 201, json: { ...saved, id: 'saved-fixture', revision: 1 } });
    }
    return route.fulfill({ json: [] });
  });
  await page.goto('/workflows');
  await page.getByRole('button', { name: 'Project task', exact: true }).click();

  const control = page.getByRole('region', { name: 'When a run of this workflow finishes' });
  const select = control.getByLabel('Sign-off', { exact: true });
  // Today's behaviour is the default, and it is stated rather than implied.
  await expect(select).toHaveValue('required');
  await expect(control.getByText('Every finished run waits in your Inbox until you accept, request changes or reject it.')).toBeVisible();

  await select.selectOption('optional');
  await expect(control.getByText('Finished runs never enter your Inbox. You can still record a decision on the run page or in its history at any time.')).toBeVisible();

  await select.selectOption('auto_when_checks_pass');
  await expect(control.getByText('A run that passes every check it declared is accepted by Hyperplane itself', { exact: false })).toBeVisible();
  // Automatic is never a person's judgment, and the editor says so where the
  // choice is made rather than leaving it to be discovered on a run page.
  await expect(control.getByText('never counts as a person’s decision', { exact: false })).toBeVisible();

  await page.getByLabel('What should change?').fill('Recompute the weekly totals');
  await page.getByLabel('Working folder', { exact: true }).fill('/tmp/project');
  await page.getByLabel('Workflow name').fill('Weekly totals');
  await page.getByRole('button', { name: 'Save workflow', exact: true }).click();
  await expect(page.getByRole('status')).toHaveText('Workflow saved');
  // Saving a policy starts nothing and calls no model; it records a setting.
  expect(saved?.signOff).toBe('auto_when_checks_pass');
});

test('a blocked Approve offers the practice test it is blocked on', async ({ page }) => {
  let generated = 0;
  await mockInbox(page);
  await page.route('**/api/skills/*/evalset/generate', route => {
    generated += 1;
    // 201 with the CASES themselves, not a count.
    return route.fulfill({ status: 201, json: { evalsetId: 'es-1', cases: Array.from({ length: 8 }, (_, i) => ({ id: `case-${i}`, input: 'x', expect: [] })) } });
  });
  await page.goto('/inbox');

  const card = page.locator('.inbox-card').filter({ hasText: 'Constrained short-form drafting' });
  await expect(card.getByRole('button', { name: /^Approve is blocked/ })).toBeDisabled();
  // The rule still states itself, and the command is still there for anyone who
  // wants it — what is new is that the card can do it.
  await expect(card.getByText('hyperplane eval gen constrained-short-form-drafting')).toBeVisible();

  const generate = card.getByRole('button', { name: /^Generate a practice test/ });
  await expect(generate).toBeVisible();
  await generate.click();
  await expect.poll(() => generated).toBe(1);
  await expect(card.getByText('Practice test created — 8 cases.')).toBeVisible();
  // Generating a test is not approving anything: the person still decides.
  await expect(card.getByText('a test nobody has read is not evidence that the method works', { exact: false })).toBeVisible();
});

test('an existing practice test is replaced only on purpose, never by a retry', async ({ page }) => {
  const calls: { replace?: boolean }[] = [];
  await mockInbox(page);
  await page.route('**/api/skills/*/evalset/generate', route => {
    const body = route.request().postDataJSON() as { replace?: boolean };
    calls.push(body);
    return body.replace
      ? route.fulfill({ status: 201, json: { evalsetId: 'es-2', cases: Array.from({ length: 6 }, (_, i) => ({ id: `case-${i}`, input: 'x', expect: [] })) } })
      // A 409 names the evalset that already exists.
      : route.fulfill({ status: 409, json: { error: 'an evalset already exists for this skill', evalsetId: 'es-1' } });
  });
  await page.goto('/inbox');

  const card = page.locator('.inbox-card').filter({ hasText: 'Constrained short-form drafting' });
  await card.getByRole('button', { name: /^Generate a practice test/ }).click();
  await expect(card.getByText('This skill already has a practice test.', { exact: false })).toBeVisible();
  // A 409 is answered with an explicit replace, not an automatic one.
  expect(calls).toEqual([{}]);

  await card.getByRole('button', { name: /^Replace the existing practice test/ }).click();
  await expect(card.getByText('Practice test created — 6 cases.')).toBeVisible();
  expect(calls).toEqual([{}, { replace: true }]);
});
