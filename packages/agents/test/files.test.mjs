import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,mkdir,writeFile,open,rm,truncate,link,symlink} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import os from 'node:os';
import path from 'node:path';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {fileURLToPath} from 'node:url';
import fs from 'node:fs/promises';
import {syncBuiltinESMExports} from 'node:module';
import {inventoryFiles,verifyFiles,readJSON,maxFileBytes,maxStepBytes} from '../src/workflows/files.mjs';

async function setup(t) {
  const root=await mkdtemp(path.join(os.tmpdir(),'sdk-files-'));
  t.after(()=>rm(root,{recursive:true,force:true}));
  const folder=path.join(root,'build');await mkdir(folder);
  return {root,folder,file:path.join(folder,'data.csv')};
}
async function sparse(file,bytes) { await writeFile(file,'');await truncate(file,bytes); }
function coarseMetadata(t,proto) {
  const stat=proto.stat,lstat=fs.lstat;
  const coarse=info=>Object.assign(info,{mtimeNs:0n,ctimeNs:0n});
  t.mock.method(proto,'stat',async function(...args){return coarse(await stat.apply(this,args));});
  t.mock.method(fs,'lstat',async(...args)=>coarse(await lstat(...args)));
  syncBuiltinESMExports();
  t.after(()=>{t.mock.restoreAll();syncBuiltinESMExports();});
}

test('large checked files stream through inventory and preservation while reports stay small',async t=>{
  const {root,folder,file}=await setup(t),size=64*1024*1024;
  await sparse(file,size);
  const hash=createHash('sha256'),chunk=Buffer.alloc(256*1024);
  for(let n=0;n<size;n+=chunk.length)hash.update(chunk);
  const files=await inventoryFiles(root,'build');
  assert.deepEqual(files,[{path:'build/data.csv',bytes:size,sha256:hash.digest('hex')}]);
  await verifyFiles(root,[{id:'build',title:'Build',files}]);
  await assert.rejects(readJSON(folder,'data.csv'),/up to 1 MB/);
  await writeFile(path.join(folder,'report.json'),' {"rows":1000000} ');
  assert.deepEqual(await readJSON(folder,'report.json'),{rows:1000000});
  const handle=await open(file,'r+');await handle.write(Buffer.from('changed'),0,7,size-7);await handle.close();
  await assert.rejects(verifyFiles(root,[{id:'build',title:'Build',files}]),/outputs changed/);
});

test('inventory rejects quota excess before streaming multi-gigabyte data',async t=>{
  const {root,folder,file}=await setup(t);
  await sparse(file,maxFileBytes+1);await assert.rejects(inventoryFiles(root,'build'),/up to 2 GB/);
  await truncate(file,maxFileBytes);
  for(let i=1;i<=maxStepBytes/maxFileBytes;i++)await sparse(path.join(folder,`extra-${i}`),maxFileBytes);
  await assert.rejects(inventoryFiles(root,'build'),/8 GB/);
});

test('artifact hashing keeps binary buffers bounded in a fresh SDK process',async t=>{
  const {folder,file}=await setup(t);await sparse(file,128*1024*1024);
  const {stdout}=await promisify(execFile)(process.execPath,['--expose-gc',fileURLToPath(new URL('./fixtures/stream-inventory.mjs',import.meta.url)),folder]);
  const result=JSON.parse(stdout);
  assert.equal(result.files[0].bytes,128*1024*1024);
  assert.ok(result.delta.arrayBuffers<4*1024*1024,`Binary buffers grew by ${result.delta.arrayBuffers} bytes`);
});

test('streaming detects growth, truncation and same-size edits during the read',async t=>{
  for(const change of ['grow','truncate','edit']) await t.test(change,async t=>{
    const {root,file}=await setup(t),size=8*1024*1024;await sparse(file,size);
    const probe=await open(file),proto=Object.getPrototypeOf(probe),original=proto.read;await probe.close();
    // Linux exposed edits sharing a timestamp. Force that condition on every OS.
    if(change==='edit')coarseMetadata(t,proto);
    let reads=0;
    t.mock.method(proto,'read',async function(...args){
      const result=await original.apply(this,args);
      if(++reads===2){
        if(change==='grow')await truncate(file,size+1);
        else if(change==='truncate')await truncate(file,size-1);
        else {const writer=await open(file,'r+');await writer.write(Buffer.from('x'),0,1,0);await writer.close();}
      }
      return result;
    });
    await assert.rejects(inventoryFiles(root,'build'),/changed|grew/);
    assert.ok(reads>=2);
  });
});

test('bounded reports reject a mixed read even when metadata does not change',async t=>{
  const {folder}=await setup(t),file=path.join(folder,'report.json');await writeFile(file,'{"n":1}');
  const probe=await open(file),proto=Object.getPrototypeOf(probe),original=proto.read;await probe.close();
  coarseMetadata(t,proto);let reads=0;
  t.mock.method(proto,'read',async function(...args){
    const result=await original.apply(this,args);
    if(++reads===1){const writer=await open(file,'r+');await writer.write(Buffer.from('2'),0,1,5);await writer.close();}
    return result;
  });
  await assert.rejects(readJSON(folder,'report.json'),/changed during/);
});

test('cancellation interrupts a large file between chunks and prevents report reads',async t=>{
  const {root,folder,file}=await setup(t);await sparse(file,64*1024*1024);
  const controller=new AbortController(),probe=await open(file),proto=Object.getPrototypeOf(probe),original=proto.read;await probe.close();
  let reads=0;
  t.mock.method(proto,'read',async function(...args){const result=await original.apply(this,args);if(++reads===4)controller.abort();return result;});
  await assert.rejects(inventoryFiles(root,'build',{signal:controller.signal}),{name:'AbortError'});
  assert.equal(reads,4);
  await assert.rejects(readJSON(folder,'data.csv',{signal:controller.signal}),{name:'AbortError'});
});

test('large-file admission does not permit hardlinks, symlinks or linked reports',async t=>{
  const {root,folder,file}=await setup(t);await sparse(file,2*1024*1024);
  const alias=path.join(folder,'alias');await link(file,alias);
  await assert.rejects(inventoryFiles(root,'build'),/unlinked/);await rm(alias);
  await symlink(file,alias);await assert.rejects(inventoryFiles(root,'build'),/unlinked/);await rm(alias);
  await writeFile(path.join(folder,'report.json'),'{}');await symlink(path.join(folder,'report.json'),alias);
  await assert.rejects(readJSON(folder,'alias'));
});
