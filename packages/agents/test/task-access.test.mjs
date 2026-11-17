import test from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import os from 'node:os';
import {fileURLToPath} from 'node:url';
import {mkdtemp,readFile,rm,stat,symlink,writeFile,realpath} from 'node:fs/promises';
import {validateTaskAccess,taskAccessOptions} from '../src/task-access.mjs';
import {runTask} from '../src/workflows/task.mjs';

const valid={access:{hosts:['example.com']}};
test('execution grants reject ambiguous hosts, broad paths and application environment settings',()=>{
  validateTaskAccess(valid);
  for(const input of [
    {access:null},{access:'hosts'}, {access:[]}, {access:{...valid.access,extra:true}},
    ...['https://example.com','*.example.com','example.com:443','Example.com','example.com.','a'.repeat(64)+'.com'].map(h=>({access:{hosts:[h]}})),
    {access:{hosts:[]}},{access:{hosts:['example.com','example.com']}},
    ...['/','relative','/data/../secret','/data/*'].map(p=>({access:{...valid.access,readPaths:[p]}})),
    ...['PATH','DATABASE_URL','OPENAI_API_KEY','WORKFLOW_lowercase','WORKFLOW_X=value'].map(k=>({access:{...valid.access,environment:[k]}})),
    {...valid,toolchain:true},{toolchain:true,sideEffect:true},{...valid,sideEffect:true},{...valid,toolchain:'true'},
  ]) assert.throws(()=>validateTaskAccess(input),JSON.stringify(input));
});

test('a readable local input needs no network host',async t=>{
  // Reading one file is not a reason to open the network. The grant still has
  // to name something: an access object that grants nothing is refused.
  validateTaskAccess({access:{readPaths:['/data/sales.csv']}});
  validateTaskAccess({access:{hosts:[],environment:['WORKFLOW_SALES_CONNECTION']}});
  assert.throws(()=>validateTaskAccess({access:{hosts:[],readPaths:[],environment:[]}}),/at least one/);
  assert.throws(()=>validateTaskAccess({access:{readPaths:['/data/sales.csv']},toolchain:true,sideEffect:true}),/allowed network hosts/);
  const work=await realpath(await mkdtemp(path.join(os.tmpdir(),'sdk-access-local-')));t.after(()=>rm(work,{recursive:true,force:true}));
  await writeFile(path.join(work,'sales.csv'),'id\n1\n');
  const policy=await taskAccessOptions({workspace:work,access:{readPaths:[path.join(work,'sales.csv')]}},{PATH:work,HOME:os.homedir()});
  t.after(()=>rm(policy.runtime,{recursive:true,force:true}));
  const raw=policy.clientOptions.configOverrides.join('\n');
  assert.match(raw,/"network"=\{"enabled"=false\}/);
  assert.match(raw,/features.network_proxy=\{"enabled"=false\}/);
  assert.match(raw,new RegExp(JSON.stringify(path.join(work,'sales.csv'))+'="read"'));
  assert.ok(!raw.includes('"allow"'));
  assert.match(policy.instructions,/Network access is disabled for this step/);
});

test('named profile uses whole TOML maps, minimal reads, dedicated environment and temporary tool roots',async t=>{
  const work=await realpath(await mkdtemp(path.join(os.tmpdir(),'sdk-access-options-')));t.after(()=>rm(work,{recursive:true,force:true}));
  await writeFile(path.join(work,'uv'),'#!/bin/sh\nexit 0\n',{mode:0o700});
  const env={PATH:work,HOME:os.homedir(),WORKFLOW_PROBE:'selected-canary',UNSELECTED_PROBE:'not-forwarded',DATABASE_URL:'private-canary',HTTP_PROXY:'private-proxy',BASH_ENV:'/private-init'};
  const policy=await taskAccessOptions({workspace:work,access:{hosts:['example.com'],environment:['WORKFLOW_PROBE']},toolchain:true,sideEffect:true},env);
  t.after(()=>rm(policy.runtime,{recursive:true,force:true}));
  assert.deepEqual(Object.keys(policy.clientOptions.env).sort(),['HOME','PATH','WORKFLOW_PROBE']);
  const raw=policy.clientOptions.configOverrides.join('\n');
  assert.match(raw,/"example.com"="allow"/);assert.match(raw,/":minimal"="read"/);
  assert.match(raw,/features.network_proxy=.*"enabled"=true/);assert.match(raw,/"UV_CACHE_DIR"/);
  assert.ok(!raw.includes('selected-canary')&&!raw.includes('private-canary')&&!raw.includes('sandbox_workspace_write'));
  assert.ok(!policy.runtime.startsWith(work));
  await assert.rejects(taskAccessOptions({workspace:work,access:{hosts:['example.com'],environment:['WORKFLOW_MISSING']}},env),/Configure.*WORKFLOW_MISSING/);
  await assert.rejects(taskAccessOptions({workspace:work,access:{hosts:['example.com'],readPaths:[path.join(work,'missing.csv')]}},env), /Readable input unavailable: .*missing\.csv\. Check that it exists and the run host can read it\./);
  await symlink('/',path.join(work,'root-link'));
  await assert.rejects(taskAccessOptions({workspace:work,access:{hosts:['example.com'],readPaths:[path.join(work,'root-link')]}},env),/filesystem root/);
  await assert.rejects(taskAccessOptions({workspace:work,...valid,toolchain:true,sideEffect:true},{}),/requires uv/);
});

test('real SDK transport omits legacy sandbox flags and removes the runtime on success, failure and cancellation',async t=>{
  const work=await mkdtemp(path.join(os.tmpdir(),'sdk-access-transport-'));t.after(()=>rm(work,{recursive:true,force:true}));
  const env={HYPERPLANE_CODEX_EXECUTABLE:fileURLToPath(new URL('../../../internal/codex/testdata/task-access',import.meta.url)),SDK_FIXTURE_CALLS:path.join(work,'calls.json'),WORKFLOW_PROBE:'selected-canary',UNSELECTED_PROBE:'not-forwarded'};
  const prior=Object.fromEntries(Object.keys({...env,SDK_FIXTURE_MODE:''}).map(k=>[k,process.env[k]]));
  t.after(()=>{for(const [k,v] of Object.entries(prior)){if(v===undefined)delete process.env[k];else process.env[k]=v;}});
  Object.assign(process.env,env);
  for(const mode of ['ordinary','rejected','transport','hang']){
    process.env.SDK_FIXTURE_MODE=mode;
    const controller=new AbortController();
    const pending=runTask({workspace:work,instruction:'Create answer.txt',access:{hosts:['example.com'],environment:['WORKFLOW_PROBE']},signal:controller.signal,
      acceptance:{description:'Check the answer',verify:async()=>({passed:mode!=='rejected',findings:mode==='rejected'?['rejected']:[]})},
      onEvent:async e=>{if(mode==='hang'&&e.type==='thread.started')controller.abort();}});
    if(['transport','hang'].includes(mode)) await assert.rejects(pending);
    else assert.equal((await pending).passed,mode==='ordinary');
    const receipt=JSON.parse(await readFile(env.SDK_FIXTURE_CALLS,'utf8'));
    await assert.rejects(stat(receipt.runtime),{code:'ENOENT'});
  }
});
