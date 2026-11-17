/** Delivery labels for server-computed SDK checks, execution state and human decisions. */

import type {
  Journey,
  JourneyAttempt,
  JourneyCoverage,
  JourneyMaturity,
  JourneyOutcome,
  JourneyStage,
} from './api';

/** What to call each stage. */
export const STAGE_LABEL: Record<JourneyStage, string> = {
  building: 'building',
  verifying: 'verifying',
  needs_decision: 'needs decision',
  rehearsed: 'rehearsal — nothing to decide',
  stopped: 'stopped',
  accepted: 'accepted',
  changes_requested: 'changes requested',
  rejected: 'rejected',
};

/** What each stage MEANS, in one sentence, shown as a tooltip on the decision
 *  chip. The accepted/changes-requested/rejected wordings are the lexicon
 *  definitions that used to hang off the maturity chip — they belong on the
 *  chip that states the human's verdict, which is what they define. */
export const STAGE_HINT: Record<JourneyStage, string> = {
  building: 'Building: an attempt is running right now. Nothing is being asked of you yet.',
  verifying: 'Verifying: the attempt finished its work and its checks are still running.',
  needs_decision:
    'Needs decision: an attempt has finished and nobody has recorded a verdict on what it delivered.',
  rehearsed:
    'Rehearsal: the newest attempt was a rehearsal, so nothing was produced and there is nothing to accept. Run the workflow for real; the delivery decision is asked on that run.',
  stopped: 'Stopped: the last attempt was interrupted, so nothing is waiting on you.',
  accepted: 'Accepted: you read a finished attempt and recorded that what it delivered is good.',
  changes_requested:
    'Changes requested: you read what was delivered and recorded that it needs more work before it counts.',
  rejected: 'Rejected: you read what was delivered and recorded that it is not what was wanted.',
};

/** State-palette chip class per stage, reusing the app's existing colours. */
export const STAGE_CHIP: Record<JourneyStage, string> = {
  building: 's-live',
  verifying: 's-live',
  needs_decision: 's-block',
  rehearsed: 's-idle',
  stopped: 's-idle',
  accepted: 's-pass',
  changes_requested: 's-block',
  rejected: 's-fail',
};

/** How a single attempt's state reads as a chip. */
export function attemptChipClass(state: string): string {
  switch (state) {
    case 'passed':
      return 's-pass';
    case 'failed':
      return 's-fail';
    case 'running':
      return 's-live';
    case 'blocked':
      return 's-block';
    default:
      return 's-idle';
  }
}

/** Explicit checks in one execution; file creation and human approval are excluded. */
export function criteriaTally(item: { criteria: number; proven: number }): string | null {
  if (item.criteria <= 0) return null;
  return `${item.proven}/${item.criteria} checks passed`;
}

/** The server's latest-attempt tally; completion is not a check result. */
export interface DeliveryTally {
  criteria: number;
  proven: number;
  /** How many criteria the counted attempt cleared with STAND-INS rather than
   *  proof. Non-zero only for a rehearsal, where `proven` is 0 — the server
   *  moves the count sideways rather than dropping it, because a reader who
   *  watched six checks go green still has to be told by what (S-17). */
  standinProven?: number;
  /** Which chapter the count came from. */
  attempt?: number;
  /** How many chapters there are, so a single-attempt delivery can stay quiet
   *  about which one it counted. */
  attemptCount?: number;
}

/** The DECISION chip: what a human recorded about this delivery, naming what
 *  kind of thing was decided. Undecided deliveries keep their stage word. */
export function decisionChipLabel(stage: JourneyStage): string {
  switch (stage) {
    case 'accepted':
      return 'delivery accepted';
    case 'changes_requested':
      return 'changes requested';
    case 'rejected':
      return 'delivery rejected';
    default:
      return STAGE_LABEL[stage];
  }
}

