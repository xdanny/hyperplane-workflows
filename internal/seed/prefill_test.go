package seed_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/seed"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// declaredProject is a project that declares something for every table slot the
// recipes inherit — the analyst's dictionary and the ML records in one document,
// as a real project under both kinds of work would carry them.
func declaredProject() store.ProjectDeclarations {
	f := func(v float64) *float64 { return &v }
	return store.ProjectDeclarations{
		KPIs: []store.KPIDecl{{
			ID: "net_revenue", Name: "net revenue", Owner: "Finance ops",
			Definitions: map[string]string{
				"Finance": "sum(amount_usd) after refunds",
				"Sales":   "sum(Amount) on closed-won",
			},
		}},
		Dashboards: []store.DashboardDecl{
			{Name: "Finance revenue", Source: "analytics.revenue_daily", ReadPath: "SQL at invoice-line level"},
			{Name: "Sales pipeline", Source: "exports/sf.csv", ReadPath: "duckdb"},
		},
		Tolerances: []store.ToleranceDecl{
			{Subject: "net_revenue", Statistic: "total", Value: f(0.05), Unit: store.ToleranceUnitPercentOfA,
				SegmentValue: f(250), Owner: "Sofia", SetAt: "2026-08-21"},
			{Subject: "payment_type", Statistic: "category-emergence", Value: f(2),
				Unit: store.ToleranceUnitPointsOfShare, WatchBand: f(1), Severity: "major", Owner: "Dev"},
		},
		Exclusions: []store.ExclusionDecl{{
			Name: "internal test accounts", Side: "A", Filter: "account_type = 'internal'", Owner: "Finance ops",
		}},
		Metrics: []store.MetricDecl{{
			Name: "PR-AUC", Threshold: ">= 0.65", OperatingPoint: "top 2% flagged",
			UncertaintyMethod: "bootstrap, 1000 resamples",
		}},
		Slices: []store.SliceDecl{{Column: "region", Floor: f(0.45)}},
	}
}

// TestPrefillMatchesSlotColumns holds the two halves of an inherited table
// together: internal/store builds the prefill rows, internal/seed declares the
// columns, and neither imports the other.
//
// It matters for one visible reason. The form marks a slot "inherited from
// project" only while its value is still EXACTLY the declared one, so a prefill
// whose cells are in a different order than the slot's columns — or which uses a
// key that is not a column — would arrive already looking edited, and a column
// renamed here would silently stop being filled in. The failure is invisible on
// screen; it fails here instead.
func TestPrefillMatchesSlotColumns(t *testing.T) {
	prefill := store.PrefillSlots(declaredProject())

	slots := map[string]store.ContextSlot{}
	for _, tmpl := range seed.DefaultTemplates {
		for _, s := range tmpl.ContextSlots {
			if s.SlotType() == store.SlotTypeTable {
				slots[s.ID] = s
			}
		}
	}

	// Every table slot the inheritance table knows about must exist on a recipe,
	// and be filled in by this project.
	for _, id := range []string{"features", "measures", "tolerances", "knownDifferences", "successMetric"} {
		slot, ok := slots[id]
		if !ok {
			t.Errorf("no recipe declares a table slot %q, but the project inheritance fills one", id)
			continue
		}
		got, ok := prefill[id]
		if !ok {
			t.Errorf("slot %q inherited nothing from a project that declares it", id)
			continue
		}
		t.Run(id, func(t *testing.T) {
			// Canonical under the REAL columns: cells in column order, so the
			// value the form receives is byte-for-byte the value it would store.
			if canon := store.NormalizeSlotValue(slot, got.Value); canon != got.Value {
				t.Errorf("the prefill is not canonical under this slot's columns\n got %s\nwant %s", got.Value, canon)
			}
			// Every key is a declared column: a prefill cell with no column is a
			// cell the form cannot render and the person cannot correct.
			var rows []map[string]json.RawMessage
			if err := json.Unmarshal([]byte(got.Value), &rows); err != nil {
				t.Fatalf("prefill is not a JSON array of rows: %v (%s)", err, got.Value)
			}
			cols := map[string]bool{}
			for _, c := range slot.Columns {
				cols[c.ID] = true
			}
			for _, row := range rows {
				for k := range row {
					if !cols[k] {
						t.Errorf("prefill writes %q, which slot %q has no column for", k, id)
					}
				}
			}
			// And the carried text starts with the first column, which is what
			// the eval-gate comparator reads out of the metric table.
			text := store.SlotTableText(slot, got.Value)
			if !strings.HasPrefix(text, "1. "+slot.Columns[0].ID+": ") {
				t.Errorf("the carried text does not lead with %q:\n%s", slot.Columns[0].ID, text)
			}
		})
	}

	// The metric table's carried text is what the platform's eval-gate parses
	// the declared metric name and threshold out of — it must lead with both.
	metric := store.SlotTableText(slots["successMetric"], prefill["successMetric"].Value)
	if !strings.HasPrefix(metric, "1. metric: PR-AUC | threshold: >= 0.65") {
		t.Errorf("the metric table's carried text does not lead with the ruler and its number:\n%s", metric)
	}
}

