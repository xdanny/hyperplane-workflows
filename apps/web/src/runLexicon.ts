/**
 * ONE status vocabulary for every supervision surface (F-H).
 *
 * The round-10 build said four different things about the same run: PASSED on
 * the Runs list, COMPLETED + NEEDS DECISION in the Inbox, "3 attempts · One
 * correction per step allowed" on its own page, and "failed at delivery" for a
 * review a person could read (Q-01, S-07, D-07, D-08, M-08, M-12). None of
 * those were wrong about the database; they were each paraphrasing it
 * differently. So the paraphrasing lives here, once, and every surface renders
 * what this module returns.
 *
 * Two rules this file exists to keep:
 *   1. Completion, program check, model review and human decision are four
 *      different claims. A word that blurs them is a bug, not a style choice.
 *   2. "attempts" never counts steps. A node workflow reports steps and the
 *      corrections it actually used.
 */

import type { OutcomeVerdict, Run, RunState, SDKWorkflowNode } from './api';
import type { WorkflowNodeCheckpoint } from './workflowNodePresentation';
import { automaticAcceptanceLine, isAutomatic } from './signOff';

// ── Chips ────────────────────────────────────────────────────────────────────

/** What a chip says — the presentation, not the stored state. */
export type RunPresentation =
  | 'passed'
  | 'review_failed'
  | 'check_failed'
  | 'failed'
  | 'waiting'
  | 'running'
  | 'stopped'
  | 'pending';

export const CHIP_LABEL: Record<RunPresentation, string> = {
  passed: 'PASSED',
  review_failed: 'REVIEW FAILED',
  check_failed: 'CHECK FAILED',
  failed: 'FAILED',
  waiting: 'WAITING FOR YOU',
  running: 'RUNNING',
  stopped: 'STOPPED',
  pending: 'PENDING',
};

export const CHIP_CLASS: Record<RunPresentation, string> = {
  passed: 's-pass',
  review_failed: 's-fail',
  check_failed: 's-fail',
  failed: 's-fail',
  // A run parked on a person is not live work and not a failure. It is the one
  // state the reader can act on, so it gets the colour that asks for an action.
  waiting: 's-block',
  running: 's-live',
  stopped: 's-idle',
  pending: 's-idle',
};

/** What each chip word actually claims — the "What these statuses mean" body. */
export const CHIP_MEANING: Record<RunPresentation, string> = {
  passed:
    'Every step finished and every declared check returned a pass. Checks test only what they state; a model review is a model’s judgment, not proof.',
  review_failed:
    'An independent model review judged the work against its stated criteria and failed it. Read the finding — a criterion can also be unsatisfiable.',
  check_failed: 'A program check ran against the produced files and returned a failure.',
  failed: 'The run ended without producing its result. The failing step names where.',
  waiting: 'Nothing is executing. The run is parked on a decision only you can make.',
  running: 'A step is executing now.',
  stopped: 'A person stopped the run. Finished steps were kept; nothing was re-run.',
  pending: 'The run has not started.',
};

/** The minimum a caller must know to render a chip. */
export interface RunStatusInput {
  state: RunState;
  waitingForPerson?: boolean;
  failingStep?: { id: string; title: string; kind?: string } | null;
}

/**
 * Which chip a run wears.
 *
 * `waitingForPerson` wins over `running` because RUNNING beside "0 steps are
 * active" is the single most-reported contradiction in the audit (Q-03, M-11).
 * The store still says running; only this word changes.
 */
export function runPresentation(run: RunStatusInput): RunPresentation {
  if (run.state === 'running') return run.waitingForPerson ? 'waiting' : 'running';
  if (run.state === 'blocked') return 'waiting';
  if (run.state === 'interrupted') return 'stopped';
  if (run.state === 'passed') return 'passed';
  if (run.state === 'pending') return 'pending';
  // failed — say WHAT failed. "failed at delivery" named an internal node id.
  if (run.failingStep?.kind === 'check') return 'review_failed';
  if (run.failingStep?.kind === 'task') return 'check_failed';
  return 'failed';
}

