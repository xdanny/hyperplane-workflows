import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '../api';
import type { Journey, JourneyAttempt, OutcomeVerdict } from '../api';
import {
  STAGE_CHIP,
  STAGE_HINT,
  coverageSentence,
  decisionChipLabel,
  proofChipClass,
  proofChipHint,
  proofChipLabel,
  attemptChipClass,
  attemptLabel,
  attemptReconciliation,
  criteriaTally,
  runTail,
  shortId,
  stageSentence,
} from '../journey';
import { NOTE_HINT, NOTE_PLACEHOLDER, REHEARSAL_BANNER, REHEARSAL_REFUSAL } from '../delivery';
import { AUTOMATIC_ACCEPTANCE_NOTE, isAutomatic, readSignOff, SIGN_OFF_SHORT } from '../signOff';
import { OutcomeBrief } from './OutcomeBrief';

/**
 * JourneyCard — one delivery line, as a product rather than as a run list.
 *
 * Header, then a CHAPTER STRIP: each chapter is a run's Delivery summary in
 * miniature (execution state, checks and human outcome) and clicking it
 * opens that SDK run. Nothing on this
 * card is stored — stage, maturity and every tally are derived server-side, so
 * there is nothing here to keep in sync.
 *
 * The decision lives here too, and only here at this grain: seven failed runs
 * that are really one product ask one question, on one card, with the whole
 * history visible while answering it.
 */

function fmtWhen(iso: string | undefined): string {
  if (!iso) return '';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('en-GB');
}

const VERDICT_LABEL: Record<OutcomeVerdict, string> = {
  accepted: 'Accepted',
  changes_requested: 'Changes requested',
  rejected: 'Rejected',
};

const VERDICT_CHIP: Record<OutcomeVerdict, string> = {
  accepted: 's-pass',
  changes_requested: 's-block',
  rejected: 's-fail',
};

/** One chapter of the story: a run, summarised the way its Delivery tab does. */
function ChapterRow({
  attempt,
  onOpenRun,
}: {
  attempt: JourneyAttempt;
  onOpenRun: (runId: string) => void;
}) {
  const tally = criteriaTally(attempt);
  // "failed" beside "4/4 proven" reads as a contradiction. When it happens, one
  // line says both facts together instead of leaving them to be reconciled by
  // eye. Present with or without the server's own reconciliation field.
  const reconciled = attemptReconciliation(attempt);
  return (
    <div className="journey-chapter-wrap">
      <button
        className={`journey-chapter${attempt.superseded ? ' journey-chapter--superseded' : ''}`}
        onClick={() => onOpenRun(attempt.runId)}
        aria-label={`Open the delivery for ${attemptLabel(attempt)}, run ${attempt.runId}`}
      >
        <span className="journey-chapter-num">{attemptLabel(attempt)}</span>
        <span className={`chip ${attemptChipClass(attempt.state)}`}>
          <span className="dot" />
          {attempt.state === 'interrupted' ? 'stopped' : attempt.state === 'passed' ? 'completed' : attempt.state}
        </span>
        {/* Right next to the state, because "passed · 4/4 proven" on this row is
            otherwise indistinguishable from a chapter where the work happened. */}
        {attempt.rehearsal && (
          <span className="chip s-block" title={REHEARSAL_BANNER}>
            <span className="dot" />
            rehearsal
          </span>
        )}
        {reconciled ? (
          <span
            className="journey-chapter-criteria"
            title="The recorded checks passed, but the attempt did not finish successfully. Review the stopping point."
          >
            {reconciled}
          </span>
        ) : (
          <>
            {tally && <span className="journey-chapter-criteria">{tally}</span>}
            {attempt.failingNode && (
              <span className="journey-chapter-failed mono" title="The step where this attempt ended">
                at {attempt.failingNode}
              </span>
            )}
          </>
        )}
        {attempt.outcome && (
          <span className={`chip ${VERDICT_CHIP[attempt.outcome.verdict] ?? 's-idle'}`}>
            <span className="dot" />
            {VERDICT_LABEL[attempt.outcome.verdict] ?? attempt.outcome.verdict}
          </span>
        )}
        {attempt.superseded && (
          <span className="journey-chapter-note faint">superseded</span>
        )}
        <span className="journey-chapter-run mono faint">{runTail(attempt.runId)}</span>
      </button>

      {attempt.continuedFrom && <button className="btn" onClick={() => onOpenRun(attempt.continuedFrom!)} aria-label={`Open source run ${attempt.continuedFrom}`}>
        Continued from {runTail(attempt.continuedFrom)}
      </button>}
    </div>
  );
}

