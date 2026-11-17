/**
 * What an optional check asks of a step, and what its pass is worth (D-06).
 *
 * The audited block was one ~450-word paragraph. Buried in it was the sentence
 * that matters most — "it does not establish label/holdout custody, train/
 * holdout isolation, split correctness or frozen-model provenance" — which is
 * exactly right and which nobody was ever going to find. The catalog now
 * carries four short arrays; this renders each under the heading that says
 * which question it answers, so the boundary is a list a reader can scan
 * instead of a clause in a wall of prose.
 */

import { CHECK_CLAIM_HEADINGS, checkClaims } from '../api';
import type { WorkflowCheck } from '../api';

type ClaimKey = keyof typeof CHECK_CLAIM_HEADINGS;

/** One labelled list, rendered only when the catalog entry has that array. */
function ClaimList({ check, claim }: { check: WorkflowCheck; claim: ClaimKey }) {
  const lines = checkClaims(check, claim);
  if (lines.length === 0) return null;
  return <div className={`check-claim check-claim--${claim}`}>
    <h5 className="check-claim-head">{CHECK_CLAIM_HEADINGS[claim]}</h5>
    <ul>{lines.map((line, index) => <li key={index}>{line}</li>)}</ul>
  </div>;
}

/**
 * What a pass proves and what it does not — the pair a reader needs beside a
 * recorded result, without the setup instructions they no longer have to
 * follow. This is the inspector's half of the contract.
 */
export function CheckClaims({ check }: { check?: WorkflowCheck }) {
  if (!check) return null;
  if (checkClaims(check, 'proves').length === 0 && checkClaims(check, 'doesNotProve').length === 0) return null;
  return <div className="check-claims">
    <ClaimList check={check} claim="proves" />
    <ClaimList check={check} claim="doesNotProve" />
  </div>;
}

export function WorkflowCheckRequirements({ acceptance, checks }: { acceptance?: string; checks?: WorkflowCheck[] }) {
  if (!acceptance) return null;
  const check = checks?.find(check => check.id === acceptance);
  const title = (id: string) => checks?.find(check => check.id === id)?.title ?? id;
  return <details className="workflow-check-requirements">
    <summary>Extra check requirements{check ? ` · ${check.title}` : ''}</summary>
    <p>This optional check adds requirements beyond your editable step instructions. Change or remove the check in the editor to change these requirements.</p>
    {!check ? <p>Requirements are unavailable. Reload the catalog before using this attached check.</p> : <>
      {/* The short intro, then the contract. The long `description` is the
          prescriptive text the SDK hands the AGENT; it stays reachable for
          anyone who wants the literal wording, but it is not what a person
          deciding whether to attach this check has to read first (D-06). A
          catalog entry from before the split has only the description, and
          that is what it shows. */}
      <p style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{check.summary || check.description}</p>
      <div className="check-claims">
        <ClaimList check={check} claim="produces" />
        <ClaimList check={check} claim="recomputes" />
        <ClaimList check={check} claim="proves" />
        <ClaimList check={check} claim="doesNotProve" />
      </div>
      {check.summary && check.description && check.summary !== check.description && <details className="check-instructions">
        <summary>The exact instructions this check gives the step</summary>
        <p style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{check.description}</p>
      </details>}
      {check.inputs?.required?.length ? <p>Required inputs: {check.inputs.required.join(', ')}.</p> : null}
      {check.inputs?.optional?.length ? <p>Optional inputs: {check.inputs.optional.join(', ')}.</p> : null}
      {check.requires?.length ? <p>Earlier checks, in order: {check.requires.map(title).join(' → ')}.</p> : null}
      {check.approvalAfter && <p>Your approval is required after {title(check.approvalAfter)}.</p>}
    </>}
  </details>;
}
