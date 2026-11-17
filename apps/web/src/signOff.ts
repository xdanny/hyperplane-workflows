/**
 * The sign-off policy, said in words a person can choose between (M-11).
 *
 * Marcus's ledger: twelve finished runs, twelve sign-offs, every one of them
 * "awaiting your sign-off" on work whose checks had already passed. The queue
 * was not wrong — nobody had recorded a verdict — it was simply asking a
 * question the workflow's owner never wanted asked.
 *
 * So the workflow says what it wants, once, and every surface renders that
 * choice rather than assuming "required". Three rules this module exists to
 * keep:
 *
 *   1. An AUTOMATIC acceptance is never attributed to a person. It says
 *      "accepted automatically", names the checks it rests on, and is not a
 *      human judgment on any surface that quotes it.
 *   2. "Optional" is not "decided". The delivery is still decidable and still
 *      appears under Decided once someone decides; it is only never QUEUED.
 *   3. Every option states its consequence in one line, in the editor, before
 *      it is chosen — a policy whose effect is only discoverable by watching
 *      the Inbox afterwards is not a choice.
 */

import { AUTO_DECIDER, readSignOff } from './api';
import type { SignOffPolicy } from './api';

export type { SignOffPolicy };
export { readSignOff };

/** The control's option labels — what the setting is, not what it is called
 *  internally. "auto_when_checks_pass" is a column value, not an answer to
 *  "when should this ask me?". */
export const SIGN_OFF_LABEL: Record<SignOffPolicy, string> = {
  required: 'Always ask me to sign off',
  optional: 'Do not ask; I can still decide later',
  auto_when_checks_pass: 'Accept automatically when every check passes',
};

/** The one line under each option: what choosing it DOES. */
export const SIGN_OFF_CONSEQUENCE: Record<SignOffPolicy, string> = {
  required:
    'Every finished run waits in your Inbox until you accept, request changes or reject it.',
  optional:
    'Finished runs never enter your Inbox. You can still record a decision on the run page or in its history at any time.',
  auto_when_checks_pass:
    'A run that passes every check it declared is accepted by Hyperplane itself, labelled as automatic. A run with no declared checks, a failed check, or any other ending still waits for you.',
};

/** How the policy reads on a run, a history row or a delivery card — short,
 *  because it sits beside a state chip rather than in a form. */
export const SIGN_OFF_SHORT: Record<SignOffPolicy, string> = {
  required: 'sign-off required',
  optional: 'sign-off optional',
  auto_when_checks_pass: 'accepted automatically when checks pass',
};

/** The recorded verdict, as much of it as any surface needs to describe it. */
export interface AcceptanceFacts {
  verdict: 'accepted' | 'changes_requested' | 'rejected';
  decidedAt?: string;
  decidedBy?: string;
  note?: string;
  checksPassed?: number;
  checksDeclared?: number;
}

/** True when the SERVER recorded this, under `auto_when_checks_pass`. */
export function isAutomatic(outcome: { decidedBy?: string } | null | undefined): boolean {
  return outcome?.decidedBy === AUTO_DECIDER;
}

/**
 * "accepted automatically · 1/1 checks" — the whole attribution of a policy
 * acceptance, on every surface that shows one.
 *
 * The check figures are what make it honest: an automatic acceptance is a
 * statement about declared checks and nothing else, so it carries them. When
 * the record does not have them (an older row), the phrase stops at
 * "accepted automatically" rather than inventing a denominator.
 */
export function automaticAcceptanceLine(outcome: AcceptanceFacts): string {
  const { checksPassed, checksDeclared } = outcome;
  const counted =
    typeof checksPassed === 'number' && typeof checksDeclared === 'number' && checksDeclared > 0;
  return counted
    ? `accepted automatically · ${checksPassed}/${checksDeclared} checks`
    : 'accepted automatically';
}

/** The sentence that explains an automatic acceptance wherever one is shown.
 *  It exists so "accepted" never reads as "somebody looked at this". */
export const AUTOMATIC_ACCEPTANCE_NOTE =
  'Accepted by Hyperplane because every declared check passed — this workflow is set to accept automatically. No person read this result.';

/**
 * Whether a finished delivery should be presented as waiting on a person.
 *
 * The server already leaves an "optional" delivery out of the queue; this is
 * the same rule said once for the surfaces that derive their own "needs you"
 * from a run rather than from the queue, so a run page cannot claim a sign-off
 * is pending that the Inbox never asked for.
 *
 * `auto_when_checks_pass` is pending exactly when it has NOT been decided: an
 * automatic acceptance is recorded at terminal persist, so an undecided run
 * under that policy is one the rule declined to accept — zero declared checks,
 * or a check that failed — and those are queued like "required".
 */
export function signOffPending(input: {
  state: string;
  decided: boolean;
  policy?: SignOffPolicy;
}): boolean {
  if (input.decided || input.state !== 'passed') return false;
  return readSignOff(input.policy) !== 'optional';
}

/**
 * Whether a finished delivery can be DECIDED here and now — which is a
 * different question from whether the Inbox is holding it (rule 2 above).
 *
 * "Optional" runs were queued nowhere and offered no control anywhere, so the
 * run page said "A person must accept this result before a skill can be
 * drafted. Sign off in Inbox." and the Inbox said "Nothing needs your
 * attention." Two auditors reached for the HTTP API to get out of it (S-14,
 * D-16). The policy governs the QUEUE; it never governs whether a person may
 * record their own judgment.
 *
 * Any ended run qualifies, not only a passed one: saying a failed delivery was
 * not good enough is an honest thing to record, and is exactly what the server
 * accepts (terminalRunState). The one verdict the server refuses — accepting a
 * rehearsal — it refuses on its own terms, with its own sentence.
 */
export function signOffDecidable(input: { state: string; decided: boolean }): boolean {
  if (input.decided) return false;
  return input.state === 'passed' || input.state === 'failed' || input.state === 'interrupted';
}
