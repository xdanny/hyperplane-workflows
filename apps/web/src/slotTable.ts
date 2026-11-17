/**
 * slotTable — the row state behind a `type: "table"` context slot.
 *
 * This module exists because of one defect. "+ Add a row" used to append a
 * blank row and immediately serialize the rows back to the stored value, and an
 * all-blank row serializes to NOTHING (that is the point: an empty row must not
 * make a required slot look answered). The rows were derived from that value on
 * every render, so the new row was dropped the instant it was created and the
 * button did nothing at all — the reconciliation recipe could not be filled in
 * from the form by anyone.
 *
 * So the editable rows and the stored value are two different things, and this
 * module owns the difference:
 *
 *   - `rows`  — what is on screen, blank cells and blank rows included, because
 *               a half-typed row is a normal state of a form.
 *   - `value` — what is stored and sent, canonical JSON with blank cells
 *               dropped and all-blank rows dropped entirely. Byte-for-byte what
 *               the server canonicalises to (internal/store.SlotTableJSON), so
 *               an untouched inherited table still compares equal to the
 *               project's declaration and keeps its "inherited" tag.
 *
 * Every transition below returns a whole new state with both halves in step,
 * which is what makes the component a thin renderer of it — and what makes the
 * behaviour testable without a DOM.
 */

/** One row: cell values by column id. Blank cells are kept while editing. */
export type TableRow = Record<string, string>;

/**
 * The columns a table slot declares. Structurally the useful half of
 * `SlotColumn` in api.ts — declared here so the state machine (and its test)
 * does not depend on the API module.
 */
export interface TableColumn {
  id: string;
  label?: string;
  hint?: string;
  options?: string[];
  required?: boolean;
}

/** The editable state of one table slot. `value` is always the serialization
 *  of `rows`, except immediately after `tableEdit` adopted a value that was not
 *  canonical (a carried text form), where it is the value as it was given. */
export interface TableEdit {
  rows: TableRow[];
  value: string;
}

/** The stored value → editable state, with one empty row ready to type into.
 *
 *  There is ALWAYS a row, so the column headers and their hints are readable
 *  before anything is committed. The empty row serializes to nothing, so the
 *  slot stays empty — and a required slot stays unfilled — until a cell is
 *  actually typed. */
export function tableEdit(value: string): TableEdit {
  const rows = parseRows(value);
  return { rows: rows.length > 0 ? rows : [{}], value };
}

/** Append an empty row. The stored value does not change — an empty row is not
 *  an answer — which is exactly why the rows cannot be derived from it. */
export function addRow(columns: TableColumn[], edit: TableEdit): TableEdit {
  return withRows(columns, [...edit.rows, {}]);
}

/** Type into one cell. */
export function setCell(
  columns: TableColumn[],
  edit: TableEdit,
  index: number,
  column: string,
  cell: string,
): TableEdit {
  return withRows(
    columns,
    edit.rows.map((r, i) => (i === index ? { ...r, [column]: cell } : r)),
  );
}

/** Remove one row. Removing the last one leaves an empty row rather than an
 *  empty box: the grid is the slot's shape, not a thing that appears later. */
export function removeRow(columns: TableColumn[], edit: TableEdit, index: number): TableEdit {
  return withRows(
    columns,
    edit.rows.filter((_, i) => i !== index),
  );
}

/**
 * Adopt a value that changed OUTSIDE this state — a different project's
 * declarations, a recipe reset, a slot cleared.
 *
 * A value equal to the one these rows serialize to is this state's own echo
 * coming back through the parent, and must NOT re-derive the rows: that would
 * throw away every blank row and half-typed cell on each keystroke.
 */
export function syncEdit(edit: TableEdit, value: string): TableEdit {
  return value === edit.value ? edit : tableEdit(value);
}

/** True when nothing has been typed yet — what a required-but-empty slot is. */
export function isEmpty(edit: TableEdit): boolean {
  return edit.value.trim() === '';
}

/** The stored value → rows. Accepts the carried text form too, so a value that
 *  has been through a run and back is still editable as a grid. */
export function parseRows(raw: string): TableRow[] {
  const v = raw.trim();
  if (v === '') return [];
  if (v.startsWith('[')) {
    try {
      const parsed: unknown = JSON.parse(v);
      if (!Array.isArray(parsed)) return [];
      return parsed
        .filter((r): r is Record<string, unknown> => Boolean(r) && typeof r === 'object' && !Array.isArray(r))
        .map((r) => {
          const row: TableRow = {};
          for (const [k, cell] of Object.entries(r)) {
            if (cell === null || cell === undefined) continue;
            row[k] = typeof cell === 'string' ? cell : String(cell);
          }
          return row;
        });
    } catch {
      return [];
    }
  }
  return v
    .split('\n')
    .map((line) => line.trim().replace(/^\d+\.\s*/, ''))
    .filter(Boolean)
    .map((line) => {
      const row: TableRow = {};
      for (const field of line.split('|')) {
        const i = field.indexOf(':');
        if (i <= 0) continue;
        const k = field.slice(0, i).trim();
        const cell = field.slice(i + 1).trim();
        if (k && cell) row[k] = cell;
      }
      return row;
    })
    .filter((row) => Object.keys(row).length > 0);
}

/** Rows → the canonical stored value: cells in COLUMN order, blanks dropped,
 *  every cell on one line. Byte-for-byte what the server canonicalises to, so
 *  an untouched inherited table keeps its "inherited from project" tag. */
export function serializeRows(columns: TableColumn[], rows: TableRow[]): string {
  const clean = (v: string) => v.replace(/\|/g, '/').split(/\s+/).filter(Boolean).join(' ');
  const out: TableRow[] = [];
  for (const row of rows) {
    const cells: TableRow = {};
    for (const c of columns) {
      const v = clean(row[c.id] ?? '');
      if (v !== '') cells[c.id] = v;
    }
    for (const k of Object.keys(row).sort()) {
      if (columns.some((c) => c.id === k)) continue;
      const v = clean(row[k] ?? '');
      if (v !== '') cells[k] = v;
    }
    if (Object.keys(cells).length > 0) out.push(cells);
  }
  return out.length === 0 ? '' : JSON.stringify(out);
}

/** Both halves in step: the rows as they are (never fewer than one), and the
 *  value they serialize to. */
function withRows(columns: TableColumn[], rows: TableRow[]): TableEdit {
  const kept = rows.length > 0 ? rows : [{}];
  return { rows: kept, value: serializeRows(columns, kept) };
}

/**
 * Whether a required table slot is still UNANSWERED — the readiness rule, and
 * the twin of store.MissingSlots so the Run button and the server agree.
 *
 * A table with rows is not a table that is filled in. An inherited features row
 * arrived with its required Type and Why cells blank, the form counted the slot
 * as answered and said "ready", and the recipe's own inputs gate refused the
 * run one screen later. A required cell is required here too.
 */
export function tableUnanswered(columns: TableColumn[], raw: string): boolean {
  if (raw.trim() === '') return true;
  const rows = parseRows(raw);
  if (rows.length === 0) return true;
  const required = columns.filter((c) => c.required);
  return rows.some((row) => required.some((c) => (row[c.id] ?? '').trim() === ''));
}
