import { pipelineMeasures } from './check-inputs.mjs';
export { pipelineMeasures } from './check-inputs.mjs';
import { getCheck } from './check-catalog.mjs';
import {lstat} from 'node:fs/promises';
import path from 'node:path';
import {isDeepStrictEqual} from 'node:util';
import {readJSON} from './files.mjs';
import {checkedProgram} from './checked-program.mjs';
import {amount,withinTolerance} from './decimal.mjs';
import {checkQualityInputs,qualityKeyColumns} from './data-quality.mjs';

export const pipelineChecks=['data-pipeline-profile','data-pipeline-rules','data-pipeline-audit'];
const require=(ok,message)=>{if(!ok)throw new Error(message);};
const text=v=>typeof v==='string'&&!!v.trim();
const count=v=>Number.isSafeInteger(v)&&v>=0;
const list=(v,name)=>{require(Array.isArray(v),`${name} must be an array`);return v;};
const distinct=(rows,key,name)=>require(new Set(rows.map(r=>r[key])).size===rows.length,`${name} must be distinct`);
const same=(a,b)=>amount(a)===amount(b);
const identifier=v=>typeof v==='string'&&/^[a-zA-Z_][a-zA-Z0-9_]{0,63}$/.test(v);
const policy=r=>({value:r.tolerance,unit:r.unit==='percent_of_control'?'percent_of_a':r.unit});

export async function checkPipelineInputs(inputs){
 pipelineMeasures(inputs);qualityKeyColumns(inputs.keyColumns);
 // Finished-table keys may be derived or aggregated. Validate source access now,
 // and validate these keys against the approved model and actual output later.
 await checkQualityInputs({dataset:inputs.sources,keyColumns:inputs.keyColumns},{keysMustExist:false});
}
function owned(row,declared){
 require(row.definition===declared.definition&&row.unit===declared.unit&&same(row.tolerance,declared.tolerance),'Preserve each declared measure definition and its own tolerance');
}
async function artifact(folder,file,prefix){
 require(text(file)&&!path.isAbsolute(file)&&!file.includes('\\')&&file.split('/').every(p=>p&&p!=='.'&&p!=='..')&&file.startsWith(prefix),'Name a file within the expected output folder');
 const info=await lstat(path.join(folder,file));require(info.isFile()&&info.nlink===1&&info.size>0,`Missing, linked or empty artifact: ${file}`);
}
function profile(doc,inputs,measures){
 require(doc.declaredSources===inputs.sources,'Profile the exact declared sources');
 const sources=list(doc.sources,'Sources');require(sources.length>0&&sources.length<=32,'Measure every declared source');distinct(sources,'name','Sources');
 for(const s of sources){
  require(text(s.name)&&text(s.readPath)&&text(s.identity)&&s.verified===true&&count(s.rows)&&s.scanned==='full','Every source needs a verified full scan, identity, read path and observed row count');
  const cols=list(s.columns,'Source columns');require(cols.length>0,'Measure source columns');distinct(cols,'name','Source columns');
  for(const c of cols)require(text(c.name)&&text(c.type)&&count(c.nullCount)&&c.nullCount<=s.rows&&count(c.distinctCount)&&c.distinctCount<=s.rows,'Source column counts must fit observed rows');
  for(const k of list(s.candidateKeys,'Source candidate keys'))require(list(k.columns,'Source key columns').length>0&&k.columns.every(name=>cols.some(c=>c.name===name))&&count(k.duplicateRows)&&k.duplicateRows<=s.rows&&k.unique===(k.duplicateRows===0),'Measure source keys rather than assuming target keys exist in raw data');
  require(text(s.observedRange)&&text(s.observedLag),'Record measured time bounds/lag or the exact missing evidence');list(s.issues,'Source issues');
 }
 const controls=list(doc.controls,'Independent controls');require(controls.length===measures.length,'Independently measure every requested measure before planning');distinct(controls,'measure','Controls');
 for(const m of measures){const c=controls.find(c=>c.measure===m.measure);require(c&&text(c.query)&&text(c.derivationPath),'Every control needs an executed query and raw-source parse path');owned(c,m);amount(c.value);}
 list(doc.assumptions,'Assumptions');list(doc.openQuestions,'Open questions');return sources;
}
function plan(doc,p,inputs,measures){
 require(doc.targetStore===inputs.targetStore,'Keep the requested target engine and delivery convention');
 const models=list(doc.models,'Models');require(models.length>0&&models.length<=32,'Plan 1–32 typed models');distinct(models,'name','Models');
 const known=new Set();
 for(const m of models){
  require(identifier(m.name)&&text(m.grain)&&text(m.materialization)&&text(m.incrementalStrategy),'Every model needs an identity, grain and build strategy');
  const cols=list(m.columns,'Model columns');require(cols.length>0&&cols.every(c=>identifier(c.name)&&text(c.type)&&text(c.description)),'Plan typed, explained output columns');distinct(cols,'name','Model columns');
  require(list(m.keyColumns,'Model keys').length>0&&m.keyColumns.every(k=>cols.some(c=>c.name===k)),'Model keys must exist in its own output columns');
  const upstreams=list(m.upstreams,'Model upstreams');require(upstreams.length>0&&upstreams.every(u=>u.kind==='source'?p.sources.some(s=>s.name===u.name):u.kind==='model'&&known.has(u.name)),'Order models after their measured sources and dependencies');
  require(new Set(upstreams.map(u=>JSON.stringify([u.kind,u.name]))).size===upstreams.length,'Model upstreams must be distinct');
  list(m.tests,'Model tests');known.add(m.name);
 }
 const output=models.find(m=>m.name===doc.outputModel);
 require(output&&output.grain===inputs.grain&&isDeepStrictEqual(output.keyColumns,qualityKeyColumns(inputs.keyColumns)),'Name one approved output model with the declared grain and finished-table keys');
 // Explicit kinds keep source and model names in separate namespaces. Only
 // declared dependencies establish source coverage, not a layer naming scheme.
 const contributingSources=new Set(),visited=new Set();
 function visit(model){if(visited.has(model.name))return;visited.add(model.name);for(const u of model.upstreams){if(u.kind==='source')contributingSources.add(u.name);else visit(models.find(m=>m.name===u.name));}}
 visit(output);
 for(const s of p.sources)require(contributingSources.has(s.name),`Measured source must contribute to the approved output: ${s.name}`);
 const mappings=list(doc.measureMap,'Measure map');require(mappings.length===measures.length,'Map every declared measure once');distinct(mappings,'measure','Mapped measures');
 for(const m of measures){const r=mappings.find(r=>r.measure===m.measure),control=p.controls.find(c=>c.measure===m.measure);require(r&&r.model===output.name&&output.columns.some(c=>c.name===r.column)&&text(r.expression)&&r.controlQuery===control.query&&r.controlPath===control.derivationPath,'Each measure needs its actual output column, expression and original independent control');owned(r,m);list(r.exclusions,'Measure exclusions');}
 const f=doc.freshness;require(f?.maxLag===inputs.maxLag&&f.refreshDeadline===(inputs.refreshDeadline||'')&&f.lateArrival===(inputs.lateArrival||'')&&text(f.verification)&&text(f.lateArrivalStrategy),'Keep maximum lag, external deadline and late-arrival policy as separate declared facts');
 require(text(doc.safeUseNotes)&&text(doc.publicationPlan),'Plan confidential-field handling and the final publication boundary');list(doc.rejectedDesigns,'Rejected designs');return output;
}



