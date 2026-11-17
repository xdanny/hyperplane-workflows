import { runtimeFingerprint, runtimeFingerprintVersion } from './runtime.mjs';
import { inspectContinuation, prepareContinuation } from './continuation.mjs';

const limit = 1_048_576;
const controller = new AbortController();
const stop = () => controller.abort(new Error('Continuation cancelled'));
process.on('SIGTERM', stop);
process.on('SIGINT', stop);
try {
  const chunks = [];
  let bytes = 0;
  for await (const chunk of process.stdin) {
    bytes += chunk.length;
    if (bytes > limit) throw new Error('Continuation request exceeds 1 MiB');
    chunks.push(chunk);
  }
  controller.signal.throwIfAborted();
  const input = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(Buffer.concat(chunks)));
  if (!input || !['inspect', 'prepare'].includes(input.operation) ||
      Object.keys(input).some(key => !['operation', 'request', 'checkpoint', 'sourceWorkspace', 'workspace', 'runtimeFingerprint'].includes(key)) ||
      !input.request || typeof input.request !== 'object' || Array.isArray(input.request) ||
      Object.keys(input.request).some(key => !['instruction', 'nodes', 'model', 'maxTokens', 'autoRepair'].includes(key)) ||
      (input.request.autoRepair !== undefined && typeof input.request.autoRepair !== 'boolean') ||
      typeof input.runtimeFingerprint !== 'string' || !/^[a-f0-9]{64}$/.test(input.runtimeFingerprint) ||
      (input.operation === 'inspect' && input.workspace !== undefined)) throw new Error('Invalid continuation request');
  const fingerprint = await runtimeFingerprint();
  if (input.runtimeFingerprint !== fingerprint) throw new Error('SDK runtime revision differs from continuation request');
  const revision = { runtimeFingerprint: fingerprint, runtimeFingerprintVersion };
  const args = { instruction: input.request.instruction, nodes: input.request.nodes, model: input.request.model,
    maxTokens: input.request.maxTokens, maxCorrections: input.request.autoRepair ? 1 : 0,
    checkpoint: input.checkpoint, sourceWorkspace: input.sourceWorkspace, workspace: input.workspace, signal: controller.signal };
  let response;
  try {
    const plan = await (input.operation === 'inspect' ? inspectContinuation(args) : prepareContinuation(args));
    response = { ok: true, plan, ...revision };
  } catch (error) {
    controller.signal.throwIfAborted();
    response = { ok: false, reason: error.message, ...revision };
  }
  const output = JSON.stringify(response) + '\n';
  if (Buffer.byteLength(output) > limit) throw new Error('Continuation response exceeds 1 MiB');
  await new Promise((resolve, reject) => process.stdout.write(output, error => error ? reject(error) : resolve()));
} catch (error) {
  process.stderr.write(String(error.message).slice(0, 4000) + '\n');
  process.exitCode = 1;
} finally {
  process.removeListener('SIGTERM', stop);
  process.removeListener('SIGINT', stop);
}
