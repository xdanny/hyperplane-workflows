import { readWorkflowValidation } from './workflowValidation';
import type { WorkflowDefinition, WorkflowValidationResult } from './workflowValidation';
import { validInputTables } from './checkInputTables';
import type { CheckInputTable } from './checkInputTables';
/* Hyperplane API client — typed fetch wrapper for the Go backend */

export type RunState = 'pending' | 'running' | 'passed' | 'failed' | 'blocked' | 'interrupted';

export interface Run {
  runId: string;
  dag: string;
  goal?: string;
  /** The project this run is scoped to; absent/'' means unscoped. */
  project?: string;
  state: RunState;
  mode?: string;
  type?: string;
  riskLevel?: string;
  startedAt: string;
  endedAt?: string;
  updatedAt: string;
  ownerPid?: number;
  parentRun?: string;
  options?: Record<string, unknown>;
  summary?: Record<string, unknown>;
  metadata?: Record<string, unknown>;
  doc?: Record<string, unknown>;
  /**
   * The node that explains an unhappy ending — set by the run LIST for runs that
   * failed, blocked or were interrupted, so a row can read "failed at
   * report-verify" before anyone opens it. Absent on a run that passed.
   */
  failingNode?: string;
  /**
   * What to CALL the recipe this run came from — resolved server-side on the
   * run DETAIL read, from the one recipe-name resolver. `doc.recipe` is the
   * template's uuid and stays there for the hover.
   */
  recipeName?: string;

  // ── Supervision fields (round 11 contract; SDK runs only) ─────────────────
  // Every one of these is resolved SERVER-SIDE and carried by both the list and
  // the detail read, so a row and a header never disagree about which run this
  // is or what stopped it. Absent on non-SDK rows, which keep `failingNode`.

  /** projectWorkflow.name — what to CALL this run. */
  workflowName?: string;
  /** 1-based ordinal among the runs sharing this workflow key in this project. */
  runNumber?: number;
  /** endedAt − startedAt in milliseconds; null/absent while still running. */
  durationMs?: number | null;
  /** The run-time input values this run was launched with. */
  slots?: Record<string, string> | null;
  /**
   * What the saved workflow CALLED each of those inputs, frozen at launch
   * (`request.slotLabels`). A value is meaningless under a machine id:
   * "weekToCover: 31 Aug–6 Sep" is a row nobody reads as "Week to cover"
   * (P-18). Absent for a run launched before labels were frozen, which is what
   * `slotLabel` falls back from.
   */
  slotLabels?: Record<string, string> | null;
  /** The sign-off policy this run was LAUNCHED under, frozen in its own record
   *  so a later edit to the saved workflow never rewrites what an old run asked
   *  for (M-11). */
  signOff?: SignOffPolicy;
  /**
   * The SDK step whose checkpoint phase is failed or rejected — the step a
   * person would open, never the internal node id "delivery" (M-12). Null when
   * nothing failed.
   */
  failingStep?: { id: string; title: string; kind?: string } | null;
  /**
   * The run is running, but nothing is executing: it is parked on an approval
   * (checkpoint.phase === "approval_pending"). The store still says `running`;
   * only the presentation changes, which is what stops a list from reading
   * RUNNING while the platform waits on a person (Q-03, M-11).
   */
  waitingForPerson?: boolean;
}

/** The three verdicts a human can record on a finished run's delivery. */
export type OutcomeVerdict = 'accepted' | 'changes_requested' | 'rejected';

/**
 * What a saved workflow asks of a person when one of its runs finishes (M-11).
 *
 *   required             — today's behaviour: every finished run is queued.
 *   optional             — decidable, but never queued. The Inbox, Home's
 *                          "Needs you" and the journey's `needsDecision` all
 *                          leave it alone.
 *   auto_when_checks_pass — a PASSED run whose declared checks ALL passed is
 *                          accepted by the server itself. One declared check
 *                          minimum; anything else is queued like "required".
 *
 * The policy is frozen into the launch record, so editing a workflow never
 * rewrites what an old run asked for.
 */
export type SignOffPolicy = 'required' | 'optional' | 'auto_when_checks_pass';

export const SIGN_OFF_POLICIES: SignOffPolicy[] = ['required', 'optional', 'auto_when_checks_pass'];

/** Reads a sign-off policy off the wire, defaulting to today's behaviour.
 *  An unknown value is NOT invented into a weaker policy: anything the client
 *  does not recognise reads as "required", the policy that still asks. */
export function readSignOff(value: unknown): SignOffPolicy {
  return value === 'optional' || value === 'auto_when_checks_pass' ? value : 'required';
}

/** `decidedBy` on an outcome the SERVER recorded, never a person. Every surface
 *  that renders an acceptance checks this before attributing it to anybody. */
export const AUTO_DECIDER = 'hyperplane:auto';

/**
 * A recorded Outcome Decision — the human's final call on what a run delivered.
 * Stored server-side as a decision record and INSERT-ONLY: once written, a
 * different verdict requires deleting the decision, so this never silently
 * changes under the reader.
 */
export interface RunOutcome {
  runId: string;
  verdict: OutcomeVerdict;
  note?: string;
  /** Who recorded it: 'local' for the person at this keyboard, `AUTO_DECIDER`
   *  for an acceptance the server recorded under a workflow's sign-off policy.
   *  The distinction is not decoration — an automatic acceptance is never a
   *  person's judgment (M-11). */
  decidedBy: string;
  decidedAt: string;
  decisionId: string;
  /** Declared checks that passed, of how many were declared, when an automatic
   *  acceptance says what it rests on. The record states them in its note; a
   *  surface with the delivery's tally in hand supplies them here instead. */
  checksPassed?: number;
  checksDeclared?: number;
}

// ── Journeys — the delivery's story across runs ────────────────────────────
//
// A journey is DERIVED, never stored: the server recomputes it from runs, their
// doc, node verdicts and decision records on every read. So none of these types
// has an editable field, and nothing in the app writes one — except the single
// thing a human owns, the verdict.

/** Where a delivery has got to. Delivery language, not board language. */
export type JourneyStage =
  | 'building'
  | 'verifying'
  | 'needs_decision'
  /** The newest attempt was a REHEARSAL. It finished, but a dry run produced
   *  nothing, so there is no delivery under it to accept — both outcome
   *  endpoints refuse an acceptance on one. Its own stage rather than
   *  needs_decision, because a card that asks for a verdict the server will
   *  refuse is asking for busywork. */
  | 'rehearsed'
  | 'stopped'
  | 'accepted'
  | 'changes_requested'
  | 'rejected';

/** Which attempts a recorded delivery verdict was pinned to.
 *
 *  A verdict covers the attempts that EXISTED when it was written — the runs it
 *  named in its refs, or (for a record that named none) the ones that started
 *  before it. Attempts after that are undecided, however green the delivery
 *  looked when it was signed off. */
export interface JourneyCoverage {
  /** Chapter numbers the verdict covered, ascending. */
  attempts: number[];
  /** Chapter numbers newer than the verdict — undecided. */
  undecided: number[];
  /** True when the delivery's current state is the state that was decided on. */
  coversNewest: boolean;
  /** 'refs' when the decision named its attempts, 'time' when its timestamp
   *  had to stand in. */
  basis: 'refs' | 'time';
  decidedAt: string;
}

/** How much this delivery has actually PROVEN. Computed from reality: three
 *  rungs, no hand-typed field. */
export type JourneyMaturity = 'unproven' | 'proven_once' | 'accepted';

/** A recorded verdict. `scope` says at which grain: 'journey' is the decision
 *  the inbox asks for, 'run' is an optional annotation on one attempt. */
export interface JourneyOutcome {
  verdict: OutcomeVerdict;
  note?: string;
  decidedAt: string;
  decisionId: string;
  scope: 'journey' | 'run';
  /** Set when scope is 'run' — which attempt carries the annotation. */
  runId?: string;
  /** Who recorded it. `AUTO_DECIDER` means the SERVER did, under the workflow's
   *  `auto_when_checks_pass` policy — never a person, and never grounds for
   *  extracting a skill (M-11). */
  decidedBy?: string;
  /** How many declared checks passed, of how many were declared, at the moment
   *  the verdict was recorded. Present on an automatic acceptance, which has to
   *  say what it accepted ON: "accepted automatically · 1/1 checks". */
  checksPassed?: number;
  checksDeclared?: number;
}

/** One execution in a delivery, with its server-computed check counts. */
export interface JourneyAttempt {
  runId: string;
  /** 1 is the oldest attempt. */
  number: number;
  dag: string;
  goal?: string;
  state: RunState;
  recipe?: string;
  project?: string;
  /** Source run of an explicitly reviewed SDK continuation. */
  continuedFrom?: string;
  /** Latest committed SDK phase, when available. */
  phase?: string;
  /** A newer execution now represents this delivery. */
  superseded: boolean;
  /** True when this attempt was a dry run: every agent node answered with a
   *  stand-in and every approval gate simulated its verdict. `criteria`/`proven`
   *  below are counted the same way either way, so this is the only thing that
   *  keeps "4/4 proven" from being a lie. */
  rehearsal?: boolean;
  criteria: number;
  proven: number;
  failingNode?: string;
  startedAt?: string;
  endedAt?: string;
  updatedAt: string;
  outcome?: JourneyOutcome;
}

/** One line of the journey's narrative. Deliberately inert — text, a time and a
 *  run to link to. No actions live here; the decision has one home. */
export interface JourneyEvent {
  at: string;
  kind: string;
  runId?: string;
  text: string;
}

/** One delivery line's whole story. */
export interface Journey {
  key: string;
  project?: string;
  name: string;
  dag: string;
  recipe?: string;
  /** What to CALL that recipe — resolved server-side by the one recipe-name
   *  resolver. Render this; keep `recipe` for the hover. */
  recipeName?: string;
  root: string;
  stage: JourneyStage;
  maturity: JourneyMaturity;
  /** Chapters, newest first. */
  attempts: JourneyAttempt[];
  attemptCount: number;
  /** The CURRENT state of the product — the newest attempt's tally, not a sum.
   *  Computed once by the server (internal/journey/tally.go); never re-derived
   *  here, because a second derivation is how two surfaces started quoting
   *  different attempts of one delivery. */
  criteria: number;
  proven: number;
  /** Which chapter that tally was counted from, so a chip can say so. */
  provenAttempt?: number;
  /** True when EVERY attempt was a rehearsal — the delivery has never actually
   *  been produced, so it cannot be accepted (the server returns 409). */
  rehearsal?: boolean;
  /** How many criteria the latest attempt cleared with stand-ins. */
  standinProven?: number;
  /** The GOVERNING verdict — absent when the recorded one covers only earlier
   *  attempts. */
  outcome?: JourneyOutcome | null;
  /** The recorded delivery verdict and what it was pinned to. */
  recordedOutcome?: JourneyOutcome | null;
  coverage?: JourneyCoverage | null;
  needsDecision: boolean;
  /** The sign-off policy the newest attempt was LAUNCHED under (M-11). An
   *  "optional" delivery is still decidable here and still listed under
   *  Decided — it is simply never queued, so `needsDecision` is false and the
   *  stage reads "done". */
  signOff?: SignOffPolicy;
  startedAt?: string;
  updatedAt: string;
  timeline: JourneyEvent[];

  // ── Supervision context (round 11 contract) ───────────────────────────────
  /** The saved workflow this delivery came from, and the newest attempt's
   *  ordinal within it — so a sign-off card can be titled by a name and a
   *  number rather than a hash (S-06, Q-04). */
  workflowName?: string;
  runNumber?: number;
  /** The final produced document of the newest attempt. */
  resultDocument?: { title: string; url: string } | null;
  /** The first FAIL finding of the newest attempt, untruncated (Q-04). */
  failureReason?: string;
}

/** The journey context embedded in a run detail response: which delivery this
 *  run is a chapter of, and where that delivery stands. */
export interface RunJourneyRef {
  key: string;
  name: string;
  stage: JourneyStage;
  maturity: JourneyMaturity;
  attemptCount: number;
  /** This run's chapter number. */
  attempt: number;
  /** The DELIVERY's proof count and the attempt it came from — the same numbers
   *  the deliveries view shows, so the delivery-grain chip on a superseded
   *  attempt stops counting the chapter in front of it. */
  criteria: number;
  proven: number;
  /** How many criteria the latest attempt cleared with STAND-INS rather than
   *  proof — non-zero only for a rehearsal, where `proven` is 0. */
  standinProven?: number;
  provenAttempt?: number;
  superseded: boolean;
  /** The verdict that governs THIS attempt, and only this attempt. Absent when
   *  the delivery's recorded verdict was pinned to earlier attempts — it is
   *  still on record as `recordedOutcome`, it just does not speak for work it
   *  never saw. */
  outcome?: JourneyOutcome | null;
  /** The delivery-grain verdict AS RECORDED, whether or not it reaches this
   *  attempt, and the attempts it was pinned to. */
  recordedOutcome?: JourneyOutcome | null;
  coverage?: JourneyCoverage | null;
  /** True when THIS attempt was a dry run. */
  rehearsal?: boolean;

}

