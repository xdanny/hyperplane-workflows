import type { Run } from '../api';
import { RunRow } from './RunRow';

export function RunList({ runs, selectedRunId, onSelect }: { runs: Run[]; selectedRunId?: string | null; onSelect: (id: string) => void }) {
  return <>{runs.map(run => <RunRow key={run.runId} run={run} selected={selectedRunId === run.runId} onClick={() => onSelect(run.runId)} />)}</>;
}
