import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../api';
import type {
  DatasetDecl,
  Decision,
  EnvironmentDecl,
  Lane,
  LoadSemanticsDecl,
  ProjectCount,
  ProjectDeclarations,
  ProjectDetail,
  PurposeDecl,
  Run,
} from '../api';
import { LOAD_MODES, savedWorkflowIdOf } from '../api';
import { DeclarationGroups, cleanGroups } from './DeclarationGroups';
import { useSelectedRun } from '../hooks/useSelectedRun';
import { ReadStatus } from './ReadStatus';
import { Link } from 'react-router-dom';
import { quotedNote } from '../runLexicon';
import { decisionLedgerLine, ledgerVerdict, ledgerVerdictClass, projectNarrativeFallback } from '../decisionLedger';
import { NO_PROJECTS_HINT } from './ProjectPicker';

/**
 * ProjectsPanel — the Projects surface, expanded from the project scope chips
 * rather than added as another nav destination.
 *
 * Two levels:
 *   index  — a card per project: name, description, per-kind counts, last
 *            activity. Opening one scopes the whole app to it.
 *   detail — that project's workflows, recent runs, lanes and decisions, plus
 *            an inline edit for its name and description.
 *
 * "Workflows of a project" is derived honestly server-side from the runs that
 * carry the tag, using the backend-owned workflow identity. Nothing here claims
 * a workflow belongs to a project that never ran it.
 */
export function ProjectsPanel({
  scoped,
  onOpen,
  onScopeUnscoped,
  onClose,
}: {
  scoped: string | null;
  onOpen: (project: string) => void;
  /**
   * Filter to the unscoped bucket. Separate from onOpen because onOpen('')
   * already means "leave the detail view" — the two would otherwise collide
   * on the same empty string with opposite meanings.
   */
  onScopeUnscoped: () => void;
  onClose: () => void;
}) {
  const projectsQuery = useQuery<ProjectCount[]>({
    queryKey: ['projects'],
    queryFn: () => api.projects(),
  });
  const [creating, setCreating] = useState(false);
  const projects = Array.isArray(projectsQuery.data) ? projectsQuery.data : [];
  // The unscoped bucket is a filter, not a project — it has no row to open and
  // no detail view. It is still shown, as a labelled bucket below the grid,
  // because work that belongs to no project has to be reachable; what it must
  // never be is a nameless card sitting among the real ones.
  const real = projects.filter((p) => p.project !== '');
  const unscoped = projects.find((p) => p.project === '' && p.count > 0);
  const open = scoped ? real.find((p) => p.project === scoped) : undefined;

  return (
    <section className="projpanel" aria-label="Projects">
      <div className="projpanel-head">
        <span className="eyebrow">
          {open ? 'Project' : 'Projects'} {projectsQuery.data !== undefined && <span className="mono">{real.length}</span>}
        </span>
        <div className="projpanel-actions">
          {open && (
            <button className="btn btn--sm" onClick={() => onOpen('')} aria-label="Back to all projects">
              ← All projects
            </button>
          )}
          {!open && (
            <button className="btn btn--sm" onClick={() => setCreating((v) => !v)}>
              {creating ? 'Cancel' : 'New project'}
            </button>
          )}
          <button className="btn btn--sm" onClick={onClose} aria-label="Close the projects panel">
            Close
          </button>
        </div>
      </div>

      {creating && !open && <ProjectForm onDone={() => setCreating(false)} />}

      <ReadStatus query={projectsQuery} label="projects" />
      {open ? (
        <ProjectDetailView key={open.project} entry={open} />
      ) : projectsQuery.data !== undefined && (
        <>
          {real.length === 0 ? projectsQuery.isSuccess && (
            <div className="empty">
              <span className="mark">◳</span>
              <span>{NO_PROJECTS_HINT}</span>
              <span className="faint">
                Compose from a prompt with a project chosen, run a recipe under one, or use
                “New project” above.
              </span>
            </div>
          ) : (
            <div className="proj-grid">
              {real.map((p) => (
                <button
                  key={p.project}
                  className="panel proj-card"
                  // The card is a button made of divs: without a name of its own
                  // a screen reader announces the whole card as its label, or
                  // nothing at all. Say what pressing it does.
                  aria-label={`Open project ${p.name || p.project}`}
                  onClick={() => onOpen(p.project)}
                >
                  <div className="proj-card-head">
                    <strong>{p.name || p.project}</strong>
                    <span className="chip proj-card-status">{p.registered ? p.status : 'tag only'}</span>
                  </div>
                  <div className="proj-card-id mono">{p.project}</div>
                  <p className="proj-card-summary">{p.summary || 'No description yet.'}</p>
                  <ProjectCounts entry={p} />
                  <div className="proj-card-when">{lastActivityLabel(p)}</div>
                </button>
              ))}
            </div>
          )}
          {unscoped && <UnscopedBucket entry={unscoped} onScope={() => onScopeUnscoped()} />}
        </>
      )}
    </section>
  );
}

/**
 * The empty-tag aggregate, rendered as what it is: a bucket of work that
 * belongs to no project, with a way to look at it. It is visually apart from
 * the project grid because it is not a project — there is nothing to open, no
 * description to edit, and it can never be chosen as a destination for new
 * work.
 */
