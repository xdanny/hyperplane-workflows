import { test, expect } from '@playwright/test';
import type { SDKWorkflowNode } from '../src/api';
import { dependentSteps, moveStep, nodeDependencies, wouldCycle, workflowGraph } from '../src/workflowGraph';

const step = (id: string, dependsOn?: string[], kind?: SDKWorkflowNode['kind']): SDKWorkflowNode =>
  ({ id, title: id, instruction: `Do ${id}`, ...(kind ? { kind } : {}), ...(dependsOn === undefined ? {} : { dependsOn }) });
const links = (nodes: SDKWorkflowNode[]) => nodes.map((node, index) => [node.id, nodeDependencies(nodes, index)] as const);

test('moving an approval up makes the step it now precedes wait for it', () => {
  // The defect: research -> brief, approval added last and moved to step 2. The
  // brief still pointed at research, so the workflow would have finished
  // without the decision the approval was added for.
  const nodes = [step('research', []), step('brief', ['research']), step('approval', undefined, 'approval')];
  const moved = moveStep(nodes, 'approval', 1);
  expect(moved.map(node => node.id)).toEqual(['research', 'approval', 'brief']);
  expect(links(moved)).toEqual([['research', []], ['approval', ['research']], ['brief', ['approval']]]);
  expect(dependentSteps(moved, 'approval').map(node => node.id)).toEqual(['brief']);
  expect(workflowGraph(moved).unresolved).toBe(false);
  // An approval that states it needs the brief keeps saying so: an explicit
  // prerequisite is a requirement, and moving the card does not withdraw it.
  const stated = [step('research', []), step('brief', ['research']), step('approval', ['brief'], 'approval')];
  expect(links(moveStep(stated, 'approval', 1))).toEqual([['research', []], ['approval', ['brief']], ['brief', ['research']]]);
});

test('moving a step down gates the step it now precedes without dropping a link', () => {
  const nodes = [step('a', []), step('b', ['a']), step('c', ['b']), step('d', ['c'])];
  const moved = moveStep(nodes, 'a', 2);
  expect(moved.map(node => node.id)).toEqual(['b', 'c', 'a', 'd']);
  // 'b' still states it waits for 'a' — now a forward reference, which the SDK
  // accepts — and 'd' waits for both 'c' and 'a', so no gate is lost.
  expect(links(moved)).toEqual([['b', ['a']], ['c', ['b']], ['a', []], ['d', ['c', 'a']]]);
  expect(workflowGraph(moved).unresolved).toBe(false);
});

test('implicit links are left alone: they already follow the visual order', () => {
  const nodes = [step('one'), step('two'), step('three')];
  const moved = moveStep(nodes, 'three', 0);
  expect(moved.map(node => node.id)).toEqual(['three', 'one', 'two']);
  expect(moved.every(node => !Object.hasOwn(node, 'dependsOn'))).toBe(true);
  expect(links(moved)).toEqual([['three', []], ['one', ['three']], ['two', ['one']]]);
});

test('an inserted approval gates every later branch that waited for the step above it', () => {
  const nodes = [step('source', []), step('left', ['source']), step('right', ['source']), step('approval', undefined, 'approval')];
  const moved = moveStep(nodes, 'approval', 1);
  expect(moved.map(node => node.id)).toEqual(['source', 'approval', 'left', 'right']);
  expect(links(moved)).toEqual([['source', []], ['approval', ['source']], ['left', ['approval']], ['right', ['approval']]]);
  expect(workflowGraph(moved).unresolved).toBe(false);
  // A redirection that would close a loop is skipped rather than applied: the
  // review already waits for both branches, so it cannot also gate them.
  const fanIn = [step('source', []), step('left', ['source']), step('right', ['source']), step('review', ['left', 'right'], 'check')];
  expect(links(moveStep(fanIn, 'review', 1))).toEqual([['source', []], ['review', ['left', 'right']], ['left', ['source']], ['right', ['source']]]);
});

test('a move keeps unknown links visible and never invents a cycle', () => {
  const nodes = [step('a', []), step('b', ['a', 'deleted']), step('c', ['b'])];
  const moved = moveStep(nodes, 'b', 0);
  expect(moved.map(node => node.id)).toEqual(['b', 'a', 'c']);
  expect(moved[0].dependsOn).toEqual(['a', 'deleted']);
  expect(moveStep(nodes, 'missing', 0)).toEqual(nodes);
  const clean = [step('a', []), step('b', ['a']), step('c', ['a', 'b']), step('d', ['c'])];
  for (const target of [0, 1, 2, 3, 9]) {
    for (const id of ['a', 'b', 'c', 'd']) {
      const result = moveStep(clean, id, target);
      expect(result.map(node => node.id).sort()).toEqual(['a', 'b', 'c', 'd']);
      expect(workflowGraph(result).unresolved).toBe(false);
    }
  }
});

test('the dependency picker hides only the choices that would close a loop', () => {
  const nodes = [step('a', []), step('b', ['a']), step('c', ['b'])];
  expect(wouldCycle(nodes, 'a', 'b')).toBe(true);
  expect(wouldCycle(nodes, 'a', 'c')).toBe(true);
  expect(wouldCycle(nodes, 'c', 'a')).toBe(false);
  expect(wouldCycle(nodes, 'b', 'b')).toBe(true);
  const implicit = [step('one'), step('two'), step('three')];
  expect(implicit.map(node => wouldCycle(implicit, 'one', node.id))).toEqual([true, true, true]);
  expect(wouldCycle(implicit, 'three', 'one')).toBe(false);
  const broken = [step('x', ['y']), step('y', ['x'])];
  expect(wouldCycle(broken, 'x', 'y')).toBe(true);
});
