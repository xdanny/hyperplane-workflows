import type { RunState } from '../api';

interface StateChipProps {
  state: RunState;
  /**
   * Overrides the bare state word. PASSED/FAILED (a run's node graph), PROVEN (a
   * criterion) and ACCEPTED (a person's verdict) share one visual vocabulary, so
   * a chip that only says "failed" leaves the reader to guess WHAT KIND of thing
   * failed — and next to an "ACCEPTED · 5/5 proven" line, to guess wrong. Where
   * that collision is possible the caller names the kind: "failed as a run".
   */
  label?: string;
  /**
   * Overrides the colour the raw state would pick. A run parked on a person is
   * stored as `running` but is not live work: it reads WAITING FOR YOU in the
   * "needs you" colour, and the state word alone cannot know that (F-H). Use
   * `runChipClass` from runLexicon rather than inventing a class here.
   */
  className?: string;
}

const LABELS: Record<RunState, string> = {
  running: 'running',
  passed: 'passed',
  failed: 'failed',
  blocked: 'blocked',
  pending: 'pending',
  interrupted: 'stopped',
};

const STATE_CLASS: Record<RunState, string> = {
  running:     's-live',
  passed:      's-pass',
  failed:      's-fail',
  blocked:     's-block',
  pending:     's-idle',
  interrupted: 's-idle',
};

export function StateChip({ state, label, className }: StateChipProps) {
  const cls = className ?? STATE_CLASS[state] ?? 's-idle';
  return (
    <span className={`chip ${cls}`}>
      <span className="dot" />
      {label ?? LABELS[state] ?? state}
    </span>
  );
}
