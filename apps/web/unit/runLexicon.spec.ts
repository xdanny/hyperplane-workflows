import { expect, test } from '@playwright/test';
import {
  CHIP_LABEL,
  acceptanceLine,
  acceptanceSuffix,
  clip,
  correctionsUsed,
  failedHeadline,
  failingRowText,
  formatAgo,
  formatDate,
  formatDateTime,
  formatDuration,
  formatTokens,
  headerMeta,
  needsYouChip,
  needsYouTitle,
  passRateSentence,
  quotedNote,
  runChipClass,
  runPresentation,
  runTitle,
  humanizeSlotId,
  runHeading,
  slotLabel,
  slotsSummary,
  sortRunsByStarted,
  stepSummaryLine,
} from '../src/runLexicon';
import { AUTO_DECIDER } from '../src/api';
import type { SDKWorkflowNode } from '../src/api';

const nodes: SDKWorkflowNode[] = [
  { id: 'research', kind: 'research', title: 'Research last week’s announcements', instruction: '' },
  { id: 'review', kind: 'check', title: 'Independent review of the brief', instruction: '' },
  { id: 'files', kind: 'task', title: 'Write the scorecard files', instruction: '' },
];

test('a failed run names WHAT failed, never a bare FAILED or an internal node id', () => {
  expect(runPresentation({ state: 'failed', failingStep: { id: 'review', title: 'Independent review of the brief', kind: 'check' } })).toBe('review_failed');
  expect(runPresentation({ state: 'failed', failingStep: { id: 'files', title: 'Write the scorecard files', kind: 'task' } })).toBe('check_failed');
  expect(runPresentation({ state: 'failed', failingStep: null })).toBe('failed');
  expect(CHIP_LABEL.review_failed).toBe('REVIEW FAILED');
  expect(CHIP_LABEL.check_failed).toBe('CHECK FAILED');
});

test('a run parked on a person says WAITING FOR YOU although the store still says running', () => {
  expect(runPresentation({ state: 'running', waitingForPerson: true })).toBe('waiting');
  expect(CHIP_LABEL[runPresentation({ state: 'running', waitingForPerson: true })]).toBe('WAITING FOR YOU');
  expect(runChipClass({ state: 'running', waitingForPerson: true })).toBe('s-block');
  expect(runPresentation({ state: 'running' })).toBe('running');
  expect(runPresentation({ state: 'blocked' })).toBe('waiting');
  expect(CHIP_LABEL[runPresentation({ state: 'interrupted' })]).toBe('STOPPED');
  expect(CHIP_LABEL[runPresentation({ state: 'passed' })]).toBe('PASSED');
});

test('a node workflow reports steps and corrections, never "attempts"', () => {
  expect(stepSummaryLine(3, 0, true)).toBe('3 steps · 0 corrections used (1 allowed per step)');
  expect(stepSummaryLine(3, 1, true)).toBe('3 steps · 1 correction used (1 allowed per step)');
  expect(stepSummaryLine(3, 0, false)).toBe('3 steps · no automatic correction');
  expect(stepSummaryLine(1, 0, false)).toBe('1 step · no automatic correction');
  expect(stepSummaryLine(2, 0, true)).not.toContain('attempt');
});

test('corrections count step retries, not steps', () => {
  expect(correctionsUsed([{ id: 'a', phase: 'passed', attempts: 1 }, { id: 'b', phase: 'passed', attempts: 1 }])).toBe(0);
  expect(correctionsUsed([{ id: 'a', phase: 'passed', attempts: 2 }, { id: 'b', phase: 'passed' }])).toBe(1);
  expect(correctionsUsed(undefined)).toBe(0);
});

test('failure wording names the step and quotes the first finding, clipped with an ellipsis', () => {
  expect(failingRowText({ title: 'Independent review of the brief' }, 'FAIL — Unsupported impact claim about onboarding time.'))
    .toBe('Independent review of the brief failed — FAIL — Unsupported impact claim about onboarding time.');
  expect(failingRowText({ title: 'Independent review of the brief' })).toBe('Independent review of the brief failed');
  expect(failingRowText(null)).toBeNull();
  const long = failingRowText({ title: 'Review' }, 'x'.repeat(300))!;
  expect(long.endsWith('…')).toBe(true);
  expect(long.length).toBeLessThan(160);
  expect(clip('a  b\n c', 99)).toBe('a b c');
  // Q-12: the cut lands on a word boundary however early it falls, so no
  // surface can produce "…how we should re". Only a single token longer than
  // the whole budget has no boundary to find.
  expect(clip('reconsider everything', 12)).toBe('reconsider…');
  expect(clip('supercalifragilistic', 12)).toBe('supercalifra…');
});

