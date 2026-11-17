import { runTask } from '../../packages/agents/src/workflows/task.mjs';
import { appendFile, mkdir, mkdtemp, writeFile, rename } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';
import path from 'node:path';

const inject = process.argv.slice(2).includes('--inject-failure');
const root = await mkdtemp(path.join(tmpdir(), 'hyperplane-sdk-ui-'));
const workspace = path.join(root, 'workspace');
const evidence = path.join(root, 'evidence');
await mkdir(workspace);
await mkdir(evidence);
console.log(`Run directory: ${root}`);
const controller = new AbortController();
const stop = () => controller.abort();
process.on('SIGINT', stop);
process.on('SIGTERM', stop);
const timer = setTimeout(stop, 180_000);
try {
  const result = await runTask({
    workspace,
    instruction: `Create a self-contained index.html for a small focus-session counter.
Use inline CSS and JavaScript only, with no external assets or network requests.
Show a heading named "Focus counter", a counter element with role="status"
starting at 0, and a native button named "Add session". Each click or Enter key
activation must increment by exactly one. Make it readable and pleasant to use.
${inject ? 'CONTROLLED FAILURE TRIAL: On this first turn only, deliberately increment by TWO instead of one. This must fail acceptance. After the independent checker reports the defect, correct it to increment by one. Do not edit the acceptance program.' : ''}`,
    check: [process.execPath, fileURLToPath(new URL('./check.mjs', import.meta.url)), workspace, evidence],
    maxCorrections: 1, maxTokens: 200_000, signal: controller.signal,
    onEvent: async (event) => appendFile(path.join(evidence, 'sdk-events.jsonl'), JSON.stringify(event) + '\n'),
    checkpoint: async (state) => {
      await appendFile(path.join(evidence, 'checkpoints.jsonl'), JSON.stringify(state) + '\n');
      await writeFile(path.join(evidence, 'checkpoint.tmp'), JSON.stringify(state, null, 2) + '\n');
      await rename(path.join(evidence, 'checkpoint.tmp'), path.join(evidence, 'checkpoint.json'));
    },
  });
  await writeFile(path.join(evidence, 'result.json'), JSON.stringify(result, null, 2) + '\n');
  console.log(JSON.stringify({ root, ...result }, null, 2));
  if (!result.passed) process.exitCode = 1;
} finally {
  clearTimeout(timer);
  process.off('SIGINT', stop);
  process.off('SIGTERM', stop);
}
