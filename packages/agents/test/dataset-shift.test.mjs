import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,mkdir,writeFile,readFile,rm} from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import {shiftControls,controlValue} from '../src/workflows/shift-controls.mjs';
import {shiftInputs,checkShiftInputs} from '../src/workflows/shift-inputs.mjs';
import {validateNodes,runNodes} from '../src/workflows/nodes.mjs';
import {shiftAcceptance} from '../src/workflows/dataset-shift.mjs';

const feature={feature:'category',type:'categorical',whyItMatters:'Report segmentation',statistic:'category-emergence',tolerance:2,unit:'points_of_share'};
const profileFor=(name,base,current)=>({features:[{name,baseline:{bins:base.map((count,i)=>({bin:String(i),count}))},current:{bins:current.map((count,i)=>({bin:String(i),count}))}}]});

test('SDK controls retain small movements, valid weights, exact ownership and both sides of a tolerance',()=>{
 const p=profileFor('category',[7,3],[7,3]);
 for(const multiple of [0.5,1,1.25]){
  const c=shiftControls(p,[feature],[{feature:'category',multiple}]),bins=c.counts.features[0],spec=c.spec.controls[0];
  const total=bins.current.bins.reduce((s,b)=>s+b.count,0),added=bins.current.bins.find(b=>b.bin==='__seeded_control__').count;
  assert.ok(Math.abs(100*added/total-2*multiple)<1e-12);assert.ok(added>0);
  for(const b of bins.current.bins)assert.ok(Math.abs(b.share-b.count/total)<1e-12);
  assert.equal(spec.expectTrip,multiple>=1);assert.equal(spec.declaredBy,'owner');
 }
 assert.equal(shiftControls(p,[feature],[]).spec.declaredBy,'platform default (1.25x)');
 const numeric={...feature,feature:'number',type:'numeric',statistic:'psi',unit:'psi',tolerance:0.8};
 const n=shiftControls(profileFor('number',[50,950],[50,950]),[numeric],[{feature:'number',multiple:1.5}]);
 const bins=n.counts.features[0];assert.ok(bins.current.bins.every(b=>b.count>=0));
 const sum=bins.current.bins.reduce((s,b)=>s+b.count,0),p0=[0.05,0.95],q=bins.current.bins.map(b=>b.count/sum);
 const actual=q.reduce((s,v,i)=>s+(Math.max(v,1e-9)-p0[i])*Math.log(Math.max(v,1e-9)/p0[i]),0);
 assert.ok(Math.abs(actual-1.2)<1e-8);
 assert.throws(()=>shiftControls(profileFor('number',[50,950],[50,950]),[{...numeric,tolerance:1000}],[{feature:'number',multiple:1.25}]),/unreachable/);
 assert.throws(()=>shiftControls(p,[{...feature,tolerance:0}],[]),/positive/);
 assert.throws(()=>controlValue('psi','psi',[{bin:'a',count:1}],[{bin:'a',count:-1}]),/nonnegative/);
});

test('shift input admission preserves table values, refuses missing tolerance and checks both source headers',async t=>{
 const root=await mkdtemp(path.join(os.tmpdir(),'sdk-shift-inputs-'));t.after(()=>rm(root,{recursive:true,force:true}));
 const baseline=path.join(root,'before.csv'),current=path.join(root,'after.csv');await writeFile(baseline,'id,category\n1,a\n');await writeFile(current,'wrong,category\n1,a\n');
 const inputs={baselineData:baseline,currentData:current,keyColumns:'id',features:JSON.stringify([feature]),seededControl:''};
 assert.deepEqual(shiftInputs(inputs).features[0],feature);
 for(const raw of [JSON.stringify([{...feature,tolerance:''}]),JSON.stringify([feature,feature]),'1. feature: category | type: categorical | whyItMatters: reports | unit: points_of_share'])assert.throws(()=>shiftInputs({...inputs,features:raw}));
 assert.throws(()=>shiftInputs({...inputs,features:JSON.stringify([{...feature,unit:'psi'}])}),/compatible unit/);
 assert.throws(()=>shiftInputs({...inputs,features:JSON.stringify([{...feature,tolerance:0}])}),/positive tolerances/);
 assert.throws(()=>shiftInputs({...inputs,seededControl:JSON.stringify([{feature:'category',multiple:1},{feature:'category',multiple:2}])}),/distinct/);
 await assert.rejects(checkShiftInputs(inputs),/key column/i);
 const p={id:'profile',kind:'task',title:'Profile',instruction:'Measure both windows',acceptance:'dataset-shift-profile',inputs};
 const r={id:'rules',kind:'task',title:'Rules',instruction:'Propose',acceptance:'dataset-shift-rules'},a={id:'approval',kind:'approval',title:'Approve',instruction:'Decide'},x={id:'audit',kind:'task',title:'Audit',instruction:'Execute',acceptance:'dataset-shift-audit'};
 assert.doesNotThrow(()=>validateNodes('Compare windows',[p,r,a,x]));
 for(const nodes of [[r],[p,x],[p,r,x],[p,a,r,x],[p,r,a,{...p,id:'new-profile'},x]])assert.throws(()=>validateNodes('Compare windows',nodes));
 let calls=0;await assert.rejects(runNodes({id:''},{workspace:root,instruction:'Compare windows',nodes:[p],executeTask:()=>{calls++;}}));assert.equal(calls,0);
});

