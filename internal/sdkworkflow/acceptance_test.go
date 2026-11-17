package sdkworkflow

import (
	"context"
	"testing"
)

func TestProfileInputsAndCurrentRuleApproval(t *testing.T) {
	for _, family := range []string{"data-quality", "dataset-shift", "reconciliation", "pipeline-change", "data-pipeline"} {
		t.Run(family, func(t *testing.T) {
			inputs := map[string]string{"dataset": "/data/source.csv", "keyColumns": "id"}
			if family == "dataset-shift" {
				inputs = map[string]string{"baselineData": "/data/before.csv", "currentData": "/data/after.csv", "keyColumns": "id", "features": `[{"feature":"category","type":"categorical","whyItMatters":"Report totals","statistic":"category-emergence","tolerance":2,"unit":"points_of_share"}]`}
			}
			if family == "reconciliation" {
				inputs = map[string]string{"sourceA": "/data/a.csv", "sourceB": "/data/b.csv", "sharedGrain": "one row per id", "keyColumns": "id", "measures": `[{"measure":"total","definitionA":"Sum amount","definitionB":"Sum amount"}]`, "tolerances": `[{"measure":"total","unit":"exact","value":"0"}]`}
			}
			if family == "pipeline-change" {
				inputs = map[string]string{"pipelineLocation": "Source reference", "runCommand": "python3 pipeline.py", "changeRequest": "Requested change", "invariants": `[{"invariant":"total","measure":"Sum amount","kind":"number","unit":"exact","tolerance":"0"}]`, "grain": "one row per id", "keyColumns": "id", "safeUse": "No sensitive outputs"}
			}
			if family == "data-pipeline" {
				inputs = map[string]string{"sources": "/data/orders.csv", "targetStore": "SQLite export", "grain": "one row per day", "keyColumns": "order_day", "measures": `[{"measure":"total","definition":"Sum amount","unit":"exact","tolerance":"0"}]`, "maxLag": "3h", "safeUse": "No sensitive outputs", "lateArrival": "", "refreshDeadline": "", "runtimeBudget": "", "handover": ""}
			}
			p := Node{ID: "profile", Kind: "task", Title: "Profile", Instruction: "Measure", Acceptance: family + "-profile", Inputs: inputs}
			r := Node{ID: "rules", Kind: "task", Title: "Rules", Instruction: "Propose", Acceptance: family + "-rules"}
			a := Node{ID: "approve", Kind: "approval", Title: "Approve", Instruction: "Review rules"}
			x := Node{ID: "audit", Kind: "task", Title: "Audit", Instruction: "Execute", Acceptance: family + "-audit"}
			if err := Validate(context.Background(), "Compare", []Node{p, r, a, x}); err != nil {
				t.Fatal(err)
			}
			newProfile := p
			newProfile.ID = "new-profile"
			for _, nodes := range [][]Node{{r}, {p, x}, {p, a, r, x}, {p, r, a, newProfile, x}} {
				if err := Validate(context.Background(), "Compare", nodes); err == nil {
					t.Fatal("accepted missing or obsolete rule approval")
				}
			}
			inputs["undeclared"] = "value"
			if err := Validate(context.Background(), "Compare", []Node{p}); err == nil {
				t.Fatal("accepted an unknown profile field")
			}
			delete(inputs, "undeclared")
			delete(inputs, "keyColumns")
			if err := Validate(context.Background(), "Compare", []Node{p}); err == nil {
				t.Fatal("accepted a missing source declaration")
			}
		})
	}
}

func TestGeneralTaskAllowsOmittedCheckButRejectsMalformedExplicitCheck(t *testing.T) {
	base := Node{ID: "write", Kind: "task", Title: "Write a file", Instruction: "Create a CSV containing the supplied rows"}
	if err := Validate(context.Background(), "Prepare the data", []Node{base}); err != nil {
		t.Fatal(err)
	}
	for _, check := range [][]string{{}, {" "}, {"echo", "bad\x00arg"}} {
		node := base
		node.Check = check
		if err := Validate(context.Background(), "Prepare the data", []Node{node}); err == nil {
			t.Fatalf("accepted explicit invalid check %#v", check)
		}
	}
	node := base
	node.Acceptance = "invented-check"
	if err := Validate(context.Background(), "Prepare the data", []Node{node}); err == nil {
		t.Fatal("accepted unknown check")
	}
	node = base
	node.Inputs = map[string]string{"source": "value"}
	if err := Validate(context.Background(), "Prepare the data", []Node{node}); err == nil {
		t.Fatal("accepted unregistered input schema")
	}
}
