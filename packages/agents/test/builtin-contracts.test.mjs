import test from 'node:test';
import assert from 'node:assert/strict';
import {pipelineAcceptance} from '../src/workflows/data-pipeline.mjs';
import {qualityAcceptance} from '../src/workflows/data-quality.mjs';
import {shiftAcceptance} from '../src/workflows/dataset-shift.mjs';
import {reconciliationAcceptance} from '../src/workflows/reconciliation.mjs';

// Cross the same acceptance interface as a task created from a short description.
// Its required formats and paths must not depend on the Go template's instructions.
const families = [
  {name:'data-pipeline',acceptance:pipelineAcceptance,inputs:{sources:'warehouse.orders',keyColumns:'order_day',measures:JSON.stringify([{measure:'net',definition:'Sum amount excluding test rows'}])},
    files:['source-profile.json','pipeline-plan.json','verification-report.json'],fields:['candidateKeys','measureMap','rowAccounting']},
  {name:'data-quality',acceptance:qualityAcceptance,inputs:{dataset:'warehouse.orders',keyColumns:'id'},
    files:['data-profile.json','quality-rules.json','quality-scorecard.json'],fields:['expectationEvidence','evidenceRequired','negativeCase']},
  {name:'dataset-shift',acceptance:shiftAcceptance,inputs:{baselineData:'warehouse.before',currentData:'warehouse.after',keyColumns:'id',features:JSON.stringify([{feature:'category',type:'categorical',whyItMatters:'Report segments',statistic:'category-emergence',tolerance:2,unit:'points_of_share'}]),seededControl:''},
    files:['window-profiles.json','shift-rules.json','shift-scorecard.json'],fields:['nullCount','threshold','negativeControls']},
  {name:'reconciliation',acceptance:reconciliationAcceptance,inputs:{sourceA:'warehouse.a',sourceB:'warehouse.b',sharedGrain:'one row per id',keyColumns:'id',measures:JSON.stringify([{measure:'revenue',definitionA:'Sum amount',definitionB:'Sum amount'}]),tolerances:JSON.stringify([{measure:'revenue',unit:'absolute',value:'0.01'}])},
    files:['source-profiles.json','recon-rules.json','reconciliation-report.json'],fields:['duplicateKeys','segmentPlan','segmentUnexplainedAfter']},
];

for (const family of families) test(`${family.name} described tasks receive owned formats and current prerequisite paths`,()=>{
  const profile={workspace:'/tmp/work/measure-again',acceptance:family.name+'-profile',inputs:family.inputs};
  const rules={workspace:'/tmp/work/decide-limits',acceptance:family.name+'-rules'};
  const stale=[{...profile,workspace:'/tmp/work/stale-data'},{...rules,workspace:'/tmp/work/stale-rules'}];
  for (const [index,stage] of ['profile','rules','audit'].entries()) {
    const workspace=stage==='profile'?profile.workspace:stage==='rules'?rules.workspace:'/tmp/work/examine';
    const previous=stage==='profile'?[]:stage==='rules'?[...stale,profile]:[...stale,profile,rules];
    const check=family.acceptance(family.name+'-'+stage,workspace,previous,family.inputs);
    assert.ok(check.description.includes(family.files[index]),'Required output filename');
    assert.ok(check.description.includes(family.fields[index]),'Output field contract');
    assert.ok(!check.description.includes('../profile/')&&!check.description.includes('../rules/'),'No template step IDs');
    assert.ok(!check.description.includes('stale-data')&&!check.description.includes('stale-rules'),'Latest prerequisites govern');
    if(stage!=='profile')assert.ok(check.description.includes('../measure-again/'+family.files[0]));
    if(stage==='audit')assert.ok(check.description.includes('../decide-limits/'+family.files[1]));
    if(stage!=='rules')assert.match(check.description,/sh checks\.sh/);
  }
  assert.throws(()=>family.acceptance(family.name+'-rules','/tmp/work/rules',[],family.inputs));
  assert.throws(()=>family.acceptance(family.name+'-audit','/tmp/work/audit',[profile],family.inputs));
});
