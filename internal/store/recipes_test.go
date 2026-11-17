package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

var testSlots = []ContextSlot{
	{ID: "market", Label: "Market", Required: true, Example: "time tracking"},
	{ID: "audience", Label: "Audience", Required: true},
	{ID: "competitorsHint", Label: "Competitors you already know"},
}

var testCriteria = []Criterion{
	{ID: "sourced-competitor-set", Title: "Every competitor is real and sourced"},
	{ID: "judged-concepts", Title: "Concepts survived an independent judge", Hint: "not self-graded"},
}

func TestMissingSlotsNamesOnlyBlankRequiredOnes(t *testing.T) {
	got := MissingSlots(testSlots, map[string]string{
		"market":          "self-hosted time tracking",
		"audience":        "   ", // blank after trim → still missing
		"competitorsHint": "",    // optional → never missing
	})
	if len(got) != 1 || got[0] != "audience" {
		t.Fatalf("MissingSlots = %v, want [audience]", got)
	}

	if got := MissingSlots(testSlots, map[string]string{"market": "m", "audience": "a"}); len(got) != 0 {
		t.Fatalf("MissingSlots with both required filled = %v, want none", got)
	}
}

func TestFilterSlotValuesDropsUndeclaredAndBlank(t *testing.T) {
	got := FilterSlotValues(testSlots, map[string]string{
		"market":   "  time tracking  ",
		"audience": "",
		"smuggled": "not a declared slot",
	})
	if len(got) != 1 || got["market"] != "time tracking" {
		t.Fatalf("FilterSlotValues = %#v, want {market: time tracking}", got)
	}
}

