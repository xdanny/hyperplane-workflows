/**
 * The text a run actually delivered, for a card that has only a status word
 * to show (D-03).
 *
 * The draft-skill card quoted `phase: passed` as the evidence for a method —
 * a checkpoint field name and its value, presented as grounds. The extractor
 * now reads the delivered documents, but a quote it pins must be a substring
 * of the step's committed checkpoint, so a quote FROM a document is dropped
 * before it is stored and older drafts keep the status word they pinned.
 *
 * Rather than leave the card with nothing a reader can weigh, this reads the
 * run's own deliverable — the same document the run page leads with, chosen by
 * the same rule — and hands back an excerpt labelled with the step that wrote
 * it. It is explicitly NOT the extractor's evidence and the card says so: it is
 * the delivery, shown so the reader can judge the method against something the
 * run produced.
 */

import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import type { SDKWorkflowNode } from '../api';
import { finalDocuments, firstLines } from '../runResult';
import { nodeFileURL } from '../workflowNodePresentation';
import type { WorkflowNodeCheckpoint } from '../workflowNodePresentation';

export interface DeliveredExcerpt {
  /** The step that wrote it — a quote without its source is not a source. */
  stepTitle: string;
  path: string;
  text: string;
}

export function useDeliveredExcerpt(runId: string | undefined, enabled: boolean): {
  excerpt: DeliveredExcerpt | null;
  loading: boolean;
} {
  const id = runId ?? '';
  const run = useQuery({
    queryKey: ['run', id], queryFn: () => api.run(id),
    enabled: enabled && id !== '', staleTime: 60_000,
  });

  const request = run.data?.options?.request as { nodes?: SDKWorkflowNode[] } | undefined;
  const checkpoints = (run.data?.doc?.sdk as { nodes?: WorkflowNodeCheckpoint[] } | undefined)?.nodes;
  const document = finalDocuments(request?.nodes, checkpoints)[0] ?? null;

  const text = useQuery({
    queryKey: ['document', document ? nodeFileURL(id, document.file) : ''],
    queryFn: async () => {
      const response = await fetch(nodeFileURL(id, document!.file));
      if (!response.ok) throw new Error(String(response.status));
      return response.text();
    },
    enabled: enabled && id !== '' && document !== null,
    staleTime: 60_000,
  });

  const body = typeof text.data === 'string' ? firstLines(text.data, 4).trim() : '';
  return {
    excerpt: document && body !== ''
      ? { stepTitle: document.stepTitle, path: document.file.path, text: body }
      : null,
    loading: enabled && (run.isPending || (document !== null && text.isPending)),
  };
}
