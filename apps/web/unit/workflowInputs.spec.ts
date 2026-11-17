import { test, expect } from '@playwright/test';
import type { ContextSlot, SDKWorkflowNode } from '../src/api';
import { definitionTokens, initialInputValues, inputIdFromLabel, inputIssue, missingInputs, rememberInputs, rememberedInputs, textTokens, unknownInputs, unusedInputs, usedInputs } from '../src/workflowInputs';

const slot = (id: string, extra: Partial<ContextSlot> = {}): ContextSlot => ({ id, label: id, required: true, ...extra });
const step = (id: string, instruction: string, extra: Partial<SDKWorkflowNode> = {}): SDKWorkflowNode => ({ id, title: id, instruction, ...extra });

test('an id is proposed from the label and stays within the server’s shape', () => {
  expect(inputIdFromLabel('Week to cover')).toBe('weekToCover');
  expect(inputIdFromLabel('  Competitors (list) ')).toBe('competitorsList');
  expect(inputIdFromLabel('Key columns')).toBe('keyColumns');
  // An id has to start with a letter, so a leading digit is prefixed, not dropped.
  expect(inputIdFromLabel('2026 month')).toBe('input2026Month');
  expect(inputIdFromLabel('!!!')).toBe('');
  expect(inputIdFromLabel('W'.repeat(80)).length).toBeLessThanOrEqual(64);
});

test('tokens are found wherever the server substitutes them, and only there', () => {
  expect(textTokens('Cover {slot:week} for {slot:competitors} and {slot:week} again')).toEqual(['week', 'competitors']);
  expect(textTokens('An unfinished { slot:week } or {slot:} is not a token')).toEqual([]);
  const nodes = [
    step('research', 'Research {slot:competitors} during {slot:week}'),
    // A token in a declared check input or a readable path is an unfilled
    // binding the server fills at launch, so it counts as a reference too.
    step('profile', 'Measure the export', { inputs: { dataset: '{slot:exportPath}' }, access: { hosts: [], readPaths: ['{slot:exportPath}'] } }),
  ];
  expect(definitionTokens('Write the {slot:audience} brief', nodes)).toEqual(['audience', 'competitors', 'week', 'exportPath']);
});

test('a token naming no declared input is the refusal; a declared-but-unused input is not', () => {
  const inputs = [slot('week'), slot('audience')];
  const nodes = [step('research', 'Cover {slot:week} for {slot:competitors}')];
  expect(unknownInputs(inputs, 'Write a brief', nodes)).toEqual(['competitors']);
  expect(unknownInputs([...inputs, slot('competitors')], 'Write a brief', nodes)).toEqual([]);
  expect(unusedInputs(inputs, 'Write a brief', nodes).map(input => input.id)).toEqual(['audience']);
  expect(usedInputs(inputs, 'Cover {slot:week}').map(input => input.id)).toEqual(['week']);
});

test('a declaration is refused for a blank label, a malformed id or a duplicate id', () => {
  const inputs = [slot('week'), slot('week')];
  expect(inputIssue({ id: 'week', label: '' }, [])).toBe('Give this input a label.');
  expect(inputIssue({ id: '', label: 'Week' }, [])).toBe('Give this input a short id.');
  expect(inputIssue({ id: '9week', label: 'Week' }, [])).toContain('starts with a letter');
  expect(inputIssue({ id: 'keyColumns', label: 'Key columns' }, [])).toBeUndefined();
  expect(inputIssue(inputs[0], inputs)).toBe('Another input already uses the id week.');
  expect(inputIssue({ id: 'rows', label: 'Rows', type: 'table' }, [])).toBe('A table input needs at least one column.');
  expect(inputIssue({ id: 'week', label: 'Week' }, [{ id: 'audience', label: 'Audience' }])).toBeUndefined();
});

