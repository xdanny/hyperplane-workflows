import { getCheck } from '../../src/workflows/check-catalog.mjs';

// Complete declaration fixtures; data references deliberately need not exist.
export function declaredTask(acceptance, id = 'work') {
  const definition = getCheck(acceptance);
  const inputs = Object.fromEntries((definition.inputs?.required ?? []).map(key => [key, `Declared ${key}`]));
  if (definition.inputs) {
    if ('keyColumns' in inputs) inputs.keyColumns = 'id';
    const specific = {
      'dataset-shift-profile': { features: JSON.stringify([{ feature: 'category', type: 'categorical', whyItMatters: 'Report totals', statistic: 'category-emergence', tolerance: 2, unit: 'points_of_share' }]) },
      'reconciliation-profile': { measures: JSON.stringify([{ measure: 'total', definitionA: 'Sum amount', definitionB: 'Sum amount' }]), tolerances: JSON.stringify([{ measure: 'total', unit: 'exact', value: '0' }]) },
      'pipeline-change-profile': { invariants: JSON.stringify([{ invariant: 'total', measure: 'Sum amount', kind: 'number', unit: 'exact', tolerance: '0' }]) },
      'data-pipeline-profile': { measures: JSON.stringify([{ measure: 'total', definition: 'Sum amount', unit: 'exact', tolerance: '0' }]) },
      'prediction-metrics': { successMetric: 'AUROC >= 0.75' },
    };
    Object.assign(inputs, specific[acceptance]);
  }
  return { id, title: 'Work', instruction: 'Do the work', kind: 'task', acceptance, ...(definition.inputs ? { inputs } : {}) };
}