export function pipelineAcceptance(kind,workspace,previous,inputs,{signal}={}){
 require(pipelineChecks.includes(kind),'Unknown data-pipeline check');
 const source=kind.endsWith('-profile')?{workspace,inputs}:previous.findLast(n=>n.acceptance==='data-pipeline-profile');require(source,'Measure the declared sources first');
 const rules=kind.endsWith('-rules')?{workspace}:previous.findLast(n=>n.acceptance==='data-pipeline-rules');if(kind.endsWith('-audit'))require(rules,'Propose and approve the pipeline plan first');
 const measures=pipelineMeasures(source.inputs),program=checkedProgram(workspace,'checks.sh',{signal});
 const relative=folder=>path.relative(workspace,folder).split(path.sep).join('/')||'.';
 const instructions=getCheck(kind).description.replaceAll('PROFILE/',relative(source.workspace)+'/').replaceAll('RULES/',rules?relative(rules.workspace)+'/':'../rules/');
 return {description:(kind.endsWith('-rules')?'Do not execute before approval.':program.description)+'\n'+instructions+'\nOriginal sources and previous steps are read-only. Use only explicitly allowed source connections and temporary tool setup; pin the actual tools used. Keep all deliveries local. Honor the declared safe-use and optional script budget; the application has a separate run deadline. Do not invent optional policies or recipients.',onEvent:kind.endsWith('-rules')?async()=>{}:program.onEvent,
 async verify(){try{
  if(!kind.endsWith('-rules'))await program.verify();
  const p=await readJSON(source.workspace,'source-profile.json',{signal});profile(p,source.inputs,measures);if(kind.endsWith('-profile'))return {passed:true,findings:[]};
  const d=await readJSON(rules.workspace,'pipeline-plan.json',{signal}),output=plan(d,p,source.inputs,measures);if(kind.endsWith('-rules'))return {passed:true,findings:[]};
  const r=await readJSON(workspace,'verification-report.json',{signal}),manifest=await readJSON(workspace,'manifest.json',{signal});
  require(manifest.targetStore===d.targetStore&&manifest.outputModel===d.outputModel&&list(manifest.models,'Manifest models').length===d.models.length,'Deliver every approved model and the named output for the requested engine');distinct(manifest.models,'name','Manifest models');
  for(const m of manifest.models){const {file,...definition}=m;require(file===`models/${m.name}.sql`&&isDeepStrictEqual(definition,d.models.find(p=>p.name===m.name)),'Manifest must retain exact approved model definitions');await artifact(workspace,file,'models/');}
  await artifact(workspace,'checks.sql','');require(text(manifest.toolchain?.runtime),'Record the execution toolchain');const packages=list(manifest.toolchain.packages,'Toolchain packages');distinct(packages,'name','Toolchain packages');require(packages.every(p=>text(p.name)&&text(p.version)),'Pin every installed package version');
  require(list(r.sources,'Executed sources').length===p.sources.length,'Execute every measured source');distinct(r.sources,'name','Executed sources');
  const accounting=list(r.rowAccounting,'Source accounting');require(accounting.length===p.sources.length,'Account for every source');distinct(accounting,'source','Source accounting');
  for(const s of p.sources){const current=r.sources.find(x=>x.name===s.name),a=accounting.find(x=>x.source===s.name);require(current?.identity===s.identity&&current.rows===s.rows,'Build from the same full source snapshot');require(a&&a.inputRows===s.rows&&count(a.retainedRows)&&a.contributedRows===a.retainedRows&&text(a.query),'Account for retained source contributions before aggregation');const excluded=list(a.excluded,'Excluded rows');distinct(excluded,'filter','Named exclusions');require(excluded.every(e=>text(e.filter)&&count(e.rows))&&a.retainedRows+excluded.reduce((n,e)=>n+e.rows,0)===a.inputRows,'Retained rows plus disjoint named exclusions must equal the source count');}
  const g=r.grainCheck;require(g?.model===output.name&&g.grain===source.inputs.grain&&isDeepStrictEqual(g.keyColumns,qualityKeyColumns(source.inputs.keyColumns))&&count(g.rows)&&g.duplicateRows===0&&g.nullKeyRows===0&&text(g.query),'Execute the declared finished-table grain and non-null key assertions');
  require(list(r.measures,'Measure results').length===measures.length,'Execute every declared measure');distinct(r.measures,'measure','Measure results');
  for(const m of measures){const v=r.measures.find(v=>v.measure===m.measure),c=p.controls.find(c=>c.measure===m.measure);require(v,'Missing measure result');owned(v,m);require(same(v.expected,c.value)&&same(v.reproducedControl,c.value)&&v.controlPath===c.derivationPath&&v.controlQuery===c.query&&text(v.modelPath)&&v.modelPath!==c.derivationPath,'Use the captured control and a separate raw-source parse path');require(amount(v.delta)===amount(v.observed)-amount(v.expected),'Report the exact observed-minus-control delta');require(v.pass===true&&withinTolerance(v.delta,v.expected,'0',policy(m)),'Every measure must reproduce its independent control within its own tolerance');}
  const negative=r.negativeCase;require(negative?.source==='model-fixture'&&negative.query===g.query&&negative.detected===true&&count(negative.baseline?.rows)&&negative.baseline.rows>0&&negative.baseline.duplicateRows===0&&negative.baseline.nullKeyRows===0&&negative.mutated?.rows===negative.baseline.rows+1&&negative.mutated.duplicateRows===1&&negative.mutated.nullKeyRows===0,'The same grain assertion must reject exactly one injected duplicate');await artifact(workspace,negative.file,'fixtures/');
  require(isDeepStrictEqual(r.publication?.tables,[output.name])&&text(r.publication.query),'Deliver only the approved output model, with the actual table-list query');await artifact(workspace,r.publication.file,'delivery/');
  const replay=r.replay;require(replay?.pass===true&&replay.rowsBefore===g.rows&&replay.rowsAfter===g.rows&&text(replay.query),'A repeated build must preserve row count');
  for(const key of ['measuresBefore','measuresAfter']){const values=list(replay[key],'Replay measures');require(values.length===measures.length,'Repeat every measure after rebuilding');distinct(values,'measure','Replay measures');for(const m of r.measures)require(same(values.find(v=>v.measure===m.measure)?.value,m.observed),'Repeated builds must preserve every measured total');}
  const f=r.freshness;require(f?.maxLag===source.inputs.maxLag&&f.refreshDeadline===(source.inputs.refreshDeadline||'')&&f.lateArrival===(source.inputs.lateArrival||'')&&f.pass===true&&text(f.observed)&&text(f.query),'Verify the declared freshness contract with observed evidence');require(text(f.lateCase?.observation),'Explain the late-arrival scenario or absent policy');if(source.inputs.lateArrival?.trim())require(f.lateCase.performed===true&&f.lateCase.pass===true&&text(f.lateCase.query),'Execute the declared late-arrival policy');else require(f.lateCase.performed===false,'Do not invent an undeclared late-arrival policy');
  const checks=list(r.checks,'Check families');require(checks.length===4&&['row-accounting','key-uniqueness','measure-reproduction','negative-duplicate-key'].every(id=>checks.some(c=>c.id===id&&c.pass===true&&text(c.expected)&&text(c.observed)&&text(c.query)))&&r.passed===4&&r.failed===0,'Report exactly four executed passing check families with evidence');
  require(r.timing?.withinBudget===true&&text(r.timing.budget)&&list(r.timing.stages,'Stage timings').length>0&&r.timing.stages.every(s=>text(s.stage)&&Number.isFinite(s.seconds)&&s.seconds>=0),'Report measured stage times and honor the declared budget');
  return {passed:true,findings:[]};
 }catch(error){return {passed:false,findings:[error.message]};}}};
}
