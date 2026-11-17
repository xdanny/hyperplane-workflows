import type {SDKWorkflowNode,WorkflowCheck} from '../api';
import {TableInput} from './RecipeSlots';

export function WorkflowReconciliationInputs({node,index,disabled,onChange,check}:{node:SDKWorkflowNode;index:number;disabled:boolean;onChange:(node:SDKWorkflowNode)=>void;check?:WorkflowCheck}){
 const update=(key:string,value:string)=>onChange({...node,inputs:{...node.inputs,[key]:value}});
 return <div className="workflow-reconciliation-inputs workflow-shift-inputs">
  {([['sourceA','source A'],['sourceB','source B'],['sharedGrain','shared grain'],['keyColumns','key columns']] as const).map(([key,label])=><div key={key}><label htmlFor={`${node.id}-${key}`}>Step {index+1} {label}</label><input id={`${node.id}-${key}`} className="input" disabled={disabled} value={node.inputs?.[key]??''} onChange={e=>update(key,e.target.value)}/></div>)}
  <h3>Measures and definitions</h3><p>Name the number and what each source includes. Scroll each table sideways to see every field.</p>
  <TableInput id={`${node.id}-measures`} columns={check?.inputTables?.measures.columns ?? []} maxRows={check?.inputTables?.measures.maxRows} value={node.inputs?.measures??''} disabled={disabled} required onChange={v=>update('measures',v)}/>
  <h3>Your tolerances</h3><p>Set how much unexplained difference is acceptable. Percentages use the absolute A or B total. Per-segment limits need their own unit; if blank, each segment uses the measure’s limit against its local total.</p>
  <TableInput id={`${node.id}-tolerances`} columns={check?.inputTables?.tolerances.columns ?? []} maxRows={check?.inputTables?.tolerances.maxRows} value={node.inputs?.tolerances??''} disabled={disabled} required onChange={v=>update('tolerances',v)}/>
  <h3>Known differences</h3><p>Optional. Name a filter that explains why sources differ. A difference without an executable predicate stays unexplained.</p>
  <TableInput id={`${node.id}-differences`} columns={check?.inputTables?.knownDifferences.columns ?? []} maxRows={check?.inputTables?.knownDifferences.maxRows} value={node.inputs?.knownDifferences??''} disabled={disabled} required={false} onChange={v=>update('knownDifferences',v)}/>
 </div>;
}
