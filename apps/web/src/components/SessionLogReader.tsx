import { useCallback, useState, useEffect, useRef } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api';
import type { NodeLog, LogFileKind } from '../api';
import { Highlight, countMatches, useMatchRefs, MIN_QUERY_LENGTH, MAX_HIGHLIGHTS } from './Highlight';

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

/** Lines fetched on open. Enough to hold a failure's stack; small enough that a
    multi-megabyte log paints instantly. "Load full log" lifts it. */
const LOG_TAIL_LINES = 500;

interface LogTabProps {
  runId: string;
  nodeId: string;
  isLive: boolean;
  /** Node error text, shown above the log so opening straight to this tab on a
      failed node never hides the reason it failed. */
  error?: string | null;
  /** Start scrolled to the end — how a live or failed log is actually read. */
  startAtEnd?: boolean;
  /** Which sidecar to read. `verdict` opens the full verdict the failure digest
      preserved when its 32 KB cap bit — same viewer, different file. */
  file?: LogFileKind;
}

function LogTab({ runId, nodeId, isLive, error, startAtEnd = false, file = 'log' }: LogTabProps) {
  const preRef = useRef<HTMLPreElement>(null);
  const [copied, setCopied] = useState(false);
  const [query, setQuery] = useState('');
  const [activeMatch, setActiveMatch] = useState(0);
  const [showFull, setShowFull] = useState(false);
  // Follow only makes sense while the node is still writing. It starts on for a
  // live node so the panel behaves like `tail -f` without being asked.
  const [follow, setFollow] = useState(isLive);
  const { register, scrollTo } = useMatchRefs();

  // What this panel is showing, for the handful of strings that name it. A
  // preserved verdict is not a log, and calling it one would send the reader
  // looking for a log that does not exist.
  const noun = file === 'verdict' ? 'verdict' : 'log';

  const { data, isLoading, isError } = useQuery<NodeLog>({
    queryKey: ['run', runId, 'log', nodeId, file, showFull ? 'full' : 'tail'],
    queryFn: () =>
      api.runNodeLog(runId, nodeId, showFull ? { file } : { tail: LOG_TAIL_LINES, file }),
    staleTime: isLive ? 0 : 30_000,
    refetchInterval: isLive && follow ? 3000 : false,
  });

  const logText = data?.text ?? '';
  const matchCount = countMatches(logText, query);
  const shownMatches = Math.min(matchCount, MAX_HIGHLIGHTS);

  // Reset the search cursor whenever the query or the text underneath it moves,
  // so "next" never points past the end of a shrunken match list.
  useEffect(() => { setActiveMatch(0); }, [query, nodeId]);

  // Jump to the active match. Runs after the paint that assigned the refs.
  useEffect(() => {
    if (matchCount > 0 && query.length >= MIN_QUERY_LENGTH) scrollTo(activeMatch);
  }, [activeMatch, matchCount, query, scrollTo]);

  // Follow the tail. Suppressed while searching — yanking the viewport back to
  // the bottom every 3s would make a live log impossible to read a match in.
  useEffect(() => {
    const el = preRef.current;
    if (!el || !logText) return;
    const searching = query.length >= MIN_QUERY_LENGTH && matchCount > 0;
    if (searching) return;
    if (follow || startAtEnd) el.scrollTop = el.scrollHeight;
  }, [logText, follow, startAtEnd, query, matchCount]);

  const step = (delta: number) => {
    if (shownMatches === 0) return;
    setActiveMatch((i) => (i + delta + shownMatches) % shownMatches);
  };

  const handleCopy = () => {
    if (!logText) return;
    void navigator.clipboard.writeText(logText).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };

  if (isLoading) {
    return (
      <div className="empty" style={{ padding: 'var(--s5)' }}>
        <span className="faint mono">loading {noun}…</span>
      </div>
    );
  }
  if (isError) {
    return (
      <div className="empty" style={{ padding: 'var(--s5)', color: 'var(--st-fail)' }}>
        <span>{noun} unavailable</span>
      </div>
    );
  }
  if (!logText || logText.trim() === '') {
    return (
      <div className="inspector-log">
        {error && <pre className="inspector-error-pre">{error}</pre>}
        <div className="empty" style={{ padding: 'var(--s5)' }}>
          <span>no {noun === 'verdict' ? 'verdict text' : 'log output'}</span>
        </div>
      </div>
    );
  }

  return (
    <div className="inspector-log">
      {error && (
        <div className="inspector-error">
          <span className="eyebrow" style={{ color: 'var(--st-fail)', marginBottom: 'var(--s2)', display: 'block' }}>
            Error
          </span>
          <pre className="inspector-error-pre">{error}</pre>
        </div>
      )}

      {/* Search row — the point of the panel */}
      <div className="log-search">
        <input
          type="search"
          className="log-search-input mono"
          placeholder={`Search this ${noun}…`}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              step(e.shiftKey ? -1 : 1);
            }
            if (e.key === 'Escape') setQuery('');
          }}
          aria-label={`Search this ${noun}`}
        />
        <span className="log-search-count mono" aria-live="polite">
          {query.length < MIN_QUERY_LENGTH
            ? ''
            : matchCount === 0
              ? 'no matches'
              : `${activeMatch + 1}/${matchCount}${matchCount > MAX_HIGHLIGHTS ? ` (first ${MAX_HIGHLIGHTS} marked)` : ''}`}
        </span>
        <button
          className="btn btn--ghost btn--sm"
          onClick={() => step(-1)}
          disabled={shownMatches === 0}
          aria-label="Previous match"
          title="Previous match (Shift+Enter)"
        >
          ↑
        </button>
        <button
          className="btn btn--ghost btn--sm"
          onClick={() => step(1)}
          disabled={shownMatches === 0}
          aria-label="Next match"
          title="Next match (Enter)"
        >
          ↓
        </button>
      </div>

      <div className="inspector-log-toolbar">
        <span className="mono faint" style={{ fontSize: 'var(--t-xs)' }}>
          {formatBytes(data?.size ?? 0)}
          {data?.truncated && !showFull && ` · showing the last ${LOG_TAIL_LINES} lines`}
          {data?.truncated && showFull && ' · showing the last 256 KB'}
        </span>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--s2)' }}>
          {isLive && (
            <label className="log-follow" title="Keep scrolling to the newest output">
              <input
                type="checkbox"
                checked={follow}
                onChange={(e) => setFollow(e.target.checked)}
              />
              <span>follow</span>
            </label>
          )}
          {/* The panel ended mid-step and never showed the final checkpoint,
              with no way to reach the earlier output (M-10). "Load earlier"
              says which direction the missing text is in; "load full" read as
              a completeness claim the 256 KB cap cannot make. */}
          {data?.truncated && !showFull && (
            <button
              className="btn btn--ghost btn--sm"
              onClick={() => setShowFull(true)}
              aria-label={`Load earlier lines of this ${noun}`}
            >
              Load earlier
            </button>
          )}
          <button
            className="btn btn--ghost btn--sm"
            onClick={handleCopy}
            aria-label={`Copy ${noun} to clipboard`}
          >
            {copied ? 'copied ✓' : 'copy'}
          </button>
        </div>
      </div>

      <pre ref={preRef} className="inspector-log-pre">
        <Highlight
          text={logText}
          query={query}
          activeIndex={activeMatch}
          onRegisterMatch={register}
        />
      </pre>
    </div>
  );
}

