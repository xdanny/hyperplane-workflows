package store

// declarations_groups.go — the declaration groups the first round left for this
// pass: what an ANALYST declares once (KPIs, dashboards, calendar, standing
// tolerances, standing exclusions, stakeholders, delivery) and what an ML
// project declares once (evalsets, holdout custody, metrics, slices, label
// availability, serving constraints, regression policy).
//
// The extensibility rule from declarations.go holds unchanged and is the reason
// no migration is needed here: `projects.declarations` is a document of typed
// GROUPS, every reader takes the groups it knows, and MergeDeclarations leaves
// the rest byte-for-byte alone. Adding a group is adding a struct and a form
// section — never a column.
//
// EVERY GROUP IS OPTIONAL. A project that is a name and a sentence is still a
// complete project: a non-engineer must never meet a wall of ML fields on the
// way to declaring which dashboard they care about.
//
// REFERENCES ONLY, again. `contactRef` is a name or a local handle, `link` is a
// URL to a dashboard, `location` is a path or a table. A credential belongs
// nowhere in this document — the same rule connector `credentialsRef` follows.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// KPIDecl is one measure the project reports on, defined once PER SYSTEM that
// computes it.
//
// `Definitions` is the whole point. "Net revenue" is not one number, it is one
// word over two definitions, and a reconciliation that carries only one of them
// reports a vocabulary difference as a gap in the data. The map key is the
// system ("Finance", "CRM", "warehouse"); the value is what that system means
// by the measure.
type KPIDecl struct {
	ID          string            `json:"id,omitempty"`
	Name        string            `json:"name,omitempty"`
	Definitions map[string]string `json:"definitions,omitempty"`
	Unit        string            `json:"unit,omitempty"`
	Currency    string            `json:"currency,omitempty"`
	Owner       string            `json:"owner,omitempty"`
}

// DashboardDecl is one dashboard AND the thing a query can actually be pointed
// at behind it. "The finance dashboard" is not a source; `source` + `readPath`
// are, which is what makes "what feeds dashboard A" answerable from a pick-list
// instead of from memory.
type DashboardDecl struct {
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	Owner          string `json:"owner,omitempty"`
	Source         string `json:"source,omitempty"`
	ReadPath       string `json:"readPath,omitempty"`
	RefreshCadence string `json:"refreshCadence,omitempty"`
	AsOfConvention string `json:"asOfConvention,omitempty"`
	Link           string `json:"link,omitempty"`
}

// CalendarDecl is the reporting calendar. "Q2" is fiscal or calendar, in some
// timezone, cut on some snapshot day, with some policy for rows that arrive
// afterwards — four facts every run currently re-guesses.
type CalendarDecl struct {
	FiscalYearStart   string `json:"fiscalYearStart,omitempty"`
	QuarterBoundaries string `json:"quarterBoundaries,omitempty"`
	Timezone          string `json:"timezone,omitempty"`
	SnapshotDay       string `json:"snapshotDay,omitempty"`
	LateArrivalPolicy string `json:"lateArrivalPolicy,omitempty"`
}

// Tolerance units. A tolerance is A NUMBER WITH A DENOMINATOR: "0.5%" and
// "$1,000" were the same string to the platform, and the denominator of a
// relative tolerance was never asked for at all — the form said "of the Finance
// total" in a placeholder while the plan said "using the dashboard total".
const (
	ToleranceUnitPercentOfA        = "percent_of_a"
	ToleranceUnitPercentOfB        = "percent_of_b"
	ToleranceUnitPercentOfBaseline = "percent_of_baseline"
	ToleranceUnitAbsolute          = "absolute"
	ToleranceUnitCount             = "count"
	ToleranceUnitExact             = "exact"
	ToleranceUnitPSI               = "psi"
	ToleranceUnitPointsOfShare     = "points_of_share"
)

// ToleranceUnits is every unit a declared tolerance may carry, in form order.
func ToleranceUnits() []string {
	return []string{
		ToleranceUnitPercentOfA, ToleranceUnitPercentOfB, ToleranceUnitPercentOfBaseline,
		ToleranceUnitAbsolute, ToleranceUnitCount, ToleranceUnitExact,
		ToleranceUnitPSI, ToleranceUnitPointsOfShare,
	}
}

