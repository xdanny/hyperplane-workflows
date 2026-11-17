import { test, expect } from '@playwright/test';
import {
  addRow,
  isEmpty,
  parseRows,
  removeRow,
  serializeRows,
  setCell,
  syncEdit,
  tableEdit,
  tableUnanswered,
  type TableColumn,
} from '../src/slotTable';

/**
 * The table-slot row state, tested without a DOM.
 *
 * The bug these pin: "+ Add a row" appended a row and then re-derived the rows
 * from the serialized value, which drops all-blank rows — so the row vanished
 * in the same tick it was created and the button did nothing. Every assertion
 * below is about the one distinction that fixes it: a blank row is a real
 * EDITING state and is not a stored value.
 */

const columns: TableColumn[] = [
  { id: 'measure', label: 'Measure' },
  { id: 'tolerance', label: 'Tolerance' },
  { id: 'unit', label: 'Unit', options: ['%', 'absolute'] },
];

test('an empty slot already has one editable row, and stores nothing', () => {
  const edit = tableEdit('');
  expect(edit.rows).toHaveLength(1);
  expect(edit.rows[0]).toEqual({});
  expect(edit.value).toBe('');
  expect(isEmpty(edit)).toBe(true);
});

test('+ Add a row adds a row that survives the value round trip', () => {
  const first = tableEdit('');
  const second = addRow(columns, first);
  expect(second.rows).toHaveLength(2);
  // The stored value is still empty — two blank rows are not an answer — which
  // is exactly why the rows may not be re-derived from it.
  expect(second.value).toBe('');
  expect(syncEdit(second, second.value).rows).toHaveLength(2);
  expect(addRow(columns, second).rows).toHaveLength(3);
});

test('a half-typed row keeps its blank cells while it is being edited', () => {
  const edit = setCell(columns, addRow(columns, tableEdit('')), 1, 'measure', 'gross revenue');
  expect(edit.rows).toHaveLength(2);
  expect(edit.rows[1]).toEqual({ measure: 'gross revenue' });
  // Stored: only the cell that was typed, and only the row that has one.
  expect(edit.value).toBe('[{"measure":"gross revenue"}]');
  // The parent echoes the value back; the blank first row must not disappear.
  const echoed = syncEdit(edit, edit.value);
  expect(echoed.rows).toHaveLength(2);
  expect(echoed.rows[0]).toEqual({});
});

test('cells serialize in column order with blanks dropped', () => {
  const edit = tableEdit('');
  const typed = setCell(
    columns,
    setCell(columns, setCell(columns, edit, 0, 'unit', '%'), 0, 'measure', 'net revenue'),
    0,
    'tolerance',
    '  0.5  ',
  );
  expect(typed.value).toBe('[{"measure":"net revenue","tolerance":"0.5","unit":"%"}]');
  expect(isEmpty(typed)).toBe(false);
  // …and reading it back gives the same rows.
  expect(tableEdit(typed.value).rows).toEqual([
    { measure: 'net revenue', tolerance: '0.5', unit: '%' },
  ]);
});

test('emptying every cell empties the slot again, without losing the row', () => {
  const one = setCell(columns, tableEdit(''), 0, 'measure', 'orders');
  const cleared = setCell(columns, one, 0, 'measure', '');
  expect(cleared.rows).toHaveLength(1);
  expect(cleared.value).toBe('');
  expect(isEmpty(cleared)).toBe(true);
});

test('removing the last row leaves an empty row, not an empty box', () => {
  const edit = tableEdit('[{"measure":"orders"}]');
  const gone = removeRow(columns, edit, 0);
  expect(gone.rows).toEqual([{}]);
  expect(gone.value).toBe('');
});

test('a value from somewhere else replaces the rows', () => {
  const mine = setCell(columns, tableEdit(''), 0, 'measure', 'mine');
  // A different project's declarations arrive: not this state's own echo.
  const theirs = syncEdit(mine, '[{"measure":"declared"}]');
  expect(theirs.rows).toEqual([{ measure: 'declared' }]);
  expect(theirs.value).toBe('[{"measure":"declared"}]');
  // Cleared entirely (the project has nothing to declare) → back to one row.
  expect(syncEdit(theirs, '').rows).toEqual([{}]);
});

test('the carried text form is editable as a grid again', () => {
  const rows = parseRows('1. measure: orders | tolerance: 0.5 | unit: %\nmeasure: refunds | unit: absolute');
  expect(rows).toEqual([
    { measure: 'orders', tolerance: '0.5', unit: '%' },
    { measure: 'refunds', unit: 'absolute' },
  ]);
  expect(serializeRows(columns, rows)).toBe(
    '[{"measure":"orders","tolerance":"0.5","unit":"%"},{"measure":"refunds","unit":"absolute"}]',
  );
});

test('a slot with no columns keeps whatever it was given', () => {
  expect(serializeRows([], [{ anything: 'kept' }])).toBe('[{"anything":"kept"}]');
  expect(serializeRows([], [{}])).toBe('');
});

/**
 * Readiness (M-17). A table with rows is not a table that is filled in: the
 * inherited features row arrived with its required Type and Why cells blank,
 * the form said "ready", and the recipe's own inputs gate refused the run one
 * screen later. These pin the Run button to the same rule the server applies.
 */
const required: TableColumn[] = [
  { id: 'feature', label: 'Feature', required: true },
  { id: 'type', label: 'Type', required: true },
  { id: 'whyItMatters', label: 'Why it matters', required: true },
  { id: 'owner', label: 'Set by' },
];

test('a row with every required cell filled in answers the slot', () => {
  const value = JSON.stringify([
    { feature: 'payment_type', type: 'categorical', whyItMatters: 'the card/cash split' },
  ]);
  expect(tableUnanswered(required, value)).toBe(false);
});

test('a row with a required cell empty leaves the slot unanswered', () => {
  const value = JSON.stringify([{ feature: 'PULocationID', owner: 'Dev' }]);
  expect(tableUnanswered(required, value)).toBe(true);
});

test('an optional cell left blank is not a gap', () => {
  const value = JSON.stringify([
    { feature: 'trip_distance', type: 'numeric', whyItMatters: 'the fare model' },
  ]);
  expect(tableUnanswered(required, value)).toBe(false);
});

test('an empty table, and a table of nothing but blank rows, are unanswered', () => {
  expect(tableUnanswered(required, '')).toBe(true);
  expect(tableUnanswered(required, '[]')).toBe(true);
});

test('the carried text form is judged by the same rule', () => {
  expect(tableUnanswered(required, '1. feature: payment_type | owner: Dev')).toBe(true);
  expect(
    tableUnanswered(
      required,
      '1. feature: payment_type | type: categorical | whyItMatters: the card/cash split',
    ),
  ).toBe(false);
});
