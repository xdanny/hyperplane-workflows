/**
 * optionLabels — what a closed set of choices SAYS, as against what it stores.
 *
 * Every select on the project-declaration form and every closed column on a
 * recipe table offered its stored token as its own label: `percent_of_a`,
 * `points_of_share`, `dev`, `holdout`, `category-emergence`. The token is the
 * right thing to store — gates match on it, and a run's inputs must survive a
 * copy edit — and the wrong thing to show. So the value stays the token and the
 * label is written here, once, for both forms.
 *
 * Labels are keyed by FIELD as well as token, because one token means two
 * things in two columns: on a shift-audit row `psi` under "Statistic" is the
 * measurement and `psi` under "Unit" is what the tolerance is counted in. A
 * single flat map would have had to pick one of them and be wrong on the other.
 *
 * A token with no label falls through unchanged — `blocker`, `numeric`, `A` and
 * `both` are already the words a person would use, and inventing captions for
 * them would only add reading.
 */

/** The two sides of a reconciliation, as the person filling it in named them. */
export interface SideNames {
  a?: string;
  b?: string;
}

/**
 * The tolerance unit. The denominator is half the tolerance — 0.5% of A's total
 * and 0.5% of B's are different numbers — so the label has to carry which side
 * it is a percentage OF, and carries the side's own name when the form knows it.
 */
function unitLabel(token: string, sides?: SideNames): string | undefined {
  switch (token) {
    case 'percent_of_a':
      return sides?.a ? `% of ${sides.a} total` : "% of A's total";
    case 'percent_of_b':
      return sides?.b ? `% of ${sides.b} total` : "% of B's total";
    case 'percent_of_control':
      return '% of the source control total';
    case 'percent_of_baseline':
      return '% of the baseline';
    case 'absolute':
      return 'an absolute amount';
    case 'count':
      return 'a count of rows';
    case 'exact':
      return 'exact match';
    case 'psi':
      return 'PSI points';
    case 'points_of_share':
      return 'points of share';
    default:
      return undefined;
  }
}

/** What is measured for a subject on a shift audit. */
const STATISTIC_LABELS: Record<string, string> = {
  psi: 'PSI — how far the distribution moved',
  kl: 'KL — how far the distribution moved, measured a second way',
  'rate-delta': 'change in the rate',
  'category-emergence': 'a value appeared or vanished',
  'volume-drift': 'change in how many rows arrive',
};

/** An evaluation set's kind. The rule is the label: it is the whole point of
 *  declaring one, and it was written nowhere on the form. */
const KIND_LABELS: Record<string, string> = {
  number: 'a number',
  boolean: 'true or false',
  text: 'text',
  dev: 'dev — may be used while building',
  holdout: 'holdout — nothing may train on it',
};

/**
 * The label for one option of one field. `field` is the FieldSpec key on the
 * declarations form and the column id on a recipe table — the same names in
 * both, which is why one function serves both.
 */
export function optionLabel(field: string, token: string, sides?: SideNames): string {
  switch (field) {
    case 'unit':
      return unitLabel(token, sides) ?? token;
    case 'statistic':
      return STATISTIC_LABELS[token] ?? token;
    case 'kind':
      return KIND_LABELS[token] ?? token;
    default:
      return token;
  }
}

/**
 * A dashboard's NAME out of the sentence that describes its source.
 *
 * "Dashboard A" is a slot whose answer is a whole paragraph — where the table
 * lives, its size, how it is read — and a unit option cannot carry a paragraph.
 * The name is the head of it: everything before the first em-dash, comma or
 * parenthetical qualifier, which is exactly the shape the project's dashboard
 * declaration prefills ("Paid Social Q2 (Looker) — owned by Priya, marketing
 * ops"). Anything longer than a label is not one, so it is left out rather than
 * truncated into something that reads like a different dashboard.
 */
export function sideName(raw: string | undefined): string | undefined {
  const head = (raw ?? '').split(/\s+—\s+|\s+-\s+|,|\n/)[0]?.trim();
  if (!head || head.length > 44) return undefined;
  return head;
}
