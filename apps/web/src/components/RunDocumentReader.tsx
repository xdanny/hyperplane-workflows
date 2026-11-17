/**
 * In-app reader for a document a run produced (F-G, P-05, S-04, Q-09).
 *
 * Before this, "Read document" opened /file/<run>/…/draft.md in a new tab: a
 * bare page with no title, no way back, and pipe tables and `[Source](url)`
 * rendered as literal text. That is the artifact a person forwards to their
 * team, and it was the one thing the app refused to render.
 *
 * It renders through DocumentViewer's `renderMarkdown` — the same transformer
 * the rest of the app uses, so links are clickable and tables are tables — and
 * keeps "Open raw file" for anyone who wants the bytes.
 */

import { useEffect, useRef } from 'react';
import { useQuery } from '@tanstack/react-query';
import { renderMarkdown } from './DocumentViewer';

export interface RunDocumentRef {
  /** Absolute in-app URL of the file, as recorded by the run. */
  url: string;
  /** What to call it — the producing step's title. */
  title: string;
  /** The step that produced it, shown as the caption. */
  stepTitle?: string;
  /** Recorded checksum, so the reader can say what it is reading. */
  sha256?: string;
}

/** Fetches the document text. A file that cannot be read says so rather than
 *  rendering an empty page that looks like an empty document. */
function useDocumentText(url: string) {
  return useQuery({
    queryKey: ['document', url],
    queryFn: async () => {
      const response = await fetch(url);
      if (!response.ok) throw new Error(`${response.status} ${response.statusText}`);
      return response.text();
    },
    staleTime: 60_000,
  });
}

export function RunDocumentReader({ doc, onClose }: { doc: RunDocumentRef; onClose: () => void }) {
  const backRef = useRef<HTMLButtonElement>(null);
  const { data, isPending, error } = useDocumentText(doc.url);

  useEffect(() => {
    function onKey(event: KeyboardEvent) {
      if (event.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  useEffect(() => { backRef.current?.focus(); }, [doc.url]);

  const isMarkdown = /\.(md|markdown|txt)$/i.test(doc.url) || !/\.[a-z0-9]+$/i.test(doc.url);

  return (
    <div
      className="ar-overlay"
      role="dialog"
      aria-modal="true"
      aria-label={`Document: ${doc.title}`}
      onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}
    >
      <div className="ar-overlay-inner">
        <div className="ar-overlay-head">
          <button ref={backRef} className="btn btn--ghost btn--sm" onClick={onClose} aria-label="Back to run">
            ← Back
          </button>
          <span className="ar-overlay-title">{doc.title}</span>
          <a className="btn btn--ghost btn--sm" href={doc.url} target="_blank" rel="noreferrer">
            Open raw file
          </a>
        </div>
        {doc.stepTitle && (
          <p className="run-doc-caption faint">
            Produced by “{doc.stepTitle}”.
            {doc.sha256 ? <> Recorded checksum <span className="mono">{doc.sha256.slice(0, 12)}…</span>.</> : null}
          </p>
        )}
        <div className="run-doc-body">
          {isPending && <p role="status">Loading the document…</p>}
          {error && (
            <p role="alert">
              This document could not be read ({error instanceof Error ? error.message : 'unknown error'}). Open the raw
              file to check whether it is still on the server.
            </p>
          )}
          {data !== undefined && (
            isMarkdown
              ? <div className="dv-body" dangerouslySetInnerHTML={{ __html: renderMarkdown(data) }} />
              : <pre className="run-doc-pre">{data}</pre>
          )}
        </div>
      </div>
    </div>
  );
}
