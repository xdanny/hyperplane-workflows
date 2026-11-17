/**
 * One saved workflow's own history — `/workflows/:workflowId` (F-C, R-04).
 *
 * Nothing in the audited build listed a workflow's runs: the Runs list was
 * chronological across everything, and the project page stopped at "2 runs ·
 * last passed" (P-11, M-06). So "how did this month's commentary differ from
 * last month's?" — the question the whole product exists to answer — had no
 * screen, and Sofia answered it by reading two raw Markdown files side by side
 * for five minutes (S-03).
 *
 * This page is that screen: what the workflow asks for, what its runs cost and
 * decided, and a comparison of any two of them. It starts no runs and changes
 * nothing; every button either navigates or opens a document.
 */

import { useEffect, useMemo, useState } from 'react';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, readSignOff } from '../api';
import type { AttachableRun, WorkflowRunRow, WorkflowRunsResult } from '../api';
import { StateChip } from './../components/StateChip';
import { RunDocumentReader } from '../components/RunDocumentReader';
import type { RunDocumentRef } from '../components/RunDocumentReader';
import { StatusLegend } from '../components/StatusLegend';
import {
  acceptanceLine, failingRowText, formatDate, formatDateTime, formatDuration,
  formatTokens, quotedNote, runChipClass, runChipLabel, slotsSummary,
} from '../runLexicon';
import { SIGN_OFF_CONSEQUENCE, SIGN_OFF_LABEL } from '../signOff';
import { comparisonLabel, compareOutputFiles, deliveredDocument, outputFilesSummary } from '../outputFiles';
import { differenceSummary, textDifference } from '../textDiff';
import { nodeKind } from '../workflowNodePresentation';
import './WorkflowHistory.css';

/** The URL a run's produced file is read at, in the in-app reader. */
function runFileURL(runId: string, path: string): string {
  return `/file/${encodeURIComponent(runId)}/nodes/delivery/OUTPUTS/${path.split('/').map(encodeURIComponent).join('/')}`;
}

/** The deliverable of a run, with the URL it is read at. `deliveredDocument`
 *  owns the rule that a review is not the result (S-16). */
function finalDocumentOf(run: WorkflowRunRow): { title: string; path: string; url: string } | null {
  const last = deliveredDocument(run.documents);
  if (!last) return null;
  return { title: last.title || last.path, path: last.path, url: runFileURL(run.runId, last.path) };
}

function ChecksCell({ run }: { run: WorkflowRunRow }) {
  if (!run.checksDeclared) return <span className="faint">no extra checks</span>;
  return <span>{run.checksPassed ?? 0} of {run.checksDeclared} passed</span>;
}

/**
 * "Output files" — every file either run recorded, compared by SHA-256 (M-18).
 *
 * The comparison above diffs the delivered document; a run that rewrote
 * `build/totals.csv` looked identical to one that did not, because nothing put
 * the two recorded hashes beside each other. This says same / changed / only in
 * one run, and claims nothing about what changed inside a file.
 */
