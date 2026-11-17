/**
 * What a run actually PRODUCED, worked out from the step graph (F-G).
 *
 * On the audited build the deliverable was the third of three identical "Read
 * document" links about 600 words below the heading, and the reader had to know
 * which one it was (Q-02, P-06, S-04, C4). The page could not lead with the
 * result because nothing in the app knew which document was the result.
 *
 * It is knowable: the result is what the LAST steps produced — the steps no
 * other step waits on. This module answers that, plus the two lines that belong
 * beside it (what the independent review said, and whose turn it is next), and
 * it answers them as data so the wording can be tested without a browser.
 */

import type { SDKWorkflowNode } from './api';
import { nodeDependencies } from './workflowGraph';
import { recordedFindingDetails, recordedNodeFiles } from './workflowNodePresentation';
import type { ReviewFinding, ReviewFindingLevel, WorkflowNodeCheckpoint, WorkflowNodeFile } from './workflowNodePresentation';
import { clip } from './runLexicon';

export type { ReviewFinding, ReviewFindingLevel };

/** Ids of the steps nothing else waits on — the ends of the graph. */
export function terminalNodeIds(nodes: SDKWorkflowNode[]): string[] {
  const waitedOn = new Set<string>();
  nodes.forEach((_, index) => nodeDependencies(nodes, index).forEach((id) => waitedOn.add(id)));
  return nodes.filter((node) => !waitedOn.has(node.id)).map((node) => node.id);
}

export interface ResultDocument {
  nodeId: string;
  /** The step that produced it — the caption a reader needs to trust the file. */
  stepTitle: string;
  kind?: string;
  file: WorkflowNodeFile;
  /** True when this is the step's main document rather than an extra file. */
  primary: boolean;
}

/**
 * The documents to lead with.
 *
 * An approval step's own record is not the deliverable — it is the receipt for
 * a decision about one — so a terminal approval falls back to what it approved.
 * A file-creating last step contributes ALL its files, because "the result" of
 * a task step is the set it wrote, not an arbitrary first member.
 */
export function finalDocuments(
  nodes: SDKWorkflowNode[] | undefined,
  checkpoints: WorkflowNodeCheckpoint[] | undefined,
): ResultDocument[] {
  if (!nodes || nodes.length === 0) return [];
  const byId = new Map((checkpoints ?? []).map((c) => [c.id, c]));
  const collect = (ids: string[], seen: Set<string>): ResultDocument[] =>
    ids.flatMap((id) => {
      if (seen.has(id)) return [];
      seen.add(id);
      const node = nodes.find((n) => n.id === id);
      if (!node) return [];
      const checkpoint = byId.get(id);
      const files = recordedNodeFiles(checkpoint);
      // An approval or a review is evidence ABOUT the result, not the result.
      if ((node.kind === 'approval' || node.kind === 'check') || files.length === 0) {
        const index = nodes.findIndex((n) => n.id === id);
        return index < 0 ? [] : collect(nodeDependencies(nodes, index), seen);
      }
      const primaryPath = checkpoint?.document?.path;
      const shown = node.kind === 'task' ? files : files.filter((f) => f.path === primaryPath || files.length === 1);
      return (shown.length > 0 ? shown : files).map((file) => ({
        nodeId: id,
        stepTitle: node.title,
        kind: node.kind,
        file,
        primary: file.path === primaryPath,
      }));
    });
  return collect(terminalNodeIds(nodes), new Set());
}

/** The first `count` non-empty lines of a document, for the preview card. */
export function firstLines(text: string, count = 6): string {
  const lines = text.replace(/\r\n?/g, '\n').split('\n');
  const out: string[] = [];
  let started = false;
  for (const line of lines) {
    if (!started && line.trim() === '') continue;
    started = true;
    out.push(line);
    if (out.filter((l) => l.trim() !== '').length >= count) break;
  }
  return out.join('\n').trimEnd();
}

export interface ReviewSummary {
  /** 'passed' | 'failed' | 'pending' — the review's own verdict, never the run's. */
  verdict: 'passed' | 'failed' | 'pending';
  /** The one line: "Independent review: passed · 1 caveat". */
  line: string;
  /** Everything the reviewer said, collapsed behind a disclosure. */
  findings: string[];
  /**
   * The same findings with their level, in the same order.
   *
   * Typed from `findingDetails` when the run recorded it; otherwise inferred
   * the way this module always inferred — a finding that says FAIL fails and
   * everything else is a caveat. The inference cannot recognise a
   * CONFIRMATION, which is exactly why the recorded level exists, so an old run
   * keeps its old (over-)count rather than being retro-fitted with a guess.
   */
  details: ReviewFinding[];
  /** How many findings are caveats — the only number the line quotes. */
  caveats: number;
  /** Findings the reviewer raised as confirmations, collapsed on their own. */
  confirmations: ReviewFinding[];
  /** The step that reviewed, so its document can be linked. */
  stepId?: string;
  stepTitle?: string;
  /**
   * The review DOCUMENT the step recorded, when it recorded one.
   *
   * "Read the review" opened the step's session log, which for a review step is
   * usually empty — "Log: Check the note … no log output" — while review.md sat
   * in the run's outputs (S-16). The verdict a reader wants is the document.
   */
  document?: WorkflowNodeFile;
}

