import { useEffect, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import type { ProjectCount, Run, RunState } from '../api';
import { RunList } from '../components/RunList';
import { SDKRun } from '../components/SDKRun';
import { ReadStatus } from '../components/ReadStatus';
import { ProjectSelector } from '../components/ProjectSelector';
import { useSelectedRun } from '../hooks/useSelectedRun';
import { useSelectedProject } from '../hooks/useSelectedProject';
import { useProjectScope } from '../hooks/useProjectScope';
import { groupRunsByWorkflow, sortRunsByStarted } from '../runLexicon';

type FilterState = 'all' | RunState;
const FILTER_OPTIONS: { label: string; value: FilterState }[] = [
  { label: 'All', value: 'all' },
  { label: 'Running', value: 'running' },
  { label: 'Passed', value: 'passed' },
  { label: 'Failed', value: 'failed' },
  { label: 'Stopped', value: 'interrupted' },
];

function RunDetailView({ runId, onBack, onContinuedRunId }: { runId: string; onBack: () => void; onContinuedRunId: (id:string)=>void }) {
  const detail = useQuery({
    queryKey: ['run', runId], queryFn: () => api.runDetail(runId),
    // P-04/M-01: the page must move on its own while there is anything to
    // report. `running` alone was not enough — a run parked on an approval and
    // a run whose API row has not caught up with its session both need the
    // poll, and both used to sit still until somebody hard-reloaded.
    refetchInterval: query => {
      const run = query.state.data?.run;
      if (!run) return false;
      return run.state === 'running' || run.state === 'blocked' || run.waitingForPerson ? 3000 : false;
    },
    // Keep polling while the tab is in the background. Launching a run and
    // switching away is the normal way to use this page, and without it the
    // reader came back to the snapshot from before they left and had to hard
    // reload — which is how the staleness read as a stuck run (M-01, P-04).
    refetchIntervalInBackground: true,
    // A stale row is worse than a slow one here: a decision recorded elsewhere
    // must show up on the next tick, not after this query's stale window.
    staleTime: 0,
    refetchOnWindowFocus: true,
  });
  if (detail.data?.run?.options?.runtime === 'codex-sdk') {
    return <SDKRun
      key={runId}
      run={detail.data.run}
      detailOutcome={detail.data.outcome ?? null}
      journey={detail.data.journey ?? null}
      accepted={(detail.data.outcome ?? detail.data.journey?.outcome)?.verdict === 'accepted'}
      onBack={onBack}
      onContinuedRunId={onContinuedRunId}
    />;
  }
  return <div>
    <button className="btn btn--ghost" onClick={onBack}>← Back to runs</button>
    {detail.isPending ? <p role="status">Loading run…</p> : detail.error ?
      <p role="alert">Could not load this run. {detail.error.message} <button className="btn" onClick={() => void detail.refetch()}>Retry</button></p> :
      <p>This run used a retired execution format. Open Workflows to create an SDK workflow.</p>}
  </div>;
}

export function Runs() {
  const [filter, setFilter] = useState<FilterState>('all');
  // Grouped by workflow by default — see the toggle below.
  const [groupByWorkflow, setGroupByWorkflow] = useState(true);
  // Project scope is app-wide: opening a project here keeps the Board and
  // Knowledge on the same project.
  const { project, setProject } = useProjectScope();
  const { runId: selectedRunId, setRunId } = useSelectedRun();
  const navigate = useNavigate();

  // The nav's Projects entry lands here as /projects — the projects rail is the
  // Projects surface, and always has been; it was simply unreachable without
  // knowing to look inside Runs.
  const [searchParams, setSearchParams] = useSearchParams();
  // `/projects` and `/projects/<tag>` render this same view with the rail open,
  // which is what makes a project linkable (P-13). The legacy `?projects=1`
  // query keeps working so an old bookmark still lands on the panel.
  const { onProjectsRoute, projectId, openProject } = useSelectedProject();
  // `?project=<tag>` is the other way people write this link — it is the name of
  // the scope on every other surface, so a reader who has one in hand types it
  // here and expects the project to open. It did nothing (S-12d). It selects the
  // project now and opens the rail, exactly as `/projects/<tag>` does.
  const queryProject = searchParams.get('project')?.trim() || null;
  const projectsOpen =
    onProjectsRoute || searchParams.get('projects') === '1' || queryProject !== null;
  const setProjectsOpen = (open: boolean) => {
    if (onProjectsRoute) {
      // Closing the panel leaves the Projects destination entirely — staying on
      // /projects with nothing open would be a URL that lies about the screen.
      if (!open) navigate('/runs');
      return;
    }
    const next = new URLSearchParams(searchParams);
    if (open) next.set('projects', '1');
    else next.delete('projects');
    setSearchParams(next, { replace: true });
  };

  // The path is the source of truth for which project is open. The app-wide
  // scope store follows it, so opening /projects/taxi-analytics in a fresh tab
  // scopes the Board and Knowledge to the same project the link named.
  useEffect(() => {
    const wanted = onProjectsRoute ? projectId : queryProject;
    if ((onProjectsRoute || wanted !== null) && wanted !== project) setProject(wanted);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [onProjectsRoute, projectId, queryProject]);

  // A project id nobody has ever registered. Falling back to the full list with
  // no notice makes a typo look like an empty account (S-12e): the reader is
  // told the name they asked for does not exist, and the list underneath is
  // then obviously the list, not the answer.
  const knownProjectsQuery = useQuery<ProjectCount[]>({
    queryKey: ['projects'],
    queryFn: () => api.projects(),
    enabled: projectsOpen,
  });
  const askedFor = (onProjectsRoute ? projectId : queryProject) ?? null;
  const projectNotFound =
    askedFor !== null && knownProjectsQuery.isSuccess &&
    !knownProjectsQuery.data.some((p) => p.project === askedFor);

  /** A project chip or card changed the scope: on the Projects destination that
   *  is a navigation, everywhere else it is only a filter. */
  const changeProject = (p: string | null) => {
    setProject(p);
    if (onProjectsRoute) {
      if (p === '') navigate('/runs');
      else openProject(p);
    }
  };
  /** Opening a run leaves the projects rail — the run's own path carries no
   *  `projects` param, so navigating there drops it without a second step. */
  const openRun = (id: string) => setRunId(id);

  const runsQuery = useQuery<Run[]>({
    queryKey: ['runs', filter, project],
    queryFn: () =>
      api.runs({
        ...(filter !== 'all' ? { state: filter } : {}),
        project,
        limit: 100,
      }),
    // Three seconds while anything is live or parked on a person, backing off
    // to thirty when the whole list is finished — a list of settled runs does
    // not need a request every three seconds in a background tab.
    refetchInterval: query =>
      query.state.data?.some(run => run.state === 'running' || run.state === 'blocked' || run.waitingForPerson)
        ? 3000 : 30000,
    refetchIntervalInBackground: true,
  });

  const safeRuns = Array.isArray(runsQuery.data) ? runsQuery.data.filter(run => run.options?.runtime === 'codex-sdk') : [];
  // "Waiting on you" is counted apart from "running now": a run parked on an
  // approval is not work in progress, and counting it as such is what let the
  // list say RUNNING while nothing ran (Q-03, M-11).
  const waitingCount = safeRuns.filter((r) => r.waitingForPerson || r.state === 'blocked').length;
  const runningCount = safeRuns.filter((r) => r.state === 'running' && !r.waitingForPerson).length;

  // `/runs/<id>` is the detail page — including when it was pasted in from
  // somewhere else. The projects rail lives on the list route (`/runs?projects=1`)
  // and so can no longer collide with a selection.
  if (selectedRunId) {
    return (
      <div className="view">
        <RunDetailView
          runId={selectedRunId}
          onContinuedRunId={setRunId}
          onBack={() => setRunId(null)}
        />
      </div>
    );
  }

  return (
    <div className="view">
      <div className="view-head">
        <span className="eyebrow">{onProjectsRoute ? 'Projects' : 'Runs'}</span>
        <div className="runs-head-row">
          <h1 className="h1">{onProjectsRoute ? 'Projects' : 'Runs'}</h1>
          <div className="runs-head-stats">
            {runningCount > 0 && (
              <span className="chip s-live" style={{ fontSize: 'var(--t-xs)' }}>
                <span className="dot" />{runningCount} running
              </span>
            )}
            {waitingCount > 0 && (
              <span className="chip s-block" style={{ fontSize: 'var(--t-xs)' }}>
                <span className="dot" />{waitingCount} waiting on you
              </span>
            )}
          </div>
        </div>
      </div>

      {/* A link naming a project that does not exist. Said out loud, above the
          list — silently showing every project instead makes a typo read as an
          empty account (S-12e). */}
      {projectNotFound && (
        <p className="runs-project-missing" role="status">
          No project called <span className="mono">{askedFor}</span> is registered here. The
          projects this platform knows about are listed below.
        </p>
      )}

      <ProjectSelector
        value={project}
        onChange={changeProject}
        defaultOpen={projectsOpen}
        onOpenChange={setProjectsOpen}
      />

      {/* Filter tabs */}
      <div className="filters">
        {FILTER_OPTIONS.map(({ label, value }) => (
          <button
            key={value}
            className={`filter${filter === value ? ' on' : ''}`}
            onClick={() => setFilter(value)}
          >
            {label}
            {value === 'blocked' && waitingCount > 0 && (
              <span className="filter-badge" aria-label={`${waitingCount} waiting on you`}>
                {waitingCount}
              </span>
            )}
          </button>
        ))}
      </div>

      <ReadStatus query={runsQuery} label="runs" />

      {runsQuery.isSuccess && safeRuns.length === 0 && (
        <div className="empty" style={{ paddingTop: 'var(--s7)' }}>
          <span className="mark">⌗</span>
          <span>No runs match this filter.</span>
        </div>
      )}

      {safeRuns.length > 0 && (
        <>
          {/* Grouping is on by default because the flat list was the problem:
              three runs of one workflow read identically in it (P-10, S-02).
              Turning it off returns the plain chronological list. */}
          <label className="runs-group-toggle">
            <input
              type="checkbox"
              checked={groupByWorkflow}
              onChange={(event) => setGroupByWorkflow(event.target.checked)}
            />
            <span>Group by workflow</span>
          </label>
          {groupByWorkflow ? (
            <div className="runs-groups">
              {groupRunsByWorkflow(safeRuns).map((group) => (
                <div key={group.key} className="runs-group panel">
                  <div className="runs-group-head">
                    <span className="runs-group-name">
                      {group.workflowId
                        ? <Link to={`/workflows/${encodeURIComponent(group.workflowId)}`}>{group.name}</Link>
                        : group.name}
                    </span>
                    <span className="runs-group-count faint">
                      {group.runs.length} run{group.runs.length === 1 ? '' : 's'}
                    </span>
                    {group.runs.length >= 2 && group.workflowId && (
                      <Link
                        className="btn btn--ghost btn--sm"
                        to={`/workflows/${encodeURIComponent(group.workflowId)}?compare=${encodeURIComponent(group.runs[0].runId)},${encodeURIComponent(group.runs[1].runId)}`}
                      >
                        Compare latest two
                      </Link>
                    )}
                  </div>
                  <span className="runs-group-latest eyebrow">latest</span>
                  <RunList runs={group.runs} selectedRunId={null} onSelect={openRun} />
                </div>
              ))}
            </div>
          ) : (
            <div className="runs-list panel">
              {/* Ordered by the column the rows actually show. The audited list
                  sorted by end time while displaying start time, so a long run
                  sat between two later-started rows and read as broken (M-17).
                  The server orders the same way; this keeps the list honest
                  about its own column whatever arrives. */}
              <RunList runs={sortRunsByStarted(safeRuns)} selectedRunId={null} onSelect={openRun} />
            </div>
          )}
        </>
      )}
    </div>
  );
}
