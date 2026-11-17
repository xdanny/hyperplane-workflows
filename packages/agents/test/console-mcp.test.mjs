import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { fileURLToPath } from 'node:url';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StdioClientTransport } from '@modelcontextprotocol/sdk/client/stdio.js';
import { consoleClientOptions, consoleSession, consoleToolNames } from '../src/console-config.mjs';

async function connection(t) {
  const requests = [];
  let failure;
  const token = 'console-session-test-token';
  const server = http.createServer(async (req, res) => {
    let body = '';
    for await (const part of req) body += part;
    assert.equal(req.headers.authorization, `Bearer ${token}`);
    requests.push({ method: req.method, path: req.url, body: body ? JSON.parse(body) : undefined });
    res.writeHead(failure?.status ?? 200, { 'Content-Type': 'application/json', ...(failure?.status === 302 ? { Location: '/api/run' } : {}) });
    res.end(JSON.stringify(failure?.data ?? (req.url.includes('/workflows/validate') ? { valid: true } : req.url.includes('/workflows') ? { id: 'saved-id', revision: 2 } : { id: 'project-id', name: 'Test project' })));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); server.close(); });
  const transport = new StdioClientTransport({ command: process.execPath, args: [fileURLToPath(new URL('../src/console-mcp.mjs', import.meta.url))],
    env: { HYPERPLANE_CONSOLE_API: `http://127.0.0.1:${server.address().port}`, HYPERPLANE_CONSOLE_TOKEN: token }, stderr: 'pipe' });
  const client = new Client({ name: 'console-test', version: '1' });
  t.after(() => client.close());
  await client.connect(transport);
  return { client, requests, token, fail: value => { failure = value; } };
}

test('official stdio MCP tools round-trip only declared management paths and exact settings', { timeout: 15000 }, async t => {
  const { client, requests } = await connection(t);
  const catalog = await client.listTools();
  assert.deepEqual(catalog.tools.map(tool => tool.name), consoleToolNames);
  assert.ok(catalog.tools.every(tool => !/run_workflow|delete|shell|http_request/.test(tool.name)));
  const workflow = { name: 'Ironman preparation', kind: 'nodes', instruction: 'Plan preparation for Ironman.', project: 'project-id', nodes: [
    { id: 'research', dependsOn: [], kind: 'research', title: 'Research preparation', instruction: 'Use current research and cite uncertainty.' },
    { id: 'review', dependsOn: ['research'], kind: 'check', title: 'Review as a coach', instruction: 'Evaluate the evidence and seven-hour weekly budget.' },
  ] };
  const calls = [
    ['list_projects', {}, 'GET', '/api/projects'], ['get_project', { id: 'a b' }, 'GET', '/api/projects/a%20b'],
    ['create_project', { name: 'Ironman', summary: 'Evidence-based preparation' }, 'POST', '/api/projects'],
    ['update_project', { id: 'project-id', changes: { summary: 'Seven hours weekly' } }, 'PATCH', '/api/projects/project-id'],
    ['list_workflows', {}, 'GET', '/api/workflows'], ['save_workflow', { workflow }, 'POST', '/api/workflows'],
    ['update_workflow', { id: 'saved-id', revision: 2, workflow }, 'PUT', '/api/workflows/saved-id'],
    ['validate_workflow', { instruction: workflow.instruction, nodes: workflow.nodes }, 'POST', '/api/workflows/validate'],
    ['list_checks', {}, 'GET', '/api/workflow-checks'],
  ];
  for (const [name, args, method, path] of calls) {
    const result = await client.callTool({ name, arguments: args });
    assert.notEqual(result.isError, true, result.content[0].text);
    assert.equal(requests.at(-1).method, method);
    assert.equal(requests.at(-1).path, path);
    if (name === 'save_workflow') {
      assert.deepEqual(requests.at(-1).body.nodes, workflow.nodes);
      assert.equal(requests.at(-1).body.project, 'project-id');
      assert.equal(JSON.parse(result.content[0].text).uiLink, '/workflows');
    }
    if (name === 'update_workflow') assert.equal(requests.at(-1).body.revision, 2);
  }
  assert.equal(requests.length, calls.length);

  for (const check of [undefined, [], ['node', '/checks/check.mjs', '', 'two words']]) {
    const settings = { name: 'Project task', kind: 'task', instruction: 'Inspect the project', targetRoot: '/tmp/project', ...(check === undefined ? {} : { check }) };
    const result = await client.callTool({ name: 'save_workflow', arguments: { workflow: settings } });
    assert.notEqual(result.isError, true);
    assert.equal(Object.hasOwn(requests.at(-1).body, 'check'), check !== undefined);
    assert.deepEqual(requests.at(-1).body.check, check);
  }

});

test('MCP errors, invalid arguments and uncertain writes never report a save or follow redirects', { timeout: 15000 }, async t => {
  const { client, requests, token, fail } = await connection(t);
  for (const call of [
    { name: 'run_workflow', arguments: {} },
    { name: 'get_project', arguments: { id: '../runs' } },
    { name: 'list_projects', arguments: { url: '/api/run' } },
    { name: 'save_workflow', arguments: { workflow: { name: 'Bad', kind: 'nodes', instruction: 'Bad', nodeRuntime: 'specialized' } } },
  ]) assert.equal((await client.callTool(call)).isError, true);
  assert.equal(requests.length, 0);
  for (const status of [400, 401, 409, 503]) {
    fail({ status, data: { error: `Not saved ${token}` } });
    const result = await client.callTool({ name: 'create_project', arguments: { name: 'Ironman' } });
    assert.equal(result.isError, true);
    assert.match(result.content[0].text, new RegExp(String(status)));
    assert.equal(result.content[0].text.includes(token), false);
  }
  fail({ status: 302, data: {} });
  assert.equal((await client.callTool({ name: 'create_project', arguments: { name: 'Ironman' } })).isError, true);
  assert.equal(requests.some(req => req.path === '/api/run'), false);
});

test('console config keeps credentials out of argv, excludes unrelated environment and fails closed on bad bridge configuration', () => {
  const token = 'console-config-private-token';
  const options = consoleClientOptions({ config: { mcp_servers: {} }, configOverrides: [] }, { PATH: process.env.PATH, HYPERPLANE_CONSOLE_API: 'http://127.0.0.1:12345', HYPERPLANE_CONSOLE_TOKEN: token, HYPERPLANE_API_TOKEN: 'app-secret' });
  assert.equal(JSON.stringify(options.configOverrides).includes(token), false);
  assert.equal(JSON.stringify(options.config).includes(token), false);
  assert.equal(options.env.HYPERPLANE_API_TOKEN, undefined);
  assert.equal(options.env.HYPERPLANE_CONSOLE_TOKEN, token);
  assert.ok(options.configOverrides.includes('mcp_servers.hyperplane.required=true'));
  assert.equal(consoleSession({}), null);
  assert.match(consoleClientOptions({ config: {} }, {}).config.developer_instructions, /conversation-only/);
  for (const address of ['https://127.0.0.1:1', 'http://example.com:1', 'http://127.0.0.1:1/api/run', 'http://user:pass@127.0.0.1:1', 'http://127.0.0.1:1?redirect=yes']) {
    assert.throws(() => consoleSession({ HYPERPLANE_CONSOLE_API: address, HYPERPLANE_CONSOLE_TOKEN: token }), /invalid/);
  }
});
