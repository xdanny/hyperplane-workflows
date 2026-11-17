import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import type { OutcomeBriefData } from '../api';

/** The platform brief may inherit the generator's neutral "step(s)" token.
 * Resolve it from the adjacent count before putting it in front of a reader. */
export function pluralizeBriefSteps(text: string): string {
  return text.replace(/\b(\d[\d,]*) step\(s\)/g, (_match, raw: string) => {
    const count = Number(raw.replaceAll(',', ''));
    return `${raw} ${count === 1 ? 'step' : 'steps'}`;
  });
}

/**
 * OutcomeBrief — "In plain language": what the work set out to build, what
 * happened, what the reader actually got, and what to do next.
 *
 * Every other piece of prose on a run page was written by a node INSIDE the
 * workflow — before the gate that judged it, before the repair, before anybody
 * decided anything — so none of it can state an outcome. This one is written by
 * the platform afterwards, from its own records, which is what makes it the
 * block worth reading first.
 *
 * Two flags travel with it and are always rendered:
 *
 *   - NUMBERS UNVERIFIED, when the platform could not find a figure the brief
 *     used in the state it was written from. The brief is still shown — a
 *     flagged account beats no account — but it must never look measured when
 *     it is not.
 *   - FALLBACK SUMMARY, when the platform wrote it deterministically instead of
 *     the engine. Fewer facts, no interpretation, and the reader should know
 *     which they are holding.
 *
 * Renders nothing at all when there is no brief yet: a run still going has no
 * outcome, and an empty "in plain language" block would say there is nothing to
 * tell.
 */
export function OutcomeBrief({
  subject,
  id,
  title,
  poll = false,
}: {
  subject: 'run' | 'delivery';
  id: string;
  /** Overrides the block heading. Defaults to "In plain language". */
  title?: string;
  /** Re-checks for a brief that has not arrived yet. On for a single run page,
   *  where a run may have finished seconds ago and the writing hook is
   *  detached; off in lists, where one poll per card would be a lot of requests
   *  for a brief that mostly already exists or never will. */
  poll?: boolean;
}) {
  const { data } = useQuery<OutcomeBriefData | null>({
    queryKey: ['brief', subject, id],
    queryFn: () => (subject === 'run' ? api.runBrief(id) : api.deliveryBrief(id)),
    enabled: Boolean(id),
    refetchInterval: (query) => (poll && !query.state.data ? 15000 : false),
  });

  if (!data) return null;
  const parts = [
    { label: 'What this set out to build', body: data.whatItBuilds },
    { label: 'What actually happened', body: data.whatHappened },
    { label: 'The true outcome', body: data.trueOutcome },
    { label: 'What to do next', body: data.nextSteps },
  ].filter((p) => (p.body ?? '').trim() !== '');
  if (parts.length === 0) return null;

  return (
    <section className="brief panel" aria-label={title ?? 'In plain language'}>
      <div className="brief-head">
        <span className="eyebrow">{title ?? 'In plain language'}</span>
        {!data.grounded && (
          <span
            className="chip s-block brief-flag"
            title={
              data.ungroundedNumbers?.length
                ? `These numbers are not in the platform's own record of this work: ${data.ungroundedNumbers.join(', ')}`
                : "Some numbers in this summary are not in the platform's own record of this work."
            }
          >
            <span className="dot" />
            numbers unverified
          </span>
        )}
        {data.engine === 'fallback' && (
          <span
            className="faint mono brief-flag brief-flag--fallback"
            title="Written by the platform from the recorded state — status facts only, no interpretation."
          >
            platform summary — status facts only
          </span>
        )}
      </div>

      <dl className="brief-parts">
        {parts.map((p) => (
          <div className="brief-part" key={p.label}>
            <dt className="brief-part-label">{p.label}</dt>
            <dd className="brief-part-body">{pluralizeBriefSteps(p.body ?? '')}</dd>
          </div>
        ))}
      </dl>

      {!data.grounded && data.ungroundedNumbers && data.ungroundedNumbers.length > 0 && (
        <p className="faint brief-unverified">
          Not found in this work&rsquo;s recorded state:{' '}
          <span className="mono">{data.ungroundedNumbers.join(', ')}</span>. Check them against the
          run&rsquo;s own outputs before repeating them.
        </p>
      )}
    </section>
  );
}
