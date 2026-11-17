import { useState } from 'react';
import type {
  CalendarDecl,
  DashboardDecl,
  DeliveryDecl,
  EvalsetDecl,
  ExclusionDecl,
  HoldoutCustodyDecl,
  KPIDecl,
  LabelAvailabilityDecl,
  MetricDecl,
  ProjectDeclarations,
  RegressionPolicyDecl,
  ServingConstraintsDecl,
  SliceDecl,
  StakeholderDecl,
  ToleranceDecl,
} from '../api';
import { TOLERANCE_UNITS } from '../api';
import { addPair, pairEdit, removePair, setPair, syncPairs, type PairEdit } from '../declPairs';
import { optionLabel } from '../optionLabels';

/**
 * DeclarationGroups — the groups a project declares beyond its data.
 *
 * Two reviewers asked for these by name: an analyst who re-typed the same KPI
 * definitions, dashboard sources, calendar and tolerances into a form once per
 * run, and an ML engineer whose project could declare nothing an evalset, a
 * holdout or a tolerance could bind to.
 *
 * Three rules hold this file together:
 *
 *  1. EVERY GROUP IS OPTIONAL, and every group is COLLAPSED. A person who came
 *     to name one dashboard must not meet fourteen sections of ML vocabulary on
 *     the way. Nothing here is ever required to save a project.
 *
 *  2. ONE ROW PER THING, with its unit as its own column. A standing tolerance
 *     is a number, a denominator, an owner and a date — not a sentence.
 *
 *  3. REFERENCES ONLY. `contactRef` is a name or a handle, `location` is a path
 *     or a table, `link` is a URL. A credential belongs nowhere in this
 *     document, which is why no field here asks for one.
 */

// ── Field specs ─────────────────────────────────────────────────────────────

type Row = Record<string, unknown>;

/**
 * One cell of a declaration.
 *
 * `hint` is held to the SAME authoring contract as a recipe slot's hint
 * (store.ContextSlot.Hint, and the group hint below):
 *
 *	Name why this matters to the person filling it in, define every term in
 *	the sentence that uses it, and give the default when one exists. The
 *	engine's own vocabulary — "workspace-write node", "regression alarm",
 *	"the builder's script", "travels into every brief verbatim" — belongs in
 *	the contract it comes from, and is NEVER copied here.
 *
 * `options` are stored as TOKENS and rendered through `optionLabel`: the value
 * a gate matches on and the words a person reads are two different things, and
 * the form showed the first where it owed the second.
 */
interface FieldSpec {
  key: string;
  label: string;
  hint?: string;
  placeholder?: string;
  kind?: 'text' | 'number' | 'date' | 'select' | 'definitions';
  options?: readonly string[];
}

/** One editable cell, rendered from its spec. */
function Field({
  spec,
  value,
  onChange,
  idPrefix,
}: {
  spec: FieldSpec;
  value: unknown;
  onChange: (v: unknown) => void;
  idPrefix: string;
}) {
  const id = `${idPrefix}-${spec.key}`;
  if (spec.kind === 'definitions') {
    return (
      <DefinitionsField
        label={spec.label}
        hint={spec.hint}
        value={(value ?? {}) as Record<string, string>}
        onChange={onChange}
      />
    );
  }
  const common = {
    id,
    className: 'input',
    value: value === undefined || value === null ? '' : String(value),
    placeholder: spec.placeholder,
  };
  return (
    <label className="proj-field" htmlFor={id}>
      <span className="eyebrow">{spec.label}</span>
      {spec.hint && <span className="proj-field-hint">{spec.hint}</span>}
      {spec.kind === 'select' ? (
        <select
          {...common}
          onChange={(e) => onChange(e.target.value === '' ? undefined : e.target.value)}
        >
          <option value="">—</option>
          {(spec.options ?? []).map((o) => (
            <option key={o} value={o}>{optionLabel(spec.key, o)}</option>
          ))}
        </select>
      ) : spec.kind === 'number' ? (
        <input
          {...common}
          inputMode="decimal"
          onChange={(e) => {
            const raw = e.target.value.trim();
            if (raw === '') return onChange(undefined);
            const n = Number(raw);
            onChange(Number.isFinite(n) ? n : undefined);
          }}
        />
      ) : (
        <input
          {...common}
          type={spec.kind === 'date' ? 'date' : 'text'}
          onChange={(e) => onChange(e.target.value === '' ? undefined : e.target.value)}
        />
      )}
    </label>
  );
}

