// Auxiliary conversations are read-only. Writable work and explicit connections
// use the managed task runtime, which owns their permissions and lifecycle.
export function workerOptions(request) {
  const sandbox = request.SandboxMode || 'read-only';
  if (sandbox !== 'read-only') throw new Error('Auxiliary SDK calls are read-only; use a managed SDK task for writable work');
  if (['Toolchain', 'WritableDirs', 'Context7'].some(key => Object.hasOwn(request, key))) {
    throw new Error('Auxiliary SDK calls do not accept tool or connection grants; use a managed SDK task');
  }
  const config = { mcp_servers: {} };
  if (request.BaseURL) {
    config.model_provider = 'local';
    config.model_providers = { local: { name: 'local', base_url: request.BaseURL, wire_api: 'responses', requires_openai_auth: false } };
  } else if (request.OSS) {
    config.model_provider = request.LocalProvider || 'lmstudio';
  }
  return {
    clientOptions: { config, configOverrides: [] },
    threadOptions: {
      model: request.Model || undefined,
      workingDirectory: request.Cwd || undefined,
      sandboxMode: sandbox,
      skipGitRepoCheck: true,
      modelReasoningEffort: request.Reasoning || 'low',
      networkAccessEnabled: false,
      webSearchMode: request.WebSearch ? 'live' : 'disabled',
      approvalPolicy: 'never',
      additionalDirectories: [],
      threadSource: 'hyperplane',
    },
  };
}
