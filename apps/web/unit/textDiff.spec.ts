import { expect, test } from '@playwright/test';
import { differenceSummary, textDifference, WORD_DIFF_LIMIT } from '../src/textDiff';

const rebuild = (spans: { op: string; text: string }[], side: 'before' | 'after') =>
  spans.filter((s) => s.op === 'same' || s.op === (side === 'before' ? 'remove' : 'add')).map((s) => s.text).join('');

test('a word-level difference reconstructs both sides exactly', () => {
  const before = 'Linear shipped a new inbox on 2 September.';
  const after = 'Linear shipped a redesigned inbox on 3 September.';
  const diff = textDifference(before, after);
  expect(diff.granularity).toBe('word');
  expect(rebuild(diff.spans, 'before')).toBe(before);
  expect(rebuild(diff.spans, 'after')).toBe(after);
  expect(diff.spans.some((s) => s.op === 'remove' && s.text.includes('new'))).toBe(true);
  expect(diff.spans.some((s) => s.op === 'add' && s.text.includes('redesigned'))).toBe(true);
});

test('identical documents say so instead of showing an empty diff', () => {
  const diff = textDifference('same text', 'same text');
  expect(diff.identical).toBe(true);
  expect(diff.added).toBe(0);
  expect(diff.removed).toBe(0);
  expect(differenceSummary(diff)).toBe('The two documents are identical.');
});

test('the summary counts both directions and refuses to claim it compared meaning', () => {
  const diff = textDifference('one two three', 'one four five six');
  expect(diff.removed).toBe(2);
  expect(diff.added).toBe(3);
  const summary = differenceSummary(diff);
  expect(summary).toContain('2 words removed');
  expect(summary).toContain('3 words added');
  expect(summary).toContain('text difference, not a comparison of meaning');
});

test('an addition to an empty document is all additions', () => {
  const diff = textDifference('', 'a brand new brief');
  expect(diff.removed).toBe(0);
  expect(diff.added).toBe(4);
  expect(rebuild(diff.spans, 'after')).toBe('a brand new brief');
});

test('newlines survive the diff so a rendered document keeps its shape', () => {
  const before = '# Brief\n\n- one\n- two\n';
  const after = '# Brief\n\n- one\n- three\n';
  const diff = textDifference(before, after);
  expect(rebuild(diff.spans, 'before')).toBe(before);
  expect(rebuild(diff.spans, 'after')).toBe(after);
});

test('a document past the word limit falls back to line granularity and says which it used', () => {
  const long = Array.from({ length: WORD_DIFF_LIMIT + 50 }, (_, i) => `word${i}`).join(' ');
  const diff = textDifference(long, `${long} extra`);
  expect(diff.granularity).toBe('line');
  expect(rebuild(diff.spans, 'after').endsWith('extra')).toBe(true);
});
