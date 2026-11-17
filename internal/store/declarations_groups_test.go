package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }

// round2Declarations is one project that declares EVERY group this round adds,
// as a document the form would send.
const round2Declarations = `{
  "kpis":[{"id":"net_revenue","name":"net revenue",
           "definitions":{"Finance":"sum(amount_usd) after refunds","Sales":"sum(Amount) on closed-won"},
           "unit":"currency","currency":"USD","owner":"Finance ops"}],
  "dashboards":[{"id":"finance-rev","name":"Finance revenue","owner":"Finance ops",
                 "source":"analytics.revenue_daily","readPath":"SQL at invoice-line level",
                 "refreshCadence":"nightly 04:00","asOfConvention":"close_date","link":"looker/12"},
                {"id":"sales-pipe","name":"Sales pipeline","source":"exports/sf.csv","readPath":"duckdb"}],
  "calendar":{"fiscalYearStart":"February 1","quarterBoundaries":"Q2 = Apr 1 - Jun 30",
              "timezone":"America/New_York","snapshotDay":"the 15th",
              "lateArrivalPolicy":"restated the following month"},
  "tolerances":[{"id":"t1","subject":"net_revenue","statistic":"total","value":0.05,
                 "unit":"percent_of_a","segmentValue":250,"severity":"blocker",
                 "owner":"Sofia","setAt":"2026-08-21"},
                {"id":"t2","subject":"payment_type","statistic":"category-emergence","value":2,
                 "unit":"points_of_share","watchBand":1,"severity":"major","owner":"Dev"}],
  "exclusions":[{"name":"internal test accounts","side":"A","filter":"account_type = 'internal'","owner":"Finance ops"}],
  "stakeholders":[{"role":"approves the equations","name":"Sofia","contactRef":"#analytics"}],
  "delivery":{"format":"markdown summary + CSV","recipientRef":"#finance-analytics"},
  "evalsets":[{"id":"churn-holdout","kind":"holdout","location":"analytics.account_daily_holdout",
               "rowCount":96417,"dateRange":"2025-04","sha256":"9b74e2c1","labelColumn":"churned_30d",
               "frozenAt":"2025-05-02"}],
  "holdoutCustody":{"holder":"Dev, ML lead","evalsetId":"churn-holdout",
                    "rule":"no workspace-write node may read it"},
  "metrics":[{"id":"m1","name":"PR-AUC","formulaOrLibrary":"sklearn.average_precision_score",
              "operatingPoint":"top 2% flagged","uncertaintyMethod":"bootstrap, 1000 resamples",
              "threshold":">= 0.65"}],
  "slices":[{"column":"region","floor":0.45},{"column":"plan","floor":0.3}],
  "labelAvailability":{"event":"a chargeback is filed","lag":"90 days"},
  "servingConstraints":{"latency":"under 50 ms at p99","artifactSize":"under 50 MB",
                        "residency":"EU stays in the EU","cost":"under $200/month"},
  "regressionPolicy":{"evalsetId":"churn-holdout","minMargin":"0.02 PR-AUC"}
}`

