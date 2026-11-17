import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { listChecks, getCheck, validateCheck, preflightCheck, createAcceptance } from '@hyperplane/agents/checks';
import { validateNodes } from '@hyperplane/agents/nodes';

import { declaredTask as task } from './fixtures/check-declarations.mjs';
// The minimal four-list check contract every catalog entry must carry.
const contract=()=>({produces:['report.json'],recomputes:['Reads report.json'],proves:['The file exists'],doesNotProve:['That the numbers are right']});
const approval = {id:'approve',title:'Approve',instruction:'Review the current plan',kind:'approval'};

test('the public immutable catalog is the single static description source', async()=>{
  const disk=JSON.parse(await readFile(new URL('../checks.json',import.meta.url),'utf8'));
  assert.equal(disk.version,1);
  assert.deepEqual(listChecks(),disk.checks);
  assert.equal(listChecks().length,16);
  assert.throws(()=>listChecks().push({}),TypeError);
  for(const definition of listChecks()) {
    assert.equal(getCheck(definition.id),definition);
    assert.ok(definition.description.length>100);
    assert.throws(()=>{definition.description='Changed';},TypeError);
    if(definition.inputs) assert.throws(()=>definition.inputs.required.push('another'),TypeError);
    if(definition.requires) assert.throws(()=>definition.requires.push('another'),TypeError);
  }
  assert.throws(()=>getCheck('unknown'),/Unknown SDK check/);
  for(const name of ['data-quality','dataset-shift','reconciliation','pipeline-change','data-pipeline']) {
    const source=await readFile(new URL(`../src/workflows/${name}.mjs`,import.meta.url),'utf8');
    assert.doesNotMatch(source,/const contracts\s*=/);
    assert.match(source,/getCheck\(kind\)\.description/);
    // Long descriptions have one home; deterministic validators stay in code.
    for(const check of listChecks().filter(c=>c.id.startsWith(name+'-'))) assert.ok(!source.includes(check.description));
  }
  const runner=await readFile(new URL('../src/workflows/nodes.mjs',import.meta.url),'utf8');
  assert.doesNotMatch(runner,/data-quality|dataset-shift|reconciliation|pipeline-change|data-pipeline|-profile|-rules|-audit/);
});

test('generic prerequisite metadata binds current measurements, plans and approvals for every check',()=>{
  for(const definition of listChecks()) {
    const node=task(definition.id);
    const previous=(definition.requires??[]).map((id,i)=>task(id,'prior-'+i));
    if(definition.approvalAfter) previous.push(approval);
    assert.doesNotThrow(()=>validateCheck(node,previous));
    assert.doesNotThrow(()=>validateNodes('Do work',[...previous,node]));
    for(const required of definition.requires??[]) {
      assert.throws(()=>validateCheck(node,previous.filter(n=>n.acceptance!==required)),/prerequisite/);
    }
    if(definition.requires?.length>1) {
      assert.throws(()=>validateCheck(node,[...previous,task(definition.requires[0],'remeasure')]),/prerequisite/);
      const renewed=[...previous,task(definition.requires[0],'remeasure'),task(definition.requires[1],'replan')];
      assert.throws(()=>validateCheck(node,renewed),/Approve/);
      assert.doesNotThrow(()=>validateCheck(node,[...renewed,{...approval,id:'approve-again'}]));
    }
    if(definition.approvalAfter) {
      assert.throws(()=>validateCheck(node,previous.filter(n=>n.kind!=='approval')),/Approve/);
      assert.throws(()=>validateCheck(node,[approval,...previous.filter(n=>n.kind!=='approval')]),/Approve/);
      assert.throws(()=>validateCheck(node,[...previous,task(definition.approvalAfter,'changed-plan')]),/Approve/);
    }
  }
});

