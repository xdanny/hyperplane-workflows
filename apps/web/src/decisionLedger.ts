/**
 * How a recorded decision reads on a ledger row — the project page's DECISIONS
 * section and the Inbox's "Decided" tab (S-13, D-15).
 *
 * Both rows used to render the record's `status`. `status` is the ADR lifecycle
 * of the ROW — "this decision stands" — and every outcome is written with
 * status "accepted", so a rejection appeared as "Delivery · Test whether…
 * accepted · 13 Sep 13:08 · journey:j-871c54fc4402e66b" directly above the
 * rejecting note. Two auditors read the ledger as saying the opposite of what
 * the run page said.
 *
 * So a row is built from three facts and nothing else: the VERDICT (`decision`),
 * WHO recorded it, and WHEN — then the work it closed, named the way every other
 * surface names it. A `journey:` or `run:` id is an internal handle and never
 * appears; a row that cannot be tied to a run says nothing about one.
 */

import { AUTO_DECIDER } from './api';
import type { Decision } from './api';
import { formatDateTime } from './runLexicon';

export type LedgerVerdict = 'accepted' | 'changes_requested' | 'rejected';

const VERDICT_LABEL: Record<LedgerVerdict, string> = {
  accepted: 'Accepted',
  changes_requested: 'Changes requested',
  rejected: 'Rejected',
};

/** The verdict a row states, or null when the record is not an outcome — a
 *  hand-written architecture decision has free text in `decision`. */
export function ledgerVerdict(decision: Pick<Decision, 'decision'>): LedgerVerdict | null {
  const value = decision.decision?.trim();
  return value === 'accepted' || value === 'changes_requested' || value === 'rejected' ? value : null;
}

/** The chip class, so a rejection never wears the colour of an acceptance. */
export function ledgerVerdictClass(verdict: LedgerVerdict | null): string {
  switch (verdict) {
    case 'accepted': return 's-pass';
    case 'rejected': return 's-fail';
    case 'changes_requested': return 's-block';
    default: return 's-idle';
  }
}

/**
 * Who recorded it.
 *
 * Hyperplane is local-first with one keyboard, so the person at it is "you" —
 * "by local" named nobody. An acceptance the SERVER recorded under a sign-off
 * policy says so instead, and is never credited to a person.
 */
export function ledgerDecider(decision: Pick<Decision, 'decidedBy'>): string {
  const who = decision.decidedBy?.trim();
  if (who === AUTO_DECIDER) return 'Hyperplane, automatically';
  if (!who || who === 'local') return 'you';
  return who;
}

/** "Weekly topic note run #2" — the work, named. Empty when the decision closed
 *  no run, or closed one this build could not resolve. */
export function ledgerWork(decision: Pick<Decision, 'workflowName' | 'runNumber'>): string {
  const name = decision.workflowName?.trim() ?? '';
  const number = typeof decision.runNumber === 'number' && decision.runNumber > 0 ? decision.runNumber : 0;
  if (name && number) return `${name} run #${number}`;
  if (name) return name;
  return number ? `run #${number}` : '';
}

/**
 * The whole row as one line:
 * "Rejected · you · 13 Sep 13:08 · Weekly topic note run #2".
 *
 * Each part is dropped when the record does not have it, rather than replaced
 * with a placeholder or an id. A decision with free text instead of a verdict
 * (an architecture decision) leads with that text.
 */
export function decisionLedgerLine(
  decision: Pick<Decision, 'decision' | 'decidedBy' | 'decidedAt' | 'createdAt' | 'workflowName' | 'runNumber'>,
  opts: { timeZone?: string } = {},
): string {
  const verdict = ledgerVerdict(decision);
  const parts: string[] = [];
  if (verdict) {
    parts.push(VERDICT_LABEL[verdict], ledgerDecider(decision));
  } else if (decision.decision?.trim()) {
    parts.push(decision.decision.trim());
  }
  const when = formatDateTime(decision.decidedAt ?? decision.createdAt, opts.timeZone);
  if (when) parts.push(when);
  const work = ledgerWork(decision);
  if (work) parts.push(work);
  return parts.join(' · ');
}

/**
 * "What this project has built so far", when the server has written no
 * narrative for it.
 *
 * The page said "Nothing has been accepted under this project yet" beside
 * "DECISIONS 3" — true about the narrative, and read by an auditor as a claim
 * about the ledger three lines below it (S-13). The sentence now agrees with
 * the rows, because it is derived from them.
 */
export function projectNarrativeFallback(decisions: Pick<Decision, 'decision'>[]): string {
  const verdicts = decisions.map(ledgerVerdict).filter((v): v is LedgerVerdict => v !== null);
  if (verdicts.length === 0) return 'Nothing has been accepted under this project yet.';
  const accepted = verdicts.filter((v) => v === 'accepted').length;
  if (accepted > 0) {
    return accepted === 1
      ? 'One result has been accepted under this project. No summary of it has been written yet.'
      : `${accepted} results have been accepted under this project. No summary of them has been written yet.`;
  }
  const rejected = verdicts.filter((v) => v === 'rejected').length;
  const changes = verdicts.filter((v) => v === 'changes_requested').length;
  const counted = [
    rejected ? `${rejected} rejected` : '',
    changes ? `${changes} sent back for changes` : '',
  ].filter(Boolean).join(', ');
  return `Nothing has been accepted under this project yet — ${verdicts.length} ${verdicts.length === 1 ? 'decision is' : 'decisions are'} recorded (${counted}).`;
}
