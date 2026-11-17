import { useEffect, useId, useRef, useState } from 'react';
import type { SDKWorkflowNode, WorkflowCheck } from '../api';
import { graphNodeState, workflowGraph } from '../workflowGraph';
import { nodeCompletionLabel, nodeKind } from '../workflowNodePresentation';
import type { WorkflowNodeCheckpoint } from '../workflowNodePresentation';
import { WorkflowNodeDetails } from './WorkflowNodeDetails';
import './WorkflowGraph.css';

interface WorkflowGraphProps {
  nodes: SDKWorkflowNode[]; checkpoints?: WorkflowNodeCheckpoint[]; runState?: string;
  runId?: string; instruction?: string; model?: string; checks?: WorkflowCheck[];
  onSelect?: (id: string) => void;
  /**
   * Start with the diagram behind "Show diagram". Omitted, a straight chain of
   * five steps or fewer collapses itself: the list below it already says more
   * than a picture of one arrow per step, and the diagram used to cost 500 px
   * above the fold to say less. A branching workflow stays open — that is the
   * shape a list cannot show. A step opened from a link always expands it.
   */
  defaultCollapsed?: boolean;
}
// A new run must not retain the previous run's selected node or evidence.
export function WorkflowGraph(props: WorkflowGraphProps) {
  return <WorkflowGraphContent key={props.runId ?? 'editor'} {...props}/>;
}
function WorkflowGraphContent({ nodes, checkpoints, runState, runId, instruction, model, checks, onSelect, defaultCollapsed }: WorkflowGraphProps) {
  const marker = useId().replaceAll(':', '');
  const cards = useRef(new Map<string, HTMLElement>());
  const nodeIds = JSON.stringify(nodes.map(node => node.id));
  const hashSelection = () => {
    if (!runId || !window.location.hash.startsWith('#sdk-step-')) return null;
    try {
      const id = decodeURIComponent(window.location.hash.slice('#sdk-step-'.length));
      return nodes.some(node => node.id === id) ? { id, focus: true } : null;
    } catch { return null; }
  };
  const [selection, setSelection] = useState<{id: string; focus: boolean} | null>(() => {
    return hashSelection();
  });
  const selectionRef = useRef(selection);
  selectionRef.current = selection;
  const graph = workflowGraph(nodes);
  // A straight chain is what the step list already reads as; only a branch
  // needs a picture. Collapsing is presentation: nothing about the definition.
  const chain = !graph.unresolved && graph.rows.every(row => row.length === 1);
  const collapsible = defaultCollapsed ?? (chain && nodes.length <= 5);
  // Until someone says otherwise the shape decides: adding a branch to a chain
  // reveals the diagram, because that is when it starts saying something.
  const [choice, setChoice] = useState<boolean | null>(null);
  const open = !collapsible || (choice ?? Boolean(selection));
  const setOpen = (value: boolean) => setChoice(value);
  const focusCard = (id: string) => {
    const card = cards.current.get(id);
    card?.focus({preventScroll: true});
    card?.scrollIntoView({block: 'nearest', inline: 'nearest'});
  };
  const selected = nodes.find(node => node.id === selection?.id);
  const inspectorId = selected ? runId ? `sdk-step-${selected.id}` : `workflow-inspector-${marker}-${selected.id}` : undefined;
  useEffect(() => {
    // Checkpoint polling must never move keyboard focus. Only hash/ID changes
    // synchronize selection; repeated browser events retain the same object.
    const sync = () => {
      const next = hashSelection();
      const previous = selectionRef.current;
      if (previous?.id === next?.id) return;
      if (next) setOpen(true);
      if (!window.location.hash && previous) focusCard(previous.id);
      setSelection(next);
    };
    if (!runId) {
      setSelection(previous => previous && !nodes.some(node => node.id === previous.id) ? null : previous);
      return;
    }
    sync();
    window.addEventListener('hashchange', sync);
    window.addEventListener('popstate', sync);
    return () => {
      window.removeEventListener('hashchange', sync);
      window.removeEventListener('popstate', sync);
    };
  }, [runId, nodeIds]);
  useEffect(() => {
    if (!selection?.focus || !inspectorId) return;
    const element = document.getElementById(inspectorId);
    element?.focus({preventScroll: true});
    element?.scrollIntoView({block: 'nearest'});
  }, [selection, inspectorId]);
  const inspect = (id: string) => {
    if (!nodes.some(node => node.id === id)) return;
    setOpen(true);
    setSelection({id, focus: true});
    const hash = `#sdk-step-${encodeURIComponent(id)}`;
    if (runId && window.location.hash !== hash) window.history.pushState(window.history.state, '', hash);
  };
  const returnToDiagram = () => {
    setSelection(null);
    if (runId) window.history.pushState(window.history.state, '', window.location.pathname + window.location.search);
    if (selected) focusCard(selected.id);
  };
  const chooser = (location: 'graph' | 'inspector') => <div className="workflow-graph-navigation">
    <label htmlFor={`${marker}-${location}-jump`}>{location === 'graph' ? 'Jump to step' : 'Inspect another step'}</label>
    <select id={`${marker}-${location}-jump`} value={selected?.id ?? ''} onChange={event => inspect(event.target.value)}>
      <option value="" disabled>Choose a step to inspect</option>
      {nodes.map((node, index) => <option key={node.id} value={node.id}>{index + 1}. {node.title || 'Untitled step'}</option>)}
    </select>
    {location === 'inspector' && <button type="button" onClick={returnToDiagram}>Return to diagram</button>}
  </div>;
  const recordCard = (id: string, element: HTMLElement | null) => {
    if (element) cards.current.set(id, element);
    else cards.current.delete(id);
  };
  if (!nodes.length) return null;
  // Compact cards: type, title, state and one line of the instruction. A linear
  // chain runs left to right, so three steps read as three steps rather than as
  // one card at a time inside a scroll box.
  const column = 264, rowHeight = 156, cardWidth = 240, cardHeight = 120;
  const width = chain ? nodes.length * column : Math.max(1, ...graph.rows.map(row => row.length)) * column;
  const height = chain ? rowHeight : graph.rows.length * rowHeight;
  const positions = new Map(chain
    ? graph.rows.flatMap((row, depth) => row.map(node => [node.id, { x: depth * column + 12, y: 12 }] as const))
    : graph.rows.flatMap((row, depth) => row.map((node, index) => [node.id, { x: (width - row.length * column) / 2 + index * column + 12, y: depth * rowHeight + 12 }] as const)));
  const title = (id: string) => nodes.find(node => node.id === id)?.title || `Missing step: ${id}`;
  const active = nodes.filter(node => ['running', 'verifying', 'saving', 'correction_pending', 'starting'].includes(checkpoints?.find(state => state.id === node.id)?.phase ?? '')).length;
  const shape = chain
    ? `${nodes.length} ${nodes.length === 1 ? 'step' : 'steps'}, each waiting for the one before it.`
    : `${nodes.length} steps across ${graph.rows.length} ${graph.rows.length === 1 ? 'stage' : 'stages'}; some run in parallel.`;
  return <section className="workflow-graph" aria-label="Workflow graph">
    <div className="workflow-graph-heading">
      <h2>How the steps connect</h2>
      {collapsible && <button type="button" className="btn btn--ghost" aria-expanded={open} onClick={() => setOpen(!open)}>{open ? 'Hide diagram' : 'Show diagram'}</button>}
    </div>
    {!open ? <p className="workflow-graph-summary">{shape} {onSelect ? 'Open the diagram to inspect a step or change what it waits for.' : 'Open the diagram to inspect a step.'}</p> : <>
    <p>Each step waits for its dependencies. Up to two ready document, research or review steps can work in parallel. File creation and approvals run on their own.</p>
    {runState ? <p className="workflow-graph-summary">{runState === 'running' ? `${active} ${active === 1 ? 'step is' : 'steps are'} active. Ready means eligible to start; a step may still wait for an available worker.` : 'States come from this run’s recorded checkpoints.'} Select a step to inspect its instructions, inputs and evidence.</p>
      : <p className="workflow-graph-summary">Separate branches can progress together. Choose dependencies below to change the connections. Steps without a custom choice follow the previous step in the list.</p>}
    {graph.unresolved && <p className="workflow-graph-warning">Some connections cannot be arranged. Review the dependency choices and the validation message before saving or running.</p>}
    <p className="workflow-graph-scroll-hint">Scroll inside the diagram to follow longer workflows and wider branches.</p>
    {chooser('graph')}
    <div className="workflow-graph-scroll" tabIndex={0} role="region" aria-label="Dependency diagram. Scroll to see all branches.">
      <div className="workflow-graph-canvas" style={{ width, height }}>
        <svg className="workflow-graph-edges" width={width} height={height} aria-hidden="true">
          <defs><marker id={marker} viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto"><path d="M 0 0 L 10 5 L 0 10 z" fill="currentColor" /></marker></defs>
          {nodes.flatMap(node => graph.dependencies.get(node.id)!.flatMap(parent => {
            const from = positions.get(parent), to = positions.get(node.id);
            if (!from || !to) return [];
            const [x1, y1, x2, y2] = chain
              ? [from.x + cardWidth, from.y + cardHeight / 2, to.x, to.y + cardHeight / 2]
              : [from.x + cardWidth / 2, from.y + cardHeight, to.x + cardWidth / 2, to.y];
            const curve = chain ? `C ${(x1+x2)/2} ${y1}, ${(x1+x2)/2} ${y2}, ${x2} ${y2}` : `C ${x1} ${(y1+y2)/2}, ${x2} ${(y1+y2)/2}, ${x2} ${y2}`;
            return <path key={`${parent}-${node.id}`} data-from={parent} data-to={node.id} d={`M ${x1} ${y1} ${curve}`} markerEnd={`url(#${marker})`} />;
          }))}
        </svg>
        <ul className="workflow-graph-nodes">
          {nodes.map(node => {
            const position = positions.get(node.id)!, parents = graph.dependencies.get(node.id)!;
            const checkpoint = checkpoints?.find(state => state.id === node.id);
            const state = runState ? graphNodeState(node, nodes, checkpoints ?? [], runState) : { kind: 'draft', label: parents.length ? 'After dependencies' : 'Starts with your goal' };
            const kind = nodeKind(node);
            const description = parents.length ? `After: ${parents.map(title).join(', ')}` : 'No dependencies';
            const content = <><span className="workflow-node-type"><span aria-hidden="true">{kind.icon}</span> {kind.label}<span className={`workflow-graph-state is-${state.kind}`}>{nodeCompletionLabel(node, checkpoint) ?? state.label}</span></span>
              <strong className="workflow-graph-title">{node.title || 'Untitled step'}</strong>
              <span className="workflow-graph-excerpt" title={description}>{node.instruction}</span></>;
            return <li key={node.id} data-node-id={node.id} data-state={state.kind} data-selected={selected?.id === node.id} style={{ left: position.x, top: position.y, width: cardWidth, height: cardHeight }}>
              {onSelect ? <><button className="workflow-graph-main" type="button" onClick={() => onSelect(node.id)} aria-label={`Edit ${node.title}. ${description}`}>{content}</button><button ref={element => recordCard(node.id, element)} className="workflow-graph-inspect" type="button" aria-label={`Inspect ${node.title}`} aria-expanded={selected?.id === node.id} onClick={() => inspect(node.id)}>Inspect step</button></>
                : <a ref={element => recordCard(node.id, element)} className="workflow-graph-main" href={`#sdk-step-${encodeURIComponent(node.id)}`} aria-expanded={selected?.id === node.id} aria-label={`${node.title}. ${state.label}. ${description}`} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return; event.preventDefault(); inspect(node.id); }}>{content}</a>}
            </li>;
          })}
        </ul>
      </div>
    </div>
    </>}
    {open && selected ? <WorkflowNodeDetails id={inspectorId} node={selected} nodes={nodes} checkpoint={checkpoints?.find(checkpoint => checkpoint.id === selected.id)} checkpoints={checkpoints} runId={runId} runState={runState} instruction={instruction} model={model} checks={checks} onInspect={inspect} navigation={chooser('inspector')}/>
      : open ? <p className="workflow-graph-summary">{onSelect ? 'Use Inspect step for the full instructions, prerequisites, expected outputs and allowed tools.' : 'Select a step for its full instructions, prerequisites, allowed tools and recorded outputs.'}</p> : null}
  </section>;
}
