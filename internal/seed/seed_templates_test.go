package seed_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/seed"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

// TestDefaultTemplatesIncludeDomainWorkflows verifies that the four M27 domain
// workflow DAGs are present in the seeded DefaultTemplates list.
func TestDefaultTemplatesIncludeDomainWorkflows(t *testing.T) {
	wantNames := []string{"trip-planning", "deep-research", "interview-prep", "finance-review"}

	got := make(map[string]bool, len(seed.DefaultTemplates))
	for _, t2 := range seed.DefaultTemplates {
		got[t2.Name] = true
	}

	for _, name := range wantNames {
		if !got[name] {
			t.Errorf("DefaultTemplates: missing domain workflow template %q", name)
		}
	}
}

// TestDefaultTemplatesHaveSpecs verifies every DefaultTemplate has a non-empty
// Spec (so it can be instantiated as a draft).
func TestDefaultTemplatesHaveSpecs(t *testing.T) {
	for _, tmpl := range seed.DefaultTemplates {
		if len(tmpl.Spec) == 0 || string(tmpl.Spec) == "{}" {
			t.Errorf("DefaultTemplates[%q]: Spec is empty or trivial", tmpl.Name)
		}
		if tmpl.Description == "" {
			t.Errorf("DefaultTemplates[%q]: Description is empty", tmpl.Name)
		}
	}
}

// The catalog returns the same SDK definition that the visual editor saves.
func TestDataPipelineRecipeUsesSDK(t *testing.T) {
	for _, template := range seed.DefaultTemplates {
		if template.Name != "data-pipeline" {
			continue
		}
		var workflow store.SDKWorkflow
		if err := json.Unmarshal(template.Spec, &workflow); err != nil {
			t.Fatal(err)
		}
		if workflow.Kind != "nodes" || len(workflow.Nodes) != 7 {
			t.Fatalf("not an SDK recipe: %s", template.Spec)
		}
		for index, acceptance := range map[int]string{0: "data-pipeline-profile", 1: "data-pipeline-rules", 4: "data-pipeline-audit"} {
			if workflow.Nodes[index].Acceptance != acceptance {
				t.Fatalf("step %d: %+v", index, workflow.Nodes[index])
			}
		}
		if workflow.Nodes[3].Kind != "approval" {
			t.Fatal("build needs explicit plan approval")
		}
		for _, index := range []int{2, 6} {
			if workflow.Nodes[index].Kind != "check" {
				t.Fatalf("missing independent review at %d", index)
			}
		}
		for _, slot := range template.ContextSlots {
			if workflow.Nodes[0].Inputs[slot.ID] != "{slot:"+slot.ID+"}" {
				t.Fatalf("slot %s does not reach SDK input", slot.ID)
			}
			if slot.ID == "measures" && (slot.Type != store.SlotTypeTable || len(slot.Columns) != 4) {
				t.Fatal("measures need definitions and individual tolerance fields")
			}
		}
		return
	}
	t.Fatal("missing data-pipeline recipe")
}

// TestRecipeSlotsAreFillableAndUsed guards the two ways a context slot goes
// wrong quietly. A slot with no hint or example asks a non-engineer for a value
// they have no way to write — and the run is BLOCKED until they write it. A slot
// the spec never substitutes reaches the wrapped process only as an appended
// context block, so a typed process input silently arrives unset while the
// composer's form still demanded it.
func TestRecipeSlotsAreFillableAndUsed(t *testing.T) {
	for _, tmpl := range seed.DefaultTemplates {
		if len(tmpl.ContextSlots) == 0 {
			continue
		}
		spec := string(tmpl.Spec)
		for _, s := range tmpl.ContextSlots {
			if s.ID == "" || s.Label == "" {
				t.Errorf("recipe %q: slot %+v needs an id and a label", tmpl.Name, s)
				continue
			}
			if s.Hint == "" {
				t.Errorf("recipe %q: slot %q has no hint — the composer form would ask for it with no guidance", tmpl.Name, s.ID)
			}
			if s.Example == "" {
				t.Errorf("recipe %q: slot %q has no example — the fastest way to show what a good answer looks like", tmpl.Name, s.ID)
			}
			if !strings.Contains(spec, "{slot:"+s.ID+"}") {
				t.Errorf("recipe %q: slot %q is never substituted into the spec — the wrapped process will not receive it as a typed input", tmpl.Name, s.ID)
			}
		}
	}
}

