import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,mkdir,writeFile,readFile,copyFile,rm} from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import {fileURLToPath} from 'node:url';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {pipelineMeasures,checkPipelineInputs,pipelineAcceptance} from '../src/workflows/data-pipeline.mjs';
import {checkQualityInputs} from '../src/workflows/data-quality.mjs';
import {runNodes,validateNodes} from '../src/workflows/nodes.mjs';
const execute=promisify(execFile);
const fixture=fileURLToPath(new URL('./fixtures/data-pipeline.py',import.meta.url));
const measure={measure:'net',definition:'Sum order amounts excluding test orders',unit:'exact',tolerance:'0'};
const usage={input_tokens:0,cached_input_tokens:0,output_tokens:0};
async function setup(t){
 const root=await mkdtemp(path.join(os.tmpdir(),'pipeline-contract-'));t.after(()=>rm(root,{recursive:true,force:true}));
 const sources=path.join(root,'orders.csv');
 await writeFile(sources,'id,ordered_at,amount,is_test,customer_email\n1,2026-09-01T12:00:00Z,10.25,0,a@example.test\n2,2026-09-01T15:00:00Z,20.50,0,b@example.test\n3,2026-09-02T12:00:00Z,5.25,0,c@example.test\n4,2026-09-02T12:00:00Z,999.00,1,d@example.test\n');
 const inputs={sources,targetStore:'SQLite database under delivery/',grain:'one row per UTC order date',keyColumns:'order_day',measures:JSON.stringify([measure]),maxLag:'0 days behind the static source',safeUse:'Never deliver customer email'};
 const workspace=path.join(root,'workflow');await mkdir(workspace);
 const profile={id:'inspect-sources',workspace:path.join(workspace,'inspect-sources'),acceptance:'data-pipeline-profile',inputs};
 const rules={id:'propose-models',workspace:path.join(workspace,'propose-models'),acceptance:'data-pipeline-rules'};
 return {root,workspace,inputs,profile,rules};
}
async function author(folder,phase,ctx,onEvent,mutate){
 await copyFile(fixture,path.join(folder,'fixture.py'));
 await writeFile(path.join(folder,'config.json'),JSON.stringify({inputs:ctx.inputs,rules:ctx.rules.workspace,layout:ctx.layout}));
 await writeFile(path.join(folder,'checks.sh'),`set -eu\npython3 fixture.py ${phase} config.json\n`);
 const item={id:'execute-checks',type:'command_execution',command:'sh checks.sh'};
 if(phase!=='rules')await onEvent({type:'item.started',item});
 await execute('sh',['checks.sh'],{cwd:folder});
 if(mutate)await mutate(folder);
 if(phase!=='rules')await onEvent({type:'item.completed',item:{...item,status:'completed',exit_code:0}});
}
async function prepare(ctx){
 for(const [node,phase,previous] of [[ctx.profile,'profile',[]],[ctx.rules,'rules',[ctx.profile]]]){
  await mkdir(node.workspace);const check=pipelineAcceptance(node.acceptance,node.workspace,previous,ctx.inputs);
  await author(node.workspace,phase,ctx,check.onEvent);assert.deepEqual(await check.verify(),{passed:true,findings:[]});
 }
}
const changeJSON=(filename,change)=>async folder=>{const file=path.join(folder,filename),doc=JSON.parse(await readFile(file,'utf8'));change(doc);await writeFile(file,JSON.stringify(doc));};

test('pipeline inputs admit derived target keys while preserving source access and owner tolerances',async t=>{
 const ctx=await setup(t);await checkPipelineInputs(ctx.inputs);
 await assert.rejects(checkQualityInputs({dataset:ctx.inputs.sources,keyColumns:'order_day'}),/Key columns/);
 await assert.rejects(checkPipelineInputs({...ctx.inputs,sources:path.join(ctx.root,'missing.csv')}),/source/i);
 assert.equal(pipelineMeasures({measures:JSON.stringify([{measure:'net',definition:'sum'}])})[0].tolerance,'0');
 for(const rows of [[],[measure,measure],[{...measure,definition:''}],[{...measure,tolerance:'1'}],[{...measure,unit:'absolute',tolerance:'-1'}],[{...measure,unit:'percent_of_baseline'}]])assert.throws(()=>pipelineMeasures({measures:JSON.stringify(rows)}));
 assert.equal(pipelineMeasures({measures:JSON.stringify([{...measure,unit:'absolute',tolerance:'0.000000000000000000000001'}])})[0].tolerance,'0.000000000000000000000001');
});

