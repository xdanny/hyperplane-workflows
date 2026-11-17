import type { SDKWorkflowNode, WorkflowCheck } from '../api';
import { WorkflowPipelineInputs } from './WorkflowPipelineInputs';
import { WorkflowPipelineChangeInputs } from './WorkflowPipelineChangeInputs';
import { WorkflowReconciliationInputs } from './WorkflowReconciliationInputs';
import { WorkflowShiftInputs } from './WorkflowShiftInputs';

// Proposals and the editor show the same typed source declarations.
export function WorkflowProfileInputs(props: {node: SDKWorkflowNode; index: number; disabled: boolean; onChange: (node: SDKWorkflowNode) => void; check?: WorkflowCheck}) {
  const {node,index,disabled,onChange}=props;
  if(node.acceptance==='data-pipeline-profile')return <WorkflowPipelineInputs {...props}/>;
  if(node.acceptance==='pipeline-change-profile')return <WorkflowPipelineChangeInputs {...props}/>;
  if(node.acceptance==='reconciliation-profile')return <WorkflowReconciliationInputs {...props}/>;
  if(node.acceptance==='dataset-shift-profile')return <WorkflowShiftInputs {...props}/>;
  if(node.acceptance!=='data-quality-profile') {
    const keys = [...(props.check?.inputs?.required ?? []), ...(props.check?.inputs?.optional ?? [])];
    return keys.length ? <div className="workflow-shift-inputs">
      <p id={`${node.id}-check-input-hint`} className="sdk-field-hint">These inputs belong to the optional extra check. Keep the declared source and success criteria consistent with your goal and step instructions. See Extra check requirements for the expected format. Naming a source does not grant access to it.</p>
      {keys.map(key => <div key={key}>
      <label htmlFor={`${node.id}-${key}`}>Step {index+1} {key.replace(/([a-z])([A-Z])/g, '$1 $2').toLowerCase()}{props.check?.inputs?.optional?.includes(key) ? ' (optional)' : ''}</label>
      <textarea id={`${node.id}-${key}`} className="input" rows={3} aria-required={props.check?.inputs?.required?.includes(key)} aria-describedby={`${node.id}-check-input-hint`} value={node.inputs?.[key] ?? ''} disabled={disabled} onChange={event => onChange({...node, inputs:{...node.inputs, [key]:event.target.value}})}/>
    </div>)}</div> : null;
  }
  return <div className="workflow-shift-inputs">
    <label htmlFor={`dataset-${node.id}`}>Step {index+1} source data</label>
    <input id={`dataset-${node.id}`} className="input" value={node.inputs?.dataset??''} disabled={disabled} placeholder="Absolute file path or table reference" onChange={e=>onChange({...node,inputs:{...node.inputs,dataset:e.target.value}})}/>
    <label htmlFor={`keys-${node.id}`}>Step {index+1} key columns</label>
    <input id={`keys-${node.id}`} className="input" value={node.inputs?.keyColumns??''} disabled={disabled} placeholder="id, date (or none, followed by candidates)" onChange={e=>onChange({...node,inputs:{...node.inputs,keyColumns:e.target.value}})}/>
  </div>;
}
