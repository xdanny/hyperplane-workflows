import { test, expect } from '@playwright/test';
import { addPair, pairEdit, removePair, sameMap, setPair, syncPairs, toMap } from '../src/declPairs';

/**
 * A KPI's definitions map, mid-edit.
 *
 * The bug these pin: "+ Add a system's definition" appended a pair, serialized
 * it to a map that drops keyless entries, and re-derived the pairs from that
 * map — so the pair vanished as it was created and no KPI could ever be given
 * a definition per system. Same distinction as the recipe table slot: a blank
 * entry is an editing state, not a stored value.
 */

test('an empty field already has one pair of boxes, and stores nothing', () => {
  const edit = pairEdit(undefined);
  expect(edit.pairs).toEqual([['', '']]);
  expect(edit.value).toBeUndefined();
});

test("+ Add a system's definition adds a pair that survives the round trip", () => {
  const second = addPair(pairEdit(undefined));
  expect(second.pairs).toHaveLength(2);
  expect(second.value).toBeUndefined();
  expect(syncPairs(second, second.value).pairs).toHaveLength(2);
  expect(addPair(second).pairs).toHaveLength(3);
});

test('a definition typed before its system name is not thrown away', () => {
  const typed = setPair(pairEdit(undefined), 0, 1, 'sum(amount_usd) after refunds');
  expect(typed.pairs[0]).toEqual(['', 'sum(amount_usd) after refunds']);
  // Nothing is stored yet — a definition with no system is not a definition —
  // but the text is still on screen to finish.
  expect(typed.value).toBeUndefined();
  const named = setPair(typed, 0, 0, 'Finance');
  expect(named.value).toEqual({ Finance: 'sum(amount_usd) after refunds' });
});

test('renaming a system does not lose its definition while the box is empty', () => {
  const one = pairEdit({ Finance: 'sum(amount_usd)' });
  const cleared = setPair(one, 0, 0, '');
  expect(cleared.pairs[0]).toEqual(['', 'sum(amount_usd)']);
  expect(cleared.value).toBeUndefined();
  const renamed = setPair(cleared, 0, 0, 'Ops');
  expect(renamed.value).toEqual({ Ops: 'sum(amount_usd)' });
});

test('two systems are stored as two entries, and read back as two pairs', () => {
  let edit = pairEdit(undefined);
  edit = setPair(setPair(edit, 0, 0, 'Finance'), 0, 1, 'sum(amount_usd) after refunds');
  edit = addPair(edit);
  edit = setPair(setPair(edit, 1, 0, ' Ops '), 1, 1, 'sum(gross) - chargebacks');
  expect(edit.value).toEqual({
    Finance: 'sum(amount_usd) after refunds',
    Ops: 'sum(gross) - chargebacks',
  });
  expect(pairEdit(edit.value).pairs).toHaveLength(2);
});

test('removing the last pair leaves an empty pair, not an empty box', () => {
  const gone = removePair(pairEdit({ Finance: 'sum(amount_usd)' }), 0);
  expect(gone.pairs).toEqual([['', '']]);
  expect(gone.value).toBeUndefined();
});

test('a map from somewhere else replaces the pairs', () => {
  const mine = setPair(pairEdit(undefined), 0, 0, 'mine');
  const theirs = syncPairs(mine, { Finance: 'declared' });
  expect(theirs.pairs).toEqual([['Finance', 'declared']]);
  expect(syncPairs(theirs, undefined).pairs).toEqual([['', '']]);
});

test('the map helpers say what they mean', () => {
  expect(toMap([['', 'orphan']])).toBeUndefined();
  expect(toMap([[' Finance ', 'x']])).toEqual({ Finance: 'x' });
  expect(sameMap(undefined, {})).toBe(true);
  expect(sameMap({ a: '1' }, { a: '1' })).toBe(true);
  expect(sameMap({ a: '1' }, { a: '2' })).toBe(false);
  expect(sameMap({ a: '1' }, { a: '1', b: '2' })).toBe(false);
});
