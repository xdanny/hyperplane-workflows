/**
 * declPairs — the row state behind a map-typed declaration field.
 *
 * A KPI's `definitions` is a map: system → what that system means by this
 * measure. A map has no room for a half-typed entry — an entry with no system
 * name yet has no key — so an editor that derives its rows from the map itself
 * cannot hold one. "+ Add a system's definition" appended an empty pair,
 * serialized it straight back to a map that drops keyless entries, and the new
 * pair was gone in the same tick: the button did nothing, and no KPI in any
 * project could ever be given a definition per system.
 *
 * (The identical defect is why "+ Add a row" on a recipe's table slot did
 * nothing — see `./slotTable`. Same rule, different shape.)
 *
 * So the pairs being edited and the map being stored are two different things:
 *
 *   - `pairs` — what is on screen, blank halves and blank pairs included.
 *   - `value` — what is stored, keyless and blank-system entries dropped, or
 *               `undefined` for "this KPI declares no definitions", which is
 *               what the declarations document expects for an absent group.
 */

/** One entry mid-edit: the system, and what it means by the measure. */
export type Pair = [system: string, definition: string];

/** The editable state of one map field. */
export interface PairEdit {
  pairs: Pair[];
  value: Record<string, string> | undefined;
}

/** The stored map → editable pairs, with one empty pair ready to type into.
 *  There is always a pair, so the two boxes and what they are for are visible
 *  before anything is committed. An empty pair stores nothing. */
export function pairEdit(value: Record<string, string> | undefined): PairEdit {
  const pairs = Object.entries(value ?? {}).map(([k, v]): Pair => [k, v]);
  return { pairs: pairs.length > 0 ? pairs : [['', '']], value: toMap(pairs) };
}

/** Append an empty pair. The stored map does not change — an empty pair is not
 *  a definition — which is exactly why the pairs cannot be derived from it. */
export function addPair(edit: PairEdit): PairEdit {
  return withPairs([...edit.pairs, ['', '']]);
}

/** Type into one half of one pair. Editing the SYSTEM half of an entry no
 *  longer throws the definition away just because the key went momentarily
 *  blank: the text is in the pair, and the map is only what the pairs mean. */
export function setPair(edit: PairEdit, index: number, half: 0 | 1, text: string): PairEdit {
  return withPairs(
    edit.pairs.map((p, i): Pair => {
      if (i !== index) return p;
      return half === 0 ? [text, p[1]] : [p[0], text];
    }),
  );
}

/** Remove one pair. Removing the last leaves an empty pair, not an empty box. */
export function removePair(edit: PairEdit, index: number): PairEdit {
  return withPairs(edit.pairs.filter((_, i) => i !== index));
}

/** Adopt a map that changed OUTSIDE this state (a different project loaded, the
 *  form reset). This state's own echo must not re-derive the pairs, or every
 *  half-typed entry would vanish on the next keystroke. */
export function syncPairs(edit: PairEdit, value: Record<string, string> | undefined): PairEdit {
  return sameMap(edit.value, value) ? edit : pairEdit(value);
}

/** The pairs as a map: blank systems dropped, systems trimmed, `undefined` when
 *  nothing is left — the shape the declarations document stores. */
export function toMap(pairs: Pair[]): Record<string, string> | undefined {
  const out: Record<string, string> = {};
  for (const [system, definition] of pairs) {
    const key = system.trim();
    if (key !== '') out[key] = definition;
  }
  return Object.keys(out).length === 0 ? undefined : out;
}

/** Two maps with the same keys and values — how this state recognises its own
 *  echo coming back through the parent. */
export function sameMap(
  a: Record<string, string> | undefined,
  b: Record<string, string> | undefined,
): boolean {
  const ka = Object.keys(a ?? {}).sort();
  const kb = Object.keys(b ?? {}).sort();
  if (ka.length !== kb.length) return false;
  return ka.every((k, i) => k === kb[i] && (a ?? {})[k] === (b ?? {})[k]);
}

function withPairs(pairs: Pair[]): PairEdit {
  const kept: Pair[] = pairs.length > 0 ? pairs : [['', '']];
  return { pairs: kept, value: toMap(kept) };
}
