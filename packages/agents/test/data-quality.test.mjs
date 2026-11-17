import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,mkdir,writeFile,rm} from 'node:fs/promises';
import path from 'node:path';
import {tmpdir} from 'node:os';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {createAcceptance, getCheck} from '@hyperplane/agents/checks';
import {runNodes,validateNodes} from '../src/workflows/nodes.mjs';
import {checkQualityInputs} from '../src/workflows/data-quality.mjs';

const profile = {id:'profile',kind:'task',title:'Profile',instruction:'Measure the source',acceptance:'data-quality-profile',inputs:{dataset:'warehouse.table',keyColumns:'id'}};
const rules = {id:'rules',kind:'task',title:'Rules',instruction:'Propose rules',acceptance:'data-quality-rules'};
const approval = {id:'approval',kind:'approval',title:'Approval',instruction:'Decide on rules'};
const audit = {id:'audit',kind:'task',title:'Audit',instruction:'Execute approved rules',acceptance:'data-quality-audit'};

test('data-quality source gate checks declared keys before any model turn',async t=>{
 const root=await mkdtemp(path.join(tmpdir(),'sdk-quality-source-'));t.after(()=>rm(root,{recursive:true,force:true}));
 const csv=path.join(root,'source with spaces.csv');await writeFile(csv,'first,"middle,quoted",last\n1,2,3\n');
 for(const keys of ['first','last','none, first; last']) await checkQualityInputs({dataset:csv,keyColumns:keys});
 await checkQualityInputs({dataset:`CSV files "${csv}" (three columns)`,keyColumns:'last'});
 const tab=path.join(root,'input.tsv');await writeFile(tab,'id\tamount\n1\t2\n');await checkQualityInputs({dataset:tab,keyColumns:'amount'});
 await checkQualityInputs({dataset:path.join(root,'*.csv'),keyColumns:'last'});
 await checkQualityInputs({dataset:'warehouse.orders',keyColumns:'id'});
 for(const [dataset,keyColumns] of [[csv,'missing'],[csv,'none'],[csv,' , ; '],[path.join(root,'missing.csv'),'first'],['relative.csv','id']]){
  let calls=0;
  await assert.rejects(()=>runNodes({id:'',runStreamed:()=>{calls++;}}, {workspace:root,instruction:'Audit data',nodes:[{...profile,inputs:{dataset,keyColumns}}],executeTask:()=>{calls++;}}));
  assert.equal(calls,0);
 }
});

test('built-in data-quality checks cannot bypass prerequisites or mix in host commands',()=>{
 assert.doesNotThrow(()=>validateNodes('Audit data',[profile,rules,approval,audit]));
 for(const nodes of [[rules],[profile,audit],[profile,rules,audit],[profile,approval,rules,audit],[{...profile,check:['sh','anything']}],[{...profile,inputs:undefined}],[{...profile,acceptance:'unknown'}],[{...rules,inputs:profile.inputs}]]) assert.throws(()=>validateNodes('Audit data',nodes));
});

