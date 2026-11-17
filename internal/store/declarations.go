package store

// declarations.go — what a project declares ONCE, so a run does not re-type it.
//
// Before this, a project declared nothing a data engineer needs, so the same
// dataset was described from memory in every run's slots — and two descriptions
// of the same six files in the same project disagreed about how many columns
// they had. Declarations are the project-level record those slots inherit from:
// stated once, edited in one place, and cited by every run under the project.
//
// EXTENSIBILITY IS THE POINT. `projects.declarations` is a document of typed
// GROUPS — `purpose`, `datasets`, `environment` today; `kpis`, `evalsets` and
// whatever else later — and every reader here takes the groups it knows and
// leaves the rest byte-for-byte alone. That is why the column is carried as raw
// JSON on the Project struct rather than as a struct that would silently drop a
// group it was not compiled with, and why UpdateProject merges group by group.
//
// REFERENCES ONLY. A dataset's `location` is a path or a table name; an
// environment is where runs execute. Never a credential, never a secret value —
// the same rule connector `credentialsRef` follows.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// EmptyDeclarations is the column's default: a document with no groups.
const EmptyDeclarations = "{}"

// ProjectDeclarations is the TYPED VIEW of the groups this build understands.
// It is a reader's convenience, never the storage shape: parsing into it and
// re-marshalling would drop every group added after this build was compiled, so
// nothing writes the column from this struct.
type ProjectDeclarations struct {
	Purpose     PurposeDecl     `json:"purpose,omitempty"`
	Datasets    []DatasetDecl   `json:"datasets,omitempty"`
	Environment EnvironmentDecl `json:"environment,omitempty"`

	// The analyst's groups. A reconciliation asks for all six of these once per
	// run today; declared here they are asked for once per project.
	KPIs         []KPIDecl         `json:"kpis,omitempty"`
	Dashboards   []DashboardDecl   `json:"dashboards,omitempty"`
	Calendar     CalendarDecl      `json:"calendar,omitempty"`
	Tolerances   []ToleranceDecl   `json:"tolerances,omitempty"`
	Exclusions   []ExclusionDecl   `json:"exclusions,omitempty"`
	Stakeholders []StakeholderDecl `json:"stakeholders,omitempty"`
	Delivery     DeliveryDecl      `json:"delivery,omitempty"`

	// The ML groups. Each is a record a learned skill, an improve loop or a
	// shift audit can cite BY ID, which is what none of them could do before.
	Evalsets           []EvalsetDecl          `json:"evalsets,omitempty"`
	HoldoutCustody     HoldoutCustodyDecl     `json:"holdoutCustody,omitempty"`
	Metrics            []MetricDecl           `json:"metrics,omitempty"`
	Slices             []SliceDecl            `json:"slices,omitempty"`
	LabelAvailability  LabelAvailabilityDecl  `json:"labelAvailability,omitempty"`
	ServingConstraints ServingConstraintsDecl `json:"servingConstraints,omitempty"`
	RegressionPolicy   RegressionPolicyDecl   `json:"regressionPolicy,omitempty"`

	// The operational groups. Each answers a question every data recipe asked
	// per run and no project could answer once: when this is expected to run
	// and what starts it, which store the tables live in and with what rights,
	// and where the code that builds them lives.
	Schedule  ScheduleDecl  `json:"schedule,omitempty"`
	Warehouse WarehouseDecl `json:"warehouse,omitempty"`
	Lineage   LineageDecl   `json:"lineage,omitempty"`
}

// PurposeDecl is the human-owned "why this project exists" block.
//
// "What this project is for" is deliberately NOT here: it is `projects.summary`,
// the field that heads the project page, and duplicating it into declarations
// would give one sentence two homes and two chances to disagree.
type PurposeDecl struct {
	// Owner: who decides whether a delivery is accepted. A name, not an account.
	Owner string `json:"owner,omitempty"`
	// Due: an ISO date (YYYY-MM-DD), optional.
	Due string `json:"due,omitempty"`
	// DoneMeans: the sentence you would say to your boss when this is finished.
	DoneMeans string `json:"doneMeans,omitempty"`
	// Constraints: sensitive-data rules, runtime ceilings, anything a run under
	// this project must not do.
	Constraints string `json:"constraints,omitempty"`
}

// DatasetDecl is one dataset the project works on.
//
// Only `name` is ever required. A non-engineer declaring "the Looker report
// 'Paid Social Q2'" and nothing else is a complete, useful entry — an engineer
// fills in the technical half later, and every field below is optional so that
// half can arrive separately.
type DatasetDecl struct {
	// ID is the stable handle a run cites. Derived from the name when absent.
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	// Location is a REFERENCE: a path, a table, an export. Never a credential.
	Location string `json:"location,omitempty"`
	Format   string `json:"format,omitempty"`
	// Kind is the ROLE this dataset plays: `fact` (the thing being measured),
	// `lookup` (a small reference table joined onto a fact) or `export` (a
	// downstream extract). Empty means undeclared.
	//
	// It exists because inheritance used to be POSITIONAL: `datasets[0]` became
	// the baseline window and `datasets[1]` the current one, so a 265-row zone
	// lookup was offered as the window under test against a 24M-row trip feed,
	// labelled "inherited from project", and the inputs gate passed. A role is
	// the fact that makes "which of these is a window" answerable at all.
	Kind string `json:"kind,omitempty"`
	// Window says which side of a two-window comparison this dataset stands
	// for: `baseline`, `current`, or `both` when ONE dataset carries both
	// periods — which is the ordinary case, since a shift audit compares two
	// windows of the SAME data.
	Window string `json:"window,omitempty"`
	// BaselinePeriod and CurrentPeriod are the two periods a `both` dataset
	// covers. They are what makes one dataset legible as two windows without
	// the platform inventing a date.
	BaselinePeriod string `json:"baselinePeriod,omitempty"`
	CurrentPeriod  string `json:"currentPeriod,omitempty"`
	// Grain is one sentence: what ONE row of this dataset stands for.
	Grain string `json:"grain,omitempty"`
	// KeyColumns are the columns that together identify one row at that grain.
	// Empty with NaturalKey=="none" means there is no natural key, in which
	// case KeyCandidate carries the closest thing to one.
	KeyColumns []string `json:"keyColumns,omitempty"`
	// NaturalKey is "none" when the owner has declared there is not one. Any
	// other value is ignored — this is a flag, not a free-text field.
	NaturalKey   string   `json:"naturalKey,omitempty"`
	KeyCandidate []string `json:"keyCandidate,omitempty"`
	// ArrivalTimestampColumn is the column that says WHEN a row landed, which
	// is what freshness and late-arrival rules are computed against.
	ArrivalTimestampColumn string `json:"arrivalTimestampColumn,omitempty"`
	// Timezone of the timestamp columns, IANA name. An undeclared timezone is
	// the single most common evidence gap a quality audit reports.
	Timezone   string   `json:"timezone,omitempty"`
	PIIColumns []string `json:"piiColumns,omitempty"`
	// SafeUse is what these numbers must NOT be used for, plus how the
	// sensitive columns may appear in a report.
	SafeUse string `json:"safeUse,omitempty"`
	// LoadSemantics is HOW this dataset is loaded: full or incremental, the
	// watermark column, how late a row may arrive and what a late row does to
	// the period it belongs to.
	//
	// It is a POINTER so that "nobody said" and "declared as full" stay
	// different answers — a zero-valued record would read as the first while
	// meaning the second. A pipeline recipe used to produce these four facts as
	// an OUTPUT of its planning step; they are the owner's facts about the
	// owner's data, and they belong on the way in.
	LoadSemantics *LoadSemanticsDecl `json:"loadSemantics,omitempty"`
	// ExpectedRowCount is what the owner BELIEVES the dataset holds. It is a
	// belief until a run measures it — which is what Measured is for.
	ExpectedRowCount *int64 `json:"expectedRowCount,omitempty"`
	// Measured is READ-ONLY to the form: the last profile a run wrote back, so
	// the next run starts from a measurement rather than from memory. Nothing
	// writes it yet; the field exists so the write-back lands in a declared
	// place rather than inventing one.
	Measured *DatasetMeasurement `json:"measured,omitempty"`
}

