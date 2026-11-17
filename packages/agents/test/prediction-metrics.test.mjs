import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, realpath, writeFile, readFile, rm, lstat, symlink } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import os from 'node:os';
import path from 'node:path';
import { runChecked } from '@hyperplane/agents';
import { ScorerInfrastructureError } from '@hyperplane/agents/ml-scoring';
import { createAcceptance, preflightCheck, getCheck } from '@hyperplane/agents/checks';

const holdout='ab'.repeat(32),dev='cd'.repeat(32);
const rows='y_true,y_score\n1,0.9\n0,0.8\n1,0.4\n0,0.1\n';
const declaration='1. metric: AUROC | threshold: >= 0.72 | operatingPoint: the review list is read top-down\n2. metric: recall@200 | threshold: >= 0.3';
const data=`dev (dev): sha256 ${dev}; final (holdout): sha256 ${holdout}.`;
const report=()=>({primaryMetric:{name:'AUROC',value:.75,threshold:.72,comparator:'>=',pass:true},evalset:{id:'final',sha256:holdout,rows:4},
  leakageChecks:[{id:'temporal-split',pass:true},{id:'seeded-leaky-feature',pass:true,expectedTrip:true,tripped:true,evidence:'Separate post-label feature fixture tripped the same checker.'}]});
async function fixture(t,{metric=declaration,dataDescription=data,csv=rows,change,command='sh checks.sh',observe=true,exitCode=0}={}) {
  const workspace=await realpath(await mkdtemp(path.join(os.tmpdir(),'sdk-prediction-')));
  t.after(()=>rm(workspace,{recursive:true,force:true}));
  const node={id:'evaluate',title:'Evaluate',kind:'task',instruction:'Evaluate the requested predictions',acceptance:'prediction-metrics',inputs:{successMetric:metric,dataDescription}};
  const acceptance=createAcceptance(node,workspace);
  await writeFile(path.join(workspace,'checks.sh'),'# Fixture-only execution events; not a model or sandbox proof.\necho evaluated\n');
  const item={id:'execute',type:'command_execution',command,status:'completed',exit_code:exitCode};
  if(observe) await acceptance.onEvent({type:'item.started',item});
  if(csv!==null) await writeFile(path.join(workspace,'predictions.csv'),csv);
  const evaluation=report();await change?.(evaluation,workspace);
  await writeFile(path.join(workspace,'eval-report.json'),JSON.stringify(evaluation));
  if(observe) await acceptance.onEvent({type:'item.completed',item});
  return {acceptance,workspace,node};
}

test('optional prediction check recomputes row one and publishes a host-owned report with explicit limits',async t=>{
  const {acceptance,workspace,node}=await fixture(t);
  await preflightCheck(node);
  assert.match(getCheck('prediction-metrics').description,/does not choose an algorithm/);
  assert.deepEqual(await acceptance.verify(),{passed:true,findings:[]});
  await assert.rejects(lstat(path.join(workspace,'score-report.json')),{code:'ENOENT'});
  await acceptance.seal();
  const scored=JSON.parse(await readFile(path.join(workspace,'score-report.json'),'utf8'));
  assert.equal(scored.value,.75);assert.equal(scored.n,4);assert.equal(scored.positives,2);
  assert.equal(scored.predictionsSha256,createHash('sha256').update(rows).digest('hex'));
  assert.equal(scored.binding.declaredHoldoutMatched,true);
  assert.deepEqual(scored.binding.declaredHoldoutHashes,[holdout]);
  assert.equal(scored.binding.comparator,'>=');assert.equal(scored.scorer,'platform');
  assert.equal(scored.limitations.holdoutCustody,'not_verified');
  assert.equal(scored.limitations.modelProvenance,'not_verified');
  assert.equal(scored.limitations.leakageControlExecution,'reported_by_harness');
});

test('checks all four metric families from actual prediction bytes, including aliases and operating points',async t=>{
  for(const [metric,value,operatingPoint] of [['ROC-AUC',.75,'rank the list'],['average precision',5/6,'rank the list'],['ACC',.75,'threshold 0.3'],['F1-score',.8,'threshold 0.3']]) {
    const f=await fixture(t,{metric:JSON.stringify([{metric,threshold:'>= 0.7',operatingPoint}]),change:r=>Object.assign(r.primaryMetric,{name:metric,value,threshold:.7})});
    assert.equal((await f.acceptance.verify()).passed,true,metric);
  }
  const f=await fixture(t,{metric:JSON.stringify([{metric:'accuracy',threshold:'>= 1',operatingPoint:'threshold .5'}]),csv:'y_true,y_score\n0,.1\n0,.2\n',change:r=>{Object.assign(r.primaryMetric,{name:'accuracy',value:1,threshold:1});r.evalset.rows=2;}});
  assert.equal((await f.acceptance.verify()).passed,true);
});

