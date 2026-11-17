import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { api } from '../api';
import type { SDKWorkflow, WorkflowCheck } from '../api';
import {TableInput} from './RecipeSlots';
import {PipelineMeasures} from './WorkflowPipelineInputs';
import {tableUnanswered} from '../slotTable';
import {templateTitle} from '../workflowSummary';

export function WorkflowTemplates({ initialId, project, disabled, hasSteps, onUse, onPreparing, checks }: {
  checks?: WorkflowCheck[]; initialId?: string; project: string; disabled: boolean; hasSteps: boolean;
  /**
   * The prepared definition, the title it came from, and the values typed here.
   * The values are THIS RUN's, not part of the definition: the prepared
   * instructions keep their `{slot:x}` tokens, so saving the result yields a
   * workflow that asks the same questions again next time.
   */
  onUse: (workflow: SDKWorkflow, prepared: { title: string; slots: Record<string, string> }) => void;
  onPreparing: (busy: boolean) => void;
}) {
  const templates = useQuery({ queryKey: ['templates'], queryFn: api.templates });
  const [id, setId] = useState(initialId ?? '');
  const [slots, setSlots] = useState<Record<string, string>>({});
  const choices = templates.data?.filter(t => (t.spec as { kind?: string })?.kind === 'nodes') ?? [];
  const selected = choices.find(t => t.id === id);
  const instantiate = useMutation({ mutationFn: async () => {
    const result = await api.instantiateTemplate(id, { slots, project });
    if (!result.workflow) throw new Error('Template did not return an editable workflow');
    return { workflow: result.workflow, slots: result.slots ?? slots };
  }, onMutate: () => onPreparing(true), onSuccess: ({ workflow, slots: accepted }) => { onUse(workflow, { title: selected ? templateTitle(selected) : workflow.name, slots: accepted }); }, onSettled: () => onPreparing(false) });
  const ready = selected && (selected.contextSlots ?? []).every(s => !s.required || (s.type === 'table' ? !tableUnanswered(s.columns??[],slots[s.id]??'') : slots[s.id]?.trim()));
  return <details className="panel sdk-start-form" open={Boolean(initialId)}>
    <summary>Start from a workflow template</summary>
    {templates.error ? <p role="alert">Could not load templates. <button className="btn" onClick={() => void templates.refetch()}>Retry</button></p>
      : templates.isPending ? <p>Loading templates…</p> : choices.length === 0 ? <p>No SDK templates are installed. You can build your steps below.</p> : <fieldset disabled={disabled || instantiate.isPending}>
        <label htmlFor="sdk-template">Workflow template</label>
        <select id="sdk-template" className="input" value={id} onChange={event => { setId(event.target.value); setSlots({}); instantiate.reset(); }}>
          <option value="">Choose a template</option>{choices.map(t => <option key={t.id} value={t.id} title={t.name}>{templateTitle(t)}</option>)}
        </select>
        {selected ? <p className="sdk-field-hint">{selected.description || 'This template has no description.'}</p> : <p className="sdk-field-hint">Each template is a reviewed set of steps. Choosing one shows what it does and what it asks for; nothing is prepared until you use it.</p>}
        {selected && <>{selected.contextSlots?.map(slot => <div key={slot.id}>
          <label htmlFor={`template-${slot.id}`}>{slot.label}{slot.required ? '' : ' (optional)'}</label>
          {slot.type === 'table' && slot.id === 'measures' && slot.columns?.map(column=>column.id).join(',') === 'measure,definition,unit,tolerance' ? <PipelineMeasures id={`template-${slot.id}`} columns={checks?.find(check=>check.id==='data-pipeline-profile')?.inputTables?.measures.columns ?? slot.columns ?? []} maxRows={checks?.find(check=>check.id==='data-pipeline-profile')?.inputTables?.measures.maxRows} value={slots[slot.id]??''} disabled={disabled || instantiate.isPending} onChange={value=>{setSlots({...slots,[slot.id]:value});instantiate.reset();}}/> : slot.type === 'table' ? <TableInput id={`template-${slot.id}`} columns={slot.columns??[]} value={slots[slot.id]??''} disabled={disabled || instantiate.isPending} required={Boolean(slot.required)} onChange={value=>{setSlots({...slots,[slot.id]:value});instantiate.reset();}}/> : <textarea id={`template-${slot.id}`} className="input" rows={2} value={slots[slot.id] ?? ''} maxLength={4000} placeholder={slot.example}
            onChange={event => { setSlots({ ...slots, [slot.id]: event.target.value }); instantiate.reset(); }} />}
          <p className="sdk-field-hint">{slot.hint}</p>
        </div>)}<p>Review and edit the prepared steps before saving or running. Preparing them makes no model calls. What you type here becomes this run's values; the workflow keeps asking for them, so you can save it and run it again with different answers.</p>
          <button type="button" className="btn" disabled={!ready} onClick={() => instantiate.mutate()}>{instantiate.isPending ? 'Preparing…' : hasSteps ? 'Replace current setup with template' : 'Use template steps'}</button>
        </>}
      </fieldset>}
    {instantiate.error && <p role="alert">{instantiate.error.message}</p>}
  </details>;
}