export const runChipLabel = (run: RunStatusInput): string => CHIP_LABEL[runPresentation(run)];
export const runChipClass = (run: RunStatusInput): string => CHIP_CLASS[runPresentation(run)];

// ── Human acceptance ─────────────────────────────────────────────────────────

export interface AcceptanceRecord {
  verdict: OutcomeVerdict;
  decidedAt?: string;
  decidedBy?: string;
  note?: string;
  /** Declared checks that passed, of how many were declared, at the moment the
   *  verdict was recorded — carried by an automatic acceptance. */
  checksPassed?: number;
  checksDeclared?: number;
  /** Who WROTE the note, when that is not the person who decided. A reviewer's
   *  note is evidence beside the decision, never a second decision (Q-18). */
  noteBy?: string;
}

const VERDICT_WORD: Record<OutcomeVerdict, string> = {
  accepted: 'accepted',
  changes_requested: 'changes requested',
  rejected: 'rejected',
};

/**
 * The recorded verdict as ONE line: "accepted 12 Sep 2026 · note by Sofia
 * (analyst review)".
 *
 * Quinn's S5 showed "accepted by you 12 Sep 2026" and, further down, "Accepted
 * by Sofia (analyst review)" — two acceptors for one acceptance, one of whom
 * was an advisory reviewer who accepted nothing (Q-18). There is exactly one
 * acceptor: the person (or, under `auto_when_checks_pass`, the platform saying
 * so plainly). Anyone else who wrote something is credited for the NOTE.
 *
 * The decider's own name is not repeated: Hyperplane is local-first with a
 * single operator, so "by local" told a reader nothing they did not know. A
 * named decider is still named, because a shared install has more than one.
 */
export function acceptanceLine(
  outcome: AcceptanceRecord,
  opts: { timeZone?: string } = {},
): string {
  const when = outcome.decidedAt ? ` ${formatDate(outcome.decidedAt, opts.timeZone)}` : '';
  if (isAutomatic(outcome)) return `${automaticAcceptanceLine(outcome)}${when}`;
  const who = outcome.decidedBy?.trim();
  const named = who && who !== 'local' && who !== 'you' ? ` by ${who}` : '';
  const noteBy = outcome.noteBy?.trim();
  const credit = noteBy && noteBy !== who ? ` · note by ${noteBy}` : '';
  return `${VERDICT_WORD[outcome.verdict]}${when}${named}${credit}`;
}

/**
 * The suffix that follows a chip once a verdict is in play.
 *
 * A PASSED run with no verdict is not finished business: the Inbox is holding a
 * sign-off for it, and saying so on the run page is what stops that second
 * decision from arriving unexplained (P-07, S-01, M-11). Returns '' when no
 * sign-off is owed — a failed run is not waiting on a signature, and neither is
 * a run whose workflow set sign-off to optional.
 */
export function acceptanceSuffix(
  outcome: AcceptanceRecord | null | undefined,
  opts: { signOffPending?: boolean; timeZone?: string } = {},
): string {
  if (outcome) return `· ${acceptanceLine(outcome, opts)}`;
  return opts.signOffPending ? '· awaiting your sign-off' : '';
}

/** The verdict note, quoted so it reads as the decider's words, not the app's. */
export const quotedNote = (note?: string): string =>
  note?.trim() ? `“${note.trim()}”` : '';

// ── The header line a node workflow gets instead of "N attempts" ─────────────

/**
 * "3 steps · 0 corrections used (1 allowed per step)".
 *
 * `corrections` is what was actually spent — one per step attempt beyond the
 * first — not the step count wearing the word "attempts" (D-08, M-08, S-07).
 */