// ToleranceDecl is a STANDING tolerance: how far a subject may move before
// somebody has to act, declared once, by a named owner, on a named date.
//
// `Subject` is a KPI id or name (for a reconciliation) or a dataset feature
// name (for a shift audit): one registry for both, because "how much movement
// is too much" is one question asked of two kinds of thing.
//
// A run may TIGHTEN one of these and may never loosen it — LoosenedTolerances
// is that sentence as a refusal.
type ToleranceDecl struct {
	ID      string `json:"id,omitempty"`
	Subject string `json:"subject,omitempty"`
	// Type is what KIND of thing the subject is: numeric, categorical or
	// timestamp. It is here because the shift audit's features table requires
	// it — a categorical feature's tolerance is a share of a category and a
	// numeric one's is a distance between distributions — and an inherited row
	// with a blank Type arrived on the form looking complete while the recipe's
	// own gate was about to refuse it.
	Type string `json:"type,omitempty"`
	// Why is what breaks downstream when this subject moves. Same reason: the
	// features table has a required "why it matters" cell, and a prefill that
	// left it blank made the form say ready over a row that could not run.
	Why string `json:"why,omitempty"`
	// Statistic is what is measured: psi, kl, rate-delta, category-emergence,
	// volume-drift, or a measure's own total for a reconciliation.
	Statistic string   `json:"statistic,omitempty"`
	Value     *float64 `json:"value,omitempty"`
	// Unit is one of ToleranceUnits(). An unrecognised unit is a validation
	// error rather than an accepted string: the defect this group closes is a
	// tolerance whose unit nobody knew.
	Unit string `json:"unit,omitempty"`
	// SegmentValue is the tighter per-segment number, where there is one.
	SegmentValue *float64 `json:"segmentValue,omitempty"`
	// WatchBand is where a subject becomes worth watching without yet being
	// worth acting on. At or below Value.
	WatchBand *float64 `json:"watchBand,omitempty"`
	Severity  string   `json:"severity,omitempty"`
	Owner     string   `json:"owner,omitempty"`
	// SetAt is an ISO date. Who set this and when is half of what makes a
	// tolerance defensible in the meeting it is quoted in.
	SetAt string `json:"setAt,omitempty"`
}

// Tolerance subject types. A subject that is not one of these is not refused —
// a project may watch something this build has no word for — but the three
// named ones are what the shift audit's features table renders.
const (
	ToleranceTypeNumeric     = "numeric"
	ToleranceTypeCategorical = "categorical"
	ToleranceTypeTimestamp   = "timestamp"
)

// ToleranceTypes is every subject type a declared tolerance may carry.
func ToleranceTypes() []string {
	return []string{ToleranceTypeNumeric, ToleranceTypeCategorical, ToleranceTypeTimestamp}
}

// ExclusionDecl is a standing known difference: a filter one side applies and
// the other does not. Named here it becomes a TERM of a reconciliation's
// accounting equation; unnamed, whatever it explains is reported as unexplained.
type ExclusionDecl struct {
	Name   string `json:"name,omitempty"`
	Side   string `json:"side,omitempty"`
	Filter string `json:"filter,omitempty"`
	Owner  string `json:"owner,omitempty"`
}

// StakeholderDecl is who plays which part. `ContactRef` is a REFERENCE — a
// name, a handle, a distribution-list id — never an address the platform would
// then be holding on someone's behalf.
type StakeholderDecl struct {
	Role       string `json:"role,omitempty"`
	Name       string `json:"name,omitempty"`
	ContactRef string `json:"contactRef,omitempty"`
}

// DeliveryDecl is where a finished deliverable is meant to go, declared once so
// that "no form has a delivery slot" has somewhere to land. A recipient
// REFERENCE, never an address list.
type DeliveryDecl struct {
	Format       string `json:"format,omitempty"`
	RecipientRef string `json:"recipientRef,omitempty"`
}

