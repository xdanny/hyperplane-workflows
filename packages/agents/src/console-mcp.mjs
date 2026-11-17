import { Server } from '@modelcontextprotocol/sdk/server/index.js';
import { StdioServerTransport } from '@modelcontextprotocol/sdk/server/stdio.js';
import { CallToolRequestSchema, ListToolsRequestSchema } from '@modelcontextprotocol/sdk/types.js';
import { AjvJsonSchemaValidator } from '@modelcontextprotocol/sdk/validation/ajv';
import { pathToFileURL } from 'node:url';
import { consoleSession } from './console-config.mjs';

const string = { type: 'string' };
const strings = { type: 'array', items: string };
const object = (properties, required = []) => ({ type: 'object', properties, required, additionalProperties: false });
const id = { type: 'string', minLength: 1, pattern: '^[^/\\\\:?#]+$' };
const node = object({
  id: string, title: string, instruction: string,
  dependsOn: { ...strings, maxItems: 20, uniqueItems: true, description: "IDs of prerequisite steps. Omit for the previous step, use [] for an independent root. Independent document, research and review steps can run in parallel; tasks and approvals run alone." },
  kind: { type: 'string', enum: ['document', 'research', 'check', 'approval', 'task'] },
  acceptance: { ...string, description: 'Optional registered acceptance check ID from list_checks. Ordinary prose completion or review criteria belong in instruction.' }, check: strings, inputs: { type: 'object', additionalProperties: string },
  access: object({ hosts: strings, readPaths: strings, environment: strings }, ['hosts']),
  toolchain: { type: 'boolean' }, sideEffect: { type: 'boolean' },
}, ['id', 'title', 'instruction']);
const workflow = object({
  name: string, kind: { type: 'string', enum: ['nodes', 'task', 'app-ideator'] }, instruction: string,
  nodes: { type: 'array', items: node }, targetRoot: string, check: strings,
  project: string, autoRepair: { type: 'boolean' },
}, ['name', 'kind', 'instruction']);
const projectFields = { name: string, summary: string, declarations: { type: 'object' }, status: string, owner: string };
const workflowBody = (settings) => ({ targetRoot: '', project: '', autoRepair: false, ...settings });
const tools = [
  { name: 'list_projects', description: 'List existing projects and their backend IDs before choosing or updating one.', inputSchema: object({}), method: 'GET', path: () => '/api/projects' },
  { name: 'get_project', description: 'Read one project and its recorded workflow summaries.', inputSchema: object({ id }, ['id']), method: 'GET', path: a => `/api/projects/${encodeURIComponent(a.id)}` },
  { name: 'create_project', description: 'Create a persistent project. Use summary for its purpose. This does not create or execute workflows.', inputSchema: object({ id, ...projectFields }, ['name']), method: 'POST', path: () => '/api/projects', body: a => a },
  { name: 'update_project', description: 'Update named fields of an existing project. Read it first; supply only the intended changes. This does not execute work.', inputSchema: object({ id, changes: { ...object(projectFields), minProperties: 1 } }, ['id', 'changes']), method: 'PATCH', path: a => `/api/projects/${encodeURIComponent(a.id)}`, body: a => a.changes },
  { name: 'list_workflows', description: 'List saved SDK workflows, exact definitions, backend IDs and current revisions before editing one.', inputSchema: object({}), method: 'GET', path: () => '/api/workflows' },
  { name: 'save_workflow', description: 'Create a saved SDK workflow from editable instructions and nodes. Project is an existing project ID. Research and independent review are ordinary node kinds; review expertise belongs in the instruction. Validate nodes before saving. Saving never starts a run.', inputSchema: object({ workflow }, ['workflow']), method: 'POST', path: () => '/api/workflows', body: a => ({ ...workflowBody(a.workflow), id: '', revision: 0 }) },
  { name: 'update_workflow', description: 'Replace an existing saved workflow using its backend ID and current revision from list_workflows. The submitted definition is complete. A stale revision is an error; reread before retrying. This never executes the workflow.', inputSchema: object({ id, revision: { type: 'integer', minimum: 1 }, workflow }, ['id', 'revision', 'workflow']), method: 'PUT', path: a => `/api/workflows/${encodeURIComponent(a.id)}`, body: a => ({ ...workflowBody(a.workflow), id: a.id, revision: a.revision }) },
  { name: 'validate_workflow', description: 'Ask the backend SDK validator to check a node definition and its dependencies without saving or executing it. Preserve and fix its reported findings.', inputSchema: object({ instruction: string, nodes: { type: 'array', items: node } }, ['instruction', 'nodes']), method: 'POST', path: () => '/api/workflows/validate', body: a => a },
  { name: 'list_checks', description: 'Read optional acceptance checks and their backend-owned input requirements. Ordinary nodes do not require a specialized check.', inputSchema: object({}), method: 'GET', path: () => '/api/workflow-checks' },
];
const validator = new AjvJsonSchemaValidator();
const validators = new Map(tools.map(tool => [tool.name, validator.getValidator(tool.inputSchema)]));
const limit = 1024 * 1024;
const redact = (text, token) => String(text).split(token).join('[redacted]');