// TestEveryRound2GroupSurvivesAParse is the whole extensibility claim, applied
// to the groups this round adds: each one is written by a form, stored as raw
// JSON, and read back by the typed view. A group that parses to its zero value
// is a group the form can fill in and the platform will never see.
func TestEveryRound2GroupSurvivesAParse(t *testing.T) {
	d, err := ParseDeclarations(JSON(round2Declarations))
	if err != nil {
		t.Fatalf("ParseDeclarations: %v", err)
	}
	if len(d.KPIs) != 1 || d.KPIs[0].Definitions["Finance"] != "sum(amount_usd) after refunds" {
		t.Errorf("kpis: %+v", d.KPIs)
	}
	if len(d.Dashboards) != 2 || d.Dashboards[0].ReadPath != "SQL at invoice-line level" {
		t.Errorf("dashboards: %+v", d.Dashboards)
	}
	if d.Calendar.Timezone != "America/New_York" || d.Calendar.SnapshotDay != "the 15th" {
		t.Errorf("calendar: %+v", d.Calendar)
	}
	if len(d.Tolerances) != 2 || d.Tolerances[0].Value == nil || *d.Tolerances[0].Value != 0.05 {
		t.Errorf("tolerances: %+v", d.Tolerances)
	}
	if d.Tolerances[0].Unit != ToleranceUnitPercentOfA {
		t.Errorf("a tolerance lost its denominator: %+v", d.Tolerances[0])
	}
	if len(d.Exclusions) != 1 || d.Exclusions[0].Side != "A" {
		t.Errorf("exclusions: %+v", d.Exclusions)
	}
	if len(d.Stakeholders) != 1 || d.Stakeholders[0].ContactRef != "#analytics" {
		t.Errorf("stakeholders: %+v", d.Stakeholders)
	}
	if d.Delivery.RecipientRef != "#finance-analytics" {
		t.Errorf("delivery: %+v", d.Delivery)
	}
	if len(d.Evalsets) != 1 || d.Evalsets[0].SHA256 != "9b74e2c1" || d.Evalsets[0].RowCount == nil {
		t.Errorf("evalsets: %+v", d.Evalsets)
	}
	if d.HoldoutCustody.Rule != HoldoutCustodyRule {
		t.Errorf("holdoutCustody: %+v", d.HoldoutCustody)
	}
	if len(d.Metrics) != 1 || d.Metrics[0].Threshold != ">= 0.65" {
		t.Errorf("metrics: %+v", d.Metrics)
	}
	if len(d.Slices) != 2 || d.Slices[0].Floor == nil || *d.Slices[0].Floor != 0.45 {
		t.Errorf("slices: %+v", d.Slices)
	}
	if d.LabelAvailability.Lag != "90 days" {
		t.Errorf("labelAvailability: %+v", d.LabelAvailability)
	}
	if d.ServingConstraints.Residency != "EU stays in the EU" {
		t.Errorf("servingConstraints: %+v", d.ServingConstraints)
	}
	if d.RegressionPolicy.MinMargin != "0.02 PR-AUC" {
		t.Errorf("regressionPolicy: %+v", d.RegressionPolicy)
	}
}

// TestRound2GroupsMergeIndependently: fourteen groups is fourteen more chances
// for one form to wipe another's work. Editing one must leave the other
// thirteen — and a group this build has never heard of — byte-for-byte.
func TestRound2GroupsMergeIndependently(t *testing.T) {
	base := JSON(round2Declarations)
	withUnknown, err := MergeDeclarations(base, JSON(`{"somethingLater":[{"x":1}]}`))
	if err != nil {
		t.Fatalf("MergeDeclarations: %v", err)
	}
	merged, err := MergeDeclarations(withUnknown, JSON(`{"metrics":[{"name":"AUROC","threshold":">= 0.72"}]}`))
	if err != nil {
		t.Fatalf("MergeDeclarations: %v", err)
	}
	var groups map[string]json.RawMessage
	if err := json.Unmarshal(merged, &groups); err != nil {
		t.Fatalf("merged is not an object: %v", err)
	}
	for _, g := range []string{
		"kpis", "dashboards", "calendar", "tolerances", "exclusions", "stakeholders", "delivery",
		"evalsets", "holdoutCustody", "metrics", "slices", "labelAvailability", "servingConstraints",
		"regressionPolicy", "somethingLater",
	} {
		if _, ok := groups[g]; !ok {
			t.Errorf("group %q was lost by an edit that never mentioned it", g)
		}
	}
	if string(groups["somethingLater"]) != `[{"x":1}]` {
		t.Errorf("an unknown group was rewritten rather than left alone: %s", groups["somethingLater"])
	}
	d, err := ParseDeclarations(merged)
	if err != nil {
		t.Fatalf("ParseDeclarations: %v", err)
	}
	if len(d.Metrics) != 1 || d.Metrics[0].Name != "AUROC" {
		t.Errorf("the patched group did not replace the old one: %+v", d.Metrics)
	}
}

// TestValidateDeclarationsNamesWhatIsWrong. Only closed sets and orderings are
// checked: requiring a field is how a declaration form becomes a wall, so a
// project that declares nothing must produce no problems at all.
func TestValidateDeclarationsNamesWhatIsWrong(t *testing.T) {
	if p := ValidateDeclarations(ProjectDeclarations{}); len(p) != 0 {
		t.Errorf("a project that declares nothing was told it was wrong: %v", p)
	}
	d, err := ParseDeclarations(JSON(round2Declarations))
	if err != nil {
		t.Fatal(err)
	}
	if p := ValidateDeclarations(d); len(p) != 0 {
		t.Errorf("a fully declared project reported problems: %v", p)
	}

	bad := ProjectDeclarations{
		Tolerances: []ToleranceDecl{
			{Subject: "net_revenue", Value: f64(0.5), Unit: "percentage"},
			{Subject: "payment_type", Value: f64(2), Unit: ToleranceUnitPointsOfShare, WatchBand: f64(3)},
		},
		Evalsets:         []EvalsetDecl{{ID: "dup", Kind: "test"}, {ID: "dup", Kind: "dev"}},
		HoldoutCustody:   HoldoutCustodyDecl{EvalsetID: "nowhere"},
		RegressionPolicy: RegressionPolicyDecl{EvalsetID: "also-nowhere"},
	}
	got := strings.Join(ValidateDeclarations(bad), "\n")
	for _, want := range []string{
		`"percentage" is not a tolerance unit`,
		"watch band (3) must be at least zero and below the tolerance (2)",
		`"test" is not an evalset kind`,
		`"dup" is declared twice`,
		`Evalset id: "nowhere" is not one of the declared evaluation sets`,
		`Evalset id: "also-nowhere" is not one of the declared evaluation sets`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("validation never says %q:\n%s", want, got)
		}
	}
}

