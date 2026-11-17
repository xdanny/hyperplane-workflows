import { pipelineChangeInputs } from './check-inputs.mjs';
export { pipelineChangeInputs } from './check-inputs.mjs';
import { getCheck } from './check-catalog.mjs';
import {readJSON as readReport} from './files.mjs';
import {lstat,readdir} from 'node:fs/promises';
import path from 'node:path';
import {isDeepStrictEqual} from 'node:util';
import {checkedProgram} from './checked-program.mjs';
import {amount,withinTolerance} from './decimal.mjs';
import {checkQualityInputs,qualityKeyColumns} from './data-quality.mjs';

export const pipelineChangeChecks=['pipeline-change-profile','pipeline-change-rules','pipeline-change-audit'];
const require=(ok,message)=>{if(!ok)throw new Error(message);};
const text=v=>typeof v==='string'&&!!v.trim();
const count=v=>Number.isSafeInteger(v)&&v>=0;
const list=(v,label)=>{require(Array.isArray(v),`${label} must be an array`);return v;};
const unique=(v,key,label)=>require(new Set(v.map(r=>r[key])).size===v.length,`${label} must be distinct`);
const same=(a,b,kind)=>kind==='number'?amount(a)===amount(b):a===b;
const value=(v,kind)=>{if(kind==='number')amount(v);else require(kind==='boolean'?typeof v==='boolean':typeof v==='string','Invariant value must match its declared kind');};
const tolerance=r=>({value:r.tolerance||'0',unit:r.unit==='percent_of_baseline'?'percent_of_a':r.unit});
export function invariantPass(expected,observed,rule){
 value(expected,rule.kind);value(observed,rule.kind);
 return rule.kind==='number'?withinTolerance(decimalDifference(amount(observed)-amount(expected)),expected,'0',tolerance(rule)):expected===observed;
}
// Format the fixed-scale difference without converting through a binary float.
const decimalDifference=n=>`${n<0n?'-':''}${(n<0n?-n:n)/10n**24n}.${((n<0n?-n:n)%10n**24n).toString().padStart(24,'0')}`;
export async function checkPipelineChangeInputs(inputs){
 pipelineChangeInputs(inputs);await checkQualityInputs({dataset:inputs.pipelineLocation,keyColumns:inputs.keyColumns});
}
async function artifact(folder,file,prefix=''){
 require(text(file)&&!path.isAbsolute(file)&&!file.includes('\\')&&file.split('/').every(p=>p&&p!=='.'&&p!=='..')&&file.startsWith(prefix),'Artifact must name a relative file in the expected output folder');
 const info=await lstat(path.join(folder,file));
 require(info.isFile()&&info.nlink===1&&info.size>0,`Missing, linked or empty artifact: ${file}`);
}
function grain(g,inputs,requireValid){
 require(g?.grain===inputs.grain&&JSON.stringify(g.keyColumns)===JSON.stringify(qualityKeyColumns(inputs.keyColumns))&&count(g.duplicateRows)&&count(g.nullKeyRows),'Grain checks must preserve the declared grain and candidate keys with measured counts');
 if(requireValid)require(g.duplicateRows===0&&g.nullKeyRows===0&&g.pass===true,'Changed output must have no duplicate or null keys');
}
function own(r,declared){
 require(r.measure===declared.measure&&r.kind===declared.kind&&r.unit===declared.unit&&amount(r.tolerance??0)===amount(declared.tolerance??0),'Preserve each invariant measure, kind and exact owner tolerance');
}
function independent(c,rows,declared){
 const r=rows.find(r=>r.invariant===c?.invariant),d=declared.find(r=>r.invariant===c?.invariant);
 require(r&&d&&text(c.path)&&text(c.query)&&c.path!==r.derivationPath,'Independently derive at least one protected value through a named separate parse path');
 require(invariantPass(r.observed??r.baseline,c.value,d),'Independent measurement disagrees with the protected value');
}
async function baseline(p,folder,inputs,declared){
 for(const entry of await readdir(folder,{withFileTypes:true})){
  const allowed=entry.isDirectory()?entry.name==='baseline':['baseline-report.json','source-snapshot.json'].includes(entry.name)||/\.(?:py|sh|sql|js|mjs|ts|go|md|txt)$/.test(entry.name);
  require(allowed,'Baseline capture cannot deliver implementation outputs: keep snapshots under baseline/, the baseline report and source-snapshot.json, and flat helper programs only');
 }
 require(p.pipeline?.location===inputs.pipelineLocation&&p.pipeline.command===inputs.runCommand&&p.pipeline.executed===true,'Execute the exact declared baseline pipeline command on a local copy');
 require(text(p.snapshot?.identity)&&text(p.snapshot.capturedAt),'Pin the original source snapshot identity and capture time');
 const models=list(p.pipeline.models,'Baseline models');require(models.length>0,'Capture baseline model outputs');unique(models,'name','Baseline models');
 for(const m of models){require(text(m.name)&&count(m.rowCount)&&m.rowCount>0,'Baseline models need observed row counts');await artifact(folder,m.file,'baseline/');}
 const rows=list(p.invariants,'Baseline invariants');unique(rows,'invariant','Baseline invariants');require(rows.length===declared.length,'Measure every declared invariant exactly once');
 for(const d of declared){const r=rows.find(r=>r.invariant===d.invariant);require(r?.measurable===true&&text(r.query)&&text(r.derivationPath),'Every invariant needs an executed query and derivation path');own(r,d);value(r.baseline,d.kind);}
 grain(p.grainCheck,inputs,false);independent(p.independentControl,rows,declared);
 list(p.consumers,'Consumers');list(p.issues,'Baseline issues');
}
function plan(p,b,declared){
 require(list(p.impact,'Impact').length>0&&p.impact.every(r=>text(r.object)&&['model','column','consumer'].includes(r.kind)&&['breaking','compatible','internal'].includes(r.classification)&&text(r.change)&&text(r.why)),'Name and classify the affected models, columns and consumers');
 require(list(p.migrationSteps,'Migration steps').length>0&&p.migrationSteps.every(r=>text(r.step)&&text(r.command)&&text(r.verifyAfter)&&typeof r.reversible==='boolean'),'Plan ordered executable changes and immediate checks');
 require(list(p.rollback?.steps,'Rollback steps').length>0&&p.rollback.steps.every(text)&&text(p.rollback.dataLoss)&&text(p.rollback.safeWindow),'Provide concrete rollback, loss and safe-window details');
 const rows=list(p.invariantImpact,'Invariant impact');unique(rows,'invariant','Planned invariants');require(rows.length===declared.length,'Plan every declared invariant exactly once');
 for(const d of declared){const r=rows.find(r=>r.invariant===d.invariant),original=b.invariants.find(r=>r.invariant===d.invariant);require(r&&text(r.why)&&['unchanged','within-tolerance'].includes(r.expected),'Explain why each protected invariant will hold');own(r,d);require(same(r.baseline,original.baseline,d.kind),'Plan must retain the measured baseline value');}
 require(text(p.newBehaviour?.statement)&&text(p.newBehaviour.assertion)&&text(p.newBehaviour.falseOnBaseline),'Define an executable new behavior and why the baseline lacks it');
 const negative=p.negativeCase;require(text(negative?.change),'Plan the copied-data negative change before approval');
 const expected=list(negative.expectedFailures,'Expected negative failures');require(expected.length>0&&new Set(expected).size===expected.length&&expected.every(id=>text(id)&&declared.some(d=>d.invariant===id)),'Expected negative failures must be a nonempty, distinct set of declared invariant names');
 list(p.consumerNotes,'Consumer notes');require(text(p.safeUseNotes),'Explain how the change preserves the safe-use boundary');list(p.rejectedAlternatives,'Rejected alternatives');
}
function results(rows,b,declared){
 unique(list(rows,'Invariant results'),'invariant','Invariant results');require(rows.length===declared.length,'Execute every declared invariant exactly once');
 for(const d of declared){const r=rows.find(r=>r.invariant===d.invariant),original=b.invariants.find(r=>r.invariant===d.invariant);require(r&&text(r.query)&&text(r.derivationPath),'Each result needs its execution query and derivation path');own(r,d);require(same(r.expected,original.baseline,d.kind),'Expected values must come from the captured baseline');value(r.observed,d.kind);
  require(d.kind==='number'?amount(r.delta)===amount(r.observed)-amount(r.expected):r.delta===null,'Report exact observed-minus-baseline deltas for numbers and null for other kinds');
  require(r.pass===invariantPass(r.expected,r.observed,d),'Invariant verdict must follow its exact owner tolerance');
 }
}
// Built-in output contracts also serve nodes created from a short description.

