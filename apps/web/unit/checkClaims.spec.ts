import { expect, test } from '@playwright/test';
import { CHECK_CLAIM_HEADINGS, checkClaims } from '../src/api';
import type { WorkflowCheck } from '../src/api';

/**
 * D-06: the check contract was one ~450-word paragraph whose key sentence —
 * the boundary — nobody was going to find. These are the four questions the
 * catalog now answers separately, and the heading each answer is filed under.
 */

const check: WorkflowCheck = {
  id: 'prediction-metrics',
  title: 'Recompute prediction metrics',
  description: 'Recomputes the submitted labels and scores.',
  produces: ['predictions.csv with one row per holdout record.'],
  recomputes: ['PR-AUC and precision at the declared operating point, from your submitted file.'],
  proves: ['The reported metrics match what the submitted predictions actually score.'],
  doesNotProve: [
    'Label or holdout custody.',
    'Train/holdout isolation.',
    'That the model was frozen before the holdout was scored.',
  ],
};

test('each array is filed under the question it answers', () => {
  expect(CHECK_CLAIM_HEADINGS.produces).toBe('Files you must produce');
  expect(CHECK_CLAIM_HEADINGS.recomputes).toBe('What the host recomputes');
  expect(CHECK_CLAIM_HEADINGS.proves).toBe('What a pass proves');
  expect(CHECK_CLAIM_HEADINGS.doesNotProve).toBe('What it does not prove');
  expect(checkClaims(check, 'doesNotProve')).toHaveLength(3);
  expect(checkClaims(check, 'proves')[0]).toContain('match what the submitted predictions');
});

test('a catalog entry from before the contract renders no empty headings', () => {
  const old: WorkflowCheck = { id: 'old', title: 'Old check', description: 'One paragraph.' };
  expect(checkClaims(old, 'produces')).toEqual([]);
  expect(checkClaims(old, 'doesNotProve')).toEqual([]);
  expect(checkClaims(undefined, 'proves')).toEqual([]);
});

test('blank and non-string entries are dropped rather than rendered as bullets', () => {
  const ragged = {
    ...check,
    produces: ['metrics.json', '   ', 42, null, 'notes.md'],
  } as unknown as WorkflowCheck;
  expect(checkClaims(ragged, 'produces')).toEqual(['metrics.json', 'notes.md']);
});
