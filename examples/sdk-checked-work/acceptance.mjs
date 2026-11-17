import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

// Caller-owned reference and check stay outside the agent's writable step.
const expectedInput = readFileSync(new URL('./input.csv', import.meta.url), 'utf8');
assert.equal(readFileSync('../input.csv', 'utf8'), expectedInput, 'Input changed');
assert.equal(readFileSync('totals.csv', 'utf8').trim().replaceAll('\r\n', '\n'),
  'category,total\nfood,15.00\ntravel,6.00', 'Expected exactly the two ordered category totals');
console.log('Two exact totals verified; input unchanged.');
