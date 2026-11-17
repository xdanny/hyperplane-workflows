import { execFile } from 'node:child_process';
import { rm } from 'node:fs/promises';
import { taskAccessOptions, validateTaskAccess } from '../task-access.mjs';
import { promisify } from 'node:util';
import { workflowCodex, runChecked } from '../index.mjs';

const execute = promisify(execFile);

// Acceptance commands are trusted caller programs, not model-generated tools.
// They run on the host in the target directory; their dependencies must be kept
// outside the agent's writable target when immutable acceptance is required.
// detailLimit bounds each captured stream in the recorded evidence. The command
// itself still runs under the unchanged 64 KB capture and 60 s timeout; this is
// how much of what it printed is KEPT, so a reader can see what the check said
// without a checkpoint carrying a log file.
const detailLimit = 4096;
const elision = '…';
const tail = value => {
  const bytes = Buffer.from(String(value ?? ''), 'utf8');
  if (bytes.length <= detailLimit) return bytes.toString('utf8');
  // The marker counts against the limit, so the recorded tail never exceeds it.
  return elision + bytes.subarray(bytes.length - (detailLimit - Buffer.byteLength(elision))).toString('utf8');
};

// A run used to record that an acceptance command passed, and nothing else: not
// what ran, not what it exited with, not what it printed. Later steps then
// wrote "the caller's acceptance command was not run", because from inside the
// sandbox that is indistinguishable from the truth. The evidence below is what
// the host actually observed.
export async function verifyTask(workspace, check, signal) {
  signal?.throwIfAborted();
  const started = Date.now();
  const detail = argv => ({ command: check, exitCode: argv.code, durationMs: Date.now() - started, stdout: tail(argv.stdout), stderr: tail(argv.stderr) });
  try {
    const result = await execute(check[0], check.slice(1), { cwd: workspace, signal, timeout: 60_000, maxBuffer: 64_000 });
    return { passed: true, findings: [], detail: detail({ code: 0, stdout: result.stdout, stderr: result.stderr }) };
  } catch (error) {
    // Launch failures, cancellation, timeouts and output overflow cannot be
    // repaired by changing the project. Only a normal nonzero exit is feedback.
    if (!Number.isInteger(error.code) || error.killed || error.signal) throw error;
    return { passed: false, findings: [`Acceptance command exited ${error.code}:\n${error.stdout || ''}${error.stderr || ''}`], detail: detail(error) };
  }
}

export function runTask({ workspace, instruction, check, model = 'gpt-6-astra', beforeCheck, acceptance, access, toolchain, sideEffect, inputRoots, ...checks }) {
  if (typeof instruction !== 'string' || !instruction.trim()) throw new Error('Describe the task');
  if (check !== undefined && (!Array.isArray(check) || !check.length || check.length > 100 || check.some(arg => typeof arg !== 'string' || arg.includes('\0') || Buffer.byteLength(arg) > 16000) || !check[0].trim())) throw new Error('Acceptance needs a program and literal arguments');
  if (acceptance !== undefined && (!acceptance || typeof acceptance.description !== 'string' || typeof acceptance.verify !== 'function')) throw new Error('Acceptance needs a description and verifier');
  if (check !== undefined && acceptance !== undefined) throw new Error('Choose a check command or verifier');
  const hasCheck = check !== undefined || acceptance !== undefined;
  validateTaskAccess({access,toolchain,sideEffect});
  return (async () => {
  const policy = await taskAccessOptions({workspace,access,toolchain,sideEffect,inputRoots});
  try {
  const client = workflowCodex(policy?.clientOptions ?? { config: { project_doc_max_bytes: 0 } });
  const thread = client.startThread({
    model, workingDirectory: workspace, ...(policy ? {} : {sandboxMode: 'workspace-write', networkAccessEnabled: false}), approvalPolicy: 'never', skipGitRepoCheck: true,
    modelReasoningEffort: 'low', webSearchMode: 'disabled', threadSource: 'hyperplane',
  });
  let verificationDetail;
  const result = await runChecked(thread, `Complete this requested task in the current directory:\n${instruction}\n
${acceptance ? acceptance.description : check ? `After your turn, the caller will run this fixed acceptance command: ${JSON.stringify(check)}.\nDo not run this acceptance command yourself; it runs outside your sandbox.` : 'No extra acceptance check is configured. Complete the requested work and describe what you actually did and any limitations. Do not claim independent acceptance or verification.'}
Preserve existing unrelated work. Do not weaken acceptance tests, deploy or publish.
${policy ? policy.instructions : 'Do not install tools or access the network.'} Write only in this target directory${policy ? ' and the explicitly supplied tool runtime' : ''}.
Finish with a concise description of the changes and any remaining limitations.`, {
    ...checks, checkpoint: async state => {
      await checks.checkpoint?.({ ...state, verification: hasCheck ? (state.phase === 'passed' ? 'passed' : state.phase === 'failed' ? 'failed' : 'pending') : 'not_requested' });
    }, verify: async ({ signal }) => {
      if (beforeCheck) await beforeCheck();
      const verdict = acceptance ? await acceptance.verify({ signal }) : check ? await verifyTask(workspace, check, signal) : { passed: true, findings: [] };
      if (verdict.detail) verificationDetail = verdict.detail;
      return verdict;
    },
  });
  return { ...result, verification: hasCheck ? (result.passed ? 'passed' : 'failed') : 'not_requested', ...(verificationDetail ? { verificationDetail } : {}) };
  } finally { if (policy) await rm(policy.runtime, {recursive:true,force:true}); }
  })();
}
