/**
 * Word-level text difference between two documents (F-C "Compare").
 *
 * Deliberately named a TEXT DIFFERENCE everywhere it is rendered. It compares
 * characters, not meaning: two briefs that say the same thing in different
 * words come out as a wall of changes, and two that contradict each other over
 * one digit come out as one. Sofia compared last month's and this month's brief
 * by hand and found real differences (328 vs 350 words, items reordered, one
 * caveat present in one and absent in the other) — this makes that mechanical,
 * and claims nothing more than that (S-03, D-13, C3).
 *
 * Runs client-side over documents already fetched for the page: no endpoint, no
 * dependency, and nothing about a comparison is stored.
 */

export type DiffOp = 'same' | 'add' | 'remove';

export interface DiffSpan {
  op: DiffOp;
  text: string;
}

/** Tokenise into words and the whitespace between them, so rebuilt text keeps
 *  its line breaks and a one-word change highlights one word. */
function tokenize(text: string): string[] {
  return text.replace(/\r\n?/g, '\n').match(/\s+|[^\s]+/g) ?? [];
}

/**
 * Above this many tokens per side, the O(n·m) table stops being something to
 * build in a render pass. Long documents fall back to a line-level comparison,
 * which is coarser but still true — and the UI says which one it used.
 */
export const WORD_DIFF_LIMIT = 2500;

function lcsDiff(a: string[], b: string[]): DiffSpan[] {
  const n = a.length;
  const m = b.length;
  // lengths[i][j] = LCS length of a[i:] and b[j:]
  const lengths: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lengths[i][j] = a[i] === b[j]
        ? lengths[i + 1][j + 1] + 1
        : Math.max(lengths[i + 1][j], lengths[i][j + 1]);
    }
  }
  const out: DiffSpan[] = [];
  const push = (op: DiffOp, text: string) => {
    const last = out[out.length - 1];
    if (last && last.op === op) last.text += text;
    else out.push({ op, text });
  };
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) { push('same', a[i]); i++; j++; }
    else if (lengths[i + 1][j] >= lengths[i][j + 1]) { push('remove', a[i]); i++; }
    else { push('add', b[j]); j++; }
  }
  while (i < n) { push('remove', a[i]); i++; }
  while (j < m) { push('add', b[j]); j++; }
  return out;
}

export interface TextDifference {
  spans: DiffSpan[];
  /** 'word' or 'line' — which granularity actually ran. */
  granularity: 'word' | 'line';
  /** Tokens only in the older text, and only in the newer one. */
  removed: number;
  added: number;
  /** True when the two texts are byte-identical. */
  identical: boolean;
}

/** Compare `before` (the older run) with `after` (the newer one). */
export function textDifference(before: string, after: string): TextDifference {
  if (before === after) {
    return { spans: before ? [{ op: 'same', text: before }] : [], granularity: 'word', removed: 0, added: 0, identical: true };
  }
  let a = tokenize(before);
  let b = tokenize(after);
  let granularity: 'word' | 'line' = 'word';
  if (a.length > WORD_DIFF_LIMIT || b.length > WORD_DIFF_LIMIT) {
    granularity = 'line';
    a = before.replace(/\r\n?/g, '\n').split(/(?<=\n)/);
    b = after.replace(/\r\n?/g, '\n').split(/(?<=\n)/);
  }
  const spans = lcsDiff(a, b);
  const count = (op: DiffOp) =>
    spans.filter((s) => s.op === op).reduce((total, s) => total + (s.text.trim() === '' ? 0 : s.text.trim().split(/\s+/).length), 0);
  return { spans, granularity, removed: count('remove'), added: count('add'), identical: false };
}

/** "12 words removed, 30 added" — the headline above the coloured text. */
export function differenceSummary(diff: TextDifference): string {
  if (diff.identical) return 'The two documents are identical.';
  const unit = diff.granularity === 'word' ? 'word' : 'word';
  const parts: string[] = [];
  if (diff.removed > 0) parts.push(`${diff.removed} ${unit}${diff.removed === 1 ? '' : 's'} removed`);
  if (diff.added > 0) parts.push(`${diff.added} ${unit}${diff.added === 1 ? '' : 's'} added`);
  if (parts.length === 0) return 'Only whitespace differs.';
  return `${parts.join(', ')}. This is a text difference, not a comparison of meaning.`;
}
