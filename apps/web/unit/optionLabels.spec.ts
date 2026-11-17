import { test, expect } from '@playwright/test';
import { optionLabel, sideName } from '../src/optionLabels';

/**
 * The tokens a select STORES and the words it SHOWS are two different things.
 *
 * Every select on the project-declaration form and every closed column on a
 * recipe table offered `percent_of_a`, `points_of_share`, `dev`, `holdout` as
 * their own labels. These pin the split: the value never changes, the label
 * always does, and one token that means two things in two columns gets two
 * labels rather than a compromise.
 */

test('a tolerance unit says which side it is a percentage of', () => {
  expect(optionLabel('unit', 'percent_of_a')).toBe("% of A's total");
  expect(optionLabel('unit', 'percent_of_b')).toBe("% of B's total");
  expect(optionLabel('unit', 'percent_of_baseline')).toBe('% of the baseline');
  expect(optionLabel('unit', 'absolute')).toBe('an absolute amount');
  expect(optionLabel('unit', 'count')).toBe('a count of rows');
  expect(optionLabel('unit', 'exact')).toBe('exact match');
  expect(optionLabel('unit', 'points_of_share')).toBe('points of share');
});

test('once the two sources are named, the unit wears their names', () => {
  const sides = { a: 'Finance revenue (Looker)', b: 'Salesforce export' };
  expect(optionLabel('unit', 'percent_of_a', sides)).toBe('% of Finance revenue (Looker) total');
  expect(optionLabel('unit', 'percent_of_b', sides)).toBe('% of Salesforce export total');
  // Only one side named: the other keeps the generic label rather than
  // borrowing the one that IS known.
  expect(optionLabel('unit', 'percent_of_b', { a: 'Finance' })).toBe("% of B's total");
});

test('psi means one thing under Unit and another under Statistic', () => {
  expect(optionLabel('unit', 'psi')).toBe('PSI points');
  expect(optionLabel('statistic', 'psi')).toContain('how far the distribution moved');
});

test("an evaluation set's kind carries its rule, which is the point of declaring one", () => {
  expect(optionLabel('kind', 'dev')).toBe('dev — may be used while building');
  expect(optionLabel('kind', 'holdout')).toBe('holdout — nothing may train on it');
});

test('a token already in a person’s words is left alone', () => {
  expect(optionLabel('severity', 'blocker')).toBe('blocker');
  expect(optionLabel('type', 'categorical')).toBe('categorical');
  expect(optionLabel('side', 'both')).toBe('both');
  expect(optionLabel('unit', 'something_new')).toBe('something_new');
});

test('a side name is the head of the sentence that describes the source', () => {
  expect(sideName('Paid Social Q2 (Looker) — owned by Priya, marketing ops')).toBe(
    'Paid Social Q2 (Looker)',
  );
  expect(sideName('analytics.revenue_daily, ~1.4M rows')).toBe('analytics.revenue_daily');
  // A paragraph is not a label, and half a paragraph reads like a different
  // dashboard — so nothing is offered rather than a truncation.
  expect(
    sideName('postgres analytics.revenue_daily read directly with SQL at invoice-line level'),
  ).toBeUndefined();
  expect(sideName('')).toBeUndefined();
  expect(sideName(undefined)).toBeUndefined();
});