for(const layout of ['direct','multi-model'])test(`SDK nodes build an aggregated ${layout} output after caller approval using custom node names`,async t=>{
 const ctx=await setup(t),steps=[];ctx.layout=layout;
 const node=(id,acceptance)=>({id,title:id,instruction:'Perform this step with its built-in contract.',kind:'task',acceptance});
 const nodes=[{...node(ctx.profile.id,ctx.profile.acceptance),inputs:ctx.inputs},node(ctx.rules.id,ctx.rules.acceptance),{id:'approve',title:'Approve model plan',instruction:'Review typed models and measures',kind:'approval'},node('build-models','data-pipeline-audit')];
 validateNodes('Build the declared pipeline',nodes);
 for(const invalid of [[nodes[1]],[nodes[0],nodes[3]],[nodes[0],nodes[2],nodes[1],nodes[3]]])assert.throws(()=>validateNodes('Build',invalid));
 const result=await runNodes({}, {workspace:ctx.workspace,instruction:'Build the declared pipeline',nodes,
  requestApproval:async state=>{assert.equal(state.currentNode,'approve');assert.deepEqual(steps,['profile','rules']);steps.push('approval');return 'approved';},
  executeTask:async task=>{
   const phase=path.basename(task.workspace)===ctx.profile.id?'profile':path.basename(task.workspace)===ctx.rules.id?'rules':'audit';
   if(phase==='audit'){assert.equal(steps.at(-1),'approval');assert.match(task.acceptance.description,/\.\.\/inspect-sources\/source-profile.json/);assert.match(task.acceptance.description,/\.\.\/propose-models\/pipeline-plan.json/);}
   await author(task.workspace,phase,ctx,task.onEvent);await task.beforeCheck();
   const checked=await task.acceptance.verify();assert.deepEqual(checked,{passed:true,findings:[]});steps.push(phase);
   return {...checked,usage,attempts:1,text:'Fixture-author SQL execution completed',threadId:'fixture-'+phase};
  }});
 const approved=JSON.parse(await readFile(path.join(ctx.rules.workspace,'pipeline-plan.json'),'utf8'));
 assert.equal(approved.models.length,layout==='direct'?1:2);assert.equal(approved.outputModel,'daily_sales');
 assert.ok(approved.models.every(m=>!('layer' in m)));
 assert.equal(result.passed,true);assert.deepEqual(steps,['profile','rules','approval','audit']);
 const report=JSON.parse(await readFile(path.join(ctx.workspace,'build-models/verification-report.json'),'utf8'));
 assert.equal(report.rowAccounting[0].inputRows,4);assert.equal(report.rowAccounting[0].retainedRows,3);assert.equal(report.grainCheck.rows,2);assert.equal(report.measures[0].observed,'36');
 const inspected=await execute('python3',['-c','import sqlite3,sys,json; d=sqlite3.connect(sys.argv[1]); print(json.dumps(d.execute("select * from daily_sales order by order_day").fetchall()))',path.join(ctx.workspace,'build-models/delivery/pipeline.sqlite')]);
 assert.deepEqual(JSON.parse(inspected.stdout),[['2026-09-01',3075],['2026-09-02',525]]);
 assert.equal(await readFile(ctx.inputs.sources,'utf8'),'id,ordered_at,amount,is_test,customer_email\n1,2026-09-01T12:00:00Z,10.25,0,a@example.test\n2,2026-09-01T15:00:00Z,20.50,0,b@example.test\n3,2026-09-02T12:00:00Z,5.25,0,c@example.test\n4,2026-09-02T12:00:00Z,999.00,1,d@example.test\n');
});

test('pipeline audit rejects contradictions after observed successful execution',async t=>{
 const ctx=await setup(t);await prepare(ctx);
 const cases=[
  ['source identity',r=>r.sources[0].identity='changed',/same full source/],
  ['missing source',r=>r.sources=[],/every measured source/],
  ['raw rows confused with grouped rows',r=>r.rowAccounting[0].retainedRows=2,/contributions|source count/],
  ['double named exclusions',r=>r.rowAccounting[0].excluded=[{filter:'test',rows:1},{filter:'test',rows:0}],/distinct/],
  ['unaccounted row',r=>r.rowAccounting[0].excluded[0].rows=0,/source count/],
  ['duplicate target key',r=>r.grainCheck.duplicateRows=1,/grain/],
  ['null target key',r=>r.grainCheck.nullKeyRows=1,/grain/],
  ['raw key substituted for derived key',r=>r.grainCheck.keyColumns=['id'],/grain/],
  ['owner definition changed',r=>r.measures[0].definition='Include test orders',/declared measure/],
  ['owner tolerance relaxed',r=>r.measures[0].tolerance='1',/declared measure/],
  ['recomputed control changed',r=>r.measures[0].reproducedControl='35',/captured control/],
  ['same parse path',r=>r.measures[0].modelPath=r.measures[0].controlPath,/separate raw-source/],
  ['rounded tiny difference',r=>{r.measures[0].observed='36.000000000000000000000001';r.measures[0].delta='0';},/exact observed/],
  ['nonzero exact difference',r=>{r.measures[0].observed='36.01';r.measures[0].delta='0.01';},/own tolerance/],
  ['negative case does not fail',r=>r.negativeCase.mutated.duplicateRows=0,/injected duplicate/],
  ['different negative assertion',r=>r.negativeCase.query='different SQL',/injected duplicate/],
  ['leaked staging',r=>r.publication.tables.push('stg_orders'),/only the approved output model/],
  ['replay doubles rows',r=>r.replay.rowsAfter*=2,/preserve row count/],
  ['replay changes total',r=>r.replay.measuresAfter[0].value='37',/preserve every measured total/],
  ['invented deadline',r=>r.freshness.refreshDeadline='6am daily',/freshness contract/],
  ['invented late policy',r=>r.freshness.lateCase.performed=true,/undeclared late/],
  ['missing check family',r=>r.checks.pop(),/four executed/],
  ['budget overrun',r=>r.timing.withinBudget=false,/stage times/],
 ];
 for(const [name,change,message] of cases)await t.test(name,async()=>{
  const folder=path.join(ctx.workspace,'case-'+cases.findIndex(c=>c[0]===name));await mkdir(folder);
  const check=pipelineAcceptance('data-pipeline-audit',folder,[ctx.profile,ctx.rules]);
  await author(folder,'audit',ctx,check.onEvent,changeJSON('verification-report.json',change));
  const result=await check.verify();assert.equal(result.passed,false);assert.match(result.findings[0],message);
 });
 for(const [name,change,message] of [
  ['changed engine',d=>d.targetStore='DuckDB',/requested engine/],
  ['changed approved output',d=>d.outputModel='stg_orders',/approved model and the named output/],
  ['changed approved model',d=>d.models[1].grain='other grain',/exact approved/],
  ['unversioned installed dependency',d=>d.toolchain.packages=[{name:'pandas'}],/package version/],
 ])await t.test(name,async()=>{
  const folder=path.join(ctx.workspace,name.replaceAll(' ','-'));await mkdir(folder);
  const check=pipelineAcceptance('data-pipeline-audit',folder,[ctx.profile,ctx.rules]);
  await author(folder,'audit',ctx,check.onEvent,changeJSON('manifest.json',change));const result=await check.verify();assert.equal(result.passed,false);assert.match(result.findings[0],message);
 });
});