/**
 * A KPI's definitions: one row per system that computes it.
 *
 * This is the field the whole analyst group turns on. "Net revenue" is one word
 * over two definitions, and a reconciliation carrying only one of them reports
 * a vocabulary difference as a gap in the data.
 *
 * The pairs on screen are STATE, not a projection of the stored map. A map has
 * no key for an entry whose system has not been typed yet, so pairs derived
 * from the map could never hold a new one: "+ Add a system's definition"
 * created a pair and dropped it in the same tick, and no KPI could be given a
 * definition per system at all. `../declPairs` owns both halves — and the same
 * rule fixed the recipe form's table slot, which had the identical defect.
 */
function DefinitionsField({
  label,
  hint,
  value,
  onChange,
}: {
  label: string;
  hint?: string;
  value: Record<string, string>;
  onChange: (v: Record<string, string> | undefined) => void;
}) {
  const [edit, setEdit] = useState<PairEdit>(() => pairEdit(value));
  // A map that is not this state's own echo came from outside and replaces the
  // pairs; the echo must not, or a half-typed entry would vanish per keystroke.
  const state = syncPairs(edit, value);
  if (state !== edit) setEdit(state);
  const commit = (next: PairEdit) => {
    setEdit(next);
    onChange(next.value);
  };
  return (
    <div className="proj-field">
      <span className="eyebrow">{label}</span>
      {hint && <span className="proj-field-hint">{hint}</span>}
      {state.pairs.map(([system, definition], i) => (
        <div className="proj-decl-pair" key={i}>
          <input
            className="input"
            aria-label={`System ${i + 1}`}
            value={system}
            placeholder="Finance"
            onChange={(e) => commit(setPair(state, i, 0, e.target.value))}
          />
          <input
            className="input"
            aria-label={`What ${system || 'that system'} means by it`}
            value={definition}
            placeholder="sum(amount_usd) after refunds"
            onChange={(e) => commit(setPair(state, i, 1, e.target.value))}
          />
          <button
            type="button"
            className="btn btn--ghost btn--sm"
            aria-label={`Remove definition ${i + 1}`}
            onClick={() => commit(removePair(state, i))}
          >
            ×
          </button>
        </div>
      ))}
      <div className="projpanel-actions">
        <button type="button" className="btn btn--sm" onClick={() => commit(addPair(state))}>
          + Add a system's definition
        </button>
      </div>
    </div>
  );
}

// ── Sections ────────────────────────────────────────────────────────────────

/**
 * A collapsed group with a one-line reason to open it.
 *
 * `hint` is under the same authoring contract as a FieldSpec's: it is read by
 * the person deciding whether to open the group, so it says what the group is
 * FOR in their words and defines the one term it cannot avoid.
 *
 * The chip carries its own UNIT rather than a bare number. "· 3" meant three
 * rows on a list group and three filled fields on a record group — one glyph
 * with two meanings, in the same column of the same form.
 */
function Group({
  title,
  hint,
  countLabel,
  children,
}: {
  title: string;
  hint: string;
  countLabel?: string;
  children: React.ReactNode;
}) {
  return (
    <details className="proj-decl-group">
      <summary>
        {title}
        {countLabel && <span className="proj-decl-count"> · {countLabel}</span>}
      </summary>
      <span className="proj-field-hint">{hint}</span>
      {children}
    </details>
  );
}

/** A group that is a LIST of things: one card per row, add and remove. */
function RowList({
  title,
  hint,
  rowLabel,
  rowLabelPlural,
  addLabel,
  fields,
  rows,
  onChange,
}: {
  title: string;
  hint: string;
  rowLabel: string;
  /** Spelled out rather than pluralized: "person" is not "persons". */
  rowLabelPlural: string;
  addLabel: string;
  fields: FieldSpec[];
  rows: Row[];
  onChange: (next: Row[]) => void;
}) {
  const patch = (i: number, key: string, v: unknown) =>
    onChange(rows.map((r, n) => (n === i ? { ...r, [key]: v } : r)));
  const countLabel =
    rows.length > 0 ? `${rows.length} ${rows.length === 1 ? rowLabel : rowLabelPlural}` : undefined;
  return (
    <Group title={title} hint={hint} countLabel={countLabel}>
      {rows.map((row, i) => (
        <div className="proj-dataset" key={i}>
          <div className="proj-dataset-head">
            <span className="eyebrow">{rowLabel} {i + 1}</span>
            <button
              type="button"
              className="btn btn--ghost btn--sm"
              onClick={() => onChange(rows.filter((_, n) => n !== i))}
            >
              Remove
            </button>
          </div>
          {fields.map((f) => (
            <Field
              key={f.key}
              spec={f}
              idPrefix={`decl-${title.replace(/\s+/g, '-').toLowerCase()}-${i}`}
              value={row[f.key]}
              onChange={(v) => patch(i, f.key, v)}
            />
          ))}
        </div>
      ))}
      <div className="projpanel-actions">
        <button type="button" className="btn btn--sm" onClick={() => onChange([...rows, {}])}>
          {addLabel}
        </button>
      </div>
    </Group>
  );
}

