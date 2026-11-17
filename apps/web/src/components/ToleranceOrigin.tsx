/**
 * ToleranceOrigin — whose number this is, on the screen where it is approved.
 *
 * The failure this exists for: an audit's rule set reached a human with two
 * category-emergence rules carrying a limit that had been approved for a
 * different feature. It was inside 22.7 KB of JSON, the approval screen
 * rendered it as one more field, and it was approved. The rule sets now mark
 * every tolerance `declared` (your own number for that subject) or `proposed`
 * (the workflow's suggestion for something you did not cover) — and a proposed
 * one has to be read as a separate decision, so it is rendered as one rather
 * than as a key/value row.
 *
 * Everything on screen here addresses the reader as YOU. The chips used to say
 * "the owner" while the summary under them said "you", so the person approving
 * had to work out that both meant them; and "the agent" is not a word this
 * platform uses anywhere else for the thing that ran.
 */

/** How a rule's tolerance got its number. Anything else is treated as unknown,
 *  which renders nothing rather than guessing. */
export type Origin = 'declared' | 'proposed';

export function originOf(row: unknown): Origin | null {
  if (!row || typeof row !== 'object') return null;
  const v = (row as Record<string, unknown>).toleranceOrigin;
  return v === 'declared' || v === 'proposed' ? v : null;
}

/** One rule's origin, as the chip that sits next to its tolerance. */
export function ToleranceOriginChip({ origin }: { origin: Origin }) {
  const declared = origin === 'declared';
  return (
    <span
      className={`chip chip--flat ${declared ? 's-pass' : 's-block'} tolerance-origin`}
      title={
        declared
          ? 'You declared this tolerance for this subject.'
          : 'You never declared a tolerance for this subject. The workflow proposed this number; approving the set approves it too.'
      }
    >
      <span className="dot" />
      {declared ? 'your number' : 'proposed by the workflow — not yours'}
    </span>
  );
}

/**
 * The one line above a rule set that says how many of its numbers are not the
 * owner's. Returns null when nothing in the packet carries an origin, so a
 * packet from a workflow that has no tolerances is untouched.
 */
export function ToleranceOriginSummary({ rows }: { rows: unknown[] }) {
  let declared = 0;
  let proposed = 0;
  for (const r of rows) {
    const o = originOf(r);
    if (o === 'declared') declared++;
    if (o === 'proposed') proposed++;
  }
  if (declared + proposed === 0) return null;
  return (
    <div className="tolerance-origin-summary">
      {proposed === 0 ? (
        <>All {declared} tolerances in this rule set are numbers you declared.</>
      ) : (
        <>
          <strong>{proposed}</strong> of {declared + proposed} tolerances in this rule set were
          proposed by the workflow, not declared by you. Approving the set approves those numbers
          too — read each one before you approve, or send the set back.
        </>
      )}
    </div>
  );
}