// DatasetMeasurement is what a run last actually observed about a dataset.
type DatasetMeasurement struct {
	RowCount      *int64 `json:"rowCount,omitempty"`
	ColumnCount   *int   `json:"columnCount,omitempty"`
	PeriodCovered string `json:"periodCovered,omitempty"`
	MeasuredAt    string `json:"measuredAt,omitempty"`
	MeasuredByRun string `json:"measuredByRun,omitempty"`
}

// Dataset roles. A role is declared, never inferred: an undeclared role is
// "nobody said", which is treated as "could be a window, could not" — the
// conservative direction, since guessing is the defect these constants close.
const (
	DatasetKindFact   = "fact"
	DatasetKindLookup = "lookup"
	DatasetKindExport = "export"
)

// DatasetKinds is every role a declared dataset may carry, in form order.
func DatasetKinds() []string { return []string{DatasetKindFact, DatasetKindLookup, DatasetKindExport} }

// Dataset windows. `both` is the ordinary answer for a shift audit: the two
// windows are two periods of ONE dataset, not two datasets.
const (
	DatasetWindowBaseline = "baseline"
	DatasetWindowCurrent  = "current"
	DatasetWindowBoth     = "both"
)

// DatasetWindows is every window a declared dataset may stand for.
func DatasetWindows() []string {
	return []string{DatasetWindowBaseline, DatasetWindowCurrent, DatasetWindowBoth}
}

// IsWindowCandidate reports whether a dataset could stand for a window under
// test. A lookup or an export never can — a zone lookup is not the current
// window of a trip feed — and a dataset whose role nobody declared still can,
// because refusing to offer it would punish the projects that predate `kind`.
func IsWindowCandidate(ds DatasetDecl) bool {
	switch strings.ToLower(strings.TrimSpace(ds.Kind)) {
	case DatasetKindLookup, DatasetKindExport:
		return false
	default:
		return true
	}
}

// EnvironmentDecl is where runs under this project execute.
type EnvironmentDecl struct {
	// Where: the machine or container, the engine available there, and where
	// outputs land.
	Where string `json:"where,omitempty"`
	// RuntimeBudgetDefault is the ceiling a run inherits when it declares none.
	RuntimeBudgetDefault string `json:"runtimeBudgetDefault,omitempty"`
}

// ParseDeclarations reads the groups this build understands out of the raw
// column. Unknown groups are not an error and are not returned — they are still
// in the column, which is the only place they need to be.
func ParseDeclarations(raw JSON) (ProjectDeclarations, error) {
	var d ProjectDeclarations
	if len(raw) == 0 {
		return d, nil
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return d, fmt.Errorf("parse project declarations: %w", err)
	}
	return d, nil
}

// MergeDeclarations applies a patch document GROUP BY GROUP.
//
// A group named in the patch replaces that group outright (a datasets array is
// the whole list, not an append). A group the patch does not mention is left
// exactly as it was — which is what lets a client that has never heard of
// `kpis` edit `datasets` without destroying it. A group explicitly set to null
// is removed.
//
// Returns an error rather than a guess when either side is not a JSON object,
// because silently replacing a malformed document is how a declaration set
// disappears.
func MergeDeclarations(existing, patch JSON) (JSON, error) {
	base := map[string]json.RawMessage{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &base); err != nil {
			return nil, fmt.Errorf("merge declarations: stored value is not a JSON object: %w", err)
		}
	}
	incoming := map[string]json.RawMessage{}
	if len(patch) > 0 {
		if err := json.Unmarshal(patch, &incoming); err != nil {
			return nil, fmt.Errorf("merge declarations: patch is not a JSON object: %w", err)
		}
	}
	for k, v := range incoming {
		if string(v) == "null" {
			delete(base, k)
			continue
		}
		base[k] = v
	}
	// Deterministic key order: the column is compared in tests and read by
	// humans in psql, and map iteration order would make both noisy.
	keys := make([]string, 0, len(base))
	for k := range base {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, fmt.Errorf("merge declarations: %w", err)
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(base[k])
	}
	b.WriteByte('}')
	return JSON(b.String()), nil
}

// ── Slot inheritance ────────────────────────────────────────────────────────

// SlotPrefill is one recipe slot's value inherited from the project, plus where
// it came from. The source is shown on the form ("inherited from project — edit
// for this run") so a person can always tell a declared value from one they
// typed, and can override it for this run without editing the project.
type SlotPrefill struct {
	Value string `json:"value"`
	// Source is the declaration path the value was read from, e.g.
	// "datasets[0].location" or "environment.runtimeBudgetDefault".
	Source string `json:"source"`
}

// SlotNote is what a slot inherits INSTEAD of a value when the project's
// declarations cannot answer it without guessing.
//
// It is deliberately not a SlotPrefill with an empty Value: "in the prefill
// map" has to keep meaning "the project declared this", or an absent
// declaration would satisfy a required slot. A note says why the slot is empty
// and what the person has to decide — "pick the window from the project's
// data" — which is the honest half of what positional inheritance used to
// assert.
type SlotNote struct {
	Slot string `json:"slot"`
	Note string `json:"note"`
}

// slotRule is one row of the slot→declaration table. Adding a recipe slot that
// should inherit is one row here and nothing else: the API, the form and the
// test all read this table.
type slotRule struct {
	// Slot is the ContextSlot id, which is shared across recipes on purpose —
	// `safeUse` means the same thing in all five data recipes.
	Slot string
	// Path is the human-readable declaration path, used as SlotPrefill.Source.
	Path string
	// Resolve returns the inherited value, or "" for "nothing to inherit".
	Resolve func(d ProjectDeclarations) string

	// PathFor names where the value actually came from, for the rules that have
	// more than one source. Absent means Path is always right. The source is
	// shown on the form ("declared at …"), so a rule that can read two places
	// must be able to say which one it read.
	PathFor func(d ProjectDeclarations) string

	// Recipes scopes a rule to the recipes where the slot id MEANS what the
	// declaration says. Empty is every recipe.
	//
	// One slot id does not always name one question. `grain` is the audited
	// dataset's grain in data-quality-audit and the FINISHED table's grain in
	// data-pipeline and pipeline-change — so inheriting a source's grain into
	// all three prefilled a mart's grain with the raw feed's, labelled it "the
	// project's answer", and the form counted it as filled. A scoped rule is
	// how one table serves two questions without answering the wrong one.
	Recipes []string
}

// appliesTo reports whether a rule may fill this slot on this recipe. An
// unscoped rule applies everywhere; the empty recipe name asks for the
// unscoped set only, which is what compose and the project page read.
func (r slotRule) appliesTo(recipe string) bool {
	if len(r.Recipes) == 0 {
		return true
	}
	for _, name := range r.Recipes {
		if name == recipe {
			return true
		}
	}
	return false
}

