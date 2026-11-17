import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import type { ProjectCount } from '../src/api';

const projects: ProjectCount[] = ['Alpha', 'Beta'].map(name => ({
  project: name.toLowerCase(), name, registered: true, status: 'active',
  count: 0, runs: 0, lanes: 0, decisions: 0, workflowCount: 0,
  summary: `${name} purpose`, declarations: { purpose: { owner: `${name} owner` } },
}));
projects.push({ project: '', name: 'Unscoped', registered: false, status: 'active', count: 1, runs: 1, lanes: 0, decisions: 0, workflowCount: 1 });

async function mockProjects(page: Page) {
  const updates: { id: string; body: Record<string, unknown> }[] = [];
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url());
    if (url.pathname === '/api/auth/check') return route.fulfill({ json: { authRequired: false, authed: true } });
    if (url.pathname === '/api/health') return route.fulfill({ json: { ok: true } });
    if (url.pathname === '/api/projects') return route.fulfill({ json: projects });
    if (url.pathname.startsWith('/api/projects/')) {
      const id = url.pathname.split('/').at(-1)!;
      const project = projects.find(p => p.project === id)!;
      if (route.request().method() === 'PATCH') updates.push({ id, body: route.request().postDataJSON() });
      return route.fulfill({ json: { ...project, workflows: [] } });
    }
    return route.fulfill({ json: [] });
  });
  return updates;
}

test('switching projects cannot save another project’s draft', async ({ page }) => {
  const updates = await mockProjects(page);
  await page.goto('/projects/alpha');
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  await page.getByLabel('name', { exact: true }).fill('Unfinished Alpha draft');
  await page.getByRole('group', { name: 'Project scope' }).getByRole('button', { name: /^Beta/ }).click();
  await expect(page).toHaveURL(/\/projects\/beta$/);
  await expect(page.getByLabel('name', { exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  await expect(page.getByLabel('name', { exact: true })).toHaveValue('Beta');
  await page.getByLabel('name', { exact: true }).fill('Updated Beta');
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(page.getByLabel('name', { exact: true })).toHaveCount(0);
  expect(updates).toHaveLength(1);
  expect(updates[0]).toMatchObject({ id: 'beta', body: { name: 'Updated Beta', summary: 'Beta purpose', declarations: { purpose: { owner: 'Beta owner' } } } });
});

test('the Projects destination and browser history follow the URL selection', async ({ page }, testInfo) => {
  await mockProjects(page);
  await page.goto('/projects/alpha');
  await expect(page.getByRole('heading', { name: 'Projects', exact: true })).toBeVisible();
  await page.getByRole('navigation', { name: 'Primary' }).getByRole('link', { name: 'Projects', exact: true }).click();
  await expect(page).toHaveURL(/\/projects$/);
  await expect(page.getByRole('button', { name: 'Open project Alpha', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Open project Beta', exact: true })).toBeVisible();
  await expect(page.getByRole('group', { name: 'Project scope' }).getByRole('button', { name: /^all / })).toHaveAttribute('aria-pressed', 'true');
  await page.goBack();
  await expect(page).toHaveURL(/\/projects\/alpha$/);
  await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeVisible();
  await expect(page.getByRole('group', { name: 'Project scope' }).getByRole('button', { name: /^Alpha / })).toHaveAttribute('aria-pressed', 'true');
  await page.goForward();
  await expect(page.getByRole('button', { name: 'Open project Beta', exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('projects-index.png'), fullPage: true });
  await page.getByRole('button', { name: 'Close the projects panel', exact: true }).click();
  await expect(page).toHaveURL(/\/runs$/);
  await expect(page.getByRole('heading', { name: 'Runs', exact: true })).toBeVisible();
});

test('a late save on the previous project cannot close the current draft', async ({ page }) => {
  const updates = await mockProjects(page);
  let release!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  let alphaPatch: unknown;
  await page.route('**/api/projects/alpha', async route => {
    if (route.request().method() === 'PATCH') {
      alphaPatch = route.request().postDataJSON();
      await held;
    }
    await route.fulfill({ json: { ...projects[0], workflows: [] } });
  });
  await page.goto('/projects/alpha');
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  await page.getByLabel('name', { exact: true }).fill('Updated Alpha');
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Saving…', exact: true })).toBeDisabled();
  await page.getByRole('group', { name: 'Project scope' }).getByRole('button', { name: /^Beta/ }).click();
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  await page.getByLabel('name', { exact: true }).fill('Beta draft');
  const refreshed = page.waitForResponse(response => new URL(response.url()).pathname === '/api/projects');
  release();
  await refreshed;
  expect(alphaPatch).toMatchObject({ name: 'Updated Alpha', summary: 'Alpha purpose' });
  await expect(page.getByLabel('name', { exact: true })).toHaveValue('Beta draft');
  await page.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(page.getByLabel('name', { exact: true })).toHaveCount(0);
  expect(updates).toHaveLength(1);
  expect(updates[0]).toMatchObject({ id: 'beta', body: { name: 'Beta draft', summary: 'Beta purpose' } });
});

for (const source of ['detail chip', 'index bucket']) {
  test(`unscoped work stays unscoped when selected from the ${source}`, async ({ page }) => {
    await mockProjects(page);
    await page.goto(source === 'detail chip' ? '/projects/alpha' : '/projects');
    const unscopedRuns = page.waitForRequest(request => {
      const url = new URL(request.url());
      return url.pathname === '/api/runs' && url.searchParams.get('project') === '';
    });
    if (source === 'detail chip') {
      await page.getByRole('group', { name: 'Project scope' }).getByRole('button', { name: /^Unscoped/ }).click();
    } else {
      await page.getByRole('button', { name: 'Show only these', exact: true }).click();
    }
    await unscopedRuns;
    await expect(page).toHaveURL(/\/runs$/);
    await expect(page.getByRole('group', { name: 'Project scope' }).getByRole('button', { name: /^Unscoped/ })).toHaveAttribute('aria-pressed', 'true');
    await expect(page.getByRole('group', { name: 'Project scope' }).getByRole('button', { name: /^all / })).toHaveAttribute('aria-pressed', 'false');
  });
}
