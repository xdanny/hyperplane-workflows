import test from 'node:test';
import assert from 'node:assert/strict';
import { workerOptions } from '../src/worker-options.mjs';

test('auxiliary calls default to read-only and reject retired grants', () => {
  const { clientOptions, threadOptions } = workerOptions({ Cwd: '/work' });
  assert.equal(threadOptions.sandboxMode, 'read-only');
  assert.equal(threadOptions.networkAccessEnabled, false);
  assert.equal(threadOptions.approvalPolicy, 'never');
  assert.deepEqual(threadOptions.additionalDirectories, []);
  assert.deepEqual(clientOptions.config.mcp_servers, {});
  for (const request of [{ SandboxMode: 'workspace-write' }, { SandboxMode: 'danger-full-access' }, { Toolchain: false }, { WritableDirs: [] }, { Context7: true }]) {
    assert.throws(() => workerOptions(request), /managed SDK task/);
  }
});

test('custom inference endpoint retains local harness placement', () => {
  const options = workerOptions({ BaseURL: 'http://localhost:8888/v1', Model: 'local-model', Cwd: '/work', Reasoning: 'high', WebSearch: true }, {});
  assert.equal(options.clientOptions.config.model_provider, 'local');
  assert.equal(options.clientOptions.config.model_providers.local.wire_api, 'responses');
  assert.equal(options.clientOptions.config.model_providers.local.requires_openai_auth, false);
  assert.equal(options.threadOptions.workingDirectory, '/work');
  assert.equal(options.threadOptions.model, 'local-model');
  assert.equal(options.threadOptions.webSearchMode, 'live');
});