// TestDefaultCategoriesIncludeDomainCategories verifies the planning/research/decision
// categories were added alongside the existing ones.
func TestDefaultCategoriesIncludeDomainCategories(t *testing.T) {
	wantCats := []string{"planning", "research", "decision"}
	got := make(map[string]bool, len(seed.DefaultCategories))
	for _, c := range seed.DefaultCategories {
		got[c.Name] = true
	}
	for _, name := range wantCats {
		if !got[name] {
			t.Errorf("DefaultCategories: missing category %q", name)
		}
	}
}

// dataRecipes are the five data-engineering recipes. They are the ones a data
// engineer would hand to their team, and the ones whose inputs were graded
// NOT ADOPT-GRADE: every slot free text, no field of its own for the key
// columns, and a grain box that invited the exact answer the verifier rejects.
var dataRecipes = []string{
	"data-pipeline",
	"pipeline-change",
	"data-quality-audit",
	"dataset-shift-audit",
	"dashboard-reconciliation",
}

// TestDataRecipesDeclareKeyColumns is the highest-yield guard on this side.
//
// A run of data-quality-audit declared a grain of "one row per trip record as
// landed" — no key at all — its inputs gate accepted it, and the run then
// failed five attempts later at rules-verify: "these expectations belong only
// in unexpressible[] until key columns are declared." The screen invited the
// answer the verifier refuses. Every data recipe must now ask for the key in a
// field of its own, typed as a list, and required.
func TestDataRecipesDeclareKeyColumns(t *testing.T) {
	byName := make(map[string]store.WorkflowTemplate, len(seed.DefaultTemplates))
	for _, tmpl := range seed.DefaultTemplates {
		byName[tmpl.Name] = tmpl
	}

	for _, name := range dataRecipes {
		tmpl, ok := byName[name]
		if !ok {
			t.Errorf("DefaultTemplates: missing data recipe %q", name)
			continue
		}
		var slot store.ContextSlot
		found := false
		for _, s := range tmpl.ContextSlots {
			if s.ID == "keyColumns" {
				slot, found = s, true
			}
		}
		if !found {
			t.Errorf("recipe %q: no keyColumns slot — the key would be folded back into prose", name)
			continue
		}
		if !slot.Required {
			t.Errorf("recipe %q: keyColumns is optional — an audit with no key is what this slot exists to stop", name)
		}
		if slot.SlotType() != store.SlotTypeList {
			t.Errorf("recipe %q: keyColumns is type %q, want %q", name, slot.SlotType(), store.SlotTypeList)
		}
		if slot.SlotAudience() != store.SlotAudienceEngineer {
			t.Errorf("recipe %q: keyColumns is addressed to %q — column names are an engineer's answer", name, slot.SlotAudience())
		}
		// The no-natural-key affordance has to be ON THE FORM. Without it the
		// person with no natural key has no legal answer and either invents one
		// or gives up.
		if !strings.Contains(slot.Hint, "none") {
			t.Errorf("recipe %q: the keyColumns hint never mentions the `none, <candidates>` answer:\n%s", name, slot.Hint)
		}
		if !strings.Contains(string(tmpl.Spec), "{slot:keyColumns}") {
			t.Errorf("recipe %q: keyColumns is never substituted into the spec — the process would not receive it", name)
		}

		// M-14: the grain slot must stop inviting a key. One contract, one copy.
		for _, s := range tmpl.ContextSlots {
			if s.ID != "grain" && s.ID != "sharedGrain" {
				continue
			}
			if strings.Contains(strings.ToLower(s.Label), "key") {
				t.Errorf("recipe %q: slot %q is still labelled %q — grain is prose, keys are the typed slot",
					name, s.ID, s.Label)
			}
			if !strings.Contains(s.Hint, "Key columns") {
				t.Errorf("recipe %q: the %q hint does not point at the Key columns slot, so the two invite the same answer:\n%s",
					name, s.ID, s.Hint)
			}
		}
	}
}

