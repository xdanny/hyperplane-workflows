import type { WorkflowValidationState } from '../hooks/useWorkflowValidation';

export function WorkflowValidationStatus({validation, showIssue = true}: {validation: WorkflowValidationState; showIssue?: boolean}) {
  return <>
    {validation.pending && <p role="status">Checking this workflow… You can keep editing.</p>}
    {validation.error && <p role="alert">Could not check this workflow. {validation.error} <button type="button" className="btn" onClick={validation.retry}>Retry workflow check</button></p>}
    {showIssue && validation.issue && <p role="alert">{validation.issue.message}</p>}
  </>;
}
