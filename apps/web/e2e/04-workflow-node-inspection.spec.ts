import { test, expect } from '@playwright/test';
import { fulfillWorkflowValidation } from './workflowValidationFixture';

const title = 'Research practical training needs and the gaps in current tools for first-time long-distance triathletes';
const instructions = 'Find dated evidence and clearly distinguish observation from assumptions.\n' + 'Keep the exact owner criteria and source limitations. '.repeat(35);
const sha256 = 'a'.repeat(64);
const nodes = [
  {id:'research', title, kind:'research', instruction:instructions, dependsOn:[]},
  {id:'review', title:'Review evidence against the original research criteria', kind:'check', instruction:'Reject unsupported audience or accuracy claims.', dependsOn:['research']},
  {id:'report', title:'Create an editable planning report', kind:'task', instruction:'Use the reviewed evidence and owner inputs to create the report.', dependsOn:['review'], acceptance:'data-quality-profile', inputs:{dataset:'/data/triathlon.csv',keyColumns:'athlete_id\nsession_id'}, access:{hosts:['data.example.com'],readPaths:['/data/triathlon.csv'],environment:['WORKFLOW_TRAINING']}, toolchain:false, sideEffect:false},
];

test.beforeEach(async ({ page }) => {
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/workflows/validate') return fulfillWorkflowValidation(route);
    if (path === '/api/runs/rich-run') return route.fulfill({json:{run:{runId:'rich-run',state:'failed',goal:'Prepare a credible training-app proposal',dag:'node-workflow',options:{runtime:'codex-sdk',model:'recorded-model',request:{workflow:'node-workflow',instruction:'Preserve research, review and owner requirements.',nodes}},doc:{sdk:{phase:'failed',nodes:[
      // `searches` stays the count; `searchLog` carries the entries. This SDK's
      // web_search events often have only a query, so one entry has no urls.
      {id:'research',phase:'passed',attempts:1,searches:3,searchLog:[
        {query:'first-time long-distance triathlete training gaps',urls:['https://www.triathlon.org/research','https://journals.example.org/endurance'],at:'2026-09-12T13:02:00Z'},
        {query:'triathlon coaching app market size 2026',urls:['https://reports.example.com/triathlon-2026'],at:'2026-09-12T13:02:30Z'},
        {query:'long course training plan adherence',at:'2026-09-12T13:03:00Z'},
      ],document:{path:'actual research report.md',bytes:452,sha256}},
      {id:'review',phase:'failed',searches:1,findings:['The claimed audience size has no dated source.'],document:{path:'review.md',bytes:221,sha256}},
      {id:'report',phase:'pending',verification:'pending',files:[{path:'../unsafe.txt',bytes:99,sha256},{path:'https://outside.example/file',bytes:99,sha256}]},
    ]}}},nodes:[],outcome:null,journey:null}});
    return route.fulfill({json:path === '/api/auth/check' ? {authRequired:false,authed:true} : path === '/api/health' ? {ok:true} : []});
  });
});
const graph = (page: import('@playwright/test').Page) => page.getByRole('region',{name:'Workflow graph',exact:true});
// A straight chain of five steps or fewer keeps its diagram behind a toggle.
async function showDiagram(page: import('@playwright/test').Page) {
  const toggle = graph(page).getByRole('button',{name:'Show diagram',exact:true});
  if (await toggle.count()) await toggle.click();
}

