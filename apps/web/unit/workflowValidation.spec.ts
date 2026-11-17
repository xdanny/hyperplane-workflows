import { test, expect } from '@playwright/test';
import type { SDKWorkflowNode } from '../src/api';
import { nodeTextIssue, readWorkflowValidation } from '../src/workflowValidation';

test('local text feedback preserves UTF-8 limits without interpreting workflow rules', () => {
  const make = (title: string, instruction = 'Create a note'): SDKWorkflowNode => ({ id: 'note', kind: 'document', title, instruction });
  for (const title of ['a'.repeat(160), 'é'.repeat(80), '😀'.repeat(40)]) expect(nodeTextIssue(make(title))).toBeUndefined();
  for (const title of ['a'.repeat(161), 'é'.repeat(81), '😀'.repeat(40) + 'a']) {
    const node = make(title);
    expect(nodeTextIssue(node)).toContain('160-byte limit');
    expect(node.title).toBe(title);
  }
  expect(nodeTextIssue(make('Note', '😀'.repeat(4000)))).toBeUndefined();
  expect(nodeTextIssue(make('Note', '😀'.repeat(4000) + 'a'))).toContain('16000-byte limit');
  expect(nodeTextIssue(make(''))).toBe('Give this step a name.');
  expect(nodeTextIssue(make('Note', ''))).toBe('Describe what this step should do.');
  // Domain/access/order decisions belong to the backend, including first approval.
  expect(nodeTextIssue({...make('Approve'),kind:'approval'})).toBeUndefined();
});

test('validation responses need an explicit verdict and runtime identity; invalid is not infrastructure failure', () => {
  const identity={runtimeFingerprint:'a'.repeat(64),runtimeFingerprintVersion:1};
  expect(readWorkflowValidation({...identity,valid:true})).toEqual({...identity,valid:true});
  const rejected={...identity,valid:false,issue:{nodeId:'measure',message:'Each measure needs a tolerance.'}};
  expect(readWorkflowValidation(rejected)).toEqual(rejected);
  for(const value of [null,[],{}, {valid:true}, {...identity,valid:'true'}, {...identity,valid:false}, {...identity,valid:false,issue:{message:''}}, {...identity,valid:true,runtimeFingerprintVersion:0}]) {
    expect(()=>readWorkflowValidation(value)).toThrow('invalid response');
  }
});