// slotInheritance is the single mapping between recipe slots and project
// declarations. Order is display order and does not otherwise matter.
//
// Every dataset slot resolves from `datasets[]`, because that is what these
// slots have always been: a project fact re-typed per run.
//
// NOTHING HERE IS POSITIONAL ANY MORE. Where a recipe wants TWO datasets — a
// baseline and a current window, two dashboards' sources — the pair is resolved
// from DECLARED ROLES (`kind`, `window`) and never from `datasets[0]` and
// `datasets[1]`: taking them in declaration order put a 265-row zone lookup in
// the window under test against a 24M-row trip feed, tagged it "inherited from
// project", and the inputs gate passed. When the roles do not settle it, the
// second slot is left EMPTY with a note rather than guessed (see slotNotes).
var slotInheritance = []slotRule{
	{Slot: "sources", Path: "datasets[]", Resolve: func(d ProjectDeclarations) string {
		return datasetList(d.Datasets)
	}},
	{Slot: "dataset", Path: "datasets[]", Resolve: func(d ProjectDeclarations) string {
		return datasetList(d.Datasets)
	}},
	{Slot: "baselineData", Path: "datasets[].window", Resolve: func(d ProjectDeclarations) string {
		b, _ := datasetWindows(d)
		return b
	}},
	{Slot: "currentData", Path: "datasets[].window", Resolve: func(d ProjectDeclarations) string {
		_, c := datasetWindows(d)
		return c
	}},
	// A reconciliation's two sources are DASHBOARDS and only dashboards — a
	// dashboard carries the read path, which is the fact that makes "what feeds
	// dashboard A" answerable. The old fallback to `datasets[0]`/`datasets[1]`
	// is gone: two unrelated datasets are not two dashboards' sources, and
	// offering them as such is the same positional guess in another costume.
	{Slot: "sourceA", Path: "dashboards[0]", Resolve: func(d ProjectDeclarations) string {
		return dashboardAt(d.Dashboards, 0)
	}},
	{Slot: "sourceB", Path: "dashboards[1]", Resolve: func(d ProjectDeclarations) string {
		return dashboardAt(d.Dashboards, 1)
	}},
	// What these numbers must NOT be used for is a dataset fact when a dataset
	// declares one — and a PROJECT fact otherwise. A project whose purpose says
	// "never used to judge an individual" was stating exactly this slot's answer
	// and watching every run ask for it again, because only the dataset-level
	// field was wired up.
	{Slot: "safeUse", Path: "datasets[].safeUse", Resolve: func(d ProjectDeclarations) string {
		project := strings.TrimSpace(d.Purpose.Constraints)
		data := firstNonEmpty(d.Datasets, func(ds DatasetDecl) string { return ds.SafeUse })
		var lines []string
		if project != "" {
			lines = append(lines, "From this project's constraints: "+project)
		}
		if data != "" {
			lines = append(lines, "From the data's safe use: "+data)
		}
		return strings.Join(lines, "\n")
	}, PathFor: func(d ProjectDeclarations) string {
		hasProject := strings.TrimSpace(d.Purpose.Constraints) != ""
		hasData := firstNonEmpty(d.Datasets, func(ds DatasetDecl) string { return ds.SafeUse }) != ""
		if hasProject && hasData {
			return "datasets[].safeUse + purpose.constraints"
		}
		if hasData {
			return "datasets[].safeUse"
		}
		return "purpose.constraints"
	}},
	// `grain` is the AUDITED DATASET's grain here and the finished table's grain
	// in data-pipeline and pipeline-change. A dataset's grain answers the first
	// and never the second, so the rule is scoped to the recipe where the two
	// questions are the same question. `sharedGrain` — what one row means once
	// BOTH sides are rolled up — is answered by neither dataset and inherits
	// nothing at all.
	{Slot: "grain", Path: "datasets[0].grain", Recipes: []string{"data-quality-audit"},
		Resolve: func(d ProjectDeclarations) string {
			return firstNonEmpty(d.Datasets, func(ds DatasetDecl) string { return ds.Grain })
		}},
	{Slot: "keyColumns", Path: "datasets[0].keyColumns", Recipes: []string{"data-quality-audit", "dataset-shift-audit"}, Resolve: func(d ProjectDeclarations) string {
		for _, ds := range d.Datasets {
			if v := DatasetKeyValue(ds); v != "" {
				return v
			}
		}
		return ""
	}},
	{Slot: "environment", Path: "environment.where", Resolve: func(d ProjectDeclarations) string {
		return strings.TrimSpace(d.Environment.Where)
	}},
	{Slot: "runtimeBudget", Path: "environment.runtimeBudgetDefault", Resolve: func(d ProjectDeclarations) string {
		return strings.TrimSpace(d.Environment.RuntimeBudgetDefault)
	}},

	// ── Table slots ─────────────────────────────────────────────────────────
	//
	// A table slot inherits its ROWS, in the canonical JSON the form edits. The
	// cell order below must match the slot's declared column order in
	// internal/seed.DefaultTemplates — TestPrefillMatchesSlotColumns holds the
	// two together, so a column renamed there fails the gate here.
	{Slot: "measures", Path: "kpis[]", Resolve: func(d ProjectDeclarations) string {
		return kpiMeasureRows(d.KPIs)
	}},
	{Slot: "tolerances", Path: "tolerances[]", Resolve: func(d ProjectDeclarations) string {
		return kpiToleranceRows(d)
	}},
	{Slot: "knownDifferences", Path: "exclusions[]", Resolve: func(d ProjectDeclarations) string {
		return exclusionRows(d.Exclusions)
	}},
	{Slot: "features", Path: "tolerances[]", Resolve: func(d ProjectDeclarations) string {
		return featureToleranceRows(d)
	}},
	{Slot: "successMetric", Path: "metrics[]", Resolve: func(d ProjectDeclarations) string {
		return metricRows(d)
	}},

	// ── Text slots the new groups can answer ────────────────────────────────
	{Slot: "period", Path: "calendar", Resolve: func(d ProjectDeclarations) string {
		return calendarSentence(d.Calendar)
	}},
	// The audit's beliefs, as TYPED LINES rather than prose. The arrival column
	// and the timezone are the two facts a quality audit names as its most
	// common evidence gaps, and both were typed on the project form and reached
	// no slot at all. The expected row count comes here rather than back into
	// the dataset textarea, where it was re-prosed into "~24083384 rows
	// expected" and dropped: as a line of its own it is a baseline the audit
	// reports the MEASURED count against.
	{Slot: "expectations", Path: "datasets[].arrivalTimestampColumn/timezone/piiColumns/expectedRowCount",
		Resolve: func(d ProjectDeclarations) string { return datasetBeliefs(d.Datasets) }},
	{Slot: "dataDescription", Path: "evalsets[]", Resolve: func(d ProjectDeclarations) string {
		return evalsetSentence(d)
	}},
	{Slot: "predictionTarget", Path: "labelAvailability", Resolve: func(d ProjectDeclarations) string {
		return labelAvailabilitySentence(d.LabelAvailability)
	}},
	{Slot: "constraints", Path: "servingConstraints", Resolve: func(d ProjectDeclarations) string {
		return servingConstraintsSentence(d.ServingConstraints)
	}},

	// ── The operational groups ──────────────────────────────────────────────
	//
	// Each of these was a prose box on a form and a question a composed plan
	// asked out loud with nowhere to write the answer.

	// The target store, as a reference rather than a description. "postgres,
	// schema analytics" said nothing about which credential reaches it or with
	// what rights; the connection is an ENV VAR NAME here and stays one all the
	// way to the node that runs.
	{Slot: "targetStore", Path: "warehouse", Resolve: func(d ProjectDeclarations) string {
		return warehouseRef(d.Warehouse)
	}},
	// When it must have landed, what starts it, and who hears about it when it
	// does not — one slot, because they are one operational fact, and because
	// leaving the trigger and the page undeclared is how "the platform will
	// take care of it" gets assumed. The sentence says out loud that this
	// platform schedules nothing.
	{Slot: "refreshDeadline", Path: "schedule", Resolve: func(d ProjectDeclarations) string {
		return scheduleSentence(d.Schedule)
	}},
	{Slot: "maxLag", Path: "schedule.maxLag", Resolve: func(d ProjectDeclarations) string {
		return strings.TrimSpace(d.Schedule.MaxLag)
	}},
	// How late a row may arrive and what it does to the period it belongs to —
	// per dataset, from the load each one declares. This half of `freshness`
	// used to be one clause inside a box that also carried the lag ceiling and
	// the deadline.
	{Slot: "lateArrival", Path: "datasets[].loadSemantics", Resolve: func(d ProjectDeclarations) string {
		return datasetLateArrival(d.Datasets)
	}},
	// The pipeline being changed, by reference: the files, the command that
	// runs them end to end, the manifest, and the convention a change lands
	// through.
	{Slot: "pipelineLocation", Path: "lineage", Resolve: func(d ProjectDeclarations) string {
		return lineageRef(d.Lineage)
	}},
	// Where a finished deliverable goes. Declared since round 2 and consumed by
	// nothing until now: no form, no plan ledger and no run page read it, so a
	// project that had said who the report was for watched every recipe write
	// one addressed to nobody.
	{Slot: "handover", Path: "delivery", Resolve: func(d ProjectDeclarations) string {
		return deliveryRef(d.Delivery)
	}},
}

