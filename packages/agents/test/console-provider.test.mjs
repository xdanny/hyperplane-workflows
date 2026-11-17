import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { mkdtemp, mkdir, rm, writeFile, readFile, chmod, access } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { workflowCodex } from '../src/index.mjs';
import { workerOptions } from '../src/worker-options.mjs';
import { consoleClientOptions, consoleToolNames } from '../src/console-config.mjs';

function respond(res, item) {
  const response = { id: 'resp_console', object: 'response', created_at: 1, status: 'completed', model: 'gpt-6-astra', output: [item], usage: { input_tokens: 1, output_tokens: 1, total_tokens: 2 } };
  res.writeHead(200, { 'content-type': 'text/event-stream' });
  for (const event of [
    { type: 'response.created', response: { ...response, status: 'in_progress', output: [] } },
    { type: 'response.output_item.added', output_index: 0, item },
    { type: 'response.output_item.done', output_index: 0, item },
    { type: 'response.completed', response },
  ]) res.write(`event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`);
  res.end();
}

// The pinned executable advertises code-mode wrappers in input.additional_tools
// and defers MCP declarations. Exercise its real V8 host to inspect ALL_TOOLS,
// including deferred functions, instead of assuming request.tools is exhaustive.
test('pinned console supports management on initial, continued and new threads without host execution or credential access', { timeout: 60000 }, async t => {
  const root = await mkdtemp(path.join(tmpdir(), 'sdk-console-provider-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const home = path.join(root, 'codex-home');
  await mkdir(home);
  await writeFile(path.join(home, 'config.toml'), '[mcp_servers.host_canary]\ncommand="HOST_MCP_CANARY_MUST_NOT_RUN"\n');
  await writeFile(path.join(root, 'AGENTS.md'), 'HOST_PROJECT_INSTRUCTION_CANARY_MUST_NOT_APPEAR');
  const argvFile = path.join(root, 'argv.jsonl');
  const launcher = path.join(root, 'capture-codex.mjs');
  const executable = fileURLToPath(new URL('../node_modules/@openai/codex/bin/codex.js', import.meta.url));
  await writeFile(launcher, `#!${process.execPath}\nimport { spawnSync } from 'node:child_process';\nimport { appendFileSync } from 'node:fs';\nappendFileSync(${JSON.stringify(argvFile)}, JSON.stringify(process.argv.slice(2)) + '\\n');\nconst result = spawnSync(process.execPath, [${JSON.stringify(executable)}, ...process.argv.slice(2)], { stdio: 'inherit' });\nprocess.exit(result.status ?? 1);\n`);
  await chmod(launcher, 0o700);
  const token = 'provider-private-canary-token';
  const forbiddenFile = path.join(root, 'forbidden-write');
  const patch = `*** Begin Patch\n*** Add File: ${forbiddenFile}\n+must not write\n*** End Patch`;
  const probe = `let importDenied = false; try { await import('node:fs'); } catch { importDenied = true; }
let patchError = ''; try { await tools.apply_patch(${JSON.stringify(patch)}); } catch (error) { patchError = String(error); }
const project = await tools.mcp__hyperplane__create_project({name: 'Ironman planning', summary: 'Research and review as ordinary instructions'});
text({catalog: ALL_TOOLS.map(tool => tool.name), globals: {process: typeof process, require: typeof require, fetch: typeof fetch, Buffer: typeof Buffer}, importDenied, patchError, project});`;
  const requests = [], management = [], outcomes = [];
  let pendingCall, calls = 0;
  const server = http.createServer(async (req, res) => {
    let body = ''; for await (const chunk of req) body += chunk;
    if (req.url === '/api/projects') {
      management.push({ method: req.method, authorized: req.headers.authorization === `Bearer ${token}`, body: JSON.parse(body) });
      res.writeHead(201, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ id: `ironman-${management.length}`, name: 'Ironman planning' }));
      return;
    }
    if (req.method !== 'POST') { res.writeHead(200, { 'content-type': 'application/json' }); res.end(JSON.stringify({ data: [{ id: 'gpt-6-astra', object: 'model' }] })); return; }
    const request = JSON.parse(body);
    requests.push(request);
    if (!pendingCall) {
      pendingCall = `call_console_${++calls}`;
      respond(res, { type: 'custom_tool_call', id: pendingCall, call_id: pendingCall, name: 'exec', namespace: 'functions', input: probe });
    } else {
      const output = request.input.find(item => item.type === 'custom_tool_call_output' && item.call_id === pendingCall);
      outcomes.push(output);
      pendingCall = null;
      respond(res, { id: 'msg_console', type: 'message', role: 'assistant', status: 'completed', content: [{ type: 'output_text', text: 'Management request completed.', annotations: [] }] });
    }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); server.close(); });
  const url = `http://127.0.0.1:${server.address().port}`;
  const { clientOptions, threadOptions } = workerOptions({ Cwd: root, BaseURL: `${url}/v1`, Model: 'gpt-6-astra' });
  const options = consoleClientOptions(clientOptions, { ...process.env, CODEX_HOME: home, HYPERPLANE_CODEX_EXECUTABLE: launcher, HYPERPLANE_CONSOLE_API: url, HYPERPLANE_CONSOLE_TOKEN: token, HYPERPLANE_API_TOKEN: 'APP_BEARER_CANARY' });
  const client = workflowCodex(options);
  const first = client.startThread(threadOptions);
  for (const thread of [first, first, client.startThread(threadOptions)]) {
    await thread.run('Create the requested planning project using management tools.', { signal: AbortSignal.timeout(15000) });
  }
  assert.equal(outcomes.length, 3);
  assert.equal(management.length, 3);
  assert.ok(management.every(request => request.authorized && request.method === 'POST' && request.body.name === 'Ironman planning'));
  const helpers = ['apply_patch', 'clock__curr_time', 'list_mcp_resource_templates', 'list_mcp_resources', 'read_mcp_resource'];
  for (const output of outcomes) {
    assert.ok(output, 'The model must receive the actual code-mode result');
    const payload = output.output.map(item => { try { return JSON.parse(item.text); } catch { return null; } }).find(item => item?.catalog);
    assert.ok(payload, JSON.stringify(output));
    assert.deepEqual(payload.catalog.filter(name => name.startsWith('mcp__')).sort(), consoleToolNames.map(name => `mcp__hyperplane__${name}`).sort());
    assert.ok(payload.catalog.every(name => helpers.includes(name) || consoleToolNames.some(tool => name === `mcp__hyperplane__${tool}`)), `Unexpected nested tool: ${payload.catalog.join(', ')}`);
    assert.deepEqual(payload.globals, { process: 'undefined', require: 'undefined', fetch: 'undefined', Buffer: 'undefined' });
    assert.equal(payload.importDenied, true);
    assert.match(payload.patchError, /read-only sandbox/);
    const saved = JSON.parse(payload.project.content[0].text);
    assert.match(saved.data.id, /^ironman-[123]$/);
    assert.equal(saved.uiLink, `/projects/${saved.data.id}`);
  }
  await assert.rejects(access(forbiddenFile), { code: 'ENOENT' });
  const directNames = [];
  function names(value) {
    if (!value || typeof value !== 'object') return;
    if (typeof value.name === 'string') directNames.push(value.name);
    for (const child of Object.values(value)) if (typeof child === 'object') names(child);
  }
  for (const request of requests) {
    names(request.tools);
    for (const item of request.input) if (item.type === 'additional_tools') names(item.tools);
  }
  assert.ok(directNames.includes('exec'));
  const directHelpers = ['functions', 'exec', 'wait', 'request_user_input', 'request_user_input_async', 'clock', 'sleep'];
  assert.ok(directNames.every(name => directHelpers.includes(name)), `Unexpected direct tool: ${directNames.join(', ')}`);
  const serialized = JSON.stringify(requests);
  const argv = await readFile(argvFile, 'utf8');
  assert.equal(argv.trim().split('\n').length, 3);
  for (const secret of [token, 'APP_BEARER_CANARY']) {
    assert.equal(serialized.includes(secret), false);
    assert.equal(argv.includes(secret), false);
  }
  assert.equal(serialized.includes('HOST_PROJECT_INSTRUCTION_CANARY_MUST_NOT_APPEAR'), false);
  assert.equal(serialized.includes('HOST_MCP_CANARY_MUST_NOT_RUN'), false);
  assert.equal(threadOptions.sandboxMode, 'read-only');
  assert.equal(threadOptions.networkAccessEnabled, false);
});