/**
 * The derived narrative. Plain sentences and run links — no action lives on a
 * timeline event, because a second place to act on the same delivery is a second
 * place to forget one.
 */
function Timeline({ journey, onOpenRun }: { journey: Journey; onOpenRun: (runId: string) => void }) {
  if (journey.timeline.length === 0) return null;
  return (
    <ol className="journey-timeline" aria-label="What happened, in order">
      {journey.timeline.map((e, i) => (
        <li key={`${e.kind}-${i}`} className="journey-timeline-item">
          <span className="journey-timeline-when mono faint">{fmtWhen(e.at)}</span>
          <span className="journey-timeline-text">{e.text}</span>
          {e.runId && (
            <button
              className="journey-timeline-link mono"
              onClick={() => onOpenRun(e.runId!)}
              aria-label={`Open run ${e.runId}`}
            >
              {runTail(e.runId)}
            </button>
          )}
        </li>
      ))}
    </ol>
  );
}

/**
 * How a verdict recorded on ONE attempt reads on the delivery it settles.
 *
 * The old sentence — "Recorded on attempt …a1b2 rather than on the delivery,
 * and it still stands for it — the newest attempt is the delivery" — was three
 * clauses about a distinction the reader has no reason to hold. What they need
 * is: you said this, on that attempt, and it still counts.
 */
function runScopeSentence(journey: Journey, runId: string | undefined): string {
  const attempt = journey.attempts.find((a) => a.runId === runId);
  const isLatest = journey.attempts[0]?.runId === runId;
  if (attempt && isLatest) {
    return `You recorded this verdict on attempt ${attempt.number}; it still applies — that attempt is the delivery’s latest.`;
  }
  if (attempt) {
    return `You recorded this verdict on attempt ${attempt.number}; it still applies to the whole delivery.`;
  }
  return 'You recorded this verdict on one attempt; it still applies to the whole delivery.';
}