test('weighted controls calibrate KL, positive-event rates and volume without rounding or invalid weights',()=>{
 for(const [statistic,unit,tolerance] of [['kl','kl',0.2],['rate-delta','points_of_share',10],['volume-drift','percent_of_baseline',20],['volume-drift','count',2]]){
  for(const multiple of [.5,1,1.25]){
   const f={...feature,feature:'value',type:'numeric',statistic,unit,tolerance};
   const {counts,spec}=shiftControls(profileFor('value',[8,2],[1,9]),[f],[{feature:'value',multiple}]);
   const weights=counts.features[0].current.bins.map(b=>b.count),total=weights.reduce((a,b)=>a+b,0),q=weights.map(v=>v/total);
   assert.ok(weights.every(v=>v>=0));
   const measured=statistic==='kl'?q.reduce((s,v,i)=>s+(v===0?0:v*Math.log(v/[.8,.2][i])),0):statistic==='rate-delta'?Math.abs(q[1]-.2)*100:unit==='count'?Math.abs(total-10):Math.abs(total-10)*10;
   assert.ok(Math.abs(measured-tolerance*multiple)<1e-8,`${statistic} at ${multiple}`);
   assert.equal(measured>=tolerance,multiple>=1);
   if(statistic==='rate-delta')assert.equal(spec.controls[0].positiveBin,'1');
  }
 }
 const vanished=controlValue('category-emergence','points_of_share',[{bin:'old',count:8},{bin:'kept',count:2}],[{bin:'kept',count:9},{bin:'new',count:1}]);
 assert.equal(vanished,80,'Use the larger directional share, not only newly emerged categories');
});

test('shift acceptance binds every approved rule, emitted control weight and verdict',async t=>{
 const root=await mkdtemp(path.join(os.tmpdir(),'sdk-shift-check-'));t.after(()=>rm(root,{recursive:true,force:true}));
 for(const d of ['profile','rules','audit'])await mkdir(path.join(root,d));
 const bins=[{bin:'a',count:7,share:.7},{bin:'b',count:3,share:.3}],side={nullCount:0,nullPct:0,distinctCount:2,bins};
 const p={baseline:{identity:'before.csv',verified:true,rows:10,periodObserved:'before',scanned:'full'},current:{identity:'after.csv',verified:true,rows:10,periodObserved:'after',scanned:'full'},features:[{name:'category',type:'categorical',binningNote:'Shared vocabulary',baseline:side,current:side}],volumeByPeriod:[{window:'baseline',period:'before',rows:10},{window:'current',period:'after',rows:10}],schemaDifferences:[],comparabilityHazards:[],openQuestions:[]};
 const inputs={baselineData:'before.csv',currentData:'after.csv',keyColumns:'id',features:JSON.stringify([feature]),seededControl:''};
 await writeFile(path.join(root,'profile/window-profiles.json'),JSON.stringify(p));
 const controls=shiftControls(p,[feature],[]);await writeFile(path.join(root,'profile/control-spec.json'),JSON.stringify(controls.spec));await writeFile(path.join(root,'profile/control-counts.json'),JSON.stringify(controls.counts));
 const rules=[{id:'category',feature:'category',statistic:'category-emergence',severity:'major',tolerance:{value:2,unit:'points_of_share',source:'Owner row'},toleranceOrigin:'declared'},{id:'volume',feature:'volume',statistic:'volume-drift',severity:'minor',tolerance:{value:20,unit:'percent_of_baseline',source:'Proposed for review'},toleranceOrigin:'proposed'}].map(r=>({...r,rule:'Measure movement',binning:'Shared bins',why:'Report decisions',expressible:true,expectedToTrip:false,profileEvidence:'Observed counts'}));
 await writeFile(path.join(root,'rules/shift-rules.json'),JSON.stringify({rules,unexpressible:[]}));
 const c=controls.spec.controls[0];
 const report={windows:{baseline:p.baseline,current:p.current},rules:rules.map(r=>({id:r.id,feature:r.feature,statistic:r.statistic,severity:r.severity,tolerance:r.tolerance.value,value:0,shifted:false,checkedUnit:'rows',checkedBaseline:10,checkedCurrent:10,excludedBaseline:0,excludedCurrent:0,query:'measure counts',topContributors:[]})),notRun:[],negativeControls:[{...c,value:c.achieved,tripped:c.expectTrip,source:'sdk-control'}],bySeverity:{blocker:{total:0,shifted:0},major:{total:1,shifted:0},minor:{total:1,shifted:0}},shifted:0,stable:2};
 const previous=[{acceptance:'dataset-shift-profile',workspace:path.join(root,'profile'),inputs},{acceptance:'dataset-shift-rules',workspace:path.join(root,'rules')}];
 await writeFile(path.join(root,'audit/checks.sh'),'echo test\n');
 for(const mode of ['pass','bad-value','wrong-tolerance','missing-rule','control-missed','control-rounded','counts']){
  const doc=structuredClone(report);if(mode==='bad-value')doc.rules[0].shifted=true;if(mode==='wrong-tolerance')doc.rules[0].tolerance=3;if(mode==='missing-rule')doc.rules.pop();if(mode==='control-missed')doc.negativeControls[0].tripped=false;if(mode==='control-rounded')doc.negativeControls[0].value=0;if(mode==='counts')doc.rules[0].checkedCurrent=9;
  const accept=shiftAcceptance('dataset-shift-audit',path.join(root,'audit'),previous);
  const item={id:'measure',type:'command_execution',command:"/bin/sh -c 'sh checks.sh'",status:'completed',exit_code:0};
  await accept.onEvent({type:'item.started',item});await writeFile(path.join(root,'audit/shift-scorecard.json'),JSON.stringify(doc));await accept.onEvent({type:'item.completed',item});
  assert.equal((await accept.verify()).passed,mode==='pass',mode);
 }
});

