import { runtimeFingerprint, runtimeFingerprintVersion } from './runtime.mjs';
import { validateNodes } from './validation.mjs';

const limit = 1_048_576;
try {
  const chunks = [];
  let bytes = 0;
  for await (const chunk of process.stdin) {
    bytes += chunk.length;
    if (bytes > limit) throw new Error('Validation request exceeds 1048576 bytes');
    chunks.push(chunk);
  }
  const request = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(Buffer.concat(chunks)));
  if (!request || typeof request !== 'object' || Array.isArray(request) ||
      Object.keys(request).some(key => !['instruction', 'nodes', 'runtimeFingerprint'].includes(key)) ||
      !['instruction', 'nodes', 'runtimeFingerprint'].every(key => Object.hasOwn(request, key)) ||
      typeof request.runtimeFingerprint !== 'string' || !/^[a-f0-9]{64}$/.test(request.runtimeFingerprint)) throw new Error('Invalid validation request');
  const fingerprint = await runtimeFingerprint();
  if (request.runtimeFingerprint !== fingerprint) throw new Error('SDK runtime revision differs from validation request');
  const revision = { runtimeFingerprint: fingerprint, runtimeFingerprintVersion };
  let response;
  try {
    validateNodes(request.instruction, request.nodes);
    response = { valid: true, ...revision };
  } catch (error) {
    response = { valid: false, issue: { ...(error.nodeId === undefined ? {} : { nodeId: error.nodeId }), message: error.message }, ...revision };
  }
  const output = JSON.stringify(response) + '\n';
  if (Buffer.byteLength(output) > limit) throw new Error('Validation response exceeds 1048576 bytes');
  await new Promise((resolve, reject) => process.stdout.write(output, error => error ? reject(error) : resolve()));
} catch (error) {
  process.stderr.write(String(error.message).slice(0, 4000) + '\n');
  process.exitCode = 1;
}