test('rich cards expose purpose and type; keyboard inspection uses exact recorded evidence and expandable instructions', async ({ page }, testInfo) => {
  await page.goto('/runs/rich-run');
  await expect(graph(page)).toContainText('3 steps, each waiting for the one before it.');
  await showDiagram(page);
  const card = graph(page).locator('[data-node-id="research"]');
  await expect(card.locator('.workflow-node-type')).toContainText('Research');
  await expect(card.getByText('Document saved',{exact:true})).toBeVisible();
  await expect(card.locator('.workflow-graph-excerpt')).toContainText('Find dated evidence');
  expect(await card.locator('.workflow-graph-title').evaluate(element => element.getBoundingClientRect().height)).toBeGreaterThan(30);
  await card.getByRole('link').focus(); await page.keyboard.press('Enter');
  const inspector = page.locator('#sdk-step-research');
  await expect(inspector).toBeFocused();
  await expect(page).toHaveURL(/#sdk-step-research$/);
  await expect(inspector.getByRole('heading',{name:title,exact:true})).toBeVisible();
  await expect(inspector.getByRole('link',{name:'actual research report.md',exact:true})).toHaveAttribute('href','/file/rich-run/nodes/delivery/OUTPUTS/actual%20research%20report.md');
  await expect(inspector.locator('a[href$="/research.md"]')).toHaveCount(0);
  const full = inspector.locator('details').filter({has: page.getByText('Read full instructions',{exact:true})});
  await expect(full).not.toHaveAttribute('open','');
  await full.locator('summary').click();
  await expect(full.locator('p')).toHaveText(instructions);
  await full.locator('summary').click();
  await inspector.getByText('Tools and access',{exact:true}).click();
  await expect(inspector).toContainText('Live web search is enabled.');
  await expect(inspector).toContainText('Model: recorded-model.');
  await inspector.getByText('Recorded execution details',{exact:true}).click();
  await expect(inspector.getByText('Completed web searches',{exact:true})).toBeVisible();
  // D-01: the searches themselves, so a reader can tell a recalled citation
  // from a query that was actually run — and the line still refuses to claim
  // that any page was read.
  await expect(inspector.getByRole('heading',{name:'Recorded searches',exact:true})).toBeVisible();
  await expect(inspector.getByText('Search: first-time long-distance triathlete training gaps → www.triathlon.org, journals.example.org')).toBeVisible();
  await expect(inspector.getByText('Search: long course training plan adherence',{exact:true})).toBeVisible();
  await expect(inspector.getByRole('link',{name:'https://reports.example.com/triathlon-2026',exact:true})).toBeVisible();
  await expect(inspector.getByText('does not establish that a page was read',{exact:false})).toBeVisible();
  await inspector.screenshot({path:testInfo.outputPath('research-step-inspector.png')});
  await graph(page).screenshot({path:testInfo.outputPath('rich-workflow-graph.png')});
});

test('review verdicts and pending file work keep findings, ancestor context and expected outputs separate', async ({ page }) => {
  await page.goto('/runs/rich-run#sdk-step-review');
  const review = page.locator('#sdk-step-review');
  await expect(review).toBeFocused();
  await expect(review).toContainText('independent model review failed');
  await expect(review).toContainText('The claimed audience size has no dated source.');
  await expect(review.getByRole('link',{name:'Review evidence against the original research criteria',exact:true})).toHaveAttribute('href','/file/rich-run/nodes/delivery/OUTPUTS/review.md');
  await graph(page).locator('[data-node-id="report"]').getByRole('link').click();
  const pending = page.locator('#sdk-step-report');
  await expect(pending).toBeFocused();
  await expect(pending).toContainText('Files and work summary');
  await expect(pending).toContainText('No file evidence is recorded');
  await expect(pending.getByRole('link')).toHaveCount(0);
  await expect(pending).toContainText('result has not been recorded');
  await expect(pending).not.toContainText('Reload the catalog');
  await pending.getByText('All prerequisite context (2 steps)',{exact:true}).click();
  await expect(pending).toContainText(title);
  await pending.getByText('Declared inputs (2)',{exact:true}).click();
  await expect(pending.getByText('athlete_id\nsession_id',{exact:true})).toBeVisible();
  await pending.getByText('Tools and access',{exact:true}).click();
  await expect(pending).toContainText('WORKFLOW_TRAINING');
  await expect(pending).toContainText('Additional tool installation is not permitted.');
});

test('mobile inspector retains readable full fields and graph overflow stays inside its bounded canvas', async ({ page }, testInfo) => {
  await page.setViewportSize({width:390,height:844});
  await page.goto('/runs/rich-run#sdk-step-report');
  const pending = page.locator('#sdk-step-report');
  await expect(pending).toBeFocused();
  await pending.getByText('Declared inputs (2)',{exact:true}).click();
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await showDiagram(page);
  const dimensions = await graph(page).getByRole('region',{name:'Dependency diagram. Scroll to see all branches.'}).evaluate(element => ({height:element.clientHeight,total:element.scrollHeight,width:element.clientWidth,across:element.scrollWidth}));
  expect(dimensions.across).toBeGreaterThan(dimensions.width);
  expect(dimensions.total).toBeLessThanOrEqual(dimensions.height + 1);
  expect(dimensions.height).toBeLessThan(600);
  await pending.screenshot({path:testInfo.outputPath('step-inspector-mobile.png')});
});

test('editor inspection preserves Edit focus and clears details when the selected step is removed', async ({ page }) => {
  await page.goto('/workflows');
  await page.getByLabel('What do you want to achieve?').fill('Write a short note');
  await page.getByRole('button',{name:/^Draft/}).click();
  await showDiagram(page);
  await graph(page).getByRole('button',{name:'Inspect Draft',exact:true}).click();
  const inspector = page.locator('.workflow-node-details');
  await expect(inspector).toBeFocused();
  await expect(inspector).toContainText('Expected outputs describe the plan');
  await expect(inspector.getByRole('link')).toHaveCount(0);
  await graph(page).getByRole('button',{name:/^Edit Draft\./}).click();
  await expect(page.getByLabel('Step 1 name')).toBeFocused();
  await page.getByRole('button',{name:'Remove step 1',exact:true}).click();
  await expect(inspector).toHaveCount(0);
});
