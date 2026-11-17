import { reconciliationInputs } from './check-inputs.mjs';
export { reconciliationInputs } from './check-inputs.mjs';
import { getCheck } from './check-catalog.mjs';
import {readJSON as readReport} from './files.mjs';
import path from 'node:path';
import {checkedProgram} from './checked-program.mjs';
import {checkQualityInputs,qualityKeyColumns} from './data-quality.mjs';

export const reconciliationChecks=['reconciliation-profile','reconciliation-rules','reconciliation-audit'];
const require=(ok,message)=>{if(!ok)throw new Error(message);};
const text=v=>typeof v==='string'&&!!v.trim();
const count=v=>Number.isSafeInteger(v)&&v>=0;
const list=(v,label)=>{require(Array.isArray(v),`${label} must be an array`);return v;};
const unique=(rows,key,label)=>{require(new Set(rows.map(r=>r[key])).size===rows.length,`${label} must be distinct`);};
import {amount,withinTolerance} from './decimal.mjs';
export {amount,withinTolerance} from './decimal.mjs';
const same=(a,b)=>amount(a)===amount(b);

export async function checkReconciliationInputs(inputs){
 const parsed=reconciliationInputs(inputs);
 for(const dataset of [inputs.sourceA,inputs.sourceB])await checkQualityInputs({dataset,keyColumns:inputs.keyColumns});
 return parsed;
}

function verifyProfile(p,inputs,declared){
 require(p.grain?.declared===inputs.sharedGrain&&JSON.stringify(p.grain.keyColumns)===JSON.stringify(qualityKeyColumns(inputs.keyColumns)),'Profile must preserve the declared shared grain and keys');
 require(typeof p.independentReadPaths==='boolean'&&text(p.readPathNote),'Describe the independence of the two source reads');
 for(const side of ['sourceA','sourceB']){const s=p[side];require(s?.verified===true&&text(s.identity)&&text(s.readPath)&&count(s.rowsStored)&&s.rowsStored>0&&count(s.rowsAtGrain)&&s.rowsAtGrain>0&&s.rowsAtGrain<=s.rowsStored&&text(s.periodObserved),'Measure both source identities, stored and grain row counts, and periods');require(['full','sample'].includes(s.scanned)&&(s.scanned!=='sample'||text(s.sampleNote)),'Declare each scan scope');require(s.grainUnique===true&&s.duplicateKeys===0&&same(s.duplicateKeyAmount,'0'),'The shared grain must be unique on both sides');}
 require(p.sourceA.scanned===p.sourceB.scanned,'Use the same scan scope on both sides');
 const measures=list(p.measures,'Profile measures');unique(measures,'name','Profile measures');require(measures.length===declared.measures.length,'Profile every declared measure exactly once');
 for(const m of declared.measures){const r=measures.find(r=>r.name===m.measure);require(r&&r.definitionA===m.definitionA&&r.definitionB===m.definitionB&&typeof r.commensurable==='boolean'&&text(r.precision),'Preserve both measure definitions and precision');
  if(!r.commensurable){require(text(r.note),'Explain an incomparable measure');continue;}
  require(amount(r.totalA)-amount(r.totalB)===amount(r.observedDelta),'Profile delta must equal A minus B');
  for(const side of ['A','B'])require(count(r['rows'+side])&&r['rows'+side]>0&&count(r['dropped'+side])&&r['rows'+side]+r['dropped'+side]===p['source'+side].rowsAtGrain,'Measure rows and exclusions must account for the shared grain');
 }
 const coverage=p.keyCoverage;require(coverage&&['onlyInA','onlyInB','inBoth'].every(k=>count(coverage[k]?.keys)),'Measure key coverage on both sides');
 require(coverage.onlyInA.keys+coverage.inBoth.keys===p.sourceA.rowsAtGrain&&coverage.onlyInB.keys+coverage.inBoth.keys===p.sourceB.rowsAtGrain,'Key coverage must account for both grain counts');
 for(const k of ['segments','observedFilters','comparabilityHazards','openQuestions'])list(p[k],k);
}