// EvalsetDecl is a FROZEN set of rows a model is scored on. `SHA256` is the
// point: "the June holdout" names a belief, a content hash names bytes.
type EvalsetDecl struct {
	ID string `json:"id,omitempty"`
	// Kind is "dev" or "holdout". The holdout is the one nothing may train on.
	Kind        string `json:"kind,omitempty"`
	Location    string `json:"location,omitempty"`
	RowCount    *int64 `json:"rowCount,omitempty"`
	DateRange   string `json:"dateRange,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	LabelColumn string `json:"labelColumn,omitempty"`
	FrozenAt    string `json:"frozenAt,omitempty"`
}

// EvalsetKinds is the closed set of evalset kinds.
func EvalsetKinds() []string { return []string{"dev", "holdout"} }

// HoldoutCustodyDecl says who holds the holdout and what may read it. The rule
// text is carried rather than assumed so it reaches every brief verbatim.
type HoldoutCustodyDecl struct {
	Holder    string `json:"holder,omitempty"`
	EvalsetID string `json:"evalsetId,omitempty"`
	// Rule is HoldoutCustodyRule unless the owner writes a stricter one.
	Rule string `json:"rule,omitempty"`
}

// HoldoutCustodyRule is the custody sentence in the words it must travel in.
const HoldoutCustodyRule = "no workspace-write node may read it"

// MetricDecl is a metric definition with the number it must clear. It exists so
// a recipe's "Success metric" slot can POINT AT a declared ruler instead of
// being a sentence the builder's own script gets to interpret.
type MetricDecl struct {
	ID                string `json:"id,omitempty"`
	Name              string `json:"name,omitempty"`
	FormulaOrLibrary  string `json:"formulaOrLibrary,omitempty"`
	OperatingPoint    string `json:"operatingPoint,omitempty"`
	UncertaintyMethod string `json:"uncertaintyMethod,omitempty"`
	// Threshold carries its own comparator (">= 0.65"), because the platform's
	// eval-gate parses a comparator and a number out of the declared metric and
	// a bare number leaves the direction to whoever reads it.
	Threshold string `json:"threshold,omitempty"`
}

// SliceDecl is a column the model must be reported on, with the floor its worst
// slice may not fall below. Undeclared, the slices are whatever the builder felt
// like reporting.
type SliceDecl struct {
	Column string   `json:"column,omitempty"`
	Floor  *float64 `json:"floor,omitempty"`
}

// LabelAvailabilityDecl is when a label becomes knowable — the fact every
// leakage check is ultimately judged against.
type LabelAvailabilityDecl struct {
	Event string `json:"event,omitempty"`
	Lag   string `json:"lag,omitempty"`
}

// ServingConstraintsDecl is what the model has to live inside once it ships.
type ServingConstraintsDecl struct {
	Latency      string `json:"latency,omitempty"`
	ArtifactSize string `json:"artifactSize,omitempty"`
	Residency    string `json:"residency,omitempty"`
	Cost         string `json:"cost,omitempty"`
}

// RegressionPolicyDecl is the evalset a refinement or a learned skill must
// re-score against, and by how much it has to win.
type RegressionPolicyDecl struct {
	EvalsetID string `json:"evalsetId,omitempty"`
	// MinMargin carries its own unit ("0.02 PR-AUC", "2 points").
	MinMargin string `json:"minMargin,omitempty"`
}

// ── Round 3: the facts a data engineer kept re-typing as prose ──────────────
//
// Three groups and one per-dataset record, each closing a finding of the shape
// "the form asks for this in every run, and the project has nowhere to say it
// once". Same rules as everything above: every field optional, references only.

// ScheduleDecl is when the owner expects this project's work to run, and what
// actually starts it.
//
// IT IS HONEST ABOUT THE PLATFORM. Hyperplane has no scheduler: a run starts
// when a person starts it or when something outside starts it. So this group
// does not promise a cadence — it RECORDS the one the owner expects, names the
// external system that will do the starting, and names who is answerable when
// the work does not land. A composed plan asked "which external system will
// trigger the refresh, since this workflow cannot schedule recurring runs?"
// once per composition and had nowhere to write the answer down.
type ScheduleDecl struct {
	// ExpectedCadence is how often the owner expects to run this. An
	// expectation, not a promise the platform keeps.
	ExpectedCadence string `json:"expectedCadence,omitempty"`
	// Deadline is the clock time the work must have landed by.
	Deadline string `json:"deadline,omitempty"`
	// Timezone is the zone that clock is read in. "06:00" is a different moment
	// in every office, which is why it is a field and not an assumption.
	Timezone string `json:"timezone,omitempty"`
	// MaxLag is the largest acceptable delay from source availability to the
	// finished tables, including its denominator (for example "6 hours").
	MaxLag string `json:"maxLag,omitempty"`
	// TriggeredBy is the external system that starts the run — the cron host,
	// the orchestrator, the person. It is named here because this platform
	// cannot do the starting itself.
	TriggeredBy string `json:"triggeredBy,omitempty"`
	// PageRef is who or what to page when the work does not land: a rota name,
	// a handle, a list id. A REFERENCE, never an address the platform would
	// then be paging on someone's behalf.
	PageRef string `json:"pageRef,omitempty"`
}

// Warehouse access levels. Which one a project declares decides which role the
// target store is rendered with on a recipe form.
const (
	WarehouseAccessRead  = "read"
	WarehouseAccessWrite = "write"
)

// WarehouseAccesses is every access level a declared warehouse may carry.
func WarehouseAccesses() []string { return []string{WarehouseAccessRead, WarehouseAccessWrite} }

// WarehouseRoleRefs are the two role REFERENCES a warehouse is reached through:
// a role name, or the name of the variable holding one. Never a grant, never a
// password.
type WarehouseRoleRefs struct {
	Read  string `json:"read,omitempty"`
	Write string `json:"write,omitempty"`
}

// WarehouseDecl is the analytical store a project's tables live in, declared as
// REFERENCES.
//
// `ConnectionRef` is the point and the risk. It is the NAME of the environment
// variable that holds the connection — `ANALYTICS_DSN` — and never the
// connection itself, which is why ValidateDeclarations refuses a value that is
// not an environment-variable name. The recipe slot this fills was prose
// ("postgres, schema analytics"), so every run re-typed the store and no run
// could say which credential it would be reached with or with what rights.
type WarehouseDecl struct {
	Engine string `json:"engine,omitempty"`
	// DatabaseSchema is the named group of tables inside the engine.
	DatabaseSchema string `json:"databaseSchema,omitempty"`
	// ConnectionRef is an ENVIRONMENT VARIABLE NAME. Never a value.
	ConnectionRef string            `json:"connectionRef,omitempty"`
	RoleRefs      WarehouseRoleRefs `json:"roleRefs,omitempty"`
	// Access is WarehouseAccessRead or WarehouseAccessWrite: what runs under
	// this project may do to the store.
	Access string `json:"access,omitempty"`
}

// LineageDecl is where the pipeline's CODE lives and how a change to it reaches
// production. References only: a path, a command, a convention.
//
// It exists because "the pipeline you are changing" was one prose box, so a
// change recipe could not cite the files it edits, the command that runs them,
// the manifest that describes them, or the review convention a change goes
// through — and all four were re-typed from memory once per run.
type LineageDecl struct {
	RepoPath string `json:"repoPath,omitempty"`
	// RunCommand runs the whole thing end to end. The baseline is captured by
	// actually running it, so this has to be a command and not a description.
	RunCommand string `json:"runCommand,omitempty"`
	// ManifestPath is the file that describes the models — a dbt manifest, a
	// generated catalogue.
	ManifestPath string `json:"manifestPath,omitempty"`
	// PRConvention is how a change lands: the convention, not a link to one
	// pull request.
	PRConvention string `json:"prConvention,omitempty"`
}

// Load modes. `full` rebuilds everything every run; `incremental` advances a
// watermark. They fail differently, and a pipeline recipe used to DERIVE which
// one it was as an OUTPUT of its own planning step.
const (
	LoadModeFull        = "full"
	LoadModeIncremental = "incremental"
)

// LoadModes is every load mode a dataset may declare.
func LoadModes() []string { return []string{LoadModeFull, LoadModeIncremental} }

// LoadSemanticsDecl is HOW one dataset is loaded.
//
// Full versus incremental, the column an incremental load advances on, how late
// a row may still arrive, and what a late row does to the period it belongs to:
// four facts the pipeline recipe used to decide for itself and report back as
// part of its plan. That is the wrong direction. They are the owner's facts
// about the owner's data, and a plan that infers them is guessing at a contract
// somebody else already knows.
type LoadSemanticsDecl struct {
	// Mode is LoadModeFull or LoadModeIncremental.
	Mode string `json:"mode,omitempty"`
	// WatermarkColumn is the column an incremental load advances on. An
	// incremental load without one names no rows as new.
	WatermarkColumn string `json:"watermarkColumn,omitempty"`
	// LateArrivalWindow is how late a row may still arrive.
	LateArrivalWindow string `json:"lateArrivalWindow,omitempty"`
	// RestatementRule is what a late row does to the period it belongs to:
	// restate that period, or count the row on the day it landed. The two give
	// different numbers for the same month, and only one of them is the
	// owner's.
	RestatementRule string `json:"restatementRule,omitempty"`
}

// ── Validation ──────────────────────────────────────────────────────────────

// ErrInvalidDeclarations is what a save carrying a declaration that is not one
// returns. It is a sentinel so the HTTP layer can answer 400 rather than 500:
// the document is the caller's, and the sentences below are addressed to them.
var ErrInvalidDeclarations = errors.New("invalid declarations")

// DeclarationError is ErrInvalidDeclarations with the sentences attached.
type DeclarationError struct {
	Problems []string
}

func (e *DeclarationError) Error() string {
	return "invalid declarations: " + strings.Join(e.Problems, "; ")
}

func (e *DeclarationError) Is(target error) bool { return target == ErrInvalidDeclarations }

// refuseNewDeclarationProblems compares the document a save would STORE against
// the one already stored and refuses only what this save introduced.
//
// The asymmetry is deliberate. Validation had never run on the write path at
// all, so a project can be carrying a row that would not be accepted today —
// and refusing an unrelated edit because of it would lock the owner out of the
// declaration form that is the only place they could fix it. What this save
// adds, this save answers for.
func refuseNewDeclarationProblems(existing, next JSON) error {
	after, err := ParseDeclarations(next)
	if err != nil {
		return &DeclarationError{Problems: []string{err.Error()}}
	}
	problems := ValidateDeclarations(after)
	if len(problems) == 0 {
		return nil
	}
	before := map[string]bool{}
	if prev, perr := ParseDeclarations(existing); perr == nil {
		for _, p := range ValidateDeclarations(prev) {
			before[p] = true
		}
	}
	var fresh []string
	for _, p := range problems {
		if !before[p] {
			fresh = append(fresh, p)
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	return &DeclarationError{Problems: fresh}
}

// ValidateDeclarations reports what is wrong with a declaration document, as
// sentences a person can act on. An empty result means "nothing here is wrong",
// which is NOT the same as "everything is declared": every group is optional and
// silence about a group is a valid answer.
//
// Only closed sets and orderings are checked — a unit that is not a unit, a
// kind that is not a kind, a watch band above the tolerance it watches. Nothing
// here requires a field to be filled in, because requiring a field is how a
// declaration form becomes a wall.
func ValidateDeclarations(d ProjectDeclarations) []string {
	var problems []string
	units := map[string]bool{}
	for _, u := range ToleranceUnits() {
		units[u] = true
	}
	for i, t := range d.Tolerances {
		where := t.Subject
		if where == "" {
			where = fmt.Sprintf("Tolerance %d (field: tolerances[%d])", i+1, i)
		}
		if u := strings.TrimSpace(t.Unit); u != "" && !units[u] {
			problems = append(problems, fmt.Sprintf(
				"%s: %q is not a tolerance unit — use one of %s", where, u, strings.Join(ToleranceUnits(), ", ")))
		}
		if t.Value != nil && *t.Value <= 0 {
			problems = append(problems, fmt.Sprintf(
				"%s: a tolerance of %g %s is not a tolerance — the number must be above zero",
				where, *t.Value, strings.TrimSpace(t.Unit)))
		}
		if t.WatchBand != nil && (*t.WatchBand < 0 || (t.Value != nil && *t.WatchBand >= *t.Value)) {
			problems = append(problems, fmt.Sprintf(
				"%s: the watch band (%g) must be at least zero and below the tolerance (%g)",
				where, *t.WatchBand, valueOrZero(t.Value)))
		}
		// A row with a subject and no number is not a tolerance, whether or not
		// it names a unit. The unit-only case was already refused here; the
		// subject-only case was not, so a row naming a feature and nothing else
		// was stored on the project and inherited into every shift run, where
		// the recipe's gate refused it one screen too late.
		if t.Value == nil && strings.TrimSpace(t.Unit) != ToleranceUnitExact {
			problems = append(problems, fmt.Sprintf(
				"%s: a tolerance with no value is not a tolerance — write the number this subject may move by, or leave the row out", where))
		}
		if ty := strings.ToLower(strings.TrimSpace(t.Type)); ty != "" {
			known := false
			for _, k := range ToleranceTypes() {
				if ty == k {
					known = true
				}
			}
			if !known {
				problems = append(problems, fmt.Sprintf(
					"%s: %q is not a subject type — use one of %s", where, t.Type, strings.Join(ToleranceTypes(), ", ")))
			}
		}
	}
	kindSet := map[string]bool{}
	for _, k := range DatasetKinds() {
		kindSet[k] = true
	}
	windowSet := map[string]bool{}
	for _, w := range DatasetWindows() {
		windowSet[w] = true
	}
	for i, ds := range d.Datasets {
		where := datasetLabel(ds, i)
		kind := strings.ToLower(strings.TrimSpace(ds.Kind))
		if kind != "" && !kindSet[kind] {
			problems = append(problems, fmt.Sprintf(
				"%s: %q is not a dataset role — use one of %s", where, ds.Kind, strings.Join(DatasetKinds(), ", ")))
		}
		window := strings.ToLower(strings.TrimSpace(ds.Window))
		if window != "" && !windowSet[window] {
			problems = append(problems, fmt.Sprintf(
				"%s: %q is not a window — use one of %s", where, ds.Window, strings.Join(DatasetWindows(), ", ")))
		}
		if window != "" && (kind == DatasetKindLookup || kind == DatasetKindExport) {
			problems = append(problems, fmt.Sprintf(
				"%s is declared as a %s and as the %s window — a lookup is joined onto the data under test, it is never the data under test",
				where, kind, window))
		}
	}
	kinds := map[string]bool{}
	for _, k := range EvalsetKinds() {
		kinds[k] = true
	}
	seen := map[string]bool{}
	for i, e := range d.Evalsets {
		where := e.ID
		if where == "" {
			where = fmt.Sprintf("Evaluation set %d (field: evalsets[%d])", i+1, i)
		}
		if k := strings.TrimSpace(e.Kind); k != "" && !kinds[k] {
			problems = append(problems, fmt.Sprintf(
				"%s: %q is not an evalset kind — use dev or holdout", where, k))
		}
		if id := strings.TrimSpace(e.ID); id != "" {
			if seen[id] {
				problems = append(problems, fmt.Sprintf("Evaluation sets: %q is declared twice — an evalset id is what a run cites", id))
			}
			seen[id] = true
		}
	}
	if id := strings.TrimSpace(d.HoldoutCustody.EvalsetID); id != "" && !seen[id] {
		problems = append(problems, fmt.Sprintf(
			"Evalset id: %q is not one of the declared evaluation sets — custody of a set nobody declared protects nothing (field: holdoutCustody.evalsetId)", id))
	}
	if id := strings.TrimSpace(d.RegressionPolicy.EvalsetID); id != "" && !seen[id] {
		problems = append(problems, fmt.Sprintf(
			"Evalset id: %q is not one of the declared evaluation sets (field: regressionPolicy.evalsetId)", id))
	}
	problems = append(problems, warehouseProblems(d.Warehouse)...)
	problems = append(problems, scheduleProblems(d.Schedule)...)
	problems = append(problems, loadSemanticsProblems(d.Datasets)...)
	return problems
}

// envVarName is the shape of an environment variable NAME, with or without the
// `$` a person tends to type in front of one.
var envVarName = regexp.MustCompile(`^\$?[A-Za-z_][A-Za-z0-9_]*$`)

// warehouseProblems refuses a warehouse that names a connection instead of
// referring to one, and an access level that is not one.
//
// The connection check is the only place in this whole document where a wrong
// answer is a leak rather than a mistake: `postgres://user:pw@host/db` typed
// into a field that wanted `ANALYTICS_DSN` puts a password into a project
// record, into every form that inherits it, and into every brief written under
// the project. So it is refused at the door, in a sentence that says what to
// write instead.
func warehouseProblems(w WarehouseDecl) []string {
	var problems []string
	if ref := strings.TrimSpace(w.ConnectionRef); ref != "" && !envVarName.MatchString(ref) {
		problems = append(problems,
			"Connection: what you entered looks like a connection string, not the name of one — declare the NAME of the environment variable that holds it (ANALYTICS_DSN).")
	}
	for _, role := range []struct{ label, ref, example string }{
		{"Read role", strings.TrimSpace(w.RoleRefs.Read), "ANALYTICS_READER_ROLE"},
		{"Write role", strings.TrimSpace(w.RoleRefs.Write), "ANALYTICS_WRITER_ROLE"},
	} {
		if role.ref != "" && !envVarName.MatchString(role.ref) {
			problems = append(problems, role.label+": what you entered looks like a credential, not the name of one — declare the NAME of the environment variable that holds it ("+role.example+").")
		}
	}
	access := strings.ToLower(strings.TrimSpace(w.Access))
	if access != "" {
		known := false
		for _, a := range WarehouseAccesses() {
			if access == a {
				known = true
			}
		}
		if !known {
			problems = append(problems, fmt.Sprintf(
				"Access: %q is not an access level — use %s (field: warehouse.access)", w.Access, strings.Join(WarehouseAccesses(), " or ")))
		}
	}
	if access == WarehouseAccessRead && strings.TrimSpace(w.RoleRefs.Write) != "" {
		problems = append(problems,
			"Access: read access and a write role were both declared — one of the two is wrong, and a run that inherits both cannot tell which (fields: warehouse.access, warehouse.roleRefs.write)")
	}
	return problems
}

// scheduleProblems refuses a paging destination that is an ADDRESS.
//
// Same rule as `contactRef` and `recipientRef`: this platform holds references
// to people, never ways of reaching them. A rota name is a reference an owner's
// own tools can resolve; an email address is a thing the platform would then be
// carrying on somebody's behalf.
func scheduleProblems(s ScheduleDecl) []string {
	page := strings.TrimSpace(s.PageRef)
	if page == "" {
		return nil
	}
	if strings.Contains(page, "@") && strings.Contains(page, ".") {
		return []string{
			"Who to page: what you entered looks like an address — give a reference (a rota name, a handle, a list id); this platform holds references to people, never ways of reaching them.",
		}
	}
	return nil
}

// loadSemanticsProblems refuses a load mode that is not one. Everything else
// about a load is a caveat rather than a refusal — see DeclarationCaveats.
func loadSemanticsProblems(list []DatasetDecl) []string {
	modes := map[string]bool{}
	for _, m := range LoadModes() {
		modes[m] = true
	}
	var problems []string
	for i, ds := range list {
		if ds.LoadSemantics == nil {
			continue
		}
		mode := strings.ToLower(strings.TrimSpace(ds.LoadSemantics.Mode))
		if mode == "" || modes[mode] {
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"%s: %q is not a load mode — use %s", datasetLabel(ds, i), ds.LoadSemantics.Mode, strings.Join(LoadModes(), " or ")))
	}
	return problems
}

