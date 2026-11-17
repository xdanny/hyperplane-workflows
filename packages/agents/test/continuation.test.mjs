import test from 'node:test';
import assert from 'node:assert/strict';
import { chmod, lstat, mkdtemp, mkdir, readdir, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { inspectContinuation, prepareContinuation, definitionFingerprint } from '@hyperplane/agents/continuation';
import { runNodes } from '@hyperplane/agents/nodes';
import { runtimeFingerprint, runtimeFingerprintVersion } from '@hyperplane/agents/runtime';
import { inventoryFiles } from '../src/workflows/files.mjs';

const definition = { instruction: 'Prepare and review useful files', model: 'gpt-6-astra', maxTokens: 100, maxCorrections: 0,
  nodes: [
    { id: 'draft', title: 'Draft', instruction: 'Write the plan' },
    { id: 'approve', kind: 'approval', title: 'Approve', instruction: 'Approve the plan' },
    { id: 'build', kind: 'task', title: 'Build', instruction: 'Write the output' },
    { id: 'review', kind: 'approval', title: 'Review', instruction: 'Review the output' },
    { id: 'finish', title: 'Finish', instruction: 'Write the conclusion' },
  ] };
let serial = 0;
function thread(calls) {
  return { id: null, async runStreamed(input) {
    this.id = 'continuation-fixture-' + ++serial;
    const id = this.id;
    return { events: (async function* () {
      calls.push(input);
      yield { type: 'thread.started', thread_id: id };
      yield { type: 'item.completed', item: { type: 'agent_message', text: 'Useful document' } };
      yield { type: 'turn.completed', usage: { input_tokens: 2, cached_input_tokens: 1, output_tokens: 3 } };
    })() };
  } };
}
async function fixture(t) {
  const root = await mkdtemp(path.join(os.tmpdir(), 'sdk-continuation-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const sourceWorkspace = path.join(root, 'source'); await mkdir(sourceWorkspace);
  let checkpoint;
  await assert.rejects(runNodes(thread([]), { ...definition, workspace: sourceWorkspace,
    requestApproval: async () => 'approved',
    executeTask: async options => {
      await writeFile(path.join(options.workspace, 'result.csv'), 'value\n42\n');
      const result = { passed: true, attempts: 1, threadId: 'task-fixture', text: 'Created result.csv', usage: { input_tokens: 7, cached_input_tokens: 2, output_tokens: 4 } };
      await options.checkpoint({ ...result, phase: 'passed' });
      return result;
    },
    checkpoint: async state => {
      if (state.currentNode === 'review' && state.nodes[3].phase === 'passed') {
        checkpoint = structuredClone({ ...state, phase: 'interrupted' });
        throw new Error('Fixture interruption after committed prefix');
      }
    },
  }), /Fixture interruption/);
  assert.equal(checkpoint.definitionFingerprint, definitionFingerprint(definition));
  assert.equal(checkpoint.runtimeFingerprint, await runtimeFingerprint());
  assert.equal(checkpoint.runtimeFingerprintVersion, runtimeFingerprintVersion);
  await writeFile(path.join(sourceWorkspace, 'finish.md'), 'Uncommitted partial result');
  const fresh = async () => { const workspace = path.join(root, 'target-' + ++serial); await mkdir(workspace); return workspace; };
  return { root, sourceWorkspace, checkpoint, fresh };
}

test('continuation copies only committed files, renews every approval and carries recorded usage', async t => {
  const f = await fixture(t), workspace = await f.fresh();
  const args = { ...definition, ...f, workspace };
  const plan = await prepareContinuation(args);
  assert.deepEqual(plan.reused.map(step => step.id), ['draft', 'build']);
  assert.deepEqual(plan.renewApprovals.map(step => step.id), ['approve', 'review']);
  assert.equal(plan.restart.id, 'finish');
  assert.deepEqual((await readdir(workspace)).sort(), ['build', 'build.md', 'draft.md']);
  assert.equal(await readFile(path.join(workspace, 'build/result.csv'), 'utf8'), 'value\n42\n');
  assert.equal(await readFile(path.join(f.sourceWorkspace, 'finish.md'), 'utf8'), 'Uncommitted partial result');
  const calls = [], approvals = [], checkpoints = [];
  const result = await runNodes(thread(calls), { ...definition, workspace, continuation: { checkpoint: plan.checkpoint },
    requestApproval: async state => { approvals.push(state.currentNode); return 'approved'; },
    executeTask: async () => { throw new Error('Reused task must never execute'); },
    checkpoint: async state => checkpoints.push(structuredClone(state)),
  });
  assert.deepEqual(approvals, ['approve', 'review']);
  assert.equal(calls.length, 1);
  assert.equal(result.passed, true);
  assert.equal(result.attempts, plan.attempts + 1);
  assert.equal(result.usage.input_tokens, plan.usage.input_tokens + 2);
  assert.equal(result.usage.output_tokens, plan.usage.output_tokens + 3);
  for (const saved of checkpoints) assert.equal(saved.definitionFingerprint, plan.definitionFingerprint);
});

test('completed task scripts retain only owner execution permission in independent copies', async t => {
  const f = await fixture(t);
  const script = '#!/bin/sh\nprintf "copied check passed\\n"\n';
  const original = path.join(f.sourceWorkspace, 'build/checks.sh');
  await writeFile(original, script, { mode: 0o700 });
  f.checkpoint.nodes[2].files = await inventoryFiles(f.sourceWorkspace, 'build');
  // Content pins intentionally do not record permissions. Copy the observed
  // owner execute bit while stripping special and group/other permissions.
  for (const sourceMode of [0o700, 0o7777, 0o644]) {
    await chmod(original, sourceMode);
    const workspace = await f.fresh();
    await prepareContinuation({ ...definition, ...f, workspace });
    const copied = path.join(workspace, 'build/checks.sh');
    const [sourceInfo, targetInfo] = await Promise.all([lstat(original), lstat(copied)]);
    assert.equal(targetInfo.mode & 0o7777, sourceMode & 0o100 ? 0o700 : 0o600);
    assert.equal(targetInfo.nlink, 1);
    assert.notEqual(targetInfo.ino, sourceInfo.ino);
    assert.equal(await readFile(copied, 'utf8'), script);
    assert.deepEqual((await readdir(workspace)).sort(), ['build', 'build.md', 'draft.md']);
    assert.deepEqual((await readdir(path.join(workspace, 'build'))).sort(), ['checks.sh', 'result.csv']);
    assert.equal((await lstat(path.join(workspace, 'build.md'))).mode & 0o7777, 0o600);
    if (sourceMode & 0o100) {
      // Source permissions can change independently after preparation.
      await chmod(original, 0o600);
      const child = spawn(copied, [], { cwd: path.join(workspace, 'build') });
      let stdout = '', stderr = '';
      child.stdout.on('data', data => { stdout += data; });
      child.stderr.on('data', data => { stderr += data; });
      const code = await new Promise((resolve, reject) => { child.once('error', reject); child.once('close', resolve); });
      assert.equal(code, 0, stderr);
      assert.equal(stdout, 'copied check passed\n');
    }
  }
});

test('prepared seed and interruption during approval renewal can themselves continue', async t => {
  const f = await fixture(t), workspace = await f.fresh();
  const plan = await prepareContinuation({ ...definition, ...f, workspace });
  const prepared = await inspectContinuation({ ...definition, checkpoint: plan.checkpoint, sourceWorkspace: workspace });
  assert.equal(prepared.reused.length, 2);
  let interrupted;
  await assert.rejects(runNodes(thread([]), { ...definition, workspace, continuation: { checkpoint: plan.checkpoint },
    requestApproval: async state => { interrupted = structuredClone({ ...state, phase: 'interrupted' }); throw new Error('Renewal interrupted'); },
  }), /Renewal interrupted/);
  const next = await prepareContinuation({ ...definition, checkpoint: interrupted, sourceWorkspace: workspace, workspace: await f.fresh() });
  assert.deepEqual(next.reused.map(step => step.id), ['draft', 'build']);
  assert.equal(next.checkpoint.reuseThrough, 4);
});

test('changed definitions, runtime, missing pins, disjoint progress and tampered files reject before model work', async t => {
  const f = await fixture(t), args = { ...definition, ...f };
  for (const change of [
    { instruction: 'Different goal' }, { model: 'other-model' }, { maxTokens: 101 }, { maxCorrections: 1 },
    { checkpoint: { ...f.checkpoint, runtimeFingerprint: '0'.repeat(64) } },
    { checkpoint: { ...f.checkpoint, phase: 'failed' } },
    { checkpoint: { ...f.checkpoint, definitionFingerprint: undefined } },
  ]) await assert.rejects(inspectContinuation({ ...args, ...change }));
  const missing = structuredClone(f.checkpoint); delete missing.nodes[0].document;
  await assert.rejects(inspectContinuation({ ...args, checkpoint: missing }), /pinned document/);
  const gap = structuredClone(f.checkpoint); gap.nodes[0].phase = 'pending';
  await assert.rejects(inspectContinuation({ ...args, checkpoint: gap }), /contiguous/);
  const workspace = await f.fresh(); await prepareContinuation({ ...args, workspace });
  await writeFile(path.join(workspace, 'build/result.csv'), 'changed');
  let called = false;
  await assert.rejects(runNodes(thread([]), { ...definition, workspace, continuation: { checkpoint: f.checkpoint }, requestApproval: async () => { called = true; return 'approved'; } }), /changed/);
  assert.equal(called, false);
  await writeFile(path.join(f.sourceWorkspace, 'draft.md'), 'tampered');
  await assert.rejects(prepareContinuation({ ...args, workspace: await f.fresh() }), /changed/);
});

test('interrupted effectful tasks are blocked, future pending tasks remain allowed and spent budget is not reset', async t => {
  const f = await fixture(t);
  for (const setting of [{ check: ['true'] }, { access: { hosts: ['example.test'] } }, { access: { hosts: ['example.test'] }, toolchain: true, sideEffect: true }]) {
    const nodes = structuredClone(definition.nodes); Object.assign(nodes[2], setting);
    const checkpoint = structuredClone(f.checkpoint); checkpoint.definitionFingerprint = definitionFingerprint({ ...definition, nodes });
    checkpoint.nodes[2] = { id: 'build', title: 'Build', phase: 'running' };
    checkpoint.nodes[3] = { id: 'review', title: 'Review', phase: 'pending' };
    await assert.rejects(inspectContinuation({ ...definition, nodes, checkpoint, sourceWorkspace: f.sourceWorkspace }), /external effects/);
    checkpoint.nodes[2].phase = 'pending';
    assert.equal((await inspectContinuation({ ...definition, nodes, checkpoint, sourceWorkspace: f.sourceWorkspace })).restart.id, 'build');
  }
  const checkpoint = structuredClone(f.checkpoint); checkpoint.usage.input_tokens = definition.maxTokens;
  const workspace = await f.fresh(); const plan = await prepareContinuation({ ...definition, checkpoint, sourceWorkspace: f.sourceWorkspace, workspace });
  const calls = [];
  await assert.rejects(runNodes(thread(calls), { ...definition, workspace, continuation: { checkpoint: plan.checkpoint }, requestApproval: async () => 'approved' }), /Token budget reached/);
  assert.equal(calls.length, 0);
});

test('copy cancellation removes partial destination files and preserves the source', { timeout: 10000 }, async t => {
  const f = await fixture(t), workspace = await f.fresh();
  await writeFile(path.join(f.sourceWorkspace, 'build/large.bin'), Buffer.alloc(8_000_000, 42));
  f.checkpoint.nodes[2].files = await inventoryFiles(f.sourceWorkspace, 'build');
  const controller = new AbortController();
  let observedCopy = false;
  const timer = setInterval(async () => {
    const info = await lstat(path.join(workspace, 'build/large.bin')).catch(() => undefined);
    if (info?.size > 0) { observedCopy = true; controller.abort(new Error('Stop copy')); }
  }, 1);
  try {
    await assert.rejects(prepareContinuation({ ...definition, ...f, workspace, signal: controller.signal }), /Stop copy/);
  } finally { clearInterval(timer); }
  assert.equal(observedCopy, true, 'Cancellation must interrupt a real copy');
  assert.deepEqual(await readdir(workspace), []);
  assert.equal((await lstat(path.join(f.sourceWorkspace, 'build/large.bin'))).size, 8_000_000);
  assert.equal(await readFile(path.join(f.sourceWorkspace, 'build/result.csv'), 'utf8'), 'value\n42\n');
});

test('all-passed interrupted receipt renews approvals and finishes without another model call', async t => {
  const f = await fixture(t), workspace = await f.fresh();
  const prepared = await prepareContinuation({ ...definition, ...f, workspace });
  const finished = await runNodes(thread([]), { ...definition, workspace, continuation: { checkpoint: prepared.checkpoint }, requestApproval: async () => 'approved' });
  const checkpoint = { ...finished, phase: 'interrupted' };
  const nextWorkspace = await f.fresh();
  const plan = await prepareContinuation({ ...definition, checkpoint, sourceWorkspace: workspace, workspace: nextWorkspace });
  assert.equal(plan.restart, null);
  const calls = [], approvals = [];
  const result = await runNodes(thread(calls), { ...definition, workspace: nextWorkspace, continuation: { checkpoint: plan.checkpoint }, requestApproval: async state => { approvals.push(state.currentNode); return 'approved'; } });
  assert.equal(result.passed, true);
  assert.deepEqual(calls, []);
  assert.deepEqual(approvals, ['approve', 'review']);
  assert.deepEqual(result.usage, finished.usage);
  assert.equal(result.attempts, finished.attempts);
});

test('continuation worker uses the same bounded fingerprint protocol without SDK execution', async t => {
  const f = await fixture(t);
  const worker = fileURLToPath(new URL('../src/continuation-worker.mjs', import.meta.url));
  async function invoke(input) {
    const child = spawn(process.execPath, [worker]); let stdout = '', stderr = '';
    child.stdout.on('data', data => { stdout += data; }); child.stderr.on('data', data => { stderr += data; }); child.stdin.on('error', () => {});
    const done = new Promise((resolve, reject) => { child.once('error', reject); child.once('close', resolve); });
    child.stdin.end(typeof input === 'string' ? input : JSON.stringify(input));
    return { code: await done, stdout, stderr };
  }
  const input = { operation: 'inspect', request: { instruction: definition.instruction, nodes: definition.nodes, model: definition.model, maxTokens: definition.maxTokens, autoRepair: false }, checkpoint: f.checkpoint, sourceWorkspace: f.sourceWorkspace, runtimeFingerprint: await runtimeFingerprint() };
  const result = await invoke(input); assert.equal(result.code, 0, result.stderr); assert.equal(JSON.parse(result.stdout).plan.restart.id, 'finish');
  const blocked = await invoke({ ...input, checkpoint: { ...f.checkpoint, phase: 'passed' } }); assert.equal(blocked.code, 0); assert.equal(JSON.parse(blocked.stdout).ok, false);
  for (const bad of [{ ...input, extra: true }, { ...input, runtimeFingerprint: '0'.repeat(64) }, JSON.stringify(input) + '{}', ' '.repeat(1_048_577)]) {
    const response = await invoke(bad); assert.notEqual(response.code, 0); assert.equal(response.stdout, '');
  }
});
