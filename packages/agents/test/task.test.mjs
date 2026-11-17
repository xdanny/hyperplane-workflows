import test from 'node:test';
import assert from 'node:assert/strict';
import { verifyTask } from '../src/workflows/task.mjs';

test('acceptance distinguishes rejected work from verifier infrastructure failure', async () => {
  const passed = await verifyTask(process.cwd(), [process.execPath, '-e', 'console.log("checked 12 rows");process.exit(0)']);
  assert.equal(passed.passed, true);
  assert.deepEqual(passed.findings, []);
  // What the host observed, so a later step can say what actually ran.
  assert.deepEqual(passed.detail.command, [process.execPath, '-e', 'console.log("checked 12 rows");process.exit(0)']);
  assert.equal(passed.detail.exitCode, 0);
  assert.match(passed.detail.stdout, /checked 12 rows/);
  assert.ok(Number.isInteger(passed.detail.durationMs) && passed.detail.durationMs >= 0);
  const rejected = await verifyTask(process.cwd(), [process.execPath, '-e', 'console.error("missing expected output");process.exit(2)']);
  assert.equal(rejected.passed, false);
  assert.match(rejected.findings[0], /missing expected output/);
  assert.equal(rejected.detail.exitCode, 2);
  assert.match(rejected.detail.stderr, /missing expected output/);
  // A chatty check cannot grow the checkpoint without limit.
  const noisy = await verifyTask(process.cwd(), [process.execPath, '-e', 'console.log("x".repeat(20000));process.exit(0)']);
  assert.ok(Buffer.byteLength(noisy.detail.stdout) <= 4096, 'captured output is bounded');
  assert.ok(noisy.detail.stdout.startsWith('…'), 'a truncated tail says it is one');
  await assert.rejects(verifyTask(process.cwd(), ['/nonexistent/hyperplane-check']), { code: 'ENOENT' });
  const controller = new AbortController();
  const pending = verifyTask(process.cwd(), [process.execPath, '-e', 'setInterval(()=>{},1000)'], controller.signal);
  controller.abort();
  await assert.rejects(pending, { name: 'AbortError' });
});
