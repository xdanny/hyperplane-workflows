import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { runNodes, validateNodes } from '../src/workflows/nodes.mjs';
import { dependencyGraph } from '../src/dependencies.mjs';
import { inspectContinuation, definitionFingerprint } from '../src/continuation.mjs';

const node = (id, dependsOn, kind = 'document') => ({ id, title: id, instruction: `Do ${id}`, kind, ...(dependsOn === undefined ? {} : { dependsOn }) });
const deferred = () => Promise.withResolvers();
async function workspace(t) {
  const folder = await mkdtemp(path.join(tmpdir(), 'sdk-dependencies-'));
  t.after(() => rm(folder, { recursive: true, force: true }));
  return folder;
}
function writer(id, { start = async () => {}, finish = async () => {}, text = `# ${id}`, search = false } = {}) {
  const prompts = [];
  return { id, prompts, async runStreamed(prompt, { signal }) {
    prompts.push(prompt);
    return { events: (async function* () {
      await start(signal);
      yield { type: 'thread.started', thread_id: id };
      await finish(signal);
      if (search) yield { type: 'item.completed', item: { type: 'web_search' } };
      yield { type: 'item.completed', item: { type: 'agent_message', text: typeof text === 'function' ? text(prompts.length) : text } };
      yield { type: 'turn.completed', usage: { input_tokens: 2, cached_input_tokens: 1, output_tokens: 1 } };
    })() };
  } };
}

test('dependencies preserve omission, explicit roots, forward edges and reject malformed graphs before execution', async t => {
  const nodes = [node('join', ['left', 'right']), node('left', []), node('right', [])];
  validateNodes('Join', nodes);
  assert.deepEqual(dependencyGraph([node('a', null), node('b', null)]).dependencies, [[], [0]]);
  assert.equal(dependencyGraph([node('a', null)]).explicit, false);
  assert.deepEqual(dependencyGraph(nodes).previous(0), [1, 2]);
  assert.deepEqual(dependencyGraph([node('a'), node('b'), node('c', [])]).dependencies, [[], [0], []]);
  for (const [nodes, expected] of [
    [[node('a', ['missing'])], /Unknown dependency/],
    [[node('a', ['a'])], /itself/],
    [[node('a', ['b']), node('b', ['a'])], /cycle/],
    [[node('a', []), node('b', ['a', 'a'])], /distinct/],
    [[node('a', 'b')], /list/],
    [[node('a', []), node('b', [], 'check')], /document before/],
  ]) {
    let calls = 0;
    await assert.rejects(runNodes({}, { workspace: await workspace(t), instruction: 'Validate', nodes, authorThread: () => { calls++; } }), expected);
    assert.equal(calls, 0);
  }
});

test('parallel SDK starts overlap, fan-in waits for both artifacts, contexts exclude siblings and receipts aggregate monotonically', { timeout: 5000 }, async t => {
  const root = await workspace(t), bothStarted = deferred(), release = deferred(), starts = [], states = [], threads = new Map();
  let calls = 0, callbacks = 0;
  const nodes = [node('join', ['left', 'right']), node('left', []), node('right', [])];
  const pending = runNodes({}, { workspace: root, instruction: 'Join both branches', nodes,
    authorThread: (step, previous, options) => {
      assert.equal(options.fresh, true);
      const thread = writer(step.id, {
        start: async () => { starts.push(step.id); if (++calls === 2) bothStarted.resolve(); },
        finish: async () => {
          if (step.id !== 'join') await release.promise;
          else for (const id of ['left', 'right']) assert.match(await readFile(path.join(root, id + '.md'), 'utf8'), new RegExp(id));
        },
      });
      threads.set(step.id, thread);
      return thread;
    },
    checkpoint: async state => {
      assert.equal(++callbacks, 1, 'host checkpoint calls cannot overlap');
      await new Promise(resolve => setTimeout(resolve, 1));
      states.push(structuredClone(state));
      callbacks--;
    },
  });
  await bothStarted.promise;
  assert.deepEqual(new Set(starts), new Set(['left', 'right']));
  assert.deepEqual(await readdir(root), []);
  release.resolve();
  const result = await pending;
  assert.deepEqual(starts, ['left', 'right', 'join']);
  assert.equal(result.passed, true);
  assert.equal(result.text, '# join');
  assert.deepEqual(result.usage, { input_tokens: 6, cached_input_tokens: 3, output_tokens: 3 });
  assert.equal(result.attempts, 3);
  assert.match(threads.get('join').prompts[0], /\["left.md","right.md"\]/);
  assert.match(threads.get('left').prompts[0], /Previous step documents in this folder: \[\]/);
  assert.doesNotMatch(threads.get('right').prompts[0], /left.md/);
  for (let index = 1; index < states.length; index++) {
    for (const key of ['input_tokens', 'output_tokens', 'cached_input_tokens']) assert.ok(states[index].usage[key] >= states[index - 1].usage[key]);
    assert.ok(states[index].attempts >= states[index - 1].attempts);
  }
});

