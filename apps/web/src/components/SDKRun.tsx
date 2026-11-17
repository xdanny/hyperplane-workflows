import { useEffect, useState } from 'react';
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { api } from '../api';
import type { JourneyOutcome, OutcomeVerdict, Run, RunJourneyRef, RunOutcome, SDKWorkflowNode } from '../api';
import { SDKContinuation } from './SDKContinuation';
import { WorkflowGraph } from './WorkflowGraph';
import { ExtractSkillAction } from './ExtractSkillAction';
import { AskReviewer } from './AskReviewer';
import { StateChip } from './StateChip';
import { StatusLegend } from './StatusLegend';
import { RunResultCard } from './RunResultCard';
import { RunDocumentReader } from './RunDocumentReader';
import type { RunDocumentRef } from './RunDocumentReader';
import { renderMarkdown } from './DocumentViewer';
import { useNodeLogReader } from './SessionLogReader';
import './SDKRun.css';
import { workflowFileLabel } from '../workflowFiles';
import type { WorkflowNodeCheckpoint } from '../workflowNodePresentation';
import { recordedNodeFiles, nodeFileURL, nodeCompletionLabel } from '../workflowNodePresentation';
import {
  acceptanceLine, acceptanceSuffix, correctionsUsed, failedHeadline, formatAgo,
  headerMeta, quotedNote, runChipClass, runChipLabel, runHeading, slotsSummary, stepSummaryLine,
} from '../runLexicon';
import { AUTOMATIC_ACCEPTANCE_NOTE, isAutomatic, readSignOff, signOffDecidable, SIGN_OFF_SHORT, signOffPending as signOffIsPending } from '../signOff';
import { finalDocuments, nextAction, resultCardVisible, reviewSummary } from '../runResult';
import { NOTE_PLACEHOLDER } from '../delivery';

interface Checkpoint {
  verification?: 'not_requested' | 'passed' | 'failed';
  approval?: { id: string; title: string; prompt: string; documents: { name: string; url: string; sha256: string }[] };
  nodes?: WorkflowNodeCheckpoint[];
  phase?: string;
  threadId?: string;
  attempt?: number;
  attempts?: number;
  text?: string;
  error?: string;
  findings?: string[];
  usage?: { input_tokens: number; cached_input_tokens: number; output_tokens: number };
}

const phases: Record<string, string> = {
  starting: 'Starting the session', running: 'Working on the task',
  verifying: 'Running acceptance checks', correction_pending: 'Preparing one correction',
  passed: 'Acceptance checks passed', failed: 'Task failed', interrupted: 'Task stopped',
};

/** Elapsed time of a running run, so a live page can show a duration too. */
function liveDuration(run: Run): number | null {
  if (typeof run.durationMs === 'number') return run.durationMs;
  if (run.endedAt) return new Date(run.endedAt).getTime() - new Date(run.startedAt).getTime();
  if (run.state === 'running') return Date.now() - new Date(run.startedAt).getTime();
  return null;
}

