import { expect, test } from '@playwright/test';
import { expandedReason, fullFailureReason, isElided } from '../src/failureReason';
import type { Run } from '../src/api';

/**
 * Q-12: "Show full reason" expanded to a sentence that still ended in an
 * ellipsis, because the server shortens the reason to 200 runes before the
 * card ever sees it. These pin both halves — recognising the cut, and
 * recovering the whole sentence from the run's own record.
 */

const run = (doc: Record<string, unknown>, failingStep?: { id: string; title: string; kind: string }): Run => ({
  runId: 'r1', dag: 'd', state: 'failed', startedAt: '', updatedAt: '',
  doc: { sdk: doc }, ...(failingStep ? { failingStep } : {}),
}) as Run;

test('an elided reason is recognised however it was cut', () => {
  expect(isElided('The note skipped checks,…')).toBe(true);
  expect(isElided('The note skipped checks,... ')).toBe(true);
  expect(isElided('The note skipped every declared check.')).toBe(false);
  expect(isElided('')).toBe(false);
});

test('the whole reason comes from the failing step the card already links to', () => {
  const record = run({
    currentNode: 'review',
    nodes: [
      { id: 'draft', findings: [] },
      { id: 'review', findings: ['The note skipped checks, and its impact claim is unsupported.', 'Coverage is thin.'] },
    ],
  }, { id: 'review', title: 'Independent review', kind: 'check' });
  expect(fullFailureReason(record)).toBe(
    'The note skipped checks, and its impact claim is unsupported.; Coverage is thin.',
  );
});

test('a recorded error outranks findings, and run-level findings outrank a step', () => {
  expect(fullFailureReason(run({ error: '  The session ran out of budget.  ', findings: ['ignored'] })))
    .toBe('The session ran out of budget.');
  expect(fullFailureReason(run({ findings: ['Two of three checks failed.'], currentNode: 'x', nodes: [{ id: 'x', findings: ['nope'] }] })))
    .toBe('Two of three checks failed.');
  // Nothing recorded is reported as nothing, never as an invented sentence.
  expect(fullFailureReason(run({}))).toBe('');
  expect(fullFailureReason(undefined)).toBe('');
});

test('expanding replaces the clip only with text that continues it', () => {
  const clipped = 'The note skipped checks,…';
  const whole = 'The note skipped checks, and its impact claim is unsupported.';
  expect(expandedReason(clipped, whole)).toEqual({ text: whole, complete: true });

  // A reason that was never cut is already complete and needs no lookup.
  expect(expandedReason(whole, '')).toEqual({ text: whole, complete: true });

  // The run's record says something ELSE now. Swapping one sentence for a
  // different sentence under a button labelled "Show full reason" is worse
  // than showing a short one, so the card keeps what it was given and says so.
  expect(expandedReason(clipped, 'A completely different finding about coverage.'))
    .toEqual({ text: clipped, complete: false });

  // Nothing recovered at all: still incomplete, still the delivered text.
  expect(expandedReason(clipped, '')).toEqual({ text: clipped, complete: false });
});

test('recovered text is whitespace-normalised before it replaces the clip', () => {
  const clipped = 'The note skipped checks,…';
  const messy = 'The note skipped checks,\n  and its impact claim   is unsupported.';
  expect(expandedReason(clipped, messy).text).toBe('The note skipped checks, and its impact claim is unsupported.');
});
