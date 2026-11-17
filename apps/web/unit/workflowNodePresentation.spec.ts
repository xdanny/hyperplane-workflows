import { test, expect } from '@playwright/test';
import type { SDKWorkflowNode } from '../src/api';
import { nodeCapabilities, nodeContext, nodeFileURL, nodeVerification, recordedNodeFiles, recordedSearches, searchCount, searchLine } from '../src/workflowNodePresentation';
import type { WorkflowNodeCheckpoint } from '../src/workflowNodePresentation';
const node = (id: string, kind: SDKWorkflowNode['kind'], dependsOn: string[]): SDKWorkflowNode => ({id, kind, dependsOn, title: id, instruction: `Do ${id}`});

test('inspected context includes transitive prerequisites and excludes unrelated sibling research', () => {
  const nodes = [node('draft', 'document', []), node('unrelated', 'research', []), node('middle', 'document', ['draft']), node('review', 'check', ['middle'])];
  expect(nodeContext(nodes[3], nodes).ancestors.map(node => node.id)).toEqual(['draft', 'middle']);
  expect(nodeCapabilities(nodes[3], nodes)).toContain('Live web search is disabled.');
  nodes[2].dependsOn = ['unrelated'];
  expect(nodeCapabilities(nodes[3], nodes)).toContain('Live web search is enabled.');
});

test('completion does not invent recorded files; safe evidence retains actual filenames and exact hashes', () => {
  expect(recordedNodeFiles({id: 'draft', phase: 'passed'})).toEqual([]);
  const file = {path: 'actual result #1.md', bytes: 123, sha256: 'a'.repeat(64)};
  const checkpoint: WorkflowNodeCheckpoint = {id: 'draft', phase: 'passed', document: file, files: [file]};
  expect(recordedNodeFiles(checkpoint)).toEqual([file]);
  expect(nodeFileURL('run one', file)).toBe('/file/run%20one/nodes/delivery/OUTPUTS/actual%20result%20%231.md');
  for (const path of ['/tmp/file.md', '../secret', 'child/../../secret', 'https://example.com/file', '//example.com/file', 'javascript:alert(1)', 'a\\b', 'a\nfile', './draft.md']) {
    expect(recordedNodeFiles({...checkpoint, document: {...file, path}, files: []})).toEqual([]);
  }
  expect(recordedNodeFiles({...checkpoint, document: {...file, sha256: ''}, files: []})).toEqual([]);
});

test('model verdicts, unchecked files and deterministic acceptance remain distinct', () => {
  expect(nodeVerification(node('review', 'check', ['source']), {id:'review',phase:'passed'})).toContain('model review passed');
  expect(nodeVerification(node('draft', 'document', []), {id:'draft',phase:'passed'})).toContain('not independently verified');
  const task = {...node('files', 'task', []), check: ['python3', 'check.py']};
  expect(nodeVerification(task, {id:'files',phase:'passed'})).toContain('has not been recorded');
  expect(nodeVerification(task, {id:'files',phase:'failed',verification:'failed'})).toContain('run’s findings');
});

test('tool installation needs the recorded access grant and both explicit switches', () => {
  const task = {...node('files', 'task', []), toolchain:true, sideEffect:true};
  expect(nodeCapabilities(task, [task])).toContain('Additional tool installation is not permitted.');
  expect(nodeCapabilities({...task,access:{hosts:['example.com']}}, [task])).toContain('Temporary tool installation is permitted in the managed runtime.');
});

test('recorded searches are the queries themselves, not a count dressed as evidence (D-01)', () => {
  // `searches` is the COUNT it has always been; `searchLog` carries the entries.
  // A checkpoint written before the log existed keeps its count and shows no
  // entries rather than pretending to have them.
  expect(searchCount({id: 'research', phase: 'passed', searches: 5})).toBe(5);
  expect(recordedSearches({id: 'research', phase: 'passed', searches: 5})).toEqual([]);
  const entries = [
    {query: 'Linear inbox redesign September 2026', urls: ['https://linear.app/changelog', 'https://www.notion.com/releases'], at: '2026-09-12T13:02:00Z'},
    {query: 'Notion release index'},
  ];
  const logged = {id: 'research', phase: 'passed', searches: 2, searchLog: entries};
  expect(searchCount(logged)).toBe(2);
  expect(recordedSearches(logged)).toHaveLength(2);
  expect(searchLine(recordedSearches(logged)[0]))
    .toBe('Search: Linear inbox redesign September 2026 → linear.app, www.notion.com');
  // This SDK's web_search events often carry only a query. That entry reads as
  // the query alone, not as a query with an empty arrow after it.
  expect(searchLine(recordedSearches(logged)[1])).toBe('Search: Notion release index');
  // A step that never searched has no log at all, and silence is not zero.
  expect(searchCount({id: 'research', phase: 'passed'})).toBeUndefined();
  expect(recordedSearches({id: 'research', phase: 'passed'})).toEqual([]);
});

test('malformed search records are dropped rather than rendered as evidence', () => {
  const searches = [
    {query: '   '},
    {query: 'ok', urls: ['https://a.example', 'https://b.example', 'https://c.example', 'https://d.example', 'https://e.example', 'https://f.example']},
    ...Array.from({length: 30}, (_, i) => ({query: `q${i}`})),
  ] as WorkflowNodeCheckpoint['searchLog'];
  const recorded = recordedSearches({id: 'research', phase: 'passed', searchLog: searches});
  expect(recorded).toHaveLength(20);
  expect(recorded[0].query).toBe('ok');
  // At most five URLs per entry, exactly as the recording is bounded.
  expect(recorded[0].urls).toHaveLength(5);
  expect(recordedSearches({id: 'r', phase: 'passed', searchLog: [{query: 'x'.repeat(400)}]})[0].query).toHaveLength(300);
  // A query that is not a URL is quoted rather than dropped.
  expect(searchLine({query: 'q', urls: ['not a url']})).toBe('Search: q → not a url');
});
