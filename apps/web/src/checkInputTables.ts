import type { SlotColumn } from './api';

export interface CheckInputColumn extends SlotColumn {
  format?: 'decimal' | 'nonnegative-number';
  default?: string;
}
export interface CheckInputTable {
  label: string;
  minRows: number;
  maxRows: number;
  uniqueBy: string;
  columns: CheckInputColumn[];
}

// The editor consumes data from the catalog, never an SDK runtime import.
export function validInputTables(tables: unknown, inputs: string[]): tables is Record<string, CheckInputTable> {
  if (!tables || typeof tables !== 'object' || Array.isArray(tables)) return false;
  return Object.entries(tables).length > 0 && Object.entries(tables).every(([key, value]) => {
    const table = value as CheckInputTable;
    if (!inputs.includes(key) || !table || Object.keys(table).some(key=>!['label','minRows','maxRows','uniqueBy','columns'].includes(key)) || typeof table.label !== 'string' || !table.label.trim() ||
      !Number.isInteger(table.minRows) || table.minRows < 0 || !Number.isInteger(table.maxRows) || table.maxRows < Math.max(1, table.minRows) || table.maxRows > 64 ||
      !Array.isArray(table.columns) || !table.columns.length || table.columns.length > 32) return false;
    const ids = table.columns.map(column => column?.id);
    return new Set(ids).size === ids.length && ids.includes(table.uniqueBy) && table.columns.every(column =>
      column && Object.keys(column).every(key=>['id','label','hint','required','options','format','default'].includes(key)) && typeof column.id === 'string' && /^[a-z][a-zA-Z0-9]*$/.test(column.id) && typeof column.label === 'string' && Boolean(column.label.trim()) &&
      (column.required === undefined || typeof column.required === 'boolean') && (column.hint === undefined || typeof column.hint === 'string') &&
      (column.format === undefined || ['decimal', 'nonnegative-number'].includes(column.format)) &&
      (column.options === undefined || (Array.isArray(column.options) && column.options.length > 0 && column.options.every(option => typeof option === 'string' && Boolean(option)) && new Set(column.options).size === column.options.length)) &&
      (column.default === undefined || (typeof column.default === 'string' && Boolean(column.default) && (!column.options || column.options.includes(column.default)))));
  });
}
