import { test, expect } from '@playwright/test';
import type { SDKWorkflowNode } from '../src/api';
import { stepKindSummary, templateTitle } from '../src/workflowSummary';

const step = (id: string, kind?: SDKWorkflowNode['kind']): SDKWorkflowNode => ({ id, title: id, instruction: id, ...(kind ? { kind } : {}) });

test('a saved card names the kinds of step it holds, not "Documents"', () => {
  expect(stepKindSummary([step('a', 'research'), step('b', 'research'), step('c'), step('d', 'check')]))
    .toBe('2 research steps · 1 document · 1 independent review');
  expect(stepKindSummary([step('a', 'task'), step('b', 'approval')])).toBe('1 file step · 1 approval');
  expect(stepKindSummary([])).toBe('');
});

test('a template is listed by its title, never by its slug', () => {
  expect(templateTitle({ name: 'market-research' })).toBe('Market research');
  expect(templateTitle({ name: 'data_quality_audit' })).toBe('Data quality audit');
  expect(templateTitle({ name: 'ml-engineering', displayName: 'ML baseline and evaluation' })).toBe('ML baseline and evaluation');
  expect(templateTitle({ name: 'ml-engineering', displayName: '   ' })).toBe('ML engineering');
});

test('an acronym in a slug is not sentence-cased into a word', () => {
  // S-17 / D-12: the picker read "Kpi commentary" and "Ml engineering".
  expect(templateTitle({ name: 'kpi-commentary' })).toBe('KPI commentary');
  expect(templateTitle({ name: 'ml-engineering' })).toBe('ML engineering');
  expect(templateTitle({ name: 'market-research' })).toBe('Market research');
  // A chosen display name still wins over any derivation.
  expect(templateTitle({ name: 'kpi-commentary', displayName: 'Monthly KPI commentary' }))
    .toBe('Monthly KPI commentary');
});