test('catalog inputs reject missing, extra, oversized and incorrectly typed values',()=>{
  for(const definition of listChecks()) {
    if(!definition.inputs) continue;
    const node=task(definition.id);
    for(const key of definition.inputs.required) {
      for(const value of [undefined,'',null,42,'x'.repeat(16001)]) assert.throws(()=>validateCheck({...node,inputs:{...node.inputs,[key]:value}}),/source inputs/);
    }
    assert.throws(()=>validateCheck({...node,inputs:{...node.inputs,notDeclared:'x'}}),/source inputs/);
    for(const key of definition.inputs.optional) {
      assert.doesNotThrow(()=>validateCheck({...node,inputs:{...node.inputs,[key]:''}}));
      assert.throws(()=>validateCheck({...node,inputs:{...node.inputs,[key]:null}}),/source inputs/);
    }
  }
  assert.throws(()=>validateCheck({...task('data-quality-profile'),check:['true']}),/Choose/);
  assert.throws(()=>validateCheck({...task('data-quality-profile'),kind:'document'}),/Choose/);
  assert.throws(()=>validateCheck({kind:'task',inputs:{source:'undeclared'}}),/selected check/);
  assert.equal(createAcceptance({kind:'task'},'/unused'),undefined);
});

test('generic preflight dispatch rejects missing source files and respects cancellation',async()=>{
  const node={...task('data-quality-profile'),inputs:{dataset:'/nonexistent/hyperplane-catalog-data.csv',keyColumns:'id'}};
  await assert.rejects(preflightCheck(node),/read|missing|exist|open/i);
  const controller=new AbortController();controller.abort(new Error('stop preflight'));
  await assert.rejects(preflightCheck(node,{signal:controller.signal}),/stop preflight/);
  await preflightCheck({kind:'task'});
});

import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

test('input table metadata is immutable and malformed catalogs fail on import', async()=>{
  const table=getCheck('dataset-shift-profile').inputTables.features;
  assert.throws(()=>{table.columns[1].options[0]='changed';},TypeError);
  const source=await readFile(new URL('../src/workflows/check-catalog.mjs',import.meta.url),'utf8');
  const root=await mkdtemp(path.join(tmpdir(),'check-table-schema-'));
  try {
    const original={version:1,checks:[{id:'example',title:'Example',summary:'A check',description:'A check',...contract(),inputs:{required:['rows'],optional:[]},inputTables:{rows:{label:'Rows',minRows:1,maxRows:32,uniqueBy:'name',columns:[{id:'name',label:'Name',required:true}]}}}]};
    const changes=[
      t=>{t.maxRows=65;},t=>{delete t.minRows;},t=>{t.uniqueBy='missing';},t=>{t.columns[0].required=null;},
      t=>{t.columns[0].format='money';},t=>{t.columns[0].options=[];},t=>{t.columns[0].unknown=true;},
      t=>{t.columns[0].options=['a'];t.columns[0].default='b';},
    ];
    for(const [index,change] of changes.entries()) {
      const folder=path.join(root,String(index));await mkdir(path.join(folder,'src','workflows'),{recursive:true});
      const catalog=structuredClone(original);change(catalog.checks[0].inputTables.rows);
      await writeFile(path.join(folder,'checks.json'),JSON.stringify(catalog));
      const file=path.join(folder,'src','workflows','check-catalog.mjs');await writeFile(file,source);
      await assert.rejects(import(pathToFileURL(file).href),/Invalid SDK check catalog/);
    }
  } finally { await rm(root,{recursive:true,force:true}); }
});


import {shiftInputs} from '../src/workflows/shift-inputs.mjs';
import {reconciliationInputs} from '../src/workflows/reconciliation.mjs';
import {pipelineChangeInputs} from '../src/workflows/pipeline-change.mjs';
import {pipelineMeasures} from '../src/workflows/data-pipeline.mjs';