// TestARunMayTightenAToleranceAndNeverWidenIt.
//
// The rule exists because a run that quietly widens the standing number
// produces a verdict the standing number would have refused, and nothing
// downstream says so. Tightening is always allowed; a subject the project says
// nothing about is not a violation, because that is the direction the registry
// is meant to grow in.
func TestARunMayTightenAToleranceAndNeverWidenIt(t *testing.T) {
	d, err := ParseDeclarations(JSON(round2Declarations))
	if err != nil {
		t.Fatal(err)
	}
	rows := func(v ...map[string]string) []map[string]string { return v }

	if got := LoosenedTolerances(d,
		rows(map[string]string{"feature": "payment_type", "tolerance": "1", "unit": "points_of_share"}),
		"feature", "tolerance", "unit"); len(got) != 0 {
		t.Errorf("tightening 2 to 1 was refused: %v", got)
	}
	if got := LoosenedTolerances(d,
		rows(map[string]string{"feature": "trip_distance", "tolerance": "0.9", "unit": "psi"}),
		"feature", "tolerance", "unit"); len(got) != 0 {
		t.Errorf("a subject the project never declared was treated as a violation: %v", got)
	}
	got := LoosenedTolerances(d,
		rows(map[string]string{"feature": "payment_type", "tolerance": "5", "unit": "points_of_share"}),
		"feature", "tolerance", "unit")
	if len(got) != 1 || !strings.Contains(got[0], "never widen") {
		t.Fatalf("widening 2 to 5 was allowed: %v", got)
	}
	if !strings.Contains(got[0], "Dev") {
		t.Errorf("the refusal does not name who set the standing number: %s", got[0])
	}

	// Swapping the denominator is how a tolerance is widened without changing
	// the number, so it is refused in its own words rather than compared.
	swapped := LoosenedTolerances(d,
		rows(map[string]string{"measure": "net revenue", "value": "0.05", "unit": "percent_of_b"}),
		"measure", "value", "unit")
	if len(swapped) != 1 || !strings.Contains(swapped[0], "two units are not comparable") {
		t.Fatalf("a swapped denominator was compared as if it were the same ruler: %v", swapped)
	}
	// ...and the subject matched through the KPI dictionary: "net revenue" is
	// the display name, "net_revenue" the id the tolerance was declared against.
	if _, ok := StandingTolerance(d, "Net Revenue"); !ok {
		t.Error("a KPI's display name does not resolve to the tolerance declared against its id")
	}
}

func TestToleranceNumbersHaveSense(t *testing.T) {
	negative := -0.5
	watch := -0.1
	d := ProjectDeclarations{Tolerances: []ToleranceDecl{{
		Subject: "net revenue", Value: &negative, WatchBand: &watch, Unit: ToleranceUnitPercentOfA,
	}}}
	got := strings.Join(ValidateDeclarations(d), "\n")
	if !strings.Contains(got, "a tolerance of -0.5 percent_of_a is not a tolerance — the number must be above zero") {
		t.Errorf("negative tolerance was not refused in the requested words: %s", got)
	}
	if !strings.Contains(got, "watch band") {
		t.Errorf("negative watch band was accepted: %s", got)
	}
	if loose := LoosenedTolerances(ProjectDeclarations{Tolerances: []ToleranceDecl{{Subject: "net revenue", Value: f64(0.5), Unit: ToleranceUnitPercentOfA}}}, []map[string]string{{"measure": "net revenue", "value": "-0.5", "unit": ToleranceUnitPercentOfA}}, "measure", "value", "unit"); !containsSubstring(loose, "number must be above zero") {
		t.Errorf("tightening treated a non-positive value as tighter: %v", loose)
	}
}

