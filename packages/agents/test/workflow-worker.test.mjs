import test from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtemp, mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { runtimeFingerprint, runtimeFingerprintVersion } from '@hyperplane/agents/runtime';

const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const workerPath = fileURLToPath(new URL('../src/workflow-worker.mjs', import.meta.url));
async function worker(t, { fixture = 'ideator', mode = 'ordinary', approval = false, ack = () => true, inputFault = false } = {}) {
  const root = await mkdtemp(path.join(tmpdir(), 'sdk-workflow-control-'));
  const workspace = path.join(root, 'workspace');
  await mkdir(workspace);
  const pids = path.join(root, 'pids');
  const calls = path.join(root, 'calls');
  const executable = path.join(root, 'fixture');
  // Record the actual SDK child PID, then replace this process with the model
  // fixture. No extra wrapper subprocess hides the cancellation result.
  await writeFile(executable, `#!/usr/bin/env python3
import os, sys
with open(os.environ['SDK_FIXTURE_PIDS'], 'a') as file:
    file.write(str(os.getpid()) + '\\n')
os.execv(os.environ['SDK_FIXTURE_EXECUTABLE'], [os.environ['SDK_FIXTURE_EXECUTABLE'], *sys.argv[1:]])
`, { mode: 0o700 });
  const faultPath = path.join(root, 'input-error');
  const args = [workerPath];
  if (inputFault) {
    const preload = path.join(root, 'input-fault.mjs');
    // Pipes do not reliably produce read errors on demand across platforms.
    // Inject that stream event only after the fixture turn has actually begun.
    await writeFile(preload, `import { existsSync } from 'node:fs';
const timer = setInterval(() => {
  if (existsSync(${JSON.stringify(faultPath)})) {
    clearInterval(timer);
    process.stdin.emit('error', new Error('fixture read error'));
  }
}, 10);
timer.unref();\n`);
    args.unshift('--import', preload);
  }
  const child = spawn(process.execPath, args, { env: { ...process.env,
    HYPERPLANE_CODEX_EXECUTABLE: executable, SDK_FIXTURE_PIDS: pids, SDK_FIXTURE_CALLS: calls,
    SDK_FIXTURE_EXECUTABLE: fileURLToPath(new URL(`../../../internal/codex/testdata/${fixture}`, import.meta.url)),
    SDK_FIXTURE_MODE: mode, SDK_FIXTURE_APPROVAL: approval ? '1' : '',
  } });
  const messages = [];
  let stdout = '', stderr = '';
  child.stdin.on('error', () => {}); // Expected EPIPE if a rejected input is still being written.
  child.stdout.on('data', chunk => {
    stdout += chunk;
    let newline;
    while ((newline = stdout.indexOf('\n')) >= 0) {
      const event = JSON.parse(stdout.slice(0, newline));
      stdout = stdout.slice(newline + 1);
      messages.push(event);
      if (event.type === 'hyperplane.checkpoint' && ack(event)) child.stdin.write(JSON.stringify({ ack: event.seq }) + '\n');
    }
  });
  child.stderr.on('data', chunk => { stderr += chunk; });
  const done = new Promise((resolve, reject) => { child.once('error', reject); child.once('close', (code, signal) => resolve({ code, signal })); });
  async function readPids() { return (await readFile(pids, 'utf8').catch(() => '')).trim().split('\n').filter(Boolean).map(Number); }
  t.after(async () => {
    for (const pid of await readPids()) { try { process.kill(pid, 'SIGKILL'); } catch {} }
    if (child.exitCode === null) child.kill('SIGKILL');
    await done;
    await rm(root, { recursive: true, force: true });
  });
  async function until(predicate) {
    const deadline = Date.now() + 5000;
    while (!messages.some(predicate)) {
      assert.equal(child.exitCode, null, `Worker exited early: ${stderr}`);
      assert.ok(Date.now() < deadline, `Timed out: ${JSON.stringify(messages)} ${stderr}`);
      await delay(10);
    }
    return messages.find(predicate);
  }
  async function stopped(expectedCode = 1) {
    const deadline = Date.now() + 3000;
    while (child.exitCode === null && child.signalCode === null) {
      assert.ok(Date.now() < deadline, `Worker did not stop: ${stderr}`);
      await delay(10);
    }
    const exit = await done;
    assert.equal(exit.code, expectedCode, stderr);
    for (const pid of await readPids()) {
      assert.throws(() => process.kill(pid, 0), { code: 'ESRCH' }, 'SDK fixture survived its worker');
    }
  }
  return { child, messages, workspace, done, until, stopped, stderr: () => stderr,
    send: value => child.stdin.write(JSON.stringify(value) + '\n'),
    calls: () => readFile(calls, 'utf8').catch(() => ''), readPids,
    failInput: () => writeFile(faultPath, ''),
  };
}

