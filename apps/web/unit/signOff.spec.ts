/**
 * The sign-off policy (M-11), and the rule that keeps it honest: an automatic
 * acceptance is never a person's decision.
 */

import { expect, test } from '@playwright/test';
import {
  AUTOMATIC_ACCEPTANCE_NOTE,
  SIGN_OFF_CONSEQUENCE,
  SIGN_OFF_LABEL,
  automaticAcceptanceLine,
  isAutomatic,
  readSignOff,
  signOffDecidable,
  signOffPending,
} from '../src/signOff';
import { AUTO_DECIDER, SIGN_OFF_POLICIES } from '../src/api';

test('an unrecognised policy reads as the one that still asks', () => {
  expect(readSignOff('required')).toBe('required');
  expect(readSignOff('optional')).toBe('optional');
  expect(readSignOff('auto_when_checks_pass')).toBe('auto_when_checks_pass');
  // A row written before the column existed, and anything a newer server sends
  // that this client does not know: both keep asking rather than quietly
  // deciding for the reader.
  expect(readSignOff(undefined)).toBe('required');
  expect(readSignOff('')).toBe('required');
  expect(readSignOff('auto')).toBe('required');
  expect(readSignOff(null)).toBe('required');
});

test('every policy states what it is and what choosing it does', () => {
  for (const policy of SIGN_OFF_POLICIES) {
    expect(SIGN_OFF_LABEL[policy].length).toBeGreaterThan(0);
    // The consequence is a sentence, not a restatement of the label: a policy
    // whose effect is only discoverable afterwards is not a choice.
    expect(SIGN_OFF_CONSEQUENCE[policy].length).toBeGreaterThan(40);
    expect(SIGN_OFF_CONSEQUENCE[policy]).not.toBe(SIGN_OFF_LABEL[policy]);
  }
});

test('an automatic acceptance names the checks it rests on', () => {
  expect(isAutomatic({ decidedBy: AUTO_DECIDER })).toBe(true);
  expect(isAutomatic({ decidedBy: 'Sofia' })).toBe(false);
  expect(isAutomatic(null)).toBe(false);
  expect(automaticAcceptanceLine({ verdict: 'accepted', checksPassed: 1, checksDeclared: 1 }))
    .toBe('accepted automatically · 1/1 checks');
  expect(automaticAcceptanceLine({ verdict: 'accepted', checksPassed: 3, checksDeclared: 3 }))
    .toBe('accepted automatically · 3/3 checks');
  // Zero declared checks can never produce an automatic acceptance, so a record
  // claiming one has no denominator to show — and none is invented.
  expect(automaticAcceptanceLine({ verdict: 'accepted', checksPassed: 0, checksDeclared: 0 }))
    .toBe('accepted automatically');
  expect(automaticAcceptanceLine({ verdict: 'accepted' })).toBe('accepted automatically');
  expect(AUTOMATIC_ACCEPTANCE_NOTE).toContain('No person read this result.');
});

test('only a required or unfired automatic policy leaves a sign-off pending', () => {
  expect(signOffPending({ state: 'passed', decided: false, policy: 'required' })).toBe(true);
  // Never queued, however green: this is the whole point of the setting.
  expect(signOffPending({ state: 'passed', decided: false, policy: 'optional' })).toBe(false);
  // An undecided run under the automatic policy is one the rule DECLINED to
  // accept — no declared checks, or a check that failed — so it is queued like
  // "required" rather than disappearing.
  expect(signOffPending({ state: 'passed', decided: false, policy: 'auto_when_checks_pass' })).toBe(true);
  expect(signOffPending({ state: 'passed', decided: true, policy: 'auto_when_checks_pass' })).toBe(false);
  // A run that did not pass is nobody's signature to give.
  expect(signOffPending({ state: 'failed', decided: false, policy: 'required' })).toBe(false);
  expect(signOffPending({ state: 'running', decided: false, policy: 'required' })).toBe(false);
  expect(signOffPending({ state: 'passed', decided: false })).toBe(true);
});

test('the policy governs the queue, never whether a person may decide', () => {
  // S-14 / D-16: an "optional" run was queued nowhere and offered no control
  // anywhere, while its page told the reader to sign off in an Inbox that said
  // "Nothing needs your attention."
  expect(signOffPending({ state: 'passed', decided: false, policy: 'optional' })).toBe(false);
  expect(signOffDecidable({ state: 'passed', decided: false })).toBe(true);

  // A required run is both queued and decidable on the page.
  expect(signOffPending({ state: 'passed', decided: false, policy: 'required' })).toBe(true);
  expect(signOffDecidable({ state: 'passed', decided: false })).toBe(true);

  // An ENDED run is decidable whatever way it ended: saying a failed delivery
  // was not good enough is an honest verdict, and the server takes it.
  for (const state of ['failed', 'interrupted']) {
    expect(signOffDecidable({ state, decided: false })).toBe(true);
    expect(signOffPending({ state, decided: false, policy: 'required' })).toBe(false);
  }

  // A run still working, and a run already decided, are not decidable.
  expect(signOffDecidable({ state: 'running', decided: false })).toBe(false);
  expect(signOffDecidable({ state: 'passed', decided: true })).toBe(false);
});