test('catalog table choices agree with executable input validators',()=>{
  const choices=(check,table,column)=>getCheck(check).inputTables[table].columns.find(c=>c.id===column).options;
  const feature={feature:'category',type:'categorical',whyItMatters:'Segmentation',statistic:'psi',unit:'absolute',tolerance:'2'};
  for(const column of ['type','severity','statistic','unit']) {
    for(const value of [...choices('dataset-shift-profile','features',column),'unsupported']) {
      const row={...feature,[column]:value};
      if(column==='unit') row.statistic=({psi:'psi',kl:'kl',points_of_share:'rate-delta',percent_of_baseline:'volume-drift',count:'volume-drift',absolute:'psi'})[value]??'psi';
      const call=()=>shiftInputs({features:JSON.stringify([row])});
      if(value==='unsupported')assert.throws(call);else assert.doesNotThrow(call);
    }
  }
  for(const column of ['kind','unit']) {
    for(const value of [...choices('pipeline-change-profile','invariants',column),'unsupported']) {
      const row={invariant:'count',measure:'Rows',kind:'number',unit:'exact',tolerance:'0',[column]:value};
      const call=()=>pipelineChangeInputs({invariants:JSON.stringify([row])});
      if(value==='unsupported')assert.throws(call);else assert.doesNotThrow(call);
    }
  }
  for(const unit of [...choices('data-pipeline-profile','measures','unit'),'unsupported']) {
    const call=()=>pipelineMeasures({measures:JSON.stringify([{measure:'count',definition:'Rows',unit,tolerance:'0'}])});
    if(unit==='unsupported')assert.throws(call);else assert.doesNotThrow(call);
  }
  for(const unit of [...choices('reconciliation-profile','tolerances','unit'),'unsupported']) {
    const call=()=>reconciliationInputs({measures:JSON.stringify([{measure:'count',definitionA:'Rows',definitionB:'Rows'}]),tolerances:JSON.stringify([{measure:'count',unit,value:'0',segmentUnit:unit,segmentValue:'0'}])});
    if(unit==='unsupported')assert.throws(call);else assert.doesNotThrow(call);
  }
  for(const side of [...choices('reconciliation-profile','knownDifferences','side'),'unsupported']) {
    const call=()=>reconciliationInputs({measures:JSON.stringify([{measure:'count',definitionA:'Rows',definitionB:'Rows'}]),tolerances:JSON.stringify([{measure:'count',unit:'exact'}]),knownDifferences:JSON.stringify([{name:'Known filter',side}])});
    if(side==='unsupported')assert.throws(call);else assert.doesNotThrow(call);
  }
});


test('every check states what it produces, recomputes, proves and does not prove', async()=>{
  for(const definition of listChecks()) {
    assert.ok(definition.summary.trim().length>0&&definition.summary.length<=900&&!/[\r\n]/.test(definition.summary),`${definition.id} has no usable summary`);
    for(const field of ['produces','recomputes','proves','doesNotProve']) {
      const items=definition[field];
      assert.ok(Array.isArray(items)&&items.length>=1&&items.length<=8,`${definition.id} ${field} must hold 1-8 sentences`);
      for(const item of items) assert.ok(typeof item==='string'&&item.trim()&&item.length<=200,`${definition.id} ${field} item is unusable: ${item}`);
      assert.throws(()=>items.push('another'),TypeError);
    }
    // A check whose pass proves everything is exactly the failure this replaced.
    assert.ok(definition.doesNotProve.length>0,`${definition.id} states no boundary`);
  }
});

test('a catalog entry without a bounded contract fails on import', async()=>{
  const source=await readFile(new URL('../src/workflows/check-catalog.mjs',import.meta.url),'utf8');
  const root=await mkdtemp(path.join(tmpdir(),'check-contract-'));
  try {
    const base={version:1,checks:[{id:'example',title:'Example',summary:'A check',description:'A check',...contract()}]};
    const changes=[
      c=>{delete c.doesNotProve;},c=>{c.doesNotProve=[];},c=>{c.produces=[];},c=>{delete c.recomputes;},
      c=>{c.proves=['x'.repeat(201)];},c=>{c.produces=['   '];},c=>{c.produces=Array(9).fill('a');},
      c=>{delete c.summary;},c=>{c.summary='A check\nwith a second paragraph';},c=>{c.summary='';},
    ];
    for(const [index,change] of changes.entries()) {
      const folder=path.join(root,String(index));await mkdir(path.join(folder,'src','workflows'),{recursive:true});
      const catalog=structuredClone(base);change(catalog.checks[0]);
      await writeFile(path.join(folder,'checks.json'),JSON.stringify(catalog));
      const file=path.join(folder,'src','workflows','check-catalog.mjs');await writeFile(file,source);
      await assert.rejects(import(pathToFileURL(file).href),/Invalid SDK check catalog/);
    }
    // The same fixture, unchanged, still loads.
    const folder=path.join(root,'valid');await mkdir(path.join(folder,'src','workflows'),{recursive:true});
    await writeFile(path.join(folder,'checks.json'),JSON.stringify(base));
    const file=path.join(folder,'src','workflows','check-catalog.mjs');await writeFile(file,source);
    const loaded=await import(pathToFileURL(file).href);
    assert.equal(loaded.listChecks().length,1);
  } finally { await rm(root,{recursive:true,force:true}); }
});
