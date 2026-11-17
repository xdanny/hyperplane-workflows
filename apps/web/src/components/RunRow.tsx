import { useRef, useEffect } from 'react';
import type { Run } from '../api';
import { StateChip } from './StateChip';
import {
  failingRowText, formatDateTime, formatDuration, formatTokens,
  runChipClass, runChipLabel, runTitle, slotsSummary,
} from '../runLexicon';

interface RunRowProps {
  run: Run;
  selected?: boolean;
  onClick: () => void;
}

/** The usage the row can show without a second request. */
function rowUsage(run: Run): string | null {
  const sdk = (run.doc as { sdk?: { usage?: { input_tokens?: number; output_tokens?: number } } } | undefined)?.sdk;
  const inTokens = formatTokens(sdk?.usage?.input_tokens);
  const outTokens = formatTokens(sdk?.usage?.output_tokens);
  return inTokens && outTokens ? `${inTokens} in / ${outTokens} out` : null;
}

/** The first finding the run recorded — the "why" behind a failing row. */
function firstFinding(run: Run): string | undefined {
  const findings = (run.doc as { sdk?: { findings?: unknown } } | undefined)?.sdk?.findings;
  if (!Array.isArray(findings)) return undefined;
  return findings.find((f): f is string => typeof f === 'string' && f.trim() !== '');
}

/**
 * One run, as a line somebody can triage from.
 *
 * The audited row was the goal sentence, a relative age and ten characters of
 * hash, which made three runs of one workflow word-for-word identical (P-10,
 * S-02, R-03). It now leads with the workflow's NAME and this run's number,
 * says which inputs it used, and — when it ended badly — names the step and
 * quotes the finding, so opening the run is a choice rather than the only way
 * to learn anything.
 */
export function RunRow({ run, selected, onClick }: RunRowProps) {
  const prev = useRef(run.state);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (prev.current !== run.state && ref.current) {
      ref.current.classList.remove('flash');
      void ref.current.offsetWidth; // restart animation
      ref.current.classList.add('flash');
    }
    prev.current = run.state;
  }, [run.state]);

  const waitingForPerson = run.waitingForPerson ?? run.state === 'blocked';
  const status = { state: run.state, waitingForPerson, failingStep: run.failingStep };
  const chip = runChipLabel(status);
  const chipClass = runChipClass(status);
  const needsYou = chip === 'WAITING FOR YOU';

  const title = runTitle(run);
  // Under the labels the workflow asked for them by, never under ids (P-18).
  const inputs = slotsSummary(run.slots, 2, run.slotLabels);
  // The goal moves to the second line only when it is not already the title,
  // and to the hover when the inputs have taken that line (F-B).
  const goal = run.goal?.trim();
  const secondary = inputs || (title !== goal ? goal : '') || '';

  const failed = run.state !== 'passed' && run.state !== 'running';
  const failing = failed
    ? failingRowText(run.failingStep, firstFinding(run))
      // Legacy rows (and any run slice A has not backfilled) keep the old node
      // id rather than showing nothing at all.
      ?? (run.failingNode ? `${run.state === 'interrupted' ? 'stopped at' : 'failed at'} ${run.failingNode}` : null)
    : null;

  const duration = formatDuration(run.durationMs);
  const usage = rowUsage(run);

  return (
    <div
      ref={ref}
      className={`run-row${selected ? ' run-row--sel' : ''}${needsYou ? ' run-row--blocked' : ''}`}
      onClick={onClick}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onClick(); } }}
      aria-pressed={selected}
      title={goal && goal !== title ? goal : undefined}
      aria-label={`${title} — ${chip}${failing ? ` — ${failing}` : ''}`}
    >
      <div className="run-row-main">
        <span className="run-row-name">{title}</span>
        {(secondary || failing) && (
          <span className="run-row-sub">
            {secondary && <span className="run-row-secondary faint">{secondary}</span>}
            {failing && <span className="run-row-failed-at" aria-hidden="true">{failing}</span>}
          </span>
        )}
        {needsYou && <span className="run-row-needs-you" aria-hidden="true">Needs you</span>}
      </div>
      <div className="run-row-right">
        <StateChip state={run.state} label={chip} className={chipClass} />
        <span className="run-row-meta mono">
          {formatDateTime(run.startedAt)}
          {duration ? ` · ${duration}` : ''}
          {usage ? ` · ${usage}` : ''}
        </span>
      </div>
    </div>
  );
}
