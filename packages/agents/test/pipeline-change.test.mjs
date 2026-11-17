import test from 'node:test';
import {mkdtemp,mkdir,writeFile,readFile,copyFile,rm} from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
const execute=promisify(execFile);
import assert from 'node:assert/strict';
import {pipelineChangeInputs,invariantPass,pipelineChangeAcceptance} from '../src/workflows/pipeline-change.mjs';
import {validateNodes} from '../src/workflows/nodes.mjs';
const owner={invariant:'US revenue',measure:'us_net',kind:'number',unit:'absolute',tolerance:'0.01'};
test('protected invariants preserve typed values and exact per-measure tolerances',()=>{
 const parse=rows=>pipelineChangeInputs({invariants:JSON.stringify(rows)});
 assert.equal(parse([owner]).length,1);
 assert.equal(invariantPass('1000000000000000000','1000000000000000000.01',owner),true);
 assert.equal(invariantPass('1000000000000000000','1000000000000000000.010000000000000000000001',owner),false);
 assert.equal(invariantPass('-100','-99',{...owner,unit:'percent_of_baseline',tolerance:'1'}),true);
 assert.equal(invariantPass('0','0.01',{...owner,unit:'percent_of_baseline',tolerance:'50'}),false);
 assert.equal(invariantPass(false,false,{...owner,kind:'boolean',unit:'exact',tolerance:'0'}),true);
 assert.equal(invariantPass(false,true,{...owner,kind:'boolean',unit:'exact',tolerance:'0'}),false);
 assert.equal(invariantPass('USD','EUR',{...owner,kind:'text',unit:'exact',tolerance:'0'}),false);
 for(const rows of [[],[owner,owner],[{...owner,tolerance:''}],[{...owner,kind:'text'}],[{...owner,unit:'exact',tolerance:'1'}],[{...owner,unit:'percent_of_other'}]])assert.throws(()=>parse(rows));
 assert.equal(parse([{...owner,kind:'boolean',unit:'exact',tolerance:''}])[0].tolerance,'0');
});
test('pipeline change requires a measured baseline and approval of the current plan before implementation',()=>{
 const p={id:'profile',title:'Baseline',instruction:'Measure it',kind:'task',acceptance:'pipeline-change-profile',inputs:{pipelineLocation:'Pipeline source reference',runCommand:'python3 pipeline.py',changeRequest:'Convert refunds',invariants:JSON.stringify([owner]),grain:'One row per id',keyColumns:'id',safeUse:'No personal information'}},r={id:'rules',title:'Plan',instruction:'Plan change',kind:'task',acceptance:'pipeline-change-rules'},a={id:'approve',title:'Approve',instruction:'Decide',kind:'approval'},x={id:'audit',title:'Implement',instruction:'Prove change',kind:'task',acceptance:'pipeline-change-audit'};
 assert.doesNotThrow(()=>validateNodes('Change',[p,r,a,x]));
 for(const nodes of [[r],[p,x],[p,a,r,x],[p,r,a,{...p,id:'new-baseline'},x]])assert.throws(()=>validateNodes('Change',nodes));
 assert.throws(()=>validateNodes('Change',[{...p,inputs:{...p.inputs,runCommand:''}}]));
});


test('briefly described built-in nodes receive complete contracts with their actual step paths',()=>{
 const inputs={invariants:JSON.stringify([owner])};
 const profile={workspace:'/tmp/work/capture-current',acceptance:'pipeline-change-profile',inputs};
 const rules={workspace:'/tmp/work/propose-change',acceptance:'pipeline-change-rules'};
 const audit=pipelineChangeAcceptance('pipeline-change-audit','/tmp/work/implement',[profile,rules]);
 assert.match(audit.description,/Write regression-report.json/);
 assert.ok(audit.description.includes('../capture-current/baseline-report.json'));
 assert.ok(audit.description.includes('../propose-change/change-plan.json'));
 assert.ok(!audit.description.includes('../profile/')&&!audit.description.includes('../rules/'));
 assert.match(audit.description,/primary declared invariants/);
 const baseline=pipelineChangeAcceptance('pipeline-change-profile',profile.workspace,[],inputs);
 assert.match(baseline.description,/Perform baseline capture ONLY/);
 assert.match(baseline.description,/baseline-report.json with this exact shape/);
});