test('read-only waves are capped at two and task plus approval run exclusively', { timeout: 5000 }, async t => {
  const root = await workspace(t), release = deferred(), started = deferred(), order = [];
  let active = 0, maxActive = 0, count = 0;
  const nodes = [node('a', []), node('b', []), node('c', []), node('task', [], 'task'), node('approve', ['a'], 'approval')];
  const pending = runNodes({}, { workspace: root, instruction: 'Bound work', nodes,
    authorThread: step => writer(step.id, {
      start: async () => { order.push(step.id); maxActive = Math.max(maxActive, ++active); if (++count === 2) started.resolve(); },
      finish: async () => { await release.promise; active--; },
    }),
    executeTask: async options => {
      assert.equal(active, 0); order.push('task');
      await writeFile(path.join(options.workspace, 'result'), 'done');
      return { passed: true, attempts: 1, usage: { input_tokens: 2, output_tokens: 1 }, text: 'done', threadId: 'task-thread' };
    },
    requestApproval: async state => {
      assert.equal(active, 0); order.push('approve');
      assert.ok(state.nodes.filter(step => ['a', 'b', 'c'].includes(step.id)).every(step => step.phase === 'passed'));
      return 'approved';
    },
  });
  await started.promise;
  assert.deepEqual(order, ['a', 'b']);
  release.resolve();
  assert.equal((await pending).passed, true);
  assert.equal(maxActive, 2);
  assert.deepEqual(order, ['a', 'b', 'c', 'task', 'approve']);
});

test('failure aborts and drains its peer before committing the failed aggregate; no dependent is started', { timeout: 5000 }, async t => {
  const both = deferred(), root = await workspace(t), states = [];
  let started = 0, drained = false;
  const pending = runNodes({}, { workspace: root, instruction: 'Stop on failure', nodes: [node('bad', []), node('slow', []), node('later', ['bad', 'slow'])],
    authorThread: step => writer(step.id, {
      text: '',
      start: async () => { assert.notEqual(step.id, 'later'); if (++started === 2) both.resolve(); },
      finish: async signal => {
        await both.promise;
        if (step.id === 'slow') {
          await new Promise(resolve => { if (signal.aborted) resolve(); else signal.addEventListener('abort', resolve, { once: true }); });
          await new Promise(resolve => setTimeout(resolve, 5));
          drained = true;
          signal.throwIfAborted();
        }
      },
    }),
    checkpoint: async state => { if (state.phase === 'failed') assert.equal(drained, true); states.push(state); },
  });
  const result = await pending;
  assert.equal(result.passed, false);
  assert.equal(drained, true);
  assert.equal(started, 2);
  assert.equal(result.attempts, 2);
  assert.equal(result.usage.input_tokens, 2);
  assert.equal(result.nodes[2].phase, 'pending');
  assert.equal(states.at(-1).phase, 'failed');
});