test('binds exact threshold and comparator and uses independently scored value without boundary epsilon',async t=>{
  for(const [comparator,threshold,passed] of [['>',.75,false],['<',.75,false],['>=',.75,true],['<=',.75,true],['=',.75,true],['>=',.7500000001,false],['<=',.7499999999,false]]) {
    const f=await fixture(t,{metric:`metric: AUROC | threshold: ${comparator} ${threshold}`,change:r=>Object.assign(r.primaryMetric,{threshold,comparator})});
    assert.equal((await f.acceptance.verify()).passed,passed,`${comparator} ${threshold}`);
  }
  const precise=await fixture(t,{metric:'metric: AUROC | threshold: >= 0.75000000000000001',change:r=>{r.primaryMetric.threshold='0.75000000000000001';}});
  assert.equal((await precise.acceptance.verify()).passed,false);
  for(const [value,passed] of [[.750001,true],[.7500011,false]]) {
    const f=await fixture(t,{change:r=>{r.primaryMetric.value=value;}});
    assert.equal((await f.acceptance.verify()).passed,passed,'1e-6 report agreement');
  }
});

test('rejects report substitution, invented bars, absent evidence and contradictory control outcomes',async t=>{
  const mutations=[
    ['later metric',r=>Object.assign(r.primaryMetric,{name:'recall@200',threshold:.3}),/same ruler/],
    ['different family',r=>Object.assign(r.primaryMetric,{name:'PR-AUC',value:5/6}),/same ruler/],
    ['different threshold',r=>{r.primaryMetric.threshold=.7;},/own bar/],
    ['different direction',r=>{r.primaryMetric.comparator='<=';},/direction/],
    ['missing comparator',r=>{delete r.primaryMetric.comparator;},/direction/],
    ['reported number',r=>{r.primaryMetric.value=.78;},/not evidence/],
    ['missing number',r=>{delete r.primaryMetric.value;},/finite/],
    ['null number',r=>{r.primaryMetric.value=null;},/finite/],
    ...['value','threshold'].flatMap(field=>['NaN','Infinity','-Infinity'].map(value=>[`${field} ${value}`,r=>{r.primaryMetric[field]=value;},/finite/])),
    ['false pass',r=>{r.primaryMetric.pass=false;},/passing result/],
    ['other holdout',r=>{r.evalset.sha256='ef'.repeat(32);},/different holdout/],
    ['dev hash',r=>{r.evalset.sha256=dev;},/different holdout/],
    ['no holdout',r=>{delete r.evalset;},/which holdout/],
    ['invalid hash',r=>{r.evalset.sha256='claimed';},/SHA256/],
    ['conflicting hashes',r=>{r.evalset.contentHash=dev;},/Conflicting/],
    ['wrong population',r=>{r.evalset.rows=4000;},/population/],
    ['wrong prediction hash',r=>{r.predictionsSha256='ab'.repeat(32);},/prediction identity/],
    ['no controls',r=>{r.leakageChecks.pop();},/expectedTrip/],
    ['control never tripped',r=>{r.leakageChecks[1].tripped=false;},/expectedTrip/],
    ['string booleans',r=>{r.leakageChecks[1].expectedTrip='true';},/expectedTrip/],
    ['absent evidence',r=>{delete r.leakageChecks[1].evidence;},/evidence/],
    ['failed real check',r=>{r.leakageChecks[0].pass=false;},/Every reported/],
    ['one passed one failed control',r=>{r.leakageChecks.push({...r.leakageChecks[1],id:'second',tripped:false});},/expectedTrip/],
  ];
  for(const [name,change,message] of mutations) {
    const f=await fixture(t,{change});const verdict=await f.acceptance.verify();
    assert.equal(verdict.passed,false,name);assert.match(verdict.findings.join(' '),message,name);
    await assert.rejects(f.acceptance.seal(),/must pass/);
  }
});

test('absence of a declared fingerprint remains an explicit limitation rather than invented custody',async t=>{
  const f=await fixture(t,{dataDescription:'Evaluate the owner-supplied holdout; no content hash supplied.'});
  assert.equal((await f.acceptance.verify()).passed,true);await f.acceptance.seal();
  const scored=JSON.parse(await readFile(path.join(f.workspace,'score-report.json'),'utf8'));
  assert.equal(scored.binding.declaredHoldoutMatched,null);
  assert.equal(scored.limitations.holdoutCustody,'not_verified');
});

