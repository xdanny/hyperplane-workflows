import { useId } from 'react';
import type { ReactNode } from 'react';
import type { SDKWorkflowNode, WorkflowCheck } from '../api';
import { graphNodeState, nodeDependencies } from '../workflowGraph';
import { inputLabel, nodeCapabilities, nodeCompletionLabel, nodeContext, nodeFileURL, nodeKind, nodeVerification, recordedNodeFiles, recordedSearches, searchCount, searchLine } from '../workflowNodePresentation';
import type { WorkflowNodeCheckpoint } from '../workflowNodePresentation';
import { workflowFileLabel } from '../workflowFiles';
import { CheckClaims, WorkflowCheckRequirements } from './WorkflowCheckRequirements';
import './WorkflowNodeDetails.css';

export interface WorkflowNodeDetailsProps {
  node: SDKWorkflowNode; nodes: SDKWorkflowNode[]; checkpoint?: WorkflowNodeCheckpoint;
  checkpoints?: WorkflowNodeCheckpoint[]; runId?: string; runState?: string;
  instruction?: string; model?: string; checks?: WorkflowCheck[]; id?: string;
  onInspect?: (id: string) => void; navigation?: ReactNode;
}
export function WorkflowNodeDetails({ node, nodes, checkpoint, checkpoints, runId, runState, instruction, model, checks, id, onInspect, navigation }: WorkflowNodeDetailsProps) {
  const kind = nodeKind(node), context = nodeContext(node, nodes), files = recordedNodeFiles(checkpoint);
  const state = runState ? graphNodeState(node, nodes, checkpoints ?? (checkpoint ? [checkpoint] : []), runState) : undefined;
  const generatedId = useId();
  const headingId = `${id ?? generatedId}-heading`;
  const dependents = nodes.filter((_, index) => nodeDependencies(nodes, index).includes(node.id));
  const stepLink = (step: SDKWorkflowNode) => onInspect ? <button type="button" className="workflow-node-link" onClick={() => onInspect(step.id)}>{step.title || 'Untitled step'}</button> : step.title;
  return <article className="workflow-node-details" id={id} tabIndex={-1} aria-labelledby={headingId}>
    {navigation}
    <header><p className="workflow-node-type"><span aria-hidden="true">{kind.icon}</span> {kind.label}</p><h3 id={headingId}>{node.title || 'Untitled step'}</h3>
      {state && <p className={`workflow-graph-state is-${state.kind}`}>{nodeCompletionLabel(node, checkpoint) ?? state.label}</p>}
    </header>
    <section aria-label="Step instructions"><h4>What this step does</h4>{node.instruction.length > 1200 ? <><p className="workflow-node-fulltext">{node.instruction.slice(0, 360)}…</p><details><summary>Read full instructions</summary><p className="workflow-node-fulltext">{node.instruction}</p></details></> : <p className="workflow-node-fulltext">{node.instruction}</p>}</section>
    <section aria-label="Step inputs"><h4>Inputs and dependencies</h4>
      <p>{context.direct.length ? 'Waits for all of these steps:' : 'Starts from your goal without waiting for another step.'}</p>
      {context.direct.length > 0 && <ul>{context.direct.map(parent => { const step = nodes.find(step => step.id === parent); return <li key={parent}>{step ? stepLink(step) : `Missing step: ${parent}`}</li>; })}</ul>}
      {context.ancestors.length > 0 && <details><summary>All prerequisite context ({context.ancestors.length} steps)</summary><p>These prerequisite documents are included as context. Unrelated branches are not dependencies.</p><ul>{context.ancestors.map(parent => <li key={parent.id}>{parent.title}</li>)}</ul></details>}
      {instruction && <details><summary>Workflow goal</summary><p className="workflow-node-fulltext">{instruction}</p></details>}
      {node.inputs && Object.keys(node.inputs).length > 0 ? <details className="workflow-node-inputs"><summary>Declared inputs ({Object.keys(node.inputs).length})</summary><dl>{Object.entries(node.inputs).map(([key, value]) => <div key={key}><dt>{inputLabel(key)}</dt><dd className="workflow-node-fulltext">{value || 'Unspecified'}</dd></div>)}</dl></details> : <p className="faint">No additional input values are declared.</p>}
    </section>
    <section aria-label="Dependent steps"><h4>Steps that follow this one</h4>{dependents.length ? <><p>These steps wait for this step and any other prerequisites they declare.</p><ul>{dependents.map(step => <li key={step.id}>{stepLink(step)}</li>)}</ul></> : <p>No steps depend on this one.</p>}</section>
    <section aria-label="Step verification"><h4>Checks and decision</h4><p>{nodeVerification(node, checkpoint)}</p>
      {node.acceptance && <><p>Extra check: {runId ? node.acceptance : checks?.find(check => check.id === node.acceptance)?.title ?? node.acceptance}</p>
        {/* What this result IS and is not, beside the result itself. The
            receipt records an identifier and a verdict; the two claim lists
            come from the catalog as it stands, which is why the receipt note
            below still says the description was not stored here (D-06). */}
        {runId ? <><CheckClaims check={checks?.find(check => check.id === node.acceptance)} />
          <p className="faint">This receipt records the check identifier and its result. The check catalog description is not stored in this receipt.</p></>
          : <WorkflowCheckRequirements acceptance={node.acceptance} checks={checks}/>}</>}
      {node.check?.length ? <details><summary>Acceptance command and literal arguments</summary><pre>{JSON.stringify(node.check, null, 2)}</pre></details> : null}
      {checkpoint?.findings?.length ? <><h5>Recorded findings</h5><ul>{checkpoint.findings.map((finding, index) => <li className="workflow-node-fulltext" key={index}>{finding}</li>)}</ul></> : null}
    </section>
    <section aria-label="Step outputs"><h4>Outputs and evidence</h4><p><strong>Expected:</strong> {kind.output}.</p>
      {files.length > 0 ? <><p><strong>Recorded:</strong> {files.length} {files.length === 1 ? 'file' : 'files'} in this checkpoint.</p><ul className="workflow-node-evidence">{files.map(file => <li key={file.path}>
        {runId ? <a href={nodeFileURL(runId, file)} target="_blank" rel="noreferrer">{workflowFileLabel(file.path, nodes)}</a> : <span>{workflowFileLabel(file.path, nodes)}</span>}
        <small>{file.bytes.toLocaleString()} bytes</small>
        <details><summary>Saved path and hash</summary><p className="mono">{file.path}</p><p className="mono">SHA-256: {file.sha256}</p></details>
      </li>)}</ul></> : <p className="faint">{runState ? 'No file evidence is recorded for this step yet.' : 'Expected outputs describe the plan. Files appear here only after a run records them.'}</p>}
    </section>
    <details className="workflow-node-capabilities"><summary>Tools and access</summary><ul>{nodeCapabilities(node, nodes).map(line => <li key={line}>{line}</li>)}</ul>
      {node.kind !== 'approval' && <p>Model: {model || (runId ? 'Not recorded in these settings' : 'Chosen when the workflow starts')}.</p>}
      {node.access && <dl><dt>Allowed hosts</dt><dd>{node.access.hosts.join(', ') || 'None'}</dd><dt>Readable inputs</dt><dd>{node.access.readPaths?.join(', ') || 'System tools and workflow outputs'}</dd><dt>Connection references</dt><dd>{node.access.environment?.join(', ') || 'None'}</dd></dl>}
      <p className="faint">These are declared capabilities, not a history of tool calls. Connection names are references, not credential values.</p>
    </details>
    {checkpoint && <details><summary>Recorded execution details</summary><dl>
      <dt>Checkpoint phase</dt><dd>{checkpoint.phase}</dd>
      {checkpoint.attempts !== undefined && <><dt>Attempts</dt><dd>{checkpoint.attempts}</dd></>}
      {searchCount(checkpoint) !== undefined && <><dt>Completed web searches</dt><dd>{searchCount(checkpoint)}</dd></>}
      {checkpoint.threadId && <><dt>SDK thread</dt><dd className="mono">{checkpoint.threadId}</dd></>}
    </dl>
      {/* The searches themselves, not just how many there were. A count could
          not tell a citation the model recalled from a source it fetched, which
          is the question a failed review turns on (D-01). These are the queries
          the worker recorded and the hosts they returned — evidence of what was
          ASKED, which is not evidence that any page was read. */}
      <RecordedSearches checkpoint={checkpoint} />
      <p className="faint">A stopped run can retain its last active checkpoint. The current run state determines whether work is still active.</p></details>}
  </article>;
}

