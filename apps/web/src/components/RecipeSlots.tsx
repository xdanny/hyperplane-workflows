import { useState } from 'react';
import {PipelineMeasures} from './WorkflowPipelineInputs';
import type { ContextSlot, SlotColumn, SlotPrefill } from '../api';
import { addRow, isEmpty, removeRow, setCell, syncEdit, tableEdit, type TableEdit } from '../slotTable';
import { optionLabel, sideName, type SideNames } from '../optionLabels';

/**
 * RecipeSlots — the context half of a recipe form.
 *
 * Three findings shaped this file, and each one is a rule here:
 *
 *  1. Half of a data recipe's required slots are an engineer's questions with
 *     the owner's name on them. Slots are GROUPED by `audience`: "From you" and
 *     "From whoever runs your data". A person can see at a glance which half
 *     they are blocked on, and "Copy the list for your engineer" hands the
 *     other half on as plain text instead of leaving them to guess.
 *
 *  2. Key columns were folded into a prose "grain and key" box, so a run could
 *     declare a grain with no key at all and pass its own inputs gate. A slot
 *     of `type: "list"` renders as tags here and reaches the process as a
 *     comma-separated string, so the value is typed without the process
 *     instructions changing at all.
 *
 *  3. The same dataset was re-typed into seven slots in one project, and two of
 *     those descriptions disagreed. A slot the project DECLARES arrives
 *     pre-filled and labelled "inherited from project — edit for this run", so
 *     the declaration is the default and the override is visible.
 *
 *  4. One prose box carried N features' tolerances, so one tolerance got
 *     borrowed across features and a human approved the borrowing without
 *     seeing it. A slot of `type: "table"` renders a row per subject with the
 *     unit as its own column; the value is stored as JSON and reaches the
 *     process as one readable line per row, so the instructions did not change.
 */

/** Which audience a slot is addressed to, defaulting to the owner. */
export function slotAudience(slot: ContextSlot): 'owner' | 'engineer' {
  return slot.audience === 'engineer' ? 'engineer' : 'owner';
}

const AUDIENCE_HEADING: Record<'owner' | 'engineer', string> = {
  owner: 'From you',
  engineer: 'From whoever runs your data',
};

const AUDIENCE_HINT: Record<'owner' | 'engineer', string> = {
  // "which windows" is in here because it is in the group: on a shift audit,
  // "From you" holds Baseline window and Current window, and the heading
  // claimed less than the group held.
  owner:
    'What you want, what counts as close enough, which windows, and what these numbers must not be used for.',
  engineer:
    'Where the data lives, what one row means, and the columns that identify it. If you do not know these, copy the list below and ask the person who runs your data — the run cannot start without them.',
};

/**
 * The whole context section: every slot, grouped by who can answer it.
 *
 * `prefill` is what the chosen project declares, keyed by slot id. A slot is
 * marked inherited only while its current value is still exactly the declared
 * one — the moment it is edited the tag goes, because it is no longer the
 * project's answer.
 */
export function RecipeSlots({
  recipeId,
  slots,
  values,
  prefill,
  notes,
  disabled,
  onChange,
}: {
  recipeId: string;
  slots: ContextSlot[];
  values: Record<string, string>;
  prefill: Record<string, SlotPrefill>;
  /**
   * What the project says about the slots it deliberately leaves EMPTY, keyed
   * by slot id. It stands where an inherited value used to: the current window
   * was prefilled with whatever dataset happened to be declared second, tagged
   * "inherited from project", and the form said ready. A question in the
   * reader's own terms is the honest half of that.
   */
  notes?: Record<string, string>;
  disabled: boolean;
  onChange: (id: string, value: string) => void;
}) {
  const groups: ('owner' | 'engineer')[] = ['owner', 'engineer'];
  const bucketed = groups
    .map((g) => ({ audience: g, slots: slots.filter((s) => slotAudience(s) === g) }))
    .filter((b) => b.slots.length > 0);

  // With nothing marked `engineer` there is only one audience, and a single
  // heading over every slot says nothing. The unlabelled list is the honest
  // rendering — market-research is not a form with two halves.
  const grouped = bucketed.length > 1;

  // WHICH DASHBOARD IS A. A reconciliation's tolerance is picked in units of
  // "% of A's total", and nothing on the form said which side A was except the
  // order the two source slots happen to appear in — so a person declaring
  // "0.5% of Finance" had to translate Finance into A themselves and hope. Once
  // the two sources are named, the unit options wear their names.
  const sides: SideNames = { a: sideName(values.sourceA), b: sideName(values.sourceB) };

  return (
    <div className="recipe-slots">
      <div className="eyebrow recipe-section-label">Context — what this recipe needs to know</div>
      {bucketed.map((bucket) => (
        <div key={bucket.audience} className="recipe-slot-group">
          {grouped && (
            <div className="recipe-slot-group-head">
              <span className="eyebrow">{AUDIENCE_HEADING[bucket.audience]}</span>
              <span className="recipe-slot-hint">{AUDIENCE_HINT[bucket.audience]}</span>
              {bucket.audience === 'engineer' && (
                <CopyForEngineer slots={bucket.slots} values={values} />
              )}
            </div>
          )}
          {bucket.slots.map((slot) => (
            <SlotField
              key={slot.id}
              recipeId={recipeId}
              slot={slot}
              value={values[slot.id] ?? ''}
              inherited={prefill[slot.id]}
              note={notes?.[slot.id]}
              sides={sides}
              disabled={disabled}
              onChange={(v) => onChange(slot.id, v)}
            />
          ))}
        </div>
      ))}
    </div>
  );
}