function UnscopedBucket({ entry, onScope }: { entry: ProjectCount; onScope: () => void }) {
  return (
    <div className="proj-unscoped">
      <div className="proj-unscoped-body">
        <strong>{entry.name || 'Unscoped'}</strong>
        <span className="faint">
          {countedUnits(entry)} that {countedUnitsSingular(entry) ? 'belongs' : 'belong'} to no
          project.
        </span>
      </div>
      <button className="btn btn--sm" onClick={onScope}>
        Show only these
      </button>
    </div>
  );
}

/**
 * A count with its units spelled out: "27 runs and 6 decisions".
 *
 * `count` is a SUM of three different kinds of thing, and "33 items" makes the
 * reader guess which. The kinds are already on the row, so name them; only the
 * ones that are actually there are mentioned, and a total that no breakdown
 * accounts for falls back to "items" rather than inventing a unit.
 */
export function countedUnits(entry: ProjectCount): string {
  const parts = countedParts(entry);
  if (parts.length === 0) return `${entry.count} item${entry.count === 1 ? '' : 's'}`;
  if (parts.length === 1) return parts[0];
  return `${parts.slice(0, -1).join(', ')} and ${parts[parts.length - 1]}`;
}

/** Whether countedUnits names exactly one thing, so a following verb agrees:
 *  "1 run that BELONGS", but "1 run and 1 decision that BELONG". */
export function countedUnitsSingular(entry: ProjectCount): boolean {
  const parts = countedParts(entry);
  if (parts.length === 0) return entry.count === 1;
  return parts.length === 1 && entry.runs + entry.lanes + entry.decisions === 1;
}

function countedParts(entry: ProjectCount): string[] {
  return ([
    [entry.runs, 'run'],
    [entry.lanes, 'lane'],
    [entry.decisions, 'decision'],
  ] as [number, string][])
    .filter(([n]) => n > 0)
    .map(([n, unit]) => `${n} ${unit}${n === 1 ? '' : 's'}`);
}

/** The per-kind breakdown shown on a card and on the detail header. */
function ProjectCounts({ entry }: { entry: ProjectCount }) {
  return (
    <dl className="proj-counts">
      <div><dt>workflows</dt><dd className="mono">{entry.workflowCount}</dd></div>
      <div><dt>runs</dt><dd className="mono">{entry.runs}</dd></div>
      <div><dt>lanes</dt><dd className="mono">{entry.lanes}</dd></div>
      <div><dt>decisions</dt><dd className="mono">{entry.decisions}</dd></div>
    </dl>
  );
}