// ── Row builders for the table slots ────────────────────────────────────────

// cell is one ordered key/value of a prefilled table row. Ordered, because the
// stored form is canonical JSON and the form compares the whole string to
// decide whether a value is still the project's answer.
type cell struct{ K, V string }

// tableJSON renders prefilled rows in the canonical form NormalizeSlotValue
// produces, so an inherited table compares equal to itself after a round trip.
// Empty cells are dropped and an empty table is "" — nothing to inherit.
func tableJSON(rows [][]cell) string {
	var b strings.Builder
	b.WriteByte('[')
	written := 0
	for _, row := range rows {
		var parts []string
		for _, c := range row {
			v := strings.Join(strings.Fields(strings.ReplaceAll(c.V, "|", "/")), " ")
			if v == "" {
				continue
			}
			parts = append(parts, jsonQuote(c.K)+":"+jsonQuote(v))
		}
		if len(parts) == 0 {
			continue
		}
		if written > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('{')
		b.WriteString(strings.Join(parts, ","))
		b.WriteByte('}')
		written++
	}
	b.WriteByte(']')
	if written == 0 {
		return ""
	}
	return b.String()
}

// kpiMeasureRows fills the reconciliation's `measures` table from the KPI
// dictionary: one row per KPI, with the two system definitions.
//
// WHICH definition lands in A and which in B is a GUESS — the systems are taken
// in declared order and each cell names the system it came from, so a person
// reading "Finance: sum(amount_usd) after refunds" can see at once whether the
// sides are the way round they meant. Same contract as the two-dataset slots:
// a starting point to correct, never an assertion.
func kpiMeasureRows(kpis []KPIDecl) string {
	var rows [][]cell
	for _, k := range kpis {
		name := strings.TrimSpace(k.Name)
		if name == "" {
			name = strings.TrimSpace(k.ID)
		}
		if name == "" {
			continue
		}
		defs := definitionsInOrder(k.Definitions)
		row := []cell{{"measure", name}}
		if len(defs) > 0 {
			row = append(row, cell{"definitionA", defs[0]})
		}
		if len(defs) > 1 {
			row = append(row, cell{"definitionB", defs[1]})
		}
		rows = append(rows, row)
	}
	return tableJSON(rows)
}

// definitionsInOrder renders "system: definition" per system, systems sorted so
// the same dictionary always prefills the same way.
func definitionsInOrder(defs map[string]string) []string {
	systems := make([]string, 0, len(defs))
	for s := range defs {
		if strings.TrimSpace(defs[s]) != "" {
			systems = append(systems, s)
		}
	}
	sort.Strings(systems)
	out := make([]string, 0, len(systems))
	for _, s := range systems {
		out = append(out, s+": "+strings.TrimSpace(defs[s]))
	}
	return out
}

// kpiToleranceRows fills the reconciliation's `tolerances` table from the
// standing tolerances whose subject is a declared KPI.
func kpiToleranceRows(d ProjectDeclarations) string {
	var rows [][]cell
	for _, t := range d.Tolerances {
		if !subjectIsKPI(d, t.Subject) {
			continue
		}
		rows = append(rows, []cell{
			{"measure", kpiDisplayName(d, t.Subject)},
			{"unit", strings.TrimSpace(t.Unit)},
			{"value", fmtNum(t.Value)},
			{"segmentValue", fmtNum(t.SegmentValue)},
			{"setBy", setByLabel(t)},
		})
	}
	return tableJSON(rows)
}

// kpiDisplayName is the ONE name a measure is called by, everywhere it is
// prefilled: the KPI's declared `name`, or its id when it has no name.
//
// A tolerance's subject may be written as either — they are matched case- and
// separator-insensitively — and the prefilled tables used to carry whichever
// spelling each row happened to use, so the Measures table said "net revenue"
// while the Tolerance table said "net_revenue" under a column hinted "One of
// the measures above". Two names for one measure is exactly the vocabulary
// difference these recipes exist to catch.
func kpiDisplayName(d ProjectDeclarations, subject string) string {
	want := toleranceKey(subject)
	if want == "" {
		return strings.TrimSpace(subject)
	}
	for _, k := range d.KPIs {
		if toleranceKey(k.ID) != want && toleranceKey(k.Name) != want {
			continue
		}
		if name := strings.TrimSpace(k.Name); name != "" {
			return name
		}
		if id := strings.TrimSpace(k.ID); id != "" {
			return id
		}
	}
	return strings.TrimSpace(subject)
}

// featureToleranceRows fills the shift audit's `features` table from the
// standing tolerances that are NOT about a KPI — which is what a dataset
// feature's tolerance is.
//
// `type` and `whyItMatters` are the slot's REQUIRED cells, and they used to
// arrive blank because the tolerance registry had nowhere to declare them: the
// form then counted the row as filled and the recipe's own gate refused it
// later. They are declared now (ToleranceDecl.Type / .Why) and carried here.
// Still nothing is invented — an undeclared type stays an empty cell, which is
// what the readiness check now counts.
func featureToleranceRows(d ProjectDeclarations) string {
	var rows [][]cell
	for _, t := range d.Tolerances {
		if subjectIsKPI(d, t.Subject) || strings.TrimSpace(t.Subject) == "" {
			continue
		}
		rows = append(rows, []cell{
			{"feature", strings.TrimSpace(t.Subject)},
			{"type", strings.ToLower(strings.TrimSpace(t.Type))},
			{"whyItMatters", strings.TrimSpace(t.Why)},
			{"severity", strings.TrimSpace(t.Severity)},
			{"statistic", strings.TrimSpace(t.Statistic)},
			{"tolerance", fmtNum(t.Value)},
			{"unit", strings.TrimSpace(t.Unit)},
			{"watchBand", fmtNum(t.WatchBand)},
			{"owner", strings.TrimSpace(t.Owner)},
		})
	}
	return tableJSON(rows)
}

