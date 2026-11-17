/**
 * "Show full reason" has to end with the full reason (Q-12).
 *
 * The Inbox card clipped the failure reason at ~200 characters and offered to
 * expand it. Expanding produced a sentence that still ended in an ellipsis —
 * "…skipped checks,… Show less" — because the reason arrives on the card
 * ALREADY shortened: the server elides it to 200 runes before the card ever
 * sees it. Two clips in a row, one of which the card cannot undo.
 *
 * So the card stops pretending the payload is complete. When the delivered
 * reason is elided, the whole text is read back from the run the card is
 * already linking to — the same recorded findings the run page renders — and
 * "Show less" restores the card-sized clip.
 *
 * Nothing here invents text. A run that cannot be read leaves the elided
 * sentence exactly as it arrived, and `expandable` says so, so the card can
 * offer to open the run instead of a button that would expand to the same
 * truncated words.
 */

import type { Run } from './api';

/** The character the server appends when it shortens a reason. */
const ELLIPSIS = '…';

/** True when this text was cut by somebody else before it reached us. */
export function isElided(text: string): boolean {
  const trimmed = text.trimEnd();
  return trimmed.endsWith(ELLIPSIS) || trimmed.endsWith('...');
}

interface SDKDoc {
  currentNode?: string;
  error?: string;
  findings?: unknown;
  nodes?: { id?: string; findings?: unknown }[];
}

const joinFindings = (raw: unknown): string =>
  (Array.isArray(raw) ? raw : [])
    .filter((finding): finding is string => typeof finding === 'string' && finding.trim() !== '')
    .join('; ');

/**
 * The complete reason a run ended badly, from the run's own record.
 *
 * This mirrors the server's derivation (internal/journey/criteria.go) up to —
 * and deliberately not including — its 200-rune cut: the recorded error if
 * there is one, else the run-level findings, else the findings of the step the
 * run stopped on. Returns '' when the record carries none of those, which is a
 * fact the caller reports rather than papers over.
 */
export function fullFailureReason(run: Run | undefined): string {
  const sdk = (run?.doc?.sdk ?? {}) as SDKDoc;
  const error = typeof sdk.error === 'string' ? sdk.error.trim() : '';
  if (error !== '') return error;
  const runLevel = joinFindings(sdk.findings);
  if (runLevel !== '') return runLevel;
  const stepId = run?.failingStep?.id ?? (typeof sdk.currentNode === 'string' ? sdk.currentNode : '');
  if (!stepId) return '';
  const step = (Array.isArray(sdk.nodes) ? sdk.nodes : []).find((node) => node?.id === stepId);
  return joinFindings(step?.findings);
}

/**
 * What the card should show, given the reason it was handed and whatever it
 * managed to read back from the run.
 *
 * The recovered text is only preferred when it genuinely CONTINUES the elided
 * one — same opening words, more of them. A run whose record has since moved on
 * would otherwise let "Show full reason" replace the sentence with a different
 * sentence, which is a worse failure than a short one.
 */
export function expandedReason(delivered: string, recovered: string): { text: string; complete: boolean } {
  const reason = delivered.trim();
  if (!isElided(reason)) return { text: reason, complete: true };
  const stem = reason.replace(/[.…]+$/, '').trim();
  const whole = recovered.replace(/\s+/g, ' ').trim();
  if (stem !== '' && whole.length > stem.length && whole.startsWith(stem.slice(0, Math.min(stem.length, 60)))) {
    return { text: whole, complete: true };
  }
  return { text: reason, complete: false };
}
