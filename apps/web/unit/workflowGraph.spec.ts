import { test, expect } from '@playwright/test';
import type { SDKWorkflowNode } from '../src/api';
import { graphNodeState, nodeDependencies, workflowGraph } from '../src/workflowGraph';
const step = (id: string, dependsOn?: string[]): SDKWorkflowNode => ({ id, title: id, instruction: `Write ${id}`, ...(dependsOn === undefined ? {} : { dependsOn }) });

test('graph display preserves omitted order, independent roots and forward dependencies', () => {
  const nodes = [step('start', []), step('join', ['left', 'right']), step('left', ['start']), step('right', ['start'])];
  expect(workflowGraph(nodes).rows.map(row => row.map(node => node.id))).toEqual([['start'], ['left', 'right'], ['join']]);
  expect(nodeDependencies(nodes, 0)).toEqual([]);
  const legacy = [step('one'), step('two'), step('three')];
  expect(workflowGraph(legacy).rows.map(row => row.map(node => node.id))).toEqual([['one'], ['two'], ['three']]);
  expect(nodeDependencies([legacy[1], legacy[0], legacy[2]], 1)).toEqual(['two']);
  expect(legacy.every(node => !Object.hasOwn(node, 'dependsOn'))).toBe(true);
});

test('invalid drafts remain visible without claiming a valid execution graph', () => {
  for (const nodes of [[step('one', ['two']), step('two', ['one'])], [step('one', ['removed']), step('two')]]) {
    const graph = workflowGraph(nodes);
    expect(graph.unresolved).toBe(true);
    expect(graph.rows.flat().map(node => node.id)).toEqual(['one', 'two']);
  }
});

test('readiness uses dependency checkpoints and never treats eligible work as active', () => {
  const nodes = [step('start', []), step('left', ['start']), step('right', ['start']), step('join', ['left', 'right'])];
  const checkpoints = [{ id: 'start', phase: 'passed' }, { id: 'left', phase: 'running' }, { id: 'right', phase: 'pending' }, { id: 'join', phase: 'pending' }];
  expect(nodes.map(node => graphNodeState(node, nodes, checkpoints, 'running').kind)).toEqual(['completed', 'active', 'ready', 'pending']);
  expect(graphNodeState(nodes[3], nodes, [], 'passed').label).toBe('No checkpoint recorded');
});

test('failed ancestors block downstream work while cancellation keeps untouched branches not run', () => {
  const nodes = [step('start', []), step('left', ['start']), step('right', ['start']), step('join', ['left', 'right']), step('after', ['join'])];
  const checkpoints = [{ id: 'start', phase: 'passed' }, { id: 'left', phase: 'failed' }, { id: 'right', phase: 'interrupted' }, { id: 'join', phase: 'pending' }, { id: 'after', phase: 'pending' }];
  expect(nodes.map(node => graphNodeState(node, nodes, checkpoints, 'failed').kind)).toEqual(['completed', 'failed', 'stopped', 'blocked', 'blocked']);
  const cancelled = checkpoints.map(node => ({ ...node, phase: node.id === 'left' ? 'interrupted' : node.phase }));
  expect(graphNodeState(nodes[3], nodes, cancelled, 'interrupted').label).toBe('Not run');
  for (const phase of ['running', 'verifying', 'saving', 'correction_pending']) {
    expect(graphNodeState(nodes[2], nodes, [{id: 'right', phase}], 'failed')).toEqual({kind: 'stopped', label: 'Stopped'});
  }
});