export function stepSummaryLine(
  steps: number,
  corrections: number,
  autoRepair: boolean | undefined,
): string {
  const stepWord = `${steps} step${steps === 1 ? '' : 's'}`;
  if (!autoRepair) return `${stepWord} · no automatic correction`;
  return `${stepWord} · ${corrections} correction${corrections === 1 ? '' : 's'} used (1 allowed per step)`;
}

/** Corrections actually spent: every step attempt after the first. */
export function correctionsUsed(checkpoints: WorkflowNodeCheckpoint[] | undefined): number {
  return (checkpoints ?? []).reduce(
    (total, node) => total + Math.max(0, (node.attempts ?? 1) - 1),
    0,
  );
}

// ── Failure wording ──────────────────────────────────────────────────────────

const MAX_FINDING = 140;

/**
 * Trim to a length with a real ellipsis, never a hard cut mid-word (Q-12).
 *
 * The cut lands on the last word boundary inside the budget, however early
 * that is. The previous rule kept a mid-word cut whenever the last space fell
 * in the first 60% of the budget, which is exactly how "…how we should re"
 * reached a run heading. A single token longer than the whole budget is the one
 * case with no boundary to find, and it is cut — there is nowhere else to cut.
 */
export function clip(text: string, max = MAX_FINDING): string {
  const clean = text.replace(/\s+/g, ' ').trim();
  if (clean.length <= max) return clean;
  const cut = clean.slice(0, max);
  const space = cut.lastIndexOf(' ');
  return `${(space > 0 ? cut.slice(0, space) : cut).trimEnd()}…`;
}

/** The runs-list failing line: "<step title> failed — <first finding>". */
export function failingRowText(
  failingStep: { title: string } | null | undefined,
  firstFinding?: string,
): string | null {
  if (!failingStep?.title) return null;
  const why = firstFinding?.trim();
  return why ? `${failingStep.title} failed — ${clip(why)}` : `${failingStep.title} failed`;
}

/**
 * The run page's headline for an unhappy ending: "Independent review failed at
 * step 2 — Independent review of the brief". The step NUMBER is what lets a
 * reader find it in the list below without re-reading every title.
 */
export function failedHeadline(
  failingStep: { id: string; title: string; kind?: string } | null | undefined,
  nodes: SDKWorkflowNode[] | undefined,
): string | null {
  if (!failingStep?.title) return null;
  const index = (nodes ?? []).findIndex((node) => node.id === failingStep.id);
  const at = index >= 0 ? ` at step ${index + 1}` : '';
  const what =
    failingStep.kind === 'check'
      ? 'Independent review failed'
      : failingStep.kind === 'task'
        ? 'Acceptance check failed'
        : failingStep.kind === 'approval'
          ? 'Rejected'
          : 'Step failed';
  return `${what}${at} — ${failingStep.title}`;
}

// ── Numbers with their units ─────────────────────────────────────────────────

/** "43 s", "2 min 43 s", "1 h 4 min". Null in, null out — never "0 s" for a
 *  run that has not finished, which would read as an instant success. */
export function formatDuration(ms: number | null | undefined): string | null {
  if (ms == null || !Number.isFinite(ms) || ms < 0) return null;
  const total = Math.round(ms / 1000);
  if (total < 60) return `${total} s`;
  const minutes = Math.floor(total / 60);
  const seconds = total % 60;
  if (minutes < 60) return seconds === 0 ? `${minutes} min` : `${minutes} min ${seconds} s`;
  const hours = Math.floor(minutes / 60);
  const restMinutes = minutes % 60;
  return restMinutes === 0 ? `${hours} h` : `${hours} h ${restMinutes} min`;
}

/**
 * 428982 → "428.9k". Token counts, where the exact digit never matters.
 *
 * Truncated rather than rounded: a usage figure should never read HIGHER than
 * what was actually spent, and "429.0k" for 428,982 is a number the receipt
 * below it does not contain.
 */