// The graph projects the saved SDK request and its checkpoints, not a second scheduler.
export function SDKRun({ run, detailOutcome, journey, onBack, onContinuedRunId, accepted = false }: {
  run: Run;
  /** The verdict recorded on THIS run, when someone annotated it. */
  detailOutcome?: RunOutcome | null;
  /** The delivery this run is a chapter of — where the sign-off actually lives. */
  journey?: RunJourneyRef | null;
  onBack: () => void;
  onContinuedRunId: (id: string) => void;
  accepted?: boolean;
}) {
  const queryClient = useQueryClient();
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [continuationBusy, setContinuationBusy] = useState(false);
  const [reading, setReading] = useState<RunDocumentRef | null>(null);
  const [signOffNote, setSignOffNote] = useState('');
  const { open, reader } = useNodeLogReader();
  const checkpoint = (run.doc?.sdk ?? {}) as Checkpoint;
  const request = run.options?.request as { workflow?: 'node-workflow' | 'checked-task' | 'app-ideator'; workspace?: string; maxTokens?: number; check?: string[]; instruction?: string; domain?: string; nodes?: SDKWorkflowNode[] } | undefined;
  const ideator = request?.workflow === 'app-ideator';
  const hasTaskCheck = ideator || Boolean(request?.check?.length);
  const taskPhase = (phase: string) => phase === 'passed' && (checkpoint.verification === 'not_requested' || !hasTaskCheck) ? 'Files produced' : phase === 'verifying' && !hasTaskCheck ? 'Checking output files' : phases[phase] ?? 'Session in progress';
  const running = run.state === 'running';
  const nodeWorkflow = request?.workflow === 'node-workflow' || Boolean(request?.nodes);
  const phase = running ? checkpoint.phase ?? 'starting' : run.state;
  const nodePhases: Record<string, string> = { approval_pending: running ? 'Waiting for your decision' : 'Stopped', rejected: 'Rejected by a person', pending: running ? 'Waiting' : 'Not run', running: running ? 'Writing document' : 'Stopped', verifying: running ? 'Checking document' : 'Stopped', saving: running ? 'Saving document' : 'Stopped', correction_pending: running ? 'Correcting document' : 'Stopped', passed: 'Document saved', failed: 'Failed', interrupted: 'Stopped' };
  const progress = request?.nodes?.map(node => {
    const saved = checkpoint.nodes?.find(n => n.id === node.id);
    const recorded = recordedNodeFiles(saved);
    const document = recorded.find(file => file.path === saved?.document?.path);
    return { ...node, phase: saved?.phase ?? 'pending', verification: saved?.verification,
      completion: nodeCompletionLabel(node, saved), document, files: recorded.filter(file => file !== document) };
  });

  // ── One vocabulary, resolved once (F-H) ───────────────────────────────────
  // The server resolves `failingStep` and `waitingForPerson`; while slice A's
  // fields are still absent this falls back to the checkpoint, so the page is
  // never blank about which step ended a run.
  const failingStep = run.failingStep ?? (() => {
    const bad = checkpoint.nodes?.find(node => node.phase === 'failed' || node.phase === 'rejected');
    const node = request?.nodes?.find(n => n.id === bad?.id);
    return bad && node ? { id: node.id, title: node.title, kind: node.kind } : null;
  })();
  const waitingForPerson = run.waitingForPerson ?? (running && checkpoint.phase === 'approval_pending');
  const chip = runChipLabel({ state: run.state, waitingForPerson, failingStep });
  const chipClass = runChipClass({ state: run.state, waitingForPerson, failingStep });
  const headline = run.state === 'failed' ? failedHeadline(failingStep, request?.nodes) : null;

  // The sign-off the Inbox is holding. A PASSED run whose delivery still wants a
  // verdict says so here rather than letting a second decision arrive
  // unexplained from the Inbox (P-07, S-01, M-11).
  const governing: JourneyOutcome | RunOutcome | null = journey?.outcome ?? detailOutcome ?? null;
  // The policy this run was LAUNCHED under, frozen in its own record: editing
  // the saved workflow afterwards never changes what this run asked for (M-11).
  // The server resolves it onto the run; the launch record is the fallback for
  // a row it has not projected.
  const signOffPolicy = readSignOff(
    run.signOff ?? (run.options?.projectWorkflow as { signOff?: unknown } | undefined)?.signOff,
  );
  // TWO questions, deliberately separate (S-14, D-16). `signOffPending` asks
  // whether the INBOX is holding this delivery — which the policy decides.
  // `decidable` asks whether a person may record a verdict at all, which the
  // policy has no business deciding: an "optional" run is never queued and was
  // therefore undecidable anywhere, on a page that told the reader to go and
  // decide it in the Inbox.
  const signOffPending = Boolean(journey) && signOffIsPending({
    state: run.state, decided: Boolean(governing), policy: signOffPolicy,
  });
  const decidable = Boolean(journey) && signOffDecidable({
    state: run.state, decided: Boolean(governing),
  });
  const automatic = isAutomatic(governing);
  // An automatic acceptance has to name the checks it rests on. The record
  // carries the count in its note rather than as fields, so the delivery's own
  // tally — the same numbers every other surface quotes — stands in for them.
  const acceptance = governing && automatic
    ? { ...governing, checksPassed: governing.checksPassed ?? journey?.proven, checksDeclared: governing.checksDeclared ?? journey?.criteria }
    : governing;
  const chipSuffix = acceptanceSuffix(acceptance, { signOffPending });

  const corrections = correctionsUsed(checkpoint.nodes);
  const stepLine = nodeWorkflow && request?.nodes
    ? stepSummaryLine(request.nodes.length, corrections, Boolean(run.options?.autoRepair))
    : `${checkpoint.attempts ?? (checkpoint.attempt == null ? 0 : checkpoint.attempt + 1)} attempt${(checkpoint.attempts ?? 1) === 1 ? '' : 's'} · ${run.options?.autoRepair ? 'One correction allowed' : 'No automatic correction'}`;

  const cancel = useMutation({
    mutationFn: () => api.cancelRun(run.runId),
    onSuccess: () => { void queryClient.invalidateQueries({ queryKey: ['run', run.runId] }); void queryClient.invalidateQueries({ queryKey: ['runs'] }); },
  });
  const artifactsQuery = useQuery({
    queryKey: ['run', run.runId, 'artifacts'], queryFn: () => api.runArtifacts(run.runId),
    refetchInterval: running ? 3000 : false,
  });
  const artifacts = artifactsQuery.data ?? [];
  // The check catalog, for the inspector's "Checks and decision". A receipt
  // records an identifier and a verdict, so what that verdict PROVES — and
  // what it does not — has to come from the catalog as it stands (D-06). The
  // inspector says as much beside the two lists; a catalog that cannot be read
  // simply leaves them out.
  const checkCatalog = useQuery({
    queryKey: ['workflow-checks'], queryFn: api.workflowChecks, retry: false,
    enabled: Boolean(request?.nodes?.some(node => node.acceptance)),
  });
  const refresh = () => {
    void queryClient.invalidateQueries({queryKey:['run',run.runId]});
    void queryClient.invalidateQueries({queryKey:['runs']});
    setRefreshVersion(value => value + 1);
  };
  const decide = useMutation({
    mutationFn: (approved: boolean) => api.approveDecision(checkpoint.approval!.id, approved),
    // The run does not become un-blocked the instant the button is pressed, but
    // the PERSON has decided, and a page that keeps offering both buttons for a
    // minute reads as a lost click (P-04). Say what was recorded, then refetch.
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['run', run.runId] });
      void queryClient.invalidateQueries({ queryKey: ['runs'] });
      void queryClient.invalidateQueries({ queryKey: ['inbox'] });
    },
  });
  const resetDecision = decide.reset;
  useEffect(() => resetDecision(), [checkpoint.approval?.id, resetDecision]);

  const signOff = useMutation({
    mutationFn: (verdict: OutcomeVerdict) => api.recordJourneyOutcome(journey!.key, run.runId, verdict, signOffNote),
    onSuccess: () => {
      setSignOffNote('');
      void queryClient.invalidateQueries({ queryKey: ['run', run.runId] });
      void queryClient.invalidateQueries({ queryKey: ['inbox'] });
    },
  });

  // ── The result, first (F-G) ───────────────────────────────────────────────
  const resultDocs = finalDocuments(request?.nodes, checkpoint.nodes);
  const review = reviewSummary(request?.nodes, checkpoint.nodes);
  const action = nextAction({ state: run.state, waitingForPerson, signOffPending });
  // ── Said once (S-12) ──────────────────────────────────────────────────────
  // The reviewer's findings appeared in the result card AND under "Check
  // findings"; the delivered document appeared in the card AND under "Latest
  // step result". Neither duplicate is suppressed blindly: each block is hidden
  // only when the card above it is actually carrying that same content, so a
  // task or ideator run — which has no result card — keeps both.
  const resultCard = !running && resultCardVisible({
    documents: resultDocs.length, review: Boolean(review),
    nextActionKind: action.kind, decision: decidable && Boolean(journey),
  });
  const findingsInCard = resultCard && (review?.findings.length ?? 0) > 0;
  const documentInCard = resultCard && resultDocs.length > 0;
  // Preview text for each result document, fetched alongside the run rather
  // than by the card, so a slow file never delays the page.
  const previews = useQueries({
    queries: resultDocs.map((doc) => ({
      queryKey: ['document', nodeFileURL(run.runId, doc.file)],
      queryFn: async () => {
        const response = await fetch(nodeFileURL(run.runId, doc.file));
        if (!response.ok) throw new Error(String(response.status));
        return response.text();
      },
      staleTime: 60_000,
    })),
  }).map((query, index) => ({
    nodeId: resultDocs[index].nodeId,
    path: resultDocs[index].file.path,
    text: typeof query.data === 'string' ? query.data : undefined,
  }));

  const identity = run.options?.projectWorkflow as { name?: string; sourceWorkflowId?: string; sourceRevision?: number } | undefined;
  const savedName = run.workflowName?.trim() || (typeof identity?.name === 'string' ? identity.name.trim() : '');
  const sourceWorkflowId = typeof (run.options as Record<string, unknown> | undefined)?.sourceWorkflowId === 'string'
    ? (run.options!.sourceWorkflowId as string)
    : identity?.sourceWorkflowId;
  const brief = run.goal?.trim() || request?.instruction?.trim() || request?.domain?.trim() || '';
  // The full saved name, never a cut sentence with a run number after it: a
  // heading is a name or it is a plain label, and the brief is read below in
  // "Workflow brief" (Q-12).
  const heading = runHeading({
    savedName, runNumber: run.runNumber, brief,
    // A run whose settings were never recorded cannot be called a project
    // task either — it keeps the last-resort label it always had.
    fallback: nodeWorkflow ? 'Workflow run' : ideator ? 'App ideas' : request ? 'Project task' : 'SDK workflow',
  });
  const inputs = slotsSummary(run.slots, 6, run.slotLabels);

  const meta = headerMeta({
    chip,
    durationMs: liveDuration(run),
    usage: checkpoint.usage,
    model: typeof run.options?.model === 'string' ? run.options.model : undefined,
  });

  return <div className="sdk-run">
    <button className="btn btn--ghost" onClick={onBack}>← Back to runs</button>
    <header className="sdk-run-header">
      <h1>{heading}</h1>
      <StateChip state={run.state} label={chip} className={chipClass} />
    </header>
    <p className="sdk-run-meta mono">
      {meta}{chipSuffix ? ` ${chipSuffix}` : ''}
    </p>
    {automatic && <p className="sdk-run-automatic">{AUTOMATIC_ACCEPTANCE_NOTE}</p>}
    {governing?.note && <p className="sdk-run-note">{quotedNote(governing.note)}</p>}
    {!governing && signOffPolicy !== 'required' && run.state === 'passed' && (
      <p className="faint sdk-run-policy">This workflow’s sign-off setting: {SIGN_OFF_SHORT[signOffPolicy]}.</p>
    )}
    {sourceWorkflowId && (
      <p className="sdk-run-lineage">
        <Link to={`/workflows/${encodeURIComponent(sourceWorkflowId)}`}>All runs of “{savedName || 'this workflow'}”</Link>
      </p>
    )}
    {inputs && <p className="sdk-run-inputs"><strong>Inputs used this run:</strong> {inputs}</p>}
    {headline && <p className="sdk-run-headline" role="alert">{headline}</p>}
    {/* The brief always has a home. When the heading could not be the goal —
        because it was a paragraph, not a name — this disclosure is the only
        place the full text appears, so it must not be suppressed (Q-12). */}
    {brief && brief !== heading && <details className="sdk-run-brief"><summary>Workflow brief</summary><p>{brief}</p></details>}

    {/* The deliverable, the review's verdict and whose turn it is — before any
        status prose, which is the whole point of F-G. */}
    {!running && <RunResultCard runState={run.state}
      runId={run.runId}
      documents={resultDocs}
      previews={previews}
      review={review}
      nextActionText={action.text}
      nextActionKind={action.kind}
      onOpenDocument={setReading}
      // The REVIEW, not the reviewer's session log: a check step's log is
      // usually empty, so "Read the review" opened "Log: Check the note … no
      // log output" while review.md sat in the run's outputs (S-16). The log
      // stays the fallback for a review that recorded no document.
      onOpenReview={() => {
        if (review?.document) {
          setReading({ url: nodeFileURL(run.runId, review.document), title: review.stepTitle ?? 'Independent review',
            stepTitle: review.stepTitle, sha256: review.document.sha256 });
          return;
        }
        if (review?.stepId) open({ runId: run.runId, nodeId: review.stepId, title: review.stepTitle ?? 'Independent review' });
      }}
      decision={decidable && journey ? <div className="run-signoff">
        {/* Why the buttons are here at all when the Inbox never asked: the
            policy governs the queue, not whether you may judge your own
            result. */}
        {!signOffPending && <p className="faint">This workflow does not queue a sign-off, so nothing asked you for this. You can still record your decision on this result here.</p>}
        <label className="run-signoff-label" htmlFor="run-signoff-note">Your note (optional)</label>
        <textarea id="run-signoff-note" className="run-signoff-note" rows={2} value={signOffNote}
          placeholder={NOTE_PLACEHOLDER} onChange={event => setSignOffNote(event.target.value)} />
        <div className="sdk-run-actions">
          <button className="btn btn--primary btn--sm" disabled={signOff.isPending || signOff.isSuccess} onClick={() => signOff.mutate('accepted')}>Accept</button>
          <button className="btn btn--sm" disabled={signOff.isPending || signOff.isSuccess} onClick={() => signOff.mutate('changes_requested')}>Request changes</button>
          <button className="btn btn--sm" disabled={signOff.isPending || signOff.isSuccess} onClick={() => signOff.mutate('rejected')}>Reject</button>
        </div>
        <p className="faint">Your decision records your judgment of this result. It does not change any recorded check result.</p>
        {signOff.isSuccess && <p role="status">Decision recorded.</p>}
        {signOff.error && <p role="alert">{signOff.error.message}</p>}
      </div> : undefined}
    />}

    <section className="panel sdk-run-section" aria-label="Session status">
      <p role="status">{waitingForPerson ? 'Waiting for your approval' : nodeWorkflow && run.state === 'passed' ? request?.nodes?.some(n => ['approval', 'check', 'task'].includes(n.kind ?? '')) ? 'Workflow completed' : 'All step documents produced' : nodeWorkflow && running ? 'Working through your steps' : taskPhase(phase)}</p>
      <p className="faint">{stepLine}</p>
      {/* Staleness said out loud. While the API row lags behind the session
          (M-01), "Last updated" is the only thing on the page that can tell a
          reader the difference between a stuck step and a stale row. */}
      <p className="faint sdk-run-updated">Last updated {formatAgo(run.updatedAt)}{running ? ' · refreshing every 3 seconds' : ''}</p>
      {checkpoint.error && <p role="alert">{checkpoint.error}</p>}
      {!findingsInCard && (checkpoint.findings?.length ?? 0) > 0 && <><h2>Check findings</h2><ul className="sdk-run-findings">{checkpoint.findings!.map((finding, i) =>
        // Findings quote a reviewer who writes Markdown links; rendering them
        // as literal "[Source](url)" made the sources untraceable (S-04).
        <li key={i} dangerouslySetInnerHTML={{ __html: renderMarkdown(finding) }} />)}</ul></>}
      <div className="sdk-run-actions">
        <button className="btn" disabled={continuationBusy} onClick={refresh}>Refresh run status</button>
        <button className="btn" onClick={() => open({ runId: run.runId, nodeId: 'delivery', title: 'Session log' })}>Read session log</button>
        {running && <button className="btn" disabled={cancel.isPending} onClick={() => cancel.mutate()}>{cancel.isPending ? 'Stopping…' : 'Stop task'}</button>}
        {!running && request && <Link className="btn" to="/workflows" state={{
          slots: run.slots ?? undefined,
          setup: {
            // Lineage survives the rerun: an edit that started from a saved
            // workflow says which one, so the relaunch is run #4 of that
            // workflow rather than a second nameless sibling (R-02, D-11).
            id: sourceWorkflowId ?? '', revision: identity?.sourceRevision ?? 0, name: savedName,
            kind: nodeWorkflow ? 'nodes' : ideator ? 'app-ideator' : 'task',
            ...(nodeWorkflow ? { nodes: request?.nodes } : {}),
            instruction: request?.instruction ?? request?.domain ?? run.goal ?? '',
            targetRoot: !nodeWorkflow && !ideator ? request?.workspace ?? '' : '', check: request?.check ?? [],
            project: run.project ?? '', autoRepair: Boolean(run.options?.autoRepair),
            // The sign-off policy travels with the setup, so re-running the
            // same workflow does not quietly revert to asking every time.
            signOff: signOffPolicy,
          },
        }}>Use this setup again</Link>}
      </div>
      {cancel.error && <p role="alert">{cancel.error.message}</p>}
    </section>
    {run.state === 'interrupted' && nodeWorkflow && <SDKContinuation runId={run.runId} refreshVersion={refreshVersion} onOpenRun={onContinuedRunId} onBusy={setContinuationBusy}/>}
    {running && checkpoint.phase === 'approval_pending' && checkpoint.approval && <section className="panel sdk-run-section" aria-label="Approval needed">
      <h2>{checkpoint.approval.title}</h2><p>{checkpoint.approval.prompt}</p>
      {/* The draft, above the buttons. Approving text you have not been shown
          is the failure Q-13 recorded; the documents list stays below it. */}
      <ApprovalDraft documents={checkpoint.approval.documents} onOpen={setReading} />
      <ul>{checkpoint.approval.documents.map(doc => <li key={doc.url}><button className="btn btn--ghost btn--sm" onClick={() => setReading({ url: doc.url, title: workflowFileLabel(doc.name, request?.nodes) })}>{workflowFileLabel(doc.name, request?.nodes)}</button></li>)}</ul>
      <p>Steps that depend on this approval wait for your decision. Waiting for approval counts toward the run’s overall time limit. You can also stop the run.</p>
      <div className="sdk-run-actions"><button className="btn btn--primary" disabled={decide.isPending || decide.isSuccess} onClick={() => decide.mutate(true)}>Approve and continue</button>
        <button className="btn" disabled={decide.isPending || decide.isSuccess} onClick={() => decide.mutate(false)}>Reject and stop</button></div>
      {decide.isSuccess && <p role="status">Decision recorded — continuing…</p>}{decide.error && <p role="alert">{decide.error.message}</p>}
    </section>}
    {request?.nodes && <WorkflowGraph key={run.runId} nodes={request.nodes} checkpoints={checkpoint.nodes} runState={run.state} runId={run.runId} instruction={request.instruction ?? run.goal} model={typeof run.options?.model === 'string' ? run.options.model : undefined} checks={checkCatalog.error ? undefined : checkCatalog.data} />}
    {progress && <section className="panel sdk-run-section" aria-label="Step progress"><h2>Your steps</h2><ol className="sdk-step-progress">{progress.map(node => <li key={node.id}><strong>{node.title}</strong><span>{node.phase === 'passed' && node.kind === 'task' ? node.completion : node.kind === 'task' && running && ['running', 'verifying', 'correction_pending'].includes(node.phase) ? node.phase === 'verifying' && !node.acceptance && !node.check?.length ? 'Checking output files' : phases[node.phase] : node.phase === 'passed' && node.kind === 'approval' ? 'Decision approved' : node.phase === 'passed' && node.kind === 'check' ? 'Model review passed' : running && node.phase === 'running' && node.kind === 'research' ? 'Researching live sources' : nodePhases[node.phase] ?? node.phase}</span>{node.document && <button className="btn btn--ghost btn--sm" onClick={() => setReading({ url: nodeFileURL(run.runId, node.document!), title: node.title, stepTitle: node.title, sha256: node.document!.sha256 })}>{node.kind === 'approval' ? 'Read decision' : node.kind === 'task' ? 'Read work summary' : 'Read document'}</button>}{node.files.length > 0 && <ul>{node.files.map(file => <li key={file.path}><button className="btn btn--ghost btn--sm" title={file.path} onClick={() => setReading({ url: nodeFileURL(run.runId, file), title: workflowFileLabel(file.path, request?.nodes), stepTitle: node.title, sha256: file.sha256 })}>{workflowFileLabel(file.path, request?.nodes)}</button></li>)}</ul>}</li>)}</ol></section>}
    {checkpoint.text && !(nodeWorkflow && documentInCard) && <section className="panel sdk-run-section" aria-label="Worker response"><h2>{nodeWorkflow ? 'Latest step result' : 'Worker response'}</h2>{checkpoint.text.length > 1800 ? <details className="sdk-response-disclosure"><summary>Read full step response</summary><div className="sdk-run-text dv-body" dangerouslySetInnerHTML={{ __html: renderMarkdown(checkpoint.text) }} /></details> : <div className="sdk-run-text dv-body" dangerouslySetInnerHTML={{ __html: renderMarkdown(checkpoint.text) }} />}</section>}
    <section className="panel sdk-run-section" aria-label="Task details">
      <h2>Task details</h2>
      {request?.workspace && <><h3>Working folder</h3><p className="mono">{request.workspace}</p></>}
      {!request ? <p>Workflow settings were not recorded. Read the session log for startup details.</p> : request.check?.length ? <><h3>Acceptance command</h3><pre>{JSON.stringify(request.check, null, 2)}</pre></> : ideator ? <p>Built-in checks verify the three ideas and their comparison brief.</p> : !nodeWorkflow ? <p>No extra check was requested. Completion means files were produced; correctness has not been independently verified.</p> : null}
      {!nodeWorkflow && !ideator && request?.workspace && <p className="faint">Project files remain in the working folder on the server.</p>}
      {artifactsQuery.error && <p role="alert">Could not load run files. <button type="button" className="btn" onClick={() => void artifactsQuery.refetch()}>Retry run files</button></p>}
      {artifacts.length > 0 && <><h3>Run files</h3><ul>{artifacts.map((file) => <li key={file.url}><button className="btn btn--ghost btn--sm" title={file.name} onClick={() => setReading({ url: file.url, title: workflowFileLabel(file.name, request?.nodes) })}>{workflowFileLabel(file.name, request?.nodes)}</button></li>)}</ul></>}
      {request?.nodes && <details><summary>Reviewed workflow settings</summary><p>All steps and approval waits share the server’s run time limit.{request.maxTokens ? ` Spending cap: ${request.maxTokens.toLocaleString()} tokens per run, cached input included.` : ""} Cancellation remains available.</p>{request.nodes.map(node => <div key={node.id}>
        <h3>{node.title}</h3><p className="sdk-run-text">{node.instruction}</p>
        {node.acceptance && <p>Extra check: {node.acceptance}</p>}
        {node.inputs && <div>{Object.entries(node.inputs).map(([key,value])=><p key={key}><strong>{({pipelineLocation:'Original pipeline',runCommand:'Baseline command',changeRequest:'Requested change',invariants:'Protected invariants',grain:'One row represents',safeUse:'Safe use',sourceA:'Source A',sourceB:'Source B',sharedGrain:'Shared grain',measures:'Measure definitions',tolerances:'Owner tolerances',knownDifferences:'Known differences',dataset:'Source',keyColumns:'Key columns',baselineData:'Baseline window',currentData:'Current window',features:'Feature tolerances',seededControl:'Requested controls'} as Record<string,string>)[key] ?? key}:</strong> <span className="sdk-run-text">{value || 'Unspecified'}</span></p>)}</div>}
        {node.access && <><p>Allowed hosts: {node.access.hosts.join(', ') || 'None'}</p><p>Readable inputs: {node.access.readPaths?.join(', ') || 'System tools and workflow outputs'}</p><p>Connection names: {node.access.environment?.join(', ') || 'None'}</p><p>Temporary tool setup: {node.toolchain && node.sideEffect ? 'Allowed' : 'Off'}</p></>}
        {node.check && <><p>Acceptance command</p><pre>{JSON.stringify(node.check, null, 2)}</pre></>}
      </div>)}</details>}
      <details><summary>Technical details</summary><p className="mono">Run: {run.runId}<br />Thread: {checkpoint.threadId || 'Not started'}</p>
        {Boolean(run.options?.continuation) && <p>Usage includes the completed work carried from the earlier run and counts toward the original budget. Interrupted turns may have unreported usage.</p>}
        {checkpoint.usage && <p>{checkpoint.usage.input_tokens.toLocaleString()} input tokens ({checkpoint.usage.cached_input_tokens.toLocaleString()} cached), {checkpoint.usage.output_tokens.toLocaleString()} output tokens.</p>}
      </details>
    </section>
    {run.state === 'passed' && <section className="panel sdk-run-section"><h2>Reuse the method</h2><p>Create a draft skill from this run’s instructions and recorded evidence. Review its limits before approving it.</p><>{accepted && !automatic ? <ExtractSkillAction runId={run.runId} /> : automatic ? <p>This result was accepted automatically by the checks policy, not by a person. A skill can only be drafted from a result a person accepted — record your own decision on this run first.</p> : <p>A person must accept this result before a skill can be drafted. {signOffPending ? <><Link to="/inbox">Sign off in Inbox</Link>.</> : decidable ? 'Record your decision in “Your result” above.' : 'This run has no delivery to decide on.'}</p>}</></section>}
    {/* ONE acceptance line for one acceptance. The header already said who
        decided and when; repeating it here with a second attribution is what
        made one verdict read as two acceptors (Q-18, Q-19). */}
    {governing && <p className="faint sdk-run-decided">
      Recorded {acceptanceLine(acceptance!)}. Decisions are kept permanently; to change one, delete its record and decide again.
    </p>}
    {!running && <AskReviewer runId={run.runId} />}
    <StatusLegend nodeWorkflow={nodeWorkflow} />
    {reader}
    {reading && <RunDocumentReader doc={reading} onClose={() => setReading(null)} />}
  </div>;
}

/** The first approval document, rendered inline above the decision buttons. */
function ApprovalDraft({ documents, onOpen }: {
  documents: { name: string; url: string; sha256: string }[];
  onOpen: (doc: RunDocumentRef) => void;
}) {
  const first = documents[0];
  const { data } = useQuery({
    queryKey: ['document', first?.url],
    queryFn: async () => {
      const response = await fetch(first.url);
      if (!response.ok) throw new Error(String(response.status));
      return response.text();
    },
    enabled: Boolean(first?.url),
    staleTime: 30_000,
  });
  if (!first || typeof data !== 'string' || data.trim() === '') return null;
  return (
    <div className="approval-draft">
      {/* Named, so the reader knows this text IS the thing the buttons below
          decide on — the same label the Inbox card uses. */}
      <div className="eyebrow">What you are approving</div>
      <div className="dv-body" dangerouslySetInnerHTML={{ __html: renderMarkdown(data) }} />
      <button className="btn btn--ghost btn--sm" onClick={() => onOpen({ url: first.url, title: first.name })}>
        Open in the reader
      </button>
    </div>
  );
}
