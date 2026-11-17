import type {SDKWorkflowNode,WorkflowCheck} from '../api';
import {TableInput} from './RecipeSlots';
const fields=[['pipelineLocation','original pipeline','Where the existing code and input data live. Use absolute file paths or explicit source references.'],['runCommand','baseline run command','The existing command that runs the pipeline. It runs through SDK tools on a copy, before any changes.'],['changeRequest','requested change','What is wrong or missing today, and what must be true afterwards.'],['grain','what one row represents','For example, one row per order date and store.'],['keyColumns','key columns','Columns that identify a row. If no natural key exists, write none followed by the closest candidate columns.'],['safeUse','confidentiality and use limits','Sensitive fields to exclude and decisions these outputs must not support.']] as const;
export function WorkflowPipelineChangeInputs({node,index,disabled,onChange,check}:{node:SDKWorkflowNode;index:number;disabled:boolean;onChange:(node:SDKWorkflowNode)=>void;check?:WorkflowCheck}){
 const update=(key:string,value:string)=>onChange({...node,inputs:{...node.inputs,[key]:value}});
 return <div className="workflow-shift-inputs">
 {fields.map(([key,label,hint])=><div key={key}><label htmlFor={`${node.id}-${key}`}>Step {index+1} {label}</label><p id={`${node.id}-${key}-hint`}>{hint}</p><textarea id={`${node.id}-${key}`} aria-describedby={`${node.id}-${key}-hint`} className="input" rows={2} disabled={disabled} value={node.inputs?.[key]??''} onChange={e=>update(key,e.target.value)}/></div>)}
 <h3>What must not change</h3><p>Give each protected number or behavior its own tolerance. A percentage uses that measure’s absolute baseline value. Scroll sideways to see every field.</p>
 <TableInput id={`${node.id}-invariants`} columns={check?.inputTables?.invariants.columns ?? []} maxRows={check?.inputTables?.invariants.maxRows} value={node.inputs?.invariants??''} disabled={disabled} required onChange={v=>update('invariants',v)}/>
 </div>;
}
