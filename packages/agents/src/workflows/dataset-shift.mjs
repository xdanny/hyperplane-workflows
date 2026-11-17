import { getCheck } from './check-catalog.mjs';
import {readJSON as readReport} from './files.mjs';
import {writeFile} from 'node:fs/promises';
import path from 'node:path';
import {checkedProgram} from './checked-program.mjs';
import {shiftInputs} from './shift-inputs.mjs';
import {shiftControls,controlValue,shifted} from './shift-controls.mjs';
export const shiftChecks=['dataset-shift-profile','dataset-shift-rules','dataset-shift-audit'];
const require=(ok,message)=>{if(!ok)throw new Error(message);};
const text=v=>typeof v==='string'&&!!v.trim();
const count=v=>Number.isSafeInteger(v)&&v>=0;
const finite=v=>typeof v==='number'&&Number.isFinite(v);
const array=(v,label)=>{require(Array.isArray(v),`${label} must be an array`);return v;};
const close=(a,b)=>finite(a)&&finite(b)&&Math.abs(a-b)<=1e-8*Math.max(1,Math.abs(b));

function verifyProfile(p,features){
 for(const side of ['baseline','current']){const w=p[side];require(w?.verified===true&&count(w.rows)&&w.rows>0&&text(w.identity)&&text(w.periodObserved),'Both windows need measured identities, positive counts and observed periods');require(['full','sample'].includes(w.scanned)&&(w.scanned!=='sample'||text(w.sampleNote)),'State each window scan scope');}
 require(p.baseline.scanned===p.current.scanned,'Use the same scan mode for both windows');
 const reported=array(p.features,'features');require(reported.length===features.length&&new Set(reported.map(f=>f.name)).size===features.length,'Profile every declared feature exactly once');
 for(const f of features){
  const x=reported.find(x=>x.name===f.feature);require(x&&x.type===f.type&&text(x.binningNote),`Profile missing feature or binning: ${f.feature}`);
  if(x.comparable===false){require(text(x.reason)&&array(p.schemaDifferences,'schema differences').some(d=>d.column===f.feature),'Uncomparable features need a reason and named schema difference');continue;}
  for(const side of ['baseline','current']){
   const v=x[side],bins=array(v?.bins,'feature bins'),rows=p[side].rows;
   require(count(v.nullCount)&&v.nullCount<=rows&&close(v.nullPct,100*v.nullCount/rows)&&count(v.distinctCount)&&v.distinctCount<=rows,'Invalid null or distinct counts');
   require(bins.length>0&&bins.every(b=>text(b.bin)&&count(b.count)&&finite(b.share))&&new Set(bins.map(b=>b.bin)).size===bins.length,'Feature distributions need distinct bins and measured counts');
   const total=bins.reduce((n,b)=>n+b.count,0);require(total>0&&total+v.nullCount===rows,'Bin and null counts must account for the window');
   require(bins.every(b=>close(b.share,b.count/total)),'Bin shares disagree with counts');
  }
  require(JSON.stringify(x.baseline.bins.map(b=>b.bin))===JSON.stringify(x.current.bins.map(b=>b.bin)),'Both windows must share bin labels/order; use a union vocabulary with zero counts');
 }
 for(const key of ['volumeByPeriod','schemaDifferences','comparabilityHazards','openQuestions'])array(p[key],key);
 for(const side of ['baseline','current'])require(p.volumeByPeriod.some(v=>v.window===side&&text(v.period)&&count(v.rows)),'Show period volumes for both windows');
}

