import { expect, test } from '@playwright/test';
import { pluralizeBriefSteps } from '../src/components/OutcomeBrief';

test('the platform brief resolves step(s) from its count', () => {
  expect(pluralizeBriefSteps('1 step(s) failed before work started.')).toBe(
    '1 step failed before work started.',
  );
  expect(pluralizeBriefSteps('12 step(s) completed.')).toBe('12 steps completed.');
});
