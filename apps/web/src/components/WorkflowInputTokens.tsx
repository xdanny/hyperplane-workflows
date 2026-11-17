import { useRef } from 'react';
import type { ContextSlot } from '../api';
import { usedInputs } from '../workflowInputs';

/**
 * Insert an input's `{slot:id}` token where the cursor is.
 *
 * Typing `{slot:competitors}` by hand is how a token ends up naming an input
 * that does not exist, so the editor writes it: the menu lists what the
 * workflow declares, and picking one splices the token into the instruction at
 * the caret without disturbing the rest of the text.
 */
export function WorkflowInputInsert({ inputs, target, describes, disabled, onInsert }: {
  inputs: ContextSlot[];
  /** The id of the textarea the token goes into. */
  target: string;
  /** What this menu writes into, for its accessible name. */
  describes: string;
  disabled: boolean;
  onInsert: (next: string) => void;
}) {
  const select = useRef<HTMLSelectElement>(null);
  if (!inputs.length) return null;
  const insert = (id: string) => {
    const field = document.getElementById(target) as HTMLTextAreaElement | null;
    if (!field) return;
    const token = `{slot:${id}}`;
    const start = field.selectionStart ?? field.value.length, end = field.selectionEnd ?? start;
    onInsert(field.value.slice(0, start) + token + field.value.slice(end));
    requestAnimationFrame(() => {
      field.focus();
      field.setSelectionRange(start + token.length, start + token.length);
    });
  };
  return <select ref={select} className="input workflow-input-insert" value="" disabled={disabled} aria-label={`Insert an input into ${describes}`}
    onChange={event => { const id = event.target.value; if (select.current) select.current.value = ''; if (id) insert(id); }}>
    <option value="">Insert input…</option>
    {inputs.filter(input => input.id).map(input => <option key={input.id} value={input.id}>{input.label || input.id}</option>)}
  </select>;
}

/** "Uses inputs: week, competitors" — what this text will have filled in. */
export function WorkflowUsesInputs({ inputs, text }: { inputs: ContextSlot[]; text: string }) {
  const used = usedInputs(inputs, text);
  if (!used.length) return null;
  return <span className="workflow-uses-inputs">Uses inputs: {used.map(input => <code key={input.id} className="workflow-token">{input.label || input.id}</code>)}</span>;
}