const taskRequest = proc => ({ workflow: 'checked-task', workspace: proc.workspace, instruction: 'Write the requested answer', maxTokens: 1000 });
const ideatorRequest = proc => ({ workflow: 'app-ideator', workspace: proc.workspace, domain: 'Cycling tools', autoRepair: true, maxTokens: 1000 });
const approvalRequest = proc => ({ workflow: 'node-workflow', workspace: proc.workspace, instruction: 'Review options', maxTokens: 1000,
  nodes: [{ id: 'explore', title: 'Explore', instruction: 'Find options' },
    { id: 'approval', kind: 'approval', title: 'Approve', instruction: 'Review options' },
    { id: 'review', title: 'Review', instruction: 'Compare approved options' }],
});

test('control EOF aborts an in-flight SDK subprocess immediately', { timeout: 15000 }, async t => {
  const proc = await worker(t, { fixture: 'task', mode: 'hang' });
  proc.send(taskRequest(proc));
  await proc.until(event => event.type === 'hyperplane.checkpoint' && event.state.threadId === 'fixture-task-thread');
  assert.equal((await proc.readPids()).length, 1);
  proc.child.stdin.end();
  await proc.stopped();
  assert.match(proc.stderr(), /control channel closed/);
  assert.equal(proc.messages.some(event => event.type === 'hyperplane.workflow_result'), false);
  assert.equal(await proc.calls(), 'start\n');
});

test('control EOF ends an approval wait without a decision or another turn', { timeout: 15000 }, async t => {
  const proc = await worker(t, { fixture: 'nodes', approval: true });
  proc.send(approvalRequest(proc));
  await proc.until(event => event.type === 'hyperplane.approval');
  assert.equal(await proc.calls(), 'explore\n');
  proc.child.stdin.end();
  await proc.stopped();
  assert.match(proc.stderr(), /control channel closed/);
  assert.equal(await proc.calls(), 'explore\n');
  await assert.rejects(readFile(path.join(proc.workspace, 'approval.md')), { code: 'ENOENT' });
});

test('a control stream read error also cancels the active SDK child', { timeout: 15000 }, async t => {
  const proc = await worker(t, { fixture: 'task', mode: 'hang', inputFault: true });
  proc.send(taskRequest(proc));
  await proc.until(event => event.type === 'hyperplane.checkpoint');
  await proc.failInput();
  await proc.stopped();
  assert.match(proc.stderr(), /control channel failed/);
  assert.equal(await proc.calls(), 'start\n');
});

test('a correction remains fenced by the exact committed checkpoint sequence', { timeout: 15000 }, async t => {
  const proc = await worker(t, { mode: 'recovery', ack: event => event.state.phase !== 'correction_pending' });
  proc.send(ideatorRequest(proc));
  const checkpoint = await proc.until(event => event.type === 'hyperplane.checkpoint' && event.state.phase === 'correction_pending');
  await delay(100);
  assert.equal(await proc.calls(), 'start\n');
  proc.send({ ack: checkpoint.seq });
  const finished = await proc.until(event => event.type === 'hyperplane.workflow_result');
  await proc.stopped(0);
  assert.equal(finished.result.passed, true);
  assert.equal(finished.result.attempts, 2);
  assert.equal(await proc.calls(), 'start\nresume\n');
  const sequences = proc.messages.filter(event => event.type === 'hyperplane.checkpoint').map(event => event.seq);
  assert.deepEqual(sequences, sequences.map((_, index) => index + 1));
});

test('incorrect checkpoint or approval acknowledgement cannot advance execution', { timeout: 15000 }, async t => {
  for (const approval of [false, true]) await t.test(approval ? 'approval' : 'checkpoint', async t => {
    const proc = await worker(t, approval ? { fixture: 'nodes', approval: true } : { mode: 'recovery', ack: event => event.state.phase !== 'correction_pending' });
    proc.send(approval ? approvalRequest(proc) : ideatorRequest(proc));
    const waiting = await proc.until(event => approval ? event.type === 'hyperplane.approval' : event.type === 'hyperplane.checkpoint' && event.state.phase === 'correction_pending');
    proc.send({ ack: waiting.seq + 1, ...(approval ? { decision: 'approved' } : {}) });
    await proc.stopped();
    assert.match(proc.stderr(), /was not committed/);
    assert.equal(await proc.calls(), approval ? 'explore\n' : 'start\n');
  });
});