function verifyRules(doc,profile,features){
 const rules=array(doc.rules,'rules'),missing=array(doc.unexpressible,'unexpressible');
 require(rules.length>0&&new Set(rules.map(r=>r.id)).size===rules.length,'Rules need distinct IDs');
 for(const r of rules){
  require(/^[a-z][a-z0-9-]{0,63}$/.test(r.id)&&['psi','kl','rate-delta','category-emergence','volume-drift'].includes(r.statistic)&&['blocker','major','minor'].includes(r.severity)&&r.expressible===true&&typeof r.expectedToTrip==='boolean','Invalid rule identity, statistic, severity or executability');
  for(const key of ['feature','rule','binning','why','profileEvidence'])require(text(r[key]),`Rule ${r.id} needs ${key}`);
  require(finite(r.tolerance?.value)&&r.tolerance.value>=0&&text(r.tolerance.unit)&&text(r.tolerance.source)&&['declared','proposed'].includes(r.toleranceOrigin),'Every tolerance needs its value, unit and origin');
  const f=features.find(f=>f.feature===r.feature);
  if(r.toleranceOrigin==='declared')require(f&&r.statistic===f.statistic&&r.tolerance.value===f.tolerance&&r.tolerance.unit===f.unit,'A declared tolerance must match that exact feature and selected statistic, including units');
 }
 for(const m of missing)require(features.some(f=>f.feature===m.feature)&&text(m.why),'Unexpressible features need a declared name and reason');
 for(const f of features){
  const measured=profile.features.find(x=>x.name===f.feature);
  if(measured.comparable===false)require(missing.some(m=>m.feature===f.feature)&&!rules.some(r=>r.feature===f.feature),'A schema-incompatible feature must be unexpressible, not scored as drift');
  const primary=rules.find(r=>r.feature===f.feature&&r.statistic===f.statistic);
  require(primary||missing.some(m=>m.feature===f.feature),`Declared feature dropped: ${f.feature}`);
  if(primary)require(primary.toleranceOrigin==='declared'&&primary.tolerance.value===f.tolerance&&primary.tolerance.unit===f.unit,'The primary feature rule must preserve its declared tolerance');
 }
 return rules;
}

// The built-in check owns its output contract for templates and described nodes alike.


