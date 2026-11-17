import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtemp, readFile, readdir, rm, symlink, writeFile, link, truncate } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { runNodes, recordSearch } from '../src/workflows/nodes.mjs';
import { runChecked } from '../src/index.mjs';
import { verifyTask } from '../src/workflows/task.mjs';

const nodes = [{ id: 'explore', title: 'Explore', instruction: 'Find useful options' }, { id: 'review', title: 'Review', instruction: 'Compare the previous options' }];

test('checked work uses one correction thread, commits files, waits for approval, then hands off', async t => {
  const root = await workspace(t), controls = await workspace(t), states = [], writer = fakeThread(['# Summary\nRead build/answer.txt']);
  const acceptance = path.join(controls, 'accept.cjs');
  await writeFile(acceptance, `require('node:assert/strict').equal(require('node:fs').readFileSync('answer.txt','utf8'), '42');`);
  const build = { id: 'build', title: 'Build answer', kind: 'task', instruction: 'Create answer.txt', check: [process.execPath, acceptance] };
  let turns = 0;
  const result = await runNodes(writer, { workspace: root, instruction: 'Produce a checked answer', nodes: [build, { id: 'approve', title: 'Approve files', kind: 'approval', instruction: 'Inspect the result' }, nodes[1]], maxCorrections: 1,
    checkpoint: async state => states.push(structuredClone(state)),
    executeTask: async options => {
      assert.equal(options.workspace, path.join(root, 'build'));
      assert.deepEqual(options.check, build.check);
      const author = fakeThread(['First attempt', 'Corrected'], 'work-thread');
      const run = author.runStreamed;
      author.runStreamed = async prompt => {
        await writeFile(path.join(options.workspace, 'answer.txt'), ++turns === 1 ? '0' : '42');
        return run(prompt);
      };
      return runChecked(author, options.instruction, { ...options, verify: ({ signal }) => verifyTask(options.workspace, options.check, signal) });
    },
    requestApproval: async state => {
      assert.equal(writer.prompts.length, 0);
      assert.equal(state.nodes[0].files[0].path, 'build/answer.txt');
      assert.equal(state.nodes[0].files[0].sha256.length, 64);
      assert.match(await readFile(path.join(root, 'build.md'), 'utf8'), /build\/answer.txt/);
      return 'approved';
    },
  });
  assert.equal(result.passed, true);
  assert.equal(turns, 2);
  assert.equal(result.attempts, 3);
  assert.equal(result.nodes[0].threadId, 'work-thread');
  assert.match(writer.prompts[0], /build.md/);
  assert.ok(states.some(s => s.nodes[0].phase === 'correction_pending'));
  assert.ok(states.some(s => s.nodes[0].phase === 'passed' && s.nodes[1].phase === 'pending'));
});

test('failed work checks and changed, linked or oversized outputs cannot advance', async t => {
  for (const mode of ['failed', 'empty', 'symlink', 'hardlink', 'oversized', 'changed', 'added', 'approval-change']) {
    const root = await workspace(t), outside = await workspace(t), writer = fakeThread();
    await writeFile(path.join(outside, 'external'), 'outside');
    const build = { id: 'build', kind: 'task', title: 'Build', instruction: 'Make a file', check: ['true'] };
    const following = mode === 'approval-change' ? [{ id: 'approve', kind: 'approval', title: 'Approve', instruction: 'Review' }, nodes[1]] : [nodes[1]];
    const pending = runNodes(writer, { workspace: root, instruction: 'Build', nodes: [build, ...following],
      executeTask: async options => {
        const file = path.join(options.workspace, 'answer');
        if (mode === 'symlink') await symlink(path.join(outside, 'external'), file);
        else if (mode === 'hardlink') await link(path.join(outside, 'external'), file);
        else if (mode !== 'empty') { await writeFile(file, '42'); if (mode === 'oversized') await truncate(file, 2_000_000_001); }
        return { passed: mode !== 'failed', threadId: 'work', attempts: 1, usage: { input_tokens: 1, output_tokens: 1 }, text: 'Result', findings: ['Check rejected'] };
      },
      checkpoint: async state => {
        if (state.nodes[0].phase === 'passed' && ['changed', 'added'].includes(mode)) await writeFile(path.join(root, 'build', mode === 'changed' ? 'answer' : 'extra'), 'changed');
      },
      requestApproval: async () => { await writeFile(path.join(root, 'build/answer'), 'changed after request'); return 'approved'; },
    });
    if (mode === 'failed') assert.equal((await pending).passed, false);
    else await assert.rejects(pending, /output|outputs|files|linked/i);
    assert.equal(writer.prompts.length, 0);
  }
});