/** One project: workflows it has run, plus its recent runs, lanes, decisions. */
function ProjectDetailView({ entry }: { entry: ProjectCount }) {
  const id = entry.project;
  const [editing, setEditing] = useState(false);
  // Opening a run navigates to its own URL, which is also what leaves the
  // projects rail — the run path carries no `projects` param, so no separate
  // close step is needed here.
  const { openRun } = useSelectedRun();
  const detailQuery = useQuery<ProjectDetail>({
    queryKey: ['project', id],
    queryFn: () => api.project(id),
  });
  const runsQuery = useQuery<Run[]>({
    queryKey: ['runs', 'project', id],
    queryFn: () => api.runs({ project: id, limit: 8 }),
  });
  const lanesQuery = useQuery<Lane[]>({
    queryKey: ['lanes', id],
    queryFn: () => api.lanes(id),
  });
  const decisionsQuery = useQuery<Decision[]>({
    queryKey: ['decisions', id],
    queryFn: () => api.decisions(id),
  });

  const detail = detailQuery.data;
  const workflows = detail?.workflows ?? [];
  const runList = Array.isArray(runsQuery.data) ? runsQuery.data : [];
  const laneList = Array.isArray(lanesQuery.data) ? lanesQuery.data : [];
  const decisionList = Array.isArray(decisionsQuery.data) ? decisionsQuery.data : [];

  return (
    <div className="proj-detail">
      <div className="proj-detail-head">
        <div>
          <div className="h2">{entry.name || id}</div>
          <div className="proj-card-id mono">{id}</div>
          {/* TWO blocks, never one. They are two sentences by two authors:
              the owner says what the project is FOR, and the platform says what
              it has BUILT, regenerated from the briefs of accepted deliveries
              (internal/brief). Sharing one field is what let a delivery
              overwrite an owner's description, and made a project with zero
              runs render typed intent under a heading claiming it was a result.
              The narrative block shows the machine's own words or nothing —
              never the owner's text under the owner's own heading. */}
          <span className="eyebrow proj-summary-label">What this project is for</span>
          <p className="proj-card-summary">{entry.summary || 'No description yet.'}</p>
          {/* The rest of what the owner DECLARED, read-only. The page used to
              show two paragraphs and nothing else, so who owns a project, when
              it is due and what it must not do were only legible by opening the
              editor — a form, to read a fact. */}
          <DeclaredFacts declarations={detail?.declarations ?? entry.declarations} />
          <span className="eyebrow proj-summary-label">What this project has built so far</span>
          {/* The machine's own words when it has written any; otherwise a
              sentence derived from the SAME rows the ledger below shows, so
              "Nothing has been accepted" can never sit above three decisions
              (S-13). */}
          <p className="proj-card-summary proj-card-narrative">
            {entry.narrative || projectNarrativeFallback(decisionList)}
          </p>
        </div>
        <button className="btn btn--sm" onClick={() => setEditing((v) => !v)}>
          {editing ? 'Cancel' : 'Edit'}
        </button>
      </div>
      {editing && (
        <ProjectForm
          existing={entry}
          onDone={() => setEditing(false)}
        />
      )}
      <ProjectCounts entry={entry} />

      <div className="proj-section">
        <div className="section-head">
          <span className="eyebrow">workflows {detailQuery.data !== undefined && <span className="mono">{workflows.length}</span>}</span>
        </div>
        <ReadStatus query={detailQuery} label="workflows" />
        {workflows.length === 0 ? detailQuery.isSuccess && (
          <div className="empty"><span>No workflows are saved or have run under this project yet.</span></div>
        ) : (
          <ul className="proj-list">
            {workflows.map((wf) => {
              // A saved workflow's own page, which exists whether or not it has
              // ever run. A project that has just saved its first workflow used
              // to read "WORKFLOWS 0" with no row and nowhere to go (M-15). The
              // server names the workflow on the row; the key is the fallback
              // for a row it has not resolved.
              const savedId = wf.sourceWorkflowId || savedWorkflowIdOf(wf.key);
              const body = (
                <>
                  <span className="mono">{wf.name}</span>
                  {/* The run doc stamps the template ID (a uuid). The name is
                      what a person can act on, so the id only appears as a
                      tooltip and only when there is a name to show instead. */}
                  {wf.recipe && (
                    <span className="dec-tag" title={wf.recipeName ? `recipe id ${wf.recipe}` : undefined}>
                      recipe: {wf.recipeName || wf.recipe}
                    </span>
                  )}
                  <span className="proj-list-meta mono">{wf.runs} run{wf.runs === 1 ? '' : 's'}</span>
                  {wf.runs === 0 && <span className="proj-list-meta">saved, never run</span>}
                  {wf.lastState && <span className="proj-list-meta">last {wf.lastState}</span>}
                </>
              );
              // A workflow row opens its most recent run — the only run it can
              // honestly point at. Without one it stays inert rather than
              // pretending to lead somewhere.
              return (
                <li key={wf.key}>
                  {wf.lastRunId ? (
                    <button
                      className="proj-list-row proj-list-row--open"
                      onClick={() => openRun(wf.lastRunId!)}
                      title={`Open the last run of ${wf.name}`}
                    >
                      {body}
                    </button>
                  ) : savedId ? (
                    // No run to open, but the workflow itself is a destination:
                    // its page shows what it asks for and what it would do.
                    <Link className="proj-list-row proj-list-row--open" to={`/workflows/${encodeURIComponent(savedId)}`}>
                      {body}
                    </Link>
                  ) : (
                    <div className="proj-list-row">{body}</div>
                  )}
                  {savedId && wf.lastRunId && (
                    <Link className="proj-list-history" to={`/workflows/${encodeURIComponent(savedId)}`}>
                      All runs of this workflow →
                    </Link>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </div>

      <div className="proj-section">
        <div className="section-head">
          <span className="eyebrow">recent runs {runsQuery.data !== undefined && <span className="mono">{runList.length}</span>}</span>
        </div>
        <ReadStatus query={runsQuery} label="recent runs" />
        {runList.length === 0 ? runsQuery.isSuccess && (
          <div className="empty"><span>No runs yet.</span></div>
        ) : (
          <ul className="proj-list">
            {runList.map((r) => (
              <li key={r.runId}>
                <button
                  className="proj-list-row proj-list-row--open"
                  onClick={() => openRun(r.runId)}
                  title={`Open run ${r.runId}`}
                >
                  <span className="mono">{r.dag}</span>
                  <span className="proj-list-meta">{r.state}</span>
                  <span className="proj-list-meta mono">{r.runId}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="proj-section">
        <div className="section-head">
          <span className="eyebrow">lanes {lanesQuery.data !== undefined && <span className="mono">{laneList.length}</span>}</span>
        </div>
        <ReadStatus query={lanesQuery} label="lanes" />
        {laneList.length === 0 ? lanesQuery.isSuccess && (
          <div className="empty"><span>No lanes yet.</span></div>
        ) : (
          <ul className="proj-list">
            {laneList.map((l) => (
              <li key={l.id} className="proj-list-row">
                <span>{l.name}</span>
                <span className="proj-list-meta">{l.status}</span>
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="proj-section">
        <div className="section-head">
          <span className="eyebrow">decisions {decisionsQuery.data !== undefined && <span className="mono">{decisionList.length}</span>}</span>
        </div>
        <ReadStatus query={decisionsQuery} label="decisions" />
        {decisionList.length === 0 ? decisionsQuery.isSuccess && (
          <div className="empty"><span>No decisions yet.</span></div>
        ) : (
          <ul className="proj-list">
            {/* A decisions row used to read "Delivery · Produce this Monday's…
                accepted" — no name, no time, no note, not clickable, so the
                number on the project page was not defensible to anyone asking
                who accepted what and why (S-01). It then read "accepted" on a
                REJECTION, because it rendered the record's lifecycle `status`
                rather than the verdict, beside a raw `journey:` id nobody can
                recognise (S-13, D-15). Both are decisionLedger's job now. */}
            {decisionList.map((d) => {
              const verdict = ledgerVerdict(d);
              return (
                <li key={d.id} className="proj-list-row proj-decision-row">
                  <div className="proj-decision-main">
                    <span className="proj-decision-title">{d.title}</span>
                    <span className="proj-decision-meta">
                      <span className={`chip ${ledgerVerdictClass(verdict)}`}>{decisionLedgerLine(d)}</span>
                    </span>
                    {d.why && <span className="proj-decision-note">{quotedNote(d.why)}</span>}
                  </div>
                  {d.runId && (
                    <Link className="proj-list-meta" to={`/runs/${encodeURIComponent(d.runId)}`}>
                      Open run
                    </Link>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </div>
  );
}

/**
 * Create or edit a project.
 *
 * Everything a run under this project would otherwise be asked to re-type is
 * asked ONCE here: what the project is for, who accepts a delivery, what "done"
 * means, the constraints, the data it works on, and where its runs execute. The
 * old form asked for a name, an optional id and a two-line description, so
 * every brief downstream had to report "not recorded" for facts nobody was
 * asked — and every recipe form started empty under a project with seven runs
 * over the same six files.
 *
 * With `existing` it PATCHes (absent fields are left alone server-side, and
 * `declarations` is merged group by group so a group this build has never heard
 * of survives); without, it POSTs a new one — the id is derived from the name
 * unless one is typed, and a taken id comes back as a plain conflict.
 */
function ProjectForm({ existing, onDone }: { existing?: ProjectCount; onDone: () => void }) {
  const queryClient = useQueryClient();
  const declared = (existing?.declarations ?? {}) as ProjectDeclarations;
  const [name, setName] = useState(existing?.name ?? '');
  const [id, setId] = useState(existing?.project ?? '');
  const [summary, setSummary] = useState(existing?.summary ?? '');
  const [purpose, setPurpose] = useState<PurposeDecl>(declared.purpose ?? {});
  const [datasets, setDatasets] = useState<DatasetDecl[]>(declared.datasets ?? []);
  const [environment, setEnvironment] = useState<EnvironmentDecl>(declared.environment ?? {});
  // The round-2 groups — KPIs, dashboards, calendar, standing tolerances and
  // exclusions, stakeholders, delivery, and the ML set — are held as one object
  // rather than fourteen useStates. They are edited by DeclarationGroups, which
  // owns their field specs; this form owns only when they are sent.
  const [groups, setGroups] = useState<ProjectDeclarations>(declared);
  const [error, setError] = useState('');

  // Only the groups this form owns are sent. The server merges group by group,
  // so a group written by another surface — or by a later build this one has
  // never heard of — is untouched by an edit here.
  const declarations = (): ProjectDeclarations => ({
    purpose: stripEmpty(purpose),
    datasets: datasets.map(cleanDataset).filter((d) => Object.keys(d).length > 0),
    environment: stripEmpty(environment),
    ...cleanGroups(groups),
  });

  const save = useMutation({
    mutationFn: () =>
      existing
        ? api.updateProject(existing.project, { name, summary, declarations: declarations() })
        : api.createProject({
            id: id.trim() || undefined,
            name,
            summary,
            declarations: declarations(),
          }),
    onSuccess: () => {
      setError('');
      void queryClient.invalidateQueries({ queryKey: ['projects'] });
      if (existing) void queryClient.invalidateQueries({ queryKey: ['project', existing.project] });
      onDone();
    },
    onError: (e: unknown) => setError(e instanceof Error ? e.message : String(e)),
  });

  const patchDataset = (i: number, next: Partial<DatasetDecl>) =>
    setDatasets((ds) => ds.map((d, n) => (n === i ? { ...d, ...next } : d)));

  return (
    <form
      className="panel proj-form"
      onSubmit={(e) => {
        e.preventDefault();
        if (!name.trim()) {
          setError('A name is required.');
          return;
        }
        save.mutate();
      }}
    >
      <label className="proj-field">
        <span className="eyebrow">name</span>
        <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="Data Engineering" />
      </label>
      {!existing && (
        <label className="proj-field">
          <span className="eyebrow">id</span>
          <span className="proj-field-hint">
            Short name used in links and file paths — leave it alone unless an engineer asks.
          </span>
          <input className="input mono" value={id} onChange={(e) => setId(e.target.value)} placeholder="data-engineering" />
        </label>
      )}

      <label className="proj-field">
        <span className="eyebrow">what this project is for</span>
        {/* "done" is asked for once, in its own field below. It was asked for
            twice: here as a clause of this hint, and there as a box. */}
        <span className="proj-field-hint">
          One or two sentences the next reader can act on: the question this project answers or the
          thing it delivers, and who it is for. This line heads the project page until a delivery is
          accepted.
        </span>
        <textarea
          className="input"
          rows={3}
          value={summary}
          onChange={(e) => setSummary(e.target.value)}
          placeholder="Monthly reliability marts and quality audits over service_reliability_events.csv for the SRE team."
        />
      </label>

      <label className="proj-field">
        <span className="eyebrow">owner</span>
        <span className="proj-field-hint">Who decides whether a delivery is accepted.</span>
        <input
          className="input"
          value={purpose.owner ?? ''}
          onChange={(e) => setPurpose((p) => ({ ...p, owner: e.target.value }))}
          placeholder="Priya, analytics lead"
        />
      </label>

      <label className="proj-field">
        <span className="eyebrow">due</span>
        <span className="proj-field-hint">A date, if there is one. Optional.</span>
        <input
          className="input"
          type="date"
          value={purpose.due ?? ''}
          onChange={(e) => setPurpose((p) => ({ ...p, due: e.target.value }))}
        />
      </label>

      <label className="proj-field">
        <span className="eyebrow">done means</span>
        <span className="proj-field-hint">
          The sentence you would say to your boss when this is finished.
        </span>
        <textarea
          className="input"
          rows={2}
          value={purpose.doneMeans ?? ''}
          onChange={(e) => setPurpose((p) => ({ ...p, doneMeans: e.target.value }))}
          placeholder="The Q1 scorecard is accepted and the board number is signed off."
        />
      </label>

      <label className="proj-field">
        <span className="eyebrow">constraints</span>
        <span className="proj-field-hint">
          Sensitive data rules, runtime ceilings, anything a run under this project must not do.
        </span>
        <textarea
          className="input"
          rows={2}
          value={purpose.constraints ?? ''}
          onChange={(e) => setPurpose((p) => ({ ...p, constraints: e.target.value }))}
          placeholder="Never show customer names. Aggregate reporting only, never per-person decisions."
        />
      </label>

      <div className="proj-field">
        <span className="eyebrow">data it works on</span>
        <span className="proj-field-hint">
          Files, tables, dashboards or systems. Name them once here and every recipe run under this
          project offers them as defaults instead of asking you to type them again. A name on its own
          is fine — an engineer can add the technical location later.
        </span>
        {datasets.length === 0 && (
          <span className="faint">No data declared yet.</span>
        )}
        {datasets.map((ds, i) => (
          <DatasetRow
            key={i}
            index={i}
            dataset={ds}
            onChange={(next) => patchDataset(i, next)}
            onRemove={() => setDatasets((all) => all.filter((_, n) => n !== i))}
          />
        ))}
        <div className="projpanel-actions">
          <button type="button" className="btn btn--sm" onClick={() => setDatasets((all) => [...all, {}])}>
            + Add data
          </button>
        </div>
      </div>

      <label className="proj-field">
        <span className="eyebrow">where runs execute</span>
        <span className="proj-field-hint">
          The machine or container runs under this project happen on, and where their outputs land.
        </span>
        <input
          className="input"
          value={environment.where ?? ''}
          onChange={(e) => setEnvironment((v) => ({ ...v, where: e.target.value }))}
          placeholder="a local workspace with duckdb; outputs land in the run's artifacts"
        />
      </label>

      <label className="proj-field">
        <span className="eyebrow">default runtime budget</span>
        <span className="proj-field-hint">
          How long a run here may take before it should fail loudly rather than grind on. Blank means
          no ceiling, and none is ever invented.
        </span>
        <input
          className="input"
          value={environment.runtimeBudgetDefault ?? ''}
          onChange={(e) => setEnvironment((v) => ({ ...v, runtimeBudgetDefault: e.target.value }))}
          placeholder="the whole run inside 20 minutes"
        />
      </label>

      <DeclarationGroups value={groups} onChange={setGroups} />

      {error && <div className="proj-form-error">{error}</div>}
      <div className="projpanel-actions">
        <button className="btn btn--sm" type="submit" disabled={save.isPending}>
          {save.isPending ? 'Saving…' : existing ? 'Save changes' : 'Create project'}
        </button>
      </div>
    </form>
  );
}

/**
 * What the owner declared, as lines you can read.
 *
 * Only the filled fields, and never an editor: a project page that answers
 * "who owns this, when is it due, what does done mean, what may it not do, and
 * what data does it work on" is the difference between a name and a project.
 * The declarations document is the source — the same one the recipe form
 * inherits from — so these lines cannot disagree with what a run inherits.
 */
function DeclaredFacts({ declarations }: { declarations?: ProjectDeclarations }) {
  const purpose = declarations?.purpose ?? {};
  const datasets = declarations?.datasets ?? [];
  const dashboards = declarations?.dashboards ?? [];
  const kpis = declarations?.kpis ?? [];

  const named = (items: { id?: string; name?: string }[], fallback?: (i: number) => string) =>
    items
      .map((it, i) => ({
        id: (it.id ?? '').trim(),
        label: (it.name ?? '').trim() || (it.id ?? '').trim() || (fallback ? fallback(i) : ''),
      }))
      .filter((it) => it.label !== '');

  const lines: { label: string; value?: string; items?: { id: string; label: string }[] }[] = [
    { label: 'Owner', value: (purpose.owner ?? '').trim() },
    { label: 'Due', value: (purpose.due ?? '').trim() },
    { label: 'Done means', value: (purpose.doneMeans ?? '').trim() },
    { label: 'Constraints', value: (purpose.constraints ?? '').trim() },
    { label: 'Data', items: named(datasets, () => '') },
    { label: 'Dashboards', items: named(dashboards) },
    { label: 'Measures', items: named(kpis) },
  ];
  const shown = lines.filter((l) => (l.value ?? '') !== '' || (l.items?.length ?? 0) > 0);
  if (shown.length === 0) return null;

  return (
    <dl className="proj-declared">
      {shown.map((line) => (
        <div className="proj-declared-line" key={line.label}>
          <dt className="eyebrow">{line.label}</dt>
          <dd>
            {line.items ? (
              line.items.map((it, i) => (
                <span key={`${it.label}-${i}`}>
                  {i > 0 && ', '}
                  {/* One name per thing, with the handle a run cites on the
                      hover — the same name the recipe form prefills, so the two
                      screens cannot call one measure two things. */}
                  <span title={it.id && it.id !== it.label ? `declared as ${it.id}` : undefined}>
                    {it.label}
                  </span>
                </span>
              ))
            ) : (
              line.value
            )}
          </dd>
        </div>
      ))}
    </dl>
  );
}

/**
 * The two load modes, in the words the person filling the form reads them in.
 * The values are `LOAD_MODES` verbatim — what is stored is the bare mode.
 */
const LOAD_MODE_LABELS: Record<(typeof LOAD_MODES)[number], string> = {
  full: 'full — every run rebuilds the whole table',
  incremental: 'incremental — every run advances a watermark',
};

/**
 * One declared dataset.
 *
 * The two fields a non-engineer can always answer — what it is called and where
 * it lives — are open; everything a data engineer adds is behind one collapsed
 * "technical details" disclosure, so the form is never a wall that stops the
 * person who knows the answer to the first two.
 */
function DatasetRow({
  index,
  dataset,
  onChange,
  onRemove,
}: {
  index: number;
  dataset: DatasetDecl;
  onChange: (next: Partial<DatasetDecl>) => void;
  onRemove: () => void;
}) {
  const noKey = (dataset.naturalKey ?? '').toLowerCase() === 'none';
  // `loadSemantics` is a nested group and `onChange` takes a flat patch of the
  // dataset, so a field edit merges into whatever is already declared rather
  // than replacing the group with one key.
  const patchLoad = (next: Partial<LoadSemanticsDecl>) =>
    onChange({ loadSemantics: { ...(dataset.loadSemantics ?? {}), ...next } });
  return (
    <div className="proj-dataset">
      <div className="proj-dataset-head">
        <span className="eyebrow">data {index + 1}</span>
        <button type="button" className="btn btn--ghost btn--sm" onClick={onRemove}>
          Remove
        </button>
      </div>
      <input
        className="input"
        value={dataset.name ?? ''}
        onChange={(e) => onChange({ name: e.target.value })}
        aria-label={`Name of data ${index + 1}`}
        placeholder="the Looker report “Paid Social Q2”"
      />
      {/* The second box was a mono input with a placeholder and nothing else:
          a person who had already typed a name could not tell what it wanted.
          It gets the label the first box's own heading implies. */}
      <label className="proj-field">
        <span className="eyebrow">Where it lives (optional)</span>
        <input
          className="input mono"
          value={dataset.location ?? ''}
          onChange={(e) => onChange({ location: e.target.value })}
          aria-label={`Where data ${index + 1} lives`}
          placeholder="data/yellow_tripdata_2025-*.csv — a path or a table, never a password"
        />
      </label>
      <details className="proj-dataset-more">
        <summary>Technical details — an engineer can fill these in later</summary>
        {/* What this data IS, before what shape it is in. A recipe that wants
            two windows used to be handed the first two datasets in declaration
            order, so a 265-row zone lookup was offered as the window under test
            against a 24M-row trip feed and the form called it the project's
            answer. These two fields are what make that answerable. */}
        <label className="proj-field">
          <span className="eyebrow">what this is</span>
          <span className="proj-field-hint">
            A lookup or an export is never offered as the data under test — only as something joined
            onto it.
          </span>
          <select
            className="input"
            value={dataset.kind ?? ''}
            onChange={(e) =>
              onChange({ kind: (e.target.value || undefined) as DatasetDecl['kind'] })
            }
            aria-label={`What data ${index + 1} is`}
          >
            <option value="">—</option>
            <option value="fact">the data itself</option>
            <option value="lookup">a lookup table</option>
            <option value="export">an export</option>
          </select>
        </label>
        <label className="proj-field">
          <span className="eyebrow">which window it is</span>
          <span className="proj-field-hint">
            A shift audit compares two periods of the SAME data — so “both windows” is the usual
            answer, and the two periods below are what tell them apart.
          </span>
          <select
            className="input"
            value={dataset.window ?? ''}
            onChange={(e) =>
              onChange({ window: (e.target.value || undefined) as DatasetDecl['window'] })
            }
            aria-label={`Which window data ${index + 1} is`}
          >
            <option value="">—</option>
            <option value="baseline">the baseline window</option>
            <option value="current">the window under test</option>
            <option value="both">both windows, two periods of this data</option>
          </select>
        </label>
        {(dataset.window === 'baseline' || dataset.window === 'both') && (
          <label className="proj-field">
            <span className="eyebrow">baseline period</span>
            <span className="proj-field-hint">
              The stretch a comparison measures AGAINST — usually what a model was trained on, or
              what a report was last trusted over.
            </span>
            <input
              className="input"
              value={dataset.baselinePeriod ?? ''}
              onChange={(e) => onChange({ baselinePeriod: e.target.value })}
              placeholder="2025-01..03"
            />
          </label>
        )}
        {(dataset.window === 'current' || dataset.window === 'both') && (
          <label className="proj-field">
            <span className="eyebrow">current period</span>
            <span className="proj-field-hint">
              The stretch under test. Left blank, a run asks for it rather than the platform picking
              a period nobody declared.
            </span>
            <input
              className="input"
              value={dataset.currentPeriod ?? ''}
              onChange={(e) => onChange({ currentPeriod: e.target.value })}
              placeholder="2025-04..06"
            />
          </label>
        )}
        <label className="proj-field">
          <span className="eyebrow">format</span>
          <input
            className="input"
            value={dataset.format ?? ''}
            onChange={(e) => onChange({ format: e.target.value })}
            placeholder="csv"
          />
        </label>
        <label className="proj-field">
          <span className="eyebrow">grain</span>
          <span className="proj-field-hint">What ONE row stands for, in a sentence.</span>
          <input
            className="input"
            value={dataset.grain ?? ''}
            onChange={(e) => onChange({ grain: e.target.value })}
            placeholder="one row per completed trip"
          />
        </label>
        <label className="proj-field">
          <span className="eyebrow">key columns</span>
          <span className="proj-field-hint">
            The columns that together identify one row at that grain — comma-separated. Every row must
            have them and no two rows may share them.
          </span>
          <input
            className="input mono"
            value={(dataset.keyColumns ?? []).join(', ')}
            disabled={noKey}
            onChange={(e) => onChange({ keyColumns: splitList(e.target.value) })}
            placeholder="order_date, store_id"
          />
        </label>
        <label className="proj-checkline">
          <input
            type="checkbox"
            checked={noKey}
            onChange={(e) =>
              onChange(
                e.target.checked
                  ? { naturalKey: 'none', keyColumns: [] }
                  : { naturalKey: '', keyCandidate: [] },
              )
            }
          />
          <span>
            This data has no natural key — no set of columns guaranteed to be unique.
          </span>
        </label>
        {noKey && (
          <label className="proj-field">
            <span className="eyebrow">closest candidate columns</span>
            <span className="proj-field-hint">
              The columns that come closest. Runs under this project treat them as a candidate and
              report whether it actually holds, rather than assuming it does.
            </span>
            <input
              className="input mono"
              value={(dataset.keyCandidate ?? []).join(', ')}
              onChange={(e) => onChange({ keyCandidate: splitList(e.target.value) })}
              placeholder="VendorID, tpep_pickup_datetime, PULocationID"
            />
          </label>
        )}
        <label className="proj-field">
          <span className="eyebrow">arrival timestamp column</span>
          <span className="proj-field-hint">
            The column that says when a row landed. Freshness and late-arrival rules are computed
            against it; an audit with no arrival timestamp reports that as a gap in evidence.
          </span>
          <input
            className="input mono"
            value={dataset.arrivalTimestampColumn ?? ''}
            onChange={(e) => onChange({ arrivalTimestampColumn: e.target.value })}
            placeholder="ingested_at"
          />
        </label>
        <label className="proj-field">
          <span className="eyebrow">timezone</span>
          <span className="proj-field-hint">
            The timezone the timestamp columns are recorded in. An undeclared one is the commonest
            reason an audit cannot judge whether a row is late.
          </span>
          <input
            className="input"
            value={dataset.timezone ?? ''}
            onChange={(e) => onChange({ timezone: e.target.value })}
            placeholder="America/New_York"
          />
        </label>
        {/* How this data is LOADED — four facts the pipeline recipe used to
            derive for itself and report back as part of its plan. They are the
            owner's facts about the owner's data, and a plan that infers them is
            guessing at a contract somebody else already knows. */}
        <label className="proj-field">
          <span className="eyebrow">how it is loaded</span>
          <span className="proj-field-hint">
            A full load rebuilds the whole table every run. An incremental load advances a
            watermark — the high-water value the last run reached — and takes only what is past it.
          </span>
          <select
            className="input"
            value={dataset.loadSemantics?.mode ?? ''}
            onChange={(e) =>
              patchLoad({ mode: (e.target.value || undefined) as LoadSemanticsDecl['mode'] })
            }
            aria-label={`How data ${index + 1} is loaded`}
          >
            <option value="">—</option>
            {LOAD_MODES.map((m) => (
              <option key={m} value={m}>
                {LOAD_MODE_LABELS[m]}
              </option>
            ))}
          </select>
        </label>
        <label className="proj-field">
          <span className="eyebrow">watermark column</span>
          <span className="proj-field-hint">
            The column an incremental load advances on. An incremental load without one names no
            rows as new.
          </span>
          <input
            className="input mono"
            value={dataset.loadSemantics?.watermarkColumn ?? ''}
            onChange={(e) => patchLoad({ watermarkColumn: e.target.value })}
            placeholder="updated_at"
          />
        </label>
        <label className="proj-field">
          <span className="eyebrow">late arrival window</span>
          <span className="proj-field-hint">How late a row may still arrive.</span>
          <input
            className="input"
            value={dataset.loadSemantics?.lateArrivalWindow ?? ''}
            onChange={(e) => patchLoad({ lateArrivalWindow: e.target.value })}
            placeholder="up to 30 days"
          />
        </label>
        <label className="proj-field">
          <span className="eyebrow">restatement rule</span>
          <span className="proj-field-hint">
            What a late row does to the period it belongs to — restate that period, or count on the
            day it landed. The two give different numbers for the same month, and only one of them
            is yours.
          </span>
          <textarea
            className="input"
            rows={2}
            value={dataset.loadSemantics?.restatementRule ?? ''}
            onChange={(e) => patchLoad({ restatementRule: e.target.value })}
            placeholder="a late refund restates the original order_date rather than counting on the day it landed"
          />
        </label>
        <label className="proj-field">
          <span className="eyebrow">sensitive columns</span>
          <span className="proj-field-hint">Comma-separated. Names only — never a value.</span>
          <input
            className="input mono"
            value={(dataset.piiColumns ?? []).join(', ')}
            onChange={(e) => onChange({ piiColumns: splitList(e.target.value) })}
            placeholder="customer_email, phone"
          />
        </label>
        <label className="proj-field">
          <span className="eyebrow">safe use</span>
          <span className="proj-field-hint">
            What these numbers must NOT be used for, and how a sensitive column may appear in a
            report. Every recipe run under this project inherits it.
          </span>
          <textarea
            className="input"
            rows={2}
            value={dataset.safeUse ?? ''}
            onChange={(e) => onChange({ safeUse: e.target.value })}
            placeholder="never show customer names; aggregate reporting only, never per-person decisions"
          />
        </label>
        <label className="proj-field">
          <span className="eyebrow">expected row count</span>
          <span className="proj-field-hint">
            What you believe it holds. It stays a belief until a run measures it.
          </span>
          <input
            className="input mono"
            inputMode="numeric"
            value={dataset.expectedRowCount == null ? '' : String(dataset.expectedRowCount)}
            onChange={(e) => {
              const n = Number(e.target.value.replace(/[^0-9]/g, ''));
              onChange({ expectedRowCount: e.target.value.trim() === '' ? undefined : n });
            }}
            placeholder="24083384"
          />
        </label>
        {dataset.measured && (
          <div className="proj-field">
            <span className="eyebrow">last measured</span>
            <span className="faint">
              {[
                dataset.measured.rowCount != null && `${dataset.measured.rowCount} rows`,
                dataset.measured.columnCount != null && `${dataset.measured.columnCount} columns`,
                dataset.measured.periodCovered,
                dataset.measured.measuredByRun && `by ${dataset.measured.measuredByRun}`,
              ]
                .filter(Boolean)
                .join(' · ')}
            </span>
          </div>
        )}
      </details>
    </div>
  );
}

/** "a, b , c" → ["a","b","c"]. Empty members are dropped, order is kept. */
function splitList(raw: string): string[] {
  return raw
    .split(/[,;\n]/)
    .map((v) => v.trim())
    .filter((v) => v !== '');
}

/** Drop blank fields so an untouched group is stored as `{}` rather than as noise. */
function stripEmpty<T extends object>(obj: T): T {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(obj)) {
    if (typeof v === 'string' ? v.trim() !== '' : v != null) out[k] = v;
  }
  return out as T;
}

/** A dataset with its blanks, empty lists and undeclared load removed. */
function cleanDataset(ds: DatasetDecl): DatasetDecl {
  const out = stripEmpty(ds) as Record<string, unknown>;
  for (const k of ['keyColumns', 'keyCandidate', 'piiColumns']) {
    const v = out[k];
    if (Array.isArray(v) && v.length === 0) delete out[k];
  }
  // A load nobody described is not a load semantics of `{}` — it is no key at
  // all. `stripEmpty` keeps any non-null object, so an opened-and-abandoned
  // sub-editor would otherwise store an empty declaration, and an empty
  // declaration reads downstream as an answer.
  const load = stripEmpty((out.loadSemantics ?? {}) as LoadSemanticsDecl);
  if (Object.keys(load).length === 0) delete out.loadSemantics;
  else out.loadSemantics = load;
  return out as DatasetDecl;
}

/** "last activity" as a plain absolute date — no invented precision. */
function lastActivityLabel(p: ProjectCount): string {
  if (!p.lastActivity) return 'no activity yet';
  const d = new Date(p.lastActivity);
  if (Number.isNaN(d.getTime())) return p.lastActivity;
  return `last activity ${d.toLocaleDateString('en-GB')}`;
}
