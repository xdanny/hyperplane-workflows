package store

import (
	"encoding/json"
	"strings"
	"testing"
)

// featuresSlot is the shape three reviewers asked for: a row per feature, with
// the tolerance and its unit as their own cells.
var featuresSlot = ContextSlot{
	ID:       "features",
	Label:    "Features that matter, and how much movement is too much",
	Required: true,
	Type:     SlotTypeTable,
	Columns: []SlotColumn{
		{ID: "feature", Label: "Feature", Required: true},
		{ID: "type", Label: "Type", Options: []string{"numeric", "categorical", "timestamp"}, Required: true},
		{ID: "whyItMatters", Label: "Why it matters"},
		{ID: "tolerance", Label: "Tolerance", Required: true},
		{ID: "unit", Label: "Unit", Required: true},
	},
}

// TestSlotTableJSONAndTextAreTheSameRows pins the two renderings and the
// contract between them: JSON is what is STORED and edited, the numbered lines
// are what is CARRIED into the DAG, and a value that has been through one and
// back is still the same rows.
func TestSlotTableJSONAndTextAreTheSameRows(t *testing.T) {
	stored := `[{"feature":"trip_distance","type":"numeric","whyItMatters":"the fare model's strongest predictor","tolerance":"0.2","unit":"psi"},` +
		`{"feature":"payment_type","type":"categorical","tolerance":"2","unit":"points_of_share"}]`

	wantText := "1. feature: trip_distance | type: numeric | whyItMatters: the fare model's strongest predictor | tolerance: 0.2 | unit: psi\n" +
		"2. feature: payment_type | type: categorical | tolerance: 2 | unit: points_of_share"
	got := SlotTableText(featuresSlot, stored)
	if got != wantText {
		t.Fatalf("carried text\n got %q\nwant %q", got, wantText)
	}

	// text → rows → JSON is the same document it started as.
	back := SlotTableJSON(featuresSlot, SlotTableRows(featuresSlot, got))
	if back != stored {
		t.Errorf("JSON → text → JSON is not a round trip\n got %s\nwant %s", back, stored)
	}
	// And normalizing either rendering produces the canonical stored form, so a
	// value that comes back from a run can be edited in the form it was typed in.
	if n := NormalizeSlotValue(featuresSlot, got); n != stored {
		t.Errorf("normalizing the carried text\n got %s\nwant %s", n, stored)
	}
	if n := NormalizeSlotValue(featuresSlot, stored); n != stored {
		t.Errorf("normalizing the stored JSON changed it\n got %s\nwant %s", n, stored)
	}
}

// TestSlotTableJSONIsCanonical: the form compares a slot's value to the
// project's prefill string to decide whether it is still inherited, so two
// orderings of the same table would read as an edit nobody made.
func TestSlotTableJSONIsCanonical(t *testing.T) {
	shuffled := `[{"unit":"psi","tolerance":"0.2","feature":"trip_distance","type":"numeric"}]`
	want := `[{"feature":"trip_distance","type":"numeric","tolerance":"0.2","unit":"psi"}]`
	if got := NormalizeSlotValue(featuresSlot, shuffled); got != want {
		t.Errorf("cells were not put in column order\n got %s\nwant %s", got, want)
	}
	// Blank cells are dropped rather than stored as empty strings, and a row
	// with nothing in it is not a row.
	if got := NormalizeSlotValue(featuresSlot, `[{"feature":"x","unit":""},{},{"feature":"  "}]`); got != `[{"feature":"x"}]` {
		t.Errorf("blank cells and empty rows survived: %s", got)
	}
	// An empty table is as unfilled as an empty textarea — which is what lets a
	// REQUIRED table slot block a run rather than pass it with "[]".
	if got := NormalizeSlotValue(featuresSlot, `[]`); got != "" {
		t.Errorf("an empty table normalized to %q, not to nothing", got)
	}
	if missing := MissingSlots([]ContextSlot{featuresSlot}, map[string]string{"features": "[]"}); len(missing) != 1 {
		t.Error("a required table with no rows passed the missing-slot check")
	}
}

