import { useState, useEffect } from 'react';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import type { Run, InboxItem, StatsTokens, RunTokensSummary, Lane } from '../api';
import { RunList } from '../components/RunList';
import { StartSomething } from '../components/StartSomething';
import { useSelectedRun } from '../hooks/useSelectedRun';
import { useProjectScope } from '../hooks/useProjectScope';
import { needsYouChip, needsYouTitle, passRateSentence } from '../runLexicon';

function useClock() {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), 1000);
    return () => clearInterval(id);
  }, []);
  return now;
}

/** Format a token count compactly: 1234 → "1.2K", 1234567 → "1.2M" */
function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return String(n);
}

/** Stacked bar chart: input (with cached portion shaded) vs output tokens per run. */
function TokenBarChart({ runs }: { runs: RunTokensSummary[] }) {
  if (runs.length === 0) return null;

  const W = 480;
  const H = 72;
  const BAR_GAP = 3;
  const barCount = runs.length;
  const barW = Math.max(6, Math.floor((W - BAR_GAP * (barCount - 1)) / barCount));
  const totalW = barCount * barW + BAR_GAP * (barCount - 1);
  const offsetX = Math.floor((W - totalW) / 2);

  const maxTotal = Math.max(...runs.map((r) => r.inputTokens + r.outputTokens), 1);

  return (
    <svg
      width={W}
      height={H}
      viewBox={`0 0 ${W} ${H}`}
      style={{ display: 'block', overflow: 'visible' }}
      aria-label="New token usage per execution — input and output bars"
      role="img"
    >
      {runs.map((run, i) => {
        const x = offsetX + i * (barW + BAR_GAP);
        const total = run.inputTokens + run.outputTokens;
        const totalH = Math.round((total / maxTotal) * (H - 4));
        const inputH = Math.round((run.inputTokens / maxTotal) * (H - 4));
        const cachedH = Math.round((run.cachedInputTokens / maxTotal) * (H - 4));
        const outputH = totalH - inputH;
        const dagLabel = run.dag || run.runId.slice(-6);

        return (
          <g key={run.runId} aria-label={`${dagLabel}: in ${fmtTokens(run.inputTokens)} out ${fmtTokens(run.outputTokens)}`}>
            {/* output portion (top) */}
            {outputH > 0 && (
              <rect
                x={x}
                y={H - totalH}
                width={barW}
                height={outputH}
                fill="var(--ink-3)"
                opacity={0.55}
              />
            )}
            {/* uncached input portion */}
            {(inputH - cachedH) > 0 && (
              <rect
                x={x}
                y={H - inputH}
                width={barW}
                height={inputH - cachedH}
                fill="var(--ink)"
                opacity={0.7}
              />
            )}
            {/* cached input portion (shaded) */}
            {cachedH > 0 && (
              <rect
                x={x}
                y={H - cachedH}
                width={barW}
                height={cachedH}
                fill="var(--st-pass)"
                opacity={0.65}
              />
            )}
          </g>
        );
      })}
    </svg>
  );
}

/** Small line chart of cache-hit-rate per run (newest = rightmost). */
function CacheRateTrendChart({ runs }: { runs: RunTokensSummary[] }) {
  const W = 480;
  const H = 36;

  const rates = runs.map((r) => (r.inputTokens > 0 ? r.cachedInputTokens / r.inputTokens : 0));
  if (rates.length < 2) return null;

  const barCount = rates.length;
  const BAR_GAP = 3;
  const barW = Math.max(6, Math.floor((W - BAR_GAP * (barCount - 1)) / barCount));
  const totalW = barCount * barW + BAR_GAP * (barCount - 1);
  const offsetX = Math.floor((W - totalW) / 2);

  return (
    <svg
      width={W}
      height={H}
      viewBox={`0 0 ${W} ${H}`}
      style={{ display: 'block', overflow: 'visible' }}
      aria-label="Cache hit rate trend per run"
      role="img"
    >
      {rates.map((rate, i) => {
        const x = offsetX + i * (barW + BAR_GAP);
        const h = Math.max(2, Math.round(rate * (H - 2)));
        return (
          <rect
            key={i}
            x={x}
            y={H - h}
            width={barW}
            height={h}
            fill="var(--st-pass)"
            opacity={0.7}
            aria-label={`Run ${i + 1}: ${Math.round(rate * 100)}% cache hit`}
          />
        );
      })}
    </svg>
  );
}

