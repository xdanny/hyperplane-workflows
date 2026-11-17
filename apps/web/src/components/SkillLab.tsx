/**
 * Skill Lab — evals / optimize / sweeps surface (M11)
 *
 * Surfaces within the Knowledge view as a second tab.
 * Sections:
 *   1. Leaderboard  — skills ranked by pass rate / gain
 *   2. Skill detail — instruction, evalset cases, eval run history, optimize history
 *   3. One-click Optimize — POST /optimize with polling
 *   4. Sweeps       — orchestrate run list with drill-in
 */
import { useState, useCallback } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api, improveTrigger, improveSkillItems, refinementCounts, regressionPolicySentence, skillLineage } from '../api';
import type { Skill, Evalset, EvalRun, OptimizeRun, OptimizeResult, OrchestrateRun, ImproveRun } from '../api';

// ── helpers ────────────────────────────────────────────────────────────────

function pct(n: number): string {
  return (n * 100).toFixed(0) + '%';
}

function score(n: number): string {
  return n.toFixed(2);
}

function relTime(iso: string): string {
  const d = new Date(iso);
  const diff = Date.now() - d.getTime();
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return d.toLocaleDateString();
}

// Gain display: e.g. "+12 pp"
function gainLabel(gain: number): string {
  const pp = (gain * 100).toFixed(0);
  return (gain >= 0 ? '+' : '') + pp + ' pp';
}

// ── Improvement loop ───────────────────────────────────────────────────────

/**
 * ImprovementLoopStrip — what the always-on loop has actually been doing.
 *
 * The loop harvests reviewer feedback (gate verdicts, panel votes), rewrites a
 * skill's instruction, and scores the rewrite against that skill's evalset. A
 * rewrite that does not WIN changes nothing, which is why this strip reports
 * "considered / refined / applied" rather than a single flattering number: the
 * gap between them is the honest picture of how often the loop finds a real
 * improvement.
 */