/** A group that is ONE record: a handful of fields, no rows. */
function Record_({
  title,
  hint,
  fields,
  value,
  onChange,
}: {
  title: string;
  hint: string;
  fields: FieldSpec[];
  value: Row;
  onChange: (next: Row) => void;
}) {
  const filled = fields.filter((f) => {
    const v = value[f.key];
    return v !== undefined && v !== null && String(v).trim() !== '';
  }).length;
  return (
    <Group
      title={title}
      hint={hint}
      countLabel={filled > 0 ? `${filled} of ${fields.length} filled` : undefined}
    >
      {fields.map((f) => (
        <Field
          key={f.key}
          spec={f}
          idPrefix={`decl-${title.replace(/\s+/g, '-').toLowerCase()}`}
          value={value[f.key]}
          onChange={(v) => onChange({ ...value, [f.key]: v })}
        />
      ))}
    </Group>
  );
}

// ── The groups themselves ───────────────────────────────────────────────────

const KPI_FIELDS: FieldSpec[] = [
  { key: 'name', label: 'name', hint: 'What you call this number in the meeting.', placeholder: 'net revenue' },
  { key: 'id', label: 'id', hint: 'A short name runs will cite. Left blank, the name is used.', placeholder: 'net_revenue' },
  {
    key: 'definitions', kind: 'definitions', label: 'what each system means by it',
    hint: 'One row per system that computes it. Two systems that define it differently do not have a discrepancy — they have two measures, and this is where that is written down once.',
  },
  { key: 'unit', label: 'unit', placeholder: 'currency', hint: 'Currency, count, percentage.' },
  { key: 'currency', label: 'currency', placeholder: 'USD' },
  { key: 'owner', label: 'owner', hint: 'Who settles an argument about what it means.', placeholder: 'Finance ops' },
];

const DASHBOARD_FIELDS: FieldSpec[] = [
  { key: 'name', label: 'name', placeholder: 'Finance revenue (Looker)' },
  {
    key: 'source', label: 'table or export behind it',
    hint: '"The finance dashboard" is not a source — the source is the thing a query can be pointed at.',
    placeholder: 'analytics.revenue_daily',
  },
  { key: 'readPath', label: 'how it is read', hint: 'The query, the view, the export path.', placeholder: 'SQL at invoice-line level' },
  { key: 'owner', label: 'owner', placeholder: 'Finance ops' },
  { key: 'refreshCadence', label: 'refresh cadence', placeholder: 'nightly at 04:00' },
  { key: 'asOfConvention', label: 'as-of convention', hint: 'What date the figure on it covers.', placeholder: 'close_date, snapshot at 00:00 ET' },
  { key: 'link', label: 'link', placeholder: 'https://looker.internal/dashboards/12' },
];

const TOLERANCE_FIELDS: FieldSpec[] = [
  {
    key: 'subject', label: 'subject',
    hint: 'The measure or the column this is about — a KPI name for a reconciliation, a feature name for a shift audit.',
    placeholder: 'net_revenue',
  },
  {
    key: 'type', label: 'subject type', kind: 'select', options: ['numeric', 'categorical', 'timestamp'],
    hint: 'What kind of thing this is — it decides which statistic can be computed, and the shift audit REQUIRES it. Left blank, the row arrives on a recipe form with a required cell empty.',
  },
  {
    key: 'why', label: 'why it matters',
    hint: 'What breaks downstream when this moves. A severity with no consequence behind it is a mood, not a severity — and this cell is required on the shift audit’s features table.',
    placeholder: 'a new code silently breaks the card/cash split',
  },
  {
    key: 'statistic', label: 'statistic', placeholder: 'psi',
    hint: 'What is measured for this subject: the total itself (for a reconciliation), a drift score — PSI (population stability index) or KL (Kullback–Leibler divergence), which score how far a distribution moved with 0 meaning no change — or category-emergence (a value that appeared or vanished).',
  },
  {
    key: 'value', label: 'value', kind: 'number', placeholder: '0.2',
    hint: 'Required. A row with a subject and no number is not a tolerance, and it is refused when you save.',
  },
  {
    key: 'unit', label: 'unit', kind: 'select', options: TOLERANCE_UNITS,
    hint: 'The denominator is half the tolerance: 0.5% of A’s total and 0.5% of B’s are different numbers.',
  },
  { key: 'segmentValue', label: 'per-segment value', kind: 'number', hint: 'The tighter number one segment must clear.', placeholder: '250' },
  { key: 'watchBand', label: 'watch band', kind: 'number', hint: 'Worth watching, not yet worth acting on. Below the value. Convention for PSI: 0.1.', placeholder: '0.1' },
  {
    key: 'severity', label: 'severity', kind: 'select', options: ['blocker', 'major', 'minor'],
    hint: 'How loudly a breach should land.',
  },
  { key: 'owner', label: 'set by', hint: 'Who stands behind this number.', placeholder: 'Sofia, analytics lead' },
  { key: 'setAt', label: 'set on', kind: 'date' },
];

