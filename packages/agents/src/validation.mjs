import path from 'node:path';
import { dependencyGraph } from './dependencies.mjs';
import { getCheck } from './workflows/check-catalog.mjs';
import { validateCheckInputs } from './workflows/check-inputs.mjs';

const connectionName = /^WORKFLOW_[A-Z0-9_]{1,55}$/;

// A `{slot:<id>}` token is an UNFILLED reusable input, not a value. A saved
// workflow may ask for the source file or the measure table each run, so a
// token stands in for the shape rules a value cannot satisfy before it exists.
// The filled definition is validated again, in full, before a run is admitted.
const slotToken = /\{slot:[^{}]{1,80}\}/;
export const unfilledSlot = value => typeof value === 'string' && slotToken.test(value);

export function validateTaskAccess({ access, toolchain, sideEffect }) {
  if (toolchain !== undefined && typeof toolchain !== 'boolean' || sideEffect !== undefined && typeof sideEffect !== 'boolean') throw new Error('Tool setup permissions must be boolean');
  if (toolchain && (!sideEffect || !access || !(access.hosts ?? []).length)) throw new Error('Tool setup requires an explicit side-effect grant and allowed network hosts');
  if (sideEffect && !toolchain) throw new Error('The side-effect grant is only used for tool setup');
  if (access === undefined) return;
  if (!access || Array.isArray(access) || typeof access !== 'object' || Object.keys(access).some(k => !['hosts', 'readPaths', 'environment'].includes(k))) throw new Error('Unsupported step access setting');
  // Hosts may be empty: reading a local file is not a reason to open the
  // network. Every other host rule is unchanged, and a grant that names
  // nothing at all is still refused.
  const hosts = access.hosts ?? [];
  if (!Array.isArray(hosts) || hosts.length > 32 || hosts.some(h => typeof h !== 'string' || h.length > 253 || !/^[a-z0-9]+(?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9]+(?:[a-z0-9-]*[a-z0-9])?)*$/.test(h) || h.split('.').some(l => l.length > 63)) || new Set(hosts).size !== hosts.length) throw new Error('Name up to 32 distinct lowercase hosts, without schemes, ports or wildcards');
  for (const key of ['readPaths', 'environment']) if (access[key] !== undefined && (!Array.isArray(access[key]) || access[key].length > 32 || new Set(access[key]).size !== access[key].length)) throw new Error(`Invalid ${key} allowlist`);
  if (!hosts.length && !(access.readPaths ?? []).length && !(access.environment ?? []).length) throw new Error('Grant at least one network host, readable input or connection environment name');
  if ((access.readPaths ?? []).some(p => typeof p !== 'string' || p.includes('\0') || /[*?\[\]]/.test(p) || Buffer.byteLength(p) > 1024 || (!unfilledSlot(p) && (!path.isAbsolute(p) || path.normalize(p) !== p || p.endsWith('/'))))) throw new Error('Readable inputs must be specific absolute files or folders');
  if ((access.environment ?? []).some(k => typeof k !== 'string' || !connectionName.test(k))) throw new Error('Connection environment names must start with WORKFLOW_');
}

// Prerequisites bind to their latest occurrence, in the declared order. A new
// measurement invalidates its older plan; a changed plan needs a later approval.
export function validateCheck(node, previous = [], precedes) {
  if (node.acceptance === undefined) {
    if (node.inputs !== undefined) throw new Error('Source inputs require a selected check');
    return;
  }
  if (node.kind !== 'task' || node.check !== undefined) throw new Error('Choose a built-in check or a caller-supplied command');
  const definition = getCheck(node.acceptance);
  const fields = definition.inputs;
  if (fields) {
    const inputs = node.inputs;
    if (!inputs || typeof inputs !== 'object' || Array.isArray(inputs) ||
        Object.keys(inputs).some(k=>![...fields.required,...fields.optional].includes(k)) ||
        fields.required.some(k=>typeof inputs[k]!=='string'||!inputs[k].trim()) ||
        Object.values(inputs).some(v=>typeof v!=='string'||Buffer.byteLength(v)>16000)) throw new Error('Check needs all declared source inputs');
  } else if (node.inputs !== undefined) throw new Error('This check does not accept source inputs');
  const latestMatch = id => {
    const matches = previous.filter(n => n.acceptance === id);
    if (precedes && matches.some((a, index) => matches.slice(index + 1).some(b => !precedes(a, b) && !precedes(b, a)))) throw new Error(`Ambiguous prerequisite ${id}; link its occurrences in dependency order`);
    return matches.at(-1);
  };
  let latest;
  for (const id of definition.requires ?? []) {
    const selected = latestMatch(id);
    if (!selected || latest && (precedes ? !precedes(latest, selected) : previous.indexOf(selected) <= previous.indexOf(latest))) throw new Error(`Run ${id} after the current prerequisite checks before ${definition.id}`);
    latest = selected;
  }
  if (definition.approvalAfter) {
    const selected = latestMatch(definition.approvalAfter);
    if (!selected || !previous.some(n => n.kind === 'approval' && (precedes ? precedes(selected, n) : previous.indexOf(n) > previous.indexOf(selected)))) throw new Error(`Approve ${definition.approvalAfter} before ${definition.id}`);
  }
}

