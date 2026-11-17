import { validateCheck } from '../validation.mjs';
export { validateCheck } from '../validation.mjs';
import { predictionChecks, predictionAcceptance, checkPredictionInputs } from './prediction-metrics.mjs';
import { listChecks, getCheck } from './check-catalog.mjs';
import { qualityChecks, qualityAcceptance, checkQualityInputs } from './data-quality.mjs';
import { shiftChecks, shiftAcceptance } from './dataset-shift.mjs';
import { checkShiftInputs } from './shift-inputs.mjs';
import { reconciliationChecks, reconciliationAcceptance, checkReconciliationInputs } from './reconciliation.mjs';
import { pipelineChangeChecks, pipelineChangeAcceptance, checkPipelineChangeInputs } from './pipeline-change.mjs';
import { pipelineChecks, pipelineAcceptance, checkPipelineInputs } from './data-pipeline.mjs';

export { listChecks, getCheck };

// Only trusted application implementations are executable. The shared JSON
// catalog declares their public requirements; it cannot introduce host code.
const adapters = new Map();
for (const [ids, acceptance, preflight] of [
  [predictionChecks, predictionAcceptance, checkPredictionInputs],
  [qualityChecks, qualityAcceptance, checkQualityInputs],
  [shiftChecks, shiftAcceptance, checkShiftInputs],
  [reconciliationChecks, reconciliationAcceptance, checkReconciliationInputs],
  [pipelineChangeChecks, pipelineChangeAcceptance, checkPipelineChangeInputs],
  [pipelineChecks, pipelineAcceptance, checkPipelineInputs],
]) {
  for (const id of ids) {
    if (adapters.has(id)) throw new Error(`Duplicate SDK check adapter: ${id}`);
    const definition = getCheck(id);
    adapters.set(id,{ acceptance, preflight: definition.inputs ? preflight : undefined });
  }
}
if (listChecks().some(check=>!adapters.has(check.id))) throw new Error('SDK check catalog has no implementation');

export async function preflightCheck(node, { signal } = {}) {
  signal?.throwIfAborted();
  if (node.acceptance === undefined) return;
  getCheck(node.acceptance);
  await adapters.get(node.acceptance).preflight?.(node.inputs);
  signal?.throwIfAborted();
}

export function createAcceptance(node, workspace, previous = [], { signal } = {}) {
  validateCheck(node, previous);
  if (node.acceptance === undefined) return undefined;
  return adapters.get(node.acceptance).acceptance(node.acceptance, workspace, previous, node.inputs, { signal });
}
