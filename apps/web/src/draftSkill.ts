/**
 * What a DRAFT skill card is allowed to claim (D-03).
 *
 * The extraction record is unusually honest: it carries a 0.2 self-reported
 * confidence, a rationale that says in plain words why the evidence is thin
 * ("its text is not displayed … has little observational support"), and an
 * "Evidence boundary" paragraph that states outright that a passed workflow
 * status cannot establish content quality. None of it reached the card. What
 * the card showed instead was the evidence quote `phase: passed` — a status
 * word presented as the grounds for a method.
 *
 * So this module pulls those three things out of the record and, separately,
 * recognises a quote that is a status word rather than delivered text, so the
 * card can say which it is holding instead of passing one off as the other.
 */

import type { Skill, SkillEvidence } from './api';

const meta = (skill: Skill): Record<string, unknown> => skill.metadata ?? {};

/**
 * The extraction's own 0..1 score.
 *
 * Rendered, but never alone and never as a percentage bar: an unexplained
 * scalar reads as rigour it has not earned, which is exactly why it used to be
 * hidden. It goes on the card next to the rationale that explains it, labelled
 * as the extractor's self-assessment.
 */
export function selfReportedConfidence(skill: Skill): number | null {
  const value = meta(skill).confidence;
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= 1 ? value : null;
}

/** "low (0.2 of 1, self-reported)" — the number with what it is. */
export function confidenceLabel(confidence: number | null): string | null {
  if (confidence === null) return null;
  const word = confidence < 0.34 ? 'low' : confidence < 0.67 ? 'moderate' : 'high';
  return `${word} (${confidence.toFixed(2)} of 1, the extractor’s own estimate)`;
}

/** Why the extractor scored it that way, in its own words. */
export function draftRationale(skill: Skill): string {
  const value = meta(skill).rationale;
  return typeof value === 'string' ? value.trim() : '';
}

/**
 * The "Evidence boundary" paragraph of the draft's documentation — the section
 * that states what the evidence does NOT establish. Returns '' when the draft
 * predates the boundary text rather than inventing a caveat.
 */
export function evidenceBoundary(skill: Skill): string {
  const doc = meta(skill).doc;
  if (typeof doc !== 'string') return '';
  const match = /Evidence boundary\s*:\s*([\s\S]*?)(?:\n\s*\n|$)/i.exec(doc);
  return match ? match[1].trim() : '';
}

/**
 * A quote that is a recorded STATUS rather than anything the run delivered.
 *
 * `phase: passed` is the checkpoint's own field name and value. Quoting it as
 * evidence for a drafting method is the eval-theater move D-03 named, so a
 * card that holds one of these says so instead of presenting it as text.
 */
export function isStatusQuote(quote: string): boolean {
  const text = quote.trim();
  if (text === '') return true;
  if (/^(phase|state|status|verification|decision|verdict)\s*[:=]\s*[\w-]+$/i.test(text)) return true;
  return /^(passed|failed|approved|rejected|completed|not_requested)$/i.test(text);
}

/** The evidence split into what the run actually said and what is only status. */
export function splitEvidence(evidence: SkillEvidence[]): {
  quoted: SkillEvidence[];
  statusOnly: SkillEvidence[];
} {
  return {
    quoted: evidence.filter((item) => !isStatusQuote(item.quote)),
    statusOnly: evidence.filter((item) => isStatusQuote(item.quote)),
  };
}
