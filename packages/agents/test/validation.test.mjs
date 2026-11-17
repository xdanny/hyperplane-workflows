import test from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { validateNodes } from '@hyperplane/agents/validation';
import { validateNodes as runtimeValidateNodes } from '@hyperplane/agents/nodes';
import { runtimeFingerprint, runtimeFingerprintVersion } from '@hyperplane/agents/runtime';
import { preflightCheck } from '@hyperplane/agents/checks';
import { declaredTask } from './fixtures/check-declarations.mjs';

const packageRoot = fileURLToPath(new URL('../', import.meta.url));
const worker = fileURLToPath(new URL('../src/validate-worker.mjs', import.meta.url));
const goal = 'Measure the supplied data';
const approval = { id: 'approve', title: 'Approve', instruction: 'Review the current plan', kind: 'approval' };
const invalidDeclarations = [
  ['data-quality-profile', { keyColumns: 'none' }, /key columns/i],
  ['dataset-shift-profile', { features: JSON.stringify([{ feature: 'x', type: 'numeric', whyItMatters: 'Totals', tolerance: 1, statistic: 'psi', unit: 'count' }]) }, /compatible unit/],
  ['dataset-shift-profile', { seededControl: JSON.stringify([{ feature: 'unknown', multiple: 2 }]) }, /declared feature/],
  ['reconciliation-profile', { measures: '[null]' }, /invalid input value or table row/],
  ['reconciliation-profile', { measures: '[broken' }, /valid JSON or named fields/],
  ['reconciliation-profile', { tolerances: JSON.stringify([{ measure: 'other', unit: 'exact', value: '0' }]) }, /tolerance/],
  ['reconciliation-profile', { tolerances: JSON.stringify([{ measure: 'total', unit: 'exact', value: '1' }]) }, /exact|zero/i],
  ['pipeline-change-profile', { invariants: JSON.stringify([{ invariant: 'x', measure: 'Sum', kind: 'text', unit: 'absolute', tolerance: '1' }]) }, /exact equality/],
  ['data-pipeline-profile', { measures: JSON.stringify([{ measure: 'x', definition: 'Sum', unit: 'exact', tolerance: '1' }]) }, /exact|zero/i],
  ['prediction-metrics', { successMetric: 'metric: AUROC | threshold: 0.75' }, /threshold and comparison/],
  ['prediction-metrics', { successMetric: 'metric: accuracy | threshold: >= 0.75' }, /operating point/],
];

test('public pure validation is the execution validator and leaves declarations unchanged', async () => {
  assert.equal(validateNodes, runtimeValidateNodes);
  for (const id of ['data-quality-profile', 'dataset-shift-profile', 'reconciliation-profile', 'pipeline-change-profile', 'data-pipeline-profile', 'prediction-metrics']) {
    const node = declaredTask(id);
    Object.freeze(node.inputs); Object.freeze(node);
    const before = JSON.stringify(node);
    assert.equal(validateNodes(goal, [node]), undefined);
    assert.equal(JSON.stringify(node), before);
  }
  const node = declaredTask('data-quality-profile');
  node.inputs.dataset = '/does-not-exist/sdk-validation-only.csv';
  assert.doesNotThrow(() => validateNodes(goal, [node]));
  await assert.rejects(preflightCheck(node), /cannot be read/);
});

test('shared declaration parsers reject cross-field mistakes with actionable node identity', () => {
  for (const [id, inputs, message] of invalidDeclarations) {
    const node = declaredTask(id, 'inspect');
    Object.assign(node.inputs, inputs);
    assert.throws(() => validateNodes(goal, [node]), error => error.nodeId === 'inspect' && message.test(error.message), id);
  }
  for (const node of [null, [], { id: 'bad', title: 'Bad', instruction: 'Work', check: [] }, { id: 'bad', kind: 'task', title: 'Bad', instruction: 'Work', inputs: {} }]) {
    assert.throws(() => validateNodes(goal, [node]));
  }
  assert.throws(() => validateNodes('', []), error => error.nodeId === undefined);
});

test('validation preserves latest prerequisite and approval ordering', () => {
  const profile = declaredTask('reconciliation-profile', 'measure');
  const rules = declaredTask('reconciliation-rules', 'plan');
  const audit = declaredTask('reconciliation-audit', 'audit');
  assert.doesNotThrow(() => validateNodes(goal, [profile, rules, approval, audit]));
  assert.throws(() => validateNodes(goal, [profile, rules, approval, { ...rules, id: 'new-plan' }, audit]), error => error.nodeId === 'audit' && /Approve/.test(error.message));
  assert.throws(() => validateNodes(goal, [profile, rules, approval, { ...profile, id: 'new-measurement' }, audit]), error => error.nodeId === 'audit' && /prerequisite/.test(error.message));
});