const EXCLUSION_FIELDS: FieldSpec[] = [
  { key: 'name', label: 'name', placeholder: 'internal test accounts' },
  { key: 'side', label: 'which side applies it', kind: 'select', options: ['A', 'B', 'both'] },
  { key: 'filter', label: 'the filter, if you know it', hint: "The condition that picks out those rows (account_type = 'internal'). Without one it cannot be a term of the equation — it is reported as unexplained.", placeholder: "account_type = 'internal'" },
  { key: 'owner', label: 'owner', placeholder: 'Finance ops' },
];

const STAKEHOLDER_FIELDS: FieldSpec[] = [
  { key: 'role', label: 'role', placeholder: 'approves the equations' },
  { key: 'name', label: 'name', placeholder: 'Sofia' },
  { key: 'contactRef', label: 'how to reach them', hint: 'A handle or a list name — a reference, never an address book entry.', placeholder: '#analytics' },
];

const EVALSET_FIELDS: FieldSpec[] = [
  { key: 'id', label: 'id', hint: 'A short name every run and every check will cite this set by.', placeholder: 'churn-holdout' },
  { key: 'kind', label: 'kind', kind: 'select', options: ['dev', 'holdout'] },
  { key: 'location', label: 'location', hint: 'A path or a table. Never a credential.', placeholder: 'analytics.account_daily_holdout' },
  { key: 'rowCount', label: 'row count', kind: 'number', placeholder: '96417' },
  { key: 'dateRange', label: 'date range', placeholder: '2025-04' },
  {
    key: 'sha256', label: 'sha256',
    hint: '"The June holdout" names a belief; a content hash names bytes. A holdout declared without one is accepted and carries a caveat wherever it is shown: no content hash — nothing can prove which bytes were scored.',
    placeholder: '9b74e2c1…',
  },
  { key: 'labelColumn', label: 'label column', placeholder: 'churned_30d' },
  { key: 'frozenAt', label: 'frozen at', kind: 'date' },
];

const METRIC_FIELDS: FieldSpec[] = [
  { key: 'name', label: 'metric', hint: "Name it exactly as the model's report will, so the platform can match them.", placeholder: 'PR-AUC' },
  { key: 'id', label: 'id', placeholder: 'pr-auc-primary' },
  { key: 'threshold', label: 'threshold', hint: 'With its comparator: the direction is half the requirement.', placeholder: '>= 0.65' },
  { key: 'operatingPoint', label: 'operating point', hint: 'Where the decision is actually made.', placeholder: 'top 2% of orders flagged' },
  { key: 'uncertaintyMethod', label: 'uncertainty', placeholder: 'bootstrap, 1000 resamples' },
  { key: 'formulaOrLibrary', label: 'formula or library', hint: 'What computes it, so two runs compute the same thing.', placeholder: 'sklearn.metrics.average_precision_score' },
];

const SLICE_FIELDS: FieldSpec[] = [
  { key: 'column', label: 'column', placeholder: 'region' },
  { key: 'floor', label: 'floor', kind: 'number', hint: 'What the worst slice on this column may not fall below.', placeholder: '0.45' },
];