// TestEveryInheritableSlotIsARealSlot: a row in the inheritance table naming a
// slot no recipe declares is a prefill nobody will ever see, and reads as
// coverage that does not exist.
func TestEveryInheritableSlotIsARealSlot(t *testing.T) {
	declared := map[string]bool{}
	for _, tmpl := range seed.DefaultTemplates {
		for _, s := range tmpl.ContextSlots {
			declared[s.ID] = true
		}
	}
	for _, id := range store.InheritableSlots() {
		if !declared[id] {
			t.Errorf("the project inheritance fills slot %q, which no recipe declares", id)
		}
	}
}

// TestTheSeededTaxiProjectNeverOffersTheLookupAsAWindow is D-16a against the
// document a fresh install actually ships.
//
// Selecting Taxi Analytics on a dataset-shift-audit form prefilled the CURRENT
// WINDOW with "TLC taxi zone lookup (data/taxi_zone_lookup.csv)" — 265 rows of
// reference data offered as the window under test against 24M rows of trips,
// because inheritance took datasets[0] and datasets[1] in declaration order.
// The form said "inherited from project", the counter said ready, and the
// inputs gate passed. Nothing downstream could catch it: both sides were real
// files, so the audit would have compared a zone table against a trip feed and
// reported every feature as shifted.
func TestTheSeededTaxiProjectNeverOffersTheLookupAsAWindow(t *testing.T) {
	var taxi seed.ExampleProject
	for _, p := range seed.DefaultProjects {
		if p.ID == "taxi-analytics" {
			taxi = p
		}
	}
	if taxi.Declarations == "" {
		t.Fatal("the seeded taxi-analytics project declares nothing")
	}
	decl, err := store.ParseDeclarations(store.JSON(taxi.Declarations))
	if err != nil {
		t.Fatalf("the seeded declaration document does not parse: %v", err)
	}
	if problems := store.ValidateDeclarations(decl); len(problems) != 0 {
		t.Errorf("the seeded document would be refused on save: %v", problems)
	}

	prefill := store.PrefillSlots(decl)
	for _, slot := range []string{"baselineData", "currentData", "sourceA", "sourceB"} {
		if v := prefill[slot].Value; strings.Contains(v, "taxi_zone_lookup") {
			t.Errorf("slot %q inherited the zone lookup: %q", slot, v)
		}
	}
	// The window that IS offered is the trip feed; the one that is not is left
	// for the person, with the note that says why.
	if v := prefill["baselineData"].Value; !strings.Contains(v, "yellow_tripdata") {
		t.Errorf("the baseline window is not the trip feed: %q", v)
	}
	if v, ok := prefill["currentData"]; ok {
		t.Errorf("the current window was guessed: %q", v.Value)
	}
	if note := store.SlotNotes(decl)["currentData"]; !strings.Contains(note, "which period is under test") {
		t.Errorf("the empty current window carries no note saying why: %q", note)
	}

	// M-15: the location leads as a bare token, so the recipe's own header
	// check can open the file the project declared.
	if v := prefill["dataset"].Value; !strings.HasPrefix(v, "data/yellow_tripdata_2025-*.csv") {
		t.Errorf("the inherited dataset value does not lead with the path a gate can tokenise: %q", v)
	}
	// M-20: the arrival column and the timezone reach a slot at last.
	beliefs := prefill["expectations"].Value
	for _, want := range []string{"tpep_pickup_datetime", "America/New_York"} {
		if !strings.Contains(beliefs, want) {
			t.Errorf("the project's typed beliefs never mention %q:\n%s", want, beliefs)
		}
	}
}