function verifyRules(doc,p,declared){
 const rules=list(doc.equations,'Equations'),missing=list(doc.unexpressible,'Unexpressible measures');unique(rules,'id','Equation IDs');unique(rules,'measure','Equations');unique(missing,'measure','Unexpressible measures');
 require(rules.length>0,'At least one measure must be reconcilable');
 for(const m of declared.measures){const rule=rules.find(r=>r.measure===m.measure),unexpressible=missing.find(r=>r.measure===m.measure),profile=p.measures.find(r=>r.name===m.measure);require(Boolean(rule)!==Boolean(unexpressible),'Every declared measure must be either executable or explicitly unexpressible');if(unexpressible){require(text(unexpressible.why),'Explain each unexpressible measure');continue;}require(profile.commensurable,'Do not reconcile incompatible definitions');}
 require(rules.length+missing.length===declared.measures.length,'Do not introduce undeclared measures');
 require(text(doc.segmentPlan?.dimension)&&doc.segmentPlan.mustSumToTotal===true&&list(doc.preconditions,'Preconditions').length>=2,'Plan a complete segment partition and grain preconditions');
 for(const r of rules){
  require(/^[a-z][a-z0-9-]{0,63}$/.test(r.id)&&r.expressible===true&&typeof r.expectedToBalance==='boolean'&&['blocker','major','minor'].includes(r.severity)&&text(r.statement)&&text(r.why)&&text(r.profileEvidence),'Equations need executable IDs, statements and consequences');
  const profile=p.measures.find(m=>m.name===r.measure),t=declared.tolerances.find(t=>t.measure===r.measure);require(t&&r.toleranceOrigin==='declared'&&r.tolerance?.unit===t.unit&&same(r.tolerance.value,t.value)&&text(r.tolerance.source),'Keep the exact owner tolerance, unit and attribution');
  const hasSegment=t.segmentValue!==undefined&&t.segmentValue!=='';
  require(hasSegment?(r.tolerance.segmentUnit===t.segmentUnit&&same(r.tolerance.segmentValue,t.segmentValue)):(r.tolerance.segmentValue===undefined&&r.tolerance.segmentUnit===undefined),'Preserve the declared per-segment tolerance and unit');
  for(const k of ['totalA','totalB','observedDelta'])require(same(r[k],profile[k]),'Rule evidence must match the measured profile');
  unique(list(r.terms,'Filter terms'),'id','Filter IDs');
  for(const term of r.terms){require(/^filter:[a-z][a-z0-9-]{0,63}$/.test(term.id)&&text(term.name)&&text(term.predicate)&&text(term.why)&&['A','B','both'].includes(term.side)&&['+','-'].includes(term.direction)&&['declared','profile'].includes(term.source),'Every explained term needs a named executable filter, side and direction');if(term.source==='declared')require(declared.differences.some(d=>d.name===term.name&&d.side===term.side&&text(d.filter)&&d.filter===term.predicate),'A declared filter must match its exact owner row');amount(term.expectedAmount);}
 }
 return rules;
}

function verifyEquation(r,rule){
 require(r.id===rule.id&&r.measure===rule.measure&&r.severity===rule.severity&&r.toleranceUnit===rule.tolerance.unit&&same(r.tolerance,rule.tolerance.value)&&text(r.query),'Equation identity, query and approved tolerance must be preserved');
 const a=amount(r.totalA),b=amount(r.totalB),terms=list(r.terms,'Executed terms');unique(terms,'id','Executed filter IDs');require(terms.length===rule.terms.length,'Execute every approved term exactly once');
 let explained=0n;
 for(const t of terms){const approved=rule.terms.find(x=>x.id===t.id);require(approved&&['name','predicate','side','source','direction'].every(k=>t[k]===approved[k])&&count(t.rowCount),'Executed filters must preserve their approved predicate and metadata');explained+=(t.direction==='+'?1n:-1n)*amount(t.amount);}
 require(a-b===amount(r.observedDelta)&&explained===amount(r.explained)&&a-b-explained===amount(r.unexplained),'Recalculate the identity A = B + signed filters + unexplained');
 require(r.balances===true&&same(r.balanceResidual,'0'),'Arithmetic balance must be exact');
 require(r.withinTolerance===withinTolerance(r.unexplained,r.totalA,r.totalB,rule.tolerance),'Agreement must follow the approved tolerance and its denominator');
}

