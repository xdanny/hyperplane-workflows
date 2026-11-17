/**
 * How an advisory persona verdict is labelled (D-18).
 *
 * "DEV — SENIOR ML ENGINEER · PASS" sat beside a run chip reading "REVIEW
 * FAILED" and read as the platform contradicting itself. The two judge
 * different things over different scopes, and the chip has to say so.
 */

import { expect, test } from '@playwright/test';
import { firstSentence, verdictChip } from '../src/components/PersonaVerdict';

test('a verdict chip names the reviewer, the verdict and that it decides nothing', () => {
  expect(verdictChip({ persona: 'dev', personaName: 'Dev — senior ML engineer', verdict: 'pass' }))
    .toBe('Reviewer Dev: PASS (advisory)');
  expect(verdictChip({ persona: 'sofia', personaName: 'Sofia — senior analyst', verdict: 'mixed' }))
    .toBe('Reviewer Sofia: MIXED (advisory)');
  // A persona with no separate title keeps the whole name.
  expect(verdictChip({ persona: 'testy', personaName: '', verdict: 'revise' }))
    .toBe('Reviewer testy: REVISE (advisory)');
});

test('the scope beside the chip is the reviewer’s own first sentence', () => {
  expect(firstSentence('PASS within the recorded negative-test scope. The rest was not exercised.'))
    .toBe('PASS within the recorded negative-test scope.');
  expect(firstSentence('  a  sentence   with   spacing  ')).toBe('a sentence with spacing');
  expect(firstSentence(undefined)).toBe('');
  expect(firstSentence('x'.repeat(300))).toHaveLength(201);
});
