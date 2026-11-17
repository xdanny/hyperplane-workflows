import { expect, test } from '@playwright/test';
import { attemptReconciliation, proofChipLabel, proofChipClass, proofChipHint, attemptLabel } from '../src/journey';
import type { JourneyAttempt } from '../src/api';

const attempt: JourneyAttempt = { runId: 'stopped', number: 1, dag: 'node-workflow', state: 'interrupted',
  superseded: false, criteria: 2, proven: 2, updatedAt: '2026-09-07T10:00:00Z' };

test('a stopped delivery keeps its stopped label when earlier checks passed', () => {
  expect(attemptReconciliation({ ...attempt, failingNode: 'summary' })).toBe('2/2 checks passed · stopped at summary');
  expect(attemptReconciliation(attempt)).toBe('2/2 checks passed · stopped after the last check');
  expect(attemptReconciliation({ ...attempt, state: 'failed' })).toBe('2/2 checks passed · failed after the last check');
  expect(attemptReconciliation({ ...attempt, criteria: 0, proven: 0 })).toBeNull();
});

test('completion and human acceptance do not invent a check result', () => {
  for (const maturity of ['unproven', 'proven_once', 'accepted'] as const) {
    expect(proofChipLabel(maturity, { criteria: 0, proven: 0 })).toBe('No extra checks');
    expect(proofChipClass(maturity, { criteria: 0, proven: 0 })).toBe('s-idle');
    expect(proofChipHint(maturity, { criteria: 0, proven: 0 })).toContain('do not establish independently verified correctness');
  }
  expect(proofChipLabel('proven_once', { criteria: 2, proven: 1, attempt: 2, attemptCount: 2 })).toBe('1/2 checks passed · attempt 2');
  expect(proofChipClass('proven_once', { criteria: 2, proven: 1 })).toBe('s-idle');
  expect(attemptLabel({ ...attempt, number: 2, continuedFrom: 'source' })).toBe('Attempt 2 · continued');
});