export function DeclarationGroups({
  value,
  onChange,
}: {
  value: ProjectDeclarations;
  onChange: (next: ProjectDeclarations) => void;
}) {
  const set = (patch: Partial<ProjectDeclarations>) => onChange({ ...value, ...patch });
  return (
    <div className="proj-decl-groups">
      <div className="eyebrow proj-decl-eyebrow">What this project declares once</div>
      <span className="proj-field-hint">
        Everything here is optional, and every recipe run under this project offers it as a default
        instead of asking you to type it again. Open only what you have an answer for.
      </span>

      <RowList
        title="Measures and what each system means by them"
        hint="Your dictionary of measures (KPIs). A reconciliation asks for both systems' definitions of every measure — declared here, it arrives filled in, and every summary the platform writes uses your names for your numbers."
        rowLabel="measure"
        rowLabelPlural="measures"
        addLabel="+ Add a measure"
        fields={KPI_FIELDS}
        rows={(value.kpis ?? []) as unknown as Row[]}
        onChange={(rows) => set({ kpis: rows as unknown as KPIDecl[] })}
      />

      <RowList
        title="Dashboards and what feeds them"
        hint="Each dashboard with the table or export behind it, so “what feeds dashboard A” is a pick-list rather than a memory test."
        rowLabel="dashboard"
        rowLabelPlural="dashboards"
        addLabel="+ Add a dashboard"
        fields={DASHBOARD_FIELDS}
        rows={(value.dashboards ?? []) as unknown as Row[]}
        onChange={(rows) => set({ dashboards: rows as unknown as DashboardDecl[] })}
      />

      <Record_
        title="Reporting calendar"
        hint="“Q2” names at least three different windows depending on the fiscal year, the timezone and the snapshot day. Say which once."
        fields={[
          { key: 'fiscalYearStart', label: 'fiscal year starts', placeholder: 'February 1' },
          { key: 'quarterBoundaries', label: 'quarter boundaries', placeholder: 'Q2 = Apr 1 – Jun 30' },
          { key: 'timezone', label: 'timezone', hint: "The timezone, as its standard name (“America/New_York”). An undeclared one is the commonest reason two correct numbers disagree.", placeholder: 'America/New_York' },
          { key: 'snapshotDay', label: 'snapshot day', hint: 'The day any snapshot is cut on.', placeholder: 'the 15th' },
          { key: 'lateArrivalPolicy', label: 'late arrivals', hint: 'What happens to rows that land after the period closes.', placeholder: 'restated in the following month' },
        ]}
        value={(value.calendar ?? {}) as unknown as Row}
        onChange={(v) => set({ calendar: v as unknown as CalendarDecl })}
      />

      <RowList
        title="Standing tolerances"
        hint="How far each measure or column may move before somebody has to act, with its unit and whose number it is. A run may tighten one of these for itself; it can never widen one."
        rowLabel="tolerance"
        rowLabelPlural="tolerances"
        addLabel="+ Add a tolerance"
        fields={TOLERANCE_FIELDS}
        rows={(value.tolerances ?? []) as unknown as Row[]}
        onChange={(rows) => set({ tolerances: rows as unknown as ToleranceDecl[] })}
      />

      <RowList
        title="Known differences"
        hint="The filters one side applies and the other does not — test accounts, intercompany rows, a snapshot cut-off. Named here they become terms of the equation; unnamed they are reported as unexplained."
        rowLabel="difference"
        rowLabelPlural="differences"
        addLabel="+ Add a known difference"
        fields={EXCLUSION_FIELDS}
        rows={(value.exclusions ?? []) as unknown as Row[]}
        onChange={(rows) => set({ exclusions: rows as unknown as ExclusionDecl[] })}
      />

      <RowList
        title="Who signs off, and who reads it"
        hint="Who owns each side, who approves the rules, who receives the report."
        rowLabel="person"
        rowLabelPlural="people"
        addLabel="+ Add a stakeholder"
        fields={STAKEHOLDER_FIELDS}
        rows={(value.stakeholders ?? []) as unknown as Row[]}
        onChange={(rows) => set({ stakeholders: rows as unknown as StakeholderDecl[] })}
      />

      <Record_
        title="How a finished deliverable is handed over"
        hint="The format and who receives it — a reference, never an address list."
        fields={[
          { key: 'format', label: 'format', placeholder: 'a one-page markdown summary plus the CSV' },
          { key: 'recipientRef', label: 'recipient', placeholder: '#finance-analytics' },
        ]}
        value={(value.delivery ?? {}) as unknown as Row}
        onChange={(v) => set({ delivery: v as unknown as DeliveryDecl })}
      />

      <RowList
        title="Evaluation sets"
        hint="The frozen rows a model is scored on, each with the content hash that says WHICH BYTES were scored rather than which set somebody meant."
        rowLabel="evaluation set"
        rowLabelPlural="evaluation sets"
        addLabel="+ Add an evalset"
        fields={EVALSET_FIELDS}
        rows={(value.evalsets ?? []) as unknown as Row[]}
        onChange={(rows) => set({ evalsets: rows as unknown as EvalsetDecl[] })}
      />

      <Record_
        title="Who holds the holdout"
        hint="The set nothing may train on, and who is answerable for it. The rule you write here is quoted word for word in every summary the platform writes about a model under this project."
        fields={[
          { key: 'holder', label: 'holder', placeholder: 'Dev, ML lead' },
          { key: 'evalsetId', label: 'evalset id', hint: 'One of the evaluation sets declared above.', placeholder: 'churn-holdout' },
          { key: 'rule', label: 'the rule', hint: 'Blank means the default rule: no step that can write files may read this set. Write a stricter rule if you have one.', placeholder: 'only the scoring step may read it, and only once' },
        ]}
        value={(value.holdoutCustody ?? {}) as unknown as Row}
        onChange={(v) => set({ holdoutCustody: v as unknown as HoldoutCustodyDecl })}
      />

      <RowList
        title="Metrics and the numbers they must clear"
        hint="The numbers a model is judged by, each with the value it must reach. Declared here, a recipe's “success metric” picks one of these — so the platform makes the comparison against your number, instead of the model-building step reading a sentence its own way."
        rowLabel="metric"
        rowLabelPlural="metrics"
        addLabel="+ Add a metric"
        fields={METRIC_FIELDS}
        rows={(value.metrics ?? []) as unknown as Row[]}
        onChange={(rows) => set({ metrics: rows as unknown as MetricDecl[] })}
      />

      <RowList
        title="Slices that must be reported"
        hint="The columns a model's results must be broken down by (each value is a “slice”), and the floor the worst slice may not fall below. Left undeclared, the breakdown is whatever the model-building step chooses to report."
        rowLabel="slice"
        rowLabelPlural="slices"
        addLabel="+ Add a slice"
        fields={SLICE_FIELDS}
        rows={(value.slices ?? []) as unknown as Row[]}
        onChange={(rows) => set({ slices: rows as unknown as SliceDecl[] })}
      />

      <Record_
        title="When the label becomes knowable"
        hint="The “label” is the true answer a model is trained to predict. Name the event that settles it and how long after the prediction that event happens. Anything known only after that moment must not be an input — that is “leakage” — and every leakage check is judged against this fact."
        fields={[
          { key: 'event', label: 'event', placeholder: 'a chargeback is filed' },
          { key: 'lag', label: 'lag', placeholder: '90 days after the order' },
        ]}
        value={(value.labelAvailability ?? {}) as unknown as Row}
        onChange={(v) => set({ labelAvailability: v as unknown as LabelAvailabilityDecl })}
      />

      <Record_
        title="What the model has to live inside"
        hint="The limits it ships under, so a model that clears the metric and misses the budget is caught before it is built."
        fields={[
          { key: 'latency', label: 'latency', placeholder: 'under 50 ms at p99 in the checkout path' },
          { key: 'artifactSize', label: 'artifact size', placeholder: 'under 50 MB' },
          { key: 'residency', label: 'residency', placeholder: 'EU data stays in the EU region' },
          { key: 'cost', label: 'cost', placeholder: 'under $200/month to serve' },
        ]}
        value={(value.servingConstraints ?? {}) as unknown as Row}
        onChange={(v) => set({ servingConstraints: v as unknown as ServingConstraintsDecl })}
      />

      <Record_
        title="What a change has to re-score against"
        hint="When the platform proposes an improved version of a skill (a “refinement”), or learns a new skill from a run, this is the evaluation set it must be scored on and the margin it must win by. Without it, “this is better” is an opinion."
        fields={[
          { key: 'evalsetId', label: 'evalset id', hint: 'One of the evaluation sets declared above.', placeholder: 'churn-holdout' },
          { key: 'minMargin', label: 'minimum margin', hint: 'With its unit.', placeholder: '0.02 PR-AUC' },
        ]}
        value={(value.regressionPolicy ?? {}) as unknown as Row}
        onChange={(v) => set({ regressionPolicy: v as unknown as RegressionPolicyDecl })}
      />

      <Round3Groups value={value} onChange={onChange} />
    </div>
  );
}

