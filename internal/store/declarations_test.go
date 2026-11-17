package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestMergeDeclarationsKeepsUnknownGroups is the extensibility guarantee in
// test form. Two more personas will add groups to this document (kpis[],
// evalsets[]); if a client that predates them can wipe them by saving the
// datasets editor, the document is not extensible, it is a race.
func TestMergeDeclarationsKeepsUnknownGroups(t *testing.T) {
	existing := JSON(`{"kpis":[{"id":"conversion","target":0.12}],` +
		`"datasets":[{"name":"old"}],"environment":{"where":"laptop"}}`)
	patch := JSON(`{"datasets":[{"name":"new","location":"data/new.csv"}]}`)

	merged, err := MergeDeclarations(existing, patch)
	if err != nil {
		t.Fatalf("MergeDeclarations: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("merged document is not an object: %v (%s)", err, merged)
	}
	if _, ok := got["kpis"]; !ok {
		t.Errorf("a group this build has never heard of was destroyed by an edit: %s", merged)
	}
	if string(got["kpis"]) != `[{"id":"conversion","target":0.12}]` {
		t.Errorf("unknown group was rewritten rather than left alone: %s", got["kpis"])
	}
	if _, ok := got["environment"]; !ok {
		t.Errorf("a group the patch did not mention was dropped: %s", merged)
	}

	decl, err := ParseDeclarations(merged)
	if err != nil {
		t.Fatalf("ParseDeclarations: %v", err)
	}
	if len(decl.Datasets) != 1 || decl.Datasets[0].Name != "new" {
		t.Errorf("the patched group did not replace the old one: %+v", decl.Datasets)
	}
}

// TestMergeDeclarationsNullRemovesAGroup: a group is deleted by naming it null,
// which is the only way to say "this project no longer declares that" without
// resending every other group.
func TestMergeDeclarationsNullRemovesAGroup(t *testing.T) {
	merged, err := MergeDeclarations(JSON(`{"datasets":[{"name":"x"}],"purpose":{"owner":"p"}}`),
		JSON(`{"datasets":null}`))
	if err != nil {
		t.Fatalf("MergeDeclarations: %v", err)
	}
	decl, err := ParseDeclarations(merged)
	if err != nil {
		t.Fatalf("ParseDeclarations: %v", err)
	}
	if len(decl.Datasets) != 0 {
		t.Errorf("null did not remove the group: %s", merged)
	}
	if decl.Purpose.Owner != "p" {
		t.Errorf("removing one group disturbed another: %s", merged)
	}
}

// TestMergeDeclarationsRefusesNonObjects: silently replacing a document it
// cannot read is how a declaration set disappears without anyone noticing.
func TestMergeDeclarationsRefusesNonObjects(t *testing.T) {
	if _, err := MergeDeclarations(JSON(`[1,2,3]`), JSON(`{}`)); err == nil {
		t.Error("a stored value that is not an object was merged into rather than refused")
	}
	if _, err := MergeDeclarations(JSON(`{}`), JSON(`"nope"`)); err == nil {
		t.Error("a patch that is not an object was accepted")
	}
}

// taxiDeclarations is the shape the seeded taxi-analytics project carries: one
// dataset with no natural key, a candidate, a safe-use boundary and an
// environment — the facts that were re-typed into seven slots across one
// project's runs, two of which then disagreed.
const taxiDeclarations = `{
  "purpose": {"owner": "the analytics lead", "doneMeans": "the monthly mart is accepted"},
  "datasets": [
    {
      "id": "yellow-trips",
      "name": "NYC TLC yellow-cab monthly trip drops",
      "location": "data/yellow_tripdata_2025-*.csv",
      "format": "csv",
      "grain": "one row per completed trip, as landed in the monthly drop",
      "naturalKey": "none",
      "keyCandidate": ["VendorID", "tpep_pickup_datetime", "PULocationID"],
      "kind": "fact",
      "arrivalTimestampColumn": "tpep_pickup_datetime",
      "timezone": "America/New_York",
      "safeUse": "aggregate reporting only, never per-rider decisions",
      "expectedRowCount": 24083384
    },
    {"id": "zones", "name": "TLC zone lookup", "location": "data/taxi_zone_lookup.csv",
     "format": "csv", "kind": "lookup", "keyColumns": ["LocationID"]}
  ],
  "environment": {"where": "a local workspace with duckdb", "runtimeBudgetDefault": "inside 20 minutes"}
}`

// TestPrefillSlots is the slot→declaration mapping. Every row it asserts is a
// slot a person was previously asked to re-type from memory.
func TestPrefillSlots(t *testing.T) {
	decl, err := ParseDeclarations(JSON(taxiDeclarations))
	if err != nil {
		t.Fatalf("ParseDeclarations: %v", err)
	}
	got := PrefillSlots(decl)

	want := map[string]string{
		// The baseline window is the FACT table, and the location LEADS as a
		// bare token so the recipe's own header check can open it (M-15).
		"baselineData":  trips,
		"safeUse":       "From the data's safe use: aggregate reporting only, never per-rider decisions",
		"environment":   "a local workspace with duckdb",
		"runtimeBudget": "inside 20 minutes",
		// The whole point of M-8: the key reaches the typed slot in the exact
		// form the slot takes, `none` first because this data has no natural key.
	}
	for slot, wantValue := range want {
		p, ok := got[slot]
		if !ok {
			t.Errorf("slot %q inherited nothing from the project", slot)
			continue
		}
		if p.Value != wantValue {
			t.Errorf("slot %q inherited %q, want %q", slot, p.Value, wantValue)
		}
		if p.Source == "" {
			t.Errorf("slot %q inherited a value with no declaration path to attribute it to", slot)
		}
	}
	if got := PrefillSlotsFor(decl, "data-quality-audit")["keyColumns"].Value; got != "none, VendorID, tpep_pickup_datetime, PULocationID" {
		t.Errorf("dataset-grain audit keyColumns = %q", got)
	}
	if _, ok := PrefillSlotsFor(decl, "data-pipeline")["keyColumns"]; ok {
		t.Error("a source dataset key inherited into a target mart")
	}
	// Both dataset-list slots carry every declared dataset, not just the first.
	for _, slot := range []string{"sources", "dataset"} {
		p, ok := got[slot]
		if !ok {
			t.Fatalf("slot %q inherited nothing", slot)
		}
		if want := trips + "; " + zones; p.Value != want {
			t.Errorf("slot %q = %q, want %q", slot, p.Value, want)
		}
	}

	// D-16a / M-16. The zone lookup is 265 rows of reference data. It was
	// offered as the CURRENT WINDOW of a 24M-row trip feed because inheritance
	// took datasets[0] and datasets[1] in declaration order, the form said
	// "inherited from project", and the inputs gate passed.
	if p, ok := got["currentData"]; ok {
		t.Errorf("the current window was prefilled with %q — nothing declared says which period is under test", p.Value)
	}
	if p := got["baselineData"].Value; strings.Contains(p, "zone_lookup") {
		t.Errorf("the baseline window is the zone lookup: %q", p)
	}
	// Neither reconciliation source falls back to a dataset: two unrelated
	// datasets are not two dashboards' sources.
	for _, slot := range []string{"sourceA", "sourceB"} {
		if p, ok := got[slot]; ok {
			t.Errorf("slot %q inherited %q from a project that declares no dashboards", slot, p.Value)
		}
	}
	// The grain of a source dataset is not the grain of a mart, so it is scoped
	// to the recipe where the two are the same question.
	for _, slot := range []string{"grain", "sharedGrain"} {
		if p, ok := got[slot]; ok {
			t.Errorf("slot %q inherited %q unscoped — a source's grain is not a target model's", slot, p.Value)
		}
	}
	if p, ok := PrefillSlotsFor(decl, "data-quality-audit")["grain"]; !ok {
		t.Error("the audited dataset's grain does not reach data-quality-audit, where it IS the dataset's grain")
	} else if p.Value != "one row per completed trip, as landed in the monthly drop" {
		t.Errorf("grain = %q", p.Value)
	}

	// M-20. The arrival column, the timezone and the expected row count were
	// typed on the project form and reached no slot at all — while the audit's
	// own criterion names the first two as its commonest evidence gaps.
	beliefs, ok := got["expectations"]
	if !ok {
		t.Fatal("the project's typed beliefs reach no slot")
	}
	for _, want := range []string{
		"arrival column: tpep_pickup_datetime",
		"timezone: America/New_York",
		"expected rows: 24,083,384",
	} {
		if !strings.Contains(beliefs.Value, want) {
			t.Errorf("the inherited beliefs never say %q:\n%s", want, beliefs.Value)
		}
	}

	// And the note that replaced the guess.
	notes := SlotNotes(decl)
	if !strings.Contains(notes["currentData"], "pick the window from the project's data") {
		t.Errorf("the empty current window carries no note: %q", notes["currentData"])
	}
}

// The two dataset renderings the assertions above are written against.
const (
	trips = "data/yellow_tripdata_2025-*.csv — NYC TLC yellow-cab monthly trip drops " +
		"(csv, fact, arrival tpep_pickup_datetime, times in America/New_York)"
	zones = "data/taxi_zone_lookup.csv — TLC zone lookup (csv, lookup)"
)

// TestBothWindowsComeFromOneDatasetWhenItCarriesBothPeriods: a shift audit
// compares two windows of the SAME data. When the project says so, both windows
// inherit from that dataset with their own periods — which is the answer
// positional inheritance could never give, however many datasets were declared.
func TestBothWindowsComeFromOneDatasetWhenItCarriesBothPeriods(t *testing.T) {
	decl, err := ParseDeclarations(JSON(`{"datasets":[
	  {"name":"zones","location":"data/zones.csv","kind":"lookup"},
	  {"name":"trips","location":"data/trips.csv","kind":"fact","window":"both",
	   "baselinePeriod":"2025-01..03","currentPeriod":"2025-04..06"}
	]}`))
	if err != nil {
		t.Fatalf("ParseDeclarations: %v", err)
	}
	got := PrefillSlots(decl)
	if want := "data/trips.csv — trips (fact) — 2025-01..03"; got["baselineData"].Value != want {
		t.Errorf("baselineData = %q, want %q", got["baselineData"].Value, want)
	}
	if want := "data/trips.csv — trips (fact) — 2025-04..06"; got["currentData"].Value != want {
		t.Errorf("currentData = %q, want %q", got["currentData"].Value, want)
	}
	if len(SlotNotes(decl)) > 0 && SlotNotes(decl)["currentData"] != "" {
		t.Error("a settled current window still carries a note asking the person to pick one")
	}
}

// TestALookupIsNeverAWindow: the declared role is the whole point. A lookup
// stays out of both windows however few other datasets there are.
func TestALookupIsNeverAWindow(t *testing.T) {
	decl, err := ParseDeclarations(JSON(
		`{"datasets":[{"name":"zones","location":"data/zones.csv","kind":"lookup"}]}`))
	if err != nil {
		t.Fatalf("ParseDeclarations: %v", err)
	}
	got := PrefillSlots(decl)
	for _, slot := range []string{"baselineData", "currentData"} {
		if p, ok := got[slot]; ok {
			t.Errorf("slot %q inherited the lookup: %q", slot, p.Value)
		}
	}
	// It is still a dataset, so the list slots still carry it.
	if got["dataset"].Value == "" {
		t.Error("a lookup vanished from the dataset list as well")
	}
}

// TestPrefillSlotsOmitsWhatIsNotDeclared: "in the map" must mean "the project
// declared this", so a form can tell an inherited value from a blank one.
func TestPrefillSlotsOmitsWhatIsNotDeclared(t *testing.T) {
	decl, err := ParseDeclarations(JSON(`{"datasets":[{"name":"just a name"}]}`))
	if err != nil {
		t.Fatalf("ParseDeclarations: %v", err)
	}
	got := PrefillSlots(decl)
	if _, ok := got["safeUse"]; ok {
		t.Error("safeUse was offered as inherited when no dataset declares one")
	}
	if _, ok := got["keyColumns"]; ok {
		t.Error("keyColumns was offered as inherited when no dataset declares a key")
	}
	// A dataset with ONLY a name is a complete entry — that is the non-engineer
	// path, and it must still supply the dataset slots.
	if got["dataset"].Value != "just a name" {
		t.Errorf("a name-only dataset did not reach the dataset slot: %+v", got["dataset"])
	}
}

// TestDatasetKeyValue covers the two legal shapes of a key declaration, and the
// one illegal one: a dataset that declares neither offers nothing rather than
// offering an empty answer that would sail through a presence check.
func TestDatasetKeyValue(t *testing.T) {
	cases := []struct {
		name string
		ds   DatasetDecl
		want string
	}{
		{"real key", DatasetDecl{KeyColumns: []string{"order_date", " store_id "}}, "order_date, store_id"},
		{"no natural key", DatasetDecl{NaturalKey: "none", KeyCandidate: []string{"VendorID", "pickup_time"}},
			"none, VendorID, pickup_time"},
		{"NONE is the same flag", DatasetDecl{NaturalKey: "NONE", KeyCandidate: []string{"a"}}, "none, a"},
		{"a real key wins over a stale none flag",
			DatasetDecl{NaturalKey: "none", KeyColumns: []string{"id"}}, "id"},
		{"nothing declared", DatasetDecl{Name: "x"}, ""},
		{"none with no candidate is still none", DatasetDecl{NaturalKey: "none"}, "none"},
	}
	for _, c := range cases {
		if got := DatasetKeyValue(c.ds); got != c.want {
			t.Errorf("%s: DatasetKeyValue = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestInheritableSlotsAreDistinct guards the one table the whole feature reads:
// a duplicated slot id would mean two rows silently competing for one slot.
func TestInheritableSlotsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, id := range InheritableSlots() {
		if seen[id] {
			t.Errorf("slot %q appears twice in slotInheritance — two rows compete for one slot", id)
		}
		seen[id] = true
	}
	if len(seen) == 0 {
		t.Fatal("slotInheritance is empty — nothing can be inherited")
	}
}

// ── typed slot values ───────────────────────────────────────────────────────

// TestNormalizeSlotValueList is the serialization contract for a `list` slot:
// however it is typed, pasted or newline-separated, the process receives one
// comma-separated string — which is why no process instruction had to change
// when the slot became typed.
func TestNormalizeSlotValueList(t *testing.T) {
	list := ContextSlot{ID: "keyColumns", Type: SlotTypeList}
	cases := []struct{ in, want string }{
		{"order_date, store_id", "order_date, store_id"},
		{"order_date,store_id", "order_date, store_id"},
		{"  order_date ,  store_id  ", "order_date, store_id"},
		{"order_date\nstore_id", "order_date, store_id"},
		{"order_date; store_id", "order_date, store_id"},
		{"a,,b", "a, b"},
		// `none` is a member, never an empty value: this is the owner declaring
		// there is no natural key and naming the closest candidate.
		{"none, VendorID, pickup_time", "none, VendorID, pickup_time"},
		{"", ""},
		{"  ,  ", ""},
	}
	for _, c := range cases {
		if got := NormalizeSlotValue(list, c.in); got != c.want {
			t.Errorf("NormalizeSlotValue(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// A text slot is left exactly as typed — commas in prose are prose.
	text := ContextSlot{ID: "grain"}
	if got := NormalizeSlotValue(text, " one row per order, per day "); got != "one row per order, per day" {
		t.Errorf("a text slot was split on its commas: %q", got)
	}
}

func TestSlotListValues(t *testing.T) {
	list := ContextSlot{ID: "keyColumns", Type: SlotTypeList}
	got := SlotListValues(list, "none, VendorID,  pickup_time")
	want := []string{"none", "VendorID", "pickup_time"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SlotListValues = %#v, want %#v", got, want)
	}
	if SlotListValues(ContextSlot{ID: "grain"}, "a, b") != nil {
		t.Error("a text slot was split into list members")
	}
	if SlotListValues(list, "   ") != nil {
		t.Error("an empty list slot produced members")
	}
}

// TestFilterSlotValuesNormalizesLists: the value that reaches the run's inputs
// is the normalized one, so a pasted CSV and a tag list are indistinguishable
// by the time a gate reads them.
func TestFilterSlotValuesNormalizesLists(t *testing.T) {
	slots := []ContextSlot{
		{ID: "keyColumns", Type: SlotTypeList, Required: true},
		{ID: "grain"},
	}
	got := FilterSlotValues(slots, map[string]string{
		"keyColumns": "order_date\nstore_id\n",
		"grain":      " one row per order per day ",
		"undeclared": "dropped",
	})
	if got["keyColumns"] != "order_date, store_id" {
		t.Errorf("keyColumns = %q, want the comma-separated form", got["keyColumns"])
	}
	if got["grain"] != "one row per order per day" {
		t.Errorf("grain = %q", got["grain"])
	}
	if _, ok := got["undeclared"]; ok {
		t.Error("an undeclared key survived — a recipe's contract is its slot list")
	}
}

// TestSlotDefaults: an unset Type/Audience means text/owner, so every recipe
// written before these fields existed keeps behaving exactly as it did.
func TestSlotDefaults(t *testing.T) {
	s := ContextSlot{ID: "market"}
	if s.SlotType() != SlotTypeText {
		t.Errorf("SlotType() = %q, want text", s.SlotType())
	}
	if s.SlotAudience() != SlotAudienceOwner {
		t.Errorf("SlotAudience() = %q, want owner", s.SlotAudience())
	}
}

// TestSafeUseFallsBackToProjectConstraints: a project whose purpose states what
// its numbers may not be used for was stating the `safeUse` slot's answer, and
// every run under it was asked the question again because only the dataset-level
// field was wired up. The dataset's own answer still wins when there is one.
func TestSafeUseFallsBackToProjectConstraints(t *testing.T) {
	constraintsOnly := ProjectDeclarations{
		Purpose:  PurposeDecl{Constraints: "Never used to judge an individual driver."},
		Datasets: []DatasetDecl{{Name: "trips"}},
	}
	got := PrefillSlots(constraintsOnly)["safeUse"]
	if got.Value != "From this project's constraints: Never used to judge an individual driver." {
		t.Errorf("safeUse = %q, want the project's constraints", got.Value)
	}
	if got.Source != "purpose.constraints" {
		t.Errorf("safeUse source = %q, want purpose.constraints — the form shows where a value came from", got.Source)
	}

	datasetWins := ProjectDeclarations{
		Purpose:  PurposeDecl{Constraints: "Never used to judge an individual driver."},
		Datasets: []DatasetDecl{{Name: "trips", SafeUse: "Aggregate reporting only."}},
	}
	got = PrefillSlots(datasetWins)["safeUse"]
	if got.Value != "From this project's constraints: Never used to judge an individual driver.\nFrom the data's safe use: Aggregate reporting only." {
		t.Errorf("safeUse = %q, want both labelled sources", got.Value)
	}
	if got.Source != "datasets[].safeUse + purpose.constraints" {
		t.Errorf("safeUse source = %q, want both declaration paths", got.Source)
	}

	if _, ok := PrefillSlots(ProjectDeclarations{Datasets: []DatasetDecl{{Name: "trips"}}})["safeUse"]; ok {
		t.Error("safeUse was prefilled from a project that declares neither — an absent declaration must never satisfy a required slot")
	}
}

// TestPrefilledMeasuresUseOneName: the Measures table and the Tolerance table
// are prefilled from the same dictionary, and the Tolerance table's own column
// hint says "one of the measures above". A tolerance written against the KPI's
// id used to carry the id into a table full of names, so one measure appeared
// under two spellings on one screen.
func TestPrefilledMeasuresUseOneName(t *testing.T) {
	d := ProjectDeclarations{
		KPIs: []KPIDecl{{
			ID:          "net_revenue",
			Name:        "net revenue",
			Definitions: map[string]string{"Finance": "sum(amount_usd) after refunds"},
		}},
		Tolerances: []ToleranceDecl{{Subject: "net_revenue", Unit: "%", Value: ptrFloat(0.5)}},
	}
	prefill := PrefillSlots(d)
	measures := prefill["measures"].Value
	tolerances := prefill["tolerances"].Value
	if !strings.Contains(measures, `"measure":"net revenue"`) {
		t.Fatalf("measures = %s, want the KPI's declared name", measures)
	}
	if !strings.Contains(tolerances, `"measure":"net revenue"`) {
		t.Errorf("tolerances = %s, want the same name the measures table uses (not the id)", tolerances)
	}
}

func ptrFloat(v float64) *float64 { return &v }