// These reports and scripts are test-authored contract fixtures. Successful
// observation checks transport and report validation, not source-data causality.
async function qualityFixture(t, severities = ['minor']) {
 const root=await mkdtemp(path.join(tmpdir(),'sdk-quality-contract-'));t.after(()=>rm(root,{recursive:true,force:true}));
 const previous=[{...profile,workspace:path.join(root,'profile')},{...rules,workspace:path.join(root,'rules')},approval];
 const workspace=path.join(root,'audit');
 await Promise.all([mkdir(previous[0].workspace),mkdir(previous[1].workspace),mkdir(workspace)]);
 const expectations=severities.map((_,i)=>`Expectation ${i+1}`);
 const proposed={rules:severities.map((severity,i)=>({id:`rule-${i+1}`,rule:expectations[i],dimension:'validity',assertion:'value < 0',scope:'Declared rows',threshold:{kind:'count',value:0,justification:'Owner permits no negative values'},severity,why:'Protect the declared report',expressible:true,expectedToFail:true,profileEvidence:'One negative value in four rows',evidenceRequired:'Numeric value',expectations:[expectations[i]]})),unexpressible:[]};
 await writeFile(path.join(previous[0].workspace,'data-profile.json'),JSON.stringify({expectationEvidence:expectations.map(expectation=>({expectation,status:'contradicted',observed:'1 of 4 rows'}))}));
 const writeRules=()=>writeFile(path.join(previous[1].workspace,'quality-rules.json'),JSON.stringify(proposed));
 await writeRules();
 const report={dataset:{rows:4,identity:'test-authored-four-row-report',scanned:'full'},notRun:[],rules:proposed.rules.map(r=>({id:r.id,severity:r.severity,checked:4,checkedUnit:'rows',violations:1,rate:0.25,pass:false,query:r.assertion,class:'violation'})),negativeCase:{rule:proposed.rules[0].id,seededRows:1,detectedRows:1,tripped:true},bySeverity:Object.fromEntries(['blocker','major','minor'].map(severity=>[severity,{total:severities.filter(s=>s===severity).length,failed:severities.filter(s=>s===severity).length}])),byClass:{violation:{total:severities.length,failed:severities.length},'evidence-gap':{total:0,failed:0}},passed:0,failed:severities.length};
 const planCheck=()=>createAcceptance(rules,previous[1].workspace,previous.slice(0,1));
 async function observe(change=()=>{}) {
  const output=structuredClone(report);change(output);
  const check=createAcceptance(audit,workspace,previous);
  await writeFile(path.join(workspace,'checks.sh'),`#!/bin/sh\ncat > quality-scorecard.json <<'QUALITY_REPORT'\n${JSON.stringify(output)}\nQUALITY_REPORT\n`);
  await check.onEvent({type:'item.started',item:{type:'command_execution',id:'quality-fixture',command:'sh checks.sh'}});
  await promisify(execFile)('sh',['checks.sh'],{cwd:workspace});
  await check.onEvent({type:'item.completed',item:{type:'command_execution',id:'quality-fixture',command:'sh checks.sh',exit_code:0,status:'completed'}});
  return check;
 }
 return {proposed,previous,workspace,planCheck,writeRules,observe};
}

test('quality acceptance permits one complete rule and warning-only audits without invented blockers',async t=>{
 for(const severities of [['blocker'],['minor'],['major','minor'],['blocker','major','minor']]) await t.test(severities.join('-'),async t=>{
  const f=await qualityFixture(t,severities);
  assert.deepEqual(await f.planCheck().verify(),{passed:true,findings:[]});
  const check=await f.observe();
  assert.deepEqual(await check.verify(),{passed:true,findings:[]});
 });
 assert.match(getCheck('data-quality-rules').description,/one complete rule is sufficient/i);
 assert.match(getCheck('data-quality-audit').description,/not independent causal proof/);
 assert.doesNotMatch(getCheck('data-quality-audit').description,/breaks one blocker rule/);
});

test('quality simplification preserves expectation coverage, nonempty unique rules and approved identity',async t=>{
 for(const [name,mutate,pattern] of [
  ['no rules',p=>{p.rules=[];},/at least one/],
  ['duplicate rules',p=>p.rules.push(structuredClone(p.rules[0])),/unique/],
  ['dropped expectation',p=>{p.rules[0].expectations=[];},/Expectation dropped/],
 ]) await t.test(name,async t=>{
  const f=await qualityFixture(t);mutate(f.proposed);await f.writeRules();
  const result=await f.planCheck().verify();assert.equal(result.passed,false);assert.match(result.findings[0],pattern);
 });
});

test('quality audit still requires observed execution, valid negative evidence and exact count verdicts',async t=>{
 for(const [name,mutate,pattern] of [
  ['missing negative case',r=>{delete r.negativeCase;},/seeded violation/],
  ['unapproved negative rule',r=>{r.negativeCase.rule='unknown';},/approved rule/],
  ['negative did not trip',r=>{r.negativeCase.tripped=false;},/seeded violation/],
  ['empty seeded population',r=>{r.negativeCase.seededRows=0;},/seeded violation/],
  ['missed seeded row',r=>{r.negativeCase.detectedRows=0;},/seeded violation/],
  ['wrong rate',r=>{r.rules[0].rate=0;},/rate disagrees/],
  ['invented passing verdict',r=>{r.rules[0].pass=true;},/approved threshold/],
  ['skipped rule',r=>{r.rules=[];},/exactly once/],
  ['changed severity',r=>{r.rules[0].severity='blocker';},/approved severity/],
 ]) await t.test(name,async t=>{
  const f=await qualityFixture(t),check=await f.observe(mutate),result=await check.verify();
  assert.equal(result.passed,false);assert.match(result.findings[0],pattern);
 });
 const f=await qualityFixture(t);
 const unobserved=createAcceptance(audit,f.workspace,f.previous);
 assert.match((await unobserved.verify()).findings[0],/Run exactly sh checks.sh/);
 const observed=await f.observe();
 await writeFile(path.join(f.workspace,'extra.txt'),'Edited after execution');
 assert.match((await observed.verify()).findings[0],/Outputs changed/);
});
