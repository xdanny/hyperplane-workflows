/**
 * "Your result" — the first card on a finished run's page (F-G).
 *
 * The audited page put the heading, a chip, six reviewer bullets and ~170 words
 * of boilerplate above the deliverable, which was then the third of three
 * identical "Read document" links (Q-02, P-06). Four people independently
 * reported that they could not find what a run produced.
 *
 * So this card is first, and it carries exactly three things in this order:
 * what was produced (with its first lines already rendered), what the
 * independent review said about it, and whose turn it is next.
 */

import { useState } from 'react';
import { Link } from 'react-router-dom';
import { renderMarkdown } from './DocumentViewer';
import type { ResultDocument, ReviewFinding, ReviewSummary } from '../runResult';
import { firstLines, resultCardVisible } from '../runResult';
import { nodeFileURL } from '../workflowNodePresentation';
import type { RunDocumentRef } from './RunDocumentReader';

/** The preview text of each result document, fetched once for the card. */
export interface ResultPreview {
  nodeId: string;
  path: string;
  text?: string;
}

export function RunResultCard({
  runId,
  documents,
  previews,
  review,
  nextActionText,
  nextActionKind,
  onOpenDocument,
  onOpenReview,
  decision,
  runState,
}: {
  runId: string;
  documents: ResultDocument[];
  previews: ResultPreview[];
  review: ReviewSummary | null;
  nextActionText: string;
  nextActionKind: 'approval' | 'sign_off' | 'none';
  onOpenDocument: (doc: RunDocumentRef) => void;
  onOpenReview: () => void;
  /** Inline sign-off controls, rendered when the journey wants a decision. */
  decision?: React.ReactNode;
  /** The run state; a failed or stopped run must not call its last draft "Your result". */
  runState?: string;
}) {
  const [showFindings, setShowFindings] = useState(false);
  // `decision` alone is reason enough to render: a finished run under an
  // optional sign-off policy has no queued next action, and hiding the card
  // would hide the only controls that can decide it (S-14).
  if (!resultCardVisible({ documents: documents.length, review: Boolean(review), nextActionKind, decision: Boolean(decision) })) return null;

  return (
    <section className="panel sdk-run-section run-result" aria-label="Your result">
      <h2>{review?.verdict === 'failed' ? 'Draft that failed review — read the findings before using it' : runState === 'interrupted' ? 'Partial output from a stopped run' : runState === 'failed' ? 'Output before the failure' : 'Your result'}</h2>

      {documents.length === 0 ? (
        <p className="faint">No document was recorded for the last step of this workflow.</p>
      ) : (
        <ul className="run-result-docs">
          {documents.map((doc) => {
            const url = nodeFileURL(runId, doc.file);
            const preview = previews.find((p) => p.path === doc.file.path)?.text;
            return (
              <li key={doc.file.path} className="run-result-doc">
                <div className="run-result-doc-head">
                  <div>
                    <strong className="run-result-doc-title">{doc.stepTitle}</strong>
                    <span className="run-result-doc-file mono faint"> {doc.file.path}</span>
                  </div>
                  <button
                    className="btn btn--primary btn--sm"
                    onClick={() => onOpenDocument({ url, title: doc.stepTitle, stepTitle: doc.stepTitle, sha256: doc.file.sha256 })}
                  >
                    Open
                  </button>
                </div>
                {preview !== undefined && preview !== '' && (
                  <div
                    className="run-result-preview dv-body"
                    // renderMarkdown escapes its input and allowlists link
                    // schemes; it is the same transformer the viewer uses.
                    dangerouslySetInnerHTML={{ __html: renderMarkdown(firstLines(preview, 6)) }}
                  />
                )}
              </li>
            );
          })}
        </ul>
      )}

      {review && (
        <div className={`run-result-review run-result-review--${review.verdict}`}>
          <p className="run-result-review-line">
            {review.line}
            {review.findings.length > 0 && (
              <button
                className="btn btn--ghost btn--sm"
                aria-expanded={showFindings}
                onClick={() => setShowFindings((open) => !open)}
              >
                {showFindings ? 'Hide findings' : `Show ${review.findings.length} finding${review.findings.length === 1 ? '' : 's'}`}
              </button>
            )}
            {(review.document || review.stepId) && (
              <button className="btn btn--ghost btn--sm" onClick={onOpenReview}>
                {review.document ? 'Read the review' : 'Read the reviewer’s session log'}
              </button>
            )}
          </p>
          {showFindings && <ReviewFindings details={review.details} />}
        </div>
      )}

      {nextActionKind !== 'none' && (
        <p className="run-result-next" role="status">
          {nextActionText}
          {nextActionKind === 'sign_off' && <> <Link to="/inbox">Sign off here</Link>.</>}
        </p>
      )}
      {decision}
    </section>
  );
}

/** One finding, quoting the reviewer with its links intact (S-04). */
function FindingList({ findings }: { findings: ReviewFinding[] }) {
  return (
    <ul className="run-result-findings">
      {findings.map((finding, index) => (
        <li key={index} dangerouslySetInnerHTML={{ __html: renderMarkdown(finding.text) }} />
      ))}
    </ul>
  );
}

/**
 * The reviewer's findings, sorted by what they oblige the reader to do.
 *
 * A flat list made "3 findings" mean one unresolved concern or three
 * agreements, indistinguishably (S-17). What FAILED and what was raised as a
 * caveat are the two things worth reading before using the result, so they are
 * open under their own headings; what the reviewer CONFIRMED is evidence the
 * review was substantive and is one click away, not in the way.
 */
function ReviewFindings({ details }: { details: ReviewFinding[] }) {
  const failed = details.filter((finding) => finding.level === 'fail');
  const caveats = details.filter((finding) => finding.level === 'caveat');
  const confirmed = details.filter((finding) => finding.level === 'confirmation');
  return (
    <div className="run-result-finding-groups">
      {failed.length > 0 && <><h3 className="run-result-finding-head">Failed</h3><FindingList findings={failed} /></>}
      {caveats.length > 0 && <><h3 className="run-result-finding-head">Caveats</h3><FindingList findings={caveats} /></>}
      {confirmed.length > 0 && (
        <details className="run-result-confirmed">
          <summary>What the review confirmed ({confirmed.length})</summary>
          <FindingList findings={confirmed} />
        </details>
      )}
    </div>
  );
}
