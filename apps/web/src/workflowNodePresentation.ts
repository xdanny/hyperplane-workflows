import type { SDKWorkflowNode } from './api';
import { nodeDependencies } from './workflowGraph';

export interface WorkflowNodeFile { path: string; bytes: number; sha256: string }

/**
 * One recorded web search (D-01): what was asked, and what came back.
 *
 * The audited inspector showed `Completed web searches 5` and nothing else, so
 * a reader could not tell a citation the model recalled from a source it
 * actually fetched — which was the exact question the failing review hinged on.
 * A query and its top hosts do not prove a page was read either, and nothing
 * here claims they do: the document reader still marks no citation as fetched.
 */
export interface WorkflowNodeSearch {
  query: string;
  urls?: string[];
  at?: string;
}

/**
 * What one review finding IS, rather than what a regular expression guessed.
 *
 * "Independent review: passed · 3 caveats" was counted off three findings that
 * all CONFIRMED something (S-17) — the reader was told to go and worry about
 * three things the reviewer had actually signed off. The reviewer now labels
 * each finding (`FAIL —`, `CAVEAT —`, `CONFIRMED —`) and the worker records the
 * level beside the text, so counting is reading rather than inference.
 */
export type ReviewFindingLevel = 'fail' | 'caveat' | 'confirmation';

export interface ReviewFinding {
  level: ReviewFindingLevel;
  /** The finding as the reviewer wrote it, label included. */
  text: string;
}

const LEVELS: ReviewFindingLevel[] = ['fail', 'caveat', 'confirmation'];

/**
 * `findingDetails` as recorded, or null when the checkpoint has none worth
 * trusting. Null is the signal to fall back to the heuristic — an empty array
 * from a review that raised nothing is NOT the same thing, so a checkpoint that
 * recorded `[]` alongside `[]` findings still reads as typed.
 */
export function recordedFindingDetails(checkpoint?: WorkflowNodeCheckpoint): ReviewFinding[] | null {
  const raw = checkpoint?.findingDetails;
  if (!Array.isArray(raw)) return null;
  const findings = (checkpoint?.findings ?? []).filter((f) => typeof f === 'string' && f.trim() !== '');
  const parsed = raw.flatMap((entry): ReviewFinding[] => {
    if (!entry || typeof entry !== 'object') return [];
    const level = (entry as ReviewFinding).level;
    const text = (entry as ReviewFinding).text;
    if (!LEVELS.includes(level) || typeof text !== 'string' || text.trim() === '') return [];
    return [{ level, text: text.trim() }];
  });
  // Same order as `findings`, says the contract. A record that lost entries on
  // the way is not a typed record: one missing level would silently become a
  // caveat attached to the wrong sentence, so the whole thing falls back.
  if (parsed.length !== raw.length) return null;
  if (findings.length > 0 && parsed.length !== findings.length) return null;
  return parsed;
}

export interface WorkflowNodeCheckpoint {
  id: string; phase: string; title?: string;
  document?: WorkflowNodeFile; files?: WorkflowNodeFile[];
  verification?: 'not_requested' | 'pending' | 'passed' | 'failed';
  attempts?: number; findings?: string[]; threadId?: string; decision?: string;
  /** One level per `findings` entry, in the same order (round-13 contract).
   *  Absent on every run recorded before the reviewer started labelling. */
  findingDetails?: ReviewFinding[];
  /**
   * How many web searches the step completed (`searches`, a count, as it has
   * always been) and what they were (`searchLog`, added in round 12).
   *
   * A step that never searched has no `searchLog` at all, which is why absence
   * here is not the same claim as zero. The union on `searches` is tolerance
   * for a checkpoint that inlined the entries into the count field rather than
   * a second supported shape.
   */
  searches?: number | WorkflowNodeSearch[];
  searchLog?: WorkflowNodeSearch[];
}

/** Entries only — never a count dressed up as evidence. Bounded at 20 the way
 *  the contract bounds the recording, so a malformed checkpoint cannot make the
 *  inspector unreadable. */