// metricRows fills ml-engineering's `successMetric` table from the declared
// metrics. The FIRST declared metric is the primary one, and the carried text
// of this table is what the platform's eval-gate parses the declared metric
// name and threshold out of — so metric and threshold lead every row.
func metricRows(d ProjectDeclarations) string {
	floor := worstSliceFloor(d.Slices)
	var rows [][]cell
	for _, m := range d.Metrics {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			name = strings.TrimSpace(m.ID)
		}
		if name == "" {
			continue
		}
		rows = append(rows, []cell{
			{"metric", name},
			{"threshold", strings.TrimSpace(m.Threshold)},
			{"operatingPoint", strings.TrimSpace(m.OperatingPoint)},
			{"uncertaintyMethod", strings.TrimSpace(m.UncertaintyMethod)},
			{"worstSliceFloor", floor},
		})
	}
	return tableJSON(rows)
}

// worstSliceFloor renders the tightest declared slice floor, naming its column
// — "0.45 on region" says more than "0.45" and is what the model card has to
// report against.
func worstSliceFloor(slices []SliceDecl) string {
	best := ""
	var bestVal float64
	for _, s := range slices {
		if s.Floor == nil || strings.TrimSpace(s.Column) == "" {
			continue
		}
		if best == "" || *s.Floor > bestVal {
			best = fmtNum(s.Floor) + " on " + strings.TrimSpace(s.Column)
			bestVal = *s.Floor
		}
	}
	return best
}

// exclusionRows fills the reconciliation's `knownDifferences` table.
func exclusionRows(list []ExclusionDecl) string {
	var rows [][]cell
	for _, e := range list {
		rows = append(rows, []cell{
			{"name", strings.TrimSpace(e.Name)},
			{"side", strings.TrimSpace(e.Side)},
			{"filter", strings.TrimSpace(e.Filter)},
			{"owner", strings.TrimSpace(e.Owner)},
		})
	}
	return tableJSON(rows)
}

// subjectIsKPI reports whether a tolerance's subject names a declared KPI. It
// is what routes one tolerance registry to two slots: a KPI's tolerance
// prefills a reconciliation, everything else prefills a shift audit's features.
func subjectIsKPI(d ProjectDeclarations, subject string) bool {
	want := toleranceKey(subject)
	if want == "" {
		return false
	}
	for _, k := range d.KPIs {
		if toleranceKey(k.ID) == want || toleranceKey(k.Name) == want {
			return true
		}
	}
	return false
}

// setByLabel is "who set this, and when" as one cell. A tolerance with neither
// is still a tolerance; it is just one nobody can be asked about.
func setByLabel(t ToleranceDecl) string {
	owner := strings.TrimSpace(t.Owner)
	at := strings.TrimSpace(t.SetAt)
	switch {
	case owner != "" && at != "":
		return owner + ", " + at
	case owner != "":
		return owner
	default:
		return at
	}
}

// calendarSentence renders the reporting calendar as the one line the `period`
// slot takes. Only the declared parts appear — an undeclared timezone stays
// undeclared rather than becoming UTC by omission.
func calendarSentence(c CalendarDecl) string {
	var parts []string
	if v := strings.TrimSpace(c.QuarterBoundaries); v != "" {
		parts = append(parts, v)
	}
	if v := strings.TrimSpace(c.FiscalYearStart); v != "" {
		parts = append(parts, "fiscal year starts "+v)
	}
	if v := strings.TrimSpace(c.Timezone); v != "" {
		parts = append(parts, "times in "+v)
	}
	if v := strings.TrimSpace(c.SnapshotDay); v != "" {
		parts = append(parts, "snapshot day "+v)
	}
	if v := strings.TrimSpace(c.LateArrivalPolicy); v != "" {
		parts = append(parts, "late arrivals: "+v)
	}
	return strings.Join(parts, "; ")
}

// evalsetSentence renders the declared evalsets as the "data available" line,
// each with the two facts that pin BYTES rather than a belief: the content hash
// and when it was frozen. The custody rule rides along, because a holdout whose
// custody nobody states is a holdout in name.
func evalsetSentence(d ProjectDeclarations) string {
	var parts []string
	for _, e := range d.Evalsets {
		name := strings.TrimSpace(e.ID)
		if name == "" {
			name = strings.TrimSpace(e.Kind)
		}
		if name == "" {
			continue
		}
		seg := name
		if k := strings.TrimSpace(e.Kind); k != "" {
			seg += " (" + k + ")"
		}
		if loc := strings.TrimSpace(e.Location); loc != "" {
			seg += ": " + loc
		}
		var extra []string
		if e.RowCount != nil {
			extra = append(extra, fmt.Sprintf("%d rows", *e.RowCount))
		}
		if v := strings.TrimSpace(e.DateRange); v != "" {
			extra = append(extra, v)
		}
		if v := strings.TrimSpace(e.LabelColumn); v != "" {
			extra = append(extra, "label "+v)
		}
		if v := strings.TrimSpace(e.SHA256); v != "" {
			extra = append(extra, "sha256 "+v)
		}
		if v := strings.TrimSpace(e.FrozenAt); v != "" {
			extra = append(extra, "frozen "+v)
		}
		if len(extra) > 0 {
			seg += " — " + strings.Join(extra, ", ")
		}
		parts = append(parts, seg)
	}
	if len(parts) == 0 {
		return ""
	}
	line := strings.Join(parts, "; ")
	if holder := strings.TrimSpace(d.HoldoutCustody.Holder); holder != "" {
		rule := strings.TrimSpace(d.HoldoutCustody.Rule)
		if rule == "" {
			rule = HoldoutCustodyRule
		}
		line += ". Holdout custody: " + holder + " — " + rule + "."
	}
	return line
}

func labelAvailabilitySentence(l LabelAvailabilityDecl) string {
	event, lag := strings.TrimSpace(l.Event), strings.TrimSpace(l.Lag)
	if event == "" && lag == "" {
		return ""
	}
	if event == "" {
		event = "not declared"
	}
	if lag == "" {
		lag = "an undeclared time"
	}
	return "Label becomes knowable: " + event + ", " + lag + " after the prediction"
}

func servingConstraintsSentence(s ServingConstraintsDecl) string {
	var parts []string
	for _, item := range []struct{ label, value string }{
		{"latency", s.Latency}, {"artifact size", s.ArtifactSize},
		{"residency", s.Residency}, {"cost", s.Cost},
	} {
		if v := strings.TrimSpace(item.value); v != "" {
			parts = append(parts, item.label+": "+v)
		}
	}
	return strings.Join(parts, "; ")
}