export function Home() {
  const clock = useClock();
  const { setRunId } = useSelectedRun();
  // The app-wide scope seeds the start card's project, so "start something"
  // lands where the user is already working. `null` is "all projects", which
  // is not a destination — that falls back to unscoped.
  const scope = useProjectScope((s) => s.project);

  const { data: sample } = useQuery<Run[]>({
    queryKey: ['runs', 'sample'],
    queryFn: () => api.runs({ limit: 100 }),
    refetchInterval: 2500,
  });
  const { data: inbox } = useQuery<InboxItem[]>({
    queryKey: ['inbox'],
    queryFn: () => api.inbox(),
    refetchInterval: 10000,
  });
  const { data: tokenStats } = useQuery<StatsTokens>({
    queryKey: ['stats', 'tokens'],
    queryFn: () => api.statsTokens(),
    refetchInterval: 30000,
  });
  const { data: lanes } = useQuery<Lane[]>({
    queryKey: ['lanes'],
    queryFn: () => api.lanes(),
    refetchInterval: 10000,
  });

  const runs = Array.isArray(sample) ? sample.filter(run => run.options?.runtime === 'codex-sdk') : [];
  // "Running now" and "Waiting on you" are two different questions, and
  // answering them with one list is what let Home say RUNNING while the
  // platform sat on a person's decision (Q-03, M-11).
  const waitingOnYou = runs.filter((r) => r.waitingForPerson || r.state === 'blocked');
  const running = runs.filter((r) => r.state === 'running' && !r.waitingForPerson);
  const recent = runs.slice(0, 7);
  const terminal = runs.filter((r) => r.state === 'passed' || r.state === 'failed');
  const passed = terminal.filter((r) => r.state === 'passed').length;
  const pending = Array.isArray(inbox) ? inbox : [];

  const activeLanes = (Array.isArray(lanes) ? lanes : []).filter((l) => l.status !== 'archived');
  const laneReview = activeLanes.reduce((sum, l) => sum + l.counts.review, 0);
  const laneBlocked = activeLanes.reduce((sum, l) => sum + l.counts.blocked, 0);

  // Opening a run IS the navigation now — the run's id is its URL.
  const open = (id: string) => setRunId(id);

  return (
    <div className="view">
      <div className="view-head">
        <span className="clock mono">{clock.toLocaleTimeString('en-GB')}</span>
        <span className="eyebrow">Control · local</span>
        <h1 className="h1">Hyperplane</h1>
      </div>

      {/* The front door — the one thing on Home that STARTS work rather than
          reporting on it, and now the first thing on the page. It absorbs the
          "launch a workflow" prompt that used to hide inside the empty
          Running-now card.

          It used to open below a metric band led by "PASS RATE 11%", which is
          the platform's self-assessment answering a question nobody arrived
          with. The numbers are all still here — they are just no longer the
          greeting. */}
      <StartSomething defaultProject={scope ?? ''} />

      <div className="bento">
        {/* Waiting on you — runs that are parked on a decision, apart from the
            ones actually executing. */}
        {waitingOnYou.length > 0 && (
          <div className="panel card span-2">
            <div className="card-head">
              <span className="eyebrow">Waiting on you</span>
              <Link to="/inbox" className="link">decide →</Link>
            </div>
            <div className="rows">
              <RunList runs={waitingOnYou} selectedRunId={null} onSelect={(id) => open(id)} />
            </div>
          </div>
        )}

        {/* Running now */}
        <div className="panel card span-2">
          <div className="card-head">
            <span className="eyebrow">Running now</span>
            <Link to="/runs" className="link">all runs →</Link>
          </div>
          {running.length === 0 ? (
            <div className="empty">
              <span className="mark">⌗</span>
              <span>Nothing running.</span>
            </div>
          ) : (
            <div className="rows">
              <RunList
                runs={running}
                selectedRunId={null}
                onSelect={(id) => open(id)}
              />
            </div>
          )}
        </div>

        {/* Needs you */}
        <div className="panel card">
          <div className="card-head">
            <span className="eyebrow">Needs you</span>
            {pending.length > 0 && <Link to="/inbox" className="link">{pending.length} decision{pending.length === 1 ? '' : 's'} →</Link>}
          </div>
          {pending.length === 0 ? (
            <div className="empty"><span className="mark">✓</span><span>No decisions waiting.</span></div>
          ) : (
            <div className="rows">
              {pending.slice(0, 5).map((item) => (
                <Link key={item.id} to="/inbox" className="row home-needs-row" style={{ gridTemplateColumns: 'auto 1fr' }}>
                  <span className={`chip ${item.type === 'selection' ? 's-live' : 's-block'}`}>{needsYouChip(item.type)}</span>
                  {/* The whole name wraps. "APPROVAL A… …767c9e" was not a name
                      anyone could choose between (S-06). */}
                  <span className="name home-needs-name">{needsYouTitle(item)}</span>
                </Link>
              ))}
            </div>
          )}
        </div>

        {/* Recent runs */}
        <div className="panel card span-2">
          <div className="card-head">
            <span className="eyebrow">Recent runs</span>
            <Link to="/runs" className="link">view all →</Link>
          </div>
          {recent.length === 0 ? (
            <div className="empty"><span className="mark">⌗</span><span>No runs yet.</span></div>
          ) : (
            <div className="rows">
              <RunList
                runs={recent}
                selectedRunId={null}
                onSelect={(id) => open(id)}
              />
            </div>
          )}
        </div>

        {/* Board summary */}
        <div className="panel card">
          <div className="card-head">
            <span className="eyebrow">Board</span>
            <Link to="/board" className="link">{activeLanes.length} lane{activeLanes.length === 1 ? '' : 's'} →</Link>
          </div>
          {activeLanes.length === 0 ? (
            <div className="empty">
              <span className="mark">⬡</span>
              <span>No lanes yet.</span>
            </div>
          ) : (
            <div className="rows">
              <Link to="/board" className="row" style={{ gridTemplateColumns: '1fr auto' }}>
                <span className="name">In review</span>
                <span className="meta mono">{laneReview}</span>
              </Link>
              <Link to="/board" className="row" style={{ gridTemplateColumns: '1fr auto' }}>
                <span className="name">Blocked</span>
                <span className="meta mono">{laneBlocked}</span>
              </Link>
            </div>
          )}
        </div>
      </div>

      {/* Platform stats — every number that used to lead the page, kept in full
          and moved to the end of it. Nothing is deleted: pass rate, cache hit
          and the token charts are exactly as they were. They are simply after
          the work now, where a statistic belongs. */}
      <div className="section-head">
        <span className="eyebrow">Platform stats</span>
        <span className="mono faint" style={{ fontSize: 'var(--t-xs)' }}>
          across {runs.length} recent run{runs.length === 1 ? '' : 's'}
        </span>
      </div>

      <div className="metrics metrics--5 metrics--quiet">
        <div className="metric">
          <span className="k">Runs</span>
          <span className="v mono">{runs.length}</span>
        </div>
        {/* A percentage with no denominator hid that "80%" was 4 of 5 (Q-08). */}
        <div className="metric" title={passRateSentence(passed, terminal.length, running.length)}>
          <span className="k">Finished runs passed</span>
          <span className="v mono">{terminal.length === 0 ? '—' : <>{passed}<small> of {terminal.length}</small></>}</span>
          <span className="metric-sub">{passRateSentence(passed, terminal.length, running.length)}</span>
        </div>
        <div className="metric" title="Runs with a step executing right now. Runs parked on your decision are counted under Needs you.">
          <span className="k">Running now</span>
          <span className="v mono">{running.length}</span>
        </div>
        <div className="metric">
          <span className="k">Needs you</span>
          <span className="v mono">{pending.length}</span>
        </div>
        <div className="metric" title="How much of what we sent the model was served from its cache.">
          <span className="k">Cache hit</span>
          <span className="v mono">
            {tokenStats && tokenStats.totalCalls > 0
              ? <>{Math.round(tokenStats.cacheHitRate * 100)}<small>%</small></>
              : '—'}
          </span>
          {tokenStats && tokenStats.totalCalls > 0 && (
            <span className="metric-sub mono">
              {fmtTokens(tokenStats.inputTokens)} in · {fmtTokens(tokenStats.outputTokens)} out
            </span>
          )}
        </div>
      </div>

      {/* Token stats section */}
      {tokenStats && tokenStats.totalCalls > 0 && (
        <div className="panel" style={{ marginTop: 'var(--s5)', padding: 'var(--s4) var(--s5) var(--s5)' }}>
          <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', marginBottom: 'var(--s4)' }}>
            <span className="eyebrow">Recorded token usage</span>
            <span className="mono faint" style={{ fontSize: 'var(--t-xs)' }}>{tokenStats.totalCalls} calls</span>
          </div>

          <p className="faint">New usage recorded by each execution. Reused usage from a continuation is counted once here; an individual run includes it toward its original budget. Interrupted turns may have unreported usage.</p>

          {/* Separated figures */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4,1fr)', gap: '1px', background: 'var(--line)', border: '1px solid var(--line)', borderRadius: 'var(--r-md)', overflow: 'hidden', marginBottom: 'var(--s4)' }}>
            {[
              { k: 'IN', v: tokenStats.inputTokens },
              { k: 'OUT', v: tokenStats.outputTokens },
              { k: 'CACHED', v: tokenStats.cachedInputTokens },
              { k: 'HIT RATE', v: null },
            ].map(({ k, v }) => (
              <div key={k} style={{ background: 'var(--paper-2)', padding: 'var(--s3) var(--s4)', display: 'flex', flexDirection: 'column', gap: '4px' }}>
                <span style={{ font: '500 var(--t-xs)/1 var(--font-mono)', letterSpacing: '0.1em', textTransform: 'uppercase', color: 'var(--ink-3)' }}>{k}</span>
                <span style={{ fontFamily: 'var(--font-mono)', fontVariantNumeric: 'tabular-nums', fontWeight: 700, fontSize: 'var(--t-h2)', letterSpacing: '-0.02em' }}>
                  {k === 'HIT RATE'
                    ? <>{Math.round(tokenStats.cacheHitRate * 100)}<small style={{ fontSize: '0.6em', fontWeight: 500, color: 'var(--ink-3)' }}>%</small></>
                    : fmtTokens(v as number)
                  }
                </span>
              </div>
            ))}
          </div>

          {/* Charts — only render when we have byRun data */}
          {tokenStats.byRun && tokenStats.byRun.length >= 2 && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--s3)' }}>
              {/* Input/output stacked bar chart */}
              <div>
                <span style={{ display: 'block', font: '500 var(--t-xs)/1 var(--font-mono)', color: 'var(--ink-3)', letterSpacing: '0.08em', textTransform: 'uppercase', marginBottom: 'var(--s2)' }}>
                  New input <span style={{ opacity: 0.6 }}>·</span> Output per execution
                  <span style={{ marginLeft: 'var(--s3)', color: 'var(--st-pass)', opacity: 0.9 }}>■</span>
                  <span style={{ marginLeft: '4px', opacity: 0.7 }}>cached</span>
                </span>
                <TokenBarChart runs={tokenStats.byRun} />
              </div>

              {/* Cache-hit-rate trend */}
              <div>
                <span style={{ display: 'block', font: '500 var(--t-xs)/1 var(--font-mono)', color: 'var(--ink-3)', letterSpacing: '0.08em', textTransform: 'uppercase', marginBottom: 'var(--s2)' }}>
                  Cache hit rate trend
                </span>
                <CacheRateTrendChart runs={tokenStats.byRun} />
              </div>

              {/* x-axis label */}
              <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                <span style={{ font: '500 var(--t-xs)/1 var(--font-mono)', color: 'var(--ink-3)' }}>oldest</span>
                <span style={{ font: '500 var(--t-xs)/1 var(--font-mono)', color: 'var(--ink-3)' }}>newest →</span>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