test('external cancellation drains every active SDK branch', { timeout: 5000 }, async t => {
  const controller = new AbortController(), started = deferred();
  let count = 0, drained = 0;
  const pending = runNodes({}, { workspace: await workspace(t), instruction: 'Cancel', nodes: [node('a', []), node('b', [])], signal: controller.signal,
    authorThread: step => writer(step.id, {
      start: async () => { if (++count === 2) started.resolve(); },
      finish: async signal => {
        await new Promise(resolve => { if (signal.aborted) resolve(); else signal.addEventListener('abort', resolve, { once: true }); });
        drained++;
        signal.throwIfAborted();
      },
    }),
  });
  // Wait for both thread-start checkpoints, then cancel within their turns.
  await started.promise;
  await new Promise(resolve => setTimeout(resolve, 5));
  controller.abort(new Error('Stop now'));
  await assert.rejects(pending, /Stop now/);
  assert.equal(drained, 2);
});

test('shared author factories cannot leak sibling conversation history', async t => {
  const shared = writer('shared');
  await assert.rejects(runNodes({}, { workspace: await workspace(t), instruction: 'Separate', nodes: [node('a', []), node('b', [])], authorThread: () => shared }), /fresh SDK conversation/);
});

test('a narrow shared budget admits only positive-budget turns and fences later waves', async t => {
  const calls = [];
  await assert.rejects(runNodes({}, { workspace: await workspace(t), instruction: 'Budget', maxTokens: 1, nodes: [node('a', []), node('b', [])],
    authorThread: step => { calls.push(step.id); return writer(step.id); },
  }), /Token budget reached/);
  assert.deepEqual(calls, ['a']);
});

test('DAG continuation is rejected before source inspection or another model turn', async t => {
  const nodes = [node('a', []), node('b', [])];
  assert.notEqual(definitionFingerprint({ instruction: 'Goal', nodes }), definitionFingerprint({ instruction: 'Goal', nodes: nodes.map(({ dependsOn, ...step }) => step) }));
  await assert.rejects(inspectContinuation({ instruction: 'Goal', nodes, sourceWorkspace: '/missing/source', checkpoint: {} }), /explicit dependencies cannot be continued/);
});

test('check prerequisites and approvals must follow the selected causal lineage', () => {
  const profile = id => ({ ...node(id, [], 'task'), acceptance: 'data-quality-profile', inputs: { dataset: 'test.csv', keyColumns: 'id' } });
  const rules = (id, deps) => ({ ...node(id, deps, 'task'), acceptance: 'data-quality-rules' });
  const audit = deps => ({ ...node('audit', deps, 'task'), acceptance: 'data-quality-audit' });
  const valid = [profile('profile'), rules('plan', ['profile']), node('approve', ['plan'], 'approval'), audit(['approve'])];
  validateNodes('Audit', valid);
  assert.throws(() => validateNodes('Audit', [profile('profile'), rules('plan', ['profile']), node('approve', ['profile'], 'approval'), audit(['plan', 'approve'])]), /Approve/);
  assert.throws(() => validateNodes('Audit', [profile('profile'), profile('other'), rules('plan', ['profile', 'other'])]), /Ambiguous prerequisite/);
  assert.throws(() => validateNodes('Audit', [profile('profile'), rules('plan', ['profile']), profile('new-profile'), node('approve', ['plan'], 'approval'), audit(['approve', 'new-profile'])]), /Ambiguous prerequisite/);
});

test('parallel research and independent reviews use only ancestor documents and the matching search permission', async t => {
  const root = await workspace(t), reviews = new Map(), authors = new Map();
  const result = await runNodes({}, { workspace: root, instruction: 'Review both branches', nodes: [
    node('research', [], 'research'), node('write', []), node('sources', ['research'], 'check'), node('quality', ['write'], 'check'),
  ],
  authorThread: step => {
    const thread = writer(step.id, { search: step.kind === 'research', text: '# Findings https://example.com/evidence' });
    authors.set(step.id, thread);
    return thread;
  },
  reviewThread: ({ node: step, webSearch }) => {
    assert.equal(webSearch, step.id === 'sources');
    const thread = writer(step.id, { search: webSearch, text: JSON.stringify({ passed: true, findings: [], restatement: 'The evidence meets the criteria.' }) });
    reviews.set(step.id, thread);
    return thread;
  },
  });
  assert.equal(result.passed, true);
  assert.equal(result.attempts, 4);
  assert.equal(result.usage.input_tokens, 8);
  assert.match(reviews.get('sources').prompts[0], /Documents: \["research.md"\]/);
  assert.doesNotMatch(reviews.get('sources').prompts[0], /write.md/);
  assert.match(reviews.get('quality').prompts[0], /Documents: \["write.md"\]/);
  assert.doesNotMatch(reviews.get('quality').prompts[0], /research.md/);
  assert.equal(new Set(result.nodes.map(step => step.threadId)).size, 4);
});