/** A finding that FAILS says so; anything else the reviewer noted is a caveat. */
const isFailFinding = (finding: string): boolean => /^\s*fail\b/i.test(finding);

/**
 * Each finding with its level.
 *
 * The recorded levels win whenever the checkpoint has a well-formed set of
 * them. The fallback is the pre-round-13 heuristic, unchanged and deliberately
 * not improved: guessing "confirmation" from the wording of an old finding
 * would quietly reclassify findings nobody labelled.
 */
export function reviewFindingDetails(checkpoint?: WorkflowNodeCheckpoint): ReviewFinding[] {
  const findings = (checkpoint?.findings ?? []).filter((f) => typeof f === 'string' && f.trim() !== '');
  const recorded = recordedFindingDetails(checkpoint);
  if (recorded) return recorded;
  return findings.map((text) => ({ level: isFailFinding(text) ? 'fail' : 'caveat', text }));
}

/**
 * One honest line about the independent review.
 *
 * "passed" here means a separate model judged the work against stated criteria
 * and did not fail it. The line never drops the caveats the reviewer raised
 * anyway — a non-failing coverage warning is exactly the thing a reader needs
 * before forwarding a brief (D-03's "Non-failing coverage concern").
 */
export function reviewSummary(
  nodes: SDKWorkflowNode[] | undefined,
  checkpoints: WorkflowNodeCheckpoint[] | undefined,
): ReviewSummary | null {
  const reviews = (nodes ?? []).filter((node) => node.kind === 'check');
  if (reviews.length === 0) return null;
  const byId = new Map((checkpoints ?? []).map((c) => [c.id, c]));
  const failed = reviews.find((node) => byId.get(node.id)?.phase === 'failed');
  const step = failed ?? reviews.find((node) => byId.get(node.id)?.phase === 'passed') ?? reviews[0];
  const checkpoint = byId.get(step.id);
  const details = reviewFindingDetails(checkpoint);
  const findings = details.map((finding) => finding.text);
  const caveats = details.filter((finding) => finding.level === 'caveat').length;
  const confirmations = details.filter((finding) => finding.level === 'confirmation');
  const document = recordedNodeFiles(checkpoint).find((file) => file.path === checkpoint?.document?.path);
  const common = { findings, details, caveats, confirmations, stepId: step.id, stepTitle: step.title, document };
  if (failed) {
    const why = details.find((finding) => finding.level === 'fail')?.text ?? findings[0];
    return {
      verdict: 'failed',
      line: why ? `Independent review: failed — ${clip(why)}` : 'Independent review: failed',
      ...common,
    };
  }
  if (checkpoint?.phase !== 'passed') {
    return { verdict: 'pending', line: 'Independent review: not recorded yet', ...common };
  }
  // Only caveats are counted. A confirmation is the reviewer agreeing with the
  // work; putting it in the same number as an unresolved concern is what made
  // "passed · 3 caveats" mean nothing (S-17). "no caveats" is said out loud
  // rather than left as an absence, because a bare "passed" is the wording a
  // reader most easily mistakes for "verified correct".
  return {
    verdict: 'passed',
    line: caveats > 0
      ? `Independent review: passed · ${caveats} caveat${caveats === 1 ? '' : 's'}`
      : 'Independent review: passed, no caveats',
    ...common,
  };
}

/**
 * Whether the run page will render the result card at all — the one rule, read
 * by the card and by the page around it.
 *
 * The page used to show the reviewer's findings twice, once inside this card
 * and again under "Check findings" (S-12). Suppressing the duplicate means the
 * page has to know whether the card is there, and a second copy of the
 * condition would drift from the first the next time either moved.
 */
export function resultCardVisible(input: {
  documents: number;
  review: boolean;
  nextActionKind: NextAction['kind'];
  decision: boolean;
}): boolean {
  return input.documents > 0 || input.review || input.nextActionKind !== 'none' || input.decision;
}

/** Whose turn it is, in one line, at the top of the page. */
export interface NextAction {
  kind: 'approval' | 'sign_off' | 'none';
  text: string;
}

export function nextAction(input: {
  state: string;
  waitingForPerson?: boolean;
  signOffPending?: boolean;
}): NextAction {
  if (input.state === 'running' && input.waitingForPerson) {
    return { kind: 'approval', text: 'Waiting for your approval below.' };
  }
  if (input.state === 'passed' && input.signOffPending) {
    return { kind: 'sign_off', text: 'Awaiting your sign-off in Inbox.' };
  }
  return { kind: 'none', text: '' };
}