test('the run page headline says which step and which kind of failure', () => {
  expect(failedHeadline({ id: 'review', title: 'Independent review of the brief', kind: 'check' }, nodes))
    .toBe('Independent review failed at step 2 — Independent review of the brief');
  expect(failedHeadline({ id: 'files', title: 'Write the scorecard files', kind: 'task' }, nodes))
    .toBe('Acceptance check failed at step 3 — Write the scorecard files');
  expect(failedHeadline({ id: 'gone', title: 'A step no longer in the graph' }, nodes))
    .toBe('Step failed — A step no longer in the graph');
  expect(failedHeadline(null, nodes)).toBeNull();
});

test('durations and token counts carry their units', () => {
  expect(formatDuration(43_000)).toBe('43 s');
  expect(formatDuration(163_200)).toBe('2 min 43 s');
  expect(formatDuration(120_000)).toBe('2 min');
  expect(formatDuration(3_840_000)).toBe('1 h 4 min');
  expect(formatDuration(null)).toBeNull();
  expect(formatDuration(undefined)).toBeNull();
  expect(formatTokens(428_982)).toBe('428.9k');
  expect(formatTokens(3_600)).toBe('3.6k');
  expect(formatTokens(428)).toBe('428');
  expect(formatTokens(2_600_000)).toBe('2.6M');
  expect(formatTokens(null)).toBeNull();
});

test('the header meta line drops what it does not know rather than printing a dash', () => {
  expect(headerMeta({ chip: 'PASSED', durationMs: 163_200, usage: { input_tokens: 428_982, output_tokens: 3_600 }, model: 'gpt-6-astra' }))
    .toBe('PASSED · 2 min 43 s · 428.9k in / 3.6k out · gpt-6-astra');
  expect(headerMeta({ chip: 'RUNNING', durationMs: null })).toBe('RUNNING');
  expect(headerMeta({ chip: 'PASSED', durationMs: 38_000, model: 'gpt-6-astra' })).toBe('PASSED · 38 s · gpt-6-astra');
});

test('dates are unambiguous and relative time is reserved for staleness', () => {
  expect(formatDateTime('2026-09-12T13:02:00Z', 'UTC')).toBe('12 Sep 13:02');
  expect(formatDate('2026-09-12T13:02:00Z', 'UTC')).toBe('12 Sep 2026');
  expect(formatDateTime(undefined)).toBe('');
  expect(formatDateTime('not a date')).toBe('');
  const now = Date.parse('2026-09-12T13:05:00Z');
  expect(formatAgo('2026-09-12T13:02:00Z', now)).toBe('3 min ago');
  expect(formatAgo('2026-09-12T13:04:56Z', now)).toBe('just now');
});

test('a run is titled by its workflow and run number, falling back to the goal', () => {
  expect(runTitle({ workflowName: 'Weekly competitor brief', runNumber: 3 })).toBe('Weekly competitor brief · run #3');
  expect(runTitle({ workflowName: 'Weekly competitor brief' })).toBe('Weekly competitor brief');
  expect(runTitle({ goal: 'Summarize what Linear announced', dag: 'node-workflow' })).toBe('Summarize what Linear announced');
  expect(runTitle({ dag: 'node-workflow' })).toBe('node-workflow');
});

test('the second line summarises the inputs that tell two runs apart, under their labels', () => {
  // The workflow's own words when the launch froze them (P-18)…
  const labels = { week: 'Week to cover', competitors: 'Competitors' };
  expect(slotsSummary({ week: '31 Aug–6 Sep', competitors: 'Linear, Notion', audience: 'Product' }, 2, labels))
    .toBe('Week to cover: 31 Aug–6 Sep · Competitors: Linear, Notion · +1 more');
  // …and a readable fallback for a run that recorded none, never a bare id.
  expect(slotsSummary({ weekToCover: '31 Aug–6 Sep' })).toBe('Week to cover: 31 Aug–6 Sep');
  expect(slotsSummary({ week: '  ' })).toBe('');
  expect(slotsSummary(null)).toBe('');
  expect(humanizeSlotId('weekToCover')).toBe('Week to cover');
  expect(humanizeSlotId('expense_csv_path')).toBe('Expense csv path');
  expect(slotLabel('week', { week: ' Week to cover ' })).toBe('Week to cover');
  expect(slotLabel('week', { week: '  ' })).toBe('Week');
});

