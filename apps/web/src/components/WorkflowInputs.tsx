import type { ContextSlot, SDKWorkflowNode } from '../api';
import { inputIdFromLabel, inputIssue, unusedInputs } from '../workflowInputs';
import './WorkflowInputs.css';

/**
 * WorkflowInputs — "Inputs this workflow asks for each run".
 *
 * The declaration half of a repeatable workflow. Every fact that changes
 * between runs — the week, the file, the competitor list — is declared here
 * once and referenced from the goal and the steps by its `{slot:id}` token,
 * instead of being frozen into instruction prose that has to be found and
 * rewritten every week.
 *
 * Declaring an input never fills it and never starts anything: the values are
 * asked for at run time, and the server does the substitution at launch.
 */
export function WorkflowInputs({ inputs, nodes, instruction, disabled, unknown, onChange }: {
  inputs: ContextSlot[];
  /** The steps, for the "declared but never referenced" note. */
  nodes: SDKWorkflowNode[];
  instruction: string;
  disabled: boolean;
  /** Tokens that name no declared input — from the server, or checked locally. */
  unknown: string[];
  onChange: (inputs: ContextSlot[]) => void;
}) {
  const replace = (index: number, next: ContextSlot) => onChange(inputs.map((input, at) => at === index ? next : input));
  const unused = unusedInputs(inputs, instruction, nodes);
  return <section className="workflow-inputs" aria-labelledby="workflow-inputs-title">
    <div className="workflow-inputs-intro">
      <h2 id="workflow-inputs-title">Inputs this workflow asks for each run</h2>
      <p>Declare what changes between runs. You are asked for these values when you start a run, and the workflow fills them in wherever a step references them. Declaring an input starts nothing and calls no model.</p>
    </div>
    {inputs.length === 0
      ? <p className="sdk-field-hint">No inputs yet. This workflow runs exactly as written every time. Add an input for anything you would otherwise retype each run — a week, a file path, a list of competitors.</p>
      : <ol className="workflow-input-list">{inputs.map((input, index) => {
        const issue = inputIssue(input, inputs);
        return <li key={index}>
          <div className="workflow-input-row">
            <div><label htmlFor={`input-label-${index}`}>Input {index + 1} label</label>
              <input id={`input-label-${index}`} className="input" maxLength={160} value={input.label} disabled={disabled} placeholder="Week to cover"
                onChange={event => replace(index, { ...input, label: event.target.value, ...(input.id === inputIdFromLabel(input.label) || !input.id ? { id: inputIdFromLabel(event.target.value) } : {}) })} /></div>
            <div><label htmlFor={`input-id-${index}`}>Input {index + 1} id</label>
              <input id={`input-id-${index}`} className="input mono" maxLength={64} value={input.id} disabled={disabled} aria-describedby={`input-token-${index}`}
                onChange={event => replace(index, { ...input, id: event.target.value })} /></div>
          </div>
          <p id={`input-token-${index}`} className="sdk-field-hint">Reference this input from the goal or any step as <code className="workflow-token">{`{slot:${input.id || 'id'}}`}</code>. The id is suggested from the label until you change it.</p>
          <label htmlFor={`input-hint-${index}`}>Input {index + 1} hint</label>
          <input id={`input-hint-${index}`} className="input" maxLength={400} value={input.hint ?? ''} disabled={disabled} placeholder="The Monday–Sunday week the brief should cover"
            onChange={event => replace(index, { ...input, hint: event.target.value })} />
          <label htmlFor={`input-example-${index}`}>Input {index + 1} example</label>
          <input id={`input-example-${index}`} className="input" maxLength={400} value={input.example ?? ''} disabled={disabled} placeholder="31 August – 6 September 2026"
            onChange={event => replace(index, { ...input, example: event.target.value })} />
          <label htmlFor={`input-type-${index}`}>Input {index + 1} type</label>
          <select id={`input-type-${index}`} className="input" value={input.type === 'table' ? 'table' : 'text'} disabled={disabled}
            onChange={event => { const { columns, ...rest } = input; return replace(index, event.target.value === 'table' ? { ...rest, type: 'table', columns: columns?.length ? columns : [{ id: 'item', label: 'Item' }] } : { ...rest, type: 'text' }); }}>
            <option value="text">Text</option>
            <option value="table">Table — one row per thing</option>
          </select>
          {input.type === 'table' && <>
            <label htmlFor={`input-columns-${index}`}>Input {index + 1} columns, one per line</label>
            <textarea id={`input-columns-${index}`} className="input" rows={3} disabled={disabled} placeholder={'Competitor\nWhat to watch'}
              value={(input.columns ?? []).map(column => column.label).join('\n')}
              onChange={event => replace(index, { ...input, columns: event.target.value.split('\n').map(line => line.trim()).filter(Boolean).map(label => ({ id: inputIdFromLabel(label) || 'column', label })) })} />
            <p className="sdk-field-hint">Each line becomes one column of the table you fill in at run time. Every row reaches the workflow as one readable line.</p>
          </>}
          <div className="workflow-input-footer">
            <label className="workflow-input-required"><input type="checkbox" checked={Boolean(input.required)} disabled={disabled}
              onChange={event => replace(index, { ...input, required: event.target.checked })} /> Required for every run</label>
            <button type="button" className="btn btn--ghost" disabled={disabled} aria-label={`Remove input ${index + 1}`}
              onClick={() => onChange(inputs.filter((_, at) => at !== index))}>Remove</button>
          </div>
          {issue && <p role="alert">{issue}</p>}
        </li>;
      })}</ol>}
    <button type="button" className="btn" disabled={disabled || inputs.length >= 20}
      onClick={() => onChange([...inputs, { id: '', label: '', hint: '', example: '', required: true, type: 'text' }])}>Add an input</button>
    {unknown.length > 0 && <p role="alert">{unknown.length === 1 ? `The token {slot:${unknown[0]}} names an input this workflow does not declare.` : `These tokens name inputs this workflow does not declare: ${unknown.map(id => `{slot:${id}}`).join(', ')}.`} Declare {unknown.length === 1 ? 'it' : 'them'} above, or correct the {unknown.length === 1 ? 'token' : 'tokens'}.</p>}
    {inputs.length > 0 && unused.length > 0 && !unknown.length && <p className="sdk-field-hint">{unused.map(input => input.label || input.id).join(', ')} {unused.length === 1 ? 'is' : 'are'} declared but never referenced by a step. The {unused.length === 1 ? 'value is' : 'values are'} still passed to the workflow as context.</p>}
  </section>;
}