export function recordedSearches(checkpoint?: WorkflowNodeCheckpoint): WorkflowNodeSearch[] {
  const raw = Array.isArray(checkpoint?.searchLog)
    ? checkpoint!.searchLog
    : Array.isArray(checkpoint?.searches) ? checkpoint!.searches : [];
  return raw
    .filter((entry): entry is WorkflowNodeSearch =>
      Boolean(entry) && typeof entry === 'object' && typeof entry.query === 'string' && entry.query.trim() !== '')
    .slice(0, 20)
    .map(entry => ({
      query: entry.query.trim().slice(0, 300),
      urls: (Array.isArray(entry.urls) ? entry.urls : [])
        .filter((url): url is string => typeof url === 'string' && url.trim() !== '')
        .slice(0, 5),
      ...(typeof entry.at === 'string' && entry.at ? { at: entry.at } : {}),
    }));
}

/** How many searches the checkpoint records — the count field when that is what
 *  it holds, the number of entries when it holds those. Undefined when the
 *  checkpoint says nothing, which is not the same claim as zero. */
export function searchCount(checkpoint?: WorkflowNodeCheckpoint): number | undefined {
  if (typeof checkpoint?.searches === 'number') return checkpoint.searches;
  const entries = recordedSearches(checkpoint);
  return entries.length > 0 ? entries.length : undefined;
}

/**
 * "Search: <query> → notion.com, linear.app" — one search as one line.
 *
 * Hosts, not full URLs: the point of the line is which SOURCES were consulted,
 * and five wrapped URLs per row buries that. The full URLs stay on the row
 * beneath it. A URL that does not parse is shown verbatim rather than dropped.
 *
 * This SDK's web-search events often carry only the query, so an entry with no
 * `urls` renders as the query alone rather than as a query with an empty arrow
 * after it — the recorded fact is that the search ran.
 */
export function searchLine(search: WorkflowNodeSearch): string {
  const hosts: string[] = [];
  for (const url of search.urls ?? []) {
    let host = url;
    try { host = new URL(url).host || url; } catch { /* not an absolute URL — quote it */ }
    if (!hosts.includes(host)) hosts.push(host);
  }
  return hosts.length > 0 ? `Search: ${search.query} → ${hosts.join(', ')}` : `Search: ${search.query}`;
}

export const nodeKinds = {
  document: { label: 'Document', icon: '▤', output: 'Markdown document' },
  research: { label: 'Research', icon: '⌕', output: 'Document with source citations' },
  check: { label: 'Independent review', icon: '✓', output: 'Model verdict and review document' },
  approval: { label: 'Your approval', icon: '◇', output: 'Your decision and its record' },
  task: { label: 'File creation', icon: '▱', output: 'Files and work summary' },
};
export const nodeKind = (node: SDKWorkflowNode) => nodeKinds[node.kind ?? 'document'];

export function nodeContext(node: SDKWorkflowNode, nodes: SDKWorkflowNode[]) {
  const index = nodes.findIndex(step => step.id === node.id);
  const direct = index < 0 ? [] : nodeDependencies(nodes, index);
  const visited = new Set<string>([node.id]);
  const ancestors: SDKWorkflowNode[] = [];
  function visit(id: string) {
    if (visited.has(id)) return;
    visited.add(id);
    const position = nodes.findIndex(step => step.id === id);
    if (position < 0) return;
    nodeDependencies(nodes, position).forEach(visit);
    ancestors.push(nodes[position]);
  }
  direct.forEach(visit);
  return { direct, ancestors };
}

