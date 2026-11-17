import { fileURLToPath } from 'node:url';

export const consoleToolNames = ['list_projects', 'get_project', 'create_project', 'update_project', 'list_workflows', 'save_workflow', 'update_workflow', 'validate_workflow', 'list_checks'];

export function consoleSession(env = process.env) {
  const address = env.HYPERPLANE_CONSOLE_API;
  const token = env.HYPERPLANE_CONSOLE_TOKEN;
  if (!address && !token) return null;
  let url;
  try { url = new URL(address); } catch { throw new Error('Console management connection is invalid. Reconnect the console.'); }
  if (url.protocol !== 'http:' || url.hostname !== '127.0.0.1' || !url.port || url.username || url.password || url.search || url.hash || url.pathname !== '/' || !/^[A-Za-z0-9_-]{16,512}$/.test(token ?? '')) {
    throw new Error('Console management connection is invalid. Reconnect the console.');
  }
  return { origin: url.origin, token };
}

export function consoleClientOptions(options, env = process.env) {
  const enabled = Boolean(consoleSession(env));
  const instructions = enabled
    ? 'You are the Hyperplane console. Use the hyperplane management tools to inspect, create and update projects and saved SDK workflows when the user asks. A request to create or save is a request to call these tools, not merely draft a plan. List existing records before updating to get backend IDs and current workflow revisions. Use ordinary editable node instructions for domain expertise, research and review criteria; do not create specialized agent runtimes. Validate node definitions using validate_workflow and consult list_checks only when an extra check is useful. Only report a project or workflow as created or saved after a successful backend tool result; use the returned IDs and UI links. Errors are not successful writes: explain the error and retain the proposed settings for correction. Saving does not run a workflow. These tools cannot execute or delete work, access arbitrary URLs, or run shell commands. Direct the user to the workflow editor for explicit launch. Never display credentials or environment variables.'
    : 'You are the Hyperplane console in conversation-only mode. Management tools are unavailable in this session. You can discuss or draft project and workflow settings, but cannot create, save, update, execute or delete them. Clearly label drafts and never claim they were saved. Reconnect through the default application console to use management tools.';
  const configOverrides = [...(options.configOverrides ?? []),
    'features.shell_tool=false', 'features.shell_snapshot=false',
    'agents.enabled=false', 'features.multi_agent_v2=false', 'features.goals=false',
    'features.view_image=false', 'features.image_generation=false',
    'shell_environment_policy.exclude=["HYPERPLANE_CONSOLE_API","HYPERPLANE_CONSOLE_TOKEN"]',
  ];
  if (enabled) configOverrides.push(
    `mcp_servers.hyperplane.command=${JSON.stringify(process.execPath)}`,
    `mcp_servers.hyperplane.args=${JSON.stringify([fileURLToPath(new URL('./console-mcp.mjs', import.meta.url))])}`,
    'mcp_servers.hyperplane.env_vars=["HYPERPLANE_CONSOLE_API","HYPERPLANE_CONSOLE_TOKEN"]',
    `mcp_servers.hyperplane.enabled_tools=${JSON.stringify(consoleToolNames)}`,
    'mcp_servers.hyperplane.required=true',
  );
  // Account authentication stays file-backed; the SDK process needs only its
  // runtime environment and this session's private MCP connection.
  const names = ['PATH', 'HOME', 'USER', 'LOGNAME', 'SHELL', 'TMPDIR', 'CODEX_HOME', 'OPENAI_API_KEY', 'CODEX_API_KEY', 'HYPERPLANE_CODEX_EXECUTABLE', 'HYPERPLANE_CONSOLE_API', 'HYPERPLANE_CONSOLE_TOKEN'];
  if (env.HYPERPLANE_CODEX_EXECUTABLE) names.push('SDK_FIXTURE_CALLS');
  const clientEnv = Object.fromEntries(names.filter(name => typeof env[name] === 'string').map(name => [name, env[name]]));
  return { ...options, env: clientEnv, config: { ...options.config, project_doc_max_bytes: 0, developer_instructions: instructions }, configOverrides };
}
