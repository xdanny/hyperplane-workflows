import { expect, test } from '@playwright/test';
import { finalDocuments, firstLines, nextAction, resultCardVisible, reviewSummary, terminalNodeIds } from '../src/runResult';
import type { SDKWorkflowNode } from '../src/api';
import type { WorkflowNodeCheckpoint } from '../src/workflowNodePresentation';

const sha = (c: string) => c.repeat(64);

const brief: SDKWorkflowNode[] = [
  { id: 'research', kind: 'research', title: 'Research last week’s announcements', instruction: '' },
  { id: 'draft', kind: 'document', title: 'Write the Monday brief', instruction: '' },
  { id: 'review', kind: 'check', title: 'Independent review of the brief', instruction: '', dependsOn: ['draft'] },
];

const checkpoints: WorkflowNodeCheckpoint[] = [
  { id: 'research', phase: 'passed', document: { path: 'research.md', bytes: 900, sha256: sha('a') } },
  { id: 'draft', phase: 'passed', document: { path: 'draft.md', bytes: 1800, sha256: sha('b') } },
  { id: 'review', phase: 'passed', document: { path: 'review.md', bytes: 700, sha256: sha('c') }, findings: ['Non-failing coverage concern: 13 September is not covered.'] },
];

test('the result is what the last steps produced, and a review is evidence about it', () => {
  // `review` ends the graph, but a review document is not the deliverable: the
  // card falls back to the step the review was about.
  expect(terminalNodeIds(brief)).toEqual(['review']);
  const docs = finalDocuments(brief, checkpoints);
  expect(docs.map((d) => d.file.path)).toEqual(['draft.md']);
  expect(docs[0].stepTitle).toBe('Write the Monday brief');
  expect(docs[0].primary).toBe(true);
});

test('a file-creating last step contributes every file it wrote', () => {
  const nodes: SDKWorkflowNode[] = [
    { id: 'profile', kind: 'task', title: 'Write the scorecard files', instruction: '' },
  ];
  const docs = finalDocuments(nodes, [{
    id: 'profile', phase: 'passed',
    document: { path: 'summary.md', bytes: 10, sha256: sha('a') },
    files: [{ path: 'counts.csv', bytes: 20, sha256: sha('b') }],
  }]);
  expect(docs.map((d) => d.file.path)).toEqual(['summary.md', 'counts.csv']);
  expect(docs.map((d) => d.primary)).toEqual([true, false]);
});

test('an approval at the end of the graph points at what it approved', () => {
  const nodes: SDKWorkflowNode[] = [
    { id: 'draft', title: 'Draft the announcement', instruction: '' },
    { id: 'approve', kind: 'approval', title: 'Approve the announcement', instruction: '' },
  ];
  const docs = finalDocuments(nodes, [
    { id: 'draft', phase: 'passed', document: { path: 'draft.md', bytes: 400, sha256: sha('d') } },
    { id: 'approve', phase: 'passed', document: { path: 'approve.md', bytes: 120, sha256: sha('e') } },
  ]);
  expect(docs.map((d) => d.file.path)).toEqual(['draft.md']);
});

test('a run with no recorded files yields no result card rather than a broken link', () => {
  expect(finalDocuments(brief, [{ id: 'review', phase: 'running' }])).toEqual([]);
  expect(finalDocuments(undefined, checkpoints)).toEqual([]);
  // A path with no hash is not evidence; recordedNodeFiles rejects it.
  expect(finalDocuments([{ id: 'draft', title: 'Draft', instruction: '' }], [
    { id: 'draft', phase: 'passed', document: { path: 'draft.md', bytes: 1, sha256: 'nope' } },
  ])).toEqual([]);
});

test('the review line keeps caveats that did not fail the work', () => {
  const passed = reviewSummary(brief, checkpoints)!;
  expect(passed.verdict).toBe('passed');
  expect(passed.line).toBe('Independent review: passed · 1 caveat');
  expect(passed.findings).toHaveLength(1);

  const clean = reviewSummary(brief, checkpoints.map((c) => c.id === 'review' ? { ...c, findings: [] } : c))!;
  // Said out loud rather than left as an absence: a bare "passed" is the
  // wording most easily read as "verified correct".
  expect(clean.line).toBe('Independent review: passed, no caveats');
  expect(clean.caveats).toBe(0);
});

test('recorded levels decide the count, so confirmations stop being caveats (S-17)', () => {
  // The audited line said "passed · 3 caveats" for three findings that all
  // CONFIRMED something. With levels recorded, the line counts one thing.
  const typed = reviewSummary(brief, checkpoints.map((c) => c.id === 'review' ? {
    ...c,
    findings: [
      'CONFIRMED — Every figure traces to the cited source.',
      'CAVEAT — 13 September is not covered.',
      'CONFIRMED — The 150-word limit is met.',
    ],
    findingDetails: [
      { level: 'confirmation' as const, text: 'CONFIRMED — Every figure traces to the cited source.' },
      { level: 'caveat' as const, text: 'CAVEAT — 13 September is not covered.' },
      { level: 'confirmation' as const, text: 'CONFIRMED — The 150-word limit is met.' },
    ],
  } : c))!;
  expect(typed.line).toBe('Independent review: passed · 1 caveat');
  expect(typed.caveats).toBe(1);
  expect(typed.confirmations.map((f) => f.level)).toEqual(['confirmation', 'confirmation']);
  // Every reader that only knows `findings` still gets all three, in order.
  expect(typed.findings).toHaveLength(3);
  expect(typed.details.map((f) => f.level)).toEqual(['confirmation', 'caveat', 'confirmation']);
});

