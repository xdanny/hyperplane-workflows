import { readFile } from 'node:fs/promises';
import { workflowCodex } from './index.mjs';
import { workerOptions } from './worker-options.mjs';
import { runTurn } from './turn.mjs';

let source = '';
for await (const chunk of process.stdin) {
  source += chunk;
  if (Buffer.byteLength(source) > 8 * 1024 * 1024) throw new Error('SDK request exceeds 8 MB');
}
try {
  const request = JSON.parse(source);
  const { clientOptions, threadOptions } = workerOptions(request, process.env);
  const client = workflowCodex(clientOptions);
  const thread = request.ThreadID ? client.resumeThread(request.ThreadID, threadOptions) : client.startThread(threadOptions);
  const outputSchema = request.OutputSchemaPath ? JSON.parse(await readFile(request.OutputSchemaPath, 'utf8')) : undefined;
  const input = [{ type: 'text', text: request.Prompt }, ...(request.Images || []).map((path) => ({ type: 'local_image', path }))];
  const { text } = await runTurn(thread, input, { outputSchema,
    onEvent: async (event) => { process.stdout.write(JSON.stringify(event) + '\n'); },
  });
  process.stdout.write(JSON.stringify({ type: 'hyperplane.result', text, threadId: thread.id }) + '\n');
} catch (error) {
  process.stderr.write(String(error.message) + '\n');
  process.exitCode = 1;
}
