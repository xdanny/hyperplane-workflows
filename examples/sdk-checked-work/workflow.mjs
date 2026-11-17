import { mkdir, copyFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { runNodeWorkflow } from '../../packages/agents/src/workflows/nodes.mjs';

const workspace = path.resolve(process.argv[2] || `sdk-checked-work-${Date.now()}`);
await mkdir(workspace, { mode: 0o700 }); // Refuse an existing folder.
await copyFile(new URL('./input.csv', import.meta.url), path.join(workspace, 'input.csv'));
const result = await runNodeWorkflow({
  workspace, instruction: 'Produce a checked category-total CSV from the supplied synthetic data and explain it.',
  nodes: [
    { id: 'build', kind: 'task', title: 'Create checked totals',
      instruction: 'Read ../input.csv. Create totals.csv with category,total columns. Sum amounts with decimal arithmetic and order categories alphabetically. Keep two decimal places. Do not modify the input.',
      check: [process.execPath, fileURLToPath(new URL('./acceptance.mjs', import.meta.url))] },
    { id: 'explain', title: 'Explain the checked result', instruction: 'Read build.md and build/totals.csv. State the totals and explain exactly what the check proves and what it does not.' },
  ],
  maxTokens: 300000, maxCorrections: 1, signal: AbortSignal.timeout(600000),
  checkpoint: async state => console.log(JSON.stringify({ phase: state.phase, step: state.currentNode, usage: state.usage })),
});
await writeFile(path.join(workspace, 'receipt.json'), JSON.stringify(result, null, 2));
console.log(JSON.stringify({ workspace, passed: result.passed, findings: result.findings }));
if (!result.passed) process.exitCode = 1;
