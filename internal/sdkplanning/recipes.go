package sdkplanning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/seed"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

type slotValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func checkedRecipes() []store.WorkflowTemplate {
	var recipes []store.WorkflowTemplate
	for _, t := range seed.DefaultTemplates {
		switch t.Name {
		case "data-quality-audit", "dataset-shift-audit", "dashboard-reconciliation", "pipeline-change", "data-pipeline", "ml-engineering":
			recipes = append(recipes, t)
		}
	}
	return recipes
}

func recipeInstructions() string {
	type choice struct {
		Name        string              `json:"name"`
		Description string              `json:"description"`
		Slots       []store.ContextSlot `json:"slots"`
	}
	var choices []choice
	for _, t := range checkedRecipes() {
		slots := append([]store.ContextSlot{}, t.ContextSlots...)
		for i := range slots {
			slots[i].Example = ""
			if slots[i].ID == "keyColumns" {
				slots[i].Hint = "Return ONLY the actual identifying column names, separated by commas. Do not include prose such as derived from, labels, or explanations. A derived key like order_day is named order_day; its derivation stays in the original goal. Preserve spaces only when part of an actual column name."
			}
			switch slots[i].ID {
			case "dataset", "baselineData", "currentData", "sourceA", "sourceB", "sources", "pipelineLocation", "runCommand", "dataDescription":
				slots[i].Hint = "Copy the raw source reference or command exactly from the brief as a contiguous substring, without labels, Markdown or added prose. Context stays in the original goal."
			}
		}
		choices = append(choices, choice{t.Name, t.Description, slots})
	}
	raw, _ := json.Marshal(choices)
	return string(raw)
}

func expandRecipe(ctx context.Context, brief, name string, slots []slotValue) (string, []sdkworkflow.Node, error) {
	var template *store.WorkflowTemplate
	for _, t := range checkedRecipes() {
		if t.Name == name {
			copy := t
			template = &copy
			break
		}
	}
	if template == nil {
		return "", nil, fmt.Errorf("choose a supported checked-work recipe")
	}
	allowed := map[string]store.ContextSlot{}
	for _, slot := range template.ContextSlots {
		allowed[slot.ID] = slot
	}
	values := map[string]string{}
	for _, slot := range slots {
		spec, ok := allowed[slot.Name]
		if !ok || len(slot.Value) > 16000 {
			return "", nil, fmt.Errorf("invalid proposed input: %s", slot.Name)
		}
		if _, exists := values[slot.Name]; exists {
			return "", nil, fmt.Errorf("duplicate proposed input: %s", slot.Name)
		}
		switch slot.Name {
		case "dataset", "baselineData", "currentData", "sourceA", "sourceB", "sources", "pipelineLocation", "runCommand", "dataDescription":
			if slot.Value != "" && !strings.Contains(brief, slot.Value) {
				return "", nil, fmt.Errorf("proposed %s must quote the source or command supplied in the goal", slot.Name)
			}
		}
		if spec.SlotType() == store.SlotTypeTable && strings.TrimSpace(slot.Value) != "" {
			var rows []map[string]string
			if err := json.Unmarshal([]byte(slot.Value), &rows); err != nil {
				return "", nil, fmt.Errorf("proposed %s needs a table of string values", slot.Name)
			}
			columns := map[string]bool{}
			for _, column := range spec.Columns {
				columns[column.ID] = true
			}
			for _, row := range rows {
				for key := range row {
					if !columns[key] {
						return "", nil, fmt.Errorf("unknown %s column: %s", slot.Name, key)
					}
				}
			}
		}
		values[slot.Name] = slot.Value
	}
	if missing := store.MissingSlots(template.ContextSlots, values); len(missing) > 0 {
		return "", nil, fmt.Errorf("supply the missing recipe inputs: %s", strings.Join(missing, ", "))
	}
	filled, err := store.FillSlots(template.Spec, template.ContextSlots, values)
	if err != nil {
		return "", nil, err
	}
	var workflow store.SDKWorkflow
	if err = json.Unmarshal(filled, &workflow); err != nil {
		return "", nil, err
	}
	instruction := brief + "\n\nREVIEWED RECIPE INPUTS:\n" + workflow.Instruction
	if err = sdkworkflow.Validate(ctx, instruction, workflow.Nodes); err != nil {
		return "", nil, err
	}
	return instruction, workflow.Nodes, nil
}