test('required inputs block a launch; optional and answered ones do not', () => {
  const inputs = [slot('week'), slot('audience', { required: false }), slot('rows', { type: 'table', columns: [{ id: 'name', label: 'Name', required: true }] })];
  const answered = (input: ContextSlot, value: string) => input.type === 'table' ? value.includes('name') : value.trim().length > 0;
  expect(missingInputs(inputs, {}, answered)).toEqual(['week', 'rows']);
  expect(missingInputs(inputs, { week: '31 Aug', rows: '[{"name":"Linear"}]' }, answered)).toEqual([]);
  expect(missingInputs(inputs, { week: '   ' }, answered)).toEqual(['week', 'rows']);
});

test('remembered values are a convenience: no store is not a launch failure', () => {
  expect(rememberedInputs('')).toEqual({});
  expect(rememberedInputs('saved-workflow')).toEqual({});
});


// ── Remembered run-time values (S-17) ─────────────────────────────────────────
// Sofia found "Week to cover" and "31 August – 6 September 2026" — a previous
// template's question and answer — sitting in a form prepared from a DIFFERENT
// template. Remembered values are keyed by the SAVED workflow id, and an
// unsaved or template-prepared definition has the id "", which is not an
// identity: it is every draft at once. These pin that "" is refused on both
// sides, and that preparing a template replaces the form rather than merging
// into it.

/** A localStorage stand-in, so the guards are tested rather than the browser. */
function fakeStore(): { store: Record<string, string>; reads: string[]; writes: string[] } {
  const store: Record<string, string> = {};
  const reads: string[] = [];
  const writes: string[] = [];
  (globalThis as { window?: unknown }).window = {
    localStorage: {
      getItem: (key: string) => { reads.push(key); return store[key] ?? null; },
      setItem: (key: string, value: string) => { writes.push(key); store[key] = value; },
    },
  };
  return { store, reads, writes };
}

test('an unsaved or template-prepared definition never reads or writes remembered values', () => {
  const { reads, writes } = fakeStore();
  expect(rememberedInputs('')).toEqual({});
  rememberInputs('', { weekOf: '31 August – 6 September 2026' });
  // Not "wrote nothing useful" — touched the store at all. A key built from ""
  // is a key every other draft would read back.
  expect(reads).toEqual([]);
  expect(writes).toEqual([]);
});

test('a saved workflow remembers its own values under its own id', () => {
  const { store, writes } = fakeStore();
  rememberInputs('wf-kpi', { weekOf: '31 Aug', audience: 'new hires' });
  expect(writes).toEqual(['hyperplane.workflowInputs.wf-kpi']);
  expect(rememberedInputs('wf-kpi')).toEqual({ weekOf: '31 Aug', audience: 'new hires' });
  // Another workflow's id is another workflow's values.
  expect(rememberedInputs('wf-competitors')).toEqual({});
  // Non-string cells are dropped rather than rendered into an input.
  store['hyperplane.workflowInputs.wf-odd'] = JSON.stringify({ a: 'one', b: 7, c: null });
  expect(rememberedInputs('wf-odd')).toEqual({ a: 'one' });
  store['hyperplane.workflowInputs.wf-bad'] = 'not json';
  expect(rememberedInputs('wf-bad')).toEqual({});
});

test('preparing a template clears the previous template’s values', () => {
  fakeStore();
  rememberInputs('wf-kpi', { weekOf: '31 August – 6 September 2026' });
  // Preparing hands over the values typed into THIS template — an empty set
  // when it asks nothing — and that set wins outright. Merging is what let a
  // previous template's answer survive into the next form.
  expect(initialInputValues('', {})).toEqual({});
  expect(initialInputValues('', { dataset: 'trips.csv' })).toEqual({ dataset: 'trips.csv' });
  // The prepared definition is not saved, so there is nothing to fall back to.
  expect(initialInputValues('')).toEqual({});
  // A saved workflow reopened with no handed-over values still gets its own.
  expect(initialInputValues('wf-kpi')).toEqual({ weekOf: '31 August – 6 September 2026' });
  // …and handed-over values (a re-run's inputs) replace them rather than merge.
  expect(initialInputValues('wf-kpi', { audience: 'support' })).toEqual({ audience: 'support' });
});