/**
 * Strip the blanks out of the round-2 groups so an untouched group is stored as
 * nothing rather than as a row of empty strings. Rows that end up empty are
 * dropped entirely — an "+ Add" someone thought better of must not become a
 * declaration.
 */
export function cleanGroups(v: ProjectDeclarations): ProjectDeclarations {
  const cleanRow = (row: Row): Row => {
    const out: Row = {};
    for (const [k, cell] of Object.entries(row)) {
      if (cell === undefined || cell === null) continue;
      if (typeof cell === 'string' && cell.trim() === '') continue;
      if (typeof cell === 'object' && !Array.isArray(cell) && Object.keys(cell as Row).length === 0) continue;
      out[k] = cell;
    }
    return out;
  };
  const list = (rows: unknown): Row[] =>
    ((rows ?? []) as Row[]).map(cleanRow).filter((r) => Object.keys(r).length > 0);
  return {
    kpis: list(v.kpis) as unknown as KPIDecl[],
    dashboards: list(v.dashboards) as unknown as DashboardDecl[],
    calendar: cleanRow((v.calendar ?? {}) as unknown as Row) as unknown as CalendarDecl,
    tolerances: list(v.tolerances) as unknown as ToleranceDecl[],
    exclusions: list(v.exclusions) as unknown as ExclusionDecl[],
    stakeholders: list(v.stakeholders) as unknown as StakeholderDecl[],
    delivery: cleanRow((v.delivery ?? {}) as unknown as Row) as unknown as DeliveryDecl,
    evalsets: list(v.evalsets) as unknown as EvalsetDecl[],
    holdoutCustody: cleanRow((v.holdoutCustody ?? {}) as unknown as Row) as unknown as HoldoutCustodyDecl,
    metrics: list(v.metrics) as unknown as MetricDecl[],
    slices: list(v.slices) as unknown as SliceDecl[],
    labelAvailability: cleanRow((v.labelAvailability ?? {}) as unknown as Row) as unknown as LabelAvailabilityDecl,
    servingConstraints: cleanRow((v.servingConstraints ?? {}) as unknown as Row) as unknown as ServingConstraintsDecl,
    regressionPolicy: cleanRow((v.regressionPolicy ?? {}) as unknown as Row) as unknown as RegressionPolicyDecl,
    ...cleanRound3(v),
  };
}


