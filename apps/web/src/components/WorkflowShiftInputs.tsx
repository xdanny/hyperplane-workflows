import type { SDKWorkflowNode, WorkflowCheck } from '../api';
import { TableInput } from './RecipeSlots';

export function WorkflowShiftInputs({node,index,disabled,onChange,check}:{node:SDKWorkflowNode;index:number;disabled:boolean;onChange:(node:SDKWorkflowNode)=>void;check?:WorkflowCheck}){
  const update=(key:string,value:string)=>onChange({...node,inputs:{...node.inputs,[key]:value}});
  return <div className="workflow-shift-inputs">
    {([['baselineData','baseline window'],['currentData','current window'],['keyColumns','key columns']] as const).map(([key,label])=><div key={key}>
      <label htmlFor={`${node.id}-${key}`}>Step {index+1} {label}</label>
      <input id={`${node.id}-${key}`} className="input" disabled={disabled} value={node.inputs?.[key]??''} onChange={e=>update(key,e.target.value)} placeholder={key==='keyColumns'?'id (or none, candidate columns)':'Absolute file path or table reference'}/>
    </div>)}
    <h3>Features and your tolerances</h3><p>Each row sets the movement that matters for that feature. These numbers are reviewed before the comparison runs. Scroll the table sideways to see every field.</p>
    <TableInput id={`${node.id}-features`} columns={check?.inputTables?.features.columns ?? []} maxRows={check?.inputTables?.features.maxRows} value={node.inputs?.features??''} disabled={disabled} required onChange={value=>update('features',value)}/>
    <details><summary>Controls that test the detector</summary><p>Choose a multiple of a feature’s tolerance. Below 1 must stay stable; 1 or above must be detected. Blank uses 1.25 for categorical features (or the first feature), attributed to the platform. A zero tolerance cannot size a multiplicative control. Controls use synthetic weights to preserve small movements.</p>
      <TableInput id={`${node.id}-controls`} columns={check?.inputTables?.seededControl.columns ?? []} maxRows={check?.inputTables?.seededControl.maxRows} value={node.inputs?.seededControl??''} disabled={disabled} required={false} onChange={value=>update('seededControl',value)}/>
    </details>
  </div>;
}
