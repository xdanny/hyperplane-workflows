import { mkdir, writeFile, rename } from 'node:fs/promises';
import path from 'node:path';
import { runIdeator } from '../src/workflows/ideator.mjs';

const destination = process.argv[2];
if (!destination) throw new Error('Usage: node examples/ideator.mjs NEW_RUN_DIRECTORY [--inject-failure]');
const root = path.resolve(destination);
// A new owned directory prevents accidentally mixing prior delivery artifacts.
await mkdir(root);
const workspace = path.join(root, 'workspace');
await mkdir(workspace);
const injected = process.argv.includes('--inject-failure');
const events = [];
const checkpoints = [];
const started = Date.now();
const result = await runIdeator({
  workspace, injectFailure: injected, model: process.env.HYPERPLANE_MODEL || 'gpt-6-astra',
  maxCorrections: 1,
  maxTokens: 300000,
  signal: AbortSignal.timeout(240000),
  onEvent: async (event) => events.push(event),
  checkpoint: async (state) => {
    checkpoints.push(state);
    await writeFile(path.join(root, 'checkpoint.tmp'), JSON.stringify(state, null, 2), { mode: 0o600 });
    await rename(path.join(root, 'checkpoint.tmp'), path.join(root, 'checkpoint.json'));
  },
}).catch((error) => ({ passed: false, error: error.message, threadId: checkpoints.at(-1)?.threadId }));
const receipt = { ...result, model: process.env.HYPERPLANE_MODEL || 'gpt-6-astra', sdkVersion: '0.153.4', platform: process.platform, injectedFailure: injected, elapsedMs: Date.now() - started };
await writeFile(path.join(root, 'receipt.json'), JSON.stringify(receipt, null, 2));
await writeFile(path.join(root, 'events.jsonl'), events.map((event) => JSON.stringify(event)).join('\n') + '\n');
await writeFile(path.join(root, 'checkpoints.json'), JSON.stringify(checkpoints, null, 2));
console.log(JSON.stringify(receipt));
process.exitCode = result.passed ? 0 : 1;
