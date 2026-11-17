import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { api, learnedProvenanceSentence, regressionPolicySentence, skillLineage, supportLabel } from '../api';
import type { Decision, InboxItem, Candidate, PersonaReview, OutcomeVerdict } from '../api';
import { clip, needsYouChip, needsYouTitle } from '../runLexicon';
import { decisionLedgerLine, ledgerVerdict, ledgerVerdictClass } from '../decisionLedger';
import type { LedgerVerdict } from '../decisionLedger';
import { confidenceLabel, draftRationale, evidenceBoundary, selfReportedConfidence, splitEvidence } from '../draftSkill';
import { expandedReason, fullFailureReason, isElided } from '../failureReason';
import { useDeliveredExcerpt } from '../hooks/useDeliveredExcerpt';
import { renderMarkdown } from '../components/DocumentViewer';
import { JourneyCard } from '../components/JourneyCard';
import { OutputView, cardTitle } from '../components/OutputView';
import { DocumentViewer, detectDocShape } from '../components/DocumentViewer';
import { PersonaVerdict } from '../components/PersonaVerdict';
import { GeneratePracticeTest } from '../components/GeneratePracticeTest';
import { ToleranceOriginChip, ToleranceOriginSummary, originOf } from '../components/ToleranceOrigin';
import { useSelectedRun } from '../hooks/useSelectedRun';
import { gateSentence, useApprovalGate } from '../hooks/useApprovalGate';
import type { ApprovalGate } from '../hooks/useApprovalGate';

// ── Filter type ───────────────────────────────────────────────────────────────
// One queue, four kinds of "your turn": a gate wants an approval, a gate wants
// a pick, a finished run wants a verdict on what it delivered, or the platform
// learned something from an accepted delivery and wants to know if it should
// keep it.
type FilterKind = 'all' | 'approval' | 'outcome' | 'selection' | 'proposal' | 'decided';

/** The tab names, in the order a person meets the work (F-D, Q-05).
 *  "Selections" and "Deliveries" appeared on no other screen; "Sign-off" and
 *  "Choices" are what the cards underneath actually ask for. */
const FILTER_LABEL: Record<FilterKind, string> = {
  all: 'All',
  approval: 'Approvals',
  outcome: 'Sign-off',
  selection: 'Choices',
  proposal: 'Learned methods',
  decided: 'Decided',
};

/** One line saying what each tab holds — shown in its empty state, because a
 *  tab nobody has defined teaches nothing when it is empty (Q-05). */
const FILTER_DEFINITION: Record<FilterKind, string> = {
  all: 'Everything waiting on you, newest first.',
  approval: 'A run has paused mid-way and cannot continue until you approve or reject the step it is on.',
  outcome: 'A run has finished. Signing off records your judgment of what it produced; it is optional and nothing is sent.',
  selection: 'A run produced several candidates and is waiting for you to pick one.',
  proposal: 'A delivery you accepted taught the system a method. Approving keeps it; it stays out of every prompt until you do.',
  decided: 'Decisions already recorded, with who decided, when, and the note they left.',
};

const FILTER_ORDER: FilterKind[] = ['all', 'approval', 'outcome', 'selection', 'proposal', 'decided'];

/** The verdict word on a Decided row's chip. */
const VERDICT_CHIP: Record<LedgerVerdict, string> = {
  accepted: 'accepted',
  changes_requested: 'changes requested',
  rejected: 'rejected',
};

// ── helpers for schema-driven comparison ──────────────────────────────────────
const SUMMARY_KEYS = new Set(['rationale', 'summary', 'description', 'killerDifferentiator',
  'title', 'name', 'label', 'id']);

function deriveCompareColumns(candidates: Candidate[], compareBy?: string[]): string[] {
  if (compareBy && compareBy.length > 0) return compareBy.slice(0, 5);
  const counts = new Map<string, number>();
  for (const c of candidates) {
    for (const k of Object.keys(c)) {
      if (SUMMARY_KEYS.has(k)) continue;
      const v = c[k];
      if (v === null || v === undefined || typeof v === 'object') continue;
      counts.set(k, (counts.get(k) ?? 0) + 1);
    }
  }
  return [...counts.entries()]
    .sort((a, b) => b[1] - a[1])
    .slice(0, 5)
    .map(([k]) => k);
}

function EvidenceListCards({ items }: { items: Record<string, unknown>[] }) {
  return (
    <div className="evidence-list-cards">
      {/* Whose numbers these are, before the rows they are buried in. */}
      <ToleranceOriginSummary rows={items} />
      {items.slice(0, 8).map((item, i) => {
        // `toleranceOrigin` is lifted out of the key/value rows and rendered as
        // a chip: "declared by the owner" and "proposed by the agent" are two
        // different decisions, and as one more row of JSON they read as one.
        const origin = originOf(item);
        const entries = Object.entries(item).filter(
          ([k, v]) => k !== 'toleranceOrigin' && (typeof v === 'string' || typeof v === 'number'),
        );
        return (
          <div key={i} className="evidence-list-card">
            <span className="evidence-list-card-num mono">{i + 1}</span>
            <div className="evidence-list-card-body">
              {entries.map(([k, v]) => (
                <div key={k} className="evidence-list-card-row">
                  <span className="evidence-list-card-key">{k}</span>
                  <span className="evidence-list-card-val">{String(v)}</span>
                </div>
              ))}
              {origin && <ToleranceOriginChip origin={origin} />}
            </div>
          </div>
        );
      })}
    </div>
  );
}

