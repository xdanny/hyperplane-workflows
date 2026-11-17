import { expect, test } from '@playwright/test';
import {
  confidenceLabel, draftRationale, evidenceBoundary, isStatusQuote,
  selfReportedConfidence, splitEvidence,
} from '../src/draftSkill';
import type { Skill } from '../src/api';

const doc = [
  'This draft method applies to short writing tasks with explicit output constraints.',
  '',
  'Quality bar: The response should satisfy each explicit constraint.',
  '',
  'Evidence boundary: The procedure above is proposed guidance. A passed workflow status alone cannot establish content quality or constraint compliance.',
].join('\n');

const skill: Skill = {
  id: 'constrained-short-form-drafting',
  name: 'Constrained short-form drafting',
  metadata: {
    confidence: 0.2,
    rationale: 'Its text is not displayed, so the procedure has little observational support.',
    doc,
    evidence: [{ nodeId: 'note', quote: 'phase: passed' }],
  },
};

test('the extractor’s own confidence is rendered as a number with what it is', () => {
  expect(selfReportedConfidence(skill)).toBe(0.2);
  expect(confidenceLabel(0.2)).toBe('low (0.20 of 1, the extractor’s own estimate)');
  expect(confidenceLabel(0.5)).toContain('moderate');
  expect(confidenceLabel(0.9)).toContain('high');
  expect(confidenceLabel(null)).toBeNull();
  expect(selfReportedConfidence({ ...skill, metadata: {} })).toBeNull();
  expect(selfReportedConfidence({ ...skill, metadata: { confidence: 4 } })).toBeNull();
});

test('the rationale and the Evidence boundary are read off the record, not invented', () => {
  expect(draftRationale(skill)).toContain('little observational support');
  expect(evidenceBoundary(skill)).toBe(
    'The procedure above is proposed guidance. A passed workflow status alone cannot establish content quality or constraint compliance.',
  );
  // A draft with no boundary text says nothing rather than inventing a caveat.
  expect(evidenceBoundary({ ...skill, metadata: { doc: 'Just a procedure.' } })).toBe('');
  expect(evidenceBoundary({ ...skill, metadata: {} })).toBe('');
  expect(draftRationale({ ...skill, metadata: {} })).toBe('');
});

test('a status word is never passed off as delivered text', () => {
  expect(isStatusQuote('phase: passed')).toBe(true);
  expect(isStatusQuote('verification = failed')).toBe(true);
  expect(isStatusQuote('  PASSED ')).toBe(true);
  expect(isStatusQuote('')).toBe(true);
  expect(isStatusQuote('The brief covers September 6–12 and states that 13 September is not covered.')).toBe(false);
});

test('evidence is split so the card can say which kind it is holding', () => {
  const split = splitEvidence([
    { nodeId: 'note', quote: 'phase: passed' },
    { nodeId: 'draft', quote: 'Coverage: September 6–12. September 13 is not covered.' },
  ]);
  expect(split.statusOnly.map((e) => e.nodeId)).toEqual(['note']);
  expect(split.quoted.map((e) => e.nodeId)).toEqual(['draft']);
});