export function formatTokens(n: number | null | undefined): string | null {
  if (n == null || !Number.isFinite(n) || n < 0) return null;
  const tenths = (value: number) => (Math.floor(value * 10) / 10).toFixed(1);
  if (n >= 1_000_000) return `${tenths(n / 1_000_000)}M`;
  if (n >= 1_000) return `${tenths(n / 1_000)}k`;
  return String(Math.round(n));
}

/** Month abbreviations written out rather than taken from Intl: ICU renders
 *  September as "Sept" in en-GB, which is a different column width and a
 *  different string on two machines reading the same run. */
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/** Local-time calendar parts of an instant, in one named zone. */
function parts(iso: string | undefined, timeZone?: string) {
  if (!iso) return null;
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return null;
  const fields = new Intl.DateTimeFormat('en-GB', {
    day: '2-digit', month: '2-digit', year: 'numeric',
    hour: '2-digit', minute: '2-digit', hour12: false, timeZone,
  }).formatToParts(date);
  const read = (type: Intl.DateTimeFormatPartTypes) => fields.find((p) => p.type === type)?.value ?? '';
  return {
    day: String(Number(read('day'))),
    month: MONTHS[Number(read('month')) - 1] ?? '',
    year: read('year'),
    // en-GB renders midnight as "24"; a time of day never reads 24:05.
    hour: read('hour') === '24' ? '00' : read('hour'),
    minute: read('minute'),
  };
}

/** "12 Sep 13:02" — the list's started column. A relative "15m" answered a
 *  question nobody asked and hid which week's brief a row was (P-10, Q-08). */
export function formatDateTime(iso: string | undefined, timeZone?: string): string {
  const p = parts(iso, timeZone);
  return p ? `${p.day} ${p.month} ${p.hour}:${p.minute}` : '';
}

/** "12 Sep 2026" — a decision's date, where the year matters. */
export function formatDate(iso: string | undefined, timeZone?: string): string {
  const p = parts(iso, timeZone);
  return p ? `${p.day} ${p.month} ${p.year}` : '';
}

/** "3 min ago" — only for "Last updated", where staleness is the point (M-01). */
export function formatAgo(iso: string | undefined, now: number = Date.now()): string {
  if (!iso) return '';
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return '';
  const seconds = Math.max(0, Math.round((now - then) / 1000));
  if (seconds < 10) return 'just now';
  if (seconds < 60) return `${seconds} s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours} h ago`;
  return `${Math.round(hours / 24)} d ago`;
}

// ── Run identity ─────────────────────────────────────────────────────────────

/** "Weekly competitor brief · run #3", falling back to the goal sentence when
 *  the run never came from a saved workflow. */
export function runTitle(run: Pick<Run, 'workflowName' | 'runNumber' | 'goal' | 'dag'>): string {
  const name = run.workflowName?.trim();
  if (name) return run.runNumber ? `${name} · run #${run.runNumber}` : name;
  return run.goal?.trim() || run.dag || 'Workflow run';
}

/** A goal long enough that it is a paragraph, not a name. Past this the
 *  heading uses a generic label and the goal is read in "Workflow brief". */
const MAX_HEADING = 120;

/**
 * The run page's heading (Q-12).
 *
 * Quinn read a heading that said "…how we should re · run #2": a goal sentence
 * cut mid-word and then given a run number, which named nothing and looked
 * broken. The rule here is that a heading is either a NAME or it is not
 * attempted:
 *
 *   · a saved workflow's name is rendered in full, however long — it is what
 *     the person called this workflow, and shortening someone's own name to fit
 *     a column is the defect, not the fix;
 *   · a run with no saved name borrows a short goal sentence verbatim;
 *   · a long goal is a brief, not a title, so the heading falls back to a plain
 *     label and the brief is read under "Workflow brief", which is where the
 *     full text already lives.
 */