/** One slot: its label, its hint, its provenance, and the right input for its type. */
function SlotField({
  recipeId,
  slot,
  value,
  inherited,
  note,
  sides,
  disabled,
  onChange,
}: {
  recipeId: string;
  slot: ContextSlot;
  value: string;
  inherited?: SlotPrefill;
  note?: string;
  sides?: SideNames;
  disabled: boolean;
  onChange: (value: string) => void;
}) {
  const id = `slot-${recipeId}-${slot.id}`;
  const unfilled = Boolean(slot.required) && !value.trim();
  const stillInherited = Boolean(inherited) && value.trim() === inherited?.value.trim();
  // A table whose unit column is a percentage OF one of the two sides cannot be
  // filled in honestly without knowing which side is which, so this table — and
  // only this table — says so where the choice is made.
  const namesSides =
    slot.type === 'table' &&
    (slot.columns ?? []).some((c) => (c.options ?? []).includes('percent_of_a'));
  const sidesLine =
    namesSides && sides?.a && sides?.b ? `A is ${sides.a}. B is ${sides.b}.` : undefined;

  return (
    <div className="recipe-slot">
      <label className="recipe-slot-label" htmlFor={id}>
        {slot.label || slot.id}
        {slot.required ? (
          <span className="recipe-slot-req" aria-label="required"> ·  required</span>
        ) : (
          <span className="recipe-slot-opt"> · optional</span>
        )}
        {stillInherited && (
          <span className="recipe-slot-inherited" title={`declared at ${inherited?.source}`}>
            {' '}· inherited from project — edit for this run
          </span>
        )}
      </label>
      {slot.hint && <div className="recipe-slot-hint">{slot.hint}</div>}
      {sidesLine && <div className="recipe-slot-sides">{sidesLine}</div>}
      {slot.type === 'table' && slot.id === 'measures' && slot.columns?.map(column=>column.id).join(',') === 'measure,definition,unit,tolerance' ? (
        <PipelineMeasures id={id} columns={slot.columns ?? []} value={value} disabled={disabled} onChange={onChange}/>
      ) : slot.type === 'table' ? (
        <TableInput
          id={id}
          columns={slot.columns ?? []}
          value={value}
          example={slot.example}
          sides={sides}
          disabled={disabled}
          required={Boolean(slot.required)}
          onChange={onChange}
        />
      ) : slot.type === 'list' ? (
        <ListInput
          id={id}
          value={value}
          placeholder={slot.example ?? ''}
          disabled={disabled}
          required={Boolean(slot.required)}
          onChange={onChange}
        />
      ) : (
        <textarea
          id={id}
          className={`input recipe-slot-input${unfilled ? ' recipe-slot-input--empty' : ''}`}
          rows={2}
          placeholder={slot.example ?? ''}
          value={value}
          disabled={disabled}
          aria-required={slot.required ? true : undefined}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
      {/* Only while the slot is still empty: once the person answers it, the
          question has been answered and repeating it is noise. */}
      {note && !value.trim() && <div className="recipe-slot-note">{note}</div>}
    </div>
  );
}

/**
 * A list slot: typed as tags, stored and sent as a comma-separated string.
 *
 * The string IS the value — the process reads it exactly as it read the old
 * free-text box — so nothing downstream had to change to make the slot typed.
 * The literal `none` is a member like any other and is deliberately not treated
 * as an empty value: "none, VendorID, pickup_time" is the owner declaring there
 * is no natural key and naming the closest candidate, which is a real answer.
 */
function ListInput({
  id,
  value,
  placeholder,
  disabled,
  required,
  onChange,
}: {
  id: string;
  value: string;
  placeholder: string;
  disabled: boolean;
  required: boolean;
  onChange: (value: string) => void;
}) {
  const [draft, setDraft] = useState('');
  const items = splitList(value);
  const commit = (raw: string) => {
    const added = splitList(raw);
    if (added.length === 0) return;
    onChange([...items, ...added].join(', '));
    setDraft('');
  };

  return (
    <div className={`recipe-slot-list${required && items.length === 0 ? ' recipe-slot-input--empty' : ''}`}>
      {items.length > 0 && (
        <ul className="recipe-slot-tags">
          {items.map((item, i) => (
            <li key={`${item}-${i}`} className="chip recipe-slot-tag">
              <span className="mono">{item}</span>
              <button
                type="button"
                className="recipe-slot-tag-x"
                aria-label={`Remove ${item}`}
                disabled={disabled}
                onClick={() => onChange(items.filter((_, n) => n !== i).join(', '))}
              >
                ×
              </button>
            </li>
          ))}
        </ul>
      )}
      <input
        id={id}
        className="input recipe-slot-input mono"
        value={draft}
        placeholder={items.length > 0 ? 'add another…' : placeholder}
        disabled={disabled}
        aria-required={required ? true : undefined}
        onChange={(e) => {
          // A pasted or typed comma ends a value, which is what makes a pasted
          // CSV work without a separate "paste" affordance.
          if (/[,;\n]/.test(e.target.value)) {
            commit(e.target.value);
            return;
          }
          setDraft(e.target.value);
        }}
        onBlur={() => commit(draft)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault();
            commit(draft);
          }
          if (e.key === 'Backspace' && draft === '' && items.length > 0) {
            onChange(items.slice(0, -1).join(', '));
          }
        }}
      />
    </div>
  );
}