// TestPrefillFromTheNewGroups is the mapping an audit asked for by name: the
// recipe forms start from what the project declared instead of empty.
func TestPrefillFromTheNewGroups(t *testing.T) {
	d, err := ParseDeclarations(JSON(round2Declarations))
	if err != nil {
		t.Fatal(err)
	}
	got := PrefillSlots(d)

	for slot, want := range map[string]string{
		"measures": `[{"measure":"net revenue","definitionA":"Finance: sum(amount_usd) after refunds","definitionB":"Sales: sum(Amount) on closed-won"}]`,
		// The tolerance is declared against the KPI's id and prefills under its
		// NAME: the column is hinted "one of the measures above", and the
		// measures table above says "net revenue". Two spellings of one measure
		// on one screen is the vocabulary difference this recipe exists to find.
		"tolerances":       `[{"measure":"net revenue","unit":"percent_of_a","value":"0.05","segmentValue":"250","setBy":"Sofia, 2026-08-21"}]`,
		"knownDifferences": `[{"name":"internal test accounts","side":"A","filter":"account_type = 'internal'","owner":"Finance ops"}]`,
		"features":         `[{"feature":"payment_type","severity":"major","statistic":"category-emergence","tolerance":"2","unit":"points_of_share","watchBand":"1","owner":"Dev"}]`,
		"successMetric":    `[{"metric":"PR-AUC","threshold":">= 0.65","operatingPoint":"top 2% flagged","uncertaintyMethod":"bootstrap, 1000 resamples","worstSliceFloor":"0.45 on region"}]`,
	} {
		if got[slot].Value != want {
			t.Errorf("prefill[%s]\n got %s\nwant %s", slot, got[slot].Value, want)
		}
	}

	// A KPI's tolerance prefills the reconciliation; everything else prefills
	// the shift audit. One registry, two slots, decided by the KPI dictionary.
	if strings.Contains(got["features"].Value, "net_revenue") {
		t.Error("a KPI's tolerance leaked into the shift audit's feature table")
	}
	if strings.Contains(got["tolerances"].Value, "payment_type") {
		t.Error("a dataset feature's tolerance leaked into the reconciliation's measure table")
	}

	// The two dashboards become source A and source B, with the read path that
	// makes "what feeds dashboard A" answerable.
	if !strings.Contains(got["sourceA"].Value, "analytics.revenue_daily") ||
		!strings.Contains(got["sourceA"].Value, "read via SQL") {
		t.Errorf("sourceA = %q", got["sourceA"].Value)
	}
	if got["sourceA"].Source != "dashboards[0]" {
		t.Errorf("sourceA provenance = %q", got["sourceA"].Source)
	}
	if !strings.Contains(got["sourceB"].Value, "exports/sf.csv") {
		t.Errorf("sourceB = %q", got["sourceB"].Value)
	}

	if !strings.Contains(got["period"].Value, "Q2 = Apr 1 - Jun 30") ||
		!strings.Contains(got["period"].Value, "America/New_York") {
		t.Errorf("period = %q", got["period"].Value)
	}
	// The evalset prefill pins BYTES: a hash and a freeze date, plus custody.
	for _, want := range []string{"churn-holdout (holdout)", "96417 rows", "sha256 9b74e2c1", "frozen 2025-05-02",
		"Holdout custody: Dev, ML lead — no workspace-write node may read it."} {
		if !strings.Contains(got["dataDescription"].Value, want) {
			t.Errorf("dataDescription prefill never says %q:\n%s", want, got["dataDescription"].Value)
		}
	}
}

// TestPrefillIsAbsentWhenNothingIsDeclared: a key in the map means "the project
// declared this", so a project with empty groups must not fill a required slot
// with an empty table.
func TestPrefillIsAbsentWhenNothingIsDeclared(t *testing.T) {
	got := PrefillSlots(ProjectDeclarations{
		KPIs: []KPIDecl{{}}, Tolerances: []ToleranceDecl{{}}, Metrics: []MetricDecl{{}},
		Exclusions: []ExclusionDecl{{}}, Dashboards: []DashboardDecl{{}},
	})
	for _, slot := range []string{"measures", "tolerances", "features", "successMetric",
		"knownDifferences", "sourceA", "period", "dataDescription"} {
		if v, ok := got[slot]; ok {
			t.Errorf("slot %q was prefilled from an empty declaration: %q", slot, v.Value)
		}
	}
}

