import { mkdir, writeFile, rename } from 'node:fs/promises';
import path from 'node:path';
import { runNodeWorkflow } from '../src/workflows/nodes.mjs';

if (!process.argv[2]) throw new Error('Usage: node examples/nodes.mjs NEW_RUN_DIRECTORY');
const root = path.resolve(process.argv[2]);
await mkdir(root);
const workspace = path.join(root, 'workspace');
await mkdir(workspace);
const nodes = [
  { id: 'explore', title: 'Explore', instruction: 'Propose two small offline tools for weekend cyclists. Describe who each helps and its simplest useful feature. Treat demand as a hypothesis. Keep the document under 250 words.' },
  { id: 'review', title: 'Review', instruction: 'Read explore.md. Compare those exact two ideas for effort and usefulness. Recommend a first experiment and explain the uncertainty. Keep the document under 250 words.' },
];
const instruction = 'Choose a small cycling app idea to test with three friends.';
const events = [], checkpoints = [];
const started = Date.now();
const result = await runNodeWorkflow({ workspace, instruction, nodes, maxTokens: 30000,
  signal: AbortSignal.timeout(180000),
  onEvent: async event => events.push(event),
  checkpoint: async state => {
    checkpoints.push(state);
    await writeFile(path.join(root, 'checkpoint.tmp'), JSON.stringify(state, null, 2), { mode: 0o600 });
    await rename(path.join(root, 'checkpoint.tmp'), path.join(root, 'checkpoint.json'));
  },
}).catch(error => ({ passed: false, error: error.message }));
await writeFile(path.join(root, 'definition.json'), JSON.stringify({ instruction, nodes }, null, 2));
await writeFile(path.join(root, 'receipt.json'), JSON.stringify({ ...result, elapsedMs: Date.now() - started, platform: process.platform, model: 'gpt-6-astra', sdkVersion: '0.153.4' }, null, 2));
await writeFile(path.join(root, 'events.jsonl'), events.map(event => JSON.stringify(event)).join('\n') + '\n');
await writeFile(path.join(root, 'checkpoints.json'), JSON.stringify(checkpoints, null, 2));
console.log(JSON.stringify({ passed: result.passed, attempts: result.attempts, threadId: result.threadId, nodes: result.nodes, error: result.error }));
process.exitCode = result.passed ? 0 : 1;