// datasetLabel names a dataset in a sentence addressed to the person who
// declared it: their name for it, else its position.
func datasetLabel(ds DatasetDecl, i int) string {
	if v := strings.TrimSpace(ds.Name); v != "" {
		return v
	}
	if v := strings.TrimSpace(ds.ID); v != "" {
		return v
	}
	return fmt.Sprintf("Dataset %d (field: datasets[%d])", i+1, i)
}

// NoHoldoutHashCaveat is what a holdout declared without a content hash has to
// say about itself, everywhere it is shown.
//
// It is a CAVEAT and not a validation problem on purpose: a project that knows
// where its holdout is and not yet what its bytes hash to has declared
// something real, and refusing it would push the declaration back into memory.
// But "the June holdout" names a belief, and a run scored against a set nobody
// can identify proves a number about bytes nobody can name — so the absence
// travels with the declaration instead of being silently omitted from it.
const NoHoldoutHashCaveat = "no content hash — nothing can prove which bytes were scored"

// DeclarationCaveats is what is WEAK about a declaration document, as sentences
// that travel with it: the form shows them beside the field, and a brief written
// under the project carries them rather than presenting the declaration as
// airtight. Nothing here refuses a save.
func DeclarationCaveats(d ProjectDeclarations) []string {
	var out []string
	for i, e := range d.Evalsets {
		if !strings.EqualFold(strings.TrimSpace(e.Kind), "holdout") {
			continue
		}
		if strings.TrimSpace(e.SHA256) != "" {
			continue
		}
		where := strings.TrimSpace(e.ID)
		if where == "" {
			where = fmt.Sprintf("evalsets[%d]", i)
		}
		out = append(out, fmt.Sprintf("holdout %s: %s", where, NoHoldoutHashCaveat))
	}
	out = append(out, loadSemanticsCaveats(d.Datasets)...)
	if c := scheduleCaveat(d.Schedule); c != "" {
		out = append(out, c)
	}
	return out
}