test('requires observed successful SDK execution and refuses missing, linked, forged or changed artifacts',async t=>{
  for(const options of [{observe:false},{exitCode:3},{command:'sh checks.sh; true'},{csv:null},{csv:'y_true,y_score\n1,NaN\n'}]) {
    const f=await fixture(t,options);assert.equal((await f.acceptance.verify()).passed,false);
  }
  const linked=await fixture(t);await rm(path.join(linked.workspace,'predictions.csv'));await symlink('/etc/hosts',path.join(linked.workspace,'predictions.csv'));
  assert.equal((await linked.acceptance.verify()).passed,false);
  const forged=await fixture(t,{change:async(_,workspace)=>writeFile(path.join(workspace,'score-report.json'),'{}')});
  assert.match((await forged.acceptance.verify()).findings.join(' '),/reserved for the host/);
  const changed=await fixture(t);assert.equal((await changed.acceptance.verify()).passed,true);
  await writeFile(path.join(changed.workspace,'predictions.csv'),rows+'1,.99\n');
  await assert.rejects(changed.acceptance.seal(),/Outputs changed/);
  await assert.rejects(lstat(path.join(changed.workspace,'score-report.json')),{code:'ENOENT'});
});


test('preflight refuses unsupported or incomplete declarations and cancelled acceptance cannot seal',async t=>{
  for(const successMetric of ['', 'metric: NDCG@10 | threshold: >= .4', 'metric: accuracy | threshold: >= .7', 'metric: F1 | threshold: >= .7', 'metric: AUROC | threshold: >= .7 or <= .9', 'metric: AUROC | threshold: .7', JSON.stringify([{metric:'AUROC',threshold:0.7}])]) {
    await assert.rejects(preflightCheck({kind:'task',acceptance:'prediction-metrics',inputs:{successMetric,dataDescription:data}}));
  }
  const f=await fixture(t),controller=new AbortController();
  const acceptance=createAcceptance(f.node,f.workspace,[],{signal:controller.signal});
  controller.abort(new Error('cancel evaluation'));
  await assert.rejects(acceptance.verify(),/cancel evaluation/);
  await assert.rejects(acceptance.seal(),/must pass/);
});


test('holdout selection distinguishes held-out hashes from development and full-source fingerprints',async t=>{
  const description=`development dataset sha256 ${dev}; held-out dataset sha256 ${holdout}`;
  const bad=await fixture(t,{dataDescription:description,change:r=>{r.evalset.sha256=dev;}});
  assert.match((await bad.acceptance.verify()).findings.join(' '),/different holdout/);
  const good=await fixture(t,{dataDescription:description,change:r=>{r.dataset={sha256:dev};}});
  assert.equal((await good.acceptance.verify()).passed,true);
});


test('missing Python and scorer protocol failures stop runtime without asking the model to repair infrastructure',async t=>{
  const prior=process.env.PATH;
  t.after(()=>{if(prior===undefined)delete process.env.PATH;else process.env.PATH=prior;});
  for(const mode of ['missing-python','invalid-report','crash','python-usage-error']) {
    const f=await fixture(t);
    const bin=await mkdtemp(path.join(os.tmpdir(),'sdk-scorer-infrastructure-'));
    t.after(()=>rm(bin,{recursive:true,force:true}));
    if(mode!=='missing-python') await writeFile(path.join(bin,'python3'), '#!/bin/sh\n'+
      (mode==='invalid-report'?"echo '{}'\n":mode==='crash'?"echo 'internal failure' >&2\nexit 1\n":"echo 'invalid interpreter flags' >&2\nexit 2\n"),{mode:0o700});
    process.env.PATH=bin;
    let turns=0;
    const thread={id:'author',runStreamed:async()=>{turns++;return {events:(async function*(){
      yield {type:'thread.started',thread_id:'author'};
      yield {type:'item.completed',item:{type:'agent_message',text:'Evaluation complete'}};
      yield {type:'turn.completed',usage:{input_tokens:1,output_tokens:1}};
    })()};}};
    await assert.rejects(runChecked(thread,'Evaluate',{maxCorrections:1,verify:()=>f.acceptance.verify()}),error=>{
      assert.ok(error instanceof ScorerInfrastructureError,`${mode}: ${error}`);
      if(mode==='missing-python') assert.equal(error.cause?.code,'ENOENT');
      return true;
    });
    assert.equal(turns,1,mode);
    await assert.rejects(f.acceptance.seal(),/must pass/);
    process.env.PATH=prior;
  }
  const invalid=await fixture(t,{csv:'y_true,y_score\n1,NaN\n'});
  const verdict=await invalid.acceptance.verify();
  assert.equal(verdict.passed,false);assert.match(verdict.findings.join(' '),/finite/);
});