/** The one decision this delivery asks for. */
function JourneyDecision({ journey, onRecorded }: { journey: Journey; onRecorded?: (recorded: { verdict: OutcomeVerdict; runId: string }) => void }) {
  const qc = useQueryClient();
  const [note, setNote] = useState('');
  const latest = journey.attempts[0];
  const [reviewedRunId, setReviewedRunId] = useState(latest?.runId);
  const [reviewRequired, setReviewRequired] = useState(false);
  const needsReview = reviewRequired || reviewedRunId !== latest?.runId;
  const refresh = useMutation({ mutationFn: () => qc.refetchQueries({ queryKey: ['inbox'] }, { throwOnError: true }), retry: false });

  const record = useMutation({
    mutationFn: ({ runId, verdict, note }: { runId: string; verdict: OutcomeVerdict; note: string }) => api.recordJourneyOutcome(journey.key, runId, verdict, note),
    retry: false,
    onError: () => { setReviewRequired(true); refresh.reset(); },
    onSuccess: (_result, variables) => {
      setNote('');
      onRecorded?.({ verdict: variables.verdict, runId: variables.runId });
      void qc.invalidateQueries({ queryKey: ['journeys'] });
      void qc.invalidateQueries({ queryKey: ['inbox'] });
      void qc.invalidateQueries({ queryKey: ['runs'] });
      void qc.invalidateQueries({ queryKey: ['run'] });
    },
  });

  // A verdict that no longer reaches the newest attempt. It is on record, it is
  // shown, and it does NOT close the card: the delivery is undecided again, and
  // the sentence says exactly which attempts it did and did not cover (M-18).
  const uncovered = coverageSentence(journey.recordedOutcome, journey.coverage);
  const earlierDecision = !journey.outcome && uncovered && journey.recordedOutcome ? (
    <div className="journey-decided">
      <span className="chip s-idle">{VERDICT_LABEL[journey.recordedOutcome.verdict]} — earlier attempts</span>
      <p className="delivery-note faint">{uncovered}</p>
      {journey.recordedOutcome.note && <blockquote>{journey.recordedOutcome.note}</blockquote>}
    </div>
  ) : null;

  if (journey.outcome) {
    const { verdict, note: recorded, decidedAt, decisionId, scope, runId } = journey.outcome;
    return (
      <div className="journey-decided">
        <span className={`chip ${VERDICT_CHIP[verdict] ?? 's-idle'}`}>
          <span className="dot" />
          {/* An automatic acceptance wears its own word. "Accepted" beside a
              date reads as a person having read this, and under the
              auto_when_checks_pass policy nobody did (M-11). */}
          {isAutomatic(journey.outcome) ? 'Accepted automatically' : VERDICT_LABEL[verdict] ?? verdict}
        </span>
        <span className="journey-decided-when mono faint">{fmtWhen(decidedAt)}</span>
        {isAutomatic(journey.outcome) && (
          <p className="delivery-note faint">{AUTOMATIC_ACCEPTANCE_NOTE}</p>
        )}
        {recorded && <blockquote className="delivery-outcome-note">{recorded}</blockquote>}
        {/* Historical: verdicts recorded before a rehearsal could be refused are
            kept, never silently re-read as statements about real work. */}
        {journey.rehearsal && verdict === 'accepted' && (
          <p className="delivery-outcome-error">
            Recorded on a rehearsal, before that could be refused. It says what somebody clicked; it
            is not evidence that anything was delivered.
          </p>
        )}
        {scope === 'run' && (
          <p className="delivery-note faint">{runScopeSentence(journey, runId)}</p>
        )}
        <p className="delivery-note faint">
          This verdict remains recorded for the reviewed attempt. A later continued attempt needs a new decision.
        </p>
        <p className="journey-decided-id mono faint" title={`Decision record ${decisionId}`}>
          decision {decisionId}
          {scope === 'run' && runId ? ` · ${runTail(runId)}` : ''}
        </p>
      </div>
    );
  }

  if (!journey.needsDecision) {
    // "Optional" is not "decided": this delivery is simply never queued, and
    // saying so is what keeps an empty Inbox from reading as an oversight
    // (M-11). It stays decidable from the run page and its history.
    const policy = readSignOff(journey.signOff);
    return <>{earlierDecision}<p className="delivery-note faint">{stageSentence(journey)}</p>
      {policy !== 'required' && <p className="delivery-note faint">This workflow’s sign-off setting: {SIGN_OFF_SHORT[policy]}. You can still record a decision from the run page.</p>}
    </>;
  }

  const busy = record.isPending || refresh.isPending;
  const decide = (verdict: OutcomeVerdict) => {
    if (busy || needsReview || !latest) return;
    record.mutate({ runId: latest.runId, verdict, note });
  };
  return (
    <div className="journey-decide">
      {earlierDecision}
      {needsReview && <div role="alert"><p>Review the current attempt before deciding. Your note is preserved.</p>
        <button className="btn" disabled={busy} onClick={() => refresh.mutate()}>Refresh delivery</button>
        <button className="btn" disabled={busy || !latest || (reviewRequired && !refresh.isSuccess)} onClick={() => { setReviewedRunId(latest?.runId); setReviewRequired(false); record.reset(); }}>I reviewed attempt {latest?.number}</button>
      </div>}
      {refresh.error && <p role="alert">Could not refresh the delivery. Try Refresh delivery again.</p>}
      <p className="delivery-note faint">
        Read the results of attempt {latest?.number} before deciding. This is your judgment of the delivery; it does not change its recorded check results.
      </p>
      <label className="delivery-outcome-label" htmlFor={`journey-note-${journey.key}`}>
        Note (optional)
      </label>
      {/* The hint, not the placeholder, carries the guidance: a placeholder
          disappears the moment someone starts typing, which is exactly when the
          advice about how to write the note starts mattering. */}
      <p className="delivery-note faint" id={`journey-note-hint-${journey.key}`}>
        {NOTE_HINT}
      </p>
      <textarea
        id={`journey-note-${journey.key}`}
        className="delivery-outcome-textarea"
        // Four lines, because the hint asks for a decision, a reason and a next
        // step, and this note becomes the delivery's headline — a two-line box
        // says "one line will do" (P-14).
        rows={4}
        value={note}
        placeholder={NOTE_PLACEHOLDER}
        aria-describedby={`journey-note-hint-${journey.key}`}
        onChange={(e) => setNote(e.target.value)}
        disabled={busy}
      />
      {/* Refused here and at the server (409, internal/httpapi/journeys.go). */}
      {latest?.rehearsal && <p className="delivery-outcome-error">{REHEARSAL_REFUSAL}</p>}
      <div className="delivery-outcome-acts">
        <button
          className="btn btn--primary"
          disabled={busy || needsReview || !latest || Boolean(latest.rehearsal)}
          title={latest?.rehearsal ? REHEARSAL_REFUSAL : undefined}
          onClick={() => decide('accepted')}
        >
          Accept
        </button>
        <button className="btn" disabled={busy || needsReview || !latest} onClick={() => decide('changes_requested')}>
          Request changes
        </button>
        <button className="btn" disabled={busy || needsReview || !latest} onClick={() => decide('rejected')}>
          Reject
        </button>
      </div>
      {record.isError && (
        <p role="alert" className="delivery-outcome-error">
          {record.error instanceof Error ? record.error.message : 'Could not record the outcome.'}
        </p>
      )}
    </div>
  );
}