test('pipeline rules enforce explicit output, source coverage and dependency identity',async t=>{
 const ctx=await setup(t);await prepare(ctx);
 for(const [name,change,message] of [
  ['missing derived key',d=>d.models[1].columns=d.models[1].columns.filter(c=>c.name!=='order_day'),/keys must exist/],
  ['unknown upstream',d=>d.models[1].upstreams[0].name='unknown',/dependencies/],
  ['missing output',d=>delete d.outputModel,/Name one approved output/],
  ['unknown output',d=>d.outputModel='missing',/Name one approved output/],
  ['wrong output',d=>d.outputModel='stg_orders',/Name one approved output/],
  ['duplicate models',d=>d.models[1].name=d.models[0].name,/Models must be distinct/],
  ['duplicate dependencies',d=>d.models[1].upstreams.push(d.models[1].upstreams[0]),/upstreams must be distinct/],
  ['self dependency',d=>d.models[1].upstreams[0].name='daily_sales',/dependencies/],
  ['forward dependency',d=>d.models.reverse(),/dependencies/],
  ['changed control',d=>d.measureMap[0].controlQuery='fabricated',/independent control/],
 ])await t.test(name,async()=>{
  const folder=path.join(ctx.workspace,name.replaceAll(' ','-'));await mkdir(folder);
  const check=pipelineAcceptance('data-pipeline-rules',folder,[ctx.profile]);await author(folder,'rules',ctx,check.onEvent,changeJSON('pipeline-plan.json',change));
  const result=await check.verify();assert.equal(result.passed,false);assert.match(result.findings[0],message);
 });
});

test('pipeline source coverage follows explicit dependency kinds into the approved output',async t=>{
 const ctx=await setup(t);await prepare(ctx);
 // Source and model may share a name: kind, not naming convention, resolves it.
 const rules=pipelineAcceptance('data-pipeline-rules',ctx.rules.workspace,[ctx.profile]);
 await changeJSON('pipeline-plan.json',d=>{d.models[0].name='orders';d.models[1].upstreams[0].name='orders';})(ctx.rules.workspace);
 assert.deepEqual(await rules.verify(),{passed:true,findings:[]});
 // Merely measuring a source or attaching it to an unrelated model cannot
 // establish that it contributes to the approved output.
 await changeJSON('source-profile.json',p=>p.sources.push({...p.sources[0],name:'other-orders'}))(ctx.profile.workspace);
 let result=await rules.verify();assert.equal(result.passed,false);assert.match(result.findings[0],/Measured source must contribute.*other-orders/);
 await changeJSON('pipeline-plan.json',d=>d.models.push({...d.models[0],name:'unrelated',upstreams:[{kind:'source',name:'other-orders'}]}))(ctx.rules.workspace);
 result=await rules.verify();assert.equal(result.passed,false);assert.match(result.findings[0],/Measured source must contribute.*other-orders/);
 await changeJSON('pipeline-plan.json',d=>d.models[0].upstreams.push({kind:'source',name:'other-orders'}))(ctx.rules.workspace);
 assert.deepEqual(await rules.verify(),{passed:true,findings:[]});
});