test('selected statistic is sufficient and optional proposals cannot replace or borrow owner thresholds',async t=>{
 const root=await mkdtemp(path.join(os.tmpdir(),'sdk-selected-shift-'));t.after(()=>rm(root,{recursive:true,force:true}));
 for(const folder of ['profile','rules'])await mkdir(path.join(root,folder));
 const side={nullCount:0,nullPct:0,distinctCount:2,bins:[{bin:'low',count:7,share:.7},{bin:'high',count:3,share:.3}]};
 const window={verified:true,rows:10,periodObserved:'observed',scanned:'full'};
 for(const type of ['numeric','categorical']){
  const declared={...feature,type,statistic:'psi',tolerance:.2,unit:'psi'};
  const other={...declared,feature:'other',tolerance:.4};
  const measured=name=>({name,type,binningNote:'Shared baseline bins',baseline:side,current:side});
  const profile={baseline:{...window,identity:'before.csv'},current:{...window,identity:'after.csv'},features:[measured('category'),measured('other')],volumeByPeriod:[{window:'baseline',period:'before',rows:10},{window:'current',period:'after',rows:10}],schemaDifferences:[],comparabilityHazards:[],openQuestions:[]};
  const rule=f=>({id:f.feature,feature:f.feature,statistic:f.statistic,severity:'major',tolerance:{value:f.tolerance,unit:f.unit,source:'Owner row'},toleranceOrigin:'declared',rule:'Measure selected statistic',binning:'Shared baseline bins',why:'Report segmentation',expressible:true,expectedToTrip:false,profileEvidence:'Measured counts'});
  const primary=[rule(declared),rule(other)];
  const extra={...rule(declared),id:'extra-emergence',statistic:'category-emergence',toleranceOrigin:'proposed',tolerance:{value:2,unit:'points_of_share',source:'Proposed for review'}};
  const volume={...extra,id:'extra-volume',feature:'volume',statistic:'volume-drift',tolerance:{value:20,unit:'percent_of_baseline',source:'Proposed for review'}};
  const inputs={baselineData:'before.csv',currentData:'after.csv',keyColumns:'id',features:JSON.stringify([declared,other]),seededControl:''};
  const previous=[{acceptance:'dataset-shift-profile',workspace:path.join(root,'profile'),inputs}];
  const cases=[
   ['selected-only',{},true],
   ['optional-extras',{rules:[...primary,extra,volume]},true],
   ['dropped-feature',{rules:[primary[0]]},false],
   ['substituted-statistic',{rules:[extra,primary[1]]},false],
   ['widened-tolerance',{rules:[{...primary[0],tolerance:{...primary[0].tolerance,value:.3}},primary[1]]},false],
   ['changed-unit',{rules:[{...primary[0],tolerance:{...primary[0].tolerance,unit:'absolute'}},primary[1]]},false],
   ['borrowed-row',{rules:[{...primary[0],tolerance:primary[1].tolerance},primary[1]]},false],
   ['extra-claims-owner',{rules:[...primary,{...extra,toleranceOrigin:'declared',tolerance:primary[0].tolerance}]},false],
   ['proposed-primary',{rules:[{...primary[0],toleranceOrigin:'proposed'},primary[1]]},false],
   ['incompatible-documented',{rules:[primary[0]],unexpressible:[{feature:'other',why:'Units differ'}],incompatible:true},true],
   ['incompatible-scored',{incompatible:true},false],
  ];
  for(const [name,change,passed] of cases){
   const p=structuredClone(profile);
   if(change.incompatible){p.features[1]={name:'other',type,comparable:false,reason:'Units differ',binningNote:'Not comparable'};p.schemaDifferences=[{column:'other',difference:'Units differ'}];}
   await writeFile(path.join(root,'profile/window-profiles.json'),JSON.stringify(p));
   await writeFile(path.join(root,'rules/shift-rules.json'),JSON.stringify({rules:primary,unexpressible:[],...change}));
   const outcome=await shiftAcceptance('dataset-shift-rules',path.join(root,'rules'),previous).verify();
   assert.equal(outcome.passed,passed,`${type}/${name}: ${outcome.findings.join('; ')}`);
  }
 }
});