export function runHeading(input: {
  savedName?: string;
  runNumber?: number;
  brief?: string;
  fallback?: string;
}): string {
  const name = input.savedName?.trim();
  if (name) return input.runNumber ? `${name} · run #${input.runNumber}` : name;
  const brief = input.brief?.replace(/\s+/g, ' ').trim() ?? '';
  if (brief && brief.length <= MAX_HEADING) return brief;
  return input.fallback?.trim() || 'Workflow run';
}

/**
 * Newest first BY START TIME, which is the column the list shows (M-17).
 *
 * The audited list sorted by end time and displayed start time, so a 14-minute
 * run that began at 12:50 sat between rows stamped 13:16 and 12:57 and read as
 * a sorting bug. The server orders the same way; this keeps the flat list
 * honest about its own column regardless of what arrives, and ties break on the
 * run id so two runs that started in the same second never swap between
 * renders.
 */
export function sortRunsByStarted<T extends { startedAt: string; runId: string }>(runs: T[]): T[] {
  const at = (run: T) => new Date(run.startedAt).getTime() || 0;
  return [...runs].sort((a, b) => at(b) - at(a) || b.runId.localeCompare(a.runId));
}

/**
 * What a workflow CALLS an input, for a run that recorded no labels (P-18).
 *
 * `weekToCover` → "Week to cover". A fallback, not a replacement: the labels
 * frozen at launch are the workflow author's own words and always win. This is
 * what an older run gets instead of a camel-case identifier.
 */
export function humanizeSlotId(id: string): string {
  const words = id
    .replace(/[_-]+/g, ' ')
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .split(/\s+/)
    .filter(Boolean);
  if (words.length === 0) return id;
  // Sentence case, not title case: "Week to cover" is how a person writes a
  // field label. An all-caps word is left alone — CSV is not Csv.
  return words
    .map((word, index) => {
      if (word.toUpperCase() === word && word.length > 1) return word;
      const lower = word.toLowerCase();
      return index === 0 ? lower.charAt(0).toUpperCase() + lower.slice(1) : lower;
    })
    .join(' ');
}

/** One input's label: the launched label, else the humanized id (P-18). */
export function slotLabel(
  id: string,
  labels?: Record<string, string> | null,
): string {
  const label = labels?.[id];
  return typeof label === 'string' && label.trim() !== '' ? label.trim() : humanizeSlotId(id);
}

/** "Week to cover: 31 Aug–6 Sep · Competitors: Linear, Notion" — the first two
 *  inputs, which is what tells two runs of one workflow apart. Values are shown
 *  under the labels the workflow asked for them by, never under their ids. */
export function slotsSummary(
  slots: Record<string, string> | null | undefined,
  max = 2,
  labels?: Record<string, string> | null,
): string {
  const entries = Object.entries(slots ?? {}).filter(([, value]) => typeof value === 'string' && value.trim() !== '');
  if (entries.length === 0) return '';
  const shown = entries.slice(0, max).map(([key, value]) => `${slotLabel(key, labels)}: ${clip(value, 48)}`);
  const rest = entries.length - shown.length;
  return rest > 0 ? `${shown.join(' · ')} · +${rest} more` : shown.join(' · ');
}

/** "PASSED · 2 min 43 s · 428.9k in / 3.6k out · gpt-6-astra" (M-03). Every
 *  part is dropped when its data is absent rather than rendered as "—". */
export function headerMeta(input: {
  chip: string;
  durationMs?: number | null;
  usage?: { input_tokens?: number; cached_input_tokens?: number; output_tokens?: number } | null;
  model?: string;
}): string {
  const parts = [input.chip];
  const duration = formatDuration(input.durationMs);
  if (duration) parts.push(duration);
  const tokensIn = formatTokens(input.usage?.input_tokens);
  const tokensOut = formatTokens(input.usage?.output_tokens);
  if (tokensIn && tokensOut) parts.push(`${tokensIn} in / ${tokensOut} out`);
  if (input.model?.trim()) parts.push(input.model.trim());
  return parts.join(' · ');
}

