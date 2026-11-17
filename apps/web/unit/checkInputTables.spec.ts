import { readFileSync } from 'node:fs';
import { test, expect } from '@playwright/test';
import { validInputTables } from '../src/checkInputTables';
import type { WorkflowCheck } from '../src/api';
const checks: WorkflowCheck[] = JSON.parse(readFileSync(new URL('../../../packages/agents/checks.json', import.meta.url), 'utf8')).checks;

test('browser rejects malformed table metadata before using an attached check', () => {
  const good=checks.find(check=>check.id==='data-pipeline-profile')!.inputTables!;
  expect(validInputTables(good,['measures'])).toBe(true);
  for(const bad of [null,{}, {measures:{...good.measures,maxRows:100}}, {measures:{...good.measures,uniqueBy:'missing'}}, {measures:{...good.measures,columns:[{id:'measure',label:'Measure',options:[]}]}}]) expect(validInputTables(bad,['measures'])).toBe(false);
});