// loadSemanticsCaveats say what an incomplete load declaration leaves undecided.
//
// These are caveats and not refusals for the reason every caveat here is: an
// owner who knows the load is incremental and has not yet looked up which
// column it advances on has declared something real, and refusing it would push
// the whole answer back into memory. But an incremental load with no watermark
// names no rows as new, and a late-arrival window with no restatement rule does
// not say what the late row does to the month it belongs to — so the gap
// travels with the declaration instead of being read as an answer.
func loadSemanticsCaveats(list []DatasetDecl) []string {
	var out []string
	for i, ds := range list {
		ls := ds.LoadSemantics
		if ls == nil {
			continue
		}
		who := datasetLabel(ds, i)
		if strings.EqualFold(strings.TrimSpace(ls.Mode), LoadModeIncremental) && strings.TrimSpace(ls.WatermarkColumn) == "" {
			out = append(out, who+": loaded incrementally with no watermark column — nothing says which rows are new")
		}
		if strings.TrimSpace(ls.LateArrivalWindow) != "" && strings.TrimSpace(ls.RestatementRule) == "" {
			out = append(out, who+": late arrivals are allowed and no restatement rule says what one does to the "+
				"period it belongs to — restating it and counting it on the day it landed give different numbers for the same month")
		}
	}
	return out
}

