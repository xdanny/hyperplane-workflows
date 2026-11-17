import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, rm, writeFile, readFile, symlink } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { runTask } from '../src/workflows/task.mjs';
import { runIdeator } from '../src/workflows/ideator.mjs';
import { verifyIdeator } from '../src/workflows/verify-ideator.mjs';

test('empty, malformed and invalid ideas fail with actionable findings rather than throwing', async (t) => {
  const root = await mkdtemp(path.join(tmpdir(), 'sdk-ideas-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  await writeFile(path.join(root, 'brief.md'), 'Unvalidated hypotheses. '.repeat(10));
  for (const content of ['', 'null', '{', '{"ideas":[{"title":123},{"title":{}},null]}']) {
    await writeFile(path.join(root, 'ideas.json'), content);
    const result = await verifyIdeator(root);
    assert.equal(result.passed, false);
    assert.ok(result.findings.length);
  }
});

test('complete delivery passes; duplicate titles and symlink outputs fail', async (t) => {
  const root = await mkdtemp(path.join(tmpdir(), 'sdk-ideas-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const ideas = ['Route Notes', 'Repair Kit', 'Ride Planner'].map((title) => ({ title, user:'Weekend cyclists', problem:'Forgetting useful route details', firstVersion:'Offline local notes' }));
  const brief = ideas.map((i) => i.title).join(', ') + '. Unvalidated hypotheses. Compare the three ideas with weekend cyclists, starting with a small route-notebook experiment before investing in a full product.';
  await writeFile(path.join(root, 'brief.md'), brief);
  await writeFile(path.join(root, 'ideas.json'), JSON.stringify({ ideas }));
  assert.equal((await verifyIdeator(root)).passed, true);
  ideas[2].title = 'route notes';
  await writeFile(path.join(root, 'ideas.json'), JSON.stringify({ ideas }));
  assert.equal((await verifyIdeator(root)).passed, false);
  await rm(path.join(root, 'ideas.json'));
  await symlink(path.join(root, 'brief.md'), path.join(root, 'ideas.json'));
  assert.equal((await verifyIdeator(root)).passed, false);
});


test('ideator recipe uses the shared task runner with real SDK transport and unchanged recovery boundaries', async t => {
  const root = await mkdtemp(path.join(tmpdir(), 'sdk-ideator-runner-'));
  t.after(() => rm(root, {recursive:true,force:true}));
  const workspace = path.join(root,'workspace');
  await mkdir(workspace);
  const fixture = fileURLToPath(new URL('../../../internal/codex/testdata/ideator',import.meta.url));
  const wrapper = path.join(root,'capture');
  const prompts = path.join(root,'prompts.jsonl');
  // Capture the actual SDK input, then delegate to the existing fixture model.
  // This exercises SDK transport and host checks; it is not a native sandbox trial.
  await writeFile(wrapper,`#!/usr/bin/env python3
import json, os, subprocess, sys
prompt = sys.stdin.read()
with open(os.environ['SDK_FIXTURE_PROMPTS'], 'a') as file:
    file.write(json.dumps({'prompt':prompt, 'args':sys.argv[1:]}) + '\\n')
result = subprocess.run([os.environ['SDK_FIXTURE_IDEATOR'], *sys.argv[1:]], input=prompt, text=True)
sys.exit(result.returncode)
`,{mode:0o700});
  const configured = {HYPERPLANE_CODEX_EXECUTABLE:wrapper, SDK_FIXTURE_IDEATOR:fixture,
    SDK_FIXTURE_PROMPTS:prompts, SDK_FIXTURE_CALLS:path.join(root,'calls'),SDK_FIXTURE_MODE:'ordinary'};
  const previous = Object.fromEntries(Object.keys(configured).map(key=>[key,process.env[key]]));
  Object.assign(process.env,configured);
  t.after(()=>{for(const [key,value] of Object.entries(previous)) {
    if(value===undefined) delete process.env[key]; else process.env[key]=value;
  }});
  const calls = async()=> (await readFile(configured.SDK_FIXTURE_CALLS,'utf8')).trim().split('\n');
  const reset = async mode => {
    process.env.SDK_FIXTURE_MODE=mode;
    await writeFile(configured.SDK_FIXTURE_CALLS,'');
    await writeFile(prompts,'');
  };
  await t.test('one failed attempt corrects on the same thread and commits total usage',async()=>{
    await reset('recovery');
    const states=[];
    const result=await runIdeator({workspace,domain:'Offline ride notes',injectFailure:true,maxCorrections:1,
      checkpoint:async state=>states.push(state)});
    assert.equal(result.passed,true);
    assert.equal(result.threadId,'fixture-ideator-thread');
    assert.equal(result.attempts,2);
    assert.deepEqual(result.usage,{input_tokens:34,cached_input_tokens:6,output_tokens:4});
    assert.deepEqual(await calls(),['start','resume']);
    assert.equal(states.at(-1).phase,'passed');
    assert.match(states.find(s=>s.phase==='correction_pending').findings.join(' '),/exactly three ideas/);
    const captured=(await readFile(prompts,'utf8')).trim().split('\n').map(line=>JSON.parse(line));
    assert.match(captured[0].prompt,/Complete this requested task in the current directory/);
    assert.match(captured[0].prompt,/Offline ride notes/);
    assert.match(captured[0].prompt,/FIRST response must write exactly ONE idea/);
    assert.match(captured[1].prompt,/independent checks rejected/);
    assert.match(captured[1].prompt,/exactly three ideas/);
    assert.equal((await verifyIdeator(workspace)).passed,true);
  });
  await t.test('correction allowance and token budget still stop after the rejected attempt',async()=>{
    for(const options of [{maxCorrections:0},{maxCorrections:1,maxTokens:19}]) {
      await reset('recovery');
      const result=await runIdeator({workspace,injectFailure:true,...options});
      assert.equal(result.passed,false);assert.equal(result.attempts,1);
      assert.deepEqual(await calls(),['start']);
    }
  });
  await t.test('transport failure and failed persistence never start a correction',async()=>{
    await reset('transport');
    await assert.rejects(runIdeator({workspace,maxCorrections:1}));
    assert.deepEqual(await calls(),['start']);
    await reset('recovery');
    await assert.rejects(runIdeator({workspace,maxCorrections:1,checkpoint:async state=>{
      if(state.phase==='correction_pending') throw new Error('checkpoint unavailable');
    }}),/checkpoint unavailable/);
    assert.deepEqual(await calls(),['start']);
  });
  await t.test('cancellation before execution starts makes no SDK call',async()=>{
    await reset('ordinary');
    const controller=new AbortController();controller.abort(new Error('cancel recipe'));
    await assert.rejects(runIdeator({workspace,signal:controller.signal}),/cancel recipe/);
    assert.equal(await readFile(configured.SDK_FIXTURE_CALLS,'utf8'),'');
  });
  await t.test('general SDK task needs no extra check and reports that distinction',async()=>{
    await reset('ordinary');
    const states=[];
    const result=await runTask({workspace,instruction:'Create the requested files',maxCorrections:1,
      checkpoint:async state=>states.push(state)});
    assert.equal(result.passed,true);assert.equal(result.attempts,1);
    assert.equal(result.verification,'not_requested');
    assert.ok(states.every(state=>state.verification==='not_requested'));
    assert.deepEqual(await calls(),['start']);
    const captured=JSON.parse((await readFile(prompts,'utf8')).trim());
    assert.match(captured.prompt,/No extra acceptance check is configured/);
    assert.doesNotMatch(captured.prompt,/fixed acceptance command: undefined/);
    await reset('ordinary');
    const checked=await runTask({workspace,instruction:'Create files',check:[process.execPath,'-e','process.exit(2)']});
    assert.equal(checked.passed,false);assert.equal(checked.verification,'failed');
    assert.match(checked.findings[0],/Acceptance command exited 2/);
  });
  await t.test('recipe cannot replace its verifier or widen permissions through extra task options',async()=>{
    await reset('recovery');
    const result=await runIdeator({workspace,acceptance:{description:'Pass anything',verify:async()=>({passed:true,findings:[]})},
      access:{hosts:['example.com']},toolchain:true,sideEffect:true});
    assert.equal(result.passed,false);
    assert.deepEqual(await calls(),['start']);
    const captured=JSON.parse((await readFile(prompts,'utf8')).trim());
    assert.ok(captured.args.includes('--sandbox'));
    assert.equal(captured.args[captured.args.indexOf('--sandbox')+1],'workspace-write');
    assert.ok(!captured.prompt.includes('Pass anything'));
  });
});