async function setupChange(t,mode='pass'){
 const root=await mkdtemp(path.join(os.tmpdir(),'pipeline-change-contract-'));t.after(()=>rm(root,{recursive:true,force:true}));
 const source=path.join(root,'orders.csv'),pipeline=path.join(root,'pipeline.py');
 await writeFile(source,'id,region,gross,refund,fx\n1,US,100,10,1\n2,EU,100,10,2\n3,US,50,5,1\n');
 await writeFile(pipeline,`import csv
from decimal import Decimal as D
rows=list(csv.DictReader(open('orders.csv')))
with open('result.csv','w',newline='') as f:
 w=csv.DictWriter(f,fieldnames=['id','region','net']);w.writeheader()
 for r in rows:w.writerow(dict(id=r['id'],region=r['region'],net=str(D(r['gross'])-D(r['refund']))))
`);
 const invariants=[{invariant:'Row count',measure:'rows',kind:'number',unit:'exact',tolerance:'0'},owner];
 if(mode.startsWith('coupled'))invariants.push({invariant:'Twice US revenue',measure:'2 * us_net',kind:'number',unit:'absolute',tolerance:'0.02'});
 const inputs={pipelineLocation:pipeline+'; input '+source,runCommand:'python3 pipeline.py',changeRequest:'Convert refunds',invariants:JSON.stringify(invariants),grain:'one row per order id',keyColumns:'id',safeUse:'Synthetic records'};
 const profile={workspace:path.join(root,'profile'),acceptance:'pipeline-change-profile',inputs},rules={workspace:path.join(root,'rules'),acceptance:'pipeline-change-rules'};
 const env={...process.env,SDK_FIXTURE_MODE:mode,SDK_FIXTURE_BASELINE:source};
 async function run(stage,mutate){
  const folder=path.join(root,stage);await mkdir(folder,{recursive:true});
  const check=pipelineChangeAcceptance('pipeline-change-'+stage,folder,stage==='profile'?[]:stage==='rules'?[profile]:[profile,rules],stage==='profile'?inputs:undefined);
  await copyFile(new URL('./fixtures/pipeline-change-program.py',import.meta.url),path.join(folder,'measure.py'));
  await writeFile(path.join(folder,'checks.sh'),'set -eu\npython3 measure.py\n');
  const item={id:'checks',type:'command_execution',command:'sh checks.sh'};
  if(stage!=='rules')await check.onEvent({type:'item.started',item});
  await execute('sh',['checks.sh'],{cwd:folder,env});
  if(mutate){const file=path.join(folder,stage==='rules'?'change-plan.json':'regression-report.json'),doc=JSON.parse(await readFile(file,'utf8'));mutate(doc);await writeFile(file,JSON.stringify(doc));}
  if(stage!=='rules')await check.onEvent({type:'item.completed',item:{...item,status:'completed',exit_code:0}});
  return check.verify();
 }
 assert.deepEqual(await run('profile'),{passed:true,findings:[]});
 return {run,root};
}

for(const mode of ['pass','coupled-pass'])test(`negative fixtures match approved ${mode==='pass'?'single':'coupled'} failure set`,async t=>{
 const ctx=await setupChange(t,mode);assert.deepEqual(await ctx.run('rules'),{passed:true,findings:[]});
 const plan=JSON.parse(await readFile(path.join(ctx.root,'rules/change-plan.json'),'utf8'));
 assert.deepEqual(plan.negativeCase.expectedFailures,mode==='pass'?['Row count']:['US revenue','Twice US revenue']);
 // Report order cannot affect set comparison.
 assert.deepEqual(await ctx.run('audit',r=>r.negativeCase.invariants.reverse()),{passed:true,findings:[]});
 const rows=await readFile(path.join(ctx.root,'audit/negative-output.csv'),'utf8');
 assert.ok(rows.includes(mode==='pass'?'1,US,90':'1,US,89'));
});

for(const [name,mutate] of [
 ['missing change',p=>delete p.negativeCase],
 ['blank change',p=>p.negativeCase.change=' '],
 ['missing expected set',p=>delete p.negativeCase.expectedFailures],
 ['empty expected set',p=>p.negativeCase.expectedFailures=[]],
 ['duplicate identities',p=>p.negativeCase.expectedFailures=['Row count','Row count']],
 ['unknown identity',p=>p.negativeCase.expectedFailures=['Unknown']],
 ['nontext identity',p=>p.negativeCase.expectedFailures=[1]],
])test(`negative plan rejects ${name} before approval`,async t=>{
 const ctx=await setupChange(t);const result=await ctx.run('rules',mutate);
 assert.equal(result.passed,false);assert.match(result.findings[0],/negative|Expected/);
});

for(const [mode,pattern] of [
 ['negative-extra-failure',/exactly the approved set/],
 ['negative-retarget',/exactly the approved set/],
 ['negative-passes',/exactly the approved set/],
 ['coupled-missing-failure',/exactly the approved set/],
 ['coupled-report-retarget',/exactly the approved set/],
 ['negative-forged-verdict',/verdict must follow/],
 ['wrong-negative-target',/approved copied-data negative change/],
])test(`negative report rejects ${mode} after successful program execution`,async t=>{
 const ctx=await setupChange(t,mode);assert.deepEqual(await ctx.run('rules'),{passed:true,findings:[]});
 const result=await ctx.run('audit');assert.equal(result.passed,false);assert.match(result.findings[0],pattern);
});

for(const [name,mutate,pattern] of [
 ['missing invariant',r=>r.negativeCase.invariants.pop(),/every declared invariant/],
 ['duplicate invariant',r=>r.negativeCase.invariants.push(r.negativeCase.invariants[0]),/distinct/],
 ['changed tolerance',r=>r.negativeCase.invariants[0].tolerance='100',/exact owner tolerance/],
 ['changed expected value',r=>r.negativeCase.invariants[0].expected='2',/captured baseline/],
 ['forged delta',r=>r.negativeCase.invariants[0].delta='0',/exact observed/],
 ['untripped case',r=>r.negativeCase.tripped=false,/same regression program/],
])test(`negative measurements reject ${name}`,async t=>{
 const ctx=await setupChange(t);assert.deepEqual(await ctx.run('rules'),{passed:true,findings:[]});
 const result=await ctx.run('audit',mutate);assert.equal(result.passed,false);assert.match(result.findings[0],pattern);
});