function OutputFilesCompare({ older, newer, onOpenDocument }: {
  older: WorkflowRunRow;
  newer: WorkflowRunRow;
  onOpenDocument: (doc: RunDocumentRef) => void;
}) {
  const compared = useMemo(
    () => compareOutputFiles(older.files, newer.files),
    [older.files, newer.files],
  );
  if (compared.length === 0) {
    return (
      <>
        <h3>Output files</h3>
        <p className="faint">
          Neither run recorded a produced file, so there is nothing to compare. Older runs recorded
          only their delivered document.
        </p>
      </>
    );
  }
  return (
    <>
      <h3>Output files</h3>
      <p className="wfh-diff-summary">{outputFilesSummary(compared, older.runNumber, newer.runNumber)}</p>
      <div className="wfh-table-wrap">
        <table className="wfh-table wfh-files" aria-label="Output files compared by checksum">
          <thead>
            <tr>
              <th scope="col">File</th>
              <th scope="col">Step</th>
              <th scope="col">Run #{older.runNumber}</th>
              <th scope="col">Run #{newer.runNumber}</th>
              <th scope="col">Comparison</th>
            </tr>
          </thead>
          <tbody>
            {compared.map((file) => (
              <tr key={file.path} className={`wfh-file wfh-file--${file.status}`}>
                <th scope="row" className="mono">{file.path}</th>
                <td>{file.stepTitle ?? <span className="faint">—</span>}</td>
                {/* A 12-character checksum is not something a person can act
                    on; the file behind it is. Each side opens that run's copy
                    in the reader (S-16). */}
                <td className="mono">{file.older
                  ? <button className="btn btn--ghost btn--sm" aria-label={`Read ${file.path} from run #${older.runNumber}`}
                      onClick={() => onOpenDocument({ url: runFileURL(older.runId, file.path), title: `${file.path} · run #${older.runNumber}`, sha256: file.older?.sha256 })}>
                      {file.older.sha256 ? file.older.sha256.slice(0, 12) : 'open'}
                    </button>
                  : <span className="faint">not recorded</span>}</td>
                <td className="mono">{file.newer
                  ? <button className="btn btn--ghost btn--sm" aria-label={`Read ${file.path} from run #${newer.runNumber}`}
                      onClick={() => onOpenDocument({ url: runFileURL(newer.runId, file.path), title: `${file.path} · run #${newer.runNumber}`, sha256: file.newer?.sha256 })}>
                      {file.newer.sha256 ? file.newer.sha256.slice(0, 12) : 'open'}
                    </button>
                  : <span className="faint">not recorded</span>}</td>
                <td>{comparisonLabel(file.status, older.runNumber, newer.runNumber)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="faint">
        Files are compared by their recorded SHA-256 only. “Changed” says the bytes differ, not what
        differs inside them; “only in run #N” says the other run recorded no file at that path,
        which is not the same as the file having been deleted.
      </p>
    </>
  );
}

/**
 * "Attach earlier runs" — claiming runs of this same definition that were
 * started before the workflow was saved (P-16).
 *
 * Priya saved a workflow after running it twice, and its page then said "Runs ·
 * 1 in total" while both finished briefs sat elsewhere under a truncated goal
 * string. Attaching rewrites only what a run is FILED under — the frozen
 * request it executed is untouched — so nothing about the run's history changes
 * except which workflow's page lists it.
 */
function AttachRuns({ workflowId, workflowName, runs }: { workflowId: string; workflowName: string; runs: AttachableRun[] }) {
  const queryClient = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const [chosen, setChosen] = useState<string[]>(() => runs.map((run) => run.runId));

  const attach = useMutation({
    mutationFn: () => api.attachRuns(workflowId, chosen),
    onSuccess: () => {
      setConfirming(false);
      void queryClient.invalidateQueries({ queryKey: ['workflowRuns', workflowId] });
      void queryClient.invalidateQueries({ queryKey: ['runs'] });
      void queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
  });

  if (runs.length === 0 && !attach.isSuccess) return null;
  // Everything on offer has been attached: the list is empty and the section is
  // now just the receipt for what happened.
  const done = runs.length === 0 && attach.isSuccess;

  const toggle = (runId: string) =>
    setChosen((current) =>
      current.includes(runId) ? current.filter((id) => id !== runId) : [...current, runId]);

  return (
    <section className="panel wfh-section wfh-attach" aria-label="Attach earlier runs">
      <h2>{done ? 'Earlier runs attached' : `Attach ${runs.length} earlier run${runs.length === 1 ? '' : 's'}`}</h2>
      {!done && <>
      <p>
        {runs.length === 1 ? 'This run' : 'These runs'} ran the same steps as this workflow in this
        project but started before it was saved, so {runs.length === 1 ? 'it is' : 'they are'} not
        listed above. Attaching files {runs.length === 1 ? 'it' : 'them'} under this workflow. It
        starts nothing, changes no result, and leaves what each run actually executed untouched.
      </p>
      <ul className="wfh-attach-list">
        {runs.map((run) => (
          <li key={run.runId}>
            <label>
              <input
                type="checkbox"
                checked={chosen.includes(run.runId)}
                disabled={attach.isPending}
                onChange={() => toggle(run.runId)}
                aria-label={`Attach the run started ${formatDateTime(run.startedAt)}`}
              />
              <span>{formatDateTime(run.startedAt)}</span>
              <StateChip state={run.state} label={runChipLabel(run)} className={runChipClass(run)} />
              <span className="faint">{run.reason}</span>
            </label>
          </li>
        ))}
      </ul>
      <div className="sdk-run-actions">
        {confirming ? (
          <>
            <button className="btn btn--primary" disabled={attach.isPending || chosen.length === 0}
              onClick={() => attach.mutate()}>
              {attach.isPending ? 'Attaching…' : `Attach ${chosen.length} run${chosen.length === 1 ? '' : 's'}`}
            </button>
            <button className="btn btn--ghost" disabled={attach.isPending} onClick={() => setConfirming(false)}>Cancel</button>
          </>
        ) : (
          <button className="btn" disabled={chosen.length === 0} onClick={() => setConfirming(true)}>
            Attach {chosen.length === runs.length ? 'these' : `${chosen.length}`} run{chosen.length === 1 ? '' : 's'}
          </button>
        )}
      </div>
      </>}
      {confirming && !attach.isPending && (
        <p role="status">
          {chosen.length === 1 ? 'This run' : `These ${chosen.length} runs`} will be listed under
          “{workflowName}” and counted in its history. Confirm to attach.
        </p>
      )}
      {attach.error && <p role="alert">Could not attach. {attach.error.message}</p>}
      {attach.isSuccess && (
        <div role="status">
          <p>
            {attach.data.attached.length === 0
              ? 'No run was attached.'
              : `Attached ${attach.data.attached.length} run${attach.data.attached.length === 1 ? '' : 's'} — the table above now lists ${attach.data.attached.length === 1 ? 'it' : 'them'}.`}
          </p>
          {/* A refused run says why, in the server's own words. Silently
              dropping one would leave the count unexplained. */}
          {attach.data.skipped.length > 0 && (
            <ul className="wfh-attach-skipped">
              {attach.data.skipped.map((skip) => (
                <li key={skip.runId}><span className="mono">…{skip.runId.slice(-8)}</span> — {skip.reason}</li>
              ))}
            </ul>
          )}
        </div>
      )}
    </section>
  );
}

/** Side-by-side comparison of two runs, with a plain text difference. */
function CompareRuns({ a, b, onOpenDocument }: {
  a: WorkflowRunRow;
  b: WorkflowRunRow;
  onOpenDocument: (doc: RunDocumentRef) => void;
}) {
  // Older on the left, newer on the right, whichever order they were picked in.
  const [older, newer] = new Date(a.startedAt) <= new Date(b.startedAt) ? [a, b] : [b, a];
  const docs = [older, newer].map(finalDocumentOf);
  const texts = useQueries({
    queries: docs.map((doc) => ({
      queryKey: ['document', doc?.url ?? 'none'],
      queryFn: async () => {
        const response = await fetch(doc!.url);
        if (!response.ok) throw new Error(String(response.status));
        return response.text();
      },
      enabled: Boolean(doc?.url),
      staleTime: 60_000,
    })),
  });
  const olderText = typeof texts[0]?.data === 'string' ? texts[0].data : null;
  const newerText = typeof texts[1]?.data === 'string' ? texts[1].data : null;
  const diff = useMemo(
    () => (olderText !== null && newerText !== null ? textDifference(olderText, newerText) : null),
    [olderText, newerText],
  );

  const row = (label: string, render: (run: WorkflowRunRow) => React.ReactNode) => (
    <tr>
      <th scope="row">{label}</th>
      <td>{render(older)}</td>
      <td>{render(newer)}</td>
    </tr>
  );

  return (
    <section className="panel wfh-compare" aria-label="Compare two runs">
      <h2>Comparing run #{older.runNumber} with run #{newer.runNumber}</h2>
      <div className="wfh-table-wrap">
        <table className="wfh-table">
          <thead>
            <tr>
              <th scope="col">&nbsp;</th>
              <th scope="col">Run #{older.runNumber} <span className="faint">(earlier)</span></th>
              <th scope="col">Run #{newer.runNumber} <span className="faint">(later)</span></th>
            </tr>
          </thead>
          <tbody>
            {row('Started', (run) => formatDateTime(run.startedAt))}
            {row('State', (run) => <StateChip state={run.state} label={runChipLabel(run)} className={runChipClass(run)} />)}
            {row('Duration', (run) => formatDuration(run.durationMs) ?? '—')}
            {row('Tokens', (run) => {
              const inTokens = formatTokens(run.usage?.inputTokens);
              const outTokens = formatTokens(run.usage?.outputTokens);
              return inTokens && outTokens ? `${inTokens} in / ${outTokens} out` : '—';
            })}
            {row('Inputs', (run) => slotsSummary(run.slots, 6, run.slotLabels) || <span className="faint">none</span>)}
            {row('Checks', (run) => <ChecksCell run={run} />)}
            {row('Failing step', (run) => failingRowText(run.failingStep) ?? <span className="faint">none</span>)}
            {row('Decision', (run) => run.outcome
              ? <>{acceptanceLine(run.outcome)}{run.outcome.note ? <> — {quotedNote(run.outcome.note)}</> : null}</>
              : <span className="faint">no decision recorded</span>)}
            {row('Result document', (run) => {
              const doc = finalDocumentOf(run);
              return doc
                ? <button className="btn btn--ghost btn--sm" onClick={() => onOpenDocument({ url: doc.url, title: `${doc.title} · run #${run.runNumber}` })}>{doc.title}</button>
                : <span className="faint">none recorded</span>;
            })}
            <tr>
              <th scope="row">Open</th>
              <td><Link to={`/runs/${encodeURIComponent(older.runId)}`}>Run #{older.runNumber}</Link></td>
              <td><Link to={`/runs/${encodeURIComponent(newer.runId)}`}>Run #{newer.runNumber}</Link></td>
            </tr>
          </tbody>
        </table>
      </div>

      <OutputFilesCompare older={older} newer={newer} onOpenDocument={onOpenDocument} />

      <h3>Text difference of the delivered document</h3>
      {!docs[0] || !docs[1] ? (
        <p className="faint">One of these runs recorded no final document, so there is nothing to compare.</p>
      ) : diff === null ? (
        <p role="status">Reading both documents…</p>
      ) : (
        <>
          <p className="wfh-diff-summary">{differenceSummary(diff)}</p>
          <pre className="wfh-diff" aria-label="Text difference between the two documents">
            {diff.spans.map((span, index) => (
              <span key={index} className={`wfh-diff-${span.op}`}>{span.text}</span>
            ))}
          </pre>
          <p className="faint">
            Struck-through text is only in run #{older.runNumber}; underlined text is only in run #{newer.runNumber}.
            This compares characters, not meaning: two runs that say the same thing in different words show as
            changes, and a changed number shows as one small change.
          </p>
        </>
      )}
    </section>
  );
}

export function WorkflowHistory() {
  const { workflowId = '' } = useParams();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const [reading, setReading] = useState<RunDocumentRef | null>(null);

  const query = useQuery<WorkflowRunsResult>({
    queryKey: ['workflowRuns', workflowId],
    queryFn: () => api.workflowRuns(workflowId),
    enabled: workflowId !== '',
    // A history with a live run in it should move like the runs list does.
    refetchInterval: (q) => (q.state.data?.runs.some((r) => r.state === 'running') ? 5000 : false),
  });

  const runs = query.data?.runs ?? [];
  const workflow = query.data?.workflow;
  const attachable = query.data?.attachable ?? [];

  // Which two runs are being compared lives in the URL, so "Compare latest two"
  // from the runs list is a link somebody can send to a colleague.
  const compareParam = searchParams.get('compare') ?? '';
  const [selected, setSelected] = useState<string[]>([]);
  useEffect(() => {
    const ids = compareParam.split(',').map((id) => id.trim()).filter(Boolean);
    if (ids.length > 0) setSelected(ids.slice(0, 2));
  }, [compareParam]);

  const toggle = (runId: string) => {
    setSelected((current) => {
      const next = current.includes(runId)
        ? current.filter((id) => id !== runId)
        // Picking a third replaces the older of the two: a comparison is always
        // of two, and refusing the click would just look broken.
        : [...current, runId].slice(-2);
      const params = new URLSearchParams(searchParams);
      if (next.length === 2) params.set('compare', next.join(','));
      else params.delete('compare');
      setSearchParams(params, { replace: true });
      return next;
    });
  };

  const pair = selected.length === 2
    ? selected.map((id) => runs.find((run) => run.runId === id)).filter((run): run is WorkflowRunRow => Boolean(run))
    : [];

  const latestSlots = runs.find((run) => run.slots && Object.keys(run.slots).length > 0)?.slots ?? undefined;
  /** Both "run again" buttons land in the editor with this workflow loaded;
   *  only the prefilled values differ. The editor owns the run itself, so
   *  nothing is launched from here. */
  const openEditor = (slots?: Record<string, string>) =>
    navigate('/workflows', { state: { setup: workflow, slots } });

  return (
    <div className="view wfh">
      <div className="view-head">
        <span className="eyebrow">Workflow</span>
        <h1 className="h1">{workflow?.name || 'Workflow history'}</h1>
      </div>

      {query.isPending && <p role="status">Loading this workflow’s runs…</p>}
      {query.error && (
        <p role="alert">
          Could not load this workflow. {query.error.message}{' '}
          <button className="btn" onClick={() => void query.refetch()}>Retry</button>
        </p>
      )}

      {workflow && (
        <>
          <section className="panel wfh-section" aria-label="What this workflow does">
            {workflow.instruction && (
              <p className="wfh-description">{workflow.instruction.split(/(?<=[.!?])\s/)[0].replace(/\{slot:([a-zA-Z0-9_-]+)\}/g, (_, id) => `[${workflow.inputs?.find((input) => input.id === id)?.label ?? id}]`)}</p>
            )}
            <div className="sdk-run-actions">
              <button className="btn btn--primary" onClick={() => openEditor(latestSlots as Record<string, string> | undefined)}>
                Run again with the same inputs
              </button>
              <button className="btn" onClick={() => openEditor(undefined)}>Run with new inputs</button>
              <Link className="btn btn--ghost" to="/workflows">Edit this workflow</Link>
            </div>
            <p className="faint">
              Both buttons open the editor with this workflow loaded. Nothing starts until you press Run there.
            </p>
          </section>

          <section className="panel wfh-section" aria-label="Inputs this workflow asks for">
            <h2>Inputs this workflow asks for each run</h2>
            {(workflow.inputs?.length ?? 0) === 0 ? (
              <p className="faint">
                This workflow declares no run-time inputs — every run uses the same instructions.
              </p>
            ) : (
              <dl className="wfh-inputs">
                {workflow.inputs!.map((slot) => (
                  <div key={slot.id} className="wfh-input">
                    <dt>{slot.label}{slot.required ? <span className="wfh-required"> required</span> : null}</dt>
                    <dd>
                      {slot.hint && <span>{slot.hint} </span>}
                      {slot.example && <span className="faint">For example: {slot.example}</span>}
                    </dd>
                  </div>
                ))}
              </dl>
            )}
          </section>

          <section className="panel wfh-section" aria-label="Steps">
            <h2>Steps</h2>
            <ol className="wfh-steps">
              {(workflow.nodes ?? []).map((node) => (
                <li key={node.id}>
                  <strong>{node.title}</strong>
                  <span className="faint"> · {nodeKind(node).label}</span>
                </li>
              ))}
            </ol>
            <p className="faint">Read-only here. Open the editor to change a step.</p>
          </section>

          {/* What this workflow asks of a person when a run finishes. It is a
              property of the workflow, so it is read where the workflow is
              read — and the editor is where it is changed (M-11). */}
          <section className="panel wfh-section" aria-label="Sign-off setting">
            <h2>When a run finishes</h2>
            <p>{SIGN_OFF_LABEL[readSignOff(workflow.signOff)]}</p>
            <p className="faint">{SIGN_OFF_CONSEQUENCE[readSignOff(workflow.signOff)]}</p>
            {/* No "whatever this says now" caveat any more: each run row reads
                the policy frozen on that run, so the table cannot be wrong
                about a finished run and does not need to warn that it might
                be. Changing this setting changes what the NEXT run asks. */}
          </section>

          <AttachRuns workflowId={workflowId} workflowName={workflow.name} runs={attachable} />
        </>
      )}

      <section className="panel wfh-section" aria-label="Runs of this workflow">
        <div className="section-head">
          <h2>Runs</h2>
          <span className="faint">{query.data?.total ?? runs.length} in total</span>
        </div>
        {query.isSuccess && runs.length === 0 ? (
          <p className="faint">This workflow has not been run yet.</p>
        ) : (
          <div className="wfh-table-wrap">
            <table className="wfh-table wfh-runs">
              <thead>
                <tr>
                  <th scope="col">Compare</th>
                  <th scope="col">Run</th>
                  <th scope="col">Started</th>
                  <th scope="col">Duration</th>
                  <th scope="col">Inputs</th>
                  <th scope="col">State</th>
                  <th scope="col">Checks</th>
                  <th scope="col">Decision</th>
                  <th scope="col">Open</th>
                </tr>
              </thead>
              <tbody>
                {runs.map((run) => (
                  <tr key={run.runId} className={selected.includes(run.runId) ? 'wfh-row--selected' : undefined}>
                    <td>
                      <input
                        type="checkbox"
                        checked={selected.includes(run.runId)}
                        onChange={() => toggle(run.runId)}
                        aria-label={`Compare run #${run.runNumber}`}
                      />
                    </td>
                    <td>#{run.runNumber}</td>
                    <td>{formatDateTime(run.startedAt)}</td>
                    <td>{formatDuration(run.durationMs) ?? <span className="faint">running</span>}</td>
                    <td className="wfh-inputs-cell">{slotsSummary(run.slots, 3, run.slotLabels) || <span className="faint">none</span>}</td>
                    <td>
                      <StateChip state={run.state} label={runChipLabel(run)} className={runChipClass(run)} />
                      {run.failingStep && <div className="wfh-failing">{failingRowText(run.failingStep)}</div>}
                    </td>
                    <td><ChecksCell run={run} /></td>
                    <td>
                      {run.outcome ? (
                        <>
                          <div>{acceptanceLine(run.outcome)}</div>
                          {run.outcome.note && <div className="faint">{quotedNote(run.outcome.note)}</div>}
                        </>
                      ) : run.state === 'passed' ? (
                        // A run launched under "sign-off optional" is not
                        // waiting on anybody: saying it is would recreate the
                        // queue the policy exists to empty (M-11). The policy
                        // is the one FROZEN on this run at launch, never the
                        // workflow's current setting — a row that borrowed the
                        // current policy could describe a finished run by a
                        // rule it was never launched under, which is why the
                        // table needed a caveat sentence to be honest at all.
                        readSignOff(run.signOff) === 'optional'
                          ? <span className="faint">no sign-off asked</span>
                          : <span className="faint">awaiting your sign-off</span>
                      ) : (
                        <span className="faint">—</span>
                      )}
                    </td>
                    <td><Link to={`/runs/${encodeURIComponent(run.runId)}`}>Open</Link></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {runs.length >= 2 && pair.length < 2 && (
          <p className="faint">
            Tick two runs to compare them side by side.
            {' '}
            <button className="btn btn--ghost btn--sm" onClick={() => {
              const ids = [runs[0].runId, runs[1].runId];
              setSelected(ids);
              const params = new URLSearchParams(searchParams);
              params.set('compare', ids.join(','));
              setSearchParams(params, { replace: true });
            }}>Compare latest two</button>
          </p>
        )}
      </section>

      {pair.length === 2 && <CompareRuns a={pair[0]} b={pair[1]} onOpenDocument={setReading} />}

      {runs.some((run) => run.outcome) && (
        <p className="faint wfh-decided">
          Decisions are kept permanently; to change one, delete its record and decide again. The most recent was {formatDate(runs.find((r) => r.outcome)!.outcome!.decidedAt)}.
        </p>
      )}

      <StatusLegend />
      {reading && <RunDocumentReader doc={reading} onClose={() => setReading(null)} />}
    </div>
  );
}
