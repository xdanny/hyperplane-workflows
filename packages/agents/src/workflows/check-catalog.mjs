import { readFileSync } from 'node:fs';

// This private leaf module avoids a cycle between catalog readers and adapters.
// Instructions are data shared with the Go API and editor, not hidden prompts.
const catalog = JSON.parse(readFileSync(new URL('../../checks.json', import.meta.url), 'utf8'));
const validID = value => typeof value === 'string' && /^[a-z][a-z0-9-]{0,63}$/.test(value);
const strings = value => Array.isArray(value) && value.every(validID) && new Set(value).size === value.length;
const fail = () => { throw new Error('Invalid SDK check catalog'); };
// The check contract a person reads: what the step must produce, what the host
// measures for itself, what a pass establishes and what it leaves open. It used
// to be one unreadable paragraph carrying the boundary in its middle (D-06).
// Short sentences, bounded so no entry can grow back into that paragraph.
const CONTRACT_FIELDS = ['produces', 'recomputes', 'proves', 'doesNotProve'];
const contractList = value => Array.isArray(value) && value.length >= 1 && value.length <= 8 &&
  value.every(item => typeof item === 'string' && item.trim() && item.length <= 200);
if (catalog.version !== 1 || Object.keys(catalog).some(k => !['version','checks'].includes(k)) || !Array.isArray(catalog.checks)) fail();
const definitions = new Map();
for (const check of catalog.checks) {
  if (!check || Object.keys(check).some(k => !['id','title','summary','description',...CONTRACT_FIELDS,'inputs','requires','approvalAfter','inputTables'].includes(k)) ||
      !validID(check.id) || definitions.has(check.id) || typeof check.title !== 'string' || !check.title.trim() ||
      typeof check.description !== 'string' || !check.description.trim()) fail();
  // The summary is the short intro a reader meets first; the description stays
  // the prescriptive contract the SDK hands the agent.
  if (typeof check.summary !== 'string' || !check.summary.trim() || check.summary.length > 900 || /[\r\n]/.test(check.summary)) fail();
  // Every entry carries all four lists, and what a pass does NOT prove is never
  // an empty list: a check with no stated boundary is the failure being fixed.
  if (CONTRACT_FIELDS.some(field => !contractList(check[field])) || !check.doesNotProve.length) fail();
  if (check.inputs !== undefined) {
    if (!check.inputs || typeof check.inputs !== 'object' || Array.isArray(check.inputs)) fail();
    const {required, optional} = check.inputs;
    const names = [...(required ?? []), ...(optional ?? [])];
    if (Object.keys(check.inputs).some(k=>!['required','optional'].includes(k)) || !Array.isArray(required) || !Array.isArray(optional) ||
        names.some(k=>typeof k!=='string'||!/^[a-z][a-zA-Z0-9]*$/.test(k)) || new Set(names).size!==names.length) fail();
  }
  if (check.inputTables !== undefined) {
    if (!check.inputTables || typeof check.inputTables !== 'object' || Array.isArray(check.inputTables) || !Object.keys(check.inputTables).length) fail();
    const inputs = [...(check.inputs?.required ?? []), ...(check.inputs?.optional ?? [])];
    for (const [key, table] of Object.entries(check.inputTables)) {
      if (!inputs.includes(key) || !table || Object.keys(table).some(k=>!['label','minRows','maxRows','uniqueBy','columns'].includes(k)) ||
          typeof table.label !== 'string' || !table.label.trim() || !Number.isSafeInteger(table.minRows) || table.minRows < 0 ||
          !Number.isSafeInteger(table.maxRows) || table.maxRows < Math.max(1,table.minRows) || table.maxRows > 64 ||
          !Array.isArray(table.columns) || !table.columns.length || table.columns.length > 32) fail();
      const ids = new Set();
      for (const column of table.columns) {
        if (!column || Object.keys(column).some(k=>!['id','label','hint','required','options','format','default'].includes(k)) ||
            typeof column.id !== 'string' || !/^[a-z][a-zA-Z0-9]*$/.test(column.id) || ids.has(column.id) ||
            typeof column.label !== 'string' || !column.label.trim() ||
            (column.required !== undefined && typeof column.required !== 'boolean') ||
            (column.hint !== undefined && typeof column.hint !== 'string') ||
            (column.format !== undefined && !['decimal','nonnegative-number'].includes(column.format)) ||
            (column.options !== undefined && (!Array.isArray(column.options) || !column.options.length || column.options.some(v=>typeof v!=='string'||!v) || new Set(column.options).size !== column.options.length)) ||
            (column.default !== undefined && (typeof column.default !== 'string' || !column.default || (column.options && !column.options.includes(column.default))))) fail();
        ids.add(column.id);
      }
      if (!ids.has(table.uniqueBy)) fail();
    }
  }
  if (check.requires !== undefined && !strings(check.requires)) fail();
  if (check.approvalAfter !== undefined && !check.requires?.includes(check.approvalAfter)) fail();
  definitions.set(check.id, check);
}
for (const check of catalog.checks) {
  if ((check.requires ?? []).some(id=>id===check.id || !definitions.has(id))) fail();
  // Catalog order is not execution order; recursively reject prerequisite cycles.
  const visit = (id, active) => {
    if (active.has(id)) fail();
    for (const dependency of definitions.get(id).requires ?? []) visit(dependency, new Set([...active,id]));
  };
  visit(check.id,new Set());
}
function freeze(value) {
  for (const nested of Object.values(value)) if (nested && typeof nested === 'object') freeze(nested);
  return Object.freeze(value);
}
freeze(catalog);

export function listChecks() { return catalog.checks; }
export function getCheck(id) {
  const check = definitions.get(id);
  if (!check) throw new Error(`Unknown SDK check: ${String(id)}`);
  return check;
}