export interface RunWithNodes {
  run: Run;
  nodes: NodeRun[];
  /** This RUN's verdict — an optional annotation, null until someone records
   *  one. The decision that is actually asked for lives on the journey. */
  outcome?: RunOutcome | null;
  /** The delivery this run is a chapter of. */
  journey?: RunJourneyRef | null;

}

export interface NodeRun {
  runId: string;
  nodeId: string;
  kind: string;
  role?: string;
  state: RunState;
  attempt?: number;
  score?: number | null;
  error?: string | null;
  output?: unknown;
  updatedAt: string;
  doc?: Record<string, unknown>;
}

export interface Artifact {
  media?: string;
  name: string;
  url: string;
  size?: number;
}

export interface Skill {
  id: string;
  name: string;
  description?: string;
  category?: string;
  reasoning?: string;
  instruction?: string;
  metadata?: Record<string, unknown>;
  updatedAt?: string;
}

/** Skill lifecycle. Extraction writes 'draft'; approval writes 'active'.
 *  A skill with no status in its metadata is active. */
export type SkillStatus = 'draft' | 'active';

/** Reads a skill's lifecycle status out of its metadata jsonb. */
export function skillStatus(skill: Skill): SkillStatus {
  const raw = skill.metadata?.status;
  return raw === 'draft' ? 'draft' : 'active';
}

export function isDraftSkill(skill: Skill): boolean {
  return skillStatus(skill) === 'draft';
}

/** One pinned excerpt behind a learned skill: the node that said it, and what
 *  it said. The (runId, nodeId) pair is what opens the run on that node, so a
 *  quote can always be checked against its source. */
export interface SkillEvidence {
  runId?: string;
  nodeId: string;
  quote: string;
}

/** The method codex proposed from a run (schemas/skill-extract.json). */
export interface SkillProposal {
  id: string;
  title: string;
  domain: string;
  instruction: string;
  doc: string;
  tags: string[];
  /** Codex's own 0..1 score. Deliberately not rendered: an unexplained scalar
   *  reads as rigour it does not have. Support count + evidence are shown. */
  confidence: number;
  rationale: string;
  evidence?: SkillEvidence[];
}

/** An existing skill that already covers a proposed method. */
export interface SkillDuplicateMatch {
  skillId: string;
  name: string;
  status: string;
  similarity: number;
  advice: string;
}

/** Verdict of POST /api/runs/{id}/extract-skill */
export interface SkillExtractResult {
  runId: string;
  status: 'drafted' | 'duplicate';
  skill?: Skill;
  proposal?: SkillProposal;
  duplicate?: SkillDuplicateMatch;
  /** How many distinct runs now stand behind the skill this landed on. */
  supportRuns?: number;
  message: string;
}

// ── Audit personas ───────────────────────────────────────────────────────────
//
// A persona is a registered audit charter (see `personas/*.md` and
// `personas/README.md`) — an adversarial professional reader with a bar, invoked
// to review a skill draft/refinement or a finished run's delivery. A review is
// ALWAYS advisory (spec AI-12): it never gates Approve and never replaces the
// evalset requirement. It is evidence placed next to the measured result for
// the human who decides.

/** What a persona's charter names itself competent over. `domains` is a
 *  free-text claim for display only — never routed on, unlike `skills`/
 *  `processes`, which the charter named explicitly. */
export interface PersonaCompetence {
  skills?: string[];
  processes?: string[];
  domains?: string[];
  /** Priya's claim: competent to judge the WORDS of any instruction, regardless
   *  of domain. */
  wording?: boolean;
  /** `domains`, said the way the charter said it: one whole sentence per
   *  free-text bullet. Render these — the short labels in `domains` joined by
   *  commas read as a run-on, because the claims contain commas of their own.
   *  See Competence.Bullets in internal/persona/charter.go. */
  bullets?: string[];
}

export interface PersonaView {
  id: string;
  name: string;
  bar?: string;
  competence: PersonaCompetence;
  version?: string;
  source?: string;
  /** The full charter markdown — present only on GET /api/personas/{id}. */
  charter?: string;
  /** How many verdicts this persona has recorded, across every target. */
  reviews: number;
  /** Why this persona matched — present only on a `?competentFor=` query. */
  competentFor?: string;
}

/** One verbatim quote a review pinned to a place in the evidence it read. */
export interface PersonaCitation {
  ref: string;
  quote: string;
}

/** One recorded verdict. `approve`/`reject`/`revise` are the SKILL-target
 *  verdicts (the charters' own three); `pass`/`fail`/`mixed` are the RUN-target
 *  verdicts — "approve" would be a category error there, since nobody is
 *  approving a finished delivery, only saying whether it holds up. */
export interface PersonaReview {
  id: string;
  persona: string;
  personaName?: string;
  bar?: string;
  charterVersion?: string;
  /** `"skill:<id>"` or `"run:<id>"`. */
  target: string;
  targetKind: 'skill' | 'run';
  targetId: string;
  targetTitle?: string;
  model?: string;
  verdict: 'approve' | 'reject' | 'revise' | 'pass' | 'fail' | 'mixed';
  summary: string;
  reasons?: string[];
  citations?: PersonaCitation[];
  /** On a 'revise' verdict: the exact replacement text, written out in full so
   *  a human can apply it without re-deriving it. */
  revisedWording?: string;
  /** Always true — recorded on the record itself so a reader of the raw row
   *  sees it too, not just this UI. */
  advisory: boolean;
  reviewedAt: string;
}

/**
 * An OUTCOME BRIEF — the platform's own plain-language account of what a run or
 * a delivery actually produced, written AFTER it finished.
 *
 * Every other piece of prose in a run was written by a node INSIDE the DAG,
 * before the gate that judged it and before any human decided anything, so none
 * of it can state an outcome. This can.
 *
 * The two flags are not decoration and must always be rendered:
 *   - `grounded: false` means the brief carries a number the platform could not
 *     find in the state it was written from. It is still shown, and badged.
 *   - `engine: 'fallback'` means the platform wrote it deterministically rather
 *     than the engine writing it: fewer facts, no interpretation.
 */
export interface OutcomeBriefData {
  id: string;
  subjectKind: 'run' | 'delivery' | 'project';
  subjectId: string;
  project?: string;
  whatItBuilds?: string;
  whatHappened?: string;
  trueOutcome?: string;
  nextSteps?: string;
  grounded: boolean;
  /** The numeric tokens the platform could not verify, so the badge can name them. */
  ungroundedNumbers?: string[];
  engine: 'codex' | 'fallback';
  createdAt: string;
}

/** Raw shape from the server — do NOT use directly in components */
interface RawApproval {
  id: string;
  type?: string;
  state?: string;
  gateKey?: string;
  runId?: string;
  kind?: string;
  consumed?: boolean;
  createdAt?: string;
  updatedAt?: string;
  doc?: {
    title?: string;
    prompt?: string;
    candidates?: unknown;
    compareBy?: unknown;
    winner?: string;
    decision?: string;
    dag?: string;
    gateNode?: string;
    evidence?: unknown;
    /** Round-11 supervision context — see InboxItem's same-named fields. */
    workflowName?: string;
    runNumber?: number;
    goal?: string;
    nextSteps?: string[];
    draftDocument?: { path: string; url: string };
    resultDocument?: { title: string; url: string };
    failureReason?: string;
    documents?: { name: string; url: string; sha256: string }[];
  };
}

/** A candidate is arbitrary content; components pick title/label + a summary. */
export type Candidate = Record<string, unknown>;

// The inbox's "needs decision" entries are Journeys (see the Journey type) —
// one per delivery, not one per run.

/** Flattened inbox item that components use. The queue is unified: mid-run
 *  gates ('approval'/'selection'), end-of-run deliveries ('outcome') and the
 *  skills the platform proposed to itself after an acceptance ('proposal') are
 *  all "your turn". */
export interface InboxItem {
  documents?: { name: string; url: string; sha256: string }[];
  id: string;
  type: 'approval' | 'selection' | 'outcome' | 'proposal';
  runId?: string;
  dag?: string;
  gateNode?: string;
  title?: string;
  prompt?: string;
  candidates?: Candidate[];
  /** Optional field keys to compare side-by-side in the candidate table */
  compareBy?: string[];
  evidence?: Record<string, unknown>;
  state?: string;
  createdAt?: string;
  updatedAt?: string;
  /** outcome items only — the newest attempt's terminal state. */
  runState?: RunState;
  /** outcome items only — recipe id + criteria count when the run has them. */
  recipe?: string;
  criteriaCount?: number;
  /** outcome items only — the whole delivery the card is asking about. One card
   *  per journey, listing every attempt; superseded attempts never get a card
   *  of their own. */
  journey?: Journey;
  /** proposal items only — the DRAFT skill extraction proposed after a delivery
   *  was accepted. It is inert until approved: never in a prompt, never offered
   *  to the composer. */
  proposal?: Skill;

  // ── Supervision context (round 11 contract) ───────────────────────────────
  // A card that names nothing but a hash asks a question nobody can answer
  // (Q-04, S-06, P-08). These are resolved server-side so the Inbox never has
  // to fetch a run per card to say which brief it is talking about.

  /** The saved workflow's name, when the run came from one. */
  workflowName?: string;
  /** This run's ordinal within that workflow. */
  runNumber?: number;
  /** approval items — the run's goal sentence, as the card's context line. */
  goal?: string;
  /** approval items — titles of the steps that WAIT on this approval. Empty
   *  means nothing waits, which is itself worth saying out loud. */
  nextSteps?: string[];
  /** approval items — the document the decision is actually about, rendered on
   *  the card above the buttons rather than linked below them (Q-13, P-08). */
  draftDocument?: { path: string; url: string };
  /** outcome items — the final produced document of the delivery. */
  resultDocument?: { title: string; url: string };
  /** outcome items — the first FAIL finding, untruncated (Q-04). */
  failureReason?: string;
  /** outcome items — the sign-off policy the run was launched under. The queue
   *  never carries an "optional" delivery; this is here so a card that arrives
   *  through another route can still say which policy it is under (M-11). */
  signOff?: SignOffPolicy;
  /** The launched labels for `slots`, so a value is never shown under its id. */
  slotLabels?: Record<string, string> | null;
}

// ── Workflow history (F-C) ───────────────────────────────────────────────────

/** One row of a saved workflow's run history — GET /api/workflows/{id}/runs. */
export interface WorkflowRunRow {
  runId: string;
  runNumber: number;
  startedAt: string;
  endedAt?: string;
  durationMs?: number | null;
  state: RunState;
  waitingForPerson?: boolean;
  slots?: Record<string, string> | null;
  /** How many checks the run DECLARED, and how many of them passed. Two
   *  numbers, never one percentage: "1 of 2" and "50%" read differently when
   *  the denominator is 2. */
  checksDeclared?: number;
  checksPassed?: number;
  failingStep?: { id: string; title: string; kind?: string } | null;
  /** The launched labels for `slots` (P-18). */
  slotLabels?: Record<string, string> | null;
  /** The verdict recorded on this run. `decidedBy: AUTO_DECIDER` means the
   *  server recorded it under `auto_when_checks_pass` — a policy, not a person
   *  (M-11), and every surface says so rather than crediting somebody. */
  outcome?: {
    verdict: OutcomeVerdict; decidedAt: string; note?: string; decidedBy?: string;
    checksPassed?: number; checksDeclared?: number;
  } | null;
  /** The sign-off policy THIS run was launched under, frozen at launch. */
  signOff?: SignOffPolicy;
  usage?: { inputTokens: number; cachedInputTokens: number; outputTokens: number } | null;
  /** The documents the run's final steps recorded. `kind` is the declared step
   *  kind, because a terminal review records a document too and a compare view
   *  that diffs the last one diffs the review, not the deliverable (S-16). */
  documents?: { stepId: string; title: string; kind?: string; path: string; bytes?: number; sha256?: string }[];
  /**
   * EVERY checkpoint file of every step, documents included (M-18).
   *
   * `documents` is the narrative thread the compare view diffs; this is the
   * full produced set, which is what makes "did the totals file change?"
   * answerable without opening two runs. Compared by sha256 only — the compare
   * view never claims to know what changed inside a binary.
   */
  files?: { stepId: string; stepTitle: string; path: string; bytes?: number; sha256?: string }[];
}