// scheduleCaveat is the deadline that names no timezone. "06:00" is a different
// moment in every office, and a deadline nobody can place on a clock cannot be
// missed or met.
func scheduleCaveat(s ScheduleDecl) string {
	if strings.TrimSpace(s.Deadline) == "" || strings.TrimSpace(s.Timezone) != "" {
		return ""
	}
	return "schedule: the deadline " + strings.TrimSpace(s.Deadline) + " names no timezone — it is a different moment in every office"
}

// ── The tightening rule ─────────────────────────────────────────────────────

// LooseningError reports a run trying to widen a tolerance the project already
// declared. It is an error rather than a warning because a run that quietly
// loosens the standing number produces a verdict the standing number would have
// refused, and nothing downstream says so.
type LooseningError struct {
	Loosened []string
}

func (e *LooseningError) Error() string {
	return "a run may tighten a standing tolerance, never loosen it: " + strings.Join(e.Loosened, "; ")
}

// StandingTolerance finds the declared tolerance for a subject — a KPI id, a
// KPI name or a dataset feature — matched case-insensitively so "Net Revenue"
// and "net_revenue" are not two different subjects.
func StandingTolerance(d ProjectDeclarations, subject string) (ToleranceDecl, bool) {
	want := toleranceKey(subject)
	if want == "" {
		return ToleranceDecl{}, false
	}
	for _, t := range d.Tolerances {
		if toleranceKey(t.Subject) == want {
			return t, true
		}
	}
	// A tolerance may be declared against a KPI's id while a run names the KPI,
	// or the reverse. Resolve through the KPI dictionary before giving up.
	for _, k := range d.KPIs {
		if toleranceKey(k.ID) != want && toleranceKey(k.Name) != want {
			continue
		}
		for _, t := range d.Tolerances {
			if toleranceKey(t.Subject) == toleranceKey(k.ID) || toleranceKey(t.Subject) == toleranceKey(k.Name) {
				return t, true
			}
		}
	}
	return ToleranceDecl{}, false
}