function ImprovementLoopStrip({ runs, loading }: { runs: ImproveRun[]; loading: boolean }) {
  const qc = useQueryClient();
  const [error, setError] = useState<string | null>(null);

  const runNow = useMutation({
    mutationFn: () => api.runImprove(),
    onSuccess: () => {
      setError(null);
      // The pass runs in the background; the list picks it up on its poll.
      void qc.invalidateQueries({ queryKey: ['improveRuns'] });
    },
    onError: (err: Error) => setError(err.message),
  });

  const last = runs[0];
  const running = last?.status === 'running';
  const trigger = last ? improveTrigger(last) : undefined;
  const applied = runs.reduce((n, r) => n + (r.skillsPromoted ?? 0), 0);

  return (
    <div className="panel sl-loop">
      <div className="sl-loop-head">
        <div>
          <span className="eyebrow">Improvement loop</span>
          <p className="sl-loop-copy">
            Runs automatically in the background: it harvests the feedback your gates and
            panels already produced, rewrites instructions, and scores each rewrite on the
            skill&rsquo;s evalset. A refinement is only applied when it wins that evaluation.
          </p>
        </div>
        <div className="sl-loop-actions">
          <button
            className="btn btn--primary btn--sm"
            onClick={() => runNow.mutate()}
            disabled={runNow.isPending || running}
            title={running ? 'A pass is already running' : undefined}
          >
            {runNow.isPending ? 'Starting…' : running ? 'Running…' : 'Run now'}
          </button>
        </div>
      </div>

      {error && <div className="sl-opt-error">{error}</div>}

      <div className="sl-loop-stats">
        <div className="sl-loop-stat">
          <span className="sl-loop-stat-v">{loading ? '—' : runs.length}</span>
          <span className="faint">passes recorded</span>
        </div>
        <div className="sl-loop-stat">
          <span className="sl-loop-stat-v">{loading ? '—' : applied}</span>
          <span className="faint">refinements applied</span>
        </div>
        <div className="sl-loop-stat">
          <span className="sl-loop-stat-v">
            {last ? relTime(last.startedAt) : loading ? '—' : 'never'}
          </span>
          <span className="faint">last pass</span>
        </div>
      </div>

      {last ? (
        <div className="sl-loop-last">
          <span className={`chip ${last.status === 'done' ? 's-pass' : last.status === 'failed' ? 's-fail' : 's-live'}`}
            style={{ fontSize: 'var(--t-xs)' }}>
            <span className="dot"></span>{last.status}
          </span>
          {trigger && <span className="tag">{trigger}</span>}
          <span className="faint" style={{ fontSize: 'var(--t-sm)' }}>
            considered {last.skillsConsidered}, refined {last.skillsOptimized}, applied {last.skillsPromoted}
          </span>
        </div>
      ) : (
        !loading && (
          <div className="faint" style={{ fontSize: 'var(--t-sm)' }}>
            No pass has run yet. The loop fires on its own schedule
            (<code className="mono">HYPERPLANE_IMPROVE_INTERVAL</code>, default 6h; <code className="mono">0</code> disables it).
          </div>
        )
      )}

      {last && improveSkillItems(last).length > 0 && (
        <div className="sl-loop-items">
          {improveSkillItems(last).slice(0, 8).map((item) => (
            <div key={item.skillRef} className="sl-loop-item">
              <span className="sl-loop-item-skill mono">{item.skillRef}</span>
              <span className="faint">{item.status}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// ── Leaderboard ────────────────────────────────────────────────────────────

interface SkillWithStats {
  skill: Skill;
  evalset?: Evalset;
  lastRun?: EvalRun;
  lastOptRun?: OptimizeRun;
}

interface LeaderboardProps {
  skills: Skill[];
  onSelect: (skill: Skill) => void;
  selectedId: string | null;
  /** How many refinements the improvement loop has APPLIED per skill. */
  refinements: Record<string, number>;
}

function Leaderboard({ skills, onSelect, selectedId, refinements }: LeaderboardProps) {
  const [sortBy, setSortBy] = useState<'pass_rate' | 'gain'>('pass_rate');

  // Fetch evalsets to match by skill_ref
  const { data: evalsets } = useQuery<Evalset[]>({
    queryKey: ['evalsets'],
    queryFn: () => api.evals(),
  });

  // For each skill that has an evalset, fetch latest eval run
  const evalsetBySkill = (evalsets ?? []).reduce<Record<string, Evalset>>((acc, es) => {
    if (es.skill_ref) acc[es.skill_ref] = es;
    return acc;
  }, {});

  const skillsWithStats: SkillWithStats[] = skills.map((skill) => ({
    skill,
    evalset: evalsetBySkill[skill.id],
  }));

  // Sort: by pass_rate descending (skills without evals go last), secondary by gain
  const sorted = [...skillsWithStats].sort((a, b) => {
    if (sortBy === 'pass_rate') {
      const ar = a.lastRun?.pass_rate ?? -1;
      const br = b.lastRun?.pass_rate ?? -1;
      return br - ar;
    }
    const ag = a.lastOptRun?.gain ?? -Infinity;
    const bg = b.lastOptRun?.gain ?? -Infinity;
    return bg - ag;
  });

  return (
    <div className="sl-leaderboard">
      <div className="sl-leaderboard-head">
        <span className="eyebrow">Skills</span>
        <div className="sl-sort">
          <span className="faint" style={{ fontSize: 'var(--t-xs)' }}>Sort:</span>
          <button
            className={`btn btn--ghost btn--sm${sortBy === 'pass_rate' ? ' sl-sort-active' : ''}`}
            onClick={() => setSortBy('pass_rate')}
            aria-pressed={sortBy === 'pass_rate'}
          >
            Pass rate
          </button>
          <button
            className={`btn btn--ghost btn--sm${sortBy === 'gain' ? ' sl-sort-active' : ''}`}
            onClick={() => setSortBy('gain')}
            aria-pressed={sortBy === 'gain'}
          >
            Gain
          </button>
        </div>
      </div>

      <div className="panel sl-leaderboard-table">
        <div className="sl-table-header">
          <span>Skill</span>
          <span>Evalset</span>
          <span>Pass rate</span>
          <span>Score</span>
          <span>Last gain</span>
        </div>
        {sorted.map(({ skill, evalset }) => (
          <SkillRow
            key={skill.id}
            skill={skill}
            evalset={evalset}
            selected={selectedId === skill.id}
            onSelect={onSelect}
            refinements={refinements[skill.id] ?? 0}
          />
        ))}
      </div>
    </div>
  );
}

interface SkillRowProps {
  skill: Skill;
  evalset?: Evalset;
  selected: boolean;
  onSelect: (skill: Skill) => void;
  refinements: number;
}

function SkillRow({ skill, evalset, selected, onSelect, refinements }: SkillRowProps) {
  // Fetch latest eval run for this evalset
  const { data: evalRuns } = useQuery<EvalRun[]>({
    queryKey: ['evalRuns', evalset?.id],
    queryFn: () => api.evalRuns(evalset!.id),
    enabled: !!evalset,
  });

  // Fetch latest optimize run for this skill
  const { data: optRuns } = useQuery<OptimizeRun[]>({
    queryKey: ['optRuns', skill.id],
    queryFn: () => api.optimizeRuns(skill.id),
  });

  const lastRun = evalRuns?.[0];
  const lastOpt = optRuns?.[0];

  return (
    <button
      className={`sl-skill-row${selected ? ' sl-skill-row--sel' : ''}`}
      onClick={() => onSelect(skill)}
      aria-pressed={selected}
    >
      <span className="sl-skill-name">
        {skill.name}
        {skill.category && <span className="sl-skill-cat">{skill.category}</span>}
        {refinements > 0 && (
          <span className="tag" title="Refinements the improvement loop applied after they won an evaluation">
            refined ×{refinements}
          </span>
        )}
      </span>
      <span className="sl-cell faint">
        {evalset ? evalset.name : <span className="sl-no-eval">—</span>}
      </span>
      <span className="sl-cell">
        {lastRun != null ? (
          <span className={`sl-stat-pass ${lastRun.pass_rate >= 0.8 ? 'sl-stat--good' : lastRun.pass_rate >= 0.5 ? 'sl-stat--mid' : 'sl-stat--low'}`}>
            {pct(lastRun.pass_rate)}
          </span>
        ) : (
          <span className="faint">—</span>
        )}
      </span>
      <span className="sl-cell faint">
        {lastRun != null ? score(lastRun.mean_score) : '—'}
      </span>
      <span className="sl-cell">
        {lastOpt != null && lastOpt.gain !== 0 ? (
          <span className={`sl-gain-badge ${lastOpt.gain > 0 ? 'sl-gain--pos' : 'sl-gain--neg'}`}>
            {gainLabel(lastOpt.gain)}
          </span>
        ) : (
          <span className="faint">—</span>
        )}
      </span>
    </button>
  );
}

// ── Skill Detail ───────────────────────────────────────────────────────────

interface SkillDetailProps {
  skill: Skill;
  onBack: () => void;
}

function SkillDetail({ skill, onBack }: SkillDetailProps) {
  const [tab, setTab] = useState<'overview' | 'evals' | 'optimize'>('overview');

  const { data: evalsets } = useQuery<Evalset[]>({
    queryKey: ['evalsets'],
    queryFn: () => api.evals(),
  });

  const evalset = evalsets?.find((es) => es.skill_ref === skill.id);

  const { data: evalRuns } = useQuery<EvalRun[]>({
    queryKey: ['evalRuns', evalset?.id],
    queryFn: () => api.evalRuns(evalset!.id),
    enabled: !!evalset,
    refetchInterval: 8000,
  });

  const { data: optRuns } = useQuery<OptimizeRun[]>({
    queryKey: ['optRuns', skill.id],
    queryFn: () => api.optimizeRuns(skill.id),
    refetchInterval: 5000,
  });

  return (
    <div className="sl-detail">
      <button className="btn btn--ghost btn--sm sl-back" onClick={onBack} aria-label="Back to leaderboard">
        ← Back
      </button>

      <div className="sl-detail-head">
        <div>
          <div className="eyebrow">Skill</div>
          <h2 className="h1" style={{ marginTop: 'var(--s1)' }}>{skill.name}</h2>
          {skill.description && <p className="sl-detail-desc">{skill.description}</p>}
        </div>
        <OptimizeButton skill={skill} evalset={evalset} />
      </div>

      <div className="inspector-tabs sl-detail-tabs">
        <button
          className={`inspector-tab${tab === 'overview' ? ' inspector-tab--active' : ''}`}
          onClick={() => setTab('overview')}
        >
          Overview
        </button>
        <button
          className={`inspector-tab${tab === 'evals' ? ' inspector-tab--active' : ''}`}
          onClick={() => setTab('evals')}
        >
          Eval runs {evalRuns ? `(${evalRuns.length})` : ''}
        </button>
        <button
          className={`inspector-tab${tab === 'optimize' ? ' inspector-tab--active' : ''}`}
          onClick={() => setTab('optimize')}
        >
          Optimize history {optRuns ? `(${optRuns.length})` : ''}
        </button>
      </div>

      <div className="panel sl-detail-body">
        {tab === 'overview' && (
          <SkillOverview skill={skill} evalset={evalset} />
        )}
        {tab === 'evals' && (
          <EvalRunsPanel evalRuns={evalRuns ?? []} evalset={evalset} />
        )}
        {tab === 'optimize' && (
          <OptimizeHistory optRuns={optRuns ?? []} />
        )}
      </div>
    </div>
  );
}

function SkillOverview({ skill, evalset }: { skill: Skill; evalset?: Evalset }) {
  // A LEARNED skill is one the platform proposed from its own runs. For those,
  // a missing evalset is not a gap in the tooling — it is the thing standing
  // between a draft and every prompt that would resolve it, so the empty state
  // says what it is blocking rather than just what is absent.
  const lineage = skillLineage(skill);
  const learned = lineage.supportRuns > 0;
  // The bar this draft has to clear, when the project it was learned under
  // declares one. It sits under the evalset because that is what it is about —
  // and it says plainly that nothing scores against it yet.
  const policy = regressionPolicySentence(lineage);
  return (
    <div className="sl-overview">
      {/* Instruction */}
      <div className="sl-section-label eyebrow">Instruction</div>
      {skill.instruction ? (
        <pre className="sl-instruction">{skill.instruction}</pre>
      ) : (
        <div className="faint" style={{ fontSize: 'var(--t-sm)' }}>No instruction defined.</div>
      )}

      <hr className="hr" style={{ margin: 'var(--s4) 0' }} />

      {/* Evalset */}
      <div className="sl-section-label eyebrow">Evalset</div>
      {evalset ? (
        <div>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 'var(--s3)', marginBottom: 'var(--s3)' }}>
            <span style={{ fontWeight: 560 }}>{evalset.name}</span>
            {evalset.description && (
              <span className="faint" style={{ fontSize: 'var(--t-sm)' }}>{evalset.description}</span>
            )}
            <span className="sl-case-count">{evalset.cases.length} cases</span>
          </div>
          <div className="sl-cases">
            {evalset.cases.map((c, i) => (
              <div key={c.id} className="sl-case">
                <span className="sl-case-num">{String(i + 1).padStart(2, '0')}</span>
                <div className="sl-case-body">
                  <span className="sl-case-id">{c.id}</span>
                  {c.criteria && <span className="sl-case-criteria">{c.criteria}</span>}
                </div>
              </div>
            ))}
          </div>
        </div>
      ) : (
        <div className="empty" style={{ padding: 'var(--s4) 0', alignItems: 'flex-start' }}>
          <span className="faint" style={{ fontSize: 'var(--t-sm)' }}>
            No evalset linked to this skill. Run <code className="mono">hyperplane eval gen {skill.id}</code> to add one.
            {learned && (
              <>
                {' '}This skill was learned from {lineage.supportRuns === 1 ? 'a single run' : `${lineage.supportRuns} runs`},
                {' '}so approval is blocked until it has a practice test — a proposal cannot enter prompts untested.
              </>
            )}
          </span>
        </div>
      )}
      {policy && (
        <div className="faint" style={{ fontSize: 'var(--t-sm)', marginTop: 'var(--s2)' }}>
          This draft {policy}
        </div>
      )}
    </div>
  );
}

function EvalRunsPanel({ evalRuns, evalset }: { evalRuns: EvalRun[]; evalset?: Evalset }) {
  const [selected, setSelected] = useState<string | null>(null);
  const run = evalRuns.find((r) => r.id === selected);

  if (!evalset) {
    return (
      <div className="empty">
        <span className="mark">⊘</span>
        <span>No evalset linked. Link one to see eval history.</span>
      </div>
    );
  }

  if (evalRuns.length === 0) {
    return (
      <div className="empty">
        <span className="mark">✦</span>
        <span>No eval runs yet. Run <code className="mono">hyperplane eval run {evalset.id}</code> to start.</span>
      </div>
    );
  }

  return (
    <div className="sl-eval-runs">
      <div className="sl-eval-list">
        {evalRuns.map((r) => (
          <button
            key={r.id}
            className={`sl-eval-row${selected === r.id ? ' sl-eval-row--sel' : ''}`}
            onClick={() => setSelected(selected === r.id ? null : r.id)}
          >
            <span className={`chip ${r.status === 'done' ? 's-pass' : r.status === 'failed' ? 's-fail' : 's-live'}`} style={{ fontSize: 'var(--t-xs)' }}>
              <span className="dot"></span>
              {r.status}
            </span>
            <span className="sl-eval-pass">{pct(r.pass_rate)}</span>
            <span className="sl-eval-score faint">{score(r.mean_score)}</span>
            <span className="faint" style={{ fontSize: 'var(--t-xs)', marginLeft: 'auto' }}>{relTime(r.started_at)}</span>
          </button>
        ))}
      </div>

      {run && run.results.length > 0 && (
        <div className="sl-case-results">
          <div className="eyebrow" style={{ marginBottom: 'var(--s3)' }}>Per-case results</div>
          {run.results.map((res) => (
            <div key={res.case_id} className={`sl-case-result ${res.passed ? 'sl-case-result--pass' : 'sl-case-result--fail'}`}>
              <span className={`sl-case-result-dot ${res.passed ? 's-pass' : 's-fail'}`}></span>
              <div className="sl-case-result-body">
                <span className="sl-case-result-id">{res.case_id}</span>
                <span className="sl-case-result-score faint">{score(res.score)}</span>
              </div>
              {res.reasons && res.reasons.length > 0 && (
                <ul className="sl-case-reasons">
                  {res.reasons.map((r, i) => <li key={i}>{r}</li>)}
                </ul>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function OptimizeHistory({ optRuns }: { optRuns: OptimizeRun[] }) {
  if (optRuns.length === 0) {
    return (
      <div className="empty">
        <span className="mark">✦</span>
        <span>No optimize runs yet. Use the Optimize button to propose and score variants.</span>
      </div>
    );
  }

  return (
    <div className="sl-opt-history">
      {optRuns.map((run) => (
        <div key={run.id} className="sl-opt-run">
          <div className="sl-opt-run-head">
            <span className="sl-opt-run-date faint">{relTime(run.created_at)}</span>
            {run.winner_instruction ? (
              <span className="sl-gain-badge sl-gain--pos">{gainLabel(run.gain)}</span>
            ) : (
              <span className="sl-gain-badge sl-gain--zero">no gain</span>
            )}
            {run.applied && (
              <span className="chip s-pass" style={{ fontSize: 'var(--t-xs)' }}>
                <span className="dot"></span> applied
              </span>
            )}
          </div>

          <div className="sl-opt-scores">
            <div className="sl-opt-score-row">
              <span className="sl-opt-score-label eyebrow">Baseline</span>
              <span className="sl-stat-pass">{pct(run.baseline_pass_rate)}</span>
              <span className="faint">{score(run.baseline_mean_score)}</span>
            </div>
            {run.winner_instruction && (
              <div className="sl-opt-score-row sl-opt-score-row--winner">
                <span className="sl-opt-score-label eyebrow">Winner</span>
                <span className="sl-stat-pass sl-stat--good">{pct(run.baseline_pass_rate + run.gain)}</span>
                <span className="faint">{score(run.baseline_mean_score)}</span>
              </div>
            )}
          </div>

          {run.winner_instruction && (
            <details className="sl-opt-instruction">
              <summary className="sl-opt-instruction-toggle">Show winner instruction</summary>
              <pre className="sl-instruction sl-instruction--sm">{run.winner_instruction}</pre>
            </details>
          )}
        </div>
      ))}
    </div>
  );
}

// ── Optimize Button ────────────────────────────────────────────────────────

interface OptimizeButtonProps {
  skill: Skill;
  evalset?: Evalset;
}

function OptimizeButton({ skill, evalset }: OptimizeButtonProps) {
  const [variants, setVariants] = useState(4);
  const [apply, setApply] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [result, setResult] = useState<OptimizeResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const queryClient = useQueryClient();

  const mutation = useMutation<OptimizeResult, Error, void>({
    mutationFn: () =>
      api.optimizeSkill(skill.id, {
        variants,
        apply,
        evalset: evalset?.id,
      }),
    onSuccess: (data) => {
      setResult(data);
      setError(null);
      // Invalidate so leaderboard + history refresh
      void queryClient.invalidateQueries({ queryKey: ['optRuns', skill.id] });
      void queryClient.invalidateQueries({ queryKey: ['evalRuns', evalset?.id] });
    },
    onError: (err) => {
      setError(err.message);
    },
  });

  return (
    <div className="sl-optimize-widget">
      {!expanded ? (
        <button
          className="btn btn--primary"
          onClick={() => setExpanded(true)}
          disabled={mutation.isPending}
        >
          Optimize
        </button>
      ) : (
        <div className="sl-opt-panel panel">
          <div className="sl-opt-panel-head">
            <span className="eyebrow">Optimize</span>
            <button
              className="btn btn--ghost btn--sm"
              onClick={() => { setExpanded(false); setResult(null); setError(null); }}
              aria-label="Close optimize panel"
            >
              ✕
            </button>
          </div>

          {!mutation.isPending && !result && (
            <div className="sl-opt-controls">
              <label className="sl-opt-label">
                <span className="faint" style={{ fontSize: 'var(--t-sm)' }}>Variants</span>
                <div className="sl-opt-stepper">
                  <button
                    className="btn btn--ghost btn--sm"
                    onClick={() => setVariants(Math.max(1, variants - 1))}
                    aria-label="Decrease variants"
                  >−</button>
                  <span className="sl-opt-stepper-val mono">{variants}</span>
                  <button
                    className="btn btn--ghost btn--sm"
                    onClick={() => setVariants(Math.min(8, variants + 1))}
                    aria-label="Increase variants"
                  >+</button>
                </div>
              </label>
              <label className="sl-opt-apply-label">
                <input
                  type="checkbox"
                  checked={apply}
                  onChange={(e) => setApply(e.target.checked)}
                />
                <span style={{ fontSize: 'var(--t-sm)' }}>Apply winner automatically</span>
              </label>
              <button
                className="btn btn--primary"
                onClick={() => mutation.mutate()}
                disabled={!evalset}
                title={!evalset ? 'Link an evalset to this skill first' : undefined}
              >
                Run optimize
              </button>
              {!evalset && (
                <span className="sl-opt-warn">No evalset linked — add one first.</span>
              )}
            </div>
          )}

          {mutation.isPending && (
            <div className="sl-opt-pending">
              <div className="compose-progress-bar" role="progressbar" aria-label="Optimizing…">
                <div className="compose-progress-bar-inner"></div>
              </div>
              <span className="compose-status">
                <span className="compose-status-dot"></span>
                Proposing and scoring variants…
              </span>
            </div>
          )}

          {error && (
            <div className="sl-opt-error">{error}</div>
          )}

          {result && !mutation.isPending && (
            <OptimizeResultView result={result} />
          )}
        </div>
      )}
    </div>
  );
}

function OptimizeResultView({ result }: { result: OptimizeResult }) {
  return (
    <div className="sl-opt-result">
      <div className="sl-opt-scores">
        <div className="sl-opt-score-row">
          <span className="sl-opt-score-label eyebrow">Baseline</span>
          <span className="sl-stat-pass">{pct(result.baseline.pass_rate)}</span>
          <span className="faint">{score(result.baseline.mean_score)}</span>
        </div>
        {result.winner ? (
          <div className="sl-opt-score-row sl-opt-score-row--winner">
            <span className="sl-opt-score-label eyebrow">Winner</span>
            <span className="sl-stat-pass sl-stat--good">{pct(result.winner.pass_rate)}</span>
            <span className="faint">{score(result.winner.mean_score)}</span>
            <span className="sl-gain-badge sl-gain--pos">{gainLabel(result.gain)}</span>
          </div>
        ) : (
          <div className="sl-opt-score-row">
            <span className="faint" style={{ fontSize: 'var(--t-sm)' }}>No variant beat the baseline.</span>
          </div>
        )}
      </div>

      <div className="sl-opt-result-foot">
        {result.applied && (
          <span className="chip s-pass" style={{ fontSize: 'var(--t-xs)' }}>
            <span className="dot"></span> Applied to skill
          </span>
        )}
        {result.feedback_grounded && (
          <span className="sl-fb-badge">feedback-grounded</span>
        )}
      </div>
    </div>
  );
}

// ── Sweeps ─────────────────────────────────────────────────────────────────

function SweepsPanel() {
  const [selectedId, setSelectedId] = useState<string | null>(null);

  // There's no list endpoint — the user pastes/selects an ID, or we could
  // show a placeholder inviting them to launch a sweep.
  // Show the selected sweep detail if an ID is entered.
  return (
    <div className="sl-sweeps">
      <div className="section-head">
        <span className="eyebrow">Orchestrate sweeps</span>
      </div>

      <div className="sl-sweeps-lookup">
        <input
          type="text"
          className="input"
          placeholder="Paste a sweep ID to inspect…"
          value={selectedId ?? ''}
          onChange={(e) => setSelectedId(e.target.value || null)}
          aria-label="Orchestrate sweep ID"
        />
      </div>

      {selectedId && selectedId.length > 4 ? (
        <SweepDetail id={selectedId} />
      ) : (
        <div className="empty">
          <span className="mark">↻</span>
          <span>Paste a sweep ID above, or launch one via the API / CLI.</span>
        </div>
      )}
    </div>
  );
}

function SweepDetail({ id }: { id: string }) {
  const { data: sweep, isLoading, error } = useQuery<OrchestrateRun>({
    queryKey: ['orchestrateRun', id],
    queryFn: () => api.orchestrateRun(id),
    refetchInterval: (q) => {
      const data = q.state.data;
      return data?.status === 'running' ? 3000 : false;
    },
  });

  if (isLoading) {
    return <div className="empty"><span>Loading sweep…</span></div>;
  }

  if (error || !sweep) {
    return (
      <div className="sl-sweep-error">
        Sweep not found or still starting. Check the ID.
      </div>
    );
  }

  const items: Array<{ title: string; state: string; runId?: string; error?: string }> =
    Array.isArray(sweep.items) ? sweep.items : [];

  return (
    <div className="sl-sweep-detail panel">
      <div className="sl-sweep-head">
        <div className="sl-sweep-meta">
          <span className="sl-sweep-id mono">{sweep.id}</span>
          <span className={`chip ${sweep.status === 'done' ? 's-pass' : sweep.status === 'failed' ? 's-fail' : 's-live'}`} style={{ fontSize: 'var(--t-xs)' }}>
            <span className="dot"></span>
            {sweep.status}
          </span>
        </div>
        <div className="sl-sweep-stats">
          <span className="sl-sweep-stat">
            <span className="sl-sweep-stat-v">{sweep.done}</span>
            <span className="faint">done</span>
          </span>
          <span className="sl-sweep-stat">
            <span className="sl-sweep-stat-v sl-stat--low">{sweep.failed}</span>
            <span className="faint">failed</span>
          </span>
          <span className="sl-sweep-stat">
            <span className="sl-sweep-stat-v">{sweep.backlogSize}</span>
            <span className="faint">total</span>
          </span>
        </div>
      </div>

      {items.length > 0 ? (
        <div className="sl-sweep-items">
          {items.map((item, i) => (
            <div key={i} className="sl-sweep-item">
              <span className={`sl-sweep-item-dot dot ${item.state === 'done' ? 's-pass' : item.state === 'failed' ? 's-fail' : item.state === 'running' ? 's-live' : 's-idle'}`}></span>
              <span className="sl-sweep-item-title">{item.title}</span>
              <span className={`sl-sweep-item-state faint`}>{item.state}</span>
              {item.runId && (
                <span className="sl-sweep-item-run mono faint">{item.runId.slice(0, 12)}</span>
              )}
            </div>
          ))}
        </div>
      ) : (
        <div className="empty" style={{ padding: 'var(--s4)' }}>
          <span>No items yet — sweep is starting.</span>
        </div>
      )}
    </div>
  );
}

// ── Root SkillLab ──────────────────────────────────────────────────────────

interface SkillLabProps {
  skills: Skill[];
  skillsLoading: boolean;
}

export function SkillLab({ skills, skillsLoading }: SkillLabProps) {
  const [view, setView] = useState<'leaderboard' | 'detail' | 'sweeps'>('leaderboard');
  const [selectedSkill, setSelectedSkill] = useState<Skill | null>(null);

  // The loop runs in the API process, so its history is the only place the UI
  // can see it working. Polling keeps a pass triggered here — or by the timer —
  // visible while it runs.
  const { data: improveRuns, isLoading: improveLoading } = useQuery<ImproveRun[]>({
    queryKey: ['improveRuns'],
    queryFn: () => api.improveRuns(),
    refetchInterval: 10000,
  });
  const passes = Array.isArray(improveRuns) ? improveRuns : [];
  const refinements = refinementCounts(passes);

  const handleSelectSkill = useCallback((skill: Skill) => {
    setSelectedSkill(skill);
    setView('detail');
  }, []);

  const handleBack = useCallback(() => {
    setView('leaderboard');
    setSelectedSkill(null);
  }, []);

  if (skillsLoading) {
    return <div className="empty"><span>Loading…</span></div>;
  }

  return (
    <div className="sl-root">
      <div className="sl-nav">
        <button
          className={`btn btn--ghost btn--sm${view !== 'sweeps' ? ' sl-nav-active' : ''}`}
          onClick={handleBack}
        >
          Leaderboard
        </button>
        <button
          className={`btn btn--ghost btn--sm${view === 'sweeps' ? ' sl-nav-active' : ''}`}
          onClick={() => setView('sweeps')}
        >
          Sweeps
        </button>
      </div>

      {view === 'sweeps' ? (
        <SweepsPanel />
      ) : view === 'detail' && selectedSkill ? (
        <SkillDetail skill={selectedSkill} onBack={handleBack} />
      ) : (
        <>
          <ImprovementLoopStrip runs={passes} loading={improveLoading} />
          <Leaderboard
            skills={skills}
            onSelect={handleSelectSkill}
            selectedId={selectedSkill?.id ?? null}
            refinements={refinements}
          />
        </>
      )}
    </div>
  );
}