/** A run that COULD be attached to a saved workflow — same project, same
 *  definition key, and no saved source of its own yet (P-16). */
export interface AttachableRun {
  runId: string;
  startedAt: string;
  state: RunState;
  /** Why it qualifies, in the server's words — rendered as-is beside the row. */
  reason: string;
}

/** POST /api/workflows/{id}/attach-runs — what was rewritten and what was not. */
export interface AttachRunsResult {
  attached: string[];
  skipped: { runId: string; reason: string }[];
}

/** GET /api/workflows/{id}/runs — the saved workflow plus its runs, newest first. */
export interface WorkflowRunsResult {
  workflow: SDKWorkflow;
  runs: WorkflowRunRow[];
  total: number;
  /** Earlier runs of the same definition that never named this saved workflow
   *  (P-16). Absent on a server that does not offer them yet. */
  attachable?: AttachableRun[];
}

/** Where a skill came from, read off its metadata. Extraction stamps the run
 *  and (when an acceptance triggered it) the delivery; both are absent on a
 *  hand-authored skill. */
export interface SkillLineage {
  sourceRun?: string;
  sourceJourney?: string;
  /** Human name of the delivery this was learned from. */
  sourceDelivery?: string;
  learnedAt?: string;
  /** Every run that reached this method. A second run landing on the same
   *  draft appends to it rather than being discarded as a duplicate. */
  sourceRuns: string[];
  /** sourceRuns.length — how much is actually behind this skill. This is what
   *  a card shows in place of the old `confidence` scalar: a self-reported
   *  0.90 with no explanation is not evidence, and one run is an anecdote. */
  supportRuns: number;
  /** The excerpts the extraction pinned as load-bearing. */
  evidence: SkillEvidence[];
  /** True when someone used HYPERPLANE_APPROVE_UNEVALED=1 to approve this
   *  learned skill without a practice test. */
  approvedWithoutEvalset: boolean;
  /** True when every run this was learned from was a REHEARSAL — a dry run whose
   *  agent nodes answered with stand-ins. Extraction refuses these now, so only
   *  drafts made before that rule can carry it; what it changes is what the card
   *  is allowed to claim. Computed server-side from the run's mode. */
  sourceRunRehearsal: boolean;
  /** The evalset a change to this draft has to beat, stamped at extraction from
   *  the project the source run belonged to. Absent when that project declares
   *  no regression policy — and present does not mean enforced: nothing
   *  re-scores the draft against it yet, which is why every surface renders it
   *  through regressionPolicySentence rather than writing its own words. */
  regressionPolicy?: { evalsetId?: string; minMargin?: string; project?: string };
}

export function skillLineage(skill: Skill): SkillLineage {
  const meta = skill.metadata ?? {};
  const str = (k: string): string | undefined =>
    typeof meta[k] === 'string' && meta[k] !== '' ? (meta[k] as string) : undefined;

  // sourceRuns is the newer, list-shaped truth; sourceRun is the first run and
  // is still written, so a draft extracted before support counting reads
  // correctly here rather than reporting zero support.
  const runs = Array.isArray(meta.sourceRuns)
    ? (meta.sourceRuns as unknown[]).filter((r): r is string => typeof r === 'string' && r !== '')
    : [];
  const sourceRun = str('sourceRun');
  const sourceRuns = sourceRun && !runs.includes(sourceRun) ? [...runs, sourceRun] : runs;

  const evidence = Array.isArray(meta.evidence)
    ? (meta.evidence as unknown[]).flatMap((raw) => {
        if (!raw || typeof raw !== 'object') return [];
        const e = raw as Record<string, unknown>;
        if (typeof e.nodeId !== 'string' || typeof e.quote !== 'string') return [];
        return [{
          runId: typeof e.runId === 'string' && e.runId !== '' ? e.runId : undefined,
          nodeId: e.nodeId,
          quote: e.quote,
        }];
      })
    : [];

  // The policy is a nested object stamped by the server. A cell that is not a
  // non-empty string is dropped rather than rendered, so a malformed stamp
  // shows nothing instead of "[object Object]".
  const rawPolicy = meta.regressionPolicy;
  let regressionPolicy: SkillLineage['regressionPolicy'];
  if (rawPolicy && typeof rawPolicy === 'object' && !Array.isArray(rawPolicy)) {
    const p = rawPolicy as Record<string, unknown>;
    const cell = (k: string): string | undefined =>
      typeof p[k] === 'string' && (p[k] as string).trim() !== '' ? (p[k] as string) : undefined;
    const picked = { evalsetId: cell('evalsetId'), minMargin: cell('minMargin'), project: cell('project') };
    if (picked.evalsetId || picked.minMargin || picked.project) regressionPolicy = picked;
  }

  return {
    sourceRun,
    sourceJourney: str('sourceJourney'),
    sourceDelivery: str('sourceDelivery'),
    learnedAt: str('learnedAt') ?? str('extractedAt'),
    sourceRuns,
    supportRuns: sourceRuns.length,
    evidence,
    approvedWithoutEvalset: meta.approvedWithoutEvalset === true,
    sourceRunRehearsal: meta.sourceRunRehearsal === true,
    regressionPolicy,
  };
}

/**
 * What the project's regression policy means for THIS draft, said without
 * overclaiming.
 *
 * A policy names the evalset a change has to beat and the margin it has to win
 * by. Wiring the reference in is not the same as scoring against it, and the
 * sentence says so in its second half: an approver who reads "must beat
 * churn-holdout by 0.02 PR-AUC" and nothing else would reasonably assume
 * something checked. Nothing does yet.
 *
 * Returns undefined when neither half is declared — there is then no policy to
 * report, and a line saying so would be noise on every card.
 */
export function regressionPolicySentence(lineage: SkillLineage): string | undefined {
  const policy = lineage.regressionPolicy;
  if (!policy) return undefined;
  const { evalsetId, minMargin } = policy;
  const who = policy.project ?? 'the project';
  const caveat = `and nothing re-scores this draft against it yet: the reference is wired, the scoring is not.`;
  if (evalsetId && minMargin) {
    return `must beat ${evalsetId} by ${minMargin} — ${who} declares it, ${caveat}`;
  }
  if (evalsetId) {
    return `must beat ${evalsetId} — ${who} declares the evalset but no margin, so by how much is undeclared; ${caveat}`;
  }
  if (minMargin) {
    return `must win by ${minMargin} — ${who} declares the margin but names no evalset, so what it must beat is undeclared; ${caveat}`;
  }
  return undefined;
}

/** What a learned draft may honestly claim about where it came from.
 *
 *  A draft extracted from a REHEARSAL used to read "Learned from a passed run
 *  you accepted" — three false claims in seven words: nothing passed for real,
 *  nobody accepted it, and the run planned a graph rather than doing the work.
 *  Every surface that renders provenance asks here instead of writing its own
 *  sentence. */
export function learnedProvenanceSentence(lineage: SkillLineage): string {
  if (lineage.sourceRunRehearsal) {
    return 'Learned from a REHEARSAL — its outputs were stand-ins; this draft has no real evidence';
  }
  if (lineage.sourceDelivery) return `Learned from the delivery you accepted: ${lineage.sourceDelivery}`;
  return 'Learned from a passed run you accepted';
}

/** How much is behind a learned skill, said without overstating it. One run is
 *  an anecdote and the label says so; a hand-written skill has no support count
 *  to report at all. */
export function supportLabel(lineage: SkillLineage): string | undefined {
  if (lineage.supportRuns === 0) return undefined;
  if (lineage.supportRuns === 1) return 'extracted from a single run — treat as an anecdote until supported';
  return `extracted from ${lineage.supportRuns} runs`;
}

/** What to call the thing a skill was learned from: the delivery when an
 *  acceptance triggered the extraction, else the run itself. */
export function learnedFromLabel(skill: Skill): string | undefined {
  const { sourceDelivery, sourceRun } = skillLineage(skill);
  if (sourceDelivery) return sourceDelivery;
  if (sourceRun) return `run …${sourceRun.slice(-8)}`;
  return undefined;
}

// ── Inspection types (M14) ─────────────────────────────────────────────────

/**
 * Which sidecar of a node the log route should serve: its codex log, or the
 * full verdict preserved when the failure digest's 32 KB cap bit. The server
 * holds the matching allowlist — a caller never names a file suffix.
 */
export type LogFileKind = 'log' | 'verdict';

/** One node's log text plus what the server had to leave out. */
export interface NodeLog {
  text: string;
  /** Full on-disk size in bytes, even when `text` is only a tail of it. */
  size: number;
  /** True when `text` is less than the whole file (tail= or the byte cap). */
  truncated: boolean;
}

export interface Health {
  ok: boolean;
  db: string;
  codex: string;
  localEngine?: string;
  time: string;
  detail?: string;
}

export interface AuthStatus {
  authRequired: boolean;
  authed: boolean;
}

export interface StarterBrief {
  id: string;
  category: string;
  title: string;
  brief: string;
  /**
   * The seeded recipe that asks for the same facts as separate labelled fields.
   * When set, the chip opens that form instead of pasting prose (Q-24, Q-25).
   */
  recipe?: string;
}

export interface KnowledgeGraphNode {
  id: string;
  kind: string;
  label: string;
  status?: string;
  community: number;
  degree: number;
}
export interface KnowledgeGraphEdge {
  from: string;
  to: string;
  kind: string;
}
export interface KnowledgeGraph {
  nodes: KnowledgeGraphNode[];
  edges: KnowledgeGraphEdge[];
  communities: number;
  counts?: Record<string, number>;
}

export interface Decision {
  id: string;
  title: string;
  /**
   * The ADR lifecycle of the RECORD — "this decision stands" — NOT the verdict.
   * Every outcome row is written with status "accepted", which is why a ledger
   * that rendered this field showed a rejection as accepted (S-13, D-15).
   * Render `decision` as the verdict; see decisionLedger.ts.
   */
  status: string;
  context?: string;
  /** The verdict: 'accepted' | 'changes_requested' | 'rejected' for an outcome
   *  decision, free text for a hand-written architecture decision. */
  decision?: string;
  why?: string;
  alternatives?: string;
  consequences?: string;
  tags?: string[];
  refs?: string[];
  project?: string;
  source?: string;
  /** Who recorded it: 'local' for the person at this keyboard, `AUTO_DECIDER`
   *  when the server recorded it under a sign-off policy. */
  decidedBy?: string;
  decidedAt?: string;
  createdAt: string;
  updatedAt: string;
  /** The run this decision closed, and the name of the work — resolved
   *  server-side so a ledger row never has to show `journey:j-871c…` (S-13). */
  runId?: string;
  workflowName?: string;
  runNumber?: number;
}

/**
 * The human-owned "why this project exists" block. "What this project is for"
 * is deliberately NOT here — it is `Project.summary`, the line that heads the
 * project page, and one sentence must not have two homes.
 */
export interface PurposeDecl {
  owner?: string;
  /** ISO date, YYYY-MM-DD. */
  due?: string;
  doneMeans?: string;
  constraints?: string;
}

/**
 * How one dataset is loaded. `full` replaces the whole table every run;
 * `incremental` appends the rows past a WATERMARK — the high-water value the
 * previous run reached in `watermarkColumn`.
 *
 * The last two cells are the ones no reconciliation can guess: how long a row
 * that belongs to a closed period is still expected to turn up, and what
 * happens to a figure that has already been reported when one does.
 */
export const LOAD_MODES = ['full', 'incremental'] as const;

export interface LoadSemanticsDecl {
  mode?: 'full' | 'incremental';
  watermarkColumn?: string;
  lateArrivalWindow?: string;
  restatementRule?: string;
}

/**
 * One dataset the project works on. Only `name` is ever required: "the Looker
 * report Paid Social Q2" and nothing else is a complete entry, and an engineer
 * fills in the technical half later.
 *
 * `location` is a REFERENCE — a path, a table, an export. Never a credential.
 */
export interface DatasetDecl {
  id?: string;
  name?: string;
  location?: string;
  format?: string;
  /**
   * The ROLE this dataset plays: what is being measured, a reference table
   * joined onto it, or a downstream extract. Inheritance used to be positional
   * — datasets[0] became the baseline window and datasets[1] the current one —
   * so a 265-row zone lookup was offered as the window under test against a
   * 24M-row trip feed. A lookup or an export is never offered as a window.
   */
  kind?: 'fact' | 'lookup' | 'export';
  /**
   * Which side of a two-window comparison this stands for. `both` is the
   * ordinary answer: a shift audit compares two PERIODS of the same data.
   */
  window?: 'baseline' | 'current' | 'both';
  /** The two periods a `both` dataset covers. */
  baselinePeriod?: string;
  currentPeriod?: string;
  grain?: string;
  keyColumns?: string[];
  /** "none" declares there is no natural key; keyCandidate then carries the closest. */
  naturalKey?: string;
  keyCandidate?: string[];
  arrivalTimestampColumn?: string;
  timezone?: string;
  piiColumns?: string[];
  safeUse?: string;
  expectedRowCount?: number;
  /** How this dataset is loaded — full or incremental, and what a late row
   *  does to a period that has already been reported. */
  loadSemantics?: LoadSemanticsDecl;
  /** Written back by runs, read-only to the form. */
  measured?: {
    rowCount?: number;
    columnCount?: number;
    periodCovered?: string;
    measuredAt?: string;
    measuredByRun?: string;
  };
}

