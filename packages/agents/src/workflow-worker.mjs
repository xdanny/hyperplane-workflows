import { runIdeator } from './workflows/ideator.mjs';
import { runNodeWorkflow } from './workflows/nodes.mjs';
import { runTask } from './workflows/task.mjs';
import { runtimeFingerprint, runtimeFingerprintVersion } from './runtime.mjs';

// The host must commit each checkpoint before acknowledging its sequence.
// Observe control-channel loss continuously, including during an SDK turn.
const controller = new AbortController();
const controlLimit = 1_000_000;
const buffer = Buffer.allocUnsafe(controlLimit);
let length = 0;
let pending;
let closing = false;
function stop(error) {
  if (closing || controller.signal.aborted) return;
  controller.abort(error);
  pending?.reject(error);
  pending = undefined;
  process.stdin.pause();
}
function read() {
  controller.signal.throwIfAborted();
  if (pending) throw new Error('Concurrent workflow control reads');
  return new Promise((resolve, reject) => { pending = { resolve, reject }; });
}
process.stdin.on('data', chunk => {
  if (controller.signal.aborted) return;
  for (let offset = 0; offset < chunk.length;) {
    const newline = chunk.indexOf(10, offset);
    const end = newline < 0 ? chunk.length : newline;
    const bytes = end - offset;
    if (length + bytes > controlLimit) {
      stop(new Error('Workflow control message exceeds 1000000 bytes'));
      return;
    }
    chunk.copy(buffer, length, offset, end);
    length += bytes;
    offset = end + 1;
    if (newline < 0) break;
    if (!pending) {
      stop(new Error('Unexpected workflow control message'));
      return;
    }
    try {
      const message = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(buffer.subarray(0, length)));
      if (!message || typeof message !== 'object' || Array.isArray(message)) throw new Error('Expected a control object');
      const waiter = pending;
      pending = undefined;
      length = 0;
      waiter.resolve(message);
    } catch {
      stop(new Error('Invalid workflow control message'));
      return;
    }
  }
});
process.stdin.on('end', () => stop(new Error('Workflow control channel closed')));
process.stdin.on('close', () => stop(new Error('Workflow control channel closed')));
process.stdin.on('error', () => stop(new Error('Workflow control channel failed')));
process.stdout.on('error', () => stop(new Error('Workflow output channel failed')));
function send(value) {
  controller.signal.throwIfAborted();
  process.stdout.write(JSON.stringify(value) + '\n');
}
try {
  const request = await read();
  const fingerprint = await runtimeFingerprint();
  if (request.runtimeFingerprint !== undefined && request.runtimeFingerprint !== fingerprint) throw new Error('SDK runtime revision differs from launch');
  const stamp = state => ({ ...state, runtimeFingerprint: fingerprint, runtimeFingerprintVersion });
  const workflow = request.workflow === 'app-ideator' ? runIdeator : request.workflow === 'checked-task' ? runTask : request.workflow === 'node-workflow' ? runNodeWorkflow : null;
  if (!workflow) throw new Error('Unknown SDK workflow');
  let seq = 0;
  const result = await workflow({
    instruction: request.instruction, check: request.check, nodes: request.nodes, continuation: request.continuation,
    workspace: request.workspace, domain: request.domain, model: request.model,
    signal: controller.signal,
    maxCorrections: request.autoRepair ? 1 : 0, maxTokens: request.maxTokens,
    onEvent: async event => send(event),
    requestApproval: async state => {
      send({ type: 'hyperplane.approval', seq: ++seq, state: stamp(state) });
      const reply = await read();
      if (reply.ack !== seq || !['approved', 'rejected'].includes(reply.decision)) throw new Error('Approval was not committed');
      return reply.decision;
    },
    checkpoint: async state => {
      send({ type: 'hyperplane.checkpoint', seq: ++seq, state: stamp(state) });
      if ((await read()).ack !== seq) throw new Error('Workflow checkpoint was not committed');
    },
  });
  send({ type: 'hyperplane.workflow_result', result: stamp(result) });
} catch (error) {
  process.stderr.write(String((controller.signal.reason ?? error).message) + '\n');
  process.exitCode = 1;
} finally {
  closing = true;
  process.stdin.destroy();
}
