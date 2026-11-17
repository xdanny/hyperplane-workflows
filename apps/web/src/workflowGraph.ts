import type { SDKWorkflowNode } from './api';

// This is a projection for display. The backend SDK remains the validator and
// scheduler; omitted dependencies retain the preceding-step compatibility rule.
export function nodeDependencies(nodes: SDKWorkflowNode[], index: number): string[] {
  return nodes[index].dependsOn ?? (index > 0 ? [nodes[index - 1].id] : []);
}

export function workflowGraph(nodes: SDKWorkflowNode[]) {
  const dependencies = new Map(nodes.map((node, index) => [node.id, nodeDependencies(nodes, index)]));
  const levels = new Map<string, number>();
  for (let pass = 0; pass < nodes.length; pass++) {
    for (const node of nodes) {
      const parents = dependencies.get(node.id)!;
      if (!levels.has(node.id) && parents.every(id => levels.has(id))) {
        levels.set(node.id, parents.length ? Math.max(...parents.map(id => levels.get(id)!)) + 1 : 0);
      }
    }
  }
  const unresolved = nodes.filter(node => !levels.has(node.id));
  const last = levels.size ? Math.max(...levels.values()) + 1 : 0;
  for (const node of unresolved) levels.set(node.id, last);
  const rows: SDKWorkflowNode[][] = [];
  for (const node of nodes) (rows[levels.get(node.id)!] ??= []).push(node);
  return { dependencies, rows, unresolved: unresolved.length > 0 };
}

export interface GraphCheckpoint { id: string; phase: string }
export function graphNodeState(node: SDKWorkflowNode, nodes: SDKWorkflowNode[], checkpoints: GraphCheckpoint[], runState: string) {
  const phase = checkpoints.find(state => state.id === node.id)?.phase;
  if (phase === 'passed') return { kind: 'completed', label: 'Completed' };
  if (phase === 'failed' || phase === 'error' || phase === 'rejected') return { kind: 'failed', label: phase === 'rejected' ? 'Rejected' : 'Failed' };
  if (phase === 'interrupted') return { kind: 'stopped', label: 'Stopped' };
  if (runState !== 'running') {
    if (phase && phase !== 'pending') return { kind: 'stopped', label: 'Stopped' };
  } else {
    if (phase === 'approval_pending') return { kind: 'approval', label: 'Waiting for approval' };
    if (['running', 'verifying', 'saving', 'correction_pending', 'starting'].includes(phase ?? '')) return { kind: 'active', label: phase === 'verifying' ? 'Checking' : phase === 'saving' ? 'Saving' : 'Working' };
  }
  const dependencies = new Map(nodes.map((step, index) => [step.id, nodeDependencies(nodes, index)]));
  function failedAncestor(id: string, seen = new Set<string>()): boolean {
    if (seen.has(id)) return false;
    seen.add(id);
    return (dependencies.get(id) ?? []).some(parent => ['failed', 'error', 'rejected'].includes(checkpoints.find(state => state.id === parent)?.phase ?? '') || failedAncestor(parent, seen));
  }
  if (failedAncestor(node.id)) return { kind: 'blocked', label: 'Blocked by a failed step' };
  if (!phase) return { kind: 'pending', label: 'No checkpoint recorded' };
  if (runState !== 'running') return { kind: 'stopped', label: 'Not run' };
  const parents = dependencies.get(node.id) ?? [];
  return parents.every(id => checkpoints.some(state => state.id === id && state.phase === 'passed'))
    ? { kind: 'ready', label: 'Ready' }
    : { kind: 'pending', label: 'Waiting for dependencies' };
}

/** Steps that name `id` as a prerequisite, explicitly or by the implicit rule. */
export function dependentSteps(nodes: SDKWorkflowNode[], id: string): SDKWorkflowNode[] {
  return nodes.filter((_, index) => nodeDependencies(nodes, index).includes(id));
}

/**
 * Whether choosing `candidateId` as a prerequisite of `nodeId` would close a
 * loop — the dependency picker hides those, because the SDK validator rejects
 * them and a picker that offers a rejected choice is a trap, not a control.
 */
export function wouldCycle(nodes: SDKWorkflowNode[], nodeId: string, candidateId: string): boolean {
  if (nodeId === candidateId) return true;
  const dependencies = new Map(nodes.map((node, index) => [node.id, nodeDependencies(nodes, index)]));
  const seen = new Set<string>();
  const reaches = (id: string): boolean => {
    if (id === nodeId) return true;
    if (seen.has(id)) return false;
    seen.add(id);
    return (dependencies.get(id) ?? []).some(reaches);
  };
  return reaches(candidateId);
}

/**
 * Move a step and keep the visual order gating.
 *
 * Moving an approval up used to leave the step it was supposed to gate still
 * pointing at the approval's old neighbour, so the workflow finished without
 * the decision — the one thing the approval was added for.
 *
 * The repair is deliberately additive. Explicit links are a person's stated
 * requirement and may point forward in the list, so nothing here removes one:
 * the only change is that steps which sit after the moved step and waited for
 * the step it now follows wait for the moved step instead. That link is
 * redirected when the moved step already waits for the same step (no gate is
 * lost, one is gained) and added alongside otherwise. A step whose prerequisite
 * is omitted follows the list already, so it needs no rewiring at all, and any
 * redirection that would close a loop is skipped.
 */
export function moveStep(nodes: SDKWorkflowNode[], id: string, index: number): SDKWorkflowNode[] {
  const from = nodes.findIndex(node => node.id === id);
  if (from < 0) return nodes;
  const moved = nodes[from];
  const rest = nodes.filter(node => node.id !== id);
  const to = Math.max(0, Math.min(index, rest.length));
  const order = [...rest];
  order.splice(to, 0, moved);
  const previous = to > 0 ? order[to - 1] : undefined;
  if (to === from || !previous) return order;
  const covered = dependsOnTransitively(order, moved.id, previous.id);
  return order.map((node, place) => {
    if (!node.dependsOn || place <= to || node.id === moved.id) return node;
    if (!node.dependsOn.includes(previous.id) || node.dependsOn.includes(moved.id)) return node;
    if (wouldCycle(order, node.id, moved.id)) return node;
    return { ...node, dependsOn: covered ? node.dependsOn.map(parent => parent === previous.id ? moved.id : parent) : [...node.dependsOn, moved.id] };
  });
}

/** Whether `id` waits for `ancestor`, directly or through other steps. */
function dependsOnTransitively(nodes: SDKWorkflowNode[], id: string, ancestor: string): boolean {
  const dependencies = new Map(nodes.map((node, index) => [node.id, nodeDependencies(nodes, index)]));
  const seen = new Set<string>();
  const walk = (current: string): boolean => {
    if (seen.has(current)) return false;
    seen.add(current);
    return (dependencies.get(current) ?? []).some(parent => parent === ancestor || walk(parent));
  };
  return walk(id);
}