export function validateNodes(instruction, nodes) {
  if (typeof instruction !== 'string' || !instruction.trim() || Buffer.byteLength(instruction) > 16000) throw new Error('Describe the workflow goal (up to 16000 bytes)');
  if (!Array.isArray(nodes) || nodes.length < 1 || nodes.length > 20) throw new Error('Add between 1 and 20 steps');
  const seen = new Set();
  for (const node of nodes) {
    try {
      if (!node || typeof node !== 'object' || Array.isArray(node) || typeof node.id !== 'string' || !/^[a-z][a-z0-9-]{0,63}$/.test(node.id) || seen.has(node.id)) throw new Error('Steps need unique valid ids');
      if (Object.keys(node).some(key => !['id', 'title', 'instruction', 'kind', 'check', 'acceptance', 'inputs', 'access', 'toolchain', 'sideEffect', 'dependsOn'].includes(key))) throw new Error('Unsupported step field');
      seen.add(node.id);
      if (node.kind === 'task') validateTaskAccess(node);
      else if (node.access !== undefined || node.toolchain !== undefined || node.sideEffect !== undefined) throw new Error('Only checked-work steps accept execution permissions');
      if (node.kind !== undefined && !['document', 'approval', 'research', 'check', 'task'].includes(node.kind)) throw new Error('Unsupported step kind');
      // Unfilled inputs carry no values to measure. Their meaning is checked
      // when the workflow runs, against the values a person actually supplied.
      if (node.acceptance !== undefined && getCheck(node.acceptance).inputs && !Object.values(node.inputs ?? {}).some(unfilledSlot)) validateCheckInputs(node.acceptance, node.inputs);
      if (node.check !== undefined) {
        if (node.kind !== 'task') throw new Error('Only task steps accept an acceptance command');
        if (!Array.isArray(node.check) || !node.check.length || node.check.length > 100 || node.check.some(arg => typeof arg !== 'string' || arg.includes('\0') || Buffer.byteLength(arg) > 16000) || !node.check[0].trim()) throw new Error('Acceptance needs a program and literal arguments');
      }
      for (const [key, limit] of [['title', 160], ['instruction', 16000]]) {
        if (typeof node[key] !== 'string' || !node[key].trim() || Buffer.byteLength(node[key]) > limit) throw new Error(`Step ${node.id} needs ${key} (up to ${limit} bytes)`);
      }
    } catch (cause) {
      const message = cause instanceof SyntaxError ? 'Source input tables must contain valid JSON or named fields'
        : cause instanceof TypeError ? 'Step contains an invalid input value or table row'
        : cause?.message || 'Invalid workflow step';
      const error = new Error(message);
      if (typeof node?.id === 'string' && node.id.length <= 64) error.nodeId = node.id;
      throw error;
    }
  }
  const graph = dependencyGraph(nodes);
  for (const [index, node] of nodes.entries()) {
    try {
      const previous = graph.previous(index).map(parent => nodes[parent]);
      if (['approval', 'check'].includes(node.kind) && !previous.length) throw new Error('Add a document before asking for approval or review');
      validateCheck(node, previous, graph.explicit ? graph.precedes : undefined);
    } catch (error) { error.nodeId = node.id; throw error; }
  }
}
