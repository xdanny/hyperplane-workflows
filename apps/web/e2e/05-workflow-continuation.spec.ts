import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import type { ContinuationReview, SDKWorkflowNode } from '../src/api';

const nodes:SDKWorkflowNode[]=[
  {id:'plan',title:'Write the plan',instruction:'Write a plan.'},
  {id:'approve',title:'Approve the plan',kind:'approval',instruction:'Review the plan.'},
  {id:'research',title:'Research the next step',kind:'research',instruction:'Research the source.'},
];
const review:ContinuationReview={eligible:true,reviewToken:'review-a',reused:[{id:'plan',title:'Write the plan',kind:'document'}],restart:{id:'research',title:'Research the next step',kind:'research',index:2},renewApprovals:[{id:'approve',title:'Approve the plan'}],runtimeFingerprint:'a'.repeat(64),usageNote:'Committed usage is carried forward as a lower bound. The budget is not reset.'};
function stoppedRun() {
  return {runId:'stopped',goal:'Research a useful plan',dag:'node-workflow',state:'interrupted',failingNode:'Research the next step',startedAt:'2026-09-07T12:00:00Z',updatedAt:'2026-09-07T12:01:00Z',options:{runtime:'codex-sdk',request:{workflow:'node-workflow',instruction:'Research a useful plan',nodes}},doc:{sdk:{phase:'interrupted',threadId:'old-session',nodes:nodes.map((node,index)=>({...node,phase:index<2?'passed':'running'}))}}};
}
async function successor(page:Page) {
  await page.route('**/api/runs/continued',route=>route.fulfill({json:{run:{...stoppedRun(),runId:'continued',state:'running',doc:{sdk:{phase:'approval_pending',approval:{id:'continued:approve:approval',title:'Approve the plan again',prompt:'Review the copied plan for this new run.',documents:[{name:'Copied plan',url:'/file/continued/nodes/delivery/OUTPUTS/plan.md',sha256:'a'.repeat(64)}]},nodes:nodes.map((node,index)=>({...node,phase:index===0?'passed':index===1?'approval_pending':'pending'}))}}},nodes:[]}}));
}

test.beforeEach(async({page})=>{
  await page.route('**/api/**',route=>{
    const path=new URL(route.request().url()).pathname;
    return route.fulfill({json:path==='/api/auth/check'?{authRequired:false,authed:true}:path==='/api/health'?{ok:true}:path==='/api/runs/stopped'?{run:stoppedRun(),nodes:[]}:path==='/api/runs'?[stoppedRun()]:[]});
  });
  await successor(page);
});

test('review is read-only, shows verified reuse and renewed approvals, and starts exactly one reviewed continuation',async({page},testInfo)=>{
  const posted:unknown[]=[];let finish:()=>Promise<void>=async()=>{};
  await page.route('**/api/runs/stopped/continuation',async route=>{
    if(route.request().method()==='GET') return route.fulfill({json:review});
    posted.push(route.request().postDataJSON());
    await new Promise<void>(resolve=>{finish=async()=>{await route.fulfill({status:202,json:{runId:'continued'}});resolve();};});
  });
  const decisions:string[]=[];
  await page.route('**/api/approvals/*/decision',route=>{decisions.push(new URL(route.request().url()).pathname);return route.fulfill({json:{state:'approved'}});});
  await page.goto('/runs/stopped');
  await expect(page.getByText('Researching live sources',{exact:true})).toHaveCount(0);
  await expect(page.getByRole('region',{name:'Step progress'})).toContainText('Stopped');
  await expect(page.getByRole('button',{name:/^Continue from/})).toHaveCount(0);
  await page.getByRole('button',{name:'Review continuation',exact:true}).click();
  const panel=page.getByRole('region',{name:'Continue stopped workflow'});
  await expect(panel.getByRole('heading',{name:'Keep completed work'})).toBeVisible();
  await expect(panel.getByRole('link',{name:'Write the plan'})).toHaveAttribute('href','/file/stopped/nodes/delivery/OUTPUTS/plan.md');
  await expect(panel).toContainText('partial outputs stay with this stopped run');
  await expect(panel).toContainText('Approvals needed again');
  await expect(panel).toContainText('The budget is not reset');
  expect(posted).toEqual([]);
  await page.setViewportSize({width:390,height:844});
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await panel.screenshot({path:testInfo.outputPath('continuation-review-mobile.png')});
  await page.getByRole('button',{name:'Continue from Research the next step',exact:true}).evaluate(button=>{(button as HTMLButtonElement).click();(button as HTMLButtonElement).click();});
  await expect.poll(()=>posted.length).toBe(1);
  expect(posted).toEqual([{reviewToken:'review-a'}]);
  await expect(page.getByRole('button',{name:'Starting continued run…'})).toBeDisabled();
  await finish();
  await expect(page).toHaveURL(/\/runs\/continued$/);
  await expect(page.getByRole('heading',{name:'Approve the plan again'})).toBeVisible();
  await page.getByRole('button',{name:'Approve and continue'}).click();
  expect(decisions).toEqual(['/api/approvals/continued%3Aapprove%3Aapproval/decision']);
});

