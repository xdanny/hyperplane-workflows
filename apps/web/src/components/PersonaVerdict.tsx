import { useState } from 'react';
import type { PersonaReview } from '../api';

/**
 * PersonaVerdict — the shared renderer for the audit-persona reviews recorded
 * on one target (a skill, or a finished run's delivery).
 *
 * Shows the LATEST verdict as a chip — persona name + verdict — collapsed by
 * default. Expanding it shows the reasons, the citations they rest on (each a
 * verbatim quote plus where it came from), and, on a `revise` verdict, the
 * exact proposed wording. The expansion states plainly that the verdict is
 * ADVISORY — the human decides, and it does not gate Approve (spec AI-12);
 * nothing here disables or enables anything else on the page.
 *
 * When more than one review exists for the target, every one of them is kept
 * and shown — newest first, none overwritten. Two personas disagreeing on the
 * same target is exactly the evidence this feature exists to surface, so the
 * component says how many there are and lets the reader see them all rather
 * than collapsing to "the latest one wins".
 *
 * Renders nothing when `reviews` is empty, so a target nobody has reviewed
 * adds no clutter to its card.
 */
export function PersonaVerdict({ reviews }: { reviews: PersonaReview[] }) {
  const [expanded, setExpanded] = useState(false);
  if (!reviews || reviews.length === 0) return null;

  const sorted = sortNewestFirst(reviews);
  const latest = sorted[0];
  const earlier = sorted.slice(1);
  const label = verdictChip(latest);
  const scope = firstSentence(latest.summary);

  return (
    <div className="persona-verdict">
      <div className="persona-verdict-head">
        <button
          type="button"
          className={`chip ${chipClass(latest.verdict)}`}
          onClick={() => setExpanded((v) => !v)}
          aria-expanded={expanded}
          aria-label={`${label}. ${expanded ? 'Hide' : 'Show'} the persona review.`}
        >
          <span className="dot" />
          {label}
        </button>
        {earlier.length > 0 && (
          <span className="faint mono persona-verdict-more">
            +{earlier.length} earlier review{earlier.length === 1 ? '' : 's'}
          </span>
        )}
      </div>
      {/* The scope, beside the chip rather than inside the collapsed body: a
          bare "PASS" next to a run chip reading REVIEW FAILED looked like the
          platform contradicting itself, when the two were judging different
          things over different scopes (D-18). */}
      {scope && <p className="faint persona-verdict-scope">{scope}</p>}

      {expanded && (
        <div className="persona-verdict-panel">
          <PersonaReviewDetail review={latest} showChip={false} />

          {earlier.length > 0 && (
            <div className="persona-verdict-history">
              <span className="eyebrow persona-verdict-history-head">
                {sorted.length} reviews recorded for this target — newest first
              </span>
              {earlier.map((r) => (
                <PersonaReviewDetail key={r.id} review={r} showChip compact />
              ))}
            </div>
          )}

          <p className="faint persona-verdict-advisory">
            Advisory — the human decides. A persona review never gates Approve and never replaces
            an evalset.
          </p>
        </div>
      )}
    </div>
  );
}

function PersonaReviewDetail({
  review,
  showChip,
  compact,
}: {
  review: PersonaReview;
  showChip: boolean;
  compact?: boolean;
}) {
  const who = review.personaName || review.persona;
  return (
    <div className={`persona-review${compact ? ' persona-review--compact' : ''}`}>
      <div className="persona-review-head">
        {showChip && (
          <span className={`chip ${chipClass(review.verdict)}`}>
            <span className="dot" />
            {verdictChip(review)}
          </span>
        )}
        <span className="mono faint persona-review-meta">
          {who}
          {review.bar ? ` · "${review.bar}"` : ''} · {fmtWhen(review.reviewedAt)}
        </span>
      </div>

      {review.summary && <p className="persona-review-summary">{review.summary}</p>}

      {review.reasons && review.reasons.length > 0 && (
        <ul className="persona-review-reasons">
          {review.reasons.map((reason, i) => (
            <li key={i}>{reason}</li>
          ))}
        </ul>
      )}

      {review.citations && review.citations.length > 0 && (
        <ul className="kn-evidence persona-review-citations">
          {review.citations.map((c, i) => (
            <li key={i} className="kn-evidence-item">
              <q className="kn-evidence-quote">{c.quote}</q>
              <span className="mono kn-evidence-node">{c.ref}</span>
            </li>
          ))}
        </ul>
      )}

      {review.verdict === 'revise' && review.revisedWording && (
        <div className="persona-review-revision">
          <span className="eyebrow">Proposed wording</span>
          <pre className="sl-instruction sl-instruction--sm">{review.revisedWording}</pre>
        </div>
      )}
    </div>
  );
}

const VERDICT_LABEL: Record<PersonaReview['verdict'], string> = {
  approve: 'APPROVE',
  reject: 'REJECT',
  revise: 'REVISE',
  pass: 'PASS',
  fail: 'FAIL',
  mixed: 'MIXED',
};

/**
 * "Reviewer Dev: PASS (advisory)".
 *
 * The chip used to read "DEV — SENIOR ML ENGINEER · PASS", which sat beside a
 * run chip reading "REVIEW FAILED" and read as the platform contradicting
 * itself (D-18). Three words fix it: WHO said it (a reviewer, not the run),
 * WHAT they said, and that it decides nothing.
 *
 * The persona's job title stays in the expanded panel; the chip carries the
 * name a reader tells reviewers apart by.
 */
export function verdictChip(review: Pick<PersonaReview, 'persona' | 'personaName' | 'verdict'>): string {
  const full = review.personaName || review.persona;
  const short = full.split(/\s+[—–-]\s+/)[0].trim() || full;
  return `Reviewer ${short}: ${VERDICT_LABEL[review.verdict] ?? review.verdict} (advisory)`;
}

/** The first sentence of a summary, for the line beside the chip. */
export function firstSentence(summary: string | undefined, max = 200): string {
  const text = (summary ?? '').trim().replace(/\s+/g, ' ');
  if (!text) return '';
  const end = text.search(/[.!?](\s|$)/);
  const sentence = end >= 0 ? text.slice(0, end + 1) : text;
  return sentence.length > max ? sentence.slice(0, max).trimEnd() + '…' : sentence;
}

/** Reuses the app's existing state-chip palette — approve/pass read as pass,
 *  reject/fail as fail, revise/mixed as the amber "needs a look" block color. */
function chipClass(verdict: PersonaReview['verdict']): string {
  switch (verdict) {
    case 'approve':
    case 'pass':
      return 's-pass';
    case 'reject':
    case 'fail':
      return 's-fail';
    case 'revise':
    case 'mixed':
      return 's-block';
    default:
      return 's-idle';
  }
}

function sortNewestFirst(reviews: PersonaReview[]): PersonaReview[] {
  return [...reviews].sort((a, b) => b.reviewedAt.localeCompare(a.reviewedAt));
}

function fmtWhen(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('en-GB');
}