test('one acceptance has one acceptor, and a pending sign-off says so', () => {
  expect(acceptanceSuffix(null, { signOffPending: true })).toBe('· awaiting your sign-off');
  expect(acceptanceSuffix(null, { signOffPending: false })).toBe('');
  // Q-18: the decider is named once. A reviewer who wrote the note is credited
  // for the NOTE, never rendered as a second acceptor.
  expect(acceptanceSuffix({ verdict: 'accepted', decidedAt: '2026-09-12T13:02:00Z', noteBy: 'Sofia (analyst review)' }, { timeZone: 'UTC' }))
    .toBe('· accepted 12 Sep 2026 · note by Sofia (analyst review)');
  expect(acceptanceSuffix({ verdict: 'accepted', decidedBy: 'Sofia', decidedAt: '2026-09-12T13:02:00Z' }, { timeZone: 'UTC' }))
    .toBe('· accepted 12 Sep 2026 by Sofia');
  // 'local' and 'you' are the single-operator install saying nothing useful.
  expect(acceptanceSuffix({ verdict: 'changes_requested', decidedBy: 'local', decidedAt: '2026-09-12T13:02:00Z' }, { timeZone: 'UTC' }))
    .toBe('· changes requested 12 Sep 2026');
  expect(quotedNote('  Good enough to send.  ')).toBe('“Good enough to send.”');
  expect(quotedNote('')).toBe('');
});

test('an automatic acceptance is never attributed to a person (M-11)', () => {
  const auto = {
    verdict: 'accepted' as const, decidedBy: AUTO_DECIDER, decidedAt: '2026-09-12T13:02:00Z',
    checksPassed: 1, checksDeclared: 1,
  };
  expect(acceptanceLine(auto, { timeZone: 'UTC' })).toBe('accepted automatically · 1/1 checks 12 Sep 2026');
  expect(acceptanceSuffix(auto, { timeZone: 'UTC' })).toBe('· accepted automatically · 1/1 checks 12 Sep 2026');
  // No recorded counts means no invented denominator.
  expect(acceptanceLine({ verdict: 'accepted', decidedBy: AUTO_DECIDER })).toBe('accepted automatically');
});

test('a heading is a name or a plain label, never a sentence cut mid-word (Q-12)', () => {
  const longName = 'Weekly competitor brief for the leadership review, every Monday morning without fail';
  expect(runHeading({ savedName: longName, runNumber: 2 })).toBe(`${longName} · run #2`);
  expect(runHeading({ savedName: 'Weekly competitor brief' })).toBe('Weekly competitor brief');
  expect(runHeading({ brief: 'Summarize what Linear announced' })).toBe('Summarize what Linear announced');
  // A paragraph is a brief, not a title: it is read under "Workflow brief".
  const paragraph = 'Take last week’s notes and work out how we should re-plan the roadmap, then write it up for the leadership review on Monday morning.';
  expect(runHeading({ brief: paragraph, fallback: 'Workflow run' })).toBe('Workflow run');
  expect(runHeading({})).toBe('Workflow run');
});

test('the flat runs list is ordered by the column it shows (M-17)', () => {
  const runs = [
    { runId: 'b', startedAt: '2026-09-12T12:57:00Z' },
    { runId: 'a', startedAt: '2026-09-12T12:50:00Z' },
    { runId: 'c', startedAt: '2026-09-12T13:16:00Z' },
  ];
  expect(sortRunsByStarted(runs).map((run) => run.runId)).toEqual(['c', 'b', 'a']);
  // A tie breaks on the id, so two runs that started in the same second do not
  // swap places between renders.
  const tied = [
    { runId: 'a', startedAt: '2026-09-12T13:00:00Z' },
    { runId: 'b', startedAt: '2026-09-12T13:00:00Z' },
  ];
  expect(sortRunsByStarted(tied).map((run) => run.runId)).toEqual(['b', 'a']);
  expect(sortRunsByStarted(runs)).not.toBe(runs);
});

test('a share keeps its denominator and its units', () => {
  expect(passRateSentence(4, 5, 2)).toBe('4 of 5 finished runs passed (2 still running).');
  expect(passRateSentence(1, 1)).toBe('1 of 1 finished run passed.');
  expect(passRateSentence(0, 0, 3)).toBe('No runs have finished yet (3 still running).');
});

test('a decision is named in full, never truncated to one letter', () => {
  expect(needsYouTitle({ type: 'approval', workflowName: 'Weekly competitor brief', runNumber: 2 }))
    .toBe('Approve: Weekly competitor brief run #2');
  expect(needsYouTitle({ type: 'outcome', workflowName: 'Weekly competitor brief', runNumber: 3 }))
    .toBe('Sign off: Weekly competitor brief run #3');
  expect(needsYouTitle({ type: 'approval', title: 'Approve the announcement' })).toBe('Approve: Approve the announcement');
  expect(needsYouChip('approval')).toBe('APPROVE');
  expect(needsYouChip('outcome')).toBe('SIGN OFF');
});
