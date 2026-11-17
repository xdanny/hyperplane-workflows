import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import type { Run, Health } from '../api';
import { TerminalPane } from '../components/TerminalPane';
import type { TermEngine } from '../components/TerminalPane';
import { StateChip } from '../components/StateChip';

type Tab = 'orchestrator' | 'shell';

function initialEngine(): TermEngine {
  return localStorage.getItem('hp_term_engine') === 'ghostty' ? 'ghostty' : 'xterm';
}

/**
 * Console — the orchestrator terminal. Two PTY-backed tabs (the interactive
 * orchestrator and a login shell) with native visibility into the environment:
 * live health and the active/recent runs, alongside the terminal.
 */
export function Console() {
  const [tab, setTab] = useState<Tab>('orchestrator');
  const [engine, setEngine] = useState<TermEngine>(initialEngine);

  const toggleEngine = () => {
    const next: TermEngine = engine === 'ghostty' ? 'xterm' : 'ghostty';
    localStorage.setItem('hp_term_engine', next);
    setEngine(next);
  };

  const { data: health } = useQuery<Health>({
    queryKey: ['health'],
    queryFn: () => api.health(),
    refetchInterval: 5000,
  });
  const { data: runs } = useQuery<Run[]>({
    queryKey: ['runs', 'console'],
    queryFn: () => api.runs({ limit: 12 }),
    refetchInterval: 4000,
  });

  const active = (runs ?? []).filter((r) => r.state === 'running' || r.state === 'blocked');
  const recent = (runs ?? []).slice(0, 8);

  return (
    <div className="view console-view">
      <header className="view-head">
        <div>
          <div className="eyebrow">orchestrator</div>
          <h1 className="h1">Console</h1>
        </div>
        <p className="view-sub">
          Describe projects and workflows to save or update them. Start saved workflows from the workflow editor; use Shell for host commands.
        </p>
      </header>

      <div className="console-grid">
        <section className="console-term card">
          <div className="term-tabs">
            <button
              className={`term-tab${tab === 'orchestrator' ? ' active' : ''}`}
              onClick={() => setTab('orchestrator')}
            >
              ❯ Orchestrator
            </button>
            <button
              className={`term-tab${tab === 'shell' ? ' active' : ''}`}
              onClick={() => setTab('shell')}
            >
              $ Shell
            </button>
            <button
              className="term-engine-toggle"
              onClick={toggleEngine}
              title="Switch terminal engine (ghostty-web ⇄ xterm.js)"
              aria-label={`terminal engine: ${engine}`}
            >
              engine: {engine}
            </button>
          </div>
          <div className="term-stack">
            <TerminalPane kind="orchestrator" active={tab === 'orchestrator'} engine={engine} />
            <TerminalPane kind="shell" active={tab === 'shell'} engine={engine} />
          </div>
        </section>

        <aside className="console-env">
          <div className="card env-card">
            <div className="eyebrow">environment</div>
            <ul className="env-list">
              <li>
                <span>API</span>
                <b className={health?.ok ? 'ok' : 'bad'}>{health?.ok ? 'online' : 'down'}</b>
              </li>
              <li>
                <span>Postgres</span>
                <b className={health?.db === 'ok' ? 'ok' : 'bad'}>{health?.db ?? '…'}</b>
              </li>
              <li>
                <span>codex</span>
                <b className={health?.codex === 'ok' ? 'ok' : 'bad'}>{health?.codex ?? '…'}</b>
              </li>
              <li>
                <span>local engine</span>
                <b className={health?.localEngine?.startsWith('reachable') ? 'ok' : 'muted'}>
                  {health?.localEngine?.startsWith('reachable') ? 'reachable' : 'off'}
                </b>
              </li>
            </ul>
          </div>

          <div className="card env-card">
            <div className="eyebrow">active · {active.length}</div>
            {active.length === 0 ? (
              <p className="env-empty">No runs in flight.</p>
            ) : (
              <ul className="env-runs">
                {active.map((r) => (
                  <li key={r.runId}>
                    <StateChip state={r.state} />
                    <span className="env-run-name" title={r.goal || r.dag}>
                      {r.goal || r.dag}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div className="card env-card">
            <div className="eyebrow">recent</div>
            <ul className="env-runs">
              {recent.map((r) => (
                <li key={r.runId}>
                  <StateChip state={r.state} />
                  <span className="env-run-name" title={r.goal || r.dag}>
                    {r.goal || r.dag}
                  </span>
                </li>
              ))}
            </ul>
          </div>
        </aside>
      </div>
    </div>
  );
}