test('changed evidence and unreconciled effects use the server reason and cannot launch',async({page})=>{
  let reason='The completed file plan.md changed; restore the recorded evidence.';let posts=0;
  await page.route('**/api/runs/stopped/continuation',route=>{
    if(route.request().method()==='POST')posts++;
    return route.fulfill({json:{eligible:false,reason}});
  });
  await page.goto('/runs/stopped');
  // A refusal that names something the reader can act on is rendered verbatim:
  // the server knows things this page does not.
  for(const message of [reason,'The unfinished connected task needs effect reconciliation before continuation.']) {
    reason=message;
    await page.getByRole('button',{name:'Review continuation',exact:true}).click();
    await expect(page.getByRole('region',{name:'Continue stopped workflow'}).getByRole('alert')).toHaveText(message);
    await expect(page.getByRole('button',{name:/^Continue from/})).toHaveCount(0);
  }
  expect(posts).toBe(0);
  await expect(page.getByRole('link',{name:'Use this setup again'})).toBeVisible();
});

test('a runtime-change refusal says what changed and that the finished work is still here (M-16)',async({page})=>{
  let posts=0;
  // The stopped run finished its first step and recorded the document for it,
  // which is the work the audited copy read as destroyed.
  const withDocument=stoppedRun();
  (withDocument.doc.sdk.nodes as Record<string,unknown>[])[0].document={path:'plan.md',bytes:120,sha256:'b'.repeat(64)};
  await page.route('**/api/runs/stopped',route=>route.fulfill({json:{run:withDocument,nodes:[]}}));
  await page.route('**/api/runs/stopped/continuation',route=>{
    if(route.request().method()==='POST')posts++;
    return route.fulfill({json:{eligible:false,reason:'The SDK implementation has changed since this run.',
      reused:[{id:'plan',title:'Write the plan',kind:'document'}]}});
  });
  await page.goto('/runs/stopped');
  const section=page.getByRole('region',{name:'Continue stopped workflow'});
  await page.getByRole('button',{name:'Review continuation',exact:true}).click();
  // Not "the SDK implementation": a component nobody outside the codebase can
  // see, in a sentence that read as though the finished work was gone.
  await expect(section.getByRole('alert')).toHaveText('This app’s runtime was updated after this run, so it cannot be continued. Its completed steps stay readable here; start a new run with the same setup.');
  await expect(section.getByText('open them from “Your steps” below',{exact:false})).toBeVisible();
  await expect(section).not.toContainText('SDK implementation');
  // The completed step is still on the page, with the button that opens it.
  await expect(page.getByRole('region',{name:'Step progress'}).getByRole('button',{name:'Read document',exact:true})).toBeVisible();
  await expect(section.getByRole('heading',{name:'Keep completed work'})).toBeVisible();
  expect(posts).toBe(0);
  await expect(page.getByRole('link',{name:'Use this setup again'})).toBeVisible();
});

test('a stale review is discarded and a new review needs fresh explicit confirmation',async({page})=>{
  const posted:string[]=[];let reads=0;
  await page.route('**/api/runs/stopped/continuation',route=>{
    if(route.request().method()==='GET')return route.fulfill({json:{...review,reviewToken:++reads===1?'old-review':'new-review'}});
    posted.push(route.request().postDataJSON().reviewToken);
    return posted.length===1?route.fulfill({status:409,json:{error:'The reviewed state changed.'}}):route.fulfill({status:202,json:{runId:'continued'}});
  });
  await page.goto('/runs/stopped');
  await page.getByRole('button',{name:'Review continuation',exact:true}).click();
  await page.getByRole('button',{name:'Continue from Research the next step'}).click();
  await expect(page.getByRole('alert')).toContainText('out of date');
  await expect(page.getByRole('button',{name:/^Continue from/})).toHaveCount(0);
  expect(reads).toBe(1);
  await page.getByRole('button',{name:'Review continuation',exact:true}).click();
  await expect(page.getByRole('button',{name:'Continue from Research the next step'})).toBeEnabled();
  expect(posted).toEqual(['old-review']);
  await page.getByRole('button',{name:'Continue from Research the next step'}).click();
  await expect(page).toHaveURL(/\/runs\/continued$/);
  expect(posted).toEqual(['old-review','new-review']);
});