// The built-in check owns its output contract for templates and described nodes alike.


export function reconciliationAcceptance(kind,workspace,previous,inputs,{signal}={}){
 const readJSON=(folder,name)=>readReport(folder,name,{signal});
 require(reconciliationChecks.includes(kind),'Unknown reconciliation acceptance');
 const profileNode=kind==='reconciliation-profile'?{workspace,inputs}:previous.findLast(n=>n.acceptance==='reconciliation-profile');require(profileNode,'Measure both sources before reconciliation');
 const declared=reconciliationInputs(profileNode.inputs),program=checkedProgram(workspace, 'checks.sh', {signal});
 const rulesNode=kind==='reconciliation-rules'?{workspace}:previous.findLast(n=>n.acceptance==='reconciliation-rules');
 if(kind==='reconciliation-audit')require(rulesNode,'Propose reconciliation rules first');
 const relative=folder=>path.relative(workspace,folder).split(path.sep).join('/')||'.';
 const contract=getCheck(kind).description.replaceAll('../profile/',relative(profileNode.workspace)+'/').replaceAll('../rules/',rulesNode?relative(rulesNode.workspace)+'/':'../rules/');
 return {description:(kind==='reconciliation-rules'?'Do not execute before approval.':program.description)+'\n'+contract+'\nThe SDK supplies these required output fields and resolved source paths for this built-in check. Perform only the current step; original sources are read-only and external access or tool setup requires explicit step permissions.',onEvent:kind==='reconciliation-rules'?async()=>{}:program.onEvent,
 async verify(){try{
  if(kind!=='reconciliation-rules')await program.verify();
  const profile=await readJSON(profileNode.workspace,'source-profiles.json');verifyProfile(profile,profileNode.inputs,declared);
  if(kind==='reconciliation-profile')return {passed:true,findings:[]};
  const rulesNode=kind==='reconciliation-rules'?{workspace}:previous.findLast(n=>n.acceptance==='reconciliation-rules');require(rulesNode,'Propose reconciliation rules first');
  const doc=await readJSON(rulesNode.workspace,'recon-rules.json'),rules=verifyRules(doc,profile,declared);
  if(kind==='reconciliation-rules')return {passed:true,findings:[]};
  const report=await readJSON(workspace,'reconciliation-report.json'),table=await readJSON(workspace,'delta-table.json'),results=list(report.measures,'Measure results');
  require(list(report.notRun,'notRun').length===0&&results.length===rules.length,'Execute every approved equation');unique(results,'id','Measure results');
  require(report.grain===profileNode.inputs.sharedGrain&&table.dimension===doc.segmentPlan.dimension,'Keep the approved grain and segment dimension');
  for(const side of ['A','B']){const s=report.sources?.[side.toLowerCase()],p=profile['source'+side];require(s&&s.identity===p.identity&&s.readPath===p.readPath&&s.rows===p.rowsStored&&s.rowsAtGrain===p.rowsAtGrain&&s.scanned===p.scanned&&s.grainUnique===true&&s.duplicateKeys===0&&text(s.periodObserved)&&(s.scanned!=='sample'||text(s.sampleNote)),'Results must refer to the same measured sources and grain');}
  const rows=list(table.rows,'Segment rows');require(rows.length>0&&rows.every(s=>rules.some(r=>r.measure===s.measure)),'Every segment must name an approved measure');
  for(const r of results){const rule=rules.find(x=>x.id===r.id);require(rule,'Unknown equation result');verifyEquation(r,rule);require(same(r.totalA,rule.totalA)&&same(r.totalB,rule.totalB),'Measured source totals changed after rule review');require(r.checkedUnit==='rows','Report checked counts as rows at the shared grain');
   for(const side of ['A','B']){require(count(r['checked'+side])&&r['checked'+side]>0&&count(r['excluded'+side]?.rows)&&r['checked'+side]+r['excluded'+side].rows===profile['source'+side].rowsAtGrain,'Account for checked and excluded grain rows');amount(r['excluded'+side].amount);}
   const segments=rows.filter(s=>s.measure===r.measure);unique(segments,'segment','Measure segments');require(segments.length>0,'Partition every measure by segment');
   const tolerance=rule.tolerance.segmentValue===undefined?rule.tolerance:{value:rule.tolerance.segmentValue,unit:rule.tolerance.segmentUnit};
   for(const s of segments){require(text(s.segment)&&count(s.checkedA)&&count(s.checkedB),'Segments need names and row counts');require(amount(s.totalA)-amount(s.totalB)===amount(s.delta)&&amount(s.delta)-amount(s.explained)===amount(s.unexplained),'Each segment must balance exactly');require(s.toleranceUnit===tolerance.unit&&same(s.tolerance,tolerance.value)&&s.withinTolerance===withinTolerance(s.unexplained,s.totalA,s.totalB,tolerance),'Segment verdicts must use the declared unit and local denominator');}
   for(const [segmentKey,totalKey] of [['totalA','totalA'],['totalB','totalB'],['delta','observedDelta'],['explained','explained'],['unexplained','unexplained']])require(segments.reduce((n,s)=>n+amount(s[segmentKey]),0n)===amount(r[totalKey]),'Segment amounts must sum to their measure, even when opposing gaps cancel');
   for(const side of ['A','B'])require(segments.reduce((n,s)=>n+s['checked'+side],0)===r['checked'+side],'Segment denominators must sum to checked rows');
   const unexplained=list(r.unexplainedBySegment,'Unexplained segments'),nonzero=segments.filter(s=>amount(s.unexplained)!==0n);unique(unexplained,'segment','Unexplained segments');require(unexplained.length===nonzero.length&&unexplained.every(s=>nonzero.some(x=>x.segment===s.segment&&same(x.unexplained,s.amount))),'Locate every nonzero segment remainder, including cancelling gaps');
  }
  require(table.segmentsSumToTotal===true&&same(table.segmentSumResidual,'0'),'Segment rollup must be exact');
  const controls=list(report.negativeControls,'Negative controls');unique(controls,'id','Control IDs');
  require(controls.length>=2&&['drop-keys','alter-segment'].every(op=>controls.some(c=>c.operation===op)),'Execute separate dropped-key and altered-segment controls');
  for(const c of controls){const original=results.find(r=>r.measure===c.measure),rule=rules.find(r=>r.measure===c.measure);require(original&&c.side==='A'&&c.source==='model-fixture'&&text(c.segment),'Controls must name the source copy, operation and segment');require(text(c.seededHow)&&text(c.query),'Control seededHow and query must be nonempty strings describing the source copy and perturbation');const delta=amount(c.seededDelta);require(delta!==0n,'A control must introduce a nonzero change');verifyEquation(c.result,rule);
   require(amount(c.result.totalA)-amount(original.totalA)===delta&&same(c.result.totalB,original.totalB)&&same(c.result.explained,original.explained),'The injected change must not alter B or be absorbed into explained terms');
   require(c.result.terms.every(t=>same(t.amount,original.terms.find(x=>x.id===t.id).amount)),'A control cannot hide offsetting changes to named terms');
   require(amount(c.result.unexplained)-amount(original.unexplained)===delta&&same(c.detectedDelta,c.seededDelta)&&c.detected===true,'Measure the injected residual change relative to the original discrepancy');
   const originalSegment=rows.find(s=>s.measure===c.measure&&s.segment===c.segment);require(originalSegment&&amount(c.segmentUnexplainedAfter)-amount(originalSegment.unexplained)===delta,'Locate the injected change in the declared segment');
   require(count(c.rowsBefore)&&count(c.rowsAfter)&&c.rowsBefore===original.checkedA&&c.result.checkedA===c.rowsAfter&&c.result.checkedB===original.checkedB&&(c.operation==='drop-keys'?c.rowsAfter<c.rowsBefore:c.operation==='alter-segment'&&c.rowsAfter===c.rowsBefore),'Control operations must change or preserve the measured row count as declared');
  }
  return {passed:true,findings:[]};
 }catch(error){return {passed:false,findings:[error.message]};}},};
}
