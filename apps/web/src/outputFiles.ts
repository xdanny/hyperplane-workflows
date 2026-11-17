/**
 * Comparing what two runs PRODUCED, by checksum (M-18).
 *
 * The compare view diffed the final document and nothing else, so a run that
 * rewrote `build/totals.csv` looked identical to one that did not: the file was
 * recorded, its SHA-256 was on the checkpoint, and no screen put the two
 * hashes beside each other. Marcus wanted one row per path saying "same" or
 * "changed"; that is exactly what this returns.
 *
 * What a comparison here claims, and what it does not:
 *   · "same" means the two recorded SHA-256 values are equal. Byte-identical.
 *   · "changed" means they differ. It says NOTHING about what changed inside —
 *     these are arbitrary files, and the word-level difference is only ever
 *     offered for the text document the run delivered.
 *   · "only in run #N" means the other run recorded no file at that path. It is
 *     not evidence the file was deleted; a run that never wrote it and a run
 *     that wrote it elsewhere both land here.
 *   · A file recorded WITHOUT a hash cannot be compared, and says so rather
 *     than defaulting to either answer.
 */

export interface OutputFile {
  stepId?: string;
  stepTitle?: string;
  path: string;
  bytes?: number;
  sha256?: string;
}

export type FileComparison = 'same' | 'changed' | 'only_older' | 'only_newer' | 'unknown';

export interface ComparedFile {
  path: string;
  status: FileComparison;
  /** The step that produced it, preferring the newer run's own title. */
  stepTitle?: string;
  older?: OutputFile;
  newer?: OutputFile;
}

/** What each verdict says, in the words the row renders. `%older%`/`%newer%`
 *  are the two run numbers, which only the caller knows. */
export const COMPARISON_LABEL: Record<FileComparison, string> = {
  same: 'same',
  changed: 'changed',
  only_older: 'only in run #%older%',
  only_newer: 'only in run #%newer%',
  unknown: 'cannot be compared — no checksum recorded',
};

export function comparisonLabel(status: FileComparison, olderRun?: number, newerRun?: number): string {
  return COMPARISON_LABEL[status]
    .replace('%older%', String(olderRun ?? '?'))
    .replace('%newer%', String(newerRun ?? '?'));
}

const hashOf = (file?: OutputFile): string =>
  typeof file?.sha256 === 'string' ? file.sha256.toLowerCase() : '';

/**
 * Every path either run recorded, sorted by path so two comparisons of the same
 * pair read identically. Duplicate paths within one run keep the first entry:
 * a checkpoint that records one path twice is a bug in the recorder, not a
 * reason for this to render two contradictory rows.
 */
export function compareOutputFiles(
  older: OutputFile[] | undefined,
  newer: OutputFile[] | undefined,
): ComparedFile[] {
  const index = (files: OutputFile[] | undefined) => {
    const map = new Map<string, OutputFile>();
    for (const file of files ?? []) {
      if (!file || typeof file.path !== 'string' || file.path.trim() === '') continue;
      if (!map.has(file.path)) map.set(file.path, file);
    }
    return map;
  };
  const left = index(older);
  const right = index(newer);
  const paths = [...new Set([...left.keys(), ...right.keys()])].sort((a, b) => a.localeCompare(b));

  return paths.map((path) => {
    const a = left.get(path);
    const b = right.get(path);
    const status: FileComparison =
      a && !b ? 'only_older'
      : b && !a ? 'only_newer'
      : !hashOf(a) || !hashOf(b) ? 'unknown'
      : hashOf(a) === hashOf(b) ? 'same'
      : 'changed';
    return {
      path,
      status,
      stepTitle: b?.stepTitle || a?.stepTitle,
      ...(a ? { older: a } : {}),
      ...(b ? { newer: b } : {}),
    };
  });
}

/** "1 file changed, 2 the same, 1 only in run #3" — the line above the table,
 *  so the answer is readable without counting rows. Kinds with no rows are
 *  left out; an empty comparison returns ''. */
export function outputFilesSummary(
  files: ComparedFile[],
  olderRun?: number,
  newerRun?: number,
): string {
  if (files.length === 0) return '';
  const count = (status: FileComparison) => files.filter((file) => file.status === status).length;
  const parts: string[] = [];
  const changed = count('changed');
  const same = count('same');
  const onlyOlder = count('only_older');
  const onlyNewer = count('only_newer');
  const unknown = count('unknown');
  if (changed > 0) parts.push(`${changed} file${changed === 1 ? '' : 's'} changed`);
  if (same > 0) parts.push(`${same} the same`);
  if (onlyOlder > 0) parts.push(`${onlyOlder} only in run #${olderRun ?? '?'}`);
  if (onlyNewer > 0) parts.push(`${onlyNewer} only in run #${newerRun ?? '?'}`);
  if (unknown > 0) parts.push(`${unknown} without a recorded checksum`);
  return `${parts.join(', ')}.`;
}

/** One document a run's final steps recorded, as the history API returns it. */
export interface RecordedDocument {
  stepId?: string;
  title?: string;
  /** The declared step kind. Absent on rows from a build that did not record it. */
  kind?: string;
  path: string;
  bytes?: number;
  sha256?: string;
}

/**
 * The DELIVERABLE among a run's recorded documents (S-16).
 *
 * "The last one" is not it. A workflow that ends with an independent review
 * records the review as a document too, so the compare view's word difference
 * was computed over "# Check the note" and reported the note unchanged when it
 * had been rewritten. A review is evidence ABOUT the result; an approval is the
 * receipt for a decision about one. The deliverable is the last document of a
 * step that is neither.
 *
 * A run whose documents carry no kind — an older row — falls back to the last
 * document, which is the answer that build always gave.
 */
export function deliveredDocument<T extends RecordedDocument>(documents: T[] | undefined): T | null {
  const all = documents ?? [];
  if (all.length === 0) return null;
  const delivered = all.filter((doc) => doc.kind !== 'check' && doc.kind !== 'approval');
  const pool = delivered.length > 0 ? delivered : all;
  return pool[pool.length - 1];
}
