import { runTask } from './task.mjs';
import { verifyIdeator } from './verify-ideator.mjs';

// A recipe for the shared SDK task runner. Only its instructions and acceptance
// differ; conversation, permissions, correction and checkpoint handling are shared.
export function runIdeator({ workspace, domain = 'offline tools for weekend cyclists', model = 'gpt-6-astra', injectFailure = false,
  signal, maxCorrections, maxTokens, outputSchema, onEvent, checkpoint }) {
  return runTask({ workspace, model, signal, maxCorrections, maxTokens, outputSchema, onEvent, checkpoint,
    instruction: `Create three small app ideas for this domain: ${JSON.stringify(domain)}.
Write ideas.json as {"ideas":[{"title":string,"user":string,"problem":string,"firstVersion":string}]}.
Write brief.md comparing every idea by its title, recommending one small first experiment and explicitly stating these are unvalidated hypotheses.
Do not claim market research. Work only in this directory. Do not install tools. Finish after writing the two files.
${injectFailure ? 'For this controlled failure-injection trial only, your FIRST response must write exactly ONE idea and its brief. A separate validator will then provide feedback.' : ''}`,
    acceptance: {
      description: 'After your turn, the caller checks ideas.json and brief.md against fixed output requirements. Do not weaken those requirements or claim market validation.',
      verify: () => verifyIdeator(workspace),
    },
  });
}
