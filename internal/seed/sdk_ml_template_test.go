package seed_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
	"github.com/xdanny/hyperplane-workflows/internal/seed"
	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestMLTemplateUsesReviewedSDKTasks(t *testing.T) {
	var template store.WorkflowTemplate
	for _, item := range seed.DefaultTemplates {
		if item.Name == "ml-engineering" {
			template = item
		}
	}
	if template.Name == "" {
		t.Fatal("ML template missing")
	}
	values := map[string]string{}
	var required []string
	for _, slot := range template.ContextSlots {
		values[slot.ID] = slot.Example
		if slot.Required {
			required = append(required, slot.ID)
		}
	}
	if !reflect.DeepEqual(required, []string{"task", "predictionTarget", "dataDescription", "splitPolicy", "successMetric"}) {
		t.Fatalf("owner declarations changed: %v", required)
	}
	for _, id := range required {
		value := values[id]
		delete(values, id)
		if missing := store.MissingSlots(template.ContextSlots, values); !reflect.DeepEqual(missing, []string{id}) {
			t.Fatalf("missing %s admitted: %v", id, missing)
		}
		values[id] = value
	}
	spec, err := store.FillSlots(template.Spec, template.ContextSlots, values)
	if err != nil {
		t.Fatal(err)
	}
	var workflow store.SDKWorkflow
	if err := json.Unmarshal(spec, &workflow); err != nil {
		t.Fatal(err)
	}
	if workflow.Kind != "nodes" || len(workflow.Nodes) != 7 || len(template.Criteria) != 0 {
		t.Fatalf("legacy template shape: %s", spec)
	}
	if err := sdkworkflow.Validate(context.Background(), workflow.Instruction, workflow.Nodes); err != nil {
		t.Fatal(err)
	}
	kinds := []string{"task", "check", "approval", "task", "check", "document", "check"}
	for i, node := range workflow.Nodes {
		if node.Kind != kinds[i] {
			t.Fatalf("step %d: %s", i, node.Kind)
		}
		if node.Access != nil || node.Toolchain != nil || node.SideEffect != nil || node.Check != nil {
			t.Fatalf("step grants unreviewed execution settings: %+v", node)
		}
		if i != 3 && (node.Acceptance != "" || node.Inputs != nil) {
			t.Fatalf("extra specialized contract at %d: %+v", i, node)
		}
	}
	evaluation := workflow.Nodes[3]
	if evaluation.Acceptance != "prediction-metrics" || len(evaluation.Inputs) != 2 || !strings.Contains(evaluation.Inputs["successMetric"], "PR-AUC") || evaluation.Inputs["dataDescription"] != values["dataDescription"] {
		t.Fatalf("owner facts lost at checker: %+v", evaluation)
	}
	if strings.Contains(string(spec), "{slot:") {
		t.Fatal("unfilled owner declaration")
	}
	// The runtime executes ordinary tasks; critical methodology and limits are
	// editable instructions, while metric arithmetic is the attached check.
	for _, phrase := range []string{"training rows only", "same evaluation rows", "sh checks.sh", "do not create it yourself"} {
		if !strings.Contains(evaluation.Instruction, phrase) {
			t.Errorf("evaluation lost %q", phrase)
		}
	}
	if !strings.Contains(workflow.Instruction, "does not prove") || !strings.Contains(workflow.Instruction, "do not grant") {
		t.Fatal("custody or permission limits missing")
	}
}

func TestAllShippedTemplatesUseSDKDefinitions(t *testing.T) {
	for _, template := range seed.DefaultTemplates {
		var workflow store.SDKWorkflow
		if err := json.Unmarshal(template.Spec, &workflow); err != nil {
			t.Fatal(err)
		}
		if workflow.Kind != "nodes" {
			t.Errorf("%s still requires graph template execution", template.Name)
		}
	}
}
