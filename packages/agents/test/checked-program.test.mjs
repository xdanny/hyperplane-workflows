import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,rm,symlink,open} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {checkedProgram} from '../src/workflows/checked-program.mjs';

test('checked programs recognize both connected and offline SDK shell forms, and reject chaining or stale outputs',async t=>{
 const work=await mkdtemp(path.join(os.tmpdir(),'sdk-checked-program-'));t.after(()=>rm(work,{recursive:true,force:true}));
 await writeFile(path.join(work,'checks.sh'),'echo measured\n');
 for(const command of ['sh checks.sh',"/bin/zsh -lc 'sh checks.sh'",'/bin/zsh -c "sh checks.sh"',"/bin/bash -c 'sh checks.sh'"]){
  const p=checkedProgram(work),item={id:'c',type:'command_execution',command,status:'completed',exit_code:0};
  await p.onEvent({type:'item.started',item});await writeFile(path.join(work,'result.json'),'{}');await p.onEvent({type:'item.completed',item});await p.verify();
  await writeFile(path.join(work,'result.json'),'{"changed":true}');await assert.rejects(p.verify(),/Outputs changed/);
  await p.onEvent({type:'item.started',item});await p.onEvent({type:'item.completed',item:{...item,exit_code:1,status:'failed'}});await assert.rejects(p.verify(),/complete it successfully/);
 }
 for(const command of ['sh checks.sh; true','cd . && sh checks.sh','sh other.sh',"/bin/zsh -c 'sh checks.sh > result'"]){
  const p=checkedProgram(work),item={id:'c',type:'command_execution',command,status:'completed',exit_code:0};
  await p.onEvent({type:'item.started',item});await p.onEvent({type:'item.completed',item});await assert.rejects(p.verify(),/Run exactly/);
 }
 const p=checkedProgram(work),item={id:'c',type:'command_execution',command:'sh checks.sh',status:'completed',exit_code:0};
 await p.onEvent({type:'item.started',item});await writeFile(path.join(work,'checks.sh'),'echo changed\n');await assert.rejects(p.onEvent({type:'item.completed',item}),/changed during/);
 await rm(path.join(work,'checks.sh'));await symlink('/etc/hosts',path.join(work,'checks.sh'));await assert.rejects(p.onEvent({type:'item.started',item}),/link|regular/);
});

test('a command may create outputs while its start event pins the unchanged check program',async t=>{
 const work=await mkdtemp(path.join(os.tmpdir(),'sdk-start-event-'));t.after(()=>rm(work,{recursive:true,force:true}));
 const script=path.join(work,'checks.sh');await writeFile(script,'echo measured\n');
 const probe=await open(script),proto=Object.getPrototypeOf(probe),original=proto.read;await probe.close();
 let reads=0;
 t.mock.method(proto,'read',async function(...args){
  const result=await original.apply(this,args);
  if(++reads===1)await writeFile(path.join(work,'result.json'),'{"rows":4}');
  return result;
 });
 const program=checkedProgram(work),item={id:'c',type:'command_execution',command:'sh checks.sh',status:'completed',exit_code:0};
 await program.onEvent({type:'item.started',item});
 await program.onEvent({type:'item.completed',item});
 await program.verify();
});