test('an uncertain response discovers the committed successor without submitting again',async({page})=>{
  let posted=0;let reads=0;
  await page.route('**/api/runs/stopped/continuation',route=>{
    if(route.request().method()==='GET'){reads++;return route.fulfill({json:posted?{eligible:false,continuedRunId:'continued'}:review});}
    posted++;return route.abort('connectionreset');
  });
  await page.goto('/runs/stopped');
  await page.getByRole('button',{name:'Review continuation',exact:true}).click();
  await page.getByRole('button',{name:'Continue from Research the next step'}).click();
  await expect(page.getByRole('button',{name:'Open continued run'})).toBeVisible();
  await expect(page.getByRole('button',{name:/^Continue from/})).toHaveCount(0);
  expect(posted).toBe(1);expect(reads).toBe(2);
  await page.getByRole('button',{name:'Open continued run'}).click();
  await expect(page).toHaveURL(/\/runs\/continued$/);
});

test('refresh discovers another client continuation and run-file failures have an explicit retry',async({page})=>{
  let filesAvailable=false;let posts=0;
  await page.route('**/api/runs/stopped/artifacts',route=>filesAvailable?route.fulfill({json:[{name:'Plan',url:'/file/stopped/nodes/delivery/OUTPUTS/plan.md'}]}):route.fulfill({status:503,json:{error:'Files unavailable'}}));
  await page.route('**/api/runs/stopped/continuation',route=>{if(route.request().method()==='POST')posts++;return route.fulfill({json:{eligible:false,continuedRunId:'continued'}});});
  await page.goto('/runs');
  // A stopped run reads STOPPED in the one status vocabulary (F-H); the row
  // still names where it stopped, from the legacy `failingNode` this fixture
  // carries rather than the round-11 `failingStep`.
  await expect(page.getByText('stopped at Research the next step',{exact:true})).toBeVisible();
  await page.getByRole('button',{name:/Research a useful plan — STOPPED/}).click();
  await expect(page.getByRole('button',{name:'Retry run files'})).toBeVisible();
  filesAvailable=true;
  await page.getByRole('button',{name:'Retry run files'}).click();
  // Run files open in the in-app reader now rather than a bare browser tab.
  await expect(page.getByRole('button',{name:'Plan',exact:true})).toBeVisible();
  await page.getByRole('button',{name:'Refresh run status'}).click();
  await expect(page.getByRole('button',{name:'Open continued run'})).toBeVisible();
  expect(posts).toBe(0);
});

test('all completed steps can finish without inventing a restart step',async({page})=>{
  let posted:unknown;
  await page.route('**/api/runs/stopped/continuation',route=>{
    if(route.request().method()==='GET')return route.fulfill({json:{...review,restart:null}});
    posted=route.request().postDataJSON();return route.fulfill({status:202,json:{runId:'continued'}});
  });
  await page.goto('/runs/stopped');
  await page.getByRole('button',{name:'Review continuation',exact:true}).click();
  await expect(page.getByRole('region',{name:'Continue stopped workflow'})).toContainText('without repeating completed model work');
  await page.getByRole('button',{name:'Finish with completed work',exact:true}).click();
  await expect(page).toHaveURL(/\/runs\/continued$/);
  expect(posted).toEqual({reviewToken:'review-a'});
});

test('another client using the same review token opens the same committed continuation',async({page})=>{
  const tokens:string[]=[];let committed=false;let creations=0;
  await page.route('**/api/runs/stopped/continuation',route=>{
    if(route.request().method()==='GET')return route.fulfill({json:review});
    tokens.push(route.request().postDataJSON().reviewToken);
    if(!committed){committed=true;creations++;}
    return route.fulfill({status:202,json:{runId:'continued'}});
  });
  await page.goto('/runs/stopped');
  await page.getByRole('button',{name:'Review continuation',exact:true}).click();
  await expect(page.getByRole('button',{name:'Continue from Research the next step'})).toBeEnabled();
  // A second client's confirmed request completes between this client's review
  // and confirmation. The server fixture returns its same committed run ID.
  await page.evaluate(async token=>{await fetch('/api/runs/stopped/continuation',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({reviewToken:token})});},review.reviewToken);
  await page.getByRole('button',{name:'Continue from Research the next step'}).click();
  await expect(page).toHaveURL(/\/runs\/continued$/);
  expect(tokens).toEqual(['review-a','review-a']);expect(creations).toBe(1);
});