export function nodeCapabilities(node: SDKWorkflowNode, nodes: SDKWorkflowNode[]) {
  if (node.kind === 'approval') return ['Waits for your decision; no model call.', 'Runs on its own while approval is requested.'];
  if (node.kind === 'task') return [
    'SDK tools can create files and run commands inside this step’s managed work folder. Earlier workflow outputs are read-only inputs.',
    node.access ? 'Network connections are limited to the declared hosts below.' : 'Network access and live web search are disabled.',
    node.access && node.toolchain && node.sideEffect ? 'Temporary tool installation is permitted in the managed runtime.' : 'Additional tool installation is not permitted.',
    'Runs on its own. Host integrations are disabled.',
  ];
  const search = node.kind === 'research' || node.kind === 'check' && nodeContext(node, nodes).ancestors.some(step => step.kind === 'research');
  return [
    node.kind === 'check' ? 'A separate SDK conversation reviews the stated criteria.' : node.kind === 'research' ? 'A separate SDK conversation researches live sources.' : 'The SDK writes this step’s document from its instructions.',
    search ? 'Live web search is enabled.' : 'Live web search is disabled.',
    'The model has read-only file access. The application saves its response. Dependency context is not a separate filesystem boundary.',
    'Host integrations, publishing and external app actions are unavailable.',
  ];
}

export function nodeVerification(node: SDKWorkflowNode, checkpoint?: WorkflowNodeCheckpoint) {
  if (node.kind === 'approval') return checkpoint?.phase === 'passed' ? 'Your approval was recorded; it is a human decision, not an independent correctness check.' : checkpoint?.phase === 'rejected' ? 'Your rejection was recorded.' : 'This step waits for your decision about the prerequisite evidence.';
  if (node.kind === 'check') return checkpoint?.phase === 'passed' ? 'The independent model review passed its stated criteria. Model judgments remain fallible.' : checkpoint?.phase === 'failed' ? 'The independent model review failed its stated criteria. Read its findings and recorded review.' : 'A separate model will return a verdict against the instructions. No verdict is recorded yet.';
  if (node.kind === 'task') {
    if (checkpoint?.verification === 'passed') return 'The configured acceptance check passed. This establishes only the check’s stated requirements.';
    if (checkpoint?.verification === 'failed') return 'The configured acceptance check failed. Read the run’s findings before reusing the outputs.';
    if (checkpoint?.verification === 'not_requested' || !node.acceptance && !node.check?.length) return 'No extra correctness check was requested. Completion checks output presence and integrity, not independent correctness.';
    return 'An extra acceptance check is configured; its result has not been recorded yet.';
  }
  return node.kind === 'research'
    ? 'Completion requires a saved document, a recorded web search and a source URL. These do not establish source accuracy.'
    : 'Completion means a document was saved. Its correctness was not independently verified.';
}

export function nodeCompletionLabel(node: SDKWorkflowNode, checkpoint?: WorkflowNodeCheckpoint) {
  if (checkpoint?.phase !== 'passed') return undefined;
  if (node.kind === 'approval') return 'Decision approved';
  if (node.kind === 'check') return 'Model review passed';
  if (node.kind === 'task') return checkpoint.verification === 'passed' ? 'Acceptance check passed' : 'Files produced';
  return 'Document saved';
}

// Evidence links require a bounded relative recorded path and a hash. A phase
// or expected filename alone never establishes that a file exists.
export function recordedNodeFiles(checkpoint?: WorkflowNodeCheckpoint): WorkflowNodeFile[] {
  const files = [checkpoint?.document, ...(checkpoint?.files ?? [])];
  const seen = new Set<string>();
  return files.filter((file): file is WorkflowNodeFile => {
    if (!file || typeof file.path !== 'string' || !file.path || /^[a-z][a-z0-9+.-]*:/i.test(file.path) || /[\\\u0000-\u001f]/.test(file.path) || file.path.split('/').some(part => !part || part === '.' || part === '..') || !Number.isSafeInteger(file.bytes) || file.bytes < 0 || !/^[a-f0-9]{64}$/i.test(file.sha256) || seen.has(file.path)) return false;
    seen.add(file.path); return true;
  });
}
export function nodeFileURL(runId: string, file: WorkflowNodeFile) {
  return `/file/${encodeURIComponent(runId)}/nodes/delivery/OUTPUTS/${file.path.split('/').map(encodeURIComponent).join('/')}`;
}
export const inputLabel = (key: string) => key.replace(/([a-z0-9])([A-Z])/g, '$1 $2').replaceAll('_', ' ').replace(/^./, character => character.toUpperCase());