/** The recorded searches of one step, one line each. */
function RecordedSearches({ checkpoint }: { checkpoint: WorkflowNodeCheckpoint }) {
  const searches = recordedSearches(checkpoint);
  if (searches.length === 0) {
    // Absence is a fact worth stating: a step with five searches and no
    // recorded queries is a step whose worker predates the recording, not a
    // step that searched nothing.
    return searchCount(checkpoint) ? <p className="faint">No queries were recorded for these searches.</p> : null;
  }
  return <>
    <h5>Recorded searches</h5>
    <ul className="workflow-node-searches">{searches.map((search, index) => <li key={index}>
      <span className="workflow-node-fulltext">{searchLine(search)}</span>
      {(search.urls?.length ?? 0) > 0 && <ul className="workflow-node-search-urls">{search.urls!.map(url =>
        <li key={url}><a className="mono" href={url} target="_blank" rel="noreferrer">{url}</a></li>)}</ul>}
    </li>)}</ul>
    {/* Said ONCE, under the list, rather than as an empty arrow on every row:
        this SDK reports the query a model issued and, usually, nothing about
        what came back (D-17). */}
    <p className="faint">
      These are the queries this step ran.{' '}
      {searches.some((search) => (search.urls?.length ?? 0) > 0)
        ? 'Results are listed for the searches that reported them.'
        : 'Results were not recorded by this SDK, only the queries.'}{' '}
      A recorded search does not establish that a page was read, and nothing in the document is marked as fetched on this basis.
    </p>
  </>;
}