// ── Log overlay — the same viewer, opened from anywhere ───────────────────────

/** What the overlay needs to read one log. `runId` is explicit because the most
 *  reader can be opened from any run result. */
export interface NodeLogRef {
  runId: string;
  nodeId: string;
  /** Heading text — defaults to the node id. A self-heal log reads better as
   *  "report-verify · self-heal" than as "report-verify.heal". */
  title?: string;
  error?: string | null;
  /** Which of the node's files to read: its log (default), or the full verdict
   *  the failure digest preserved when its 32 KB cap bit. */
  file?: LogFileKind;
}

/**
 * Searchable session log overlay. Escape or a backdrop click closes it.
 */
export function NodeLogReader({ log, onClose }: { log: NodeLogRef; onClose: () => void }) {
  const closeRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  useEffect(() => { closeRef.current?.focus(); }, [log.runId, log.nodeId, log.file]);

  const kind = log.file === 'verdict' ? 'Verdict' : 'Log';

  return (
    <div
      className="ar-overlay"
      role="dialog"
      aria-modal="true"
      aria-label={`${kind}: ${log.title ?? log.nodeId}`}
      onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}
    >
      <div className="ar-overlay-inner">
        <div className="ar-overlay-head">
          <span className="ar-overlay-title mono">{log.title ?? log.nodeId}</span>
          <button
            ref={closeRef}
            className="btn btn--ghost btn--sm"
            onClick={onClose}
            aria-label={`Close the ${kind.toLowerCase()} reader`}
          >
            ✕
          </button>
        </div>
        <div className="fd-log-body">
          <LogTab
            runId={log.runId}
            nodeId={log.nodeId}
            isLive={false}
            error={log.error}
            file={log.file}
            // A log is read from its end; a verdict is an argument and is read
            // from its beginning.
            startAtEnd={log.file !== 'verdict'}
          />
        </div>
      </div>
    </div>
  );
}

/** Wiring helper: call `open(ref)` from a handler and render `reader` in the
 *  same tree. Reader state stays local to the result view. */
export function useNodeLogReader() {
  const [log, setLog] = useState<NodeLogRef | null>(null);
  const open = useCallback((ref: NodeLogRef) => setLog(ref), []);
  const close = useCallback(() => setLog(null), []);
  const reader = log ? <NodeLogReader log={log} onClose={close} /> : null;
  return { open, close, reader };
}
