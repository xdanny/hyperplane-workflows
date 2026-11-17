import type { SDKWorkflowNode } from './api';

export interface WorkflowDefinition { instruction: string; nodes: SDKWorkflowNode[] }
export interface WorkflowIssue { nodeId?: string; message: string }
export type WorkflowValidationResult = { runtimeFingerprint: string; runtimeFingerprintVersion: number } &
  ({ valid: true } | { valid: false; issue: WorkflowIssue });

// Local feedback concerns unfinished text only. The backend owns workflow rules.
const encoder = new TextEncoder();
export function textByteIssue(value: string, label: string, maxBytes: number): string | undefined {
  if (encoder.encode(value).length > maxBytes) return `Shorten ${label} to fit the ${maxBytes}-byte limit.`;
}
export function nodeTextIssue(node: SDKWorkflowNode): string | undefined {
  if (!node.title.trim()) return 'Give this step a name.';
  if (!node.instruction.trim()) return 'Describe what this step should do.';
  return textByteIssue(node.title, 'the step name', 160) ?? textByteIssue(node.instruction, 'the step instructions', 16000);
}

export function readWorkflowValidation(value: unknown): WorkflowValidationResult {
  const result = value as WorkflowValidationResult | null;
  if (!result || !/^[a-f0-9]{64}$/.test(result.runtimeFingerprint) ||
    !Number.isSafeInteger(result.runtimeFingerprintVersion) || result.runtimeFingerprintVersion < 1 ||
    (result.valid !== true && (result.valid !== false || !result.issue || typeof result.issue.message !== 'string' || !result.issue.message.trim() ||
      (result.issue.nodeId !== undefined && typeof result.issue.nodeId !== 'string')))) {
    throw new Error('The workflow check returned an invalid response. Retry before saving or running.');
  }
  return result;
}