/** "4 of 5 finished runs passed (2 still running)" — a share with both its
 *  numbers, because "PASS RATE 80%" hid the denominator (Q-08). */
export function passRateSentence(passed: number, finished: number, running = 0): string {
  if (finished === 0) return running > 0 ? `No runs have finished yet (${running} still running).` : 'No runs have finished yet.';
  const tail = running > 0 ? ` (${running} still running)` : '';
  return `${passed} of ${finished} finished run${finished === 1 ? '' : 's'} passed${tail}.`;
}

// ── Home / Inbox naming ──────────────────────────────────────────────────────

/** "Approve: Weekly competitor brief run #2" / "Sign off: … run #3". A one
 *  letter truncation ("APPROVAL A…") is not a name (S-06). */
export function needsYouTitle(item: {
  type: 'approval' | 'selection' | 'outcome' | 'proposal';
  workflowName?: string;
  runNumber?: number;
  title?: string;
  prompt?: string;
}): string {
  const verb =
    item.type === 'approval' ? 'Approve' :
    item.type === 'outcome' ? 'Sign off' :
    item.type === 'selection' ? 'Choose' : 'Review method';
  const name = item.workflowName?.trim() || item.title?.trim() || item.prompt?.trim() || 'a run';
  const run = item.runNumber ? ` run #${item.runNumber}` : '';
  return `${verb}: ${name}${run}`;
}

// ── Grouping the runs list by workflow (F-B) ─────────────────────────────────

export interface RunGroup {
  /** Stable identity of the workflow these runs share. */
  key: string;
  /** What to call it in the header. */
  name: string;
  /** The saved workflow's id when there is one — the header links to history. */
  workflowId?: string;
  /** Newest first; the first entry is "latest". */
  runs: Run[];
}

/** The identity two runs of one workflow share. The server's `projectWorkflow`
 *  key is authoritative; the name and then the goal stand in for runs that
 *  never came from a saved workflow. */
function workflowKey(run: Run): { key: string; name: string; workflowId?: string } {
  const identity = run.options?.projectWorkflow as { key?: string; name?: string; sourceWorkflowId?: string } | undefined;
  const workflowId = typeof identity?.sourceWorkflowId === 'string' ? identity.sourceWorkflowId : undefined;
  const name = run.workflowName?.trim() || identity?.name?.trim() || run.goal?.trim() || run.dag;
  return { key: identity?.key || name || run.runId, name: name || 'Workflow', workflowId };
}

/**
 * Group a runs list by the workflow each run came from, newest first inside
 * each group and by newest run between them.
 *
 * The point is comparison: three runs of the weekly brief belong under one
 * heading with run numbers, not scattered through a flat list where they read
 * identically (P-10, S-02, M-06).
 */
export function groupRunsByWorkflow(runs: Run[]): RunGroup[] {
  const groups = new Map<string, RunGroup>();
  for (const run of runs) {
    const { key, name, workflowId } = workflowKey(run);
    const group = groups.get(key) ?? { key, name, workflowId, runs: [] };
    if (!group.workflowId && workflowId) group.workflowId = workflowId;
    group.runs.push(run);
    groups.set(key, group);
  }
  const started = (run: Run) => new Date(run.startedAt).getTime() || 0;
  for (const group of groups.values()) group.runs = sortRunsByStarted(group.runs);
  return [...groups.values()].sort((a, b) => started(b.runs[0]) - started(a.runs[0]));
}

/** The Home chip beside that title — "APPROVE" / "SIGN OFF" / … */
export const needsYouChip = (type: 'approval' | 'selection' | 'outcome' | 'proposal'): string =>
  type === 'approval' ? 'APPROVE' : type === 'outcome' ? 'SIGN OFF' : type === 'selection' ? 'CHOOSE' : 'LEARNED';