/** Report explicit check results independently of execution completion and human outcome. */
export function proofChipLabel(_maturity: JourneyMaturity, tally: DeliveryTally): string {
  if (tally.criteria <= 0) return 'No extra checks';
  // A rehearsal's gates recorded a pass without checking anything. The count
  // says so on the chip rather than three sections further down, where the
  // honest label used to sit while "6/6 PROVEN" led the page.
  const standin = tally.standinProven ?? 0;
  const count =
    standin > 0
      ? `${tally.proven}/${tally.criteria} checks passed — ${standin} stand-in result${standin === 1 ? '' : 's'}`
      : `${tally.proven}/${tally.criteria} checks passed`;
  if (tally.attempt && (tally.attemptCount ?? 0) > 1) return `${count} · attempt ${tally.attempt}`;
  return count;
}

/** The proof chip's tooltip: what the numbers count, and which attempt they are
 *  a statement about — the sentence whose absence let "4/5" and "5/5" describe
 *  one delivery without either saying so. */
export function proofChipHint(_maturity: JourneyMaturity, tally: DeliveryTally): string {
  if (tally.criteria <= 0) return 'No extra checks were declared. Completion and human acceptance do not establish independently verified correctness.';
  const standin = tally.standinProven ?? 0;
  if (standin > 0) {
    return `This attempt was a rehearsal: ${standin} of ${tally.criteria} criteria received stand-in results without a check running. Nothing here is proof.`;
  }
  const where =
    tally.attempt && (tally.attemptCount ?? 0) > 1
      ? ` They are attempt ${tally.attempt}'s — the latest — not a total across all ${tally.attemptCount} attempts.`
      : '';
  return `${tally.proven} of ${tally.criteria} declared checks passed. This includes optional acceptance checks and independent model reviews; each has its own limits. File creation and human approval are not counted.${where}`;
}

/** Highlight a complete set of passed checks; no checks stays neutral. */
export function proofChipClass(_maturity: JourneyMaturity, tally: DeliveryTally): string {
  if (tally.criteria <= 0) return 's-idle';
  return tally.proven >= tally.criteria ? 's-pass' : 's-idle';
}

/** One sentence naming what a delivery is waiting on. */
export function stageSentence(j: Journey): string {
  const attempts = `${j.attemptCount} attempt${j.attemptCount === 1 ? '' : 's'}`;
  switch (j.stage) {
    case 'building':
      return `Attempt ${j.attemptCount} is running.`;
    case 'verifying':
      return `Attempt ${j.attemptCount} is being verified.`;
    case 'needs_decision':
      return `${attempts}, no verdict recorded. Read the chapters, then decide.`;
    case 'rehearsed':
      return `The last attempt was a rehearsal, so nothing was produced and there is nothing to accept.`;
    case 'stopped':
      return `The last attempt was stopped, so nothing is waiting on you.`;
    default:
      return `Decided after ${attempts}.`;
  }
}

/** Short label for a chapter, e.g. "Attempt 2 · repair". */
export function attemptLabel(a: JourneyAttempt): string {
  return a.continuedFrom ? `Attempt ${a.number} · continued` : `Attempt ${a.number}`;
}

/** Last 8 characters of a run id — how run ids are referenced everywhere else. */
export function runTail(runId: string): string {
  return `…${runId.slice(-8)}`;
}

/** A long machine id, shortened for a line of prose. A readable slug is left
 *  alone; a uuid becomes its tail. The full value belongs on the hover. */
export function shortId(id: string): string {
  return id.length > 24 ? `…${id.slice(-8)}` : id;
}

/**
 * The one line that reconciles "this attempt failed" with "every criterion it
 * promised is proven" — a pairing that reads as a contradiction unless it is
 * said out loud: the checks passed and then something after them broke.
 *
 * The server may stamp this sentence itself (the runner work that knows WHICH
 * stage broke). Until it does — and if it never does — the same sentence is
 * derived from the tally already on the attempt. Read tolerantly on purpose:
 * whichever name that field lands under, this picks it up, and nothing here
 * breaks if it never arrives.
 */