test('oversized unterminated and malformed control input abort a running turn', { timeout: 20000 }, async t => {
  for (const [name, payload, error] of [
    ['oversized', Buffer.alloc(1_000_001, 120), /exceeds 1000000 bytes/],
    ['malformed', Buffer.from('{oops}\n'), /Invalid workflow control message/],
    ['non_object', Buffer.from('null\n'), /Invalid workflow control message/],
    ['invalid_utf8', Buffer.from([0xff, 10]), /Invalid workflow control message/],
  ]) await t.test(name, async t => {
    // Hold the first checkpoint so input validation runs while an SDK child
    // remains alive. The parent never closes its control pipe in these cases.
    const proc = await worker(t, { fixture: 'task', mode: 'hang', ack: () => false });
    proc.send(taskRequest(proc));
    await proc.until(event => event.type === 'hyperplane.checkpoint');
    proc.child.stdin.write(payload);
    await proc.stopped();
    assert.match(proc.stderr(), error);
    assert.equal(await proc.calls(), 'start\n');
  });
});

test('unsolicited control messages are rejected instead of queued during a turn', { timeout: 10000 }, async t => {
  const proc = await worker(t, { fixture: 'task', mode: 'hang' });
  proc.send(taskRequest(proc));
  await proc.until(event => event.type === 'hyperplane.checkpoint');
  // The valid acknowledgement is already sent; this second message cannot be
  // banked as permission for a checkpoint the host has not yet observed.
  await delay(50);
  proc.send({ ack: 2 });
  await proc.stopped();
  assert.match(proc.stderr(), /Unexpected workflow control message/);
});


test('runtime mismatch rejects every workflow before any SDK subprocess or checkpoint', { timeout: 15000 }, async t => {
  const installed = await runtimeFingerprint();
  const mismatch = (installed[0] === '0' ? '1' : '0') + installed.slice(1);
  for (const [name, fixture, request] of [
    ['task', 'task', taskRequest],
    ['ideator', 'ideator', ideatorRequest],
    ['nodes', 'nodes', approvalRequest],
  ]) await t.test(name, async t => {
    const proc = await worker(t, { fixture });
    proc.send({ ...request(proc), runtimeFingerprint: mismatch });
    await proc.stopped();
    assert.match(proc.stderr(), /SDK runtime revision differs from launch/);
    assert.deepEqual(await proc.readPids(), [], 'A mismatched runtime started an SDK child');
    assert.equal(await proc.calls(), '');
    assert.deepEqual(proc.messages, [], 'A mismatched runtime emitted workflow evidence');
    assert.deepEqual(await readdir(proc.workspace), [], 'A mismatched runtime created work files');
  });
});

test('worker stamps committed checkpoints, approval evidence and final result with installed runtime identity', { timeout: 15000 }, async t => {
  const fingerprint = await runtimeFingerprint();
  const proc = await worker(t, { fixture: 'nodes', approval: true });
  proc.send({ ...approvalRequest(proc), runtimeFingerprint: fingerprint });
  const approval = await proc.until(event => event.type === 'hyperplane.approval');
  assert.equal(approval.state.runtimeFingerprint, fingerprint);
  assert.equal(approval.state.runtimeFingerprintVersion, runtimeFingerprintVersion);
  assert.equal(await proc.calls(), 'explore\n');
  proc.send({ ack: approval.seq, decision: 'approved' });
  const finished = await proc.until(event => event.type === 'hyperplane.workflow_result');
  await proc.stopped(0);
  assert.equal(finished.result.passed, true);
  assert.equal(await proc.calls(), 'explore\nreview\n');
  const evidence = proc.messages.filter(event => ['hyperplane.checkpoint', 'hyperplane.approval', 'hyperplane.workflow_result'].includes(event.type));
  assert.ok(evidence.some(event => event.type === 'hyperplane.checkpoint'));
  assert.ok(evidence.some(event => event.type === 'hyperplane.approval'));
  assert.ok(evidence.some(event => event.type === 'hyperplane.workflow_result'));
  for (const event of evidence) {
    const state = event.state ?? event.result;
    assert.equal(state.runtimeFingerprint, fingerprint, `${event.type} lost the installed fingerprint`);
    assert.equal(state.runtimeFingerprintVersion, runtimeFingerprintVersion, `${event.type} lost the fingerprint format version`);
  }
  const sequences = evidence.filter(event => event.seq !== undefined).map(event => event.seq);
  assert.deepEqual(sequences, sequences.map((_, index) => index + 1), 'Stamping changed the commit protocol');
});