/**
 * A table slot: rows typed in a grid, stored as canonical JSON.
 *
 * The shape exists because three reviewers independently reported the same
 * defect from three directions — one textarea carrying N subjects' tolerances,
 * so a subject with no tolerance of its own quietly borrowed another's and the
 * approval screen showed the borrowing as a fact. A row per subject, with the
 * unit as its own column, is the smallest change that makes the missing
 * tolerance visible before the run starts rather than after it is approved.
 *
 * Two rules, both learned the hard way:
 *
 *  1. THE ROWS ARE STATE, not a projection of the stored value. An all-blank
 *     row serializes to nothing (deliberately: an empty row must not make a
 *     required slot look answered), so rows derived from the value could never
 *     hold one — "+ Add a row" appended a row and dropped it in the same tick,
 *     and the recipe could not be filled in from the form at all. The two
 *     halves live in `../slotTable`; this component only renders them.
 *
 *  2. THE HEADER IS ALWAYS THERE, with one empty row ready to type into. The
 *     column labels and their hints are how a person learns what the table
 *     wants, so they are readable before anything is committed, not after.
 */
export function TableInput({
  id,
  columns,
  value,
  example,
  sides,
  disabled,
  required,
  onChange,
  maxRows,
}: {
  id: string;
  columns: SlotColumn[];
  maxRows?: number;
  value: string;
  example?: string;
  sides?: SideNames;
  disabled: boolean;
  required: boolean;
  onChange: (value: string) => void;
}) {
  const [edit, setEdit] = useState<TableEdit>(() => tableEdit(value));
  // A value that is NOT this state's own echo came from outside — a different
  // project's declarations, a reset — and replaces the rows. Its own echo must
  // not, or every blank row and half-typed cell would vanish on each keystroke.
  const state = syncEdit(edit, value);
  if (state !== edit) setEdit(state);
  const commit = (next: TableEdit) => {
    setEdit(next);
    onChange(next.value);
  };

  // A table with no columns cannot be rendered as one. It is a declaration bug
  // (the seed lint refuses it), and showing the raw value is the honest
  // fallback — better than an empty grid that silently eats what is typed.
  if (columns.length === 0) {
    return (
      <textarea
        id={id}
        className="input recipe-slot-input"
        rows={3}
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }

  return (
    <div className={`recipe-slot-table${required && isEmpty(state) ? ' recipe-slot-input--empty' : ''}`}>
      <div className="recipe-slot-table-scroll">
        <table className="recipe-slot-grid">
          <thead>
            <tr>
              {columns.map((c) => (
                <th key={c.id} scope="col">
                  <span className="recipe-slot-col-label">
                    {c.label}
                    {c.required && <span className="recipe-slot-req" aria-hidden="true"> ·</span>}
                  </span>
                  {c.hint && <span className="recipe-slot-col-hint">{c.hint}</span>}
                </th>
              ))}
              <th scope="col" className="recipe-slot-grid-action">
                <span className="visually-hidden">Remove row</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {state.rows.map((row, i) => (
              <tr key={i}>
                {columns.map((c, col) => (
                  <td key={c.id}>
                    {c.options && c.options.length > 0 ? (
                      <select
                        className="input recipe-slot-cell"
                        aria-label={`${c.label}, row ${i + 1}`}
                        value={row[c.id] ?? ''}
                        disabled={disabled}
                        onChange={(e) => commit(setCell(columns, state, i, c.id, e.target.value))}
                      >
                        <option value="">—</option>
                        {/* The token is the VALUE — a gate matches on it and it
                            has to survive a copy edit — and the label is what a
                            person reads. This select offered the token as both. */}
                        {c.options.map((o) => (
                          <option key={o} value={o}>{optionLabel(c.id, o, sides)}</option>
                        ))}
                      </select>
                    ) : (
                      <input
                        /* The first cell of the first row carries the slot's id,
                           so the label above the table focuses the box a person
                           is meant to type in first. */
                        id={i === 0 && col === 0 ? id : undefined}
                        className="input recipe-slot-cell"
                        aria-label={`${c.label}, row ${i + 1}`}
                        value={row[c.id] ?? ''}
                        disabled={disabled}
                        onChange={(e) => commit(setCell(columns, state, i, c.id, e.target.value))}
                      />
                    )}
                  </td>
                ))}
                <td className="recipe-slot-grid-action">
                  <button
                    type="button"
                    className="btn btn--ghost btn--sm"
                    disabled={disabled}
                    aria-label={`Remove row ${i + 1}`}
                    onClick={() => commit(removeRow(columns, state, i))}
                  >
                    ×
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="recipe-slot-table-foot">
        <button
          type="button"
          className="btn btn--sm"
          disabled={disabled || (maxRows !== undefined && state.rows.length >= maxRows)}
          onClick={() => commit(addRow(columns, state))}
        >
          + Add a row
        </button>
        {/* The example is one filled-in row, collapsed: it answers "what goes in
            here" for the person who wants it, without pushing the boxes they
            came to type in off the screen. */}
        {example && (
          <details className="recipe-slot-example">
            <summary>See an example row</summary>
            <span className="recipe-slot-example-body">{example}</span>
          </details>
        )}
      </div>
    </div>
  );
}

/**
 * "Copy the list for your engineer" — the slots this person cannot answer,
 * with their labels and hints, as plain text they can paste into a message.
 *
 * The list is the FORM's own words, not a paraphrase: whatever the engineer
 * sends back can be pasted straight into the boxes it names.
 */
function CopyForEngineer({
  slots,
  values,
}: {
  slots: ContextSlot[];
  values: Record<string, string>;
}) {
  const [copied, setCopied] = useState(false);
  const text = slots
    .map((s) => {
      const answered = (values[s.id] ?? '').trim();
      const head = `${s.label || s.id}${s.required ? ' (required)' : ' (optional)'}`;
      const body = [s.hint, s.example ? `Example: ${s.example}` : '', answered ? `Current answer: ${answered}` : '']
        .filter(Boolean)
        .join('\n  ');
      return `- ${head}\n  ${body}`;
    })
    .join('\n\n');

  const copy = () => {
    const payload =
      'To run this workflow I need these from you — each one is a box on the form, so your answer can be pasted straight in:\n\n' +
      text;
    void copyText(payload).then((ok) => {
      if (!ok) return;
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    });
  };

  return (
    <button type="button" className="btn btn--sm" onClick={copy}>
      {copied ? 'Copied' : 'Copy the list for your engineer'}
    </button>
  );
}

/**
 * Put text on the clipboard, and say whether it actually landed.
 *
 * `navigator.clipboard.writeText` rejects in ordinary situations — the document
 * not focused, a permissions policy — and the button was left saying nothing at
 * all, so a person pressing it could not tell whether they had the list. The
 * selection path is the older way and still works where the async API refuses;
 * "Copied" is shown only when something was really copied.
 */
async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Fall through: a rejection here is normal, not exceptional.
  }
  try {
    const box = document.createElement('textarea');
    box.value = text;
    box.setAttribute('readonly', '');
    box.style.position = 'fixed';
    box.style.top = '-1000px';
    box.style.opacity = '0';
    document.body.appendChild(box);
    box.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(box);
    return ok;
  } catch {
    return false;
  }
}

/** "a, b , c" → ["a","b","c"]. Empty members dropped, order kept. */
function splitList(raw: string): string[] {
  return raw
    .split(/[,;\n]/)
    .map((v) => v.trim())
    .filter((v) => v !== '');
}
