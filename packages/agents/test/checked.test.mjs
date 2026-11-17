import test from 'node:test';
import assert from 'node:assert/strict';
import { runChecked } from '../src/index.mjs';

function fakeThread(turns) {
  const prompts = [];
  return {
    id: 'same-thread', prompts,
    async runStreamed(input) {
      prompts.push(input);
      return { events: (async function* () { yield* turns.shift(); })() };
    },
  };
}
const completed = (text = 'done') => [
  { type: 'thread.started', thread_id: 'same-thread' },
  { type: 'item.completed', item: { type: 'agent_message', text } },
  { type: 'turn.completed', usage: { input_tokens: 10, cached_input_tokens: 2, output_tokens: 3 } },
];

test('failed check corrects on the same SDK thread with total usage and checkpoints', async () => {
  const thread = fakeThread([completed('one idea'), completed('three ideas')]);
  const checkpoints = [];
  const result = await runChecked(thread, 'Make ideas', {
    maxCorrections: 1,
    verify: async ({ attempt }) => ({ passed: attempt === 1, findings: attempt ? [] : ['Need three ideas'] }),
    checkpoint: async (state) => checkpoints.push(state),
  });
  assert.equal(result.passed, true);
  assert.equal(result.attempts, 2);
  assert.deepEqual(result.usage, { input_tokens: 20, cached_input_tokens: 4, output_tokens: 6 });
  assert.match(thread.prompts[1], /Need three ideas/);
  assert.equal(checkpoints.at(-1).phase, 'passed');
  assert.deepEqual(checkpoints.find((state) => state.phase === 'correction_pending').findings, ['Need three ideas']);
});

test('failed transport, missing completion and failing verifier cannot report success or trigger repair', async () => {
  for (const events of [[{ type: 'turn.failed', error: { message: 'offline' } }], []]) {
    const thread = fakeThread([events]);
    let checked = false;
    await assert.rejects(runChecked(thread, 'work', { maxCorrections: 1, verify: async () => { checked = true; } }));
    assert.equal(checked, false);
    assert.equal(thread.prompts.length, 1);
  }
  const thread = fakeThread([completed()]);
  await assert.rejects(runChecked(thread, 'work', { maxCorrections: 1, verify: async () => { throw Error('disk unavailable'); } }), /disk unavailable/);
  assert.equal(thread.prompts.length, 1);
});

test('invalid verdict fails closed; exhausted budget and correction allowance stop', async () => {
  for (const verdict of [{ passed: true, findings: ['bad'] }, { passed: false, findings: [] }, null]) {
    await assert.rejects(runChecked(fakeThread([completed()]), 'work', { verify: async () => verdict }), /invalid verdict/);
  }
  const thread = fakeThread([completed()]);
  const result = await runChecked(thread, 'work', { maxCorrections: 2, maxTokens: 13, verify: async () => ({ passed: false, findings: ['bad'] }) });
  assert.equal(result.passed, false);
  assert.equal(thread.prompts.length, 1);
});

test('cancellation and failed checkpoint stop before more work', async () => {
  const controller = new AbortController();
  const thread = fakeThread([completed()]);
  await assert.rejects(runChecked(thread, 'work', {
    signal: controller.signal,
    verify: async () => { controller.abort(); return { passed: true, findings: [] }; },
  }));
  const second = fakeThread([completed()]);
  await assert.rejects(runChecked(second, 'work', { checkpoint: async () => { throw Error('state write failed'); }, verify: async () => ({ passed: true, findings: [] }) }), /state write failed/);
});

test('duplicate completion cannot spend another correction or run acceptance', async () => {
  const thread = fakeThread([[...completed(), ...completed()]]);
  let verified = false;
  await assert.rejects(runChecked(thread, 'work', {
    maxCorrections: 1,
    verify: async () => { verified = true; return { passed: true, findings: [] }; },
  }), /completed more than once/);
  assert.equal(verified, false);
  assert.equal(thread.prompts.length, 1);
});
