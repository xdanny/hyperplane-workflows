import { useEffect, useState } from 'react';
import { api } from '../api';
import type { WorkflowDefinition, WorkflowValidationResult } from '../workflowValidation';

interface CheckedDraft {
  key: string;
  attempt: number;
  result?: WorkflowValidationResult;
  error?: string;
}

// Readiness is tied to the exact submitted definition, never the last successful
// response. Edits invalidate it during render, before the next effect can run.
export function useWorkflowValidation(definition?: WorkflowDefinition) {
  const key = definition ? JSON.stringify(definition) : undefined;
  const [attempt, setAttempt] = useState(0);
  const [checked, setChecked] = useState<CheckedDraft>();
  useEffect(() => {
    if (!key) return;
    const controller = new AbortController();
    let active = true;
    const timer = setTimeout(() => {
      void api.validateWorkflow(JSON.parse(key) as WorkflowDefinition, controller.signal).then(
        result => { if (active) setChecked({ key, attempt, result }); },
        error => { if (active) setChecked({ key, attempt, error: error instanceof Error ? error.message : 'Could not check this workflow.' }); },
      );
    }, 250);
    return () => { active = false; clearTimeout(timer); controller.abort(); };
  }, [key, attempt]);
  const current = key && checked?.key === key && checked.attempt === attempt ? checked : undefined;
  return {
    pending: Boolean(key && !current),
    valid: Boolean(current?.result?.valid),
    issue: current?.result?.valid === false ? current.result.issue : undefined,
    error: current?.error,
    retry: () => setAttempt(value => value + 1),
  };
}
export type WorkflowValidationState = ReturnType<typeof useWorkflowValidation>;
