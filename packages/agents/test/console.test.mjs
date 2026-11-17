import test from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

async function consoleProcess(t) {
  const root = await mkdtemp(path.join(tmpdir(), 'sdk-console-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const calls = path.join(root, 'calls.jsonl');
  const child = spawn(process.execPath, [fileURLToPath(new URL('../src/console.mjs', import.meta.url))], {
    env: { ...process.env, HYPERPLANE_PLATFORM_ROOT: root, SDK_FIXTURE_CALLS: calls,
      HYPERPLANE_CODEX_EXECUTABLE: fileURLToPath(new URL('../../../internal/codex/testdata/console', import.meta.url)) },
  });
  t.after(() => { if (child.exitCode === null) child.kill('SIGKILL'); });
  let stdout = '', stderr = '';
  child.stdout.on('data', (data) => stdout += data);
  child.stderr.on('data', (data) => stderr += data);
  const done = new Promise((resolve, reject) => { child.once('error', reject); child.once('close', resolve); });
  async function until(text) {
    const end = Date.now() + 5000;
    while (!stdout.includes(text)) {
      assert.equal(child.exitCode, null, stderr + stdout);
      assert.ok(Date.now() < end, `Timed out waiting for ${text}: ${stdout} ${stderr}`);
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
  }
  return { child, done, until, stdout: () => stdout, stderr: () => stderr,
    calls: async () => (await readFile(calls, 'utf8')).trim().split('\n').map(JSON.parse) };
}

test('console uses SDK continuation, resets explicitly, and never presents a failed turn as complete', { timeout: 15000 }, async (t) => {
  const proc = await consoleProcess(t);
  proc.child.stdin.end('first\nsecond\n/new\nthird\nfail\n/exit\n');
  assert.equal(await proc.done, 0, proc.stderr());
  const calls = await proc.calls();
  assert.deepEqual(calls.map((call) => call.resumed), [false, true, false, true]);
  assert.deepEqual(calls.map((call) => call.prompt), ['first', 'second', 'third', 'fail']);
  assert.match(proc.stdout(), /reply: second/);
  assert.match(proc.stdout(), /Turn failed: fixture transport failed/);
  assert.equal(proc.stdout().match(/turn finished/g).length, 3);
  assert.match(proc.stdout(), /read-only/);
});

test('console interrupt aborts the SDK turn and still accepts the next message', { timeout: 15000 }, async (t) => {
  const proc = await consoleProcess(t);
  proc.child.stdin.write('hang\n');
  await proc.until('[Inspecting project');
  proc.child.kill('SIGINT');
  await proc.until('Turn cancelled.');
  proc.child.stdin.end('after cancellation\n/exit\n');
  assert.equal(await proc.done, 0, proc.stderr());
  assert.match(proc.stdout(), /reply: after cancellation/);
  assert.equal((await proc.calls()).length, 2);
});