/** Where runs under this project execute. */
export interface EnvironmentDecl {
  where?: string;
  runtimeBudgetDefault?: string;
}

/**
 * One measure the project reports on, defined once PER SYSTEM that computes it.
 * `definitions` is the point: "net revenue" is one word over two definitions,
 * and a reconciliation that carries only one of them reports a vocabulary
 * difference as a gap in the data.
 */
export interface KPIDecl {
  id?: string;
  name?: string;
  /** system name → what that system means by this measure. */
  definitions?: Record<string, string>;
  unit?: string;
  currency?: string;
  owner?: string;
}

/** One dashboard and the thing a query can actually be pointed at behind it. */
export interface DashboardDecl {
  id?: string;
  name?: string;
  owner?: string;
  source?: string;
  readPath?: string;
  refreshCadence?: string;
  asOfConvention?: string;
  link?: string;
}

/** The reporting calendar. "Q2" is three different windows without it. */
export interface CalendarDecl {
  fiscalYearStart?: string;
  quarterBoundaries?: string;
  timezone?: string;
  snapshotDay?: string;
  lateArrivalPolicy?: string;
}

/** Every unit a declared tolerance may carry. A number with no denominator is
 *  not a tolerance — 0.5% of A's total and 0.5% of B's total differ. */
export const TOLERANCE_UNITS = [
  'percent_of_a',
  'percent_of_b',
  'percent_of_baseline',
  'absolute',
  'count',
  'exact',
  'psi',
  'points_of_share',
] as const;

/**
 * A STANDING tolerance: how far a subject may move before somebody has to act,
 * declared once by a named owner. `subject` is a KPI (a reconciliation) or a
 * dataset feature (a shift audit) — one registry, because "how much movement is
 * too much" is one question asked of two kinds of thing. A run may TIGHTEN one
 * and may never widen it.
 */
export interface ToleranceDecl {
  id?: string;
  subject?: string;
  /**
   * What KIND of thing the subject is. The shift audit's features table
   * requires it, and an inherited row with it blank arrived on the form looking
   * complete while the recipe's own gate was about to refuse the run.
   */
  type?: 'numeric' | 'categorical' | 'timestamp';
  /** What breaks downstream when this moves. Required on the same table. */
  why?: string;
  statistic?: string;
  value?: number;
  unit?: string;
  segmentValue?: number;
  watchBand?: number;
  severity?: string;
  owner?: string;
  /** ISO date. Who set it and when is half of what makes it defensible. */
  setAt?: string;
}

/** A standing known difference: a filter one side applies and the other does
 *  not. Named, it becomes a TERM of the equation; unnamed, it is unexplained. */
export interface ExclusionDecl {
  name?: string;
  side?: string;
  filter?: string;
  owner?: string;
}

/** Who plays which part. `contactRef` is a reference, never an address. */
export interface StakeholderDecl {
  role?: string;
  name?: string;
  contactRef?: string;
}

/** Where a finished deliverable is meant to go. A recipient REFERENCE. */
export interface DeliveryDecl {
  format?: string;
  recipientRef?: string;
}

/** A FROZEN set of rows a model is scored on. `sha256` is the point: "the June
 *  holdout" names a belief, a content hash names bytes. */
export interface EvalsetDecl {
  id?: string;
  /** "dev" | "holdout" — the holdout is the one nothing may train on. */
  kind?: string;
  location?: string;
  rowCount?: number;
  dateRange?: string;
  sha256?: string;
  labelColumn?: string;
  frozenAt?: string;
}

/** Who holds the holdout, and what may read it. */
export interface HoldoutCustodyDecl {
  holder?: string;
  evalsetId?: string;
  rule?: string;
}

/** A metric definition with the number it must clear. The recipe's success
 *  metric points at one of these instead of being a sentence a script reads. */
export interface MetricDecl {
  id?: string;
  name?: string;
  formulaOrLibrary?: string;
  operatingPoint?: string;
  uncertaintyMethod?: string;
  /** Carries its comparator (">= 0.65") — the direction is half the number. */
  threshold?: string;
}

/** A column the model must be reported on, and the floor its worst slice may
 *  not fall below. */
export interface SliceDecl {
  column?: string;
  floor?: number;
}

/** When the label becomes knowable — what leakage is judged against. */
export interface LabelAvailabilityDecl {
  event?: string;
  lag?: string;
}

/** What the model has to live inside once it ships. */
export interface ServingConstraintsDecl {
  latency?: string;
  artifactSize?: string;
  residency?: string;
  cost?: string;
}

/** The evalset a refinement or a learned skill must re-score against, and by
 *  how much it has to win. */
export interface RegressionPolicyDecl {
  evalsetId?: string;
  minMargin?: string;
}

/**
 * When a run is expected, and who is answerable when it does not happen.
 *
 * Hyperplane schedules nothing: a run starts when a person or an external
 * system starts it. Nothing declared here makes a run happen — it records what
 * the owner EXPECTS, and `triggeredBy` names the system that does the starting.
 */
export interface ScheduleDecl {
  expectedCadence?: string;
  deadline?: string;
  timezone?: string;
  triggeredBy?: string;
  /** Who or what to page when it does not land. A REFERENCE — a rota name, a
   *  handle, a list id — never an address. */
  pageRef?: string;
}

/** What a run under this project may do to the warehouse. */
export const WAREHOUSE_ACCESS = ['read', 'write'] as const;

/** The two roles, each held as the NAME of the environment variable that
 *  carries it. A role name is a reference; a role's credential is not. */
export interface WarehouseRoleRefs {
  read?: string;
  write?: string;
}

/**
 * The warehouse the declared tables live in.
 *
 * `connectionRef` is the name of the environment variable that holds the
 * connection, never the connection itself — the platform refuses anything that
 * is not an environment-variable name, which is what keeps a DSN with a
 * password in it out of this document.
 */
export interface WarehouseDecl {
  engine?: string;
  databaseSchema?: string;
  connectionRef?: string;
  roleRefs?: WarehouseRoleRefs;
  access?: 'read' | 'write';
}

/**
 * Where the pipeline's code lives, so a finding about a table can be traced to
 * the model that writes it. A path, a command, a convention — never a
 * credential.
 */
export interface LineageDecl {
  repoPath?: string;
  runCommand?: string;
  manifestPath?: string;
  /** How a change to it reaches production: the convention, not a link to one
   *  particular pull request. */
  prConvention?: string;
}

/**
 * A project's declaration document: an EXTENSIBLE set of typed groups. Every
 * reader takes the groups it knows and leaves the rest alone, which is why the
 * index signature is here and why PATCH merges group by group server-side — a
 * form that edits `datasets` must not be able to delete a group added later.
 */
export interface ProjectDeclarations {
  purpose?: PurposeDecl;
  datasets?: DatasetDecl[];
  environment?: EnvironmentDecl;
  // The analyst's groups: what a reconciliation asks for once per run today.
  kpis?: KPIDecl[];
  dashboards?: DashboardDecl[];
  calendar?: CalendarDecl;
  tolerances?: ToleranceDecl[];
  exclusions?: ExclusionDecl[];
  stakeholders?: StakeholderDecl[];
  delivery?: DeliveryDecl;
  // The ML groups: records a learned skill, an improve loop or a shift audit
  // can cite BY ID, which is what none of them could do before.
  evalsets?: EvalsetDecl[];
  holdoutCustody?: HoldoutCustodyDecl;
  metrics?: MetricDecl[];
  slices?: SliceDecl[];
  labelAvailability?: LabelAvailabilityDecl;
  servingConstraints?: ServingConstraintsDecl;
  regressionPolicy?: RegressionPolicyDecl;
  // The operating groups: when a run is expected, where the tables live, and
  // where the code that writes them lives.
  schedule?: ScheduleDecl;
  warehouse?: WarehouseDecl;
  lineage?: LineageDecl;
  [group: string]: unknown;
}

/** One recipe slot's value inherited from the project, with where it came from. */
export interface SlotPrefill {
  value: string;
  /** The declaration path, e.g. "datasets[0].location". */
  source: string;
}

/**
 * A first-class project: the row behind the free-text `project` tag that runs,
 * lanes and decisions carry. The tag stays the join key — there are no foreign
 * keys — and the row is auto-registered the first time something is tagged.
 *
 * TWO sentences, two authors, never one field:
 *   `summary`   — human-owned. "What this project is for", typed by the owner
 *                 and never written by the platform.
 *   `narrative` — machine-written. "What this project has built so far",
 *                 regenerated from the briefs of accepted deliveries. Empty
 *                 means nothing has been accepted yet.
 */
export interface Project {
  id: string;
  name: string;
  summary?: string;
  narrative?: string;
  declarations?: ProjectDeclarations;
  status: string;
  owner?: string;
  provenance: string;
  createdAt: string;
  updatedAt: string;
}

/**
 * One project as the selector and the Projects panel see it: the tag, the
 * combined item count, the per-kind breakdown, and the registered row's
 * metadata. `project` + `count` keep their original meaning, so the scope
 * chips read the same fields they always did.
 *
 * `registered: false` means the project exists only as a tag on some item —
 * possible on a database written before projects had rows.
 */
export interface ProjectCount {
  project: string;
  count: number;
  registered: boolean;
  name?: string;
  /** Human-owned "what this project is for". */
  summary?: string;
  /** Machine-written "what this project has built so far"; empty = nothing accepted. */
  narrative?: string;
  declarations?: ProjectDeclarations;
  status?: string;
  provenance?: string;
  runs: number;
  lanes: number;
  decisions: number;
  /** Distinct workflow identities that have run under the tag — the LIST lives on ProjectDetail. */
  workflowCount: number;
  lastActivity?: string;
}

/** One workflow a project has run — derived from its runs, never declared. */
export interface ProjectWorkflow {
  /** Backend-owned identity; distinct saved setups remain separate. */
  key: string;
  /** Display name captured from the most recently updated run in this group. */
  name: string;
  /** Compatibility runtime label, not the workflow identity. */
  dag: string;
  /** Set when the run was instantiated from a recipe — a template id (a uuid). */
  recipe?: string;
  /** That template's display name. Render this, never the id. */
  recipeName?: string;
  /** The saved workflow this group came from, when it came from one. Present
   *  on a saved workflow with ZERO runs too, which is what lets its row link to
   *  `/workflows/<id>` rather than sitting inert (M-15). */
  sourceWorkflowId?: string;
  runs: number;
  /** How many of those runs were NOT rehearsals. Zero means this project has
   *  never run this workflow for real, which is what pre-ticks Dry run. */
  realRuns?: number;
  lastRunId?: string;
  lastState?: string;
  lastRunAt?: string;
}

/**
 * The saved workflow a `projectWorkflow` key names, when it names one (M-15).
 *
 * The key is the backend's identity for a group of runs: `saved:<workflow id>`
 * for anything launched from a saved definition, `definition:<hash>` for an
 * ad-hoc run. A saved workflow with ZERO runs is listed too, and it has no run
 * to open — this is what lets its row link to `/workflows/<id>` instead of
 * sitting inert.
 */
export function savedWorkflowIdOf(key: string | undefined): string | undefined {
  if (typeof key !== 'string' || !key.startsWith('saved:')) return undefined;
  const id = key.slice('saved:'.length).trim();
  return id || undefined;
}

/** GET /api/projects/{id} — the summary row plus the workflows it has run. */
export interface ProjectDetail extends ProjectCount {
  workflows: ProjectWorkflow[];
  /**
   * What this project's declarations can fill in on a recipe form, keyed by
   * context-slot id. Resolved server-side so the slot→declaration mapping lives
   * in one place (internal/store/declarations.go) rather than once per client.
   *
   * A prefill that only makes sense on ONE recipe is keyed `<recipe>/<slot>`:
   * `grain` is the audited dataset's grain in data-quality-audit and the
   * FINISHED table's grain in data-pipeline, and a dataset answers the first
   * and not the second. Read a slot with `lookupPrefill`, never by bare id.
   */
  slotDefaults?: Record<string, SlotPrefill>;
  /**
   * What the project says about the slots it deliberately leaves EMPTY. A shift
   * audit compares two periods of the same dataset, and nothing a project
   * declares says which period is under test — so the second window carries the
   * question instead of a guess about which dataset it is.
   */
  slotNotes?: Record<string, string>;
}