export function JourneyCard({
  journey,
  onOpenRun,
  defaultOpen = false,
  onRecorded,
}: {
  journey: Journey;
  onOpenRun: (runId: string) => void;
  /** Open the chapter strip without a click — the inbox wants the history in
   *  view while the question is being answered. */
  defaultOpen?: boolean;
  /** A verdict was just recorded: the surface that owns this card says so, because
   *  the card itself leaves the queue the moment the Inbox refetches (P-15). */
  onRecorded?: (recorded: { verdict: OutcomeVerdict; runId: string }) => void;
}) {
  const [open, setOpen] = useState(defaultOpen);
  // ONE tally, from the server's latest-attempt selector, read by both chips and
  // by the meta line. See internal/journey/tally.go — a second derivation here
  // is exactly what made this card and the run page disagree.
  const tally = {
    criteria: journey.criteria,
    proven: journey.proven,
    standinProven: journey.standinProven,
    attempt: journey.provenAttempt,
    attemptCount: journey.attemptCount,
  };

  return (
    <div className={`journey-card panel${journey.needsDecision ? ' journey-card--asking' : ''}`}>
      <div className="journey-head">
        <div className="journey-head-row">
          <span className="journey-name">{journey.name}</span>
          {/* Two chips, two DIFFERENT questions: what a human decided, and how
              much is proven. A decided card used to render both as the same
              word ("ACCEPTED ACCEPTED"), neither naming its kind. */}
          <span className={`chip ${STAGE_CHIP[journey.stage]}`} title={STAGE_HINT[journey.stage]}>
            <span className="dot" />
            {decisionChipLabel(journey.stage)}
          </span>
          <span
            className={`chip ${proofChipClass(journey.maturity, tally)}`}
            title={proofChipHint(journey.maturity, tally)}
          >
            <span className="dot" />
            {proofChipLabel(journey.maturity, tally)}
          </span>
          {/* Every attempt was a dry run, so the proof chip beside this one is
              counting simulated verdicts. The delivery has never happened. */}
          {journey.rehearsal && (
            <span className="chip s-block" title={REHEARSAL_BANNER}>
              <span className="dot" />
              rehearsal
            </span>
          )}
        </div>
        {journey.rehearsal && (
          <p className="delivery-note faint">
            Stand-in results, not evidence: every attempt of this delivery was a rehearsal. It cannot be accepted until it has been run for real.
          </p>
        )}
        {/* What the card is ABOUT, in the header. The machine ids that used to
            sit here — the recipe's, chiefly — are identifiers, not information,
            and they now live one click down with the attempts. */}
        <div className="journey-meta mono faint">
          {journey.project && <span className="journey-meta-project">{journey.project}</span>}
          <span>{journey.dag}</span>
          <span>
            {journey.attemptCount} attempt{journey.attemptCount === 1 ? '' : 's'}
          </span>
          {/* The proven count is NOT repeated here. It is the proof chip's job,
              and this line is where it used to be said a second time — with a
              different attempt's numbers. */}
        </div>
      </div>

      {/* The delivery in plain language, where the delivery is met. It is filed
          under the newest attempt — the run this delivery currently IS — and
          renders nothing until somebody has recorded a verdict, which is the
          moment a delivery brief can first be written. */}
      <OutcomeBrief subject="delivery" id={journey.attempts[0]?.runId ?? ''} />

      <button
        className="journey-toggle"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-label={`${open ? 'Hide' : 'Show'} the ${journey.attemptCount} attempts of ${journey.name}`}
      >
        <span className="journey-toggle-chevron" aria-hidden="true">
          {open ? '⌄' : '›'}
        </span>
        {open ? 'Hide attempts' : `Show ${journey.attemptCount} attempt${journey.attemptCount === 1 ? '' : 's'}`}
      </button>

      {open && (
        <>
          <div className="journey-chapters">
            {journey.attempts.map((a) => (
              <ChapterRow key={a.runId} attempt={a} onOpenRun={onOpenRun} />
            ))}
          </div>
          <Timeline journey={journey} onOpenRun={onOpenRun} />
          {/* The recipe's NAME, with the template id on the hover. This line
              printed "recipe …33bab14c" — a code nobody could look up, on the
              card that asks for a decision about it. */}
          {journey.recipe && (
            <p
              className={`journey-ids faint${journey.recipeName ? '' : ' mono'}`}
              title={`Recipe ${journey.recipe}`}
            >
              recipe {journey.recipeName || shortId(journey.recipe)}
            </p>
          )}
        </>
      )}

      <JourneyDecision journey={journey} onRecorded={onRecorded} />
    </div>
  );
}