async function readJSON(response) {
  let size = 0;
  const chunks = [];
  for await (const chunk of response.body) {
    size += chunk.byteLength;
    if (size > limit) throw new Error('Console response exceeds its size limit.');
    chunks.push(chunk);
  }
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}

export function createConsoleMCP(session) {
  const server = new Server({ name: 'hyperplane-console', version: '1.0.0' }, { capabilities: { tools: {} } });
  server.setRequestHandler(ListToolsRequestSchema, async () => ({ tools: tools.map(({ name, description, inputSchema, method }) => ({
    name, description, inputSchema, annotations: { readOnlyHint: method === 'GET' || name === 'validate_workflow', destructiveHint: false, openWorldHint: false },
  })) }));
  server.setRequestHandler(CallToolRequestSchema, async (request, extra) => {
    const tool = tools.find(tool => tool.name === request.params.name);
    const fail = text => ({ isError: true, content: [{ type: 'text', text: redact(text, session.token) }] });
    if (!tool) return fail('Unknown console tool. Only project and saved-workflow management is available.');
    const args = request.params.arguments ?? {};
    const valid = validators.get(tool.name)(args);
    if (!valid.valid) return fail(`Invalid tool arguments: ${valid.errorMessage}`);
    const body = tool.body ? JSON.stringify(tool.body(args)) : undefined;
    if (body && Buffer.byteLength(body) > limit) return fail('Console request exceeds 1 MiB. Shorten the workflow.');
    try {
      const response = await fetch(session.origin + tool.path(args), {
        method: tool.method, headers: { Authorization: `Bearer ${session.token}`, ...(body ? { 'Content-Type': 'application/json' } : {}) },
        body, redirect: 'error', signal: AbortSignal.any([extra.signal, AbortSignal.timeout(30_000)]),
      });
      const data = await readJSON(response);
      if (!response.ok) return fail(`Backend rejected ${tool.name} (${response.status}): ${typeof data.error === 'string' ? data.error : 'Request failed. Recheck the current saved settings before retrying.'}`);
      const backendID = data?.id || data?.project;
      const uiLink = typeof backendID === 'string' && backendID
        ? tool.name.includes('project') ? `/projects/${encodeURIComponent(backendID)}` : tool.name.includes('workflow') ? '/workflows' : undefined
        : undefined;
      return { content: [{ type: 'text', text: redact(JSON.stringify({ data, ...(uiLink ? { uiLink } : {}) }), session.token) }] };
    } catch {
      return fail('The console backend request could not be confirmed. Read current project or workflow records before retrying a write; do not assume it was saved.');
    }
  });
  return server;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const session = consoleSession();
    if (!session) throw new Error('Console management connection is unavailable.');
    const server = createConsoleMCP(session);
    process.stdin.once('end', () => { void server.close(); });
    await server.connect(new StdioServerTransport(process.stdin, process.stdout, { maxBufferSize: 2 * limit }));
  } catch {
    process.stderr.write('Console management tools could not start. Reconnect through the application console.\n');
    process.exitCode = 1;
  }
}