test('a run recorded before levels existed keeps the old heuristic', () => {
  const old = reviewSummary(brief, checkpoints.map((c) => c.id === 'review' ? {
    ...c, findings: ['Confirmed: every figure traces to its source.', 'Coverage note.'],
  } : c))!;
  // Two caveats, over-counted exactly as before — not retro-fitted with a
  // guess at what the reviewer meant.
  expect(old.line).toBe('Independent review: passed · 2 caveats');
  expect(old.details.every((f) => f.level === 'caveat')).toBe(true);
});

test('findingDetails that do not line up with findings are not trusted', () => {
  const mismatched = reviewSummary(brief, checkpoints.map((c) => c.id === 'review' ? {
    ...c,
    findings: ['CAVEAT — a.', 'CONFIRMED — b.'],
    // One level for two findings: pairing them by index would attach the wrong
    // level to the wrong sentence, so the whole record falls back.
    findingDetails: [{ level: 'confirmation' as const, text: 'CONFIRMED — b.' }],
  } : c))!;
  expect(mismatched.details.map((f) => f.level)).toEqual(['caveat', 'caveat']);
  expect(mismatched.line).toBe('Independent review: passed · 2 caveats');

  const malformed = reviewSummary(brief, checkpoints.map((c) => c.id === 'review' ? {
    ...c, findings: ['CAVEAT — a.'],
    findingDetails: [{ level: 'unknown', text: 'CAVEAT — a.' }] as never,
  } : c))!;
  expect(malformed.details.map((f) => f.level)).toEqual(['caveat']);
});

test('a failed review leads with the finding LEVELLED as the failure', () => {
  const failed = reviewSummary(brief, checkpoints.map((c) => c.id === 'review' ? {
    ...c, phase: 'failed',
    findings: ['A note about coverage.', 'Impact claim is unsupported.'],
    findingDetails: [
      { level: 'caveat' as const, text: 'A note about coverage.' },
      // No "FAIL" prefix on the text: the recorded level is what makes this
      // the failure, which is the point of recording it.
      { level: 'fail' as const, text: 'Impact claim is unsupported.' },
    ],
  } : c))!;
  expect(failed.line).toBe('Independent review: failed — Impact claim is unsupported.');
});

test('the page and the card agree about whether there is a result card (S-12)', () => {
  const none = { documents: 0, review: false, nextActionKind: 'none' as const, decision: false };
  expect(resultCardVisible(none)).toBe(false);
  expect(resultCardVisible({ ...none, documents: 1 })).toBe(true);
  expect(resultCardVisible({ ...none, review: true })).toBe(true);
  expect(resultCardVisible({ ...none, nextActionKind: 'sign_off' })).toBe(true);
  // A finished run under an optional policy queues nothing, so the only
  // controls that can decide it are the card's (S-14).
  expect(resultCardVisible({ ...none, decision: true })).toBe(true);
});

test('a failed review leads with the failing finding, not with the run state', () => {
  const failed = reviewSummary(brief, checkpoints.map((c) =>
    c.id === 'review' ? { ...c, phase: 'failed', findings: ['Coverage note.', 'FAIL — Unsupported impact claim.'] } : c))!;
  expect(failed.verdict).toBe('failed');
  expect(failed.line).toBe('Independent review: failed — FAIL — Unsupported impact claim.');
  expect(failed.stepTitle).toBe('Independent review of the brief');
});

test('a workflow with no review step claims no review', () => {
  expect(reviewSummary([{ id: 'draft', title: 'Draft', instruction: '' }], [])).toBeNull();
  expect(reviewSummary(brief, [])!.verdict).toBe('pending');
});

test('the first lines of a document skip its leading blank lines', () => {
  expect(firstLines('\n\n# Title\n\nOne\nTwo\n\nThree\nFour\nFive\nSix\nSeven', 6))
    .toBe('# Title\n\nOne\nTwo\n\nThree\nFour\nFive');
  expect(firstLines('only one line', 6)).toBe('only one line');
});

test('the next-action line names whose turn it is, and stays silent otherwise', () => {
  expect(nextAction({ state: 'running', waitingForPerson: true })).toEqual({ kind: 'approval', text: 'Waiting for your approval below.' });
  expect(nextAction({ state: 'passed', signOffPending: true })).toEqual({ kind: 'sign_off', text: 'Awaiting your sign-off in Inbox.' });
  expect(nextAction({ state: 'passed', signOffPending: false }).kind).toBe('none');
  expect(nextAction({ state: 'failed' }).text).toBe('');
});

test('the review summary carries the review document, so "Read the review" opens it', () => {
  // S-16: "Read the review" opened the check step's session log — "Log: Check
  // the note … no log output" — while review.md sat in the run's outputs.
  const nodes = [
    { id: 'draft', title: 'Write the note', kind: 'document' as const, instruction: 'Write it' },
    { id: 'check', title: 'Check the note', kind: 'check' as const, instruction: 'Review it' },
  ];
  const checkpoints = [
    { id: 'draft', phase: 'passed', document: { path: 'note.md', bytes: 741, sha256: sha('a') } },
    { id: 'check', phase: 'passed', document: { path: 'review.md', bytes: 120, sha256: sha('b') },
      findings: ['121 words differs from the 120-word instruction'] },
  ];
  const review = reviewSummary(nodes, checkpoints);
  expect(review?.stepId).toBe('check');
  expect(review?.document?.path).toBe('review.md');
  expect(review?.document?.sha256).toBe(sha('b'));

  // A review that recorded no document has none — the session log stays the
  // only thing there is to open, and the card says so rather than promising a
  // review it cannot show.
  const bare = reviewSummary(nodes, [{ id: 'check', phase: 'passed' }]);
  expect(bare?.document).toBe(undefined);
});
