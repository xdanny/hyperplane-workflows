import type { ContextSlot } from '../api';
import { TableInput } from './RecipeSlots';
import { tableUnanswered } from '../slotTable';

/** A value counts as given when it is non-blank — a table, when its required cells are. */
export function inputAnswered(input: ContextSlot, value: string): boolean {
  return input.type === 'table' ? !tableUnanswered(input.columns ?? [], value) : value.trim().length > 0;
}

/**
 * WorkflowRunInputs — "Fill inputs for this run".
 *
 * The values are this run's, not the workflow's: they are sent with the launch
 * and the server fills the saved definition's tokens. Nothing here edits the
 * saved workflow, so running last week's brief for a new week changes no
 * instruction text and leaves the workflow reusable.
 */
export function WorkflowRunInputs({ inputs, values, disabled, missing, onChange }: {
  inputs: ContextSlot[];
  values: Record<string, string>;
  disabled: boolean;
  /** Ids the launch was refused for, from the server's `missingSlots`. */
  missing: string[];
  onChange: (values: Record<string, string>) => void;
}) {
  return <section className="workflow-run-inputs" aria-labelledby="run-inputs-title">
    <h2 id="run-inputs-title">Fill inputs for this run</h2>
    <p className="sdk-field-hint">These values are used for this run only. The saved workflow keeps its inputs, so the next run can use different values.</p>
    {inputs.map(input => {
      const value = values[input.id] ?? '';
      const wanted = missing.includes(input.id);
      return <div key={input.id}>
        <label htmlFor={`run-input-${input.id}`}>{input.label || input.id}{input.required ? '' : ' (optional)'}</label>
        {input.type === 'table'
          ? <TableInput id={`run-input-${input.id}`} columns={input.columns ?? []} value={value} disabled={disabled} required={Boolean(input.required)}
              example={input.example} onChange={next => onChange({ ...values, [input.id]: next })} />
          : <textarea id={`run-input-${input.id}`} className="input" rows={2} maxLength={4000} value={value} disabled={disabled} required={Boolean(input.required)}
              placeholder={input.example} aria-describedby={`run-input-hint-${input.id}`}
              onChange={event => onChange({ ...values, [input.id]: event.target.value })} />}
        <p id={`run-input-hint-${input.id}`} className="sdk-field-hint">{input.hint}</p>
        {wanted && !inputAnswered(input, value) && <p role="alert">{input.label || input.id} is required before this workflow can run.</p>}
      </div>;
    })}
  </section>;
}
