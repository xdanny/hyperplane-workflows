/** Literal, case-insensitive search and highlighting for the SDK session log. */

import { useCallback, useRef } from 'react';

/** Below this length a query matches too much to be worth highlighting. */
export const MIN_QUERY_LENGTH = 2;

/**
 * Cap on rendered <mark> elements for one block of text. A 256 KB log with a
 * one-character query would otherwise mount six figures of DOM nodes and stall
 * the tab. Counting is unbounded and cheap, so the toolbar still reports the
 * true total and says when it is showing fewer.
 */
export const MAX_HIGHLIGHTS = 2000;

/** Total case-insensitive occurrences of `query` in `text`. */
export function countMatches(text: string, query: string): number {
  if (query.length < MIN_QUERY_LENGTH || !text) return 0;
  const haystack = text.toLowerCase();
  const needle = query.toLowerCase();
  let count = 0;
  let pos = haystack.indexOf(needle);
  while (pos !== -1) {
    count++;
    pos = haystack.indexOf(needle, pos + needle.length);
  }
  return count;
}

/**
 * Collects DOM refs for the rendered matches so a caller can scroll to the nth
 * one. Returns a stable `register` callback plus a getter — the array is reset
 * on every render pass, keyed off the render's own match ordering.
 */
export function useMatchRefs() {
  const refs = useRef<(HTMLElement | null)[]>([]);
  const register = useCallback((el: HTMLElement | null, index: number) => {
    refs.current[index] = el;
  }, []);
  const scrollTo = useCallback((index: number) => {
    const el = refs.current[index];
    if (el) el.scrollIntoView({ block: 'center', behavior: 'smooth' });
  }, []);
  return { register, scrollTo };
}

interface HighlightProps {
  text: string;
  query: string;
  /** Index of the match to render as the current one (from jump next/prev). */
  activeIndex?: number;
  onRegisterMatch?: (el: HTMLElement | null, index: number) => void;
}

/**
 * Renders `text` with every occurrence of `query` wrapped in a <mark>. Returns
 * the text untouched when the query is too short to be meaningful, so typing
 * the first character never repaints the whole block.
 */
export function Highlight({ text, query, activeIndex = -1, onRegisterMatch }: HighlightProps) {
  if (query.length < MIN_QUERY_LENGTH || !text) return <>{text}</>;

  const haystack = text.toLowerCase();
  const needle = query.toLowerCase();
  const parts: React.ReactNode[] = [];
  let cursor = 0;
  let matchIndex = 0;

  while (matchIndex < MAX_HIGHLIGHTS) {
    const at = haystack.indexOf(needle, cursor);
    if (at === -1) break;
    if (at > cursor) parts.push(text.slice(cursor, at));
    const index = matchIndex;
    parts.push(
      <mark
        key={`${at}-${index}`}
        ref={onRegisterMatch ? (el) => onRegisterMatch(el, index) : undefined}
        className={`hl${index === activeIndex ? ' hl--active' : ''}`}
      >
        {text.slice(at, at + query.length)}
      </mark>,
    );
    cursor = at + query.length;
    matchIndex++;
  }
  parts.push(text.slice(cursor));

  return <>{parts}</>;
}
