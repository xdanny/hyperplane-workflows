/**
 * The decision ledger rows (S-13, D-15): the VERDICT, who recorded it, when,
 * and the work it closed — never the record's lifecycle `status`, and never a
 * `journey:` or `run:` id.
 */

import { expect, test } from '@playwright/test';
import {
  decisionLedgerLine,
  ledgerDecider,
  ledgerVerdict,
  ledgerVerdictClass,
  ledgerWork,
  projectNarrativeFallback,
} from '../src/decisionLedger';
import { AUTO_DECIDER } from '../src/api';
import type { Decision } from '../src/api';

const rejection: Decision = {
  id: 'outcome-journey-j-871c54fc4402e66b:20260913-x',
  title: 'Delivery · Weekly topic note',
  // The row the audit saw: every outcome is STORED with status "accepted",
  // which is what the ledger was rendering.
  status: 'accepted',
  decision: 'rejected',
  why: 'Rejected by Sofia: the review failed on the word count.',
  source: 'journey:j-871c54fc4402e66b',
  refs: ['journey:j-871c54fc4402e66b', 'run:20260913-x'],
  decidedBy: 'local',
  decidedAt: '2026-09-13T13:08:00Z',
  createdAt: '2026-09-13T13:08:00Z',
  updatedAt: '2026-09-13T13:08:00Z',
  runId: '20260913-x',
  workflowName: 'Weekly topic note',
  runNumber: 2,
};

test('a rejection reads as a rejection, never as the record’s stored status', () => {
  expect(ledgerVerdict(rejection)).toBe('rejected');
  expect(ledgerVerdictClass(ledgerVerdict(rejection))).toBe('s-fail');
  expect(decisionLedgerLine(rejection, { timeZone: 'UTC' }))
    .toBe('Rejected · you · 13 Sep 13:08 · Weekly topic note run #2');
});

test('the line never carries a journey or run id', () => {
  const line = decisionLedgerLine(rejection, { timeZone: 'UTC' });
  expect(line).not.toContain('journey:');
  expect(line).not.toContain('run:');
  expect(line).not.toContain('j-871c54fc4402e66b');
});

test('an automatic acceptance is not credited to a person', () => {
  expect(ledgerDecider({ decidedBy: AUTO_DECIDER })).toBe('Hyperplane, automatically');
  expect(ledgerDecider({ decidedBy: 'local' })).toBe('you');
  expect(ledgerDecider({})).toBe('you');
  expect(ledgerDecider({ decidedBy: 'Sofia' })).toBe('Sofia');
  expect(decisionLedgerLine({ ...rejection, decision: 'accepted', decidedBy: AUTO_DECIDER }, { timeZone: 'UTC' }))
    .toBe('Accepted · Hyperplane, automatically · 13 Sep 13:08 · Weekly topic note run #2');
});

test('a decision that closed no run names none, and keeps its own words', () => {
  const adr: Decision = {
    id: 'use-the-reviewed-sdk-workflow', title: 'Use the reviewed SDK workflow',
    status: 'accepted', decision: 'Use the reviewed SDK workflow', source: 'owner',
    createdAt: '2026-09-01T09:00:00Z', updatedAt: '2026-09-01T09:00:00Z',
  };
  expect(ledgerVerdict(adr)).toBe(null);
  expect(ledgerWork(adr)).toBe('');
  expect(decisionLedgerLine(adr, { timeZone: 'UTC' }))
    .toBe('Use the reviewed SDK workflow · 1 Sep 09:00');
});

test('a row that resolved only half the naming says the half it has', () => {
  expect(ledgerWork({ workflowName: 'Weekly topic note' })).toBe('Weekly topic note');
  expect(ledgerWork({ runNumber: 3 })).toBe('run #3');
  expect(ledgerWork({})).toBe('');
});

test('the project summary agrees with the rows below it', () => {
  // The sentence the audit saw beside "DECISIONS 3".
  expect(projectNarrativeFallback([])).toBe('Nothing has been accepted under this project yet.');
  expect(projectNarrativeFallback([
    { decision: 'rejected' }, { decision: 'rejected' }, { decision: 'changes_requested' },
  ])).toBe('Nothing has been accepted under this project yet — 3 decisions are recorded (2 rejected, 1 sent back for changes).');
  expect(projectNarrativeFallback([{ decision: 'accepted' }, { decision: 'rejected' }]))
    .toBe('One result has been accepted under this project. No summary of it has been written yet.');
  // A hand-written architecture decision is not a delivery verdict.
  expect(projectNarrativeFallback([{ decision: 'Use the reviewed SDK workflow' }]))
    .toBe('Nothing has been accepted under this project yet.');
});