// ── Round 3: when it runs, where the tables are, where the code is ──────────
//
// These three groups sit at the END of the file, and so does the import they
// need. Two branches were editing this file at once; keeping the addition
// append-only meant the round-2 groups above could be reworded without either
// side touching a line the other had moved.

import type { LineageDecl, ScheduleDecl, WarehouseDecl, WarehouseRoleRefs } from '../api';
import { WAREHOUSE_ACCESS } from '../api';

const SCHEDULE_FIELDS: FieldSpec[] = [
  {
    key: 'expectedCadence', label: 'expected cadence',
    hint: 'How often you expect to run this.',
    placeholder: 'every weekday morning',
  },
  {
    key: 'deadline', label: 'deadline',
    hint: 'The clock time it has to have landed by.',
    placeholder: '06:00',
  },
  {
    key: 'timezone', label: 'timezone',
    hint: 'A deadline with no timezone is a different moment in every office.',
    placeholder: 'America/New_York',
  },
  {
    key: 'triggeredBy', label: 'triggered by',
    hint: 'The external system that actually starts it — Hyperplane cannot schedule a recurring run, so name what will.',
    placeholder: 'Airflow DAG analytics_daily',
  },
  {
    key: 'pageRef', label: 'who to page',
    hint: 'Who or what to page when it does not land. A reference — a rota name, a handle, a list id — never an address.',
    placeholder: 'analytics-oncall',
  },
];

// `roleRefs` is nested and `Field` is flat, so the two roles are held on the
// form under flat keys and folded back into `{roleRefs: {read, write}}` before
// anything is stored. The shape on the wire never changes.
const WAREHOUSE_FIELDS: FieldSpec[] = [
  { key: 'engine', label: 'engine', hint: 'What runs the SQL.', placeholder: 'postgres' },
  { key: 'databaseSchema', label: 'database schema', hint: 'The schema the declared tables are in.', placeholder: 'analytics' },
  {
    key: 'connectionRef', label: 'connection',
    hint: 'The NAME of the environment variable that holds the connection, never the connection itself — the platform refuses anything that is not an environment-variable name.',
    placeholder: 'ANALYTICS_DSN',
  },
  {
    key: 'roleRead', label: 'read role',
    hint: 'The name of the environment variable holding the reading role. A reference, never the credential behind it.',
    placeholder: 'ANALYTICS_READER_ROLE',
  },
  {
    key: 'roleWrite', label: 'write role',
    hint: 'The name of the environment variable holding the writing role. A reference, never the credential behind it.',
    placeholder: 'ANALYTICS_WRITER_ROLE',
  },
  {
    key: 'access', label: 'access', kind: 'select', options: WAREHOUSE_ACCESS,
    hint: 'What a run under this project may do here: read these tables, or write them.',
  },
];

const LINEAGE_FIELDS: FieldSpec[] = [
  {
    key: 'repoPath', label: 'repo path',
    hint: 'Where the code that writes these tables lives.',
    placeholder: 'analytics/models/*.sql',
  },
  { key: 'runCommand', label: 'run command', hint: 'What runs the pipeline.', placeholder: 'analytics/run.sh' },
  {
    key: 'manifestPath', label: 'manifest path',
    hint: 'The file that lists what the pipeline builds.',
    placeholder: 'analytics/manifest.json',
  },
  {
    key: 'prConvention', label: 'PR convention',
    hint: 'How a change to it reaches production — the convention, not a link to one pull request.',
    placeholder: 'one PR per model, reviewed by the data team',
  },
];