// toleranceKey normalises a subject so a KPI's display name and its column name
// compare equal: lower-cased, with spaces, underscores and hyphens removed.
func toleranceKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	r := strings.NewReplacer(" ", "", "_", "", "-", "")
	return r.Replace(s)
}

// LoosenedTolerances returns one sentence per run-declared tolerance that is
// WIDER than the project's standing one for the same subject, or that changes
// its unit — a percent-of-A tolerance and an absolute one are not comparable,
// so swapping the unit is a way of loosening without changing the number.
//
// A subject the project says nothing about is not a violation: a run is allowed
// to declare a tolerance nobody declared before it. That is the direction the
// registry is meant to grow in.
func LoosenedTolerances(d ProjectDeclarations, runRows []map[string]string, subjectCol, valueCol, unitCol string) []string {
	var out []string
	for _, row := range runRows {
		subject := strings.TrimSpace(row[subjectCol])
		standing, ok := StandingTolerance(d, subject)
		if !ok || standing.Value == nil {
			continue
		}
		value, ok := parseFloat(row[valueCol])
		if !ok {
			continue
		}
		if value <= 0 {
			out = append(out, fmt.Sprintf(
				"a tolerance of %g %s is not a tolerance — the number must be above zero", value, strings.TrimSpace(row[unitCol])))
			continue
		}
		unit := strings.TrimSpace(row[unitCol])
		if unit != "" && strings.TrimSpace(standing.Unit) != "" && unit != strings.TrimSpace(standing.Unit) {
			out = append(out, fmt.Sprintf(
				"%s is declared at %g %s by %s and this run states it in %s — two units are not comparable, so state it in %s or change the project's declaration",
				subject, *standing.Value, standing.Unit, ownerOrSomeone(standing.Owner), unit, standing.Unit))
			continue
		}
		if value > *standing.Value {
			out = append(out, fmt.Sprintf(
				"%s is declared at %g %s by %s and this run asks for %g — a run may tighten it, never widen it",
				subject, *standing.Value, unitOrBare(standing.Unit), ownerOrSomeone(standing.Owner), value))
		}
	}
	return out
}