/**
 * The recipe's own answer for a slot when the project scoped one to it, and the
 * shared answer otherwise. The twin of store.LookupPrefill — one rule, so the
 * form and the compose ledger cannot disagree about which answer wins.
 */
export function lookupPrefill(
  prefill: Record<string, SlotPrefill>,
  recipe: string,
  slot: string,
): SlotPrefill | undefined {
  return (recipe ? prefill[`${recipe}/${slot}`] : undefined) ?? prefill[slot];
}

export interface ComposeResult {
  workflow: SDKWorkflow | null;
  unsupported: string[];
  project: string;
}

// ── Evals + Optimize + Orchestrate types (M11/M21) ────────────────────────

export interface EvalCheckSpec {
  kind: string;
  min?: number;
  max?: number;
  key?: string;
}

export interface EvalCase {
  id: string;
  input: unknown;
  criteria?: string;
  expect?: unknown;
  checks?: EvalCheckSpec[];
}

export interface EvalCaseResult {
  case_id: string;
  passed: boolean;
  score: number;
  output?: unknown;
  reasons?: string[];
}

export interface Evalset {
  id: string;
  name: string;
  description?: string;
  skill_ref?: string;
  cases: EvalCase[];
  created_at: string;
  updated_at: string;
}

export interface EvalRun {
  id: string;
  evalset_id: string;
  target: string;
  engine: string;
  model?: string;
  started_at: string;
  ended_at?: string;
  results: EvalCaseResult[];
  pass_rate: number;
  mean_score: number;
  status: string;
}

export interface VariantScore {
  instruction: string;
  pass_rate: number;
  mean_score: number;
}

export interface OptimizeResult {
  skill_id: string;
  evalset_id: string;
  baseline: VariantScore;
  variants: VariantScore[];
  winner?: VariantScore;
  gain: number;
  applied: boolean;
  feedback_grounded?: boolean;
}

export interface OptimizeRun {
  id: string;
  skill_id: string;
  evalset_id: string;
  baseline_pass_rate: number;
  baseline_mean_score: number;
  winner_instruction?: string;
  gain: number;
  applied: boolean;
  variants: unknown;
  created_at: string;
}

// ── The improvement loop (M26) ─────────────────────────────

/** One skill's outcome in an improve pass. The pass also records a synthetic
 *  first item whose skillRef is `(cycle)` and whose status names what triggered
 *  the pass — see `improveTrigger`. */
export interface ImproveItem {
  skillRef: string;
  status: string;
  winner?: string;
}

/** One pass of the always-on self-improvement loop: harvest reviewer feedback,
 *  score variants against the skill's evalset, apply only a winner. */
export interface ImproveRun {
  id: string;
  startedAt: string;
  skillsConsidered: number;
  skillsOptimized: number;
  skillsPromoted: number;
  items: ImproveItem[] | null;
  status: string;
}

/** The synthetic item that carries the trigger rather than a skill outcome. */
const CYCLE_ITEM_REF = '(cycle)';

/** Reads what started a pass: 'timer', 'startup', 'manual', or undefined for a
 *  pass recorded before the loop existed. */
export function improveTrigger(run: ImproveRun): string | undefined {
  const head = (run.items ?? []).find((i) => i.skillRef === CYCLE_ITEM_REF);
  if (!head) return undefined;
  const m = /^trigger:\s*([a-z]+)/.exec(head.status);
  return m ? m[1] : undefined;
}

/** The per-skill outcomes, without the synthetic trigger row. */
export function improveSkillItems(run: ImproveRun): ImproveItem[] {
  return (run.items ?? []).filter((i) => i.skillRef !== CYCLE_ITEM_REF && i.skillRef !== '');
}

/** How many times the loop has APPLIED a refinement to each skill, across every
 *  recorded pass. Only 'promoted' counts: a variant that lost the evaluation
 *  changed nothing, and counting it would overstate what the loop has done. */
export function refinementCounts(runs: ImproveRun[]): Record<string, number> {
  const counts: Record<string, number> = {};
  for (const run of runs) {
    for (const item of improveSkillItems(run)) {
      if (item.status === 'promoted') {
        counts[item.skillRef] = (counts[item.skillRef] ?? 0) + 1;
      }
    }
  }
  return counts;
}

export interface OrchestrateItem {
  title: string;
  state: string;
  runId?: string;
  error?: string;
}

export interface OrchestrateRun {
  id: string;
  startedAt: string;
  backlogSize: number;
  maxItems: number;
  done: number;
  failed: number;
  items: OrchestrateItem[];
  status: string;
}

/** Per-run token summary within StatsTokens.byRun */
export interface RunTokensSummary {
  runId: string;
  dag: string;
  startedAt: string;
  inputTokens: number;
  cachedInputTokens: number;
  outputTokens: number;
}

/** GET /api/stats/tokens — new committed usage per execution, excluding continuation seeds. */
export interface StatsTokens {
  totalCalls: number;
  inputTokens: number;
  cachedInputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cacheHitRate: number;
  /** Most recent ~15 runs, oldest-first (newest = rightmost in charts) */
  byRun: RunTokensSummary[];
}

// ── Knowledge depth types (M19) ────────────────────────────────────────────

export interface TechRadar {
  id: string;
  tech: string;
  quadrant: string;
  ring: string;
  note?: string;
  createdAt?: string;
}

export interface Stack {
  id: string;
  name: string;
  description?: string;
  runtime?: string;
  dockerized: boolean;
  notes?: string;
  files?: unknown;
  createdAt?: string;
}

export interface Category {
  id: string;
  name: string;
  kind: string;
  count?: number;
  createdAt?: string;
}

/** One typed thing a recipe must KNOW before it can start. */
export interface ContextSlot {
  id: string;
  label: string;
  /**
   * Read by the PERSON filling the slot, never by the model that consumes the
   * value. See ContextSlot.Hint in internal/store/recipes.go for the authoring
   * contract this field is held to.
   */
  hint?: string;
  required?: boolean;
  example?: string;
  /** Value shape: text (default) | list | date | number | table. */
  type?: 'text' | 'list' | 'date' | 'number' | 'table';
  /**
   * The row schema of a `table` slot, in display order. A table slot's value is
   * canonical JSON — `[{"feature":"trip_distance","unit":"psi"}, …]` — and
   * reaches the process as one readable line per row, so an instruction written
   * against the old free-text slot keeps reading a string.
   */
  columns?: SlotColumn[];
  /** Who can answer: owner (default) | engineer. Drives the form's grouping. */
  audience?: 'owner' | 'engineer';
}

/** One column of a table slot: what to ask for in that cell. */
export interface SlotColumn {
  /** The key in the stored JSON and in the carried line. Gates match on it. */
  id: string;
  label: string;
  hint?: string;
  /** Closes the column to a set — a unit column is the difference between
   *  "0.5%" and a number the platform understands. */
  options?: string[];
  required?: boolean;
}

/** One thing a recipe promises to PROVE. Carried verbatim onto the run doc. */
export interface Criterion {
  id: string;
  title: string;
  hint?: string;
  /** Node id whose verdict settles this criterion — usually a verify/gate inside
   *  the trusted process the recipe wraps, so it is matched across the run tree.
   *  Absent (or naming a node that never ran) leaves the criterion unverified. */
  proof?: string;
}

/**
 * One thing a recipe hands back.
 *
 * `name` is the ENGINE's id for it — kept for the hover and for matching a
 * product back to its promise, never rendered as the caption. `forOwner` is the
 * person-facing line and the only sentence shown: the model-facing contract
 * description is not sent at all, because for as long as it was, it was what
 * the form displayed.
 */
export interface RecipeOutput {
  name: string;
  forOwner: string;
}

export interface WorkflowCheck {
  inputTables?: Record<string, CheckInputTable>;
  id: string;
  title: string;
  /** The one-paragraph intro a reader meets first (D-06). */
  summary?: string;
  /** The prescriptive methodology the SDK hands the AGENT, unchanged. It is
   *  the ~450-word block nobody read; a person now meets `summary` plus the
   *  four arrays below, and this stays available for anyone who wants the
   *  literal wording. A catalog entry from before the split has only this. */
  description: string;
  /** Files the step must produce for the check to run at all. */
  produces?: string[];
  /** What the host measures or verifies itself, rather than believing. */
  recomputes?: string[];
  /** What a pass establishes. */
  proves?: string[];
  /** What a pass does NOT establish. Never empty on a catalog entry: a check
   *  that claims no limits is the one most likely to be over-read. */
  doesNotProve?: string[];
  inputs?: { required?: string[]; optional?: string[] };
  requires?: string[];
  approvalAfter?: string;
}

/** The four contract arrays with the words a reader sees above each one. */
export const CHECK_CLAIM_HEADINGS = {
  produces: 'Files you must produce',
  recomputes: 'What the host recomputes',
  proves: 'What a pass proves',
  doesNotProve: 'What it does not prove',
} as const;

/** One array's non-empty sentences, or [] — a catalog entry recorded before
 *  the contract existed renders no heading rather than an empty list. */
export function checkClaims(check: WorkflowCheck | undefined, key: keyof typeof CHECK_CLAIM_HEADINGS): string[] {
  const raw = check?.[key];
  return Array.isArray(raw) ? raw.filter((line): line is string => typeof line === 'string' && line.trim() !== '') : [];
}

export interface ContinuationReview {
  eligible: boolean;
  reviewToken?: string;
  reason?: string;
  continuedRunId?: string;
  reused?: { id: string; title: string; kind: string }[];
  restart?: { id: string; title: string; kind: string; index: number } | null;
  renewApprovals?: { id: string; title: string }[];
  runtimeFingerprint?: string;
  usageNote?: string;
}
export class ContinuationError extends Error {
  constructor(message: string, readonly status: number) { super(message); }
}

/** A refused practice-test generation, carrying the status so the card can tell
 *  "one already exists" (409) apart from a failure worth retrying (D-04). */
export class EvalsetGenerateError extends Error {
  constructor(message: string, readonly status: number) { super(message); }
  /** True when an evalset is already on file — answered with Replace, not a retry. */
  get alreadyExists(): boolean { return this.status === 409; }
}

export interface SDKWorkflowNode {
  /** Omitted follows the previous step; [] starts independently. */
  dependsOn?: string[];
  access?: { hosts: string[]; readPaths?: string[]; environment?: string[] };
  toolchain?: boolean;
  sideEffect?: boolean;
  inputs?: Record<string, string>;
  acceptance?: string;
  kind?: 'document' | 'approval' | 'research' | 'check' | 'task';
  check?: string[];
  id: string;
  title: string;
  instruction: string;
}

export interface SDKWorkflow {
  nodes?: SDKWorkflowNode[];
  /**
   * What this workflow asks for each run, in the order it asks. The same typed
   * ContextSlot templates use: instructions reference an input by its
   * `{slot:<id>}` token and the server fills them at launch (store.FillSlots),
   * so the saved definition stays the reusable one. Always present on the wire
   * (`[]` when the workflow asks for nothing).
   */
  inputs?: ContextSlot[];
  id: string;
  name: string;
  kind: 'task' | 'app-ideator' | 'nodes';
  instruction: string;
  targetRoot: string;
  check: string[];
  project: string;
  autoRepair: boolean;
  /**
   * What this workflow asks of a person when one of its runs finishes (M-11).
   * Always present on the wire; `normalizeWorkflow` defaults it to 'required',
   * which is the behaviour every workflow had before this field existed.
   */
  signOff?: SignOffPolicy;
  revision: number;
  updatedAt?: string;
}

export interface WorkflowTemplate {
  id: string;
  name: string;
  description?: string;
  spec?: unknown;
  /** Non-empty makes this template a recipe: the composer asks for these first. */
  contextSlots?: ContextSlot[];
  criteria?: Criterion[];
  createdAt?: string;
  /** The human name of the trusted process this recipe wraps, when `name` is a
   *  slug. Resolved server-side; render this and keep the slug for the hover. */
  displayName?: string;
  /** What the recipe returns — resolved from the process contract's outputs, so
   *  the form can answer "what will I get back?" (see recipe_outputs.go). */
  outputs?: RecipeOutput[];
}

/** POST /api/templates/{id}/instantiate response. */
export interface InstantiateResult {
  draft?: Draft;
  /**
   * The prepared definition. With `run:false` its instructions KEEP their
   * `{slot:x}` tokens and `workflow.inputs` carries the template's slots, so
   * saving it yields a workflow that can be run again with new values.
   */
  workflow?: SDKWorkflow;
  recipe: string;
  /** The values accepted for this preparation — this run's input values. */
  slots: Record<string, string>;
  criteria: Criterion[];
  runId?: string;
  /** The project the recipe was filled in under; '' means unscoped. */
  project?: string;
}

