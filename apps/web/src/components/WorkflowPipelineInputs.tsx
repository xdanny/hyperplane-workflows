import {useState} from 'react';
import './WorkflowNodes.css';
import type {SDKWorkflowNode,SlotColumn,WorkflowCheck} from '../api';
import {optionLabel} from '../optionLabels';
import {tableEdit,syncEdit,setCell,addRow,removeRow,type TableEdit} from '../slotTable';

const requiredFields=[
 ['sources','source data','List every source file or table. For files, use an absolute path. Include format and how new data arrives; keep credentials out of this field.'],
 ['targetStore','finished delivery','Name the database engine and the local database or export you need. For example, a SQLite database containing the daily sales table.'],
 ['grain','what one finished row represents','For example, one row per UTC order date. Several source rows may contribute to one finished row.'],
 ['keyColumns','finished-table key columns','The columns that identify a finished row, separated by commas. They can be derived by the pipeline, such as order_day from a timestamp.'],
 ['maxLag','maximum source lag','How far behind the source the finished tables may fall, including the time unit.'],
 ['safeUse','confidentiality and use limits','Which sensitive fields must be excluded or masked, and which decisions these numbers must not support.'],
] as const;
const optionalFields=[
 ['refreshDeadline','refresh deadline and trigger','Optional finish time, timezone, external trigger and responsible owner. This workflow does not install a schedule.'],
 ['lateArrival','late-arriving rows','Optional policy: how late a row may arrive and whether it changes the original period or the period when it arrived.'],
 ['runtimeBudget','build time limit','Optional maximum time for a full build and its checks. Checks must fail on overrun; they cannot sample the data to finish sooner.'],
 ['handover','handover format','Optional format and intended reader for the delivered explanation. This does not send or publish anything.'],
] as const;

export function PipelineMeasures({id,value,disabled,onChange,columns,maxRows}:{id:string;value:string;disabled:boolean;onChange:(value:string)=>void;columns:SlotColumn[];maxRows?:number}){
 const [edit,setEdit]=useState<TableEdit>(()=>tableEdit(value));
 const state=syncEdit(edit,value);if(state!==edit)setEdit(state);
 const commit=(next:TableEdit)=>{setEdit(next);onChange(next.value);};
 const cellId=(index:number,key:string)=>index===0&&key==='measure'?id:`${id}-${index}-${key}`;
 const units=columns.find(column=>column.id==='unit')?.options ?? [];
 return <div className="workflow-pipeline-measures">
  {state.rows.map((row,index)=><fieldset key={index}>
   <legend>Number {index+1}</legend>
   {columns.map(column=><div key={column.id}>
    <label htmlFor={cellId(index,column.id)}>{column.label}</label>
    {column.id==='unit'?<select id={cellId(index,column.id)} className="input" aria-label={`${column.label}, row ${index+1}`} value={row.unit??''} disabled={disabled} onChange={e=>commit(setCell(columns,state,index,column.id,e.target.value))}>
      <option value="">Exact match (default)</option>{units.map(value=><option key={value} value={value}>{optionLabel('unit',value)}</option>)}
     </select>:column.id==='definition'?<textarea id={cellId(index,column.id)} className="input" rows={3} aria-label={`${column.label}, row ${index+1}`} value={row[column.id]??''} disabled={disabled} onChange={e=>commit(setCell(columns,state,index,column.id,e.target.value))}/>:<input id={cellId(index,column.id)} className="input" aria-label={`${column.label}, row ${index+1}`} value={row[column.id]??''} disabled={disabled} onChange={e=>commit(setCell(columns,state,index,column.id,e.target.value))}/>}
   </div>)}
   <button type="button" className="btn" disabled={disabled} onClick={()=>commit(removeRow(columns,state,index))}>Remove number {index+1}</button>
  </fieldset>)}
  <button type="button" className="btn" disabled={disabled||(maxRows!==undefined&&state.rows.length>=maxRows)} onClick={()=>commit(addRow(columns,state))}>Add a number</button>
 </div>;
}

export function WorkflowPipelineInputs({node,index,disabled,onChange,check}:{node:SDKWorkflowNode;index:number;disabled:boolean;onChange:(node:SDKWorkflowNode)=>void;check?:WorkflowCheck}){
 const update=(key:string,value:string)=>onChange({...node,inputs:{...node.inputs,[key]:value}});
 const field=([key,label,hint]:readonly [string,string,string])=><div key={key}>
  <label htmlFor={`${node.id}-${key}`}>Step {index+1} {label}</label>
  <p id={`${node.id}-${key}-hint`}>{hint}</p>
  <textarea id={`${node.id}-${key}`} aria-describedby={`${node.id}-${key}-hint`} className="input" rows={2} disabled={disabled} value={node.inputs?.[key]??''} onChange={e=>update(key,e.target.value)}/>
 </div>;
 return <div className="workflow-shift-inputs">
  {requiredFields.map(field)}
  <h3>Numbers the finished tables must reproduce</h3>
  <p>Define what to add up and which rows to exclude. Each number is compared with a total measured directly from the sources before the pipeline is built. Blank difference fields mean exact equality. A percentage uses the absolute source control total.</p>
  <PipelineMeasures id={`${node.id}-measures`} columns={check?.inputTables?.measures.columns ?? []} maxRows={check?.inputTables?.measures.maxRows} value={node.inputs?.measures??''} disabled={disabled} onChange={value=>update('measures',value)}/>
  <details><summary>Timing and handover options</summary>{optionalFields.map(field)}</details>
 </div>;
}
