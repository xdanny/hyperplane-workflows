import test from 'node:test';
import assert from 'node:assert/strict';
import {amount,withinTolerance,reconciliationInputs,checkReconciliationInputs} from '../src/workflows/reconciliation.mjs';
import {validateNodes,runNodes} from '../src/workflows/nodes.mjs';
import {mkdtemp,writeFile,rm} from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';

const inputs={sourceA:'/data/a.csv',sourceB:'/data/b.csv',sharedGrain:'one row per id',keyColumns:'id',measures:JSON.stringify([{measure:'revenue',definitionA:'Sum amount',definitionB:'Sum amount'}]),tolerances:JSON.stringify([{measure:'revenue',unit:'absolute',value:'0.01'}])};
test('reconciliation uses exact decimal amounts, inclusive boundaries and the selected percentage denominator',()=>{
 assert.equal(amount('1000000000000000000.01')-amount('1000000000000000000'),amount('0.01'));
 assert.equal(withinTolerance('0.01','1000000000000000000','0',{value:'0.01',unit:'absolute'}),true);
 assert.equal(withinTolerance('0.010000000000000000000001','1000000000000000000','0',{value:'0.01',unit:'absolute'}),false);
 assert.equal(withinTolerance('1','100','10',{value:'1',unit:'percent_of_a'}),true);
 assert.equal(withinTolerance('1','100','10',{value:'1',unit:'percent_of_b'}),false);
 assert.equal(withinTolerance('-1','-100','10',{value:'1',unit:'percent_of_a'}),true);
 assert.equal(withinTolerance('0','0','0',{value:'50',unit:'percent_of_a'}),true);
 assert.equal(withinTolerance('0.01','0','0',{value:'50',unit:'percent_of_a'}),false);
 assert.equal(withinTolerance('0','0','0',{value:'0',unit:'exact'}),true);
 for(const value of ['NaN','1e-9','0.0000000000000000000000001',Number.MAX_SAFE_INTEGER+1])assert.throws(()=>amount(value));
});
test('source admission preserves per-measure units and requires approval after the latest profile and equations',async t=>{
 assert.equal(reconciliationInputs(inputs).measures.length,1);
 assert.equal(reconciliationInputs({...inputs,tolerances:'[{"measure":"revenue","unit":"exact"}]'}).tolerances[0].value,'0');
 for(const tolerances of ['[]','[{"measure":"other","unit":"absolute","value":"1"}]','[{"measure":"revenue","unit":"absolute","value":"1","segmentValue":"2"}]'])assert.throws(()=>reconciliationInputs({...inputs,tolerances}));
 const p={id:'profile',title:'Profile',kind:'task',instruction:'Read both sources',acceptance:'reconciliation-profile',inputs},r={id:'rules',title:'Rules',kind:'task',instruction:'Propose equations',acceptance:'reconciliation-rules'},a={id:'approve',title:'Approve',kind:'approval',instruction:'Decide'},x={id:'audit',title:'Reconcile',kind:'task',instruction:'Execute equations',acceptance:'reconciliation-audit'};
 assert.doesNotThrow(()=>validateNodes('Compare sources',[p,r,a,x]));
 for(const nodes of [[r],[p,x],[p,a,r,x],[p,r,a,{...p,id:'later-profile'},x]])assert.throws(()=>validateNodes('Compare sources',nodes));
 const root=await mkdtemp(path.join(os.tmpdir(),'sdk-recon-inputs-'));t.after(()=>rm(root,{recursive:true,force:true}));const source=path.join(root,'a.csv');await writeFile(source,'wrong,amount\n1,100\n');const bad={...inputs,sourceA:source};
 await assert.rejects(checkReconciliationInputs(bad),/key column/i);
 let calls=0;await assert.rejects(runNodes({id:''},{workspace:root,instruction:'Compare',nodes:[{...p,inputs:bad}],executeTask:()=>{calls++;}}));assert.equal(calls,0);
});