export function attemptReconciliation(attempt: JourneyAttempt): string | null {
  const extra = attempt as unknown as Record<string, unknown>;
  for (const key of ['reconciliation', 'reconciled', 'provenYetFailed', 'failureReconciliation']) {
    const value = extra[key];
    if (typeof value === 'string' && value.trim() !== '') return value.trim();
  }
  const failed = attempt.state === 'failed' || attempt.state === 'interrupted';
  const allProven = attempt.criteria > 0 && attempt.proven >= attempt.criteria;
  if (!failed || !allProven) return null;
  const ending = attempt.state === 'interrupted' ? 'stopped' : 'failed';
  const where = attempt.failingNode ? `${ending} at ${attempt.failingNode}` : `${ending} after the last check`;
  return `${attempt.proven}/${attempt.criteria} checks passed · ${where}`;
}

/**
 * ── The pin ──────────────────────────────────────────────────────────────────
 *
 * A recorded verdict covers the attempts that existed when it was written, and
 * nothing else. The delivery accepted on 2026-08-21, when it had two attempts,
 * was still headed DELIVERY ACCEPTED on attempt 5 — a rehearsal that failed at
 * its inputs gate — because the journey key binds every later attempt into the
 * same line and the verdict simply flowed forward onto work nobody had seen.
 *
 * The server does the pinning (internal/journey/pin.go). These are the sentences
 * that say what it decided, in one place, so a card and a run header cannot
 * describe the same coverage differently.
 */

/** A run of chapter numbers as prose: "1–2", "3, 5", "4". */
export function attemptRange(numbers: number[] | undefined): string {
  if (!numbers || numbers.length === 0) return '';
  const sorted = [...numbers].sort((a, b) => a - b);
  const parts: string[] = [];
  let start = sorted[0];
  let prev = sorted[0];
  const flush = () => {
    if (start === prev) parts.push(String(start));
    else if (prev === start + 1) parts.push(`${start}, ${prev}`);
    else parts.push(`${start}–${prev}`);
  };
  for (const n of sorted.slice(1)) {
    if (n === prev || n === prev + 1) {
      prev = n;
      continue;
    }
    flush();
    start = n;
    prev = n;
  }
  flush();
  return parts.join(', ');
}

/** A verdict's date, in the short form the coverage sentence uses. */
function verdictDate(iso: string | undefined): string {
  if (!iso) return '';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '' : d.toISOString().slice(0, 10);
}

/** A verdict as it reads at the head of a sentence: "Accepted", "Rejected". */
function verdictHead(verdict: string): string {
  const words = verdict.replace(/_/g, ' ');
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/**
 * What a recorded verdict actually covers, said out loud. Null when there is
 * nothing to qualify — no verdict, or one that covers the delivery as it stands.
 */
export function coverageSentence(
  outcome: JourneyOutcome | null | undefined,
  coverage: JourneyCoverage | null | undefined,
): string | null {
  if (!outcome || !coverage || coverage.coversNewest) return null;
  const when = verdictDate(coverage.decidedAt ?? outcome.decidedAt);
  const covered = attemptRange(coverage.attempts);
  const undecided = attemptRange(coverage.undecided);
  const head = when ? `${verdictHead(outcome.verdict)} on ${when}` : verdictHead(outcome.verdict);
  if (!covered) {
    return `${head}, covering no attempt this delivery still has. Nothing on it has been decided.`;
  }
  const n = coverage.undecided?.length ?? 0;
  const noun = n === 1 ? 'attempt' : 'attempts';
  const verb = n === 1 ? 'is' : 'are';
  return `${head} covering attempt${coverage.attempts.length === 1 ? '' : 's'} ${covered}; ${noun} ${undecided} ${verb} newer than that verdict and undecided.`;
}