export function shiftAcceptance(kind,workspace,previous,inputs,{signal}={}){
 const json=(folder,name)=>readReport(folder,name,{signal});
 require(shiftChecks.includes(kind),'Unknown dataset-shift check');
 const prior=k=>previous.findLast(n=>n.acceptance===k);
 const profileNode=kind==='dataset-shift-profile'?{workspace,inputs}:prior('dataset-shift-profile');
 require(profileNode,'Profile both windows before shift rules or execution');
 const declared=shiftInputs(profileNode.inputs),program=checkedProgram(workspace, 'checks.sh', {signal});
 const rulesNode=kind==='dataset-shift-rules'?{workspace}:prior('dataset-shift-rules');
 if(kind==='dataset-shift-audit')require(rulesNode,'Propose shift rules before audit');
 const relative=folder=>path.relative(workspace,folder).split(path.sep).join('/')||'.';
 const contract=getCheck(kind).description.replaceAll('../profile/',relative(profileNode.workspace)+'/').replaceAll('../rules/',rulesNode?relative(rulesNode.workspace)+'/':'../rules/');
 let controls;
 return {
  description:(kind==='dataset-shift-rules'?'Do not execute rules before approval.':program.description)+'\n'+contract+'\nThe SDK supplies these required output fields and resolved source paths for this built-in check. Perform only the current step; original sources are read-only and external access or tool setup requires explicit step permissions.',
  onEvent:kind==='dataset-shift-rules'?async()=>{}:program.onEvent,
  async verify(){try{
   if(kind!=='dataset-shift-rules')await program.verify();
   const p=await json(profileNode.workspace,'window-profiles.json');verifyProfile(p,declared.features);
   if(kind==='dataset-shift-profile'){controls=shiftControls(p,declared.features,declared.controls);return {passed:true,findings:[]};}
   const rulesNode=kind==='dataset-shift-rules'?{workspace}:prior('dataset-shift-rules');require(rulesNode,'Approve shift rules before audit');
   const rules=verifyRules(await json(rulesNode.workspace,'shift-rules.json'),p,declared.features);
   if(kind==='dataset-shift-audit'){
    const report=await json(workspace,'shift-scorecard.json'),results=array(report.rules,'scorecard rules');
    require(array(report.notRun,'notRun').length===0,'Every approved rule must execute');
    require(results.length===rules.length&&new Set(results.map(r=>r.id)).size===rules.length,'Every approved rule must appear exactly once');
    for(const side of ['baseline','current']){const w=report.windows?.[side];require(w&&w.identity===p[side].identity&&w.rows===p[side].rows&&w.scanned===p[side].scanned&&text(w.periodObserved)&&(w.scanned!=='sample'||text(w.sampleNote)),'Audit windows must match the measured profiles');}
    const totals=Object.fromEntries(['blocker','major','minor'].map(s=>[s,{shifted:0,total:0}]));
    for(const r of results){
     const rule=rules.find(x=>x.id===r.id);require(rule&&r.feature===rule.feature&&r.statistic===rule.statistic&&r.severity===rule.severity&&r.tolerance===rule.tolerance.value,'Rule identity or approved tolerance changed');
     require(finite(r.value)&&r.value>=0&&r.shifted===shifted(r.value,rule.tolerance.value)&&text(r.checkedUnit)&&text(r.query),'Shift verdict must follow its observed value and tolerance');
     for(const side of ['Baseline','Current']){require(count(r['checked'+side])&&r['checked'+side]>0&&count(r['excluded'+side]),'Each rule needs positive counts and explicit exclusions for both windows');if(r.checkedUnit==='rows')require(r['checked'+side]+r['excluded'+side]===p[side.toLowerCase()].rows,'Checked and excluded rows must account for each window');}
     require(array(r.topContributors,'contributors').length>0||!r.shifted,'A shifted rule needs contributing bins');
     totals[r.severity].total++;if(r.shifted)totals[r.severity].shifted++;
    }
    for(const [s,t] of Object.entries(totals))require(report.bySeverity?.[s]?.total===t.total&&report.bySeverity?.[s]?.shifted===t.shifted,'Severity totals disagree');
    require(report.shifted===results.filter(r=>r.shifted).length&&report.stable===results.filter(r=>!r.shifted).length,'Overall totals disagree');
    const spec=await json(profileNode.workspace,'control-spec.json'),counts=await json(profileNode.workspace,'control-counts.json'),observed=array(report.negativeControls,'negative controls');
    require(observed.length===spec.controls.length&&new Set(observed.map(c=>c.feature)).size===observed.length,'Every fixed control must be reported exactly once');
    for(const c of spec.controls){
     const result=observed.find(r=>r.feature===c.feature),f=counts.features.find(f=>f.name===c.feature),value=controlValue(c.statistic,c.unit,f.baseline.bins,f.current.bins);
     require(rules.some(r=>r.feature===c.feature&&r.statistic===c.statistic&&r.tolerance.value===c.tolerance&&r.tolerance.unit===c.unit),'The fixed control must test a matching approved rule');
     require(result&&result.source==='sdk-control'&&result.statistic===c.statistic&&result.multiple===c.multiple&&result.tolerance===c.tolerance&&result.unit===c.unit&&result.target===c.target&&result.expectTrip===c.expectTrip&&result.declaredBy===c.declaredBy,'Control metadata differs from the caller-owned specification');
     require(close(result.value,value)&&result.tripped===c.expectTrip&&result.tripped===shifted(result.value,c.tolerance),'Seeded control did not match its independently calculated movement and expected verdict');
    }
   }
   return {passed:true,findings:[]};
  }catch(error){return {passed:false,findings:[error.message]};}},
  async seal(){if(kind==='dataset-shift-profile'){
   require(controls,'Profile acceptance must pass before attaching fixed controls');
   // The SDK turn has ended. These host-written files join the profile inventory
   // and become immutable inputs for all later steps, including approval.
   await writeFile(path.join(workspace,'control-spec.json'),JSON.stringify(controls.spec,null,2),{flag:'wx',mode:0o600});
   await writeFile(path.join(workspace,'control-counts.json'),JSON.stringify(controls.counts,null,2),{flag:'wx',mode:0o600});
  }},
 };
}