export interface Draft {
  id: string;
  name: string;
  spec: unknown;
  status: string;
  notes: string;
  /** The project the draft was composed under; '' means unscoped. */
  project: string;
  created_at: string;
  updated_at: string;
}

// ── Board: lanes + tickets ────────────────────────────────────────────────────

export type TicketStatus = 'todo' | 'in_progress' | 'review' | 'done' | 'blocked';
export type TicketPriority = 1 | 2 | 3;

export interface LaneCounts {
  todo: number;
  inProgress: number;
  review: number;
  done: number;
  blocked: number;
}

export interface Lane {
  id: string;
  name: string;
  description?: string;
  status?: string;
  targetRoot?: string;
  project?: string;
  /** Run this lane was decomposed from, if any — links the Board back to its origin run. */
  originRun?: string;
  counts: LaneCounts;
}

export interface Ticket {
  sdkRunId?: string;
  id: string;
  laneId: string;
  title: string;
  detail?: string;
  status: TicketStatus;
  priority: TicketPriority;
  assignee?: string;
  deps: string[];
  output?: string;
  error?: string;
  createdAt: string;
  updatedAt: string;
}

// ── Helpers ────────────────────────────────────────────────

function buildQuery(params: Record<string, string | number | boolean | undefined>): string {
  const entries = Object.entries(params).filter(([, v]) => v !== undefined);
  if (entries.length === 0) return '';
  const qs = entries.map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`).join('&');
  return `?${qs}`;
}

async function get<T>(path: string): Promise<T> {
  const res = await fetch(path);
  if (!res.ok) throw new Error(`GET ${path} → ${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}

/** GET that treats 404 as "there is none yet" rather than as a failure. For
 *  resources whose absence is an ordinary state a component renders nothing
 *  for — an outcome brief on a run that has not finished, say. Any other
 *  non-OK status still throws, so a real fault is never mistaken for absence. */
async function getOrNull<T>(path: string): Promise<T | null> {
  const res = await fetch(path);
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(`GET ${path} → ${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}

// Launch refusals name the input at fault; every other failure stays a message.
async function startRun(body: unknown): Promise<{ runId: string }> {
  const res = await fetch('/api/run', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  if (!res.ok) throw await workflowInputError(res, 'POST', '/api/run');
  return res.json() as Promise<{ runId: string }>;
}

async function post<T>(path: string, body: unknown, signal?: AbortSignal): Promise<T> {
  const res = await fetch(path, {
    signal,
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(await errorMessage(res, 'POST', path));
  return res.json() as Promise<T>;
}

/** Surfaces the API's {error} body when it has one. A bare status code hides
 *  exactly the sentence the server wrote for the user ("skill is not a draft"). */
async function errorMessage(res: Response, method: string, path: string): Promise<string> {
  try {
    const body = (await res.json()) as { error?: unknown };
    if (typeof body?.error === 'string' && body.error) return body.error;
  } catch {
    /* not a JSON body — fall through to the status line */
  }
  return `${method} ${path} → ${res.status} ${res.statusText}`;
}

async function patch<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(path, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(await errorMessage(res, 'PATCH', path));
  return res.json() as Promise<T>;
}

/** Flattens a delivery awaiting a verdict into the shared inbox item shape.
 *
 *  The queue is journey-grained: one item per DELIVERY, carrying its attempts,
 *  rather than one per run. `runId` is the newest attempt, so "review this"
 *  still lands somewhere specific. The id is namespaced so it cannot collide
 *  with an approval/selection record id. */
function flattenOutcomeItem(raw: Journey): InboxItem {
  const newest = raw.attempts?.[0];
  return {
    id: `journey-${raw.key}`,
    type: 'outcome',
    runId: newest?.runId,
    dag: raw.dag,
    title: raw.name,
    runState: newest?.state,
    recipe: raw.recipe,
    criteriaCount: raw.criteria,
    createdAt: newest?.endedAt ?? raw.updatedAt,
    updatedAt: raw.updatedAt,
    journey: raw,
    workflowName: raw.workflowName,
    runNumber: raw.runNumber,
    resultDocument: raw.resultDocument ?? undefined,
    failureReason: raw.failureReason,
    // The policy the newest attempt was launched under. The queue never holds
    // an "optional" delivery, so this is normally 'required' — it is carried so
    // a card that reaches this list by another route can still say which
    // policy it is under rather than assuming (M-11).
    signOff: readSignOff(raw.signOff),
  };
}

/** Flattens a learned draft skill into a proposal card.
 *
 *  A proposal is a DRAFT skill row, not a new record type: extraction persists
 *  what it proposed as a draft, and a draft is already inert until somebody
 *  approves it. Answering the card in the inbox and approving the same draft in
 *  the Knowledge registry are the same act on the same row. */
function flattenProposalItem(skill: Skill): InboxItem {
  const { sourceRun, sourceDelivery } = skillLineage(skill);
  const from = learnedFromLabel(skill);
  return {
    id: `proposal-${skill.id}`,
    type: 'proposal',
    runId: sourceRun,
    dag: typeof skill.metadata?.sourceDag === 'string' ? (skill.metadata.sourceDag as string) : undefined,
    title: from ? `Learned from ${from}: ${skill.name}` : `Learned: ${skill.name}`,
    prompt: skill.description,
    createdAt: skill.updatedAt,
    updatedAt: skill.updatedAt,
    recipe: sourceDelivery,
    proposal: skill,
  };
}

function flattenInboxItem(raw: RawApproval, itemType: 'approval' | 'selection'): InboxItem {
  const rawCands = raw.doc?.candidates;
  const candidates: Candidate[] = Array.isArray(rawCands)
    ? rawCands.filter((c): c is Candidate => typeof c === 'object' && c !== null)
    : [];
  const evidence =
    raw.doc?.evidence && typeof raw.doc.evidence === 'object'
      ? (raw.doc.evidence as Record<string, unknown>)
      : undefined;
  const compareBy =
    Array.isArray(raw.doc?.compareBy) &&
    (raw.doc!.compareBy as unknown[]).every((k) => typeof k === 'string')
      ? (raw.doc!.compareBy as string[])
      : undefined;

  return {
    id: raw.id,
    type: itemType,
    runId: raw.runId,
    dag: raw.doc?.dag,
    gateNode: raw.doc?.gateNode,
    documents: raw.doc?.documents,
    title: raw.doc?.title,
    prompt: raw.doc?.prompt,
    workflowName: raw.doc?.workflowName,
    runNumber: raw.doc?.runNumber,
    goal: raw.doc?.goal,
    nextSteps: Array.isArray(raw.doc?.nextSteps)
      ? raw.doc!.nextSteps!.filter((t): t is string => typeof t === 'string' && t !== '')
      : undefined,
    draftDocument: raw.doc?.draftDocument,
    candidates: candidates.length > 0 ? candidates : undefined,
    compareBy,
    evidence,
    state: raw.state,
    createdAt: raw.createdAt,
    updatedAt: raw.updatedAt,
  };
}

// ── API object ─────────────────────────────────────────────

// Forms use an empty list for no extra check; the wire omits it for tasks.
// A step workflow always carries its declared run inputs, so "no inputs" and
// "an older row that predates inputs" are the same editable thing: an empty list.
const normalizeWorkflow = (workflow: SDKWorkflow): SDKWorkflow => ({ ...workflow, check: workflow.check ?? [],
  // A row written before sign-off policies existed asked for a signature, so
  // that is what it keeps asking for. An unrecognised value reads the same way:
  // the policy that still asks is the safe one to guess (M-11).
  signOff: readSignOff(workflow.signOff),
  ...(workflow.kind === 'nodes' ? { inputs: workflow.inputs ?? [] } : {}) });

/**
 * A refused save or launch that named which inputs were at fault.
 *
 * The server answers `{"error":…}` with `unknownInputs` (a token naming an
 * input the workflow does not declare), `missingSlots` (a required input with
 * no value) or `revision` (the saved workflow changed since it was loaded).
 * Carrying them separately lets the editor mark the field instead of printing
 * one sentence and leaving the person to find it.
 */
export class WorkflowInputError extends Error {
  constructor(message: string, readonly unknownInputs: string[] = [], readonly missingSlots: string[] = [], readonly revision?: number) { super(message); }
}
async function workflowInputError(res: Response, method: string, path: string): Promise<Error> {
  let body: { error?: unknown; unknownInputs?: unknown; missingSlots?: unknown; revision?: unknown } | undefined;
  try { body = await res.clone().json() as typeof body; } catch { /* not a JSON body */ }
  const ids = (value: unknown): string[] => Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : [];
  const message = typeof body?.error === 'string' && body.error ? body.error : `${method} ${path} → ${res.status} ${res.statusText}`;
  const unknown = ids(body?.unknownInputs), missing = ids(body?.missingSlots);
  const revision = typeof body?.revision === 'number' ? body.revision : undefined;
  return unknown.length || missing.length || revision !== undefined ? new WorkflowInputError(message, unknown, missing, revision) : new Error(message);
}
const taskWithoutEmptyCheck = <T extends { check?: string[] }>(value: T): T | Omit<T, 'check'> => {
  if (value.check?.length) return value;
  const { check, ...rest } = value;
  return rest;
};

export const api = {
  validateWorkflow: async (definition: WorkflowDefinition, signal?: AbortSignal): Promise<WorkflowValidationResult> =>
    readWorkflowValidation(await post<unknown>('/api/workflows/validate', definition, signal)),

  workflowChecks: async (): Promise<WorkflowCheck[]> => {
    const result = await get<{ checks: WorkflowCheck[] }>('/api/workflow-checks');
    const strings = (value: unknown): value is string[] => Array.isArray(value) && value.every(item => typeof item === 'string');
    if (!Array.isArray(result.checks) || result.checks.some(check =>
      !check || typeof check.id !== 'string' || !check.id || typeof check.title !== 'string' || !check.title || typeof check.description !== 'string' || !check.description ||
      (check.requires !== undefined && !strings(check.requires)) || (check.approvalAfter !== undefined && typeof check.approvalAfter !== 'string') ||
      (check.inputs !== undefined && (!check.inputs || !strings(check.inputs.required) || (check.inputs.optional !== undefined && !strings(check.inputs.optional)))) ||
      (check.inputTables !== undefined && !validInputTables(check.inputTables, [...(check.inputs?.required ?? []), ...(check.inputs?.optional ?? [])]))
    ) || new Set(result.checks.map(check => check.id)).size !== result.checks.length) throw new Error('The extra check catalog is invalid. Retry before using attached checks.');
    return result.checks;
  },

  workflows: async (): Promise<SDKWorkflow[]> => (await get<SDKWorkflow[]>('/api/workflows')).map(normalizeWorkflow),

  /**
   * GET /api/workflows/{id}/runs — this saved workflow's own run history.
   *
   * Read-only, and deliberately a separate read from `runs()`: the runs list is
   * scoped by project and filter, while a workflow's history is scoped by the
   * workflow itself and must not change when someone changes a filter
   * elsewhere. `runs` is normalised to an array so an empty history renders as
   * "no runs yet" rather than as a crash.
   */
  workflowRuns: async (id: string, limit = 50): Promise<WorkflowRunsResult> => {
    const result = await get<WorkflowRunsResult>(
      `/api/workflows/${encodeURIComponent(id)}/runs${buildQuery({ limit })}`,
    );
    return {
      workflow: normalizeWorkflow(result.workflow),
      runs: Array.isArray(result.runs) ? result.runs : [],
      total: typeof result.total === 'number' ? result.total : (result.runs?.length ?? 0),
      // Absent on a server that does not offer attachment yet, which the page
      // renders as "nothing to attach" rather than as a broken section.
      attachable: Array.isArray(result.attachable) ? result.attachable : [],
    };
  },

  /**
   * POST /api/workflows/{id}/attach-runs — claim earlier runs of this same
   * definition for this saved workflow (P-16).
   *
   * It rewrites only `options.projectWorkflow`; the frozen `request` that the
   * run actually executed is untouched, so attaching changes what a run is
   * FILED under and never what it did. Runs that already name a saved source,
   * or that ran a different definition, come back in `skipped` with the
   * server's reason rather than being silently dropped.
   */
  attachRuns: async (id: string, runIds: string[]): Promise<AttachRunsResult> => {
    const result = await post<AttachRunsResult>(
      `/api/workflows/${encodeURIComponent(id)}/attach-runs`, { runIds },
    );
    return {
      attached: Array.isArray(result?.attached) ? result.attached : [],
      skipped: Array.isArray(result?.skipped) ? result.skipped : [],
    };
  },

  saveWorkflow: async (workflow: SDKWorkflow): Promise<SDKWorkflow> => {
    const path = `/api/workflows${workflow.id ? '/' + encodeURIComponent(workflow.id) : ''}`;
    const method = workflow.id ? 'PUT' : 'POST';
    const res = await fetch(path, { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(workflow.kind === 'task' ? taskWithoutEmptyCheck(workflow) : workflow) });
    if (!res.ok) throw await workflowInputError(res, method, path);
    return normalizeWorkflow(await res.json() as SDKWorkflow);
  },

  removeWorkflow: async (workflow: SDKWorkflow): Promise<void> => {
    const path = `/api/workflows/${encodeURIComponent(workflow.id)}?revision=${workflow.revision}`;
    const res = await fetch(path, { method: 'DELETE' });
    if (!res.ok) throw new Error(await errorMessage(res, 'DELETE', path));
  },

  health: (): Promise<Health> =>
    get<Health>('/api/health'),

  /**
   * `project` distinguishes "no filter" (`undefined`/`null` — omit the param,
   * matching runs from every project) from "filter to exactly this value"
   * (a string, including `''` for unscoped-only runs).
   */
  runs: (params?: { state?: string; limit?: number; project?: string | null }): Promise<Run[]> =>
    get<Run[]>(
      `/api/runs${buildQuery({
        ...params,
        project: params?.project == null ? undefined : params.project,
      })}`,
    ),

  /** Returns the full {run, nodes} object. */
  runDetail: (id: string): Promise<RunWithNodes> =>
    get<RunWithNodes>(`/api/runs/${encodeURIComponent(id)}`),

  continuationReview: async (id: string): Promise<ContinuationReview> => {
    const path = `/api/runs/${encodeURIComponent(id)}/continuation`;
    const response = await fetch(path);
    if (!response.ok) throw new ContinuationError(await errorMessage(response, 'GET', path), response.status);
    const review = await response.json() as ContinuationReview;
    if (!review || typeof review.eligible !== 'boolean') throw new Error('The continuation review was incomplete. Review again before starting.');
    return review;
  },
  continueRun: async (id: string, reviewToken: string): Promise<{runId: string}> => {
    const path = `/api/runs/${encodeURIComponent(id)}/continuation`;
    const response = await fetch(path, {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({reviewToken})});
    if (!response.ok) throw new ContinuationError(await errorMessage(response, 'POST', path), response.status);
    const result = await response.json() as {runId:string};
    if (!result || typeof result.runId !== 'string' || !result.runId) throw new Error('The continued run could not be identified.');
    return result;
  },

  cancelRun: (id: string): Promise<{ cancelled: string }> =>
    post<{ cancelled: string }>(`/api/runs/${encodeURIComponent(id)}/cancel`, {}),

  /** Convenience: just the run metadata. */
  run: async (id: string): Promise<Run> => {
    const { run } = await get<RunWithNodes>(`/api/runs/${encodeURIComponent(id)}`);
    return run;
  },

  runArtifacts: (id: string): Promise<Artifact[]> =>
    get<Artifact[]>(`/api/runs/${encodeURIComponent(id)}/artifacts`),

  skills: (q?: string): Promise<Skill[]> =>
    get<Skill[]>(`/api/skills${buildQuery({ q })}`),

  /** POST /api/skills — create or update a skill (id is the primary key). */
  saveSkill: (skill: Skill): Promise<Skill> =>
    post<Skill>('/api/skills', skill),

  /** POST /api/runs/{id}/extract-skill — distil the reusable method a passed
   *  run demonstrates into a draft skill. Runs a codex call, so it is slow. */
  extractSkill: (runId: string): Promise<SkillExtractResult> =>
    post<SkillExtractResult>(`/api/runs/${encodeURIComponent(runId)}/extract-skill`, {}),

  /** POST /api/skills/{id}/approve — draft → active, making the skill usable. */
  approveSkill: (id: string): Promise<Skill> =>
    post<Skill>(`/api/skills/${encodeURIComponent(id)}/approve`, {}),

  /** POST /api/skills/{id}/reject — discard a draft skill. */
  rejectSkill: (id: string): Promise<{ id: string; status: string }> =>
    post<{ id: string; status: string }>(`/api/skills/${encodeURIComponent(id)}/reject`, {}),

  /**
   * POST /api/skills/{id}/evalset/generate — build the practice test that
   * Approve is gated on (D-04).
   *
   * The same generator `hyperplane eval gen <skillId>` runs, so the button and
   * the terminal command produce the same thing; this exists because the remedy
   * for a blocked Approve was a command nobody reading the card could run. It
   * makes an engine call and takes minutes. A 409 means an evalset already
   * exists — `replace` is the deliberate second answer to that, never a retry.
   */
  generateEvalset: async (
    id: string,
    opts: { engine?: 'codex' | 'local'; replace?: boolean } = {},
  ): Promise<{ evalsetId: string; cases: EvalCase[] }> => {
    const path = `/api/skills/${encodeURIComponent(id)}/evalset/generate`;
    const res = await fetch(path, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        ...(opts.engine ? { engine: opts.engine } : {}),
        ...(opts.replace ? { replace: true } : {}),
      }),
    });
    // The status matters here and a bare Error loses it: 409 is "one already
    // exists", which the card answers with a REPLACE offer rather than by
    // retrying the same request.
    if (!res.ok) throw new EvalsetGenerateError(await errorMessage(res, 'POST', path), res.status);
    const result = await res.json() as { evalsetId: string; cases?: EvalCase[] };
    return { evalsetId: result.evalsetId, cases: Array.isArray(result.cases) ? result.cases : [] };
  },

  // ── Outcome briefs ─────────────────────────────────────────────────────────

  /** GET /api/runs/{id}/brief — the latest outcome brief for a run, or null
   *  when none has been written yet (a run still going, or one that finished
   *  before briefs existed). Null rather than a throw: an absent brief is an
   *  ordinary state, not an error, and the component simply renders nothing. */
  runBrief: (runId: string): Promise<OutcomeBriefData | null> =>
    getOrNull<OutcomeBriefData>(`/api/runs/${encodeURIComponent(runId)}/brief`),

  /** GET /api/decisions/{id}/brief — the brief written once a human recorded a
   *  verdict. Accepts the outcome decision's id or the run id it is about. */
  deliveryBrief: (id: string): Promise<OutcomeBriefData | null> =>
    getOrNull<OutcomeBriefData>(`/api/decisions/${encodeURIComponent(id)}/brief`),

  // ── Audit personas (AI-12) ─────────────────────────────────────────────────

  /** GET /api/personas — every registered charter. Pass `competentFor` (e.g.
   *  `"skill:foo"`) to get back only the personas that named that skill or
   *  process, each carrying why it matched. */
  personas: (competentFor?: string): Promise<PersonaView[]> =>
    get<PersonaView[]>(`/api/personas${buildQuery({ competentFor })}`),

  /** GET /api/personas/{id} — one persona, with its full charter markdown. */
  persona: (id: string): Promise<PersonaView> =>
    get<PersonaView>(`/api/personas/${encodeURIComponent(id)}`),

  /** GET /api/persona-reviews — reviews newest-first, optionally filtered to
   *  one target (`"skill:<id>"` or `"run:<id>"`). Omitting `target` fetches
   *  every review, which is how the skill/proposal lists get theirs in one
   *  request instead of one per row. */
  personaReviews: (target?: string, limit?: number): Promise<PersonaReview[]> =>
    get<PersonaReview[]>(`/api/persona-reviews${buildQuery({ target, limit })}`),

  /** GET /api/persona-reviews?run=<id> — every verdict recorded about ONE run.
   *  The same question `target: "run:<id>"` asks, without the caller having to
   *  know how a target string is spelled. Sending both is refused, so this
   *  never combines them. */
  runPersonaReviews: (runId: string, limit?: number): Promise<PersonaReview[]> =>
    get<PersonaReview[]>(`/api/persona-reviews${buildQuery({ run: runId, limit })}`),

  /** POST /api/personas/{id}/review — spawn a review now. Runs a codex call
   *  read-only, so it is slow; the verdict it returns is advisory evidence,
   *  never a gate. */
  personaReview: (id: string, target: string, model?: string): Promise<PersonaReview> =>
    post<PersonaReview>(`/api/personas/${encodeURIComponent(id)}/review`, { target, model }),

  /** Fetches {approvals, selections, outcomes, proposals} and flattens to one
   *  queue. */
  inbox: async (): Promise<InboxItem[]> => {
    const raw = await get<{
      approvals?: RawApproval[];
      selections?: RawApproval[];
      outcomes?: Journey[];
      proposals?: Skill[];
    }>('/api/inbox');
    const approvals = Array.isArray(raw.approvals)
      ? raw.approvals.map((a) => flattenInboxItem(a, 'approval'))
      : [];
    const selections = Array.isArray(raw.selections)
      ? raw.selections.map((s) => flattenInboxItem(s, 'selection'))
      : [];
    const outcomes = Array.isArray(raw.outcomes)
      ? raw.outcomes.map(flattenOutcomeItem)
      : [];
    const proposals = Array.isArray(raw.proposals)
      ? raw.proposals.map(flattenProposalItem)
      : [];
    return [...approvals, ...selections, ...outcomes, ...proposals];
  },

  /** POST /api/runs/{id}/outcome — record a verdict on ONE attempt. Still
   *  available, no longer demanded anywhere: the queue asks at journey grain,
   *  and this is the optional annotation for a single run. Rejects with the
   *  server's message on 409 (an outcome already exists, or the run has not
   *  finished). */
  recordOutcome: (runId: string, verdict: OutcomeVerdict, note?: string): Promise<RunOutcome> =>
    post<RunOutcome>(`/api/runs/${encodeURIComponent(runId)}/outcome`, { verdict, note: note ?? '' }),

  /** GET /api/journeys/{key} — one delivery's whole story. */
  journey: (key: string): Promise<Journey> =>
    get<Journey>(`/api/journeys/${encodeURIComponent(key)}`),

  /** Record an immutable outcome for the viewed latest attempt; stale/duplicate attempts return 409. */
  recordJourneyOutcome: (key: string, runId: string, verdict: OutcomeVerdict, note?: string): Promise<JourneyOutcome & { key: string }> =>
    post<JourneyOutcome & { key: string }>(`/api/journeys/${encodeURIComponent(key)}/outcome`, {
      runId, verdict, note: note ?? '',
    }),

  /**
   * POST /api/run — a reviewed SDK step sequence, project task or named SDK workflow.
   * `project` scopes the run; omit it (or pass '') to leave it unscoped.
   * `autoRepair` permits one SDK correction after failed acceptance (per task
   * step for ordered workflows). It does not launch a graph repair.
   * Returns {runId}.
   */
  startRun: (body: {
    /** Edited from this saved setup; submitted settings remain authoritative. */
    sourceWorkflowId?: string;
    /**
     * The saved workflow's revision when launching it BY REFERENCE (no `nodes`,
     * `task` or `workflow` in the body): the server refuses with 409 and the
     * current revision when the saved definition changed since it was loaded.
     */
    revision?: number;
    /**
     * This run's values for the saved workflow's declared `inputs`, by input id.
     * Only meaningful when launching by reference — the server fills the tokens
     * and validates, so the launched definition is the saved one. Missing
     * required inputs come back as 400 `{missingSlots}`.
     */
    slots?: Record<string, string>;
    workflowName?: string;
    nodes?: SDKWorkflowNode[];
    task?: string;
    check?: string[];
    targetRoot?: string;
    workflow?: 'app-ideator';
    inputs?: Record<string, unknown>;
    goal?: string;
    project?: string;
    /** Allow one SDK correction after failed acceptance; off by default. */
    autoRepair?: boolean;
  }): Promise<{ runId: string }> => startRun(body.task ? taskWithoutEmptyCheck(body) : body),

  /** Propose SDK document steps for review; never starts or saves a workflow. */
  compose: (brief: string, project?: string, signal?: AbortSignal): Promise<ComposeResult> =>
    post<ComposeResult>('/api/compose', { brief, project: project ?? '' }, signal),

  /** POST /api/approvals/{id}/decision {decision: 'approved'|'rejected'} */
  approveDecision: (id: string, approved: boolean): Promise<void> =>
    post<void>(`/api/approvals/${encodeURIComponent(id)}/decision`, {
      decision: approved ? 'approved' : 'rejected',
    }),

  /** POST /api/selections/{id}/decision {winner, winnerCandidate?} */
  selectDecision: (id: string, winner: string, winnerCandidate?: unknown): Promise<void> =>
    post<void>(`/api/selections/${encodeURIComponent(id)}/decision`, { winner, winnerCandidate }),

  preferences: (skill: string): Promise<unknown> =>
    get<unknown>(`/api/preferences/${encodeURIComponent(skill)}`),

  /**
   * GET /api/runs/{id}/log?nodeId=X[&tail=N] — one node's log.
   *
   * Without `tail` the whole log comes back, capped server-side at the last
   * 256 KB. With `tail: N` only the last N lines cross the wire — what the log
   * panel opens with, so a multi-megabyte log renders instantly and the full
   * text is a deliberate second click.
   */
  runNodeLog: async (
    runId: string,
    nodeId: string,
    opts?: { tail?: number; file?: LogFileKind },
  ): Promise<NodeLog> => {
    const params = new URLSearchParams({ nodeId });
    if (opts?.tail && opts.tail > 0) params.set('tail', String(opts.tail));
    if (opts?.file && opts.file !== 'log') params.set('file', opts.file);
    const res = await fetch(`/api/runs/${encodeURIComponent(runId)}/log?${params}`);
    if (!res.ok) throw new Error(`GET log → ${res.status} ${res.statusText}`);
    const text = await res.text();
    const size = Number(res.headers.get('X-Log-Size'));
    return {
      text,
      size: Number.isFinite(size) && size > 0 ? size : text.length,
      truncated: res.headers.get('X-Log-Truncated') === 'true',
    };
  },

  /** GET /api/stats/tokens — aggregate token usage across all runs */
  statsTokens: (): Promise<StatsTokens> =>
    get<StatsTokens>('/api/stats/tokens'),

  // ── Evals (M11) ──────────────────────────────────────────────────────────

  /** GET /api/evals — list all evalsets */
  evals: (): Promise<Evalset[]> =>
    get<Evalset[]>('/api/evals'),

  /** GET /api/evals/{id} — single evalset with cases */
  evalset: (id: string): Promise<Evalset> =>
    get<Evalset>(`/api/evals/${encodeURIComponent(id)}`),

  /** GET /api/evals/{id}/runs — eval run history for an evalset */
  evalRuns: (evalsetId: string): Promise<EvalRun[]> =>
    get<EvalRun[]>(`/api/evals/${encodeURIComponent(evalsetId)}/runs`),

  /** GET /api/eval-runs/{id} — single eval run with per-case results */
  evalRun: (id: string): Promise<EvalRun> =>
    get<EvalRun>(`/api/eval-runs/${encodeURIComponent(id)}`),

  // ── Optimize (M11/M18) ───────────────────────────────────────────────────

  /** POST /api/skills/{id}/optimize — trigger optimize loop, returns Result */
  optimizeSkill: (
    skillId: string,
    opts?: { variants?: number; apply?: boolean; evalset?: string; fromRuns?: boolean },
  ): Promise<OptimizeResult> =>
    post<OptimizeResult>(`/api/skills/${encodeURIComponent(skillId)}/optimize`, {
      variants: opts?.variants ?? 4,
      apply: opts?.apply ?? false,
      evalset: opts?.evalset,
    }),

  /** GET /api/skills/{id}/optimize-runs — history of optimize invocations */
  optimizeRuns: (skillId: string): Promise<OptimizeRun[]> =>
    get<OptimizeRun[]>(`/api/skills/${encodeURIComponent(skillId)}/optimize-runs`),

  // ── The improvement loop (M26) ───────────────────────────────────────────

  /** GET /api/improve — every self-improvement pass, newest first. The loop
   *  runs these on a timer inside the API; this is also where a manually
   *  triggered pass lands. */
  improveRuns: (): Promise<ImproveRun[]> =>
    get<ImproveRun[]>('/api/improve'),

  /** POST /api/improve — run one pass now, with the same configuration and the
   *  same non-overlap lock the scheduled pass uses. Rejects with the server's
   *  message (409) when a pass is already running. */
  runImprove: (): Promise<{ id: string }> =>
    post<{ id: string }>('/api/improve', {}),

  // ── Orchestrate (M21) ────────────────────────────────────────────────────

  /** GET /api/orchestrate/{id} — single orchestrate sweep record */
  orchestrateRun: (id: string): Promise<OrchestrateRun> =>
    get<OrchestrateRun>(`/api/orchestrate/${encodeURIComponent(id)}`),

  // ── Knowledge depth (M19) ────────────────────────────────────────────────

  /** GET /api/tech-radar — all tech radar entries */
  techRadar: (): Promise<TechRadar[]> =>
    get<TechRadar[]>('/api/tech-radar'),

  /** POST /api/tech-radar — create a tech radar entry */
  createTechRadar: (entry: Omit<TechRadar, 'id' | 'createdAt'>): Promise<TechRadar> =>
    post<TechRadar>('/api/tech-radar', entry),

  /** GET /api/stacks — all stacks */
  stacks: (): Promise<Stack[]> =>
    get<Stack[]>('/api/stacks'),

  /** POST /api/stacks — create a stack */
  createStack: (stack: Omit<Stack, 'id' | 'createdAt'>): Promise<Stack> =>
    post<Stack>('/api/stacks', stack),

  /** GET /api/categories — all categories with live counts */
  categories: (): Promise<Category[]> =>
    get<Category[]>('/api/categories'),

  /** POST /api/categories — create a category */
  createCategory: (cat: Omit<Category, 'id' | 'createdAt' | 'count'>): Promise<Category> =>
    post<Category>('/api/categories', cat),

  /** GET /api/templates — all workflow templates */
  templates: (): Promise<WorkflowTemplate[]> =>
    get<WorkflowTemplate[]>('/api/templates'),

  /** POST /api/templates — create a workflow template */
  createTemplate: (tmpl: Omit<WorkflowTemplate, 'id' | 'createdAt'>): Promise<WorkflowTemplate> =>
    post<WorkflowTemplate>('/api/templates', tmpl),

  /** Prepare an unsaved SDK definition for review; never launch from the template picker. */
  instantiateTemplate: (
    id: string,
    opts: { slots?: Record<string, string>; project?: string } = {},
  ): Promise<InstantiateResult> =>
    post<InstantiateResult>(`/api/templates/${encodeURIComponent(id)}/instantiate`, {
      slots: opts.slots ?? {}, run: false, project: opts.project ?? '',
    }).then(result => ({...result, ...(result.workflow ? {workflow: normalizeWorkflow(result.workflow)} : {})})),

  /** POST /api/inbox/{recordType}/{id}/dismiss — dismiss a single pending inbox item */
  dismissInboxItem: (recordType: 'approvals' | 'selections', id: string): Promise<{ status: string }> =>
    post<{ status: string }>(`/api/inbox/${encodeURIComponent(recordType)}/${encodeURIComponent(id)}/dismiss`, {}),

  /** POST /api/inbox/clear-stale — bulk-dismiss all pending records for dead runs */
  clearStaleInbox: (): Promise<{ dismissed: number }> =>
    post<{ dismissed: number }>('/api/inbox/clear-stale', {}),

  // ── Board: lanes + tickets (swimlane kanban) ──────────────────────────────

  /**
   * GET /api/lanes — list lanes with per-column counts. `project` distinguishes
   * "no filter" (`undefined`/`null` — omit the param, all lanes) from "filter
   * to exactly this value" (a string, including `''` for unscoped-only lanes).
   */
  lanes: (project?: string | null): Promise<Lane[]> =>
    get<Lane[]>(`/api/lanes${buildQuery({ project: project == null ? undefined : project })}`),

  /** POST /api/lanes — create a lane */
  createLane: (body: { name: string; description?: string; targetRoot?: string; project?: string }): Promise<Lane> =>
    post<Lane>('/api/lanes', body),

  /** GET /api/lanes/{id}/tickets — all tickets for a lane */
  laneTickets: (laneId: string): Promise<Ticket[]> =>
    get<Ticket[]>(`/api/lanes/${encodeURIComponent(laneId)}/tickets`),

  /** POST /api/tickets — create a ticket */
  createTicket: (body: {
    laneId: string;
    title: string;
    detail?: string;
    priority?: TicketPriority;
    deps?: string[];
  }): Promise<Ticket> =>
    post<Ticket>('/api/tickets', body),

  /** PATCH /api/tickets/{id} — update ticket status / assignee / output */
  updateTicket: (
    id: string,
    body: { status?: TicketStatus; assignee?: string; output?: string; expectedUpdatedAt: string; expectedRunId: string },
  ): Promise<Ticket> =>
    patch<Ticket>(`/api/tickets/${encodeURIComponent(id)}`, body),

  /** Admit one ready task into the managed SDK runtime. */
  startLaneTask: (laneId: string): Promise<{ runId: string; ticketId: string; laneId: string }> =>
    post<{ runId: string; ticketId: string; laneId: string }>(`/api/lanes/${encodeURIComponent(laneId)}/work`, { count: 1 }),

  // ── Decision records ─────────────────────────────────────────────────────

  /**
   * GET /api/decisions — decision records, newest first. `project` distinguishes
   * "no filter" (`undefined`/`null` — omit the param, all decisions) from
   * "filter to exactly this value" (a string, including `''` for unscoped-only).
   */
  decisions: (project?: string | null): Promise<Decision[]> =>
    get<Decision[]>(`/api/decisions${project == null ? '' : `?project=${encodeURIComponent(project)}`}`),

  /**
   * GET /api/projects — every project (registered rows and any tag still in
   * use without one) with per-kind counts and last activity.
   */
  projects: (): Promise<ProjectCount[]> => get<ProjectCount[]>('/api/projects'),

  /** GET /api/projects/{id} — one project plus the workflows it has run. */
  project: (id: string): Promise<ProjectDetail> =>
    get<ProjectDetail>(`/api/projects/${encodeURIComponent(id)}`),

  /** POST /api/projects — register a project. 409 when the id is taken. */
  createProject: (body: {
    id?: string;
    name: string;
    summary?: string;
    owner?: string;
    declarations?: ProjectDeclarations;
  }): Promise<Project> => post<Project>('/api/projects', body),

  /**
   * PATCH /api/projects/{id} — partial edit; absent fields are left alone.
   * `declarations` is merged GROUP BY GROUP server-side, so sending only the
   * groups this client knows about cannot destroy the others.
   * `narrative` is machine-owned and is not accepted here.
   */
  updateProject: (
    id: string,
    body: {
      name?: string;
      summary?: string;
      status?: string;
      owner?: string;
      declarations?: ProjectDeclarations;
    },
  ): Promise<Project> => patch<Project>(`/api/projects/${encodeURIComponent(id)}`, body),

  /** GET /api/decisions/{id} */
  decision: (id: string): Promise<Decision> =>
    get<Decision>(`/api/decisions/${encodeURIComponent(id)}`),

  /** POST /api/decisions — upsert a decision record. */
  createDecision: (body: Partial<Decision> & { title: string }): Promise<Decision> =>
    post<Decision>('/api/decisions', body),

  /** GET /api/compose/briefs — built-in starter briefs (workflow templates). */
  composeBriefs: (): Promise<StarterBrief[]> => get<StarterBrief[]>('/api/compose/briefs'),

  /**
   * GET /api/graph/knowledge — knowledge graph, optionally filtered by kinds +
   * project. `project` distinguishes "no filter" (`undefined`/`null` — omit
   * the param) from "filter to exactly this value" (a string, including `''`
   * for unscoped-only).
   */
  knowledgeGraph: (kinds?: string[], project?: string | null): Promise<KnowledgeGraph> => {
    const p = new URLSearchParams();
    if (kinds && kinds.length) p.set('kinds', kinds.join(','));
    if (project != null) p.set('project', project);
    const qs = p.toString();
    return get<KnowledgeGraph>(`/api/graph/knowledge${qs ? `?${qs}` : ''}`);
  },

  // ── Auth (remote access) ─────────────────────────────────────────────────

  /** GET /api/auth/check — whether a token is required and if we're authed. */
  authCheck: (): Promise<AuthStatus> => get<AuthStatus>('/api/auth/check'),

  /** POST /api/auth/login — exchange a token for an hp_token cookie. */
  login: (token: string): Promise<{ ok: boolean }> =>
    post<{ ok: boolean }>('/api/auth/login', { token }),

  /** POST /api/auth/logout — clear the auth cookie. */
  logout: (): Promise<{ ok: boolean }> => post<{ ok: boolean }>('/api/auth/logout', {}),
};

/**
 * terminalWsUrl builds the WebSocket URL for a PTY terminal session. Auth rides
 * the hp_token cookie (same-origin), so no token is appended here.
 */
export function terminalWsUrl(kind: 'orchestrator' | 'shell', cols: number, rows: number): string {
  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws';
  return `${proto}://${window.location.host}/api/terminal/ws?kind=${kind}&cols=${cols}&rows=${rows}`;
}