/** The stored `{roleRefs: {read, write}}` flattened onto the form's two cells. */
function warehouseToRow(w: WarehouseDecl | undefined): Row {
  const { roleRefs, ...rest } = (w ?? {}) as WarehouseDecl;
  return { ...(rest as unknown as Row), roleRead: roleRefs?.read, roleWrite: roleRefs?.write };
}

/** The form's two cells folded back under `roleRefs`. An empty `roleRefs` is
 *  never stored — a group nobody filled in must not become a declaration. */
function rowToWarehouse(row: Row): WarehouseDecl {
  const { roleRead, roleWrite, ...rest } = row;
  const out = { ...(rest as unknown as WarehouseDecl) };
  const roleRefs: WarehouseRoleRefs = {};
  if (typeof roleRead === 'string' && roleRead.trim() !== '') roleRefs.read = roleRead;
  if (typeof roleWrite === 'string' && roleWrite.trim() !== '') roleRefs.write = roleWrite;
  if (Object.keys(roleRefs).length > 0) out.roleRefs = roleRefs;
  else delete out.roleRefs;
  return out;
}

/**
 * The three operating groups: when a run is expected, which warehouse the
 * declared tables are in, and where the code that writes them lives.
 *
 * Same three rules as everything above — optional, collapsed, references only.
 * The schedule group carries one extra caution in its hint, because the word
 * "schedule" invites a belief the platform cannot honour: Hyperplane starts
 * nothing on a clock.
 */
export function Round3Groups({
  value,
  onChange,
}: {
  value: ProjectDeclarations;
  onChange: (next: ProjectDeclarations) => void;
}) {
  const set = (patch: Partial<ProjectDeclarations>) => onChange({ ...value, ...patch });
  return (
    <>
      <Record_
        title="When this has to run, and what starts it"
        hint="Hyperplane does not schedule anything: a run starts when a person or an external system starts it. What you declare here is what you EXPECT and who is answerable when it does not happen."
        fields={SCHEDULE_FIELDS}
        value={(value.schedule ?? {}) as unknown as Row}
        onChange={(v) => set({ schedule: v as unknown as ScheduleDecl })}
      />

      <Record_
        title="The warehouse these tables live in"
        hint="Where the declared tables actually are, and under which role a run reaches them. Every cell is a reference — an engine, a schema, the name of an environment variable; never a connection string and never a credential."
        fields={WAREHOUSE_FIELDS}
        value={warehouseToRow(value.warehouse as WarehouseDecl | undefined)}
        onChange={(v) => set({ warehouse: rowToWarehouse(v) })}
      />

      <Record_
        title="Where the pipeline’s code lives"
        hint="So a finding about a table can be traced to the model that writes it. References only — a path, a command, a convention; never a credential."
        fields={LINEAGE_FIELDS}
        value={(value.lineage ?? {}) as unknown as Row}
        onChange={(v) => set({ lineage: v as unknown as LineageDecl })}
      />
    </>
  );
}

/**
 * Strip the blanks out of the round-3 groups, the same way `cleanGroups` cleans
 * its records: a blank cell is dropped, and a group whose every cell was blank
 * is stored as nothing rather than as a row of empty strings.
 *
 * `roleRefs` needs its own pass. It is the one nested object here, so the flat
 * blank-drop above it would keep `{read: ""}` — a role reference that names no
 * role.
 */
export function cleanRound3(v: ProjectDeclarations): {
  schedule: ScheduleDecl;
  warehouse: WarehouseDecl;
  lineage: LineageDecl;
} {
  const cleanRow = (row: Row): Row => {
    const out: Row = {};
    for (const [k, cell] of Object.entries(row)) {
      if (cell === undefined || cell === null) continue;
      if (typeof cell === 'string' && cell.trim() === '') continue;
      if (typeof cell === 'object' && !Array.isArray(cell) && Object.keys(cell as Row).length === 0) continue;
      out[k] = cell;
    }
    return out;
  };
  const warehouse = cleanRow((v.warehouse ?? {}) as unknown as Row);
  const roleRefs = cleanRow((warehouse.roleRefs ?? {}) as Row);
  if (Object.keys(roleRefs).length > 0) warehouse.roleRefs = roleRefs;
  else delete warehouse.roleRefs;
  return {
    schedule: cleanRow((v.schedule ?? {}) as unknown as Row) as unknown as ScheduleDecl,
    warehouse: warehouse as unknown as WarehouseDecl,
    lineage: cleanRow((v.lineage ?? {}) as unknown as Row) as unknown as LineageDecl,
  };
}