// TestRefuseLoosenedTolerancesAtInstantiation walks the rule through the store:
// a project declares 2 points of share for payment_type, and a run asking for 5
// is refused before a draft exists.
func TestRefuseLoosenedTolerancesAtInstantiation(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	id := "decl2-" + uniq()
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, id) })
	if _, err := st.CreateProject(ctx, Project{
		ID: id, Name: "Round two", Summary: "What this project is for.",
		Declarations: JSON(round2Declarations),
	}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	slot := ContextSlot{ID: "features", Type: SlotTypeTable, Required: true, Columns: []SlotColumn{
		{ID: "feature", Label: "Feature"}, {ID: "tolerance", Label: "Tolerance"}, {ID: "unit", Label: "Unit"},
	}}
	slots := []ContextSlot{slot}

	tighter := map[string]string{"features": `[{"feature":"payment_type","tolerance":"1","unit":"points_of_share"}]`}
	if err := st.RefuseLoosenedTolerances(ctx, id, slots, tighter); err != nil {
		t.Errorf("tightening was refused: %v", err)
	}
	wider := map[string]string{"features": `[{"feature":"payment_type","tolerance":"5","unit":"points_of_share"}]`}
	err := st.RefuseLoosenedTolerances(ctx, id, slots, wider)
	if err == nil {
		t.Fatal("a run widened a standing tolerance and nothing objected")
	}
	if !strings.Contains(err.Error(), "never loosen it") {
		t.Errorf("the refusal does not say the rule: %v", err)
	}
	// A run under no project, or under a project that declares nothing, is
	// untouched: the rule is about the intersection.
	if err := st.RefuseLoosenedTolerances(ctx, "", slots, wider); err != nil {
		t.Errorf("an unscoped run was refused: %v", err)
	}
	if err := st.RefuseLoosenedTolerances(ctx, "no-such-project-"+uniq(), slots, wider); err != nil {
		t.Errorf("a run under an unregistered project was refused: %v", err)
	}
}

// TestRound2GroupsRoundTripThroughTheColumn stores every new group in Postgres
// and reads it back, because "the JSONB document is extensible" is a claim
// about the column, not about the struct.
func TestRound2GroupsRoundTripThroughTheColumn(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	id := "decl3-" + uniq()
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, id) })
	if _, err := st.CreateProject(ctx, Project{
		ID: id, Name: "Round two", Declarations: JSON(round2Declarations),
	}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	// An edit from a form that only knows about datasets — the round-1 form.
	patch := JSON(`{"datasets":[{"name":"trips","location":"data/trips.csv"}]}`)
	if _, err := st.UpdateProject(ctx, id, ProjectPatch{Declarations: &patch}); err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	got, err := st.GetProject(ctx, id)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	d, err := ParseDeclarations(got.Declarations)
	if err != nil {
		t.Fatalf("ParseDeclarations: %v", err)
	}
	if len(d.KPIs) != 1 || len(d.Evalsets) != 1 || len(d.Tolerances) != 2 ||
		d.Calendar.Timezone == "" || d.RegressionPolicy.EvalsetID == "" {
		t.Fatalf("an edit from a form that knows only about datasets destroyed the round-2 groups: %s", got.Declarations)
	}
	if len(d.Datasets) != 1 || d.Datasets[0].Location != "data/trips.csv" {
		t.Errorf("the edited group did not land: %+v", d.Datasets)
	}

	detail, err := st.GetProjectDetail(ctx, id)
	if err != nil {
		t.Fatalf("GetProjectDetail: %v", err)
	}
	if detail.SlotDefaults["successMetric"].Value == "" {
		t.Error("the detail endpoint hands the recipe form no metric table")
	}
	if detail.SlotDefaults["measures"].Source != "kpis[]" {
		t.Errorf("measures provenance = %q", detail.SlotDefaults["measures"].Source)
	}
}

// TestEvalsetRowCountSurvivesAsANumber guards the one field that is a pointer
// to an int64 rather than a string: a row count that round-trips as 9.6417e+04
// is a row count nobody can join on.
func TestEvalsetRowCountSurvivesAsANumber(t *testing.T) {
	d, err := ParseDeclarations(JSON(round2Declarations))
	if err != nil {
		t.Fatal(err)
	}
	if d.Evalsets[0].RowCount == nil || *d.Evalsets[0].RowCount != int64(96417) {
		t.Fatalf("rowCount: %+v", d.Evalsets[0].RowCount)
	}
	out, err := json.Marshal(d.Evalsets[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"rowCount":96417`) {
		t.Errorf("rowCount was re-marshalled as something else: %s", out)
	}
	_ = i64(1)
}