test('parallel corrections stay in their own conversation and cumulative accounting never double-counts a completed sibling', async t => {
  const threads = new Map(), states = [];
  const result = await runNodes({}, { workspace: await workspace(t), instruction: 'Correct', nodes: [node('a', []), node('b', [])], maxCorrections: 1, maxTokens: 12,
    authorThread: step => {
      const thread = writer(step.id, { text: count => step.id === 'a' && count === 1 ? '' : '# Result' });
      threads.set(step.id, thread);
      return thread;
    },
    checkpoint: async state => states.push(state),
  });
  assert.equal(result.passed, true);
  assert.equal(threads.get('a').prompts.length, 2);
  assert.equal(threads.get('b').prompts.length, 1);
  assert.match(threads.get('a').prompts[1], /Correct the artifacts/);
  assert.equal(result.attempts, 3);
  assert.deepEqual(result.usage, { input_tokens: 6, cached_input_tokens: 3, output_tokens: 3 });
  assert.ok(states.every(state => state.attempts <= 3 && state.usage.input_tokens <= 6));
});

test('a wave splits its remaining correction budget between admitted branches', async t => {
  const threads = [];
  const result = await runNodes({}, { workspace: await workspace(t), instruction: 'Bound corrections', nodes: [node('a', []), node('b', [])], maxCorrections: 5, maxTokens: 6,
    authorThread: step => { const thread = writer(step.id, { text: '' }); threads.push(thread); return thread; },
  });
  assert.equal(result.passed, false);
  assert.equal(threads.length, 2);
  assert.ok(threads.every(thread => thread.prompts.length === 1));
});

test('tampering with an independent committed output fences later task execution', async t => {
  const root = await workspace(t);
  let taskCalls = 0, tampered = false;
  await assert.rejects(runNodes({}, { workspace: root, instruction: 'Keep outputs pinned', nodes: [node('a', []), node('b', []), node('task', ['a'], 'task')],
    authorThread: step => writer(step.id),
    checkpoint: async state => {
      if (!tampered && state.nodes.slice(0, 2).every(step => step.phase === 'passed')) {
        tampered = true;
        await writeFile(path.join(root, 'b.md'), 'changed');
      }
    },
    executeTask: async () => { taskCalls++; },
  }), /changed|bound/);
  assert.equal(tampered, true);
  assert.equal(taskCalls, 0);
});

test('a rejected concurrent checkpoint closes the write queue and drains peers before returning', { timeout: 5000 }, async t => {
  let drained = 0, rejected = false, afterRejection = 0, taskCalls = 0;
  const threads = [];
  await assert.rejects(runNodes({}, { workspace: await workspace(t), instruction: 'Commit before advancing', nodes: [node('a', []), node('b', []), node('task', ['a', 'b'], 'task')], maxCorrections: 1,
    authorThread: step => {
      const thread = writer(step.id, { finish: async signal => {
        await new Promise(resolve => { if (signal.aborted) resolve(); else signal.addEventListener('abort', resolve, { once: true }); });
        signal.throwIfAborted();
      } });
      const run = thread.runStreamed;
      thread.runStreamed = async (...args) => {
        const { events } = await run(...args);
        return { events: (async function* () { try { yield* events; } finally { drained++; } })() };
      };
      threads.push(thread);
      return thread;
    },
    checkpoint: async state => {
      if (rejected) afterRejection++;
      if (state.attempts === 2) { rejected = true; throw new Error('Checkpoint storage unavailable'); }
    },
    executeTask: async () => { taskCalls++; },
  }), /Checkpoint storage unavailable/);
  assert.equal(rejected, true);
  assert.equal(drained, 2);
  assert.equal(afterRejection, 0);
  assert.equal(taskCalls, 0);
  assert.equal(threads.length, 2);
  assert.ok(threads.every(thread => thread.prompts.length === 1));
});
