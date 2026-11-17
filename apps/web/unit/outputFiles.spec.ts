/**
 * Comparing produced files by checksum (M-18): every path either run recorded,
 * with a verdict that claims only what a hash comparison can claim.
 */

import { expect, test } from '@playwright/test';
import { compareOutputFiles, comparisonLabel, deliveredDocument, outputFilesSummary } from '../src/outputFiles';

const hash = (c: string) => c.repeat(64);

const olderFiles = [
  { stepId: 'draft', stepTitle: 'Write the Monday brief', path: 'draft.md', bytes: 1700, sha256: hash('a') },
  { stepId: 'totals', stepTitle: 'Recompute the totals', path: 'build/totals.csv', bytes: 900, sha256: hash('b') },
  { stepId: 'totals', stepTitle: 'Recompute the totals', path: 'build/dropped.csv', bytes: 40, sha256: hash('c') },
];
const newerFiles = [
  { stepId: 'draft', stepTitle: 'Write the Monday brief', path: 'draft.md', bytes: 1800, sha256: hash('d') },
  { stepId: 'totals', stepTitle: 'Recompute the totals', path: 'build/totals.csv', bytes: 900, sha256: hash('b') },
  { stepId: 'totals', stepTitle: 'Recompute the totals', path: 'build/rejected.csv', bytes: 12, sha256: hash('e') },
];

test('files are same, changed, or only in one run — by hash, sorted by path', () => {
  const compared = compareOutputFiles(olderFiles, newerFiles);
  expect(compared.map((file) => [file.path, file.status])).toEqual([
    ['build/dropped.csv', 'only_older'],
    ['build/rejected.csv', 'only_newer'],
    ['build/totals.csv', 'same'],
    ['draft.md', 'changed'],
  ]);
  // The step that produced it travels with the row, so a changed file is
  // traceable to the step a reader would open.
  expect(compared.find((file) => file.path === 'draft.md')?.stepTitle).toBe('Write the Monday brief');
  // Case never decides a comparison: a hash is a hash.
  expect(compareOutputFiles(
    [{ path: 'x', sha256: hash('a').toUpperCase() }],
    [{ path: 'x', sha256: hash('a') }],
  )[0].status).toBe('same');
});

test('a file with no recorded checksum is not guessed either way', () => {
  const compared = compareOutputFiles([{ path: 'x', bytes: 4 }], [{ path: 'x', bytes: 4 }]);
  expect(compared[0].status).toBe('unknown');
  expect(comparisonLabel('unknown')).toBe('cannot be compared — no checksum recorded');
  // Equal byte counts are not equal bytes, and this never says they are.
  expect(comparisonLabel('same')).toBe('same');
  expect(comparisonLabel('changed')).toBe('changed');
});

test('"only in run #N" names the run it is missing from', () => {
  expect(comparisonLabel('only_older', 2, 3)).toBe('only in run #2');
  expect(comparisonLabel('only_newer', 2, 3)).toBe('only in run #3');
});

test('the summary counts every kind, and an empty comparison says nothing', () => {
  expect(outputFilesSummary(compareOutputFiles(olderFiles, newerFiles), 2, 3))
    .toBe('1 file changed, 1 the same, 1 only in run #2, 1 only in run #3.');
  expect(outputFilesSummary(compareOutputFiles(undefined, undefined))).toBe('');
  expect(outputFilesSummary(compareOutputFiles([{ path: 'x', sha256: hash('a') }], [{ path: 'x', sha256: hash('a') }]), 1, 2))
    .toBe('1 the same.');
});

test('a malformed record cannot make the table lie or duplicate a row', () => {
  const compared = compareOutputFiles(
    [{ path: '', sha256: hash('a') }, { path: 'x', sha256: hash('a') }, { path: 'x', sha256: hash('f') }],
    [{ path: 'x', sha256: hash('a') }],
  );
  expect(compared).toHaveLength(1);
  expect(compared[0].status).toBe('same');
});

test('the deliverable is the last document that is not a review or an approval', () => {
  // S-16: the compare view diffed "# Check the note" against "# Check the
  // note" and called the note unchanged, because the review step records a
  // document too and it is the LAST one.
  const documents = [
    { stepId: 'draft', kind: 'document', title: 'Write the note', path: 'note.md' },
    { stepId: 'check', kind: 'check', title: 'Check the note', path: 'review.md' },
  ];
  expect(deliveredDocument(documents)?.path).toBe('note.md');

  // An approval's record is the receipt for a decision, not the deliverable.
  expect(deliveredDocument([
    { stepId: 'draft', kind: 'document', path: 'note.md' },
    { stepId: 'sign', kind: 'approval', path: 'sign.md' },
  ])?.path).toBe('note.md');

  // Several deliverables: the last of them, as before.
  expect(deliveredDocument([
    { stepId: 'a', kind: 'document', path: 'one.md' },
    { stepId: 'b', kind: 'task', path: 'two.md' },
    { stepId: 'c', kind: 'check', path: 'review.md' },
  ])?.path).toBe('two.md');

  // A row from a build that recorded no kinds keeps the old answer rather than
  // silently claiming a run delivered nothing.
  expect(deliveredDocument([{ stepId: 'a', path: 'one.md' }, { stepId: 'b', path: 'two.md' }])?.path).toBe('two.md');
  // And a run that is ONLY a review has nothing else to offer.
  expect(deliveredDocument([{ stepId: 'c', kind: 'check', path: 'review.md' }])?.path).toBe('review.md');
  expect(deliveredDocument([])).toBe(null);
  expect(deliveredDocument(undefined)).toBe(null);
});