// TestSlotTypesAndAudiencesAreKnown: an unrecognised value silently falls back
// to the default, so a typo in `audience` would quietly put an engineer's
// question back under "From you".
func TestSlotTypesAndAudiencesAreKnown(t *testing.T) {
	types := map[string]bool{
		"": true, store.SlotTypeText: true, store.SlotTypeList: true,
		store.SlotTypeDate: true, store.SlotTypeNumber: true, store.SlotTypeTable: true,
	}
	audiences := map[string]bool{
		"": true, store.SlotAudienceOwner: true, store.SlotAudienceEngineer: true,
	}
	for _, tmpl := range seed.DefaultTemplates {
		for _, s := range tmpl.ContextSlots {
			if !types[s.Type] {
				t.Errorf("recipe %q: slot %q has unknown type %q", tmpl.Name, s.ID, s.Type)
			}
			if !audiences[s.Audience] {
				t.Errorf("recipe %q: slot %q has unknown audience %q", tmpl.Name, s.ID, s.Audience)
			}
		}
	}
}

// TestTableSlotsDeclareTheirColumns is the guard on the shape three reviewers
// asked for independently. A `table` slot with no `columns` renders as a raw
// textarea and stores whatever is typed — which is the prose box it replaced
// with a new type name on it. Every column also needs a label, because the
// header is the only place the person filling the row is told what the cell is.
//
// store.ValidateContextSlots is the rule; this test is where it is enforced for
// the shipped recipes, so a slot added later fails the gate and not the form.
func TestTableSlotsDeclareTheirColumns(t *testing.T) {
	for _, tmpl := range seed.DefaultTemplates {
		for _, p := range store.ValidateContextSlots(tmpl.ContextSlots) {
			t.Errorf("recipe %q: %s", tmpl.Name, p)
		}
	}
}

// TestTableSlotExamplesAreRows: a table slot's example is pasted into the grid,
// so it has to BE rows. An example written as prose teaches the shape the slot
// was typed to stop, and leaves a required cell with nothing to copy.
func TestTableSlotExamplesAreRows(t *testing.T) {
	for _, tmpl := range seed.DefaultTemplates {
		for _, s := range tmpl.ContextSlots {
			if s.SlotType() != store.SlotTypeTable {
				continue
			}
			rows := store.SlotTableRows(s, s.Example)
			if len(rows) == 0 {
				t.Errorf("recipe %q: table slot %q has an example that is not rows: %s", tmpl.Name, s.ID, s.Example)
				continue
			}
			for _, c := range s.Columns {
				if !c.Required {
					continue
				}
				if strings.TrimSpace(rows[0][c.ID]) == "" {
					t.Errorf("recipe %q: table slot %q example leaves required column %q empty",
						tmpl.Name, s.ID, c.ID)
				}
			}
		}
	}
}

// TestDataRecipesSplitTheirSlotsByAudience is Priya's ask. Three of seven
// required slots on dashboard-reconciliation were an engineer's boxes with the
// owner's name on them, and the form gave no way to tell. Every data recipe
// must have BOTH audiences represented, or the grouping says nothing.
func TestDataRecipesSplitTheirSlotsByAudience(t *testing.T) {
	byName := make(map[string]store.WorkflowTemplate, len(seed.DefaultTemplates))
	for _, tmpl := range seed.DefaultTemplates {
		byName[tmpl.Name] = tmpl
	}
	for _, name := range dataRecipes {
		tmpl, ok := byName[name]
		if !ok {
			continue
		}
		var owner, engineer int
		for _, s := range tmpl.ContextSlots {
			if s.SlotAudience() == store.SlotAudienceEngineer {
				engineer++
			} else {
				owner++
			}
		}
		if engineer == 0 {
			t.Errorf("recipe %q: no slot is marked as an engineer's — the form cannot say which half is not yours", name)
		}
		if owner == 0 {
			t.Errorf("recipe %q: every slot is an engineer's, so the owner has nothing to fill in", name)
		}
	}
}

// Examples are complete owner declarations, not merely placeholder-shaped data.
func TestTemplateExamplesPassSDKValidation(t *testing.T) {
	for _, template := range seed.DefaultTemplates {
		t.Run(template.Name, func(t *testing.T) {
			values := map[string]string{}
			for _, slot := range template.ContextSlots {
				values[slot.ID] = slot.Example
			}
			if missing := store.MissingSlots(template.ContextSlots, values); len(missing) > 0 {
				t.Fatalf("incomplete example declarations: %v", missing)
			}
			raw, err := store.FillSlots(template.Spec, template.ContextSlots, values)
			if err != nil {
				t.Fatal(err)
			}
			var workflow store.SDKWorkflow
			if err = json.Unmarshal(raw, &workflow); err != nil {
				t.Fatal(err)
			}
			if err = sdkworkflow.Validate(context.Background(), workflow.Instruction, workflow.Nodes); err != nil {
				t.Fatal(err)
			}
		})
	}
}