func TestFillSlotsSubstitutesTokens(t *testing.T) {
	spec := JSON(`{
	  "name": "market-research",
	  "nodes": [
	    {"id": "research", "kind": "process", "ref": "pr/market-research",
	     "with": {"domain": "Market: {slot:market}. Audience: {slot:audience}. Known: {slot:competitorsHint}"}}
	  ]
	}`)

	out, err := FillSlots(spec, testSlots, map[string]string{
		"market":   "self-hosted time tracking",
		"audience": "agency owners",
	})
	if err != nil {
		t.Fatalf("FillSlots: %v", err)
	}

	var doc struct {
		Nodes []struct {
			With struct {
				Domain string `json:"domain"`
			} `json:"with"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("filled spec is not valid JSON: %v", err)
	}
	got := doc.Nodes[0].With.Domain
	if !strings.Contains(got, "self-hosted time tracking") || !strings.Contains(got, "agency owners") {
		t.Errorf("slot values not substituted: %q", got)
	}
	if strings.Contains(got, "{slot:") {
		t.Errorf("a literal {slot:…} token survived into the spec: %q", got)
	}
	// An unfilled optional slot must degrade, not leak its token.
	if !strings.Contains(got, "(not declared)") {
		t.Errorf("unfilled optional slot did not render as not declared in prose: %q", got)
	}
}

func TestUnfilledExactBindingIsEmptyNotAWord(t *testing.T) {
	slots := []ContextSlot{{ID: "x", Label: "X"}}
	out, err := FillSlots(JSON(`{"nodes":[{"id":"p","kind":"process","with":{"x":"{slot:x}"}}]}`), slots, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "unspecified") || !strings.Contains(string(out), `"x":""`) {
		t.Fatalf("unfilled carried value must be empty: %s", out)
	}
}

func TestFillSlotsAppendsContextBlockForUnreferencedSlots(t *testing.T) {
	// The DAG names no slot at all, so every filled slot must arrive via the
	// CONTEXT block on the root instruction rather than vanishing.
	spec := JSON(`{
	  "name": "r",
	  "nodes": [
	    {"id": "root", "kind": "agent", "instruction": "Research the market."},
	    {"id": "later", "kind": "agent", "deps": ["root"], "instruction": "Summarize."}
	  ]
	}`)

	out, err := FillSlots(spec, testSlots, map[string]string{
		"market":   "self-hosted time tracking",
		"audience": "agency owners",
	})
	if err != nil {
		t.Fatalf("FillSlots: %v", err)
	}

	var doc struct {
		Nodes []struct {
			ID          string `json:"id"`
			Instruction string `json:"instruction"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("filled spec is not valid JSON: %v", err)
	}
	byID := map[string]string{}
	for _, n := range doc.Nodes {
		byID[n.ID] = n.Instruction
	}
	root := byID["root"]
	if !strings.Contains(root, "CONTEXT") {
		t.Fatalf("root instruction has no CONTEXT block: %q", root)
	}
	if !strings.Contains(root, "Market: self-hosted time tracking") {
		t.Errorf("CONTEXT block is missing the market slot: %q", root)
	}
	if !strings.Contains(root, "Audience: agency owners") {
		t.Errorf("CONTEXT block is missing the audience slot: %q", root)
	}
	if strings.Contains(root, "Competitors you already know") {
		t.Errorf("an unfilled slot leaked into the CONTEXT block: %q", root)
	}
	if strings.Contains(byID["later"], "CONTEXT") {
		t.Errorf("CONTEXT block landed on a non-root node: %q", byID["later"])
	}
}

func TestRecipeDocKeyShape(t *testing.T) {
	// These four key names are a contract with the delivery view.
	doc, err := RecipeDoc("tmpl-123", testCriteria, testSlots, map[string]string{"market": "m"})
	if err != nil {
		t.Fatalf("RecipeDoc: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(doc, &got); err != nil {
		t.Fatalf("RecipeDoc is not valid JSON: %v", err)
	}
	for _, k := range []string{"recipe", "slots", "slotSpecs", "criteria"} {
		if _, ok := got[k]; !ok {
			t.Errorf("RecipeDoc is missing the %q key: %s", k, doc)
		}
	}

	// Empty criteria/slots must still serialize as [] and {}, never null.
	empty, err := RecipeDoc("tmpl-123", nil, nil, nil)
	if err != nil {
		t.Fatalf("RecipeDoc(empty): %v", err)
	}
	if !strings.Contains(string(empty), `"criteria":[]`) || !strings.Contains(string(empty), `"slots":{}`) {
		t.Errorf("empty RecipeDoc serialized nulls: %s", empty)
	}
}

// The frozen request has to read in the order the form asked, and to carry the
// shape of the answer — a table is a table, not the JSON it is stored as.
func TestRecipeDocSlotSpecsAreOrderedAndTyped(t *testing.T) {
	slots := []ContextSlot{
		{ID: "grain", Label: "One row = …", Audience: SlotAudienceEngineer},
		{ID: "question", Label: "The question this has to answer"},
		{ID: "tolerances", Label: "Tolerance per measure", Type: SlotTypeTable,
			Columns: []SlotColumn{{ID: "measure", Label: "Measure"}, {ID: "unit", Label: "Unit"}}},
		{ID: "unanswered", Label: "Left blank"},
	}
	doc, err := RecipeDoc("tmpl-123", nil, slots, map[string]string{
		"grain": "one row per day", "question": "why did Q2 drop?",
		"tolerances": `[{"measure":"net_revenue","unit":"percent_of_a"}]`,
	})
	if err != nil {
		t.Fatalf("RecipeDoc: %v", err)
	}
	var got struct {
		SlotSpecs []RecipeSlotSpec `json:"slotSpecs"`
	}
	if err := json.Unmarshal(doc, &got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	var ids []string
	for _, s := range got.SlotSpecs {
		ids = append(ids, s.ID)
	}
	// Owner slots first, in template order, then the engineer's. An unanswered
	// slot is not part of the request.
	want := []string{"question", "tolerances", "grain"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("slotSpecs order: want %v, got %v", want, ids)
	}
	table := got.SlotSpecs[1]
	if table.Type != SlotTypeTable || len(table.Columns) != 2 || table.Columns[1].Label != "Unit" {
		t.Errorf("a table slot must carry its column headers, got %+v", table)
	}
}

func TestTemplateSlotsAndCriteriaRoundTrip(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	created, err := st.CreateTemplate(ctx, WorkflowTemplate{
		Name:         "recipe-roundtrip-" + uniq(),
		Description:  "round-trip guard",
		Spec:         JSON(`{"name":"r","nodes":[{"id":"a","kind":"agent","instruction":"do {slot:market}"}]}`),
		ContextSlots: testSlots,
		Criteria:     testCriteria,
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	got, err := st.GetTemplate(ctx, created.ID)
	if err != nil {
		t.Fatalf("get template: %v", err)
	}
	if len(got.ContextSlots) != len(testSlots) {
		t.Fatalf("context slots: got %d, want %d", len(got.ContextSlots), len(testSlots))
	}
	if got.ContextSlots[0].ID != "market" || !got.ContextSlots[0].Required ||
		got.ContextSlots[0].Label != "Market" || got.ContextSlots[0].Example != "time tracking" {
		t.Errorf("context slot did not survive the round trip: %+v", got.ContextSlots[0])
	}
	if got.ContextSlots[2].Required {
		t.Errorf("optional slot came back required: %+v", got.ContextSlots[2])
	}
	if len(got.Criteria) != len(testCriteria) || got.Criteria[1].Hint != "not self-graded" {
		t.Errorf("criteria did not survive the round trip: %+v", got.Criteria)
	}
	if !got.IsRecipe() {
		t.Error("template with context slots does not report IsRecipe")
	}

	// A template with no slots must read back as empty arrays, not nulls.
	plain, err := st.CreateTemplate(ctx, WorkflowTemplate{
		Name: "plain-roundtrip-" + uniq(),
		Spec: JSON(`{"name":"p","nodes":[]}`),
	})
	if err != nil {
		t.Fatalf("create plain template: %v", err)
	}
	if plain.ContextSlots == nil || plain.Criteria == nil {
		t.Errorf("plain template returned nil arrays: %+v", plain)
	}
	if plain.IsRecipe() {
		t.Error("template with no context slots reports IsRecipe")
	}
}

func TestInstantiateTemplateValidatesAndPreparesSDKWorkflow(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	tmpl, err := st.CreateTemplate(ctx, WorkflowTemplate{
		Name:         "recipe-instantiate-" + uniq(),
		Spec:         JSON(`{"kind":"nodes","instruction":"Research {slot:market} for {slot:audience}.","nodes":[{"id":"a","title":"Research","instruction":"Research {slot:market}."}]}`),
		ContextSlots: testSlots,
		Criteria:     testCriteria,
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	// Required slots missing → typed error, and nothing written.
	_, err = st.InstantiateTemplate(ctx, tmpl.ID, map[string]string{"market": "time tracking"}, "", true)
	var missing *MissingSlotsError
	if !errors.As(err, &missing) {
		t.Fatalf("instantiate with a blank required slot: got %v, want *MissingSlotsError", err)
	}
	if len(missing.Missing) != 1 || missing.Missing[0] != "audience" {
		t.Fatalf("missing slots = %v, want [audience]", missing.Missing)
	}

	values := map[string]string{
		"market":   "self-hosted time tracking",
		"audience": "agency owners",
		"unknown":  "dropped",
	}
	res, err := st.InstantiateTemplate(ctx, tmpl.ID, values, "", true)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}

	if _, ok := res.Values["unknown"]; ok {
		t.Error("an undeclared slot value reached the run")
	}

	if res.Workflow == nil || res.Workflow.Kind != "nodes" || res.Workflow.Nodes[0].Instruction != "Research self-hosted time tracking." {
		t.Fatalf("expected an editable SDK workflow: %+v", res.Workflow)
	}

	// Preparing for review is the reusable path: the tokens survive and the
	// template's questions become the workflow's own inputs, so saving the
	// result can be run again next week without editing prose.
	prepared, err := st.InstantiateTemplate(ctx, tmpl.ID, values, "", false)
	if err != nil {
		t.Fatalf("prepare without filling: %v", err)
	}
	if prepared.Workflow.Nodes[0].Instruction != "Research {slot:market}." || prepared.Workflow.Instruction != "Research {slot:market} for {slot:audience}." {
		t.Fatalf("preparation baked this run's answers into the steps: %+v", prepared.Workflow)
	}
	if len(prepared.Workflow.Inputs) != len(testSlots) {
		t.Fatalf("prepared workflow did not carry the template's inputs: %+v", prepared.Workflow.Inputs)
	}
	if prepared.Values["market"] != "self-hosted time tracking" {
		t.Fatalf("prepared workflow lost the accepted values: %+v", prepared.Values)
	}
	if len(res.Template.Criteria) != len(testCriteria) {
		t.Fatal("template criteria were lost")
	}

}

func TestSDKInputBindingsPreserveCanonicalTablesAndEmptyOptionals(t *testing.T) {
	slots := []ContextSlot{{ID: "measures", Type: SlotTypeTable, Columns: []SlotColumn{{ID: "measure"}, {ID: "definition"}}}, {ID: "lateArrival"}, {ID: "other"}}
	values := map[string]string{"measures": `[{"measure":"net","definition":"Sum values tagged {slot:other}"}]`, "other": "MUST NOT REPLACE DATA"}
	spec, err := FillSlots(JSON(`{"kind":"nodes","instruction":"Measures: {slot:measures}; late: {slot:lateArrival}","nodes":[{"id":"profile","inputs":{"measures":"{slot:measures}","lateArrival":"{slot:lateArrival}"}}]}`), slots, values)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Instruction string
		Nodes       []struct{ Inputs map[string]string }
	}
	if err := json.Unmarshal(spec, &got); err != nil {
		t.Fatal(err)
	}
	if got.Nodes[0].Inputs["measures"] != NormalizeSlotValue(slots[0], values["measures"]) || got.Nodes[0].Inputs["lateArrival"] != "" {
		t.Fatalf("SDK inputs were formatted as prose: %s", spec)
	}
	if !strings.Contains(got.Instruction, "1. measure: net") || !strings.Contains(got.Instruction, "late: (not declared)") {
		t.Fatalf("Human instructions lost readable formatting: %s", spec)
	}
}