// TestSlotTableSurvivesProse: a "why it matters" cell is written by a person,
// so it will contain commas, colons and the occasional pipe. Only the field
// separator is taken away from them, and taking it away must not corrupt the
// round trip.
func TestSlotTableSurvivesProse(t *testing.T) {
	stored := NormalizeSlotValue(featuresSlot, `[{"feature":"fare_amount","type":"numeric",`+
		`"whyItMatters":"the revenue report sums it: a 3% move is $40k, and finance | ops both quote it","tolerance":"0.2","unit":"psi"}]`)
	text := SlotTableText(featuresSlot, stored)
	rows := ParseSlotTableText(featuresSlot, text)
	if len(rows) != 1 {
		t.Fatalf("prose broke the parse: %q", text)
	}
	if !strings.Contains(rows[0]["whyItMatters"], "a 3% move is $40k") {
		t.Errorf("the sentence did not survive: %q", rows[0]["whyItMatters"])
	}
	if rows[0]["tolerance"] != "0.2" || rows[0]["unit"] != "psi" {
		t.Errorf("a prose cell swallowed the cells after it: %+v", rows[0])
	}
	if strings.Contains(rows[0]["whyItMatters"], "|") {
		t.Errorf("the field separator was left inside a cell: %q", rows[0]["whyItMatters"])
	}
}

// TestTableSlotReachesTheDagAsText is the compatibility promise. Every
// instruction and every gate in the three recipes was written against a slot
// whose value was a string; typing the slot must not rewrite either.
func TestTableSlotReachesTheDagAsText(t *testing.T) {
	spec := JSON(`{"nodes":[{"id":"audit","kind":"process","with":{"features":"{slot:features}"}}]}`)
	stored := `[{"feature":"payment_type","type":"categorical","tolerance":"2","unit":"points_of_share"}]`

	out, err := FillSlots(spec, []ContextSlot{featuresSlot}, map[string]string{"features": stored})
	if err != nil {
		t.Fatalf("FillSlots: %v", err)
	}
	var got struct {
		Nodes []struct {
			With map[string]string `json:"with"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("filled spec: %v", err)
	}
	carried := got.Nodes[0].With["features"]
	if strings.HasPrefix(carried, "[") {
		t.Fatalf("the raw JSON reached the DAG instead of the carried text: %q", carried)
	}
	if carried != "1. feature: payment_type | type: categorical | tolerance: 2 | unit: points_of_share" {
		t.Errorf("carried value = %q", carried)
	}

	// A table the DAG never names still reaches the workflow, one line per row.
	unref := JSON(`{"nodes":[{"id":"root","kind":"agent","instruction":"do the thing"}]}`)
	out, err = FillSlots(unref, []ContextSlot{featuresSlot}, map[string]string{"features": stored})
	if err != nil {
		t.Fatalf("FillSlots: %v", err)
	}
	if !strings.Contains(string(out), "1. feature: payment_type") {
		t.Errorf("an unreferenced table was dropped from the context block: %s", out)
	}
}

// TestValidateContextSlotsRefusesATableWithNoColumns. A table with no column
// schema renders as a textarea and stores whatever is typed — the prose box it
// replaced, wearing a new type name.
func TestValidateContextSlotsRefusesATableWithNoColumns(t *testing.T) {
	problems := ValidateContextSlots([]ContextSlot{
		{ID: "features", Type: SlotTypeTable},
		{ID: "grain", Type: SlotTypeText, Columns: []SlotColumn{{ID: "x", Label: "X"}}},
		{ID: "dupe", Type: SlotTypeTable, Columns: []SlotColumn{{ID: "a", Label: "A"}, {ID: "a", Label: "A again"}, {ID: "b"}}},
		{ID: "dupe"},
	})
	got := strings.Join(problems, "\n")
	for _, want := range []string{
		`slot "features" is a table with no columns`,
		`slot "grain" declares columns but is a text slot`,
		`slot "dupe" declares column "a" twice`,
		`slot "dupe" column "b" has no label`,
		`slot "dupe" is declared twice`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("validation never says %q:\n%s", want, got)
		}
	}
	if p := ValidateContextSlots([]ContextSlot{featuresSlot}); len(p) != 0 {
		t.Errorf("a well-formed table slot reported problems: %v", p)
	}
}

// TestNonTableSlotsAreUntouched: `table` is a fourth type, not a rewrite of the
// three that already worked.
func TestNonTableSlotsAreUntouched(t *testing.T) {
	list := ContextSlot{ID: "keyColumns", Type: SlotTypeList}
	if got := NormalizeSlotValue(list, "VendorID; pickup_time\nPULocationID"); got != "VendorID, pickup_time, PULocationID" {
		t.Errorf("list slot = %q", got)
	}
	text := ContextSlot{ID: "grain"}
	if got := NormalizeSlotValue(text, "  one row per trip  "); got != "one row per trip" {
		t.Errorf("text slot = %q", got)
	}
	if rows := SlotTableRows(text, `[{"a":"b"}]`); rows != nil {
		t.Errorf("a text slot was read as a table: %+v", rows)
	}
	if got := SlotCarriedValue(text, "  prose  "); got != "prose" {
		t.Errorf("carried text slot = %q", got)
	}
}