// fmtNum renders an optional declared number without inventing one. A nil
// pointer is an empty cell, which is what "not declared" has to look like.
func fmtNum(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

// dashboardRef is one dashboard as a slot value: what it is called, the thing
// behind it, and how that thing is read — the three facts a reconciliation
// needs before it can point a query anywhere.
func dashboardRef(db DashboardDecl) string {
	name := strings.TrimSpace(db.Name)
	if name == "" {
		name = strings.TrimSpace(db.ID)
	}
	src := strings.TrimSpace(db.Source)
	switch {
	case name != "" && src != "":
		name += " (" + src + ")"
	case name == "" && src != "":
		name = src
	case name == "":
		return ""
	}
	var extra []string
	if v := strings.TrimSpace(db.ReadPath); v != "" {
		extra = append(extra, "read via "+v)
	}
	if v := strings.TrimSpace(db.AsOfConvention); v != "" {
		extra = append(extra, "as-of "+v)
	}
	if v := strings.TrimSpace(db.RefreshCadence); v != "" {
		extra = append(extra, "refreshed "+v)
	}
	if v := strings.TrimSpace(db.Owner); v != "" {
		extra = append(extra, "owned by "+v)
	}
	if len(extra) > 0 {
		name += " — " + strings.Join(extra, ", ")
	}
	return name
}

func dashboardAt(list []DashboardDecl, i int) string {
	if i < 0 || i >= len(list) {
		return ""
	}
	return dashboardRef(list[i])
}

// NoSchedulerHere is the sentence every inherited schedule carries.
//
// It is not a caveat about a feature that is coming. This platform has no
// scheduler and is not going to grow one by being asked politely: a run starts
// when a person starts it or when something outside starts it. A composed plan
// already says this out loud ("do not claim scheduled execution because this
// platform has no scheduler"); the declaration that answers it has to say the
// same thing, or a filled-in cadence reads as a promise the platform made.
const NoSchedulerHere = "Hyperplane does not schedule this — a run starts when a person or an external system starts it"

// warehouseRef renders the declared warehouse as the `targetStore` slot's value:
// the engine and schema, the environment variable the connection is read from,
// and the role the run acts as.
//
// "postgres analytics (connection from $ANALYTICS_DSN, write role
// $ANALYTICS_WRITER_ROLE)" — three references and no secret, which is the whole
// difference between this and the prose it replaces.
func warehouseRef(w WarehouseDecl) string {
	head := strings.TrimSpace(strings.TrimSpace(w.Engine) + " " + strings.TrimSpace(w.DatabaseSchema))
	var extra []string
	if ref := strings.TrimSpace(w.ConnectionRef); ref != "" {
		extra = append(extra, "connection from "+envRef(ref))
	}
	access := strings.ToLower(strings.TrimSpace(w.Access))
	read, write := strings.TrimSpace(w.RoleRefs.Read), strings.TrimSpace(w.RoleRefs.Write)
	switch {
	case access == WarehouseAccessWrite && write != "":
		// The role already says the access. "write role $X, write access" is
		// the same fact twice, and a slot value read aloud in a review does not
		// need the platform saying it twice.
		extra = append(extra, "write role "+envRef(write))
	case access == WarehouseAccessRead && read != "":
		extra = append(extra, "read role "+envRef(read))
	case access != "":
		extra = append(extra, access+" access, with no role declared for it")
	default:
		// No access declared: name every role there is rather than choosing
		// one, because choosing one would be asserting the rights of the run.
		if read != "" {
			extra = append(extra, "read role "+envRef(read))
		}
		if write != "" {
			extra = append(extra, "write role "+envRef(write))
		}
	}
	switch {
	case head != "" && len(extra) > 0:
		return head + " (" + strings.Join(extra, ", ") + ")"
	case head != "":
		return head
	case len(extra) > 0:
		return strings.Join(extra, ", ")
	}
	return ""
}

// envRef prints a variable name the way a person reads one. The `$` is display
// only — what is stored and validated is the bare name.
func envRef(name string) string {
	if strings.HasPrefix(name, "$") {
		return name
	}
	return "$" + name
}

// scheduleSentence renders the declared schedule as the `refreshDeadline`
// slot's value: when it must have landed, how often it is expected, what starts
// it, and who to page when it does not — followed by NoSchedulerHere.
//
// Four facts in one slot because they are one operational answer, and because
// splitting them would let three of the four be left blank while the form still
// read as ready. A schedule that declares nothing renders nothing.
func scheduleSentence(s ScheduleDecl) string {
	var parts []string
	if v := strings.TrimSpace(s.Deadline); v != "" {
		if tz := strings.TrimSpace(s.Timezone); tz != "" {
			v += " " + tz
		}
		parts = append(parts, "must have landed by "+v)
	}
	if v := strings.TrimSpace(s.ExpectedCadence); v != "" {
		parts = append(parts, "expected "+v)
	}
	if v := strings.TrimSpace(s.TriggeredBy); v != "" {
		parts = append(parts, "started by "+v)
	}
	if v := strings.TrimSpace(s.PageRef); v != "" {
		parts = append(parts, "page "+v+" when it does not land")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ") + ". " + NoSchedulerHere + "."
}

// datasetLateArrival renders the late-arrival half of every dataset's declared
// load, one line each: how late a row may still be, and what a late row does to
// the period it belongs to.
//
// The other half of a load — full or incremental, and the column an incremental
// one advances on — rides on the dataset reference itself (see datasetRef), so
// neither half is declared into a slot nobody reads.
func datasetLateArrival(list []DatasetDecl) string {
	var lines []string
	for i, ds := range list {
		ls := ds.LoadSemantics
		if ls == nil {
			continue
		}
		var facts []string
		if v := strings.TrimSpace(ls.LateArrivalWindow); v != "" {
			if strings.HasPrefix(strings.ToLower(v), "up to ") {
				facts = append(facts, "rows may arrive "+v+" late")
			} else {
				facts = append(facts, "rows may arrive up to "+v+" late")
			}
		}
		if v := strings.TrimSpace(ls.RestatementRule); v != "" {
			facts = append(facts, "a late row: "+v)
		}
		if len(facts) == 0 {
			continue
		}
		lines = append(lines, datasetLabel(ds, i)+" — "+strings.Join(facts, "; "))
	}
	return strings.Join(lines, "\n")
}

// lineageRef renders the declared lineage as the `pipelineLocation` slot's
// value: the files, the command that runs them end to end, the manifest, and
// the convention a change lands through. References only.
func lineageRef(l LineageDecl) string {
	head := strings.TrimSpace(l.RepoPath)
	var extra []string
	if v := strings.TrimSpace(l.RunCommand); v != "" {
		extra = append(extra, "run end to end with "+v)
	}
	if v := strings.TrimSpace(l.ManifestPath); v != "" {
		extra = append(extra, "manifest "+v)
	}
	if v := strings.TrimSpace(l.PRConvention); v != "" {
		extra = append(extra, "changes land as "+v)
	}
	switch {
	case head != "" && len(extra) > 0:
		return head + " — " + strings.Join(extra, "; ")
	case head != "":
		return head
	}
	return strings.Join(extra, "; ")
}

// deliveryRef renders the declared hand-over as the `handover` slot's value:
// the format, and the recipient REFERENCE it is addressed to.
//
// "a one-page markdown summary plus the CSV, to #finance-analytics". Never an
// address list — the recipient is a handle the owner's own tools resolve, which
// is what keeps this platform out of the business of holding contact details.
func deliveryRef(d DeliveryDecl) string {
	format, to := strings.TrimSpace(d.Format), strings.TrimSpace(d.RecipientRef)
	switch {
	case format != "" && to != "":
		return format + ", to " + to
	case format != "":
		return format
	case to != "":
		return "to " + to
	}
	return ""
}

// SignOffRef names who settles whether a delivery is accepted, from the
// declared stakeholders — the role first, because "Sofia" alone does not say
// what Sofia does.
//
// A stakeholder whose role mentions approving, signing off, deciding or owning
// is the one; failing that, the project's own declared owner; failing that,
// nobody, and the surfaces that read this say so by showing nothing rather than
// by inventing a name.
func SignOffRef(d ProjectDeclarations) string {
	for _, s := range d.Stakeholders {
		role := strings.ToLower(strings.TrimSpace(s.Role))
		if role == "" {
			continue
		}
		if !strings.Contains(role, "approv") && !strings.Contains(role, "sign") &&
			!strings.Contains(role, "decid") && !strings.Contains(role, "own") &&
			!strings.Contains(role, "accept") {
			continue
		}
		if name := stakeholderName(s); name != "" {
			return name + " (" + strings.TrimSpace(s.Role) + ")"
		}
	}
	if owner := strings.TrimSpace(d.Purpose.Owner); owner != "" {
		return owner + " (the project's owner — no approver is declared)"
	}
	for _, s := range d.Stakeholders {
		if name := stakeholderName(s); name != "" {
			return name + " (no approver is declared; first stakeholder)"
		}
	}
	return ""
}

// stakeholderName is what to call a declared stakeholder: their name, else the
// reference that stands in for one.
func stakeholderName(s StakeholderDecl) string {
	if v := strings.TrimSpace(s.Name); v != "" {
		return v
	}
	return strings.TrimSpace(s.ContactRef)
}

// RegressionPolicyStamp renders a project's regression policy as the three
// REFERENCES a learned-skill card can show: the evalset a change must re-score
// against, the margin it has to win by, and whose policy it is.
//
// D-2: the policy was declarable for a whole round and read by no surface, so
// the Skill Lab said "skipped: no evalset yet" about a project that had named
// one. This is deliberately only half the fix — nothing re-scores a draft
// against that evalset yet — and the card that renders it says so in the same
// sentence. A reference somebody can see is what makes the missing scoring
// visible; a reference nobody can see is what made it invisible.
//
// A project that declares neither half stamps nothing: "no policy" and "a
// policy nobody rendered" are different facts about different projects.
func RegressionPolicyStamp(d ProjectDeclarations, project string) map[string]string {
	evalset := strings.TrimSpace(d.RegressionPolicy.EvalsetID)
	margin := strings.TrimSpace(d.RegressionPolicy.MinMargin)
	if evalset == "" && margin == "" {
		return nil
	}
	out := map[string]string{}
	if p := strings.TrimSpace(project); p != "" {
		out["project"] = p
	}
	if evalset != "" {
		out["evalsetId"] = evalset
	}
	if margin != "" {
		out["minMargin"] = margin
	}
	return out
}

// DatasetKeyValue renders a dataset's key declaration in the exact form the
// typed `keyColumns` slot takes: a comma-separated list, or the literal `none`
// followed by the closest candidate columns.
//
// `none,<candidates>` is not a missing value. It is the owner declaring that
// this data has no natural key and naming what comes closest, which the audit
// then reports on rather than assuming.
func DatasetKeyValue(ds DatasetDecl) string {
	if len(ds.KeyColumns) > 0 {
		return strings.Join(trimAll(ds.KeyColumns), ", ")
	}
	if strings.EqualFold(strings.TrimSpace(ds.NaturalKey), "none") {
		parts := append([]string{"none"}, trimAll(ds.KeyCandidate)...)
		return strings.Join(parts, ", ")
	}
	return ""
}

// PrefillSlots resolves every slot the project can supply a value for. Slots
// with nothing to inherit are absent from the result rather than present and
// empty, so a caller can treat "in the map" as "the project declared this".
// A rule scoped to a recipe is keyed by RECIPE AND SLOT, so one flat map can
// carry both the answers that mean the same thing everywhere and the ones that
// only mean something on one recipe. Every reader looks up the scoped key first
// and falls back to the plain one, which is what lets `grain` be the audited
// dataset's grain in data-quality-audit and nothing at all in data-pipeline
// without either surface holding a second copy of the mapping.
func PrefillSlots(d ProjectDeclarations) map[string]SlotPrefill {
	out := PrefillSlotsFor(d, "")
	for _, recipe := range ScopedRecipes() {
		for slot, p := range PrefillSlotsFor(d, recipe) {
			if _, unscoped := out[slot]; unscoped {
				continue
			}
			out[ScopedSlotKey(recipe, slot)] = p
		}
	}
	return out
}

// ScopedSlotKey is how a recipe-scoped prefill is keyed in a prefill map. The
// separator cannot appear in a slot id, so a scoped key never collides with a
// plain one.
func ScopedSlotKey(recipe, slot string) string { return recipe + "/" + slot }

// LookupPrefill resolves a slot for a named recipe: the recipe's own answer
// when it has one, the shared answer otherwise. One function, so the form, the
// compose ledger and the tests cannot disagree about which answer wins.
func LookupPrefill(prefill map[string]SlotPrefill, recipe, slot string) (SlotPrefill, bool) {
	if recipe != "" {
		if p, ok := prefill[ScopedSlotKey(recipe, slot)]; ok {
			return p, true
		}
	}
	p, ok := prefill[slot]
	return p, ok
}

// PrefillSlotsFor is PrefillSlots for ONE recipe: the unscoped rules plus the
// rules scoped to that recipe. The empty recipe name asks for the unscoped set
// alone, which is what compose and the project page read — a rule that only
// makes sense on one recipe must not answer a question asked by another.
func PrefillSlotsFor(d ProjectDeclarations, recipe string) map[string]SlotPrefill {
	out := map[string]SlotPrefill{}
	for _, rule := range slotInheritance {
		if !rule.appliesTo(recipe) {
			continue
		}
		if v := strings.TrimSpace(rule.Resolve(d)); v != "" {
			source := rule.Path
			if rule.PathFor != nil {
				if p := strings.TrimSpace(rule.PathFor(d)); p != "" {
					source = p
				}
			}
			out[rule.Slot] = SlotPrefill{Value: v, Source: source}
		}
	}
	return out
}

// ScopedRecipes lists the recipes that have at least one rule of their own, so
// a caller can build the per-recipe prefill maps without knowing the table.
func ScopedRecipes() []string {
	seen := map[string]bool{}
	var out []string
	for _, rule := range slotInheritance {
		for _, name := range rule.Recipes {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// InheritableSlots lists the slot ids that can be inherited, in table order.
// Exported so a test can assert the table covers the slots the recipes declare.
func InheritableSlots() []string {
	out := make([]string, 0, len(slotInheritance))
	for _, r := range slotInheritance {
		out = append(out, r.Slot)
	}
	return out
}

// datasetRef is one dataset as a slot value.
//
// THE LOCATION LEADS, BARE. It used to be wrapped — `Name (data/trips.csv) —
// csv` — and every data recipe's inputs gate tokenises its declared data with
// `for tok in $HYPERPLANE_INPUT_…; case "$tok" in *.csv|…`, which cannot match
// `(data/trips.csv)`. So the project's OWN inherited default silently demoted
// the header check to "presence only": a key naming a column that does not
// exist passed against the inherited value and failed against the same files
// typed as bare paths. Leading with the unwrapped location makes the path the
// first whitespace-delimited token, which is the form the gate can read.
func datasetRef(ds DatasetDecl) string {
	name := strings.TrimSpace(ds.Name)
	loc := strings.TrimSpace(ds.Location)
	var head string
	switch {
	case loc != "" && name != "":
		head = loc + " — " + name
	case loc != "":
		head = loc
	case name != "":
		head = name
	default:
		return ""
	}
	// The row count is deliberately NOT here: it is a typed baseline the audit
	// reports a measured count against (see datasetBeliefs), not an adjective.
	var extra []string
	if f := strings.TrimSpace(ds.Format); f != "" {
		extra = append(extra, f)
	}
	if k := strings.ToLower(strings.TrimSpace(ds.Kind)); k != "" {
		extra = append(extra, k)
	}
	if v := strings.TrimSpace(ds.ArrivalTimestampColumn); v != "" {
		extra = append(extra, "arrival "+v)
	}
	if v := strings.TrimSpace(ds.Timezone); v != "" {
		extra = append(extra, "times in "+v)
	}
	// How it is loaded rides with the reference to it, so full-versus-
	// incremental stops being something a pipeline plan decides for itself.
	if ls := ds.LoadSemantics; ls != nil {
		if mode := strings.ToLower(strings.TrimSpace(ls.Mode)); mode != "" {
			if col := strings.TrimSpace(ls.WatermarkColumn); col != "" {
				extra = append(extra, mode+" load on "+col)
			} else {
				extra = append(extra, mode+" load")
			}
		}
	}
	if cols := trimAll(ds.PIIColumns); len(cols) > 0 {
		extra = append(extra, "sensitive: "+strings.Join(cols, ", "))
	}
	if len(extra) > 0 {
		head += " (" + strings.Join(extra, ", ") + ")"
	}
	return head
}

// datasetBeliefs renders the typed facts a quality audit is meant to turn into
// evidence, one line each. Only declared facts appear: an undeclared timezone
// stays undeclared rather than becoming UTC by omission.
func datasetBeliefs(list []DatasetDecl) string {
	var lines []string
	for _, ds := range list {
		who := strings.TrimSpace(ds.Name)
		if who == "" {
			who = strings.TrimSpace(ds.Location)
		}
		if who == "" {
			who = strings.TrimSpace(ds.ID)
		}
		if who == "" {
			continue
		}
		var facts []string
		if v := strings.TrimSpace(ds.ArrivalTimestampColumn); v != "" {
			facts = append(facts, "arrival column: "+v+" — freshness and late arrivals are measured against it")
		}
		if v := strings.TrimSpace(ds.Timezone); v != "" {
			facts = append(facts, "timezone: "+v+" — every date boundary in this audit is cut in it")
		}
		if cols := trimAll(ds.PIIColumns); len(cols) > 0 {
			facts = append(facts, "sensitive columns: "+strings.Join(cols, ", "))
		}
		if ds.ExpectedRowCount != nil {
			facts = append(facts, fmt.Sprintf(
				"expected rows: %s — the audit reports the measured count against it", groupDigits(*ds.ExpectedRowCount)))
		}
		if len(facts) == 0 {
			continue
		}
		for _, f := range facts {
			lines = append(lines, who+" — "+f)
		}
	}
	return strings.Join(lines, "\n")
}

// groupDigits renders a row count the way a person writes one, because
// 24083384 and 24,083,384 are the same number and only one of them is read.
func groupDigits(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// datasetWindows resolves the baseline and current windows of a shift audit
// from DECLARED ROLES, and returns "" for a window the declarations do not
// settle rather than a guess.
//
// The order of preference is the order a data engineer would answer in:
//
//  1. One dataset declared `window: both` (or carrying both periods) IS both
//     windows — a shift audit compares two periods of the SAME data, so the
//     same dataset with two periods is the ordinary answer and two different
//     datasets is the unusual one.
//  2. One declared `baseline` and one declared `current`.
//  3. Exactly one window candidate and nothing else to compare it with: it
//     fills the baseline, and the current window is left for the person, who is
//     the only one who knows which period is under test.
//
// Anything else — several candidates, no roles — fills the BASELINE only. That
// is finding D-16a/M-16: two different-role slots must never be filled from two
// positional datasets of different kinds.
func datasetWindows(d ProjectDeclarations) (baseline, current string) {
	var candidates []DatasetDecl
	for _, ds := range d.Datasets {
		if datasetRef(ds) != "" && IsWindowCandidate(ds) {
			candidates = append(candidates, ds)
		}
	}
	if len(candidates) == 0 {
		return "", ""
	}
	window := func(ds DatasetDecl) string {
		return strings.ToLower(strings.TrimSpace(ds.Window))
	}
	carriesBoth := func(ds DatasetDecl) bool {
		return window(ds) == DatasetWindowBoth ||
			(strings.TrimSpace(ds.BaselinePeriod) != "" && strings.TrimSpace(ds.CurrentPeriod) != "")
	}
	for _, ds := range candidates {
		if !carriesBoth(ds) {
			continue
		}
		return withPeriod(datasetRef(ds), ds.BaselinePeriod), withPeriod(datasetRef(ds), ds.CurrentPeriod)
	}
	var base, cur *DatasetDecl
	for i := range candidates {
		switch window(candidates[i]) {
		case DatasetWindowBaseline:
			if base == nil {
				base = &candidates[i]
			}
		case DatasetWindowCurrent:
			if cur == nil {
				cur = &candidates[i]
			}
		}
	}
	if base != nil || cur != nil {
		if base != nil {
			baseline = withPeriod(datasetRef(*base), base.BaselinePeriod)
		}
		if cur != nil {
			current = withPeriod(datasetRef(*cur), cur.CurrentPeriod)
		}
		return baseline, current
	}
	return withPeriod(datasetRef(candidates[0]), candidates[0].BaselinePeriod), ""
}

// withPeriod appends a declared period to a window value. An undeclared period
// is left off entirely — the window then says where the data is and not when,
// which is true, where a defaulted period would not be.
func withPeriod(ref, period string) string {
	if ref == "" {
		return ""
	}
	if p := strings.TrimSpace(period); p != "" {
		return ref + " — " + p
	}
	return ref
}

// UnfilledWindowNote is what the current-window slot carries when the project's
// datasets cannot say which period is under test. It is the sentence that
// replaced a guess.
const UnfilledWindowNote = "pick the window from the project's data — a shift audit compares two periods of the SAME dataset, " +
	"and nothing declared says which period is under test. Declare `window: both` with a baselinePeriod and a currentPeriod " +
	"on the project to have both windows inherit."

// UnfilledSourceNote is the same sentence for a reconciliation's second source.
const UnfilledSourceNote = "name the dashboard this side reads — the project's datasets are not two dashboards' sources, " +
	"and offering two of them as one would be a guess about which figure came from where."

// slotNotes resolves the notes for the slots a project could not answer without
// guessing. A slot that inherited a real value never has one.
func slotNotes(d ProjectDeclarations) []SlotNote {
	var out []SlotNote
	baseline, current := datasetWindows(d)
	if baseline != "" && current == "" {
		out = append(out, SlotNote{Slot: "currentData", Note: UnfilledWindowNote})
	}
	if len(d.Dashboards) < 2 && len(d.Datasets) > 0 {
		out = append(out, SlotNote{Slot: "sourceB", Note: UnfilledSourceNote})
		if len(d.Dashboards) == 0 {
			out = append(out, SlotNote{Slot: "sourceA", Note: UnfilledSourceNote})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}

// SlotNotes is what a project says about the slots it deliberately leaves
// empty, keyed by slot id. Exported for the project detail payload.
func SlotNotes(d ProjectDeclarations) map[string]string {
	notes := slotNotes(d)
	if len(notes) == 0 {
		return nil
	}
	out := make(map[string]string, len(notes))
	for _, n := range notes {
		out[n.Slot] = n.Note
	}
	return out
}

func datasetList(list []DatasetDecl) string {
	var refs []string
	for _, ds := range list {
		if r := datasetRef(ds); r != "" {
			refs = append(refs, r)
		}
	}
	return strings.Join(refs, "; ")
}

func firstNonEmpty(list []DatasetDecl, get func(DatasetDecl) string) string {
	for _, ds := range list {
		if v := strings.TrimSpace(get(ds)); v != "" {
			return v
		}
	}
	return ""
}

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