function CandidateCompareTable({
  candidates,
  compareBy,
  busy,
  onSelect,
}: {
  candidates: Candidate[];
  compareBy?: string[];
  busy: boolean;
  onSelect: (winner: string, c: Candidate) => void;
}) {
  const cols = deriveCompareColumns(candidates, compareBy);
  if (cols.length === 0) return null;

  return (
    <div className="cand-compare-wrap" style={{ overflowX: 'auto' }}>
      <table className="cand-compare-table" aria-label="Side-by-side candidate comparison">
        <thead>
          <tr>
            <th className="cand-compare-th cand-compare-th--label">Candidate</th>
            {cols.map((col) => (
              <th key={col} className="cand-compare-th">{col}</th>
            ))}
            <th className="cand-compare-th cand-compare-th--action" />
          </tr>
        </thead>
        <tbody>
          {candidates.map((c, i) => {
            const label = cardTitle(c, i);
            const winner = String((c as Record<string, unknown>).id ?? label);
            return (
              <tr key={i} className="cand-compare-row">
                <td className="cand-compare-td cand-compare-td--label">
                  <span className="cand-compare-name">{label}</span>
                </td>
                {cols.map((col) => {
                  const v = (c as Record<string, unknown>)[col];
                  return (
                    <td key={col} className="cand-compare-td">
                      {v !== undefined && v !== null ? String(v) : <span className="faint">—</span>}
                    </td>
                  );
                })}
                <td className="cand-compare-td cand-compare-td--action">
                  <button
                    className="btn btn--sm btn--primary"
                    disabled={busy}
                    onClick={() => onSelect(winner, c)}
                    aria-label={`Select candidate: ${label}`}
                  >
                    Pick
                  </button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

/**
 * DeliveryCard — one DELIVERY awaiting a verdict, with every attempt it took.
 *
 * This used to be one card per finished run, which meant seven failed runs at
 * two products asked seven questions. It is now one card per journey: the
 * attempts are listed as chapters, superseded ones included for context but
 * never asking on their own, and the single verdict covers the line.
 *
 * The card carries the decision because the evidence is on it — the chapter
 * strip shows what each attempt proved. Reading a full attempt is still one
 * click away in its Delivery tab, which remains the renderer.
 */
function DeliveryCard({ item, onOpen, onRecorded }: { item: InboxItem; onOpen: (runId: string) => void; onRecorded?: (recorded: { verdict: OutcomeVerdict; runId: string; name: string }) => void }) {
  const [showFullReason, setShowFullReason] = useState(false);
  const [showResult, setShowResult] = useState(false);
  const delivered = item.failureReason?.trim() ?? '';
  // The reason reaches this card already shortened by the server, so expanding
  // it means reading the run's own recorded findings (Q-12). Fetched only once
  // somebody asks, and only when there is more text to find.
  const reasonRunId = item.journey?.attempts.find((attempt) => !attempt.superseded)?.runId ?? item.journey?.root ?? '';
  const wantsFull = showFullReason && isElided(delivered) && reasonRunId !== '';
  const reasonRun = useQuery({
    queryKey: ['run', reasonRunId], queryFn: () => api.run(reasonRunId),
    enabled: wantsFull, staleTime: 60_000,
  });
  if (!item.journey) return null;
  const name = needsYouTitle({ type: 'outcome', workflowName: item.workflowName, runNumber: item.runNumber, title: item.title });
  const reason = delivered;
  const short = reason ? clip(reason, 200) : '';
  const expanded = expandedReason(reason, fullFailureReason(reasonRun.data));
  // Two different buttons: one that can show more, and none at all when the
  // sentence on the card is everything this app has. Offering "Show full
  // reason" for text that expands to the same clipped words is the defect.
  const canExpand = short !== reason || isElided(reason);
  return (
    <div className="inbox-signoff">
      {/* The card leads with WHAT is being signed off and a way to read it.
          The audited card's only link was a hash, and its failure reason was
          cut at ~200 characters with no way to expand (Q-04). */}
      <div className="inbox-signoff-head">
        <span className="chip s-block"><span className="dot" />SIGN OFF</span>
        <span className="inbox-signoff-name">{name}</span>
      </div>
      {item.resultDocument && (
        <p className="inbox-signoff-doc">
          Deliverable: {item.resultDocument.title} —{' '}
          <button type="button" className="btn btn--ghost btn--sm" aria-expanded={showResult} onClick={() => setShowResult((open) => !open)}>{showResult ? 'Hide it' : 'Read it'}</button>
        </p>
      )}
      {item.resultDocument && showResult && (
        <ApprovalDraftPreview document={item.resultDocument} eyebrow="The deliverable you are signing off" linkText="Open raw file" />
      )}
      {reason && (
        <p className="inbox-signoff-reason">
          <strong>{(item.journey?.proven ?? 0) < (item.journey?.criteria ?? 0) ? 'Why it failed:' : 'What the check found:'}</strong>{' '}
          {showFullReason ? expanded.text : short}
          {canExpand && (
            <button className="btn btn--ghost btn--sm" onClick={() => setShowFullReason((open) => !open)}>
              {showFullReason ? 'Show less' : 'Show full reason'}
            </button>
          )}
          {/* Said rather than hidden: the card is showing a shortened sentence
              and cannot lengthen it, so it points at the page that has the
              findings in full instead of leaving an ellipsis unexplained. */}
          {showFullReason && !expanded.complete && (
            <span className="faint">
              {reasonRun.isPending && wantsFull
                ? ' Reading the rest of this finding…'
                : ' This is as much of the finding as the delivery recorded.'}
              {reasonRunId !== '' && (
                <> <button type="button" className="btn btn--ghost btn--sm" onClick={() => onOpen(reasonRunId)}>Open the run</button></>
              )}
            </span>
          )}
        </p>
      )}
      <JourneyCard journey={item.journey} onOpenRun={onOpen} defaultOpen onRecorded={(recorded) => onRecorded?.({ ...recorded, name })} />
      <p className="faint inbox-signoff-caveat">
        Your decision records your judgment of this result. It does not change the recorded check result
        ({item.journey.criteria === 0 ? 'no checks were declared' : `${item.journey.proven} of ${item.journey.criteria} passed`}).
      </p>
    </div>
  );
}

/**
 * ProposalCard — a skill the platform learned from a delivery you accepted.
 *
 * This is the only card in the queue the platform raised on its own, so it says
 * plainly where it came from and what it currently is: a DRAFT, which means it
 * is in no prompt and offered to no workflow until it is approved. Extraction
 * runs automatically; keeping what it found does not.
 *
 * What the card offers as grounds is deliberately not a confidence score. A
 * self-reported "0.90" with no explanation invites trust it has not earned; the
 * two facts that can actually be checked are how many runs reached this method
 * and which lines in those runs say so. Both are here, every quote opens the
 * node it came from, and the run it was learned from is a link to THAT run
 * rather than to the runs list — provenance that cannot be followed is a claim,
 * not evidence. A draft extracted before evidence pinning existed says so
 * plainly instead of showing an empty space.
 */

/**
 * What the run delivered, shown when the pinned evidence is only a status word.
 *
 * Labelled as the delivery and not as evidence: the extractor did not choose
 * this excerpt, so calling it evidence would repeat D-03's mistake in the other
 * direction. It is here so a reader deciding whether to keep a method has the
 * text that method produced.
 */
function DeliveredInstead({ runId }: { runId?: string }) {
  const { excerpt, loading } = useDeliveredExcerpt(runId, Boolean(runId));
  if (loading) return <p className="kn-evidence-none faint">Reading what the run delivered…</p>;
  if (!excerpt) return null;
  return (
    <div className="kn-delivered">
      <span className="eyebrow">What the run delivered · {excerpt.stepTitle}</span>
      <blockquote className="kn-delivered-quote">{excerpt.text}</blockquote>
      <p className="faint mono kn-delivered-path">{excerpt.path}</p>
      <p className="faint">
        The first lines of the delivered document, not a quote the extraction pinned. Read the whole
        run before approving the method.
      </p>
    </div>
  );
}

function ProposalCard({
  item, busy, gate, error, reviews, onApprove, onReject, onOpenEvidence,
}: {
  item: InboxItem;
  busy: boolean;
  /** Whether the evalset rule refuses approval here, and what to say if so. */
  gate: ApprovalGate;
  error?: string;
  /** Persona reviews recorded for this draft, newest first. Advisory evidence
   *  only — see PersonaVerdict; never gates the Approve button above. */
  reviews: PersonaReview[];
  onApprove: () => void;
  onReject: () => void;
  onOpenEvidence: (runId: string, nodeId: string) => void;
}) {
  const [showInstruction, setShowInstruction] = useState(false);
  const skill = item.proposal;
  if (!skill) return null;
  const lineage = skillLineage(skill);
  const { sourceRun, evidence } = lineage;
  const support = supportLabel(lineage);
  // What the extraction itself said about how well founded this is. All three
  // exist on the record and none of them reached the card (D-03).
  const confidence = confidenceLabel(selfReportedConfidence(skill));
  const rationale = draftRationale(skill);
  const boundary = evidenceBoundary(skill);
  // `phase: passed` is a checkpoint field, not something the run delivered.
  const { quoted, statusOnly } = splitEvidence(evidence);
  // The bar the project sets for a change like this one. Named here because
  // this card is where the draft is approved, and honest about the fact that
  // approving it does not run the comparison.
  const policy = regressionPolicySentence(lineage);

  return (
    <div className="inbox-card panel">
      <div className="top">
        <span className="chip s-block"><span className="dot" />learned</span>
        <span className="q">{skill.name}</span>
        <span className="ref">
          {skill.category ?? 'skill'}
          {sourceRun && (
            <>
              {' · '}
              <Link
                to={`/runs/${encodeURIComponent(sourceRun)}`}
                className="ref"
                style={{ textDecoration: 'underline' }}
                title={`Open the run this was learned from (${sourceRun})`}
              >
                …{sourceRun.slice(-8)}
              </Link>
            </>
          )}
        </span>
      </div>

      <p className="decision-criterion">
        {/* One sentence, one source. A draft extracted from a REHEARSAL used to
            read "Learned from a passed run you accepted" — nothing passed for
            real, nobody accepted it, and following the link led to a run in
            plan mode with no decision on it. See learnedProvenanceSentence. */}
        {learnedProvenanceSentence(lineage)}
        {sourceRun ? (
          <>
            {' ('}
            <Link
              to={`/runs/${encodeURIComponent(sourceRun)}`}
              className="mono"
              style={{ textDecoration: 'underline' }}
              aria-label={`Open run ${sourceRun}, which this skill was learned from`}
            >
              run …{sourceRun.slice(-8)}
            </Link>
            {')'}
          </>
        ) : null}
        .{' '}It stays a draft — not used by any workflow — until you approve it.
      </p>

      {skill.description && <div className="kn-desc">{skill.description}</div>}

      <div className="kn-meta mono">
        {support && <span className="kn-support">{support}</span>}{' '}
        <button
          className="btn btn--ghost btn--sm"
          onClick={() => setShowInstruction((v) => !v)}
          aria-expanded={showInstruction}
        >
          {showInstruction ? 'Hide method' : 'Read the method'}
        </button>
      </div>

      {policy && (
        <div className="faint" style={{ fontSize: 'var(--t-sm)' }}>This draft {policy}</div>
      )}

      {/* The extraction's own assessment, in its own words. Hiding a 0.2 does
          not make a draft better founded; it just makes the card quieter about
          it than the record is (D-03). */}
      {(confidence || rationale) && (
        <div className="kn-selfreport">
          {confidence && <p><strong>How well founded the extractor thinks this is:</strong> {confidence}</p>}
          {rationale && <p className="faint">{rationale}</p>}
        </div>
      )}
      {boundary && (
        <div className="kn-boundary">
          <span className="eyebrow">Evidence boundary</span>
          <p>{boundary}</p>
        </div>
      )}

      {quoted.length > 0 ? (
        <ul className="kn-evidence">
          {quoted.map((ev, i) => {
            const runId = ev.runId ?? sourceRun;
            return (
              <li key={`${ev.nodeId}-${i}`} className="kn-evidence-item">
                <q className="kn-evidence-quote">{ev.quote}</q>
                {runId ? (
                  <button
                    type="button"
                    className="btn btn--ghost btn--sm mono"
                    onClick={() => onOpenEvidence(runId, ev.nodeId)}
                    aria-label={`Open node ${ev.nodeId} of run ${runId}`}
                  >
                    {ev.nodeId} ↗
                  </button>
                ) : (
                  <span className="mono kn-evidence-node">{ev.nodeId}</span>
                )}
              </li>
            );
          })}
        </ul>
      ) : statusOnly.length > 0 ? (
        // The card used to print `phase: passed` inside quotation marks as the
        // grounds for a method. It is a recorded status, and it is named as
        // one here rather than dressed up as delivered text (D-03) — and the
        // delivery itself is shown underneath, so the reader has something the
        // run produced to judge the method against.
        <>
          <p className="kn-evidence-none faint">
            No delivered text was pinned as evidence — the extraction pinned only a recorded step status
            ({statusOnly.map((ev) => `${ev.nodeId}: ${ev.quote}`).join(', ')}), which records that a step
            finished and says nothing about what it produced.
            {sourceRun && ' What the run delivered is below; read it before approving this.'}
          </p>
          <DeliveredInstead runId={sourceRun} />
        </>
      ) : (
        // Drafts extracted before evidence pinning have no quotes and never
        // will. Saying which it is beats an empty space that reads as "nothing
        // supports this".
        <p className="kn-evidence-none faint">
          No pinned evidence — extracted before evidence pinning.
          {sourceRun && ' Its grounds have to be read in the run itself.'}
        </p>
      )}

      {showInstruction && skill.instruction && (
        <pre className="sl-instruction">{skill.instruction}</pre>
      )}

      {/* A competent reader's judgement, beside the measured evidence above —
          never a second gate. See PersonaVerdict. */}
      <PersonaVerdict reviews={reviews} />

      {/* The evalset rule, stated where the decision is: the button is refused
          here rather than at the server, and the sentence says what clears it.
          The 409 stays as the backstop for anything this cannot see. */}
      {gate.blocked && (
        <div className="kn-feedback">
          {gate.reason}
          {gate.command && (
            <>
              {': '}
              <code className="mono">{gate.command}</code>
            </>
          )}
        </div>
      )}
      {/* …or press this, which runs the same generator that command runs. The
          remedy for a blocked Approve was a terminal nobody reading this card
          has open (D-04). The gate is not lifted by generating — it is lifted
          by the evalset existing, which is a fact this then re-reads. */}
      {gate.blocked && gate.command && <GeneratePracticeTest skillId={skill.id} />}

      {/* Whatever the server refused with, shown rather than swallowed. */}
      {error && <div className="kn-feedback">{error}</div>}

      <div className="acts">
        <button
          className="btn btn--primary btn--sm"
          disabled={busy || gate.blocked}
          onClick={onApprove}
          title={gate.blocked ? gateSentence(gate) : undefined}
          aria-label={
            gate.blocked
              ? `Approve is blocked for ${skill.id}: ${gateSentence(gate)}`
              : `Approve learned skill ${skill.id}`
          }
        >
          Approve
        </button>
        {/* Rejecting an untested draft is always available — the gate is on
            letting it into prompts, not on discarding it. */}
        <button className="btn btn--sm" disabled={busy} onClick={onReject}
          aria-label={`Reject learned skill ${skill.id}`}>
          Reject
        </button>
      </div>
    </div>
  );
}

/**
 * The draft this approval is about, rendered inline above the buttons.
 *
 * A person asked to approve text they have not been shown will approve open
 * placeholders — Quinn watched a draft that ended "Details to confirm: launch
 * date, designated reviewer" sit 400 words below its own Approve button
 * (Q-13, P-08). The card shows it; the link still opens the full reader.
 */
function ApprovalDraftPreview({ document: draft, eyebrow = 'What you are approving', linkText = 'Open the full document' }: { document: { path?: string; url: string }; eyebrow?: string; linkText?: string }) {
  const { data } = useQuery({
    queryKey: ['document', draft.url],
    queryFn: async () => {
      const response = await fetch(draft.url);
      if (!response.ok) throw new Error(String(response.status));
      return response.text();
    },
    staleTime: 30_000,
  });
  if (typeof data !== 'string' || data.trim() === '') return null;
  return (
    <div className="inbox-draft">
      <div className="eyebrow">{eyebrow}</div>
      {/* renderMarkdown escapes its input and allowlists link schemes. */}
      <div className="dv-body" dangerouslySetInnerHTML={{ __html: renderMarkdown(data) }} />
      <a href={draft.url} target="_blank" rel="noreferrer">{linkText}</a>
    </div>
  );
}

function DecisionCard({
  item, busy, decided, onApprove, onSelect, onDismiss,
}: {
  item: InboxItem;
  busy: boolean;
  /** 'approved' | 'rejected' once this card's own decision has been recorded. */
  decided?: 'approved' | 'rejected';
  onApprove: (approved: boolean) => void;
  onSelect: (winner: string, candidate: Candidate) => void;
  onDismiss: () => void;
}) {
  const isSelection = item.type === 'selection';
  const evidence = item.evidence ? Object.entries(item.evidence) : [];
  const candidates = item.candidates ?? [];

  const cols = candidates.length > 0 ? deriveCompareColumns(candidates, item.compareBy) : [];
  const showCompareTable = isSelection && candidates.length > 0 && cols.length > 0;

  // The card's own name, before anything else on it: a workflow and a run
  // number, not "node-workflow …3a920a45" (P-08, S-06, Q-04).
  const heading = needsYouTitle({
    type: item.type,
    workflowName: item.workflowName,
    runNumber: item.runNumber,
    title: item.title,
    prompt: item.prompt,
  });

  return (
    <div className="inbox-card panel">
      <div className="top">
        <span className={`chip ${isSelection ? 's-live' : 's-block'}`}>
          <span className="dot" />{needsYouChip(item.type)}
        </span>
        <span className="q">{heading}</span>
        <span className="ref">
          {item.gateNode ? `${item.gateNode} · ` : ''}
          {item.runId && (
            <Link to={`/runs/${encodeURIComponent(item.runId)}`} className="ref" style={{ textDecoration: 'underline' }}>Open run</Link>
          )}
        </span>
        <button
          className="btn btn--ghost btn--sm inbox-dismiss-btn"
          onClick={onDismiss}
          disabled={busy}
          aria-label="Dismiss"
        >✕</button>
      </div>

      {item.goal && <p className="inbox-goal">{item.goal}</p>}
      {item.prompt && item.prompt !== heading && (
        <p className="decision-criterion">{item.prompt}</p>
      )}

      {/* The draft, above the buttons (Q-13). */}
      {!isSelection && item.draftDocument && <ApprovalDraftPreview document={item.draftDocument} />}

      {showCompareTable && (
        <>
          <div className="eyebrow" style={{ margin: 'var(--s3) 0 var(--s2)' }}>
            {candidates.length} candidates — comparing {cols.join(', ')}
          </div>
          <CandidateCompareTable
            candidates={candidates}
            compareBy={item.compareBy}
            busy={busy}
            onSelect={onSelect}
          />
        </>
      )}

      {isSelection && candidates.length > 0 && !showCompareTable && (
        <>
          <div className="eyebrow" style={{ margin: 'var(--s3) 0 var(--s2)' }}>
            {candidates.length} candidates — pick one
          </div>
          <div className="cand-grid">
            {candidates.map((c, i) => {
              const label = cardTitle(c, i);
              const winner = String((c as Record<string, unknown>).id ?? label);
              const summary =
                (c.rationale as string) ?? (c.summary as string) ?? (c.description as string) ?? (c.killerDifferentiator as string);
              return (
                <button key={i} className="cand" disabled={busy} onClick={() => onSelect(winner, c)}
                  aria-label={`Select candidate: ${label}`}>
                  <div className="cl">{label}</div>
                  {summary && <div className="cs">{summary}</div>}
                  <div className="idea-chips">
                    {typeof c.impact === 'string' && <span className="tag">impact: {c.impact}</span>}
                    {typeof c.effort === 'string' && <span className="tag">effort: {c.effort}</span>}
                  </div>
                </button>
              );
            })}
          </div>
        </>
      )}

      {item.documents && item.documents.length > 0 && <div className="evidence"><p>Documents under review</p><ul>{item.documents.map(doc => <li key={doc.url}><a href={doc.url} target="_blank" rel="noreferrer">{doc.name}</a></li>)}</ul></div>}
      {!isSelection && evidence.length > 0 && (
        <div className="evidence">
          <div className="eyebrow" style={{ marginBottom: 'var(--s2)' }}>Evidence under review</div>
          {evidence.map(([nodeId, output]) => {
            const listItems = (() => {
              if (!output || typeof output !== 'object' || Array.isArray(output)) return null;
              const o = output as Record<string, unknown>;
              for (const key of Object.keys(o)) {
                const val = o[key];
                if (Array.isArray(val) && val.length > 0 && val.every((x) => x && typeof x === 'object')) {
                  return val as Record<string, unknown>[];
                }
              }
              return null;
            })();
            const docShape = output ? detectDocShape(output) : null;
            return (
              <div className="evidence-block" key={nodeId}>
                <div className="evidence-node mono">{nodeId}</div>
                {listItems ? (
                  <EvidenceListCards items={listItems} />
                ) : docShape ? (
                  <DocumentViewer value={output} showControls={true} />
                ) : (
                  <OutputView value={output} />
                )}
              </div>
            );
          })}
        </div>
      )}

      {!isSelection && (
        <>
          {/* What approving actually sets off, and what it does not. "Nothing
              is sent" is the sentence P-08 went to the run page to find. */}
          {item.nextSteps !== undefined && (
            <p className="inbox-consequence">
              {item.nextSteps.length > 0
                ? <>After you approve: {item.nextSteps.join(', ')} {item.nextSteps.length === 1 ? 'runs' : 'run'} (nothing is sent).</>
                : <>No step waits for this approval — the run will finish without it.</>}
            </p>
          )}
          {decided ? (
            <p className="inbox-decided" role="status">
              {decided === 'approved' ? 'Approved — the run continues.' : 'Rejected — the run stops here.'}
              {item.runId && <> <Link to={`/runs/${encodeURIComponent(item.runId)}`}>Open run</Link>.</>}
            </p>
          ) : (
            <div className="acts">
              <button className="btn btn--primary btn--sm" disabled={busy} onClick={() => onApprove(true)}>Approve</button>
              <button className="btn btn--sm" disabled={busy} onClick={() => onApprove(false)}>Reject</button>
            </div>
          )}
        </>
      )}
    </div>
  );
}

// ── Group items by run ────────────────────────────────────────────────────────
interface RunGroup {
  runId: string;
  /** The heading: the delivery's name, the saved workflow's name, or — only
   *  when neither exists — the runtime label the group used to always show. */
  dag: string;
  /** True when `dag` is a name somebody chose rather than a runtime label. */
  named: boolean;
  /** Which run of that workflow, when the server resolved one. */
  runNumber?: number;
  items: InboxItem[];
  newestAt: string;
}

function groupByRun(items: InboxItem[]): RunGroup[] {
  const map = new Map<string, RunGroup>();
  for (const item of items) {
    // A delivery groups by its JOURNEY, not by a run: the card is about a line
    // of attempts, so pinning it to one attempt's id would merge it with that
    // run's mid-run gates and split it from the rest of its own story.
    const key = item.journey ? `journey:${item.journey.key}` : (item.runId ?? '__unknown__');
    if (!map.has(key)) {
      // The saved workflow's own name first: a group headed "node-workflow
      // …ed6227a2" named the RUNTIME and a hash, neither of which is the work
      // a person is being asked about (S-17). `run #N` replaces the id tail
      // wherever the server resolved one.
      const named = item.workflowName?.trim();
      map.set(key, {
        runId: key,
        dag: item.journey ? item.journey.name : (named || item.dag || 'workflow'),
        named: Boolean(item.journey || named),
        runNumber: item.journey ? undefined : item.runNumber,
        items: [],
        newestAt: item.updatedAt ?? '',
      });
    }
    const g = map.get(key)!;
    g.items.push(item);
    if ((item.updatedAt ?? '') > g.newestAt) g.newestAt = item.updatedAt ?? '';
  }
  return [...map.values()].sort((a, b) => b.newestAt.localeCompare(a.newestAt));
}

// ── Inbox view ────────────────────────────────────────────────────────────────
export function Inbox() {
  const qc = useQueryClient();
  const { setRunId, openRun } = useSelectedRun();
  const [filter, setFilter] = useState<FilterKind>('all');
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  // Which proposal a learning action failed on, and what the server said. Kept
  // per-skill so a refused approval explains itself on its own card.
  const [learnError, setLearnError] = useState<{ id: string; message: string } | null>(null);
  // What this session decided, so a card can say "Approved — the run
  // continues. Open run" instead of vanishing with no confirmation (P-08, S-01).
  const [decided, setDecided] = useState<Record<string, 'approved' | 'rejected'>>({});
  // Sign-offs recorded in this session. The card leaves the queue when the Inbox
  // refetches, so the confirmation has to live outside it (P-15).
  const [recorded, setRecorded] = useState<{ verdict: OutcomeVerdict; runId: string; name: string }[]>([]);

  // Opening a delivery selects the run and lands on Runs, which opens a
  // finished run on its Delivery tab.
  const openDelivery = (runId: string | undefined) => {
    if (!runId) return;
    setRunId(runId);
  };

  const { data: items, isLoading, isError } = useQuery<InboxItem[]>({
    queryKey: ['inbox'], queryFn: () => api.inbox(), refetchInterval: 5000,
  });

  // Every persona review, fetched ONCE here rather than once per proposal card,
  // and sliced below to each draft's own target — advisory evidence beside the
  // measured one, never a second gate.
  const { data: personaReviews } = useQuery<PersonaReview[]>({
    queryKey: ['personaReviews'], queryFn: () => api.personaReviews(),
  });
  const safePersonaReviews = Array.isArray(personaReviews) ? personaReviews : [];

  // The evalset rule, asked here rather than left to the server's 409: this is
  // one of the two surfaces that carries an Approve button.
  const approvalGate = useApprovalGate();

  // Recorded decisions, for the Decided tab. Answering a card used to make it
  // vanish with no confirmation and no record a person could point at (S-01).
  const { data: decisions } = useQuery<Decision[]>({
    queryKey: ['decisions', null],
    queryFn: () => api.decisions(),
    enabled: filter === 'decided' || filter === 'all',
  });

  const approve = useMutation({
    mutationFn: ({ id, approved }: { id: string; approved: boolean }) => api.approveDecision(id, approved),
    onSuccess: (_result, { id, approved }) => {
      setDecided((current) => ({ ...current, [id]: approved ? 'approved' : 'rejected' }));
      void qc.invalidateQueries({ queryKey: ['inbox'] });
      void qc.invalidateQueries({ queryKey: ['runs'] });
    },
  });
  const select = useMutation({
    mutationFn: ({ id, winner, candidate }: { id: string; winner: string; candidate: Candidate }) =>
      api.selectDecision(id, winner, candidate),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['inbox'] }),
  });
  const dismiss = useMutation({
    mutationFn: ({ type, id }: { type: 'approvals' | 'selections'; id: string }) =>
      api.dismissInboxItem(type, id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['inbox'] }),
  });
  const clearStale = useMutation({
    mutationFn: () => api.clearStaleInbox(),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['inbox'] }),
  });
  // Answering a proposal is the same act as approving the draft in the
  // Knowledge registry — one row, one lifecycle — so both queries refresh.
  const refreshLearning = () => {
    setLearnError(null);
    void qc.invalidateQueries({ queryKey: ['inbox'] });
    void qc.invalidateQueries({ queryKey: ['skills'] });
  };
  const learnFailed = (id: string, err: unknown) =>
    setLearnError({ id, message: err instanceof Error ? err.message : 'Failed' });
  const approveSkill = useMutation({
    mutationFn: (id: string) => api.approveSkill(id),
    onMutate: () => setLearnError(null),
    onSuccess: refreshLearning,
    onError: (err, id) => learnFailed(id, err),
  });
  const rejectSkill = useMutation({
    mutationFn: (id: string) => api.rejectSkill(id),
    onMutate: () => setLearnError(null),
    onSuccess: refreshLearning,
    onError: (err, id) => learnFailed(id, err),
  });

  const all = Array.isArray(items) ? items : [];
  // "Decided" is a record, not a queue: it lists what has already been answered
  // rather than filtering the items still waiting.
  const filtered = filter === 'all' || filter === 'decided' ? all : all.filter((i) => i.type === filter);
  const groups = groupByRun(filtered);
  const totalCount = all.length;
  const busy = approve.isPending || select.isPending || dismiss.isPending;
  const learnBusy = approveSkill.isPending || rejectSkill.isPending;

  function toggleCollapse(runId: string) {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(runId)) next.delete(runId);
      else next.add(runId);
      return next;
    });
  }

  return (
    <div className="view">
      <div className="view-head">
        <span className="eyebrow">Decisions</span>
        <h1 className="h1">
          Inbox
          {totalCount > 0 && (
            <span className="inbox-count-badge" aria-label={`${totalCount} items waiting`}>
              {totalCount}
            </span>
          )}
        </h1>
      </div>

      {/* Filter bar */}
      <div className="inbox-toolbar">
        <div className="inbox-filter-tabs" role="tablist" aria-label="Filter inbox by type">
          {FILTER_ORDER.map((f) => (
            <button
              key={f}
              role="tab"
              aria-selected={filter === f}
              className={`inbox-filter-tab${filter === f ? ' inbox-filter-tab--active' : ''}`}
              onClick={() => setFilter(f)}
            >
              {FILTER_LABEL[f]}
            </button>
          ))}
        </div>
        {/* "Stale" is the app's word, not the reader's: say what leaves. */}
        <button
          className="btn btn--sm btn--ghost inbox-clear-btn"
          onClick={() => clearStale.mutate()}
          disabled={clearStale.isPending}
          title="Removes items whose run has since finished or been withdrawn."
          aria-label="Clear stale items from inbox — removes items whose run has since finished or been withdrawn"
        >
          Clear stale
        </button>
      </div>

      {isLoading && <div className="empty"><span>Loading…</span></div>}
      {isError && <div className="errbox"><div className="h2">Failed to load inbox</div></div>}

      {/* The Decided tab: what has already been answered, by whom, when, and
          with what note — the record S-01 could not point at. */}
      {!isLoading && filter === 'decided' && (
        <div className="inbox-decided-list">
          {(decisions ?? []).length === 0 ? (
            <div className="inbox-explainer panel">
              <span className="eyebrow">What lands here</span>
              <p>{FILTER_DEFINITION.decided}</p>
            </div>
          ) : (
            (decisions ?? []).slice(0, 25).map((decision) => {
              // The chip used to render `status` — the record's own lifecycle,
              // which is "accepted" for every outcome ever written — so a
              // rejection was labelled ACCEPTED directly above the rejecting
              // note (S-13, D-15). The verdict is `decision`, and the line
              // beside it names the work rather than a `journey:` id.
              const verdict = ledgerVerdict(decision);
              return (
                <div key={decision.id} className="inbox-decided-row panel">
                  <div className="inbox-decided-head">
                    <span className="inbox-decided-title">{decision.title}</span>
                    <span className={`chip ${ledgerVerdictClass(verdict)}`}>
                      {verdict ? VERDICT_CHIP[verdict] : 'recorded'}
                    </span>
                  </div>
                  <p className="faint">
                    {decisionLedgerLine(decision)}
                    {decision.project ? ` · ${decision.project}` : ''}
                  </p>
                  {decision.why && <p className="inbox-decided-note">“{decision.why}”</p>}
                  {decision.runId && <Link to={`/runs/${encodeURIComponent(decision.runId)}`}>Open the run this decided</Link>}
                </div>
              );
            })
          )}
        </div>
      )}

      {!isLoading && !isError && filter !== 'decided' && groups.length === 0 && (
        <>
          <div className="empty inbox-empty" style={{ paddingTop: 'var(--s7)' }}>
            <span className="inbox-empty-mark">✓</span>
            <span>Nothing needs your attention.</span>
          </div>
          {/* A tab whose name nobody has defined teaches nothing when it is
              empty, so every tab says what would appear in it (Q-05). */}
          <div className="inbox-explainer panel">
            <span className="eyebrow">What lands here</span>
            <p>{FILTER_DEFINITION[filter]}</p>
            {filter === 'proposal' && (
              <p className="faint">
                A draft is inert until you approve it — it is in no prompt and offered to no workflow.
              </p>
            )}
          </div>
        </>
      )}

      {recorded.length > 0 && (
        <div className="inbox-recorded" role="status">
          {recorded.map((entry) => (
            <p key={entry.runId} className="inbox-recorded-line">
              <strong>{entry.verdict === 'accepted' ? 'Accepted' : entry.verdict === 'rejected' ? 'Rejected' : 'Changes requested'}:</strong> {entry.name}.{' '}
              {entry.verdict === 'accepted' ? 'The result is marked final for your team. Nothing is sent.' : entry.verdict === 'rejected' ? 'The result is marked rejected. Nothing is sent.' : 'Your note is recorded with the result. Nothing is sent.'}{' '}
              <button type="button" className="btn btn--ghost btn--sm" onClick={() => openDelivery(entry.runId)}>Open run</button>{' '}
              <button type="button" className="btn btn--ghost btn--sm" aria-label="Dismiss this confirmation" onClick={() => setRecorded((current) => current.filter((e) => e.runId !== entry.runId))}>×</button>
            </p>
          ))}
        </div>
      )}
      <div className="inbox-list">
        {(filter === 'decided' ? [] : groups).map((group) => {
          const isCollapsed = collapsed.has(group.runId);
          // A journey group is identified by its derived key, which is not a run
          // id and has no tail worth showing — the attempts carry their own.
          const isJourney = group.runId.startsWith('journey:');
          // "run #4" when the server resolved one; the id tail only for a group
          // that has no name to carry.
          const runTail = group.runNumber
            ? `run #${group.runNumber}`
            : !isJourney && !group.named && group.runId !== '__unknown__' ? `…${group.runId.slice(-8)}` : '';
          return (
            <div key={group.runId} className="inbox-group">
              <button
                className="inbox-group-header"
                onClick={() => toggleCollapse(group.runId)}
                aria-expanded={!isCollapsed}
                aria-label={`${group.dag}${runTail ? ' ' + runTail : ''} — ${group.items.length} item${group.items.length !== 1 ? 's' : ''}`}
              >
                <span className="inbox-group-dag">{group.dag}</span>
                {runTail && <span className="inbox-group-run mono">{runTail}</span>}
                <span className="inbox-group-count">{group.items.length}</span>
                <span className="inbox-group-chevron" aria-hidden="true">{isCollapsed ? '›' : '⌄'}</span>
              </button>
              {!isCollapsed && (
                <div className="inbox-group-items">
                  {group.items.map((item) =>
                    item.type === 'outcome' ? (
                      <DeliveryCard key={item.id} item={item} onOpen={openDelivery} onRecorded={(entry) => setRecorded((current) => [entry, ...current].slice(0, 5))} />
                    ) : item.type === 'proposal' ? (
                      <ProposalCard
                        key={item.id}
                        item={item}
                        busy={learnBusy}
                        gate={item.proposal ? approvalGate(item.proposal) : { blocked: false }}
                        error={learnError && learnError.id === item.proposal?.id ? learnError.message : undefined}
                        reviews={
                          item.proposal
                            ? safePersonaReviews.filter((r) => r.target === `skill:${item.proposal!.id}`)
                            : []
                        }
                        onApprove={() => item.proposal && approveSkill.mutate(item.proposal.id)}
                        onReject={() => item.proposal && rejectSkill.mutate(item.proposal.id)}
                        onOpenEvidence={(runId) => openRun(runId)}
                      />
                    ) : (
                      <DecisionCard
                        key={item.id}
                        item={item}
                        busy={busy}
                        decided={decided[item.id]}
                        onApprove={(approved) => approve.mutate({ id: item.id, approved })}
                        onSelect={(winner, candidate) => select.mutate({ id: item.id, winner, candidate })}
                        onDismiss={() => dismiss.mutate({ type: item.type === 'selection' ? 'selections' : 'approvals', id: item.id })}
                      />
                    ),
                  )}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
