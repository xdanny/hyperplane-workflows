import { Codex } from '@openai/codex-sdk';
import { fileURLToPath } from 'node:url';
import { runTurn } from './turn.mjs';

export { Codex } from '@openai/codex-sdk';

// Codex still loads its existing account authentication. Workflow instructions
// must not acquire ambient skills or host integrations. Both skill settings are
// exercised against the pinned executable's actual provider request in tests.
const isolatedConfig = [
  'mcp_servers={}', 'features.plugins=false', 'features.apps=false',
  'features.hooks=false', 'features.memories=false', 'features.multi_agent=false',
  'skills.include_instructions=false', 'features.skill_search=false',
];
export function workflowCodex(options = {}) {
  return new Codex({
    ...options,
    configOverrides: [...isolatedConfig, ...(options.config?.default_permissions ? [] : ['sandbox_workspace_write.exclude_tmpdir_env_var=true', 'sandbox_workspace_write.exclude_slash_tmp=true']), ...(options.configOverrides || [])],
    codexPathOverride: fileURLToPath(new URL('../bin/codex-isolated', import.meta.url)),
  });
}

// The supplied object is an actual Codex SDK Thread, including resumeThread().
// Verification is application code; this extension never owns a second agent
// tool loop, transcript store, or model transport.
export async function runChecked(thread, input, {
  verify,
  maxCorrections = 0,
  maxTokens = Infinity,
  signal,
  outputSchema,
  onEvent = async () => {},
  checkpoint = async () => {},
} = {}) {
  if (typeof verify !== 'function') throw new Error('A verifier is required');
  if (!Number.isInteger(maxCorrections) || maxCorrections < 0 || maxCorrections > 5) {
    throw new Error('maxCorrections must be an integer from 0 to 5');
  }
  if (!(maxTokens > 0)) throw new Error('maxTokens must be positive');
  let prompt = input;
  let findings = [];
  const usage = { input_tokens: 0, cached_input_tokens: 0, output_tokens: 0 };
  for (let attempt = 0; attempt <= maxCorrections; attempt++) {
    signal?.throwIfAborted();
    const turn = await runTurn(thread, prompt, { signal, outputSchema, onEvent: async (event) => {
      await onEvent(event);
      if (event.type === 'thread.started') {
        await checkpoint({ threadId: thread.id, phase: 'running', attempt, usage: { ...usage }, findings });
      }
    } });
    const { text } = turn;
    for (const key of Object.keys(usage)) usage[key] += turn.usage[key] ?? 0;
    await checkpoint({ threadId: thread.id, phase: 'verifying', attempt, usage: { ...usage } });
    // Verifier infrastructure errors propagate. They are never model findings.
    const verdict = await verify({ text, threadId: thread.id, attempt, signal });
    if (!verdict || typeof verdict.passed !== 'boolean' || !Array.isArray(verdict.findings) ||
        verdict.findings.some((finding) => typeof finding !== 'string') ||
        (verdict.passed && verdict.findings.length !== 0) ||
        (!verdict.passed && verdict.findings.length === 0)) {
      throw new Error('Verifier returned an invalid verdict');
    }
    signal?.throwIfAborted();
    findings = verdict.findings;
    const result = { threadId: thread.id, text, usage: { ...usage }, attempts: attempt + 1, passed: verdict.passed, findings: verdict.findings };
    if (verdict.passed || attempt === maxCorrections || usage.input_tokens + usage.output_tokens >= maxTokens) {
      await checkpoint({ ...result, phase: verdict.passed ? 'passed' : 'failed' });
      return result;
    }
    await checkpoint({ ...result, phase: 'correction_pending' });
    // maxTokens is a between-turn budget: an in-flight model turn may exceed it.
    // Enable corrections only for workspace-contained work the caller can replay.
    prompt = 'The independent checks rejected the delivery. Correct the artifacts against these unchanged checks.\n' +
      JSON.stringify(verdict.findings) + '\nDo not weaken the checks or claim a pass before they run.';
  }
}
