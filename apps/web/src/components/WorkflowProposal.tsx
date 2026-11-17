import { useEffect, useRef } from 'react';
import { useMutation } from '@tanstack/react-query';
import { api } from '../api';
import { textByteIssue } from '../workflowValidation';
import { useWorkflowValidation } from '../hooks/useWorkflowValidation';
import { WorkflowValidationStatus } from './WorkflowValidationStatus';
import { WorkflowProfileInputs } from './WorkflowProfileInputs';
import { WorkflowCheckRequirements } from './WorkflowCheckRequirements';
import { nodeDependencies } from '../workflowGraph';
import type { SDKWorkflow, WorkflowCheck } from '../api';

export function WorkflowProposal({ goal, project, count, disabled, onUse, checks }: {
  goal: string; project: string; count: number; disabled: boolean; onUse: (workflow: SDKWorkflow) => void; checks?: WorkflowCheck[];
}) {
  const controller = useRef<AbortController | null>(null);
  const plan = useMutation({ mutationFn: async () => {
    controller.current?.abort();
    controller.current = new AbortController();
    const forGoal = goal.trim();
    const result = await api.compose(forGoal, project, controller.current.signal);
    return { ...result, forGoal };
  } });
  const reset = plan.reset;
  useEffect(() => { controller.current?.abort(); reset(); return () => controller.current?.abort(); }, [goal, project, reset]);
  const proposal = plan.data?.forGoal === goal.trim() ? plan.data : undefined;
  // A partial proposal cannot silently drop requested actions — the findings
  // stay visible above the steps. It is not a dead end either: a proposal whose
  // only problem is something Hyperplane cannot do (a schedule) is still a
  // usable set of steps, so "Use these steps anyway" is always offered when a
  // workflow came back. Only a definition the SDK validator rejects is refused.
  const nodes = proposal?.workflow?.nodes ?? [];
  const validation = useWorkflowValidation(proposal?.workflow ? { instruction: proposal.workflow.instruction ?? '', nodes } : undefined);
  const requirementsAvailable = nodes.every(node => !node.acceptance || checks?.some(check => check.id === node.acceptance));
  const usable = Boolean(proposal?.workflow) && validation.valid && requirementsAvailable;
  return <section className="workflow-proposal" aria-label="Suggested steps">
    <p>Have a goal but no steps yet? Ask for a proposal, then review it before using it.</p>
    <button type="button" className="btn" disabled={disabled || plan.isPending || !goal.trim() || Boolean(textByteIssue(goal.trim(), 'the goal', 16000))} onClick={() => plan.mutate()}>{plan.isPending ? 'Suggesting steps…' : 'Suggest steps from goal'}</button>
    {plan.isPending && <button type="button" className="btn btn--ghost" onClick={() => controller.current?.abort()}>Stop suggesting</button>}
    {plan.error && <p role="alert">{plan.error.name === 'AbortError' ? 'Suggestion stopped. Your steps are unchanged.' : plan.error.message}</p>}
    {proposal && <div className="workflow-proposal-result">
      {proposal.unsupported.length > 0 && <><h3>What this proposal still needs</h3><ul>{proposal.unsupported.map((finding, i) => <li key={i}>{finding}</li>)}</ul><p>{proposal.workflow ? 'The steps below are still usable — read the findings first, then use them and edit anything they do not cover.' : 'Add the missing facts to your goal, or configure additional capabilities in the editor.'}</p></>}
      {proposal.workflow && <><h3>{proposal.workflow.name}</h3>{proposal.workflow.instruction && <details><summary>Goal and input declarations</summary><p style={{whiteSpace:'pre-wrap'}}>{proposal.workflow.instruction}</p></details>}<ol>{nodes.map((node,index) => <li key={node.id}><strong>{node.title}</strong>{node.instruction.length > 600 ? <details><summary>Instructions and required evidence</summary><p>{node.instruction}</p></details> : <p>{node.instruction}</p>}
        <p className="sdk-field-hint">{nodeDependencies(nodes, index).length ? `Waits for: ${nodeDependencies(nodes, index).map(id => nodes.find(step => step.id === id)?.title ?? id).join(', ')}` : 'Starts with your goal; no dependencies.'}</p>
        <WorkflowCheckRequirements acceptance={node.acceptance} checks={checks}/>
        {node.inputs && <div role="group" aria-label={`Proposed inputs for ${node.title}`}><WorkflowProfileInputs node={{...node,id:`proposal-${node.id}`}} index={index} check={checks?.find(check => check.id === node.acceptance)} disabled onChange={()=>{}}/></div>}
      </li>)}</ol></>}
      <WorkflowValidationStatus validation={validation}/>
      <p>Review the source inputs and business rules above. You can edit them after choosing these steps. File steps create files in managed folders. Attached extra checks may add prerequisites or require approval; they are optional and can be changed in the editor.</p>
      <p>These are proposed steps. Research uses live sources; independent reviews use a separate model conversation. Nothing has been saved or started.</p>
      {usable && <button type="button" className="btn" disabled={disabled} onClick={() => { onUse(proposal.workflow!); plan.reset(); }}>{count ? `Replace ${count} ${count === 1 ? 'step' : 'steps'} with these steps${proposal.unsupported.length ? ' anyway' : ''}` : proposal.unsupported.length ? 'Use these steps anyway' : 'Use these steps'}</button>}
      <button type="button" className="btn btn--ghost" onClick={() => plan.reset()}>Dismiss proposal</button>
    </div>}
  </section>;
}
