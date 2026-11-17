import { test, expect } from '@playwright/test';

test('Board admits one SDK task, exposes errors, and cancels through its run', async ({ page }) => {
  let fail = true;
  let task = { id: 'task', laneId: 'lane', title: 'Report totals', detail: 'Create a report', status: 'todo', priority: 2,
    deps: [], updatedAt: new Date().toISOString(), createdAt: new Date().toISOString(), sdkRunId: '' };
  const work: unknown[] = [];
  const cancellation: string[] = [];
  const edits: unknown[] = [];
  await page.route('**/api/**', async route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/auth/check') return route.fulfill({ json: { authRequired: false, authed: true } });
    if (path === '/api/lanes') return route.fulfill({ json: [{ id: 'lane', name: 'Reporting', status: 'active', counts: {
      todo: task.status === 'todo' ? 1 : 0, inProgress: task.status === 'in_progress' ? 1 : 0, review: 0, done: 0, blocked: task.status === 'blocked' ? 1 : 0,
    } }] });
    if (path === '/api/lanes/lane/tickets') return route.fulfill({ json: [task] });
    if (path === '/api/lanes/lane/work') {
      work.push(route.request().postDataJSON());
      if (fail) return route.fulfill({ status: 409, json: { error: 'Target already has an active writer' } });
      task = { ...task, status: 'in_progress', sdkRunId: 'sdk-run' };
      return route.fulfill({ status: 202, json: { laneId: 'lane', ticketId: 'task', runId: 'sdk-run' } });
    }
    if (path === '/api/tickets/task' && route.request().method() === 'PATCH') {
      edits.push(route.request().postDataJSON());
      return route.fulfill({ status: 409, json: { error: 'Task changed while editing; refresh the Board' } });
    }
    if (path === '/api/runs/sdk-run/cancel') {
      cancellation.push(path);
      task = { ...task, status: 'blocked' };
      return route.fulfill({ json: { cancelled: 'sdk-run' } });
    }
    return route.fulfill({ json: path === '/api/health' ? { ok: true } : [] });
  });
  await page.goto('/board');
  await expect(page.getByRole('spinbutton')).toHaveCount(0);
  await page.getByRole('button', { name: 'Run next task in lane Reporting' }).click();
  await expect(page.getByRole('alert')).toContainText('Target already has an active writer');
  expect(work).toEqual([{ count: 1 }]);
  fail = false;
  await page.getByRole('button', { name: 'Run next task in lane Reporting' }).click();
  await expect(page.getByRole('button', { name: 'Run next task in lane Reporting' })).toBeDisabled();
  await expect(page.getByRole('button', { name: 'Open started run' })).toBeVisible();
  await page.getByRole('button', { name: 'Open ticket: Report totals' }).click();
  const drawer = page.getByRole('dialog', { name: 'Ticket: Report totals' });
  await expect(drawer.getByRole('button', { name: 'Set status to Done', exact: true })).toBeDisabled();
  await drawer.getByRole('button', { name: 'Cancel task' }).click();
  await expect(drawer.getByRole('button', { name: 'Cancel task' })).toHaveCount(0);
  await expect(drawer.getByRole('button', { name: 'Set status to Blocked', exact: true })).toHaveAttribute('aria-pressed', 'true');
  expect(cancellation).toEqual(['/api/runs/sdk-run/cancel']);
  await drawer.getByRole('button', { name: 'Set status to Done', exact: true }).click();
  await expect(drawer.getByRole('alert')).toContainText('Task changed while editing');
  expect(edits).toEqual([{ status: 'done', expectedRunId: 'sdk-run', expectedUpdatedAt: task.updatedAt }]);
  await expect(drawer.getByRole('button', { name: 'Set status to Blocked', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await drawer.getByRole('button', { name: 'Open run' }).click();
  await expect(page).toHaveURL(/\/runs\/sdk-run$/);
});

test('Board creates tasks with the numeric priority accepted by the backend', async ({ page }) => {
  let created: unknown;
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/auth/check') return route.fulfill({ json: { authRequired: false, authed: true } });
    if (path === '/api/lanes') return route.fulfill({ json: [{ id: 'lane', name: 'Reporting', status: 'active', counts: { todo: 0, inProgress: 0, review: 0, done: 0, blocked: 0 } }] });
    if (path === '/api/tickets' && route.request().method() === 'POST') {
      created = route.request().postDataJSON();
      return route.fulfill({ status: 201, json: { id: 'created', ...created as object } });
    }
    return route.fulfill({ json: path === '/api/health' ? { ok: true } : [] });
  });
  await page.goto('/board');
  await page.getByRole('button', { name: 'Add ticket to lane Reporting' }).click();
  const form = page.getByRole('dialog', { name: 'Add ticket to Reporting' });
  await form.getByLabel('Title', { exact: true }).fill('Analyze totals');
  await form.getByRole('button', { name: 'High', exact: true }).click();
  await form.getByRole('button', { name: /Add ticket/i }).click();
  await expect.poll(() => created).toEqual({ laneId: 'lane', title: 'Analyze totals', priority: 3 });
});