async function invoke(payload, { preload } = {}) {
  const child = spawn(process.execPath, [...(preload ? ['--import', preload] : []), worker]);
  let stdout = '', stderr = '';
  child.stdin.on('error', () => {});
  child.stdout.on('data', data => { stdout += data; });
  child.stderr.on('data', data => { stderr += data; });
  const timeout = setTimeout(() => child.kill('SIGKILL'), 5000);
  const done = new Promise((resolve, reject) => { child.once('error', reject); child.once('close', code => resolve(code)); });
  child.stdin.end(Buffer.isBuffer(payload) || typeof payload === 'string' ? payload : JSON.stringify(payload));
  try { return { code: await done, stdout, stderr }; } finally { clearTimeout(timeout); }
}

test('validation process reads only trusted package files, never user sources or SDK tools', async t => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'sdk-validation-guard-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const marker = path.join(root, 'side-effects');
  const preload = path.join(root, 'guard.mjs');
  await writeFile(preload, `import fs from 'node:fs';
import promises from 'node:fs/promises';
import cp from 'node:child_process';
import {syncBuiltinESMExports} from 'node:module';
import {fileURLToPath} from 'node:url';
const log=fs.appendFileSync;
const deny=operation=>{log(${JSON.stringify(marker)},operation+'\\n');throw new Error('Forbidden validation side effect: '+operation);};
const allowed=${JSON.stringify(packageRoot)};
for(const name of ['readFileSync','statSync','openSync']){const original=fs[name];fs[name]=function(file,...args){const value=file instanceof URL?fileURLToPath(file):String(file);if(!value.startsWith(allowed))deny(name);return original.call(this,file,...args);};}
for(const name of ['readFile','stat','lstat','open','realpath','glob']){const original=promises[name];promises[name]=function(file,...args){const value=file instanceof URL?fileURLToPath(file):String(file);if(!value.startsWith(allowed))deny(name);return original.call(this,file,...args);};}
for(const name of ['mkdir','mkdtemp','writeFile','rm']) promises[name]=()=>deny(name);
for(const name of ['spawn','spawnSync','exec','execFile','execSync','execFileSync','fork']) cp[name]=()=>deny(name);
syncBuiltinESMExports();
`);
  const fingerprint = await runtimeFingerprint();
  const node = declaredTask('data-quality-profile');
  node.inputs.dataset = '/unavailable/source.csv';
  node.access = { hosts: ['example.test'], readPaths: ['/unavailable'], environment: ['WORKFLOW_NOT_CONFIGURED'] };
  node.toolchain = true; node.sideEffect = true;
  const result = await invoke({ instruction: goal, nodes: [node], runtimeFingerprint: fingerprint }, { preload });
  assert.equal(result.code, 0, result.stderr);
  assert.deepEqual(JSON.parse(result.stdout), { valid: true, runtimeFingerprint: fingerprint, runtimeFingerprintVersion });
  await assert.rejects(readFile(marker), { code: 'ENOENT' });
});

test('validation process distinguishes semantic issues from malformed protocol and runtime mismatch', async () => {
  const fingerprint = await runtimeFingerprint();
  const node = declaredTask('prediction-metrics', 'evaluate');
  node.inputs.successMetric = 'metric: AUROC | threshold: 0.75';
  const request = { instruction: goal, nodes: [node], runtimeFingerprint: fingerprint };
  const invalid = await invoke(request);
  assert.equal(invalid.code, 0, invalid.stderr);
  const response = JSON.parse(invalid.stdout);
  assert.equal(response.valid, false);
  assert.equal(response.issue.nodeId, 'evaluate');
  assert.match(response.issue.message, /threshold and comparison/);
  assert.equal(response.runtimeFingerprint, fingerprint);
  assert.equal(response.runtimeFingerprintVersion, runtimeFingerprintVersion);
  const validRequest = { ...request, nodes: [declaredTask('prediction-metrics')] };
  const boundary = await invoke(JSON.stringify(validRequest).padEnd(1_048_576, ' '));
  assert.equal(boundary.code, 0, boundary.stderr);
  assert.equal(JSON.parse(boundary.stdout).valid, true);
  for (const payload of [
    { ...request, extra: true }, { ...request, runtimeFingerprint: '0'.repeat(64) },
    { instruction: goal, nodes: [] }, 'null', '{broken', JSON.stringify(request) + '{}',
    Buffer.from([0xff]), Buffer.alloc(1_048_577, 32),
  ]) {
    const result = await invoke(payload);
    assert.notEqual(result.code, 0);
    assert.equal(result.stdout, '');
    assert.ok(result.stderr.length > 0 && result.stderr.length <= 4001);
  }
});
