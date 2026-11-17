import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { api } from '../api';
import type { SkillExtractResult } from '../api';

export function ExtractSkillAction({ runId }: { runId: string }) {
  const queryClient = useQueryClient();
  const [result, setResult] = useState<SkillExtractResult | null>(null);
  const [error, setError] = useState('');

  const extract = useMutation({
    mutationFn: () => api.extractSkill(runId),
    onMutate: () => {
      setError('');
      setResult(null);
    },
    onSuccess: (res) => {
      setResult(res);
      void queryClient.invalidateQueries({ queryKey: ['skills'] });
    },
    onError: (err) => setError(err instanceof Error ? err.message : 'Extraction failed'),
  });

  const drafted = result?.status === 'drafted';

  return (
    <div className="run-extract">
      <button
        className="btn btn--sm"
        disabled={extract.isPending || drafted}
        onClick={() => extract.mutate()}
        aria-label="Draft a reusable skill from this run"
      >
        {extract.isPending ? 'Extracting…' : drafted ? 'Draft created' : 'Draft reusable skill'}
      </button>

      {extract.isPending && (
        <span className="run-extract-note">Preparing a draft from the recorded method and its evidence…</span>
      )}

      {result && (
        <span className="run-extract-note">
          <span className={`chip ${drafted ? 's-pass' : 's-idle'}`} style={{ fontSize: 'var(--t-xs)' }}>
            {drafted ? 'draft created' : 'duplicate'}
          </span>
          {/* The draft is linked, not described. The button used to simply
              reappear, so a second click risked a duplicate and the draft
              itself was only findable by going looking (D-09). */}
          {drafted
            ? <>
                Draft created: {result.skill?.name || result.skill?.id}{' '}
                → <Link to={`/knowledge?skill=${encodeURIComponent(result.skill?.id ?? '')}`}>Review it in Knowledge</Link>
                {' '}before approving it. Completion alone does not prove correctness.
              </>
            : `“${result.duplicate?.skillId}” already covers this method (${Math.round(
                (result.duplicate?.similarity ?? 0) * 100,
              )}% similar) — refine that skill instead of adding a near-duplicate.`}
        </span>
      )}

      {error && <span className="run-extract-error">{error}</span>}
    </div>
  );
}
