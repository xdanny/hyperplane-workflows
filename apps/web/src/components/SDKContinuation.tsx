import { useEffect, useRef, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api, ContinuationError } from '../api';
import type { ContinuationReview } from '../api';

/** The refusals that are about the RUNTIME having moved on, in either wording
 *  the server uses for them. */
const RUNTIME_REFUSAL = /sdk implementation has changed|installed runtime differs|runtime .*(changed|differs)/i;

/**
 * Why a continuation was refused, in words that name a thing the reader has.
 *
 * Every other refusal the server gives names something the reader can act on —
 * a file that changed, an effect that needs reconciling — and is rendered
 * verbatim, because the server knows things this does not. The runtime-identity
 * refusal is the exception: "The SDK implementation has changed since this run."
 * names a component nobody outside the codebase can see, and it reads as though
 * the finished work is gone. Quinn and Marcus both stopped there, with a
 * completed Outline sitting on the same page (M-16, Q-19).
 *
 * The replacement says what changed, what it means for this run, and what is
 * still here.
 */
export function refusalSentence(reason?: string): string {
  const said = reason?.trim();
  if (said && RUNTIME_REFUSAL.test(said)) {
    return 'This app’s runtime was updated after this run, so it cannot be continued. Its completed steps stay readable here; start a new run with the same setup.';
  }
  return said || 'This workflow cannot be continued. You can use its setup to start a new run.';
}

export function SDKContinuation({runId, refreshVersion, onOpenRun, onBusy}: {
  runId: string; refreshVersion: number; onOpenRun: (id:string)=>void; onBusy: (busy:boolean)=>void;
}) {
  const queryClient = useQueryClient();
  const [review, setReview] = useState<ContinuationReview>();
  const [notice, setNotice] = useState('');
  // Also guard before React commits the pending state: rapid clicks submit once.
  const submitting = useRef(false);
  const checkingUncertainResult = useRef(false);
  const inspect = useMutation({
    mutationFn: () => api.continuationReview(runId),
    onMutate: () => setReview(undefined),
    onSuccess: result => {
      setReview(result);
      if (checkingUncertainResult.current) {
        setNotice(result.continuedRunId ? 'The continuation was recorded. Open the new run below.' : 'Review the current details below before deciding whether to try again.');
        checkingUncertainResult.current = false;
      }
    },
  });
  const inspectRun = inspect.mutate;
  useEffect(() => { if (refreshVersion > 0) inspectRun(); }, [refreshVersion, inspectRun]);
  const continueRun = useMutation({
    mutationFn: (reviewToken:string) => api.continueRun(runId, reviewToken),
    onSuccess: result => {
      void queryClient.invalidateQueries({queryKey:['run',runId]});
      void queryClient.invalidateQueries({queryKey:['runs']});
      void queryClient.invalidateQueries({queryKey:['inbox']});
      onOpenRun(result.runId);
    },
    onError: error => {
      setReview(undefined);
      if (error instanceof ContinuationError && error.status === 409) {
        setNotice('This continuation review is out of date. Review it again before confirming. ' + error.message);
      } else {
        checkingUncertainResult.current = true;
        setNotice('The continuation request could not be confirmed. Checking whether a new run was created; nothing will be started again automatically.');
        inspectRun();
      }
    },
    onSettled: () => { submitting.current = false; },
  });
  const busy = inspect.isPending || continueRun.isPending;
  useEffect(() => { onBusy(busy); }, [busy,onBusy]);
  useEffect(() => () => onBusy(false), [onBusy]);
  const eligible = review?.eligible && Boolean(review.reviewToken) && !review.continuedRunId;
  const confirm = () => {
    if (!eligible || busy || submitting.current) return;
    submitting.current = true;
    setNotice('');
    continueRun.mutate(review!.reviewToken!);
  };
  return <section className="panel sdk-run-section" aria-label="Continue stopped workflow">
    <h2>Continue this workflow</h2>
    <p>Review what can be kept and where work would start again. Reviewing does not start a run.</p>
    <button type="button" className="btn" disabled={busy} onClick={() => { setNotice(''); inspectRun(); }}>{inspect.isPending ? 'Checking completed work…' : 'Review continuation'}</button>
    {notice && <p role="alert">{notice}</p>}
    {inspect.error && <p role="alert">Could not review this workflow. {inspect.error.message} Use Review continuation to retry.</p>}
    {review && <div className="sdk-continuation-review">
      {review.continuedRunId ? <><p>A continued run already exists. Open it to inspect its current progress.</p><button type="button" className="btn btn--primary" onClick={() => onOpenRun(review.continuedRunId!)}>Open continued run</button></> : <>
        {!eligible && <p role="alert">{refusalSentence(review.reason)}</p>}
        {/* A refused continuation is not a dead end: the steps that DID finish
            are still on this page, and their Open buttons still open them. The
            audited copy ("The SDK implementation has changed since this run.")
            named an internal component and offered nothing, so a stopped run
            with a finished Outline read as work destroyed (M-16, Q-19). */}
        {!eligible && (review.reused?.length ?? 0) > 0 && (
          <p>Its completed steps stay readable here — open them from “Your steps” below.</p>
        )}
        {(review.reused?.length ?? 0) > 0 && <><h3>Keep completed work</h3>
          {/* "Verified" was doing a lot of unexplained work here: it means the
              file on disk still hashes to what the checkpoint recorded, and
              nothing more. The lineage sentence was missing entirely (M-13). */}
          <p>Each of these steps was checked by SHA-256 against the hash its checkpoint recorded, so the file on disk is the file this run produced. The new run will reference this one (…{runId.slice(-8)}).</p>
          <ul>{review.reused!.map(step => <li key={step.id}><a href={`/file/${encodeURIComponent(runId)}/nodes/delivery/OUTPUTS/${encodeURIComponent(step.id)}.md`} target="_blank" rel="noreferrer">{step.title}</a> · <span className="faint">verified by SHA-256 against checkpoint</span></li>)}</ul></>}
        {eligible && <>
          <h3>{review.restart ? `Start again at ${review.restart.title}` : 'Finish with completed work'}</h3>
          <p>{review.restart ? 'A new run keeps the verified completed work. The unfinished step starts in a fresh conversation and folder; partial outputs stay with this stopped run.' : 'A new run keeps the verified completed work and finishes the result without repeating completed model work.'}</p>
          {(review.renewApprovals?.length ?? 0) > 0 && <><h3>Approvals needed again</h3><p>Earlier approvals do not authorize the new run. It will pause for a new decision at each of these steps.</p><ul>{review.renewApprovals!.map(step => <li key={step.id}>{step.title}</li>)}</ul></>}
          {review.usageNote && <p>{review.usageNote}</p>}
          <p>Workflow instructions and permissions stay as reviewed. Use this setup again if you want to edit them.</p>
          <button type="button" className="btn btn--primary" disabled={busy} onClick={confirm}>{continueRun.isPending ? 'Starting continued run…' : review.restart ? `Continue from ${review.restart.title}` : 'Finish with completed work'}</button>
        </>}
        {review.runtimeFingerprint && <details><summary>Technical details</summary><p>Runtime identity</p><p className="mono">{review.runtimeFingerprint}</p><p>This identifies runtime source and dependency manifests, not installed dependencies or host tools.</p></details>}
      </>}
    </div>}
  </section>;
}
