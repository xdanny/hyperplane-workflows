import { expect, test } from '@playwright/test';
import { workflowFileLabel } from '../src/workflowFiles';

test('SDK files use declared titles without guessing unknown paths or hiding nested filenames', () => {
  const nodes = [{ id: 'step-123', title: 'Create guide' }, { id: 'step-1234', title: 'Review' }];
  expect(workflowFileLabel('step-123.md', nodes)).toBe('Create guide');
  expect(workflowFileLabel('delivery/step-1234.md', nodes)).toBe('Review');
  expect(workflowFileLabel('step-123/guide.txt', nodes)).toBe('Create guide · guide.txt');
  expect(workflowFileLabel('delivery/step-123/notes/guide.txt', nodes)).toBe('Create guide · notes/guide.txt');
  expect(workflowFileLabel('delivery/unknown.md', nodes)).toBe('delivery/unknown.md');
  expect(workflowFileLabel('step-1234/guide.txt')).toBe('step-1234/guide.txt');
});
