import type { ContextSlot, SDKWorkflowNode } from './api';

/**
 * workflowInputs — the run-time inputs a saved workflow asks for each run.
 *
 * A saved workflow used to freeze every changing fact (this week's dates, this
 * month's export path, this run's competitor list) inside instruction prose, so
 * "run it again next week" meant finding and rewriting that prose in N places.
 * A workflow now DECLARES its inputs, and the instructions reference them by
 * `{slot:<id>}` token — the same token syntax templates already use, filled by
 * store.FillSlots on the server at launch.
 *
 * Everything here is a projection for the editor: the server validates tokens
 * on save and on run, and the SDK validator stays the single declaration
 * validator. Nothing in this module fills a token — client-side filling would
 * make the launched definition differ from the saved one.
 */

/**
 * Input ids are the substitution key; the server holds them to this shape.
 * Capitals are part of it because shipped templates already declare slots like
 * `keyColumns` and `competitorsHint`, and a saved workflow prepared from one of
 * those templates keeps their ids.
 */
export const INPUT_ID = /^[a-zA-Z][a-zA-Z0-9_-]{0,63}$/;
const TOKEN = /\{slot:([a-zA-Z0-9_-]{1,64})\}/g;

/** A readable id proposed from a label; editable, never forced on a person. */
export function inputIdFromLabel(label: string): string {
  const words = label.replace(/[^a-zA-Z0-9]+/g, ' ').trim().split(' ').filter(Boolean);
  const id = words.map((word, index) => index === 0
    ? word[0].toLowerCase() + word.slice(1)
    : word[0].toUpperCase() + word.slice(1)).join('').slice(0, 64);
  return INPUT_ID.test(id) ? id : id ? `input${id[0].toUpperCase()}${id.slice(1)}`.slice(0, 64) : '';
}

/** Every `{slot:id}` token in one string, in first-seen order, deduplicated. */
export function textTokens(text: string): string[] {
  return [...new Set(Array.from(text.matchAll(TOKEN), match => match[1]))];
}

/** Every string of a step a token may appear in (CONTRACT: token syntax). */
function nodeText(node: SDKWorkflowNode): string[] {
  return [node.instruction, ...Object.values(node.inputs ?? {}), ...(node.access?.readPaths ?? [])];
}

/** Every token the definition references, wherever it appears. */
export function definitionTokens(instruction: string, nodes: SDKWorkflowNode[]): string[] {
  return [...new Set([instruction, ...nodes.flatMap(nodeText)].flatMap(textTokens))];
}

/** Tokens naming an input this workflow never declares — the server's 400. */
export function unknownInputs(inputs: ContextSlot[], instruction: string, nodes: SDKWorkflowNode[]): string[] {
  const declared = new Set(inputs.map(input => input.id));
  return definitionTokens(instruction, nodes).filter(id => !declared.has(id));
}

/** Declared inputs one step (or the goal) actually uses, for its footer line. */
export function usedInputs(inputs: ContextSlot[], text: string): ContextSlot[] {
  const used = new Set(textTokens(text));
  return inputs.filter(input => used.has(input.id));
}

/** Declared but never referenced: allowed (FillSlots appends a context block). */
export function unusedInputs(inputs: ContextSlot[], instruction: string, nodes: SDKWorkflowNode[]): ContextSlot[] {
  const referenced = new Set(definitionTokens(instruction, nodes));
  return inputs.filter(input => !referenced.has(input.id));
}

/** What stops this declaration from being saved, in the person's words. */
export function inputIssue(input: ContextSlot, others: ContextSlot[]): string | undefined {
  if (!input.label.trim()) return 'Give this input a label.';
  if (!input.id) return 'Give this input a short id.';
  if (!INPUT_ID.test(input.id)) return 'An id starts with a letter and uses only letters, digits, hyphens and underscores.';
  if (others.some(other => other !== input && other.id === input.id)) return `Another input already uses the id ${input.id}.`;
  if (input.type === 'table' && !(input.columns ?? []).length) return 'A table input needs at least one column.';
  return undefined;
}

/** Required inputs with no value — the server's `missingSlots`, checked early. */
export function missingInputs(inputs: ContextSlot[], values: Record<string, string>, answered: (input: ContextSlot, value: string) => boolean): string[] {
  return inputs.filter(input => input.required && !answered(input, values[input.id] ?? '')).map(input => input.id);
}

// Remembering last run's values is a convenience, never a source of truth: a
// blocked or absent store must not stop a launch, so both sides are guarded.
//
// The key is the SAVED workflow id and nothing else. An unsaved draft and a
// definition just prepared from a template both have the id "", and "" is not
// an identity — it is every one of them at once. Keying anything on it is how
// "Week to cover" and "31 August – 6 September 2026" appeared in a form
// prepared from a different template (S-17): values one definition remembered
// were read back by an unrelated one. Both sides refuse "" instead.
const storeKey = (workflowId: string) => `hyperplane.workflowInputs.${workflowId}`;

export function rememberedInputs(workflowId: string): Record<string, string> {
  if (!workflowId) return {};
  try {
    const raw = window.localStorage.getItem(storeKey(workflowId));
    const parsed: unknown = raw ? JSON.parse(raw) : null;
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
    return Object.fromEntries(Object.entries(parsed as Record<string, unknown>).filter(([, value]) => typeof value === 'string')) as Record<string, string>;
  } catch { return {}; }
}

export function rememberInputs(workflowId: string, values: Record<string, string>): void {
  if (!workflowId) return;
  try { window.localStorage.setItem(storeKey(workflowId), JSON.stringify(values)); } catch { /* Private windows and full stores are not launch failures. */ }
}

/**
 * The run-time values an editor should hold after loading a definition.
 *
 * `provided` is what the caller KNOWS this definition's values are — a previous
 * run's inputs handed over for a re-run, or the answers just typed into a
 * template. When there is such a set, it wins outright and is not merged with
 * anything: preparing a template replaces the form, so an answer to the
 * previous template's question cannot survive into the new one, not even for a
 * field the new template happens to name the same way.
 *
 * With no provided set, a SAVED workflow gets what it remembered last time and
 * an unsaved one ("") gets nothing.
 */
export function initialInputValues(workflowId: string, provided?: Record<string, string>): Record<string, string> {
  return provided ?? rememberedInputs(workflowId);
}
