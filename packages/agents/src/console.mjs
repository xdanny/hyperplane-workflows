import { createInterface } from 'node:readline';
import { workflowCodex } from './index.mjs';
import { runTurn } from './turn.mjs';
import { workerOptions } from './worker-options.mjs';
import { consoleClientOptions, consoleSession } from './console-config.mjs';

// This is an interactive SDK conversation, not an accepted workflow delivery.
// Management tools save application records; execution remains an explicit workflow action.
const { clientOptions, threadOptions } = workerOptions({
  Cwd: process.env.HYPERPLANE_PLATFORM_ROOT || process.cwd(),
  Model: process.env.HYPERPLANE_MODEL || 'gpt-6-astra',
  SandboxMode: 'read-only',
}, process.env);
const management = Boolean(consoleSession());
const client = workflowCodex(consoleClientOptions(clientOptions));
let thread = client.startThread(threadOptions);
let active;
let stopping = false;
const terminal = Boolean(process.stdin.isTTY && process.stdout.isTTY);
const lines = createInterface({ input: process.stdin, output: process.stdout, terminal, prompt: 'you> ' });
const write = (text) => process.stdout.write(text + '\n');
const help = () => write((management ? 'Describe a project or workflow to create or update it. Saving does not run it.' : 'Conversation only: management tools are unavailable. Drafts are not saved.') + '\n/new starts a fresh conversation; /exit closes it. Ctrl-C cancels a turn.');
function stop() {
  stopping = true;
  active?.abort();
  lines.close();
}
function interrupt() {
  if (active) active.abort();
  else stop();
}
for (const event of ['SIGTERM', 'SIGHUP']) process.on(event, stop);
process.on('SIGINT', interrupt);
lines.on('SIGINT', interrupt);
write(management ? 'Hyperplane · Project and workflow management (read-only files, no execution or host MCP)' : 'Hyperplane · Conversation only (read-only, no management tools)');
help();
if (terminal) lines.prompt();
try {
  for await (const line of lines) {
    if (stopping) break;
    const input = line.trim();
    if (input === '/exit') break;
    if (input === '/new') { thread = client.startThread(threadOptions); write('Started a new conversation.'); }
    else if (input === '/help') help();
    else if (input.startsWith('/')) write('Unknown command. Use /help.');
    else if (input) {
      if (Buffer.byteLength(input) > 64_000) { write('Message exceeds 64 KB.'); }
      else {
        active = new AbortController();
        try {
          const result = await runTurn(thread, input, { signal: active.signal,
            onEvent: async (event) => {
              if (event.type === 'item.completed' && event.item.type === 'agent_message') write('\n' + event.item.text);
              if (event.type === 'item.started' && event.item.type === 'command_execution') write('[Inspecting project…]');
              if (event.type === 'item.started' && event.item.type === 'mcp_tool_call') write('[Checking or updating saved settings…]');
            },
          });
          write(`[session ${result.threadId} · turn finished; no acceptance check]`);
        } catch (error) {
          write(active.signal.aborted ? 'Turn cancelled.' : `Turn failed: ${error.message}`);
        } finally { active = undefined; }
      }
    }
    if (terminal && !stopping) lines.prompt();
  }
} finally {
  lines.close();
  for (const event of ['SIGTERM', 'SIGHUP']) process.off(event, stop);
  process.off('SIGINT', interrupt);
}