func valueOrZero(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

// parseFloat reads a declared number out of a cell, tolerating the units a
// person types next to it ("0.5%", "$500", "2 points"). A cell with no number
// in it is not a number, which is why the second return exists.
func parseFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	var b strings.Builder
	started := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			started = true
		case (r == '.' || r == '-' || r == '+') && !strings.ContainsRune(b.String(), '.'):
			b.WriteRune(r)
		case started:
			// The number ended; whatever follows is its unit.
			goto done
		}
	}
done:
	v, err := strconv.ParseFloat(strings.Trim(b.String(), "."), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// toleranceSlotColumns names, per table slot, which of its columns carry the
// SUBJECT, the VALUE and the UNIT of a tolerance. It is what lets one rule —
// "a run may tighten a standing tolerance, never loosen it" — be enforced over
// two differently-shaped tables without either of them knowing about the other.
var toleranceSlotColumns = map[string][3]string{
	"features":   {"feature", "tolerance", "unit"},
	"tolerances": {"measure", "value", "unit"},
}

// RefuseLoosenedTolerances is the tightening rule at the moment a recipe is
// instantiated: the last point where the run's numbers and the project's
// standing numbers are both in hand and nothing has been computed yet.
//
// A project that declares no tolerances, or a run that declares none, passes
// through untouched — the rule is about the intersection, and there is no
// intersection to police.
func (s *Store) RefuseLoosenedTolerances(ctx context.Context, project string, slots []ContextSlot, values map[string]string) error {
	if strings.TrimSpace(project) == "" {
		return nil
	}
	var relevant []ContextSlot
	for _, slot := range slots {
		if slot.SlotType() != SlotTypeTable {
			continue
		}
		if _, ok := toleranceSlotColumns[slot.ID]; ok && strings.TrimSpace(values[slot.ID]) != "" {
			relevant = append(relevant, slot)
		}
	}
	if len(relevant) == 0 {
		return nil
	}
	p, err := s.GetProject(ctx, project)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	decl, err := ParseDeclarations(p.Declarations)
	if err != nil {
		// A declaration document nobody can parse declares no tolerance to
		// tighten. Refusing the run over it would be worse than running it.
		return nil
	}
	if len(decl.Tolerances) == 0 {
		return nil
	}
	var loosened []string
	for _, slot := range relevant {
		cols := toleranceSlotColumns[slot.ID]
		loosened = append(loosened,
			LoosenedTolerances(decl, SlotTableRows(slot, values[slot.ID]), cols[0], cols[1], cols[2])...)
	}
	if len(loosened) > 0 {
		return &LooseningError{Loosened: loosened}
	}
	return nil
}

func ownerOrSomeone(owner string) string {
	if o := strings.TrimSpace(owner); o != "" {
		return o
	}
	return "the project"
}

func unitOrBare(unit string) string {
	if u := strings.TrimSpace(unit); u != "" {
		return u
	}
	return "(no unit declared)"
}