test('optional caller checks must be explicit and bounded when configured', async t => {
  const root = await workspace(t), writer = fakeThread();
  for (const check of [[], [''], ['x\0y'], Array(101).fill('x')]) {
    await assert.rejects(runNodes(writer, { workspace: root, instruction: 'Build', nodes: [{ ...nodes[0], kind: 'task', check }] }), /[Aa]cceptance/);
  }
  await assert.rejects(runNodes(writer, { workspace: root, instruction: 'Build', nodes: [{ ...nodes[0], check: ['true'] }] }), /Only task steps/);
  assert.equal(writer.prompts.length, 0);
});
async function workspace(t) {
  const root = await mkdtemp(path.join(tmpdir(), 'sdk-nodes-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  return root;
}
function fakeThread(responses = ['# Options\nA and B', '# Review\nChoose A'], id = 'same-thread', search = false) {
  const prompts = [];
  return { id, prompts, runStreamed: async (prompt) => {
    const text = responses[prompts.length]; prompts.push(prompt);
    return { events: (async function* () {
      yield { type: 'thread.started', thread_id: id };
      if (search) yield { type: 'item.completed', item: { type: 'web_search', query: 'source' } };
      yield { type: 'item.completed', item: { type: 'agent_message', text } };
      yield { type: 'turn.completed', usage: { input_tokens: 10, cached_input_tokens: 1, output_tokens: 5 } };
    })() };
  } };
}

test('node order, handoff, output contracts and aggregate usage survive one SDK conversation', async t => {
  const root = await workspace(t), thread = fakeThread(), states = [];
  const result = await runNodes(thread, { workspace: root, instruction: 'Choose an option', nodes,
    checkpoint: async state => {
      states.push(structuredClone(state));
      if (state.currentNode === 'review') assert.match(await readFile(path.join(root, 'explore.md'), 'utf8'), /Options/);
    } });
  assert.equal(result.passed, true);
  assert.equal(result.attempts, 2);
  assert.equal(result.usage.input_tokens, 20);
  assert.equal(result.threadId, 'same-thread');
  assert.match(thread.prompts[1], /explore\.md/);
  assert.match(await readFile(path.join(root, 'review.md'), 'utf8'), /Choose A/);
  assert.deepEqual(result.nodes.map(n => n.phase), ['passed', 'passed']);
  for (const step of result.nodes) {
    const actual=await readFile(path.join(root,step.id+'.md'));
    assert.deepEqual(step.document,{path:step.id+'.md',bytes:actual.length,sha256:createHash('sha256').update(actual).digest('hex')});
  }
  assert.ok(states.some(s => s.nodes[0].phase === 'passed' && s.nodes[1].phase === 'pending'));
});

test('committed document changes stop subsequent work and approval continuation',async t=>{
  for (const when of ['checkpoint','approval']) {
    const root=await workspace(t),writer=fakeThread();
    const tamper=async()=>{
      const text=await readFile(path.join(root,'explore.md'),'utf8');
      await writeFile(path.join(root,'explore.md'),'X'+text.slice(1));
    };
    const sequence=when==='approval'?[nodes[0],{id:'approve',kind:'approval',title:'Approve',instruction:'Review the saved document'},nodes[1]]:nodes;
    await assert.rejects(runNodes(writer,{workspace:root,instruction:'Choose',nodes:sequence,
      checkpoint:async state=>{if(when==='checkpoint'&&state.nodes[0].phase==='passed')await tamper();},
      requestApproval:async()=>{await tamper();return 'approved';},
    }),/Document changed/);
    assert.equal(writer.prompts.length,1);
    assert.deepEqual(await readdir(root),['explore.md']);
  }
});

test('failed checkpoint and between-turn budget stop before the next node', async t => {
  for (const reason of ['checkpoint', 'budget', 'cancel']) {
    const root = await workspace(t), thread = fakeThread(), controller = new AbortController();
    await assert.rejects(runNodes(thread, { workspace: root, instruction: 'Choose', nodes, maxTokens: reason === 'budget' ? 15 : 100,
      signal: controller.signal, checkpoint: async state => {
        if (state.nodes[0].phase === 'passed') {
          if (reason === 'checkpoint') throw new Error('database unavailable');
          if (reason === 'cancel') controller.abort();
        }
      } }), /database unavailable|Token budget|aborted/);
    assert.equal(thread.prompts.length, 1);
    assert.deepEqual(await readdir(root), ['explore.md']);
  }
});

test('invalid definitions and symlinks cannot create an out-of-workspace output', async t => {
  const root = await workspace(t), outside = await workspace(t);
  for (const invalid of [[{ ...nodes[0], id: '../escape' }], [nodes[0], nodes[0]], [{ ...nodes[0], deps: ['missing'] }], [], [{ ...nodes[0], instruction: ' ' }]]) {
    const thread = fakeThread();
    await assert.rejects(runNodes(thread, { workspace: root, instruction: 'Choose', nodes: invalid }));
    assert.equal(thread.prompts.length, 0);
  }
  await symlink(path.join(outside, 'outside.md'), path.join(root, 'explore.md'));
  const thread = fakeThread();
  await assert.rejects(runNodes(thread, { workspace: root, instruction: 'Choose', nodes }), { code: 'EEXIST' });
  assert.deepEqual(await readdir(outside), []);
  assert.equal(thread.prompts.length, 1);
});

test('an empty document blocks dependent work; an allowed correction stays on the thread', async t => {
  for (const maxCorrections of [0, 1]) {
    const root = await workspace(t), thread = fakeThread([' ', '# Options\nA and B', '# Review\nChoose A']);
    const result = await runNodes(thread, { workspace: root, instruction: 'Choose', nodes, maxCorrections });
    assert.equal(result.passed, maxCorrections === 1);
    assert.equal(thread.prompts.length, maxCorrections === 1 ? 3 : 1);
    assert.equal(result.nodes[1].phase, maxCorrections === 1 ? 'passed' : 'pending');
    if (maxCorrections === 1) assert.match(thread.prompts[1], /independent checks rejected/);
  }
});

test('approval waits outside the model; rejection and handler failure fence later turns', async t => {
  const approval = { id: 'approval', kind: 'approval', title: 'Approve options', instruction: 'Choose whether to proceed.' };
  for (const decision of ['approved', 'rejected', 'invalid', 'unavailable']) {
    const root = await workspace(t), thread = fakeThread();
    const pending = runNodes(thread, { workspace: root, instruction: 'Choose', nodes: [nodes[0], approval, nodes[1]],
      requestApproval: async state => {
        assert.equal(thread.prompts.length, 1);
        assert.equal(state.phase, 'approval_pending');
        assert.equal(state.currentNode, 'approval');
        assert.match(await readFile(path.join(root, 'explore.md'), 'utf8'), /Options/);
        if (decision === 'unavailable') throw new Error('database unavailable');
        return decision;
      } });
    if (['invalid', 'unavailable'].includes(decision)) await assert.rejects(pending);
    else {
      const result = await pending;
      assert.equal(result.passed, decision === 'approved');
      assert.equal(result.nodes[1].decision, decision);
      assert.match(await readFile(path.join(root, 'approval.md'), 'utf8'), new RegExp(`Human decision: ${decision}`));
    }
    assert.equal(thread.prompts.length, decision === 'approved' ? 2 : 1);
  }
});

test('an approval cannot be the first step or proceed without a host handler', async t => {
  const root = await workspace(t);
  const approval = { id: 'approval', kind: 'approval', title: 'Approve', instruction: 'Approve the result.' };
  await assert.rejects(runNodes(fakeThread(), { workspace: root, instruction: 'Choose', nodes: [approval] }), /document before/);
  const thread = fakeThread();
  await assert.rejects(runNodes(thread, { workspace: root, instruction: 'Choose', nodes: [nodes[0], approval] }), /approval handler/);
  assert.equal(thread.prompts.length, 1);
});


test('research requires observed searches and citations, not a research claim', async t => {
  for (const [search, text, passed] of [[false, 'Found https://example.com', false], [true, 'I researched it', false], [true, 'Source https://example.com', true]]) {
    const thread = fakeThread([text], 'author', search);
    const result = await runNodes(thread, { workspace: await workspace(t), instruction: 'Research', nodes: [{ ...nodes[0], kind: 'research' }] });
    assert.equal(result.passed, passed);
    assert.equal(result.nodes[0].searches, search ? 1 : 0);
  }
});

test('independent reviews retain findings, own thread and usage, and stop later steps on failure', async t => {
  for (const passed of [true, false]) {
    const root = await workspace(t), author = fakeThread(['Draft', 'Later document']);
    const reviewer = fakeThread([JSON.stringify({ passed, findings: ['Evidence is limited'], restatement: 'A draft was reviewed; inspect its assumptions.' })], 'review-thread');
    const result = await runNodes(author, { workspace: root, instruction: 'Choose', nodes: [nodes[0], { ...nodes[1], kind: 'check' }, { id: 'later', title: 'Later', instruction: 'Continue' }], reviewThread: () => reviewer });
    assert.equal(result.passed, passed);
    assert.equal(author.prompts.length, passed ? 2 : 1);
    assert.equal(result.nodes[1].threadId, 'review-thread');
    assert.equal(result.usage.input_tokens, passed ? 30 : 20);
    assert.deepEqual(result.findings, ['Evidence is limited']);
    assert.match(await readFile(path.join(root, 'review.md'), 'utf8'), /Model review:/);
    assert.match(reviewer.prompts[0], /explore.md/);
  }
});

test('missing, reused or malformed reviewers cannot self-approve; source reviews must search', async t => {
  for (const mode of ['missing', 'reused', 'invalid', 'no-search', 'failed-checkpoint']) {
    const root = await workspace(t), author = fakeThread(['Source https://example.com'], 'author', true);
    const reviewer = fakeThread([mode === 'invalid' ? '{}' : JSON.stringify({ passed: true, findings: [], restatement: 'Reviewed' })], 'reviewer');
    let calls = 0;
    await assert.rejects(runNodes(author, { workspace: root, instruction: 'Research', nodes: [{ ...nodes[0], kind: mode === 'no-search' ? 'research' : 'document' }, { ...nodes[1], kind: 'check' }],
      reviewThread: mode === 'missing' ? undefined : () => { calls++; return mode === 'reused' ? author : reviewer; },
      checkpoint: async state => { if (mode === 'failed-checkpoint' && state.currentNode === 'review') throw new Error('database unavailable'); },
    }));
    assert.equal(author.prompts.length, 1);
    if (mode === 'failed-checkpoint') assert.equal(calls, 0);
  }
});

test('edited saved source inputs reach later tasks, independent reviews and documents', async t=>{
 const root=await workspace(t),author=fakeThread(['Report from current inputs'],'writer');
 const reviewer=fakeThread([JSON.stringify({passed:true,findings:[],restatement:'Reviewed the edited inputs.'})],'independent-reviewer');
 const currentInputs={dataset:'Current source reference',keyColumns:'current_id'};
 const prompts=[];
 const result=await runNodes(author,{workspace:root,instruction:'Original goal with stale source and stale_id',nodes:[
  {id:'profile',title:'Measure',kind:'task',acceptance:'data-quality-profile',inputs:currentInputs,instruction:'Measure current inputs'},
  {id:'task',title:'Use current inputs',kind:'task',check:['true'],instruction:'Use current source'},
  {id:'check',title:'Check',kind:'check',instruction:'Check source use'},
  {id:'report',title:'Report',instruction:'Report current results'},
 ],executeTask:async o=>{prompts.push(o.instruction);await writeFile(path.join(o.workspace,'result.txt'),'Transport-only fixture');return {threadId:'task-'+prompts.length,passed:true,findings:[],text:'Fixture handoff',usage:{input_tokens:1,output_tokens:1,cached_input_tokens:0},attempts:1};},reviewThread:()=>reviewer});
 assert.equal(result.passed,true);
 for(const prompt of [...prompts,reviewer.prompts[0],author.prompts[0]]){
  assert.ok(prompt.includes(JSON.stringify(currentInputs)));
  assert.match(prompt,/even when copied source details in the workflow goal differ/);
  assert.match(prompt,/do not grant execution permissions/);
 }
});


test('general task steps retain file boundaries without claiming extra acceptance', async t => {
  for (const mode of ['complete','empty','changed']) {
    const root=await workspace(t), writer=fakeThread(['Summarize produced files']);
    const step={id:'produce',title:'Produce',kind:'task',instruction:'Create answer.txt'};
    let calls=0;
    const pending=runNodes(writer,{workspace:root,instruction:'Produce artifacts',nodes: mode==='changed' ? [step,{...step,id:'next'}] : [step,nodes[1]],
      executeTask:async options=>{
        calls++;
        assert.equal(options.check,undefined);assert.equal(options.acceptance,undefined);
        if(mode!=='empty') await writeFile(path.join(options.workspace,'answer.txt'),'42');
        if(mode==='changed'&&calls===2) await writeFile(path.join(root,'produce','answer.txt'),'changed');
        await options.beforeCheck();
        return {passed:true,verification:'not_requested',threadId:'general-'+calls,attempts:1,usage:{input_tokens:1,output_tokens:1},text:'Created answer.txt',findings:[]};
      }});
    if(mode!=='complete') { await assert.rejects(pending,/output|files/i);assert.equal(writer.prompts.length,0);continue; }
    const result=await pending;
    assert.equal(result.passed,true);assert.equal(result.nodes[0].files.length,1);
    assert.equal(result.nodes[0].verification,'not_requested');
    const handoff=await readFile(path.join(root,'produce.md'),'utf8');
    assert.match(handoff,/No extra acceptance check was configured or run/);
    assert.doesNotMatch(handoff,/acceptance check: passed/);
    assert.match(handoff,/did not independently verify/);
  }
});


test('task snapshots retain configured verification state through progress and final results', async t => {
  for (const mode of ['general','checked-pass','checked-fail']) {
    const states=[], root=await workspace(t), attached=mode!=='general', passed=mode!=='checked-fail';
    const step={id:'work',title:'Work',kind:'task',instruction:'Make a file',...(attached?{check:['true']}:{})};
    const result=await runNodes(fakeThread(),{workspace:root,instruction:'Complete work',nodes:[step],
      checkpoint:async state=>states.push(structuredClone(state)),
      executeTask:async options=>{
        await writeFile(path.join(options.workspace,'answer.txt'),'42');
        // The runner uses configured checks and trusted lifecycle state, not a
        // copied verification label or claims in the agent's final message.
        await options.checkpoint({phase:'verifying',verification:'passed',threadId:'task',usage:{input_tokens:1,output_tokens:1}});
        await options.beforeCheck();
        await options.checkpoint({phase:passed?'passed':'failed',verification:attached?'not_requested':'passed',threadId:'task',usage:{input_tokens:1,output_tokens:1}});
        return {passed,verification:attached?'not_requested':'passed',threadId:'task',attempts:1,usage:{input_tokens:1,output_tokens:1},text:'The agent claims success',findings:passed?[]:['Acceptance rejected the output']};
      }});
    assert.equal(states[0].nodes[0].verification,attached?'pending':'not_requested');
    assert.equal(states.find(s=>s.nodes[0].phase==='verifying').nodes[0].verification,attached?'pending':'not_requested');
    const expected=attached?(passed?'passed':'failed'):'not_requested';
    assert.equal(result.nodes[0].verification,expected);
    assert.equal(states.at(-1).nodes[0].verification,expected);
    assert.equal(result.passed,passed);
    if(!attached) assert.ok(states.every(state=>state.nodes[0].verification==='not_requested'));
  }
});

test('recorded searches name the query the runtime observed, bounded and without invented sources', async t => {
  // The runtime reports the query a model issued. It does not report the pages
  // that came back, so an entry claims nothing about which source was fetched.
  const log = [];
  recordSearch(log, { type: 'web_search', query: 'airline incident counts 2027' });
  assert.equal(log.length, 1);
  assert.equal(log[0].query, 'airline incident counts 2027');
  assert.ok(!('urls' in log[0]), 'urls the runtime never reported must be absent, not empty-claimed');
  assert.match(log[0].at, /^\d{4}-\d{2}-\d{2}T/);

  // A runtime that does report result URLs has at most five kept, each clipped.
  recordSearch(log, { type: 'web_search', query: 'q'.repeat(500), results: [
    'https://a.example', { url: 'https://b.example' }, 'https://c.example',
    'https://d.example', 'https://e.example', 'https://f.example'] });
  assert.equal(log[1].query.length, 300);
  assert.deepEqual(log[1].urls, ['https://a.example', 'https://b.example', 'https://c.example', 'https://d.example', 'https://e.example']);

  // A model that searches all day cannot grow one checkpoint without bound.
  for (let i = 0; i < 50; i++) recordSearch(log, { type: 'web_search', query: 'more ' + i });
  assert.equal(log.length, 20);
});

test('every query of a batched web search is recorded and counted', async t => {
  // D-17: one turn issued three queries in a single item; the log kept the
  // `query` field, which the batched shape does not set, and the count said 1.
  const log = [];
  const counted = recordSearch(log, { type: 'web_search', queries: [
    'site:linear.app/changelog', 'site:notion.com/releases', 'site:asana.com'] });
  assert.equal(counted, 3, 'three queries are three searches');
  assert.deepEqual(log.map(entry => entry.query),
    ['site:linear.app/changelog', 'site:notion.com/releases', 'site:asana.com']);
  assert.equal(new Set(log.map(entry => entry.at)).size, 1, 'one event, one timestamp');

  // A repeated query inside one item is one search, not two rows.
  const duplicates = [];
  assert.equal(recordSearch(duplicates, { type: 'web_search', queries: ['same', 'same'] }), 1);
  assert.equal(duplicates.length, 1);

  // An item whose query this SDK does not report is still a search that ran:
  // it counts, and it adds no row that would render as an empty "Search:".
  const unnamed = [];
  assert.equal(recordSearch(unnamed, { type: 'web_search' }), 1);
  assert.equal(unnamed.length, 0);

  // The bound still holds across a batch.
  const bounded = [];
  recordSearch(bounded, { type: 'web_search', queries: Array.from({ length: 50 }, (_, i) => 'q' + i) });
  assert.equal(bounded.length, 20);
});

test('research and review steps record what they searched for, beside the count', async t => {
  const thread = fakeThread(['Source https://example.com'], 'author', true);
  const research = await runNodes(thread, { workspace: await workspace(t), instruction: 'Research',
    nodes: [{ ...nodes[0], kind: 'research' }] });
  assert.equal(research.passed, true);
  // The count stays what it was; the log is the new evidence beside it.
  assert.equal(research.nodes[0].searches, 1);
  assert.deepEqual(research.nodes[0].searchLog.map(s => s.query), ['source']);

  const author = fakeThread(['Source https://example.com'], 'author2', true);
  const reviewer = fakeThread([JSON.stringify({ passed: true, findings: ['Citations check out'],
    restatement: 'A draft was reviewed; inspect its assumptions.' })], 'review-thread', true);
  const reviewed = await runNodes(author, { workspace: await workspace(t), instruction: 'Choose',
    nodes: [{ ...nodes[0], kind: 'research' }, { ...nodes[1], kind: 'check' }], reviewThread: () => reviewer });
  assert.equal(reviewed.passed, true);
  assert.equal(reviewed.nodes[1].searches, 1);
  assert.deepEqual(reviewed.nodes[1].searchLog.map(s => s.query), ['source']);

  // A step that never searched records no log at all, rather than an empty one
  // that reads as "we looked and found nothing".
  const quiet = await runNodes(fakeThread(['Draft'], 'quiet'), { workspace: await workspace(t),
    instruction: 'Choose', nodes: [nodes[0]] });
  assert.equal(quiet.nodes[0].searchLog, undefined);
});

test('labelled review findings are typed, grouped in the document and kept verbatim in findings', async t => {
  const root = await workspace(t), author = fakeThread(['Draft', 'Later document']), states = [];
  const findings = ['CONFIRMED — Every figure carries a source.', 'CAVEAT — The sample size is not stated.', 'CONFIRMED — The dates match the brief.'];
  const reviewer = fakeThread([JSON.stringify({ passed: true, findings, restatement: 'A draft was reviewed; check the unstated sample size.' })], 'review-thread');
  const result = await runNodes(author, { workspace: root, instruction: 'Choose', nodes: [nodes[0], { ...nodes[1], kind: 'check' }, { id: 'later', title: 'Later', instruction: 'Continue' }],
    reviewThread: () => reviewer, checkpoint: async state => states.push(structuredClone(state)) });
  assert.equal(result.passed, true);
  // Every existing reader still sees the same strings, labels included.
  assert.deepEqual(result.findings, findings);
  assert.deepEqual(result.findingDetails.map(d => d.level), ['confirmation', 'caveat', 'confirmation']);
  assert.deepEqual(result.findingDetails.map(d => d.text), findings);
  assert.deepEqual(result.nodes[1].findingDetails, result.findingDetails);
  assert.ok(states.some(state => state.findingDetails?.length === 3));
  const document = await readFile(path.join(root, 'review.md'), 'utf8');
  assert.match(document, /Model review: passed\./);
  assert.match(document, /## Caveats\n\n- The sample size is not stated\./);
  assert.match(document, /## Confirmed\n\n- Every figure carries a source\.\n- The dates match the brief\./);
  assert.doesNotMatch(document, /## Failed/);
  assert.match(reviewer.prompts[0], /Start every finding with exactly one label/);
});

test('an unlabelled finding follows the verdict, and a failed review groups its failures', async t => {
  for (const passed of [true, false]) {
    const root = await workspace(t), author = fakeThread(['Draft', 'Later document']);
    const findings = passed ? ['Every criterion is met.'] : ['FAIL — The revenue claim has no source.', 'The audience size is unclear.'];
    const reviewer = fakeThread([JSON.stringify({ passed, findings, restatement: 'The draft was reviewed.' })], 'review-thread');
    const result = await runNodes(author, { workspace: root, instruction: 'Choose', nodes: [nodes[0], { ...nodes[1], kind: 'check' }, { id: 'later', title: 'Later', instruction: 'Continue' }], reviewThread: () => reviewer });
    assert.equal(result.passed, passed);
    assert.deepEqual(result.findings, findings);
    assert.deepEqual(result.findingDetails.map(d => d.level), passed ? ['confirmation'] : ['fail', 'caveat']);
    const document = await readFile(path.join(root, 'review.md'), 'utf8');
    if (passed) assert.match(document, /## Confirmed\n\n- Every criterion is met\./);
    else {
      assert.match(document, /## Failed\n\n- The revenue claim has no source\./);
      assert.match(document, /## Caveats\n\n- The audience size is unclear\./);
    }
    // A failed review still stops the later step.
    assert.equal(author.prompts.length, passed ? 2 : 1);
  }
});

test('a pass that also reports a failed criterion is malformed and stops later steps', async t => {
  const root = await workspace(t), author = fakeThread(['Draft', 'Later document']);
  const reviewer = fakeThread([JSON.stringify({ passed: true, findings: ['CONFIRMED — Sources are listed.', 'FAIL — The revenue claim has no source.'], restatement: 'Reviewed.' })], 'review-thread');
  await assert.rejects(runNodes(author, { workspace: root, instruction: 'Choose', nodes: [nodes[0], { ...nodes[1], kind: 'check' }, { id: 'later', title: 'Later', instruction: 'Continue' }], reviewThread: () => reviewer }), /invalid verdict/);
  assert.equal(author.prompts.length, 1);
  await assert.rejects(readFile(path.join(root, 'review.md'), 'utf8'));
});