export function pipelineChangeAcceptance(kind,workspace,previous,inputs,{signal}={}){
 const readJSON=(folder,name)=>readReport(folder,name,{signal});
 require(pipelineChangeChecks.includes(kind),'Unknown pipeline-change acceptance');
 const profile=kind==='pipeline-change-profile'?{workspace,inputs}:previous.findLast(n=>n.acceptance==='pipeline-change-profile');require(profile,'Capture the pipeline baseline first');
 const declared=pipelineChangeInputs(profile.inputs),program=checkedProgram(workspace, 'checks.sh', {signal});
 const rulesFolder=previous.findLast(n=>n.acceptance==='pipeline-change-rules')?.workspace;
 const contract=getCheck(kind).description.replaceAll('../profile/',path.relative(workspace,profile.workspace).split(path.sep).join('/')+'/').replaceAll('../rules/',rulesFolder?path.relative(workspace,rulesFolder).split(path.sep).join('/')+'/':'../rules/');
 const policy='Perform only the current step. Original pipeline files and data are read-only; do not deploy, publish, send messages or mutate an external database. Source access and temporary tools require explicit SDK step permissions. Pin the tools used. Use plain decimal strings with up to 24 fractional digits and no exponent or rounding. Enforce any declared script duration with measured stage times and an overrun failure; do not meet it by sampling or dropping checks. The application has its own separate run deadline.\n';
 return {description:policy+(kind.endsWith('-rules')?'Do not implement before approval. ':program.description+' ')+contract+'\nThese application-owned output fields and resolved input paths govern this built-in check, including when a template uses an example step name.',onEvent:kind.endsWith('-rules')?async()=>{}:program.onEvent,
 async verify(){try{
  if(!kind.endsWith('-rules'))await program.verify();
  const b=await readJSON(profile.workspace,'baseline-report.json');await baseline(b,profile.workspace,profile.inputs,declared);
  if(kind.endsWith('-profile'))return {passed:true,findings:[]};
  const rules=kind.endsWith('-rules')?{workspace}:previous.findLast(n=>n.acceptance==='pipeline-change-rules');require(rules,'Plan the change first');const p=await readJSON(rules.workspace,'change-plan.json');plan(p,b,declared);
  if(kind.endsWith('-rules'))return {passed:true,findings:[]};
  const r=await readJSON(workspace,'regression-report.json'),manifest=await readJSON(workspace,'manifest.json');
  require(r.baselineRef==='../'+path.basename(profile.workspace)+'/baseline-report.json'&&r.snapshotIdentity===b.snapshot.identity,'Compare against the captured baseline and the same original data snapshot');
  results(r.invariants,b,declared);require(r.invariants.every(r=>r.pass),'Every protected invariant must pass');require(r.passed===declared.length&&r.failed===0,'Report passed/failed as counts of primary declared invariants only, excluding behavior, grain and negative-case checks');
  const c=r.changeAssertion;require(c?.statement===p.newBehaviour.statement&&c.assertion===p.newBehaviour.assertion&&c.passOnBaseline===false&&c.passOnChanged===true&&c.pass===true&&text(c.observed),'The approved change assertion must fail on captured baseline output and pass on changed output');
  grain(r.grainCheck,profile.inputs,true);independent(r.independentControl,r.invariants,declared);
  const negative=r.negativeCase;require(negative?.id==='broken-invariant-fixture'&&negative.whatWasBroken===p.negativeCase.change&&text(negative.query)&&negative.tripped===true,'Execute the approved copied-data negative change through the same regression program');
  results(negative.invariants,b,declared);
  const failed=negative.invariants.filter(r=>!invariantPass(r.expected,r.observed,declared.find(d=>d.invariant===r.invariant))).map(r=>r.invariant);
  require(failed.length===p.negativeCase.expectedFailures.length&&p.negativeCase.expectedFailures.every(id=>failed.includes(id)),'The copied-data negative case must fail exactly the approved set of protected invariants');
  const models=list(manifest.models,'Changed models');require(models.length>0,'Deliver actual model files');unique(models,'name','Changed models');require(models.some(m=>['added','modified'].includes(m.status)),'Deliver a real change, not an unchanged manifest');
  for(const m of models){require(text(m.name)&&['added','modified','unchanged'].includes(m.status)&&Array.isArray(m.changedColumns),'Describe each delivered model and its changed columns');await artifact(workspace,m.file,'models/');}
  require(isDeepStrictEqual(manifest.migrationSteps,p.migrationSteps)&&isDeepStrictEqual(manifest.rollback,p.rollback),'Manifest must preserve the approved migration and rollback');
  require(list(r.consumersChecked,'Consumers checked').every(c=>text(c.consumer)&&text(c.result))&&b.consumers.every(c=>r.consumersChecked.some(x=>x.consumer===c)),'Account for each discovered downstream consumer');
  return {passed:true,findings:[]};
 }catch(error){return {passed:false,findings:[error.message]};}}};
}
