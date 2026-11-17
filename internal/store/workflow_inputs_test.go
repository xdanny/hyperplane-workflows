package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/sdkworkflow"
)

// A saved workflow that asks for its values keeps asking. Saving stores the
// declarations and the tokens; nothing is substituted and no model is called.
func TestSaveSDKWorkflowKeepsReusableInputs(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()

	w := SDKWorkflow{
		Name:        "Weekly brief " + uniq(),
		Kind:        "nodes",
		Instruction: "Write the brief for {slot:week}.",
		Inputs: []ContextSlot{
			{ID: "week", Label: "Week", Hint: "The Monday–Sunday week the brief covers.", Required: true, Example: "2026-09-07"},
			{ID: "audience", Label: "Audience"},
		},
		Nodes: []sdkworkflow.Node{
			{ID: "write", Title: "Write it", Instruction: "Summarise {slot:week} for the reader."},
		},
	}
	saved, err := st.SaveSDKWorkflow(ctx, w)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if len(saved.Inputs) != 2 || saved.Inputs[0].ID != "week" || !saved.Inputs[0].Required {
		t.Fatalf("declarations were not stored: %+v", saved.Inputs)
	}
	if saved.Instruction != w.Instruction || saved.Nodes[0].Instruction != w.Nodes[0].Instruction {
		t.Fatalf("saving substituted values into the definition: %+v", saved)
	}
	read, err := st.GetSDKWorkflow(ctx, saved.ID)
	if err != nil || len(read.Inputs) != 2 || read.Revision != saved.Revision {
		t.Fatalf("read back: %+v %v", read, err)
	}

	// A workflow that declares nothing still answers with an empty list rather
	// than a null every reader has to guard.
	plain, err := st.SaveSDKWorkflow(ctx, SDKWorkflow{Name: "Plain " + uniq(), Kind: "nodes", Instruction: "Write something.",
		Nodes: []sdkworkflow.Node{{ID: "write", Title: "Write", Instruction: "Write it."}}})
	if err != nil || plain.Inputs == nil || len(plain.Inputs) != 0 {
		t.Fatalf("empty declarations: %+v %v", plain.Inputs, err)
	}
}

// A token naming no declared input is a fixable mistake, and the ids come back
// so the editor can point at each one.
func TestSaveSDKWorkflowRejectsUndeclaredAndMalformedInputs(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := context.Background()
	base := SDKWorkflow{Name: "Rejects " + uniq(), Kind: "nodes", Instruction: "Write the brief.",
		Nodes: []sdkworkflow.Node{{ID: "write", Title: "Write", Instruction: "Write it."}}}

	unknownCases := map[string]func(w *SDKWorkflow){
		"goal":        func(w *SDKWorkflow) { w.Instruction = "Write the brief for {slot:week}." },
		"step":        func(w *SDKWorkflow) { w.Nodes[0].Instruction = "Summarise {slot:week}." },
		"step inputs": func(w *SDKWorkflow) { w.Nodes[0].Inputs = map[string]string{"dataset": "{slot:week}"} },
		"read path": func(w *SDKWorkflow) {
			w.Nodes[0].Kind, w.Nodes[0].Access = "task", &sdkworkflow.TaskAccess{ReadPaths: []string{"{slot:week}"}}
		},
	}
	for name, mutate := range unknownCases {
		t.Run(name, func(t *testing.T) {
			w := base
			w.Name = "Rejects " + uniq()
			w.Nodes = []sdkworkflow.Node{base.Nodes[0]}
			mutate(&w)
			_, err := st.SaveSDKWorkflow(ctx, w)
			var unknown *UnknownInputsError
			if !errors.As(err, &unknown) || len(unknown.Unknown) != 1 || unknown.Unknown[0] != "week" {
				t.Fatalf("undeclared token accepted or misreported: %v", err)
			}
		})
	}

	for name, w := range map[string]SDKWorkflow{
		"duplicate id": {Inputs: []ContextSlot{{ID: "week"}, {ID: "week"}}},
		"bad id":       {Inputs: []ContextSlot{{ID: "the week"}}},
		"unknown type": {Inputs: []ContextSlot{{ID: "week", Type: "spreadsheet"}}},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Name = "Rejects " + uniq()
			candidate.Inputs = w.Inputs
			if _, err := st.SaveSDKWorkflow(ctx, candidate); !errors.Is(err, ErrInvalidWorkflow) {
				t.Fatalf("accepted an invalid declaration: %v", err)
			}
		})
	}

	// A declared input no token references is allowed: FillSlots carries its
	// value in as context rather than dropping it.
	allowed := base
	allowed.Name = "Unreferenced " + uniq()
	allowed.Inputs = []ContextSlot{{ID: "week", Label: "Week"}}
	if _, err := st.SaveSDKWorkflow(ctx, allowed); err != nil {
		t.Fatalf("an unreferenced declaration was refused: %v", err)
	}
}

func TestFillWorkflowSlotsSubstitutesEveryTokenPosition(t *testing.T) {
	w := SDKWorkflow{
		Kind:        "nodes",
		Instruction: "Write the brief for {slot:week}.",
		Inputs: []ContextSlot{
			{ID: "week", Label: "Week", Required: true},
			{ID: "source", Label: "Source file"},
			{ID: "note", Label: "Standing note"},
		},
		Nodes: []sdkworkflow.Node{{
			ID: "read", Kind: "task", Title: "Read the file", Instruction: "Read {slot:source} for {slot:week}.",
			Inputs: map[string]string{"dataset": "{slot:source}"},
			Access: &sdkworkflow.TaskAccess{ReadPaths: []string{"{slot:source}"}},
			Check:  []string{"true"},
		}},
	}

	if _, _, err := FillWorkflowSlots(w, map[string]string{}); err == nil {
		t.Fatal("a missing required input launched anyway")
	} else {
		var missing *MissingSlotsError
		if !errors.As(err, &missing) || len(missing.Missing) != 1 || missing.Missing[0] != "week" {
			t.Fatalf("missing inputs were not named: %v", err)
		}
	}

	filled, accepted, err := FillWorkflowSlots(w, map[string]string{
		"week": "2026-09-07", "source": "/data/sales.csv", "note": "Round to whole units", "stray": "dropped",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := accepted["stray"]; ok {
		t.Error("an undeclared value reached the run")
	}
	if filled.Instruction != "Write the brief for 2026-09-07." {
		t.Fatalf("goal not filled: %q", filled.Instruction)
	}
	node := filled.Nodes[0]
	if !strings.HasPrefix(node.Instruction, "Read /data/sales.csv for 2026-09-07.") {
		t.Fatalf("step instruction not filled: %q", node.Instruction)
	}
	if node.Inputs["dataset"] != "/data/sales.csv" {
		t.Fatalf("step source input not filled: %+v", node.Inputs)
	}
	if node.Access == nil || len(node.Access.ReadPaths) != 1 || node.Access.ReadPaths[0] != "/data/sales.csv" {
		t.Fatalf("readable input not filled: %+v", node.Access)
	}
	// A value no token references is still carried, as context on the root step.
	if !strings.Contains(filled.Nodes[0].Instruction, "Round to whole units") {
		t.Fatalf("an unreferenced value was dropped: %q", filled.Nodes[0].Instruction)
	}
	if len(filled.Inputs) != 3 {
		t.Fatalf("filling lost the declarations: %+v", filled.Inputs)
	}
	// The saved definition itself is untouched: filling is per run.
	if w.Nodes[0].Instruction != "Read {slot:source} for {slot:week}." {
		t.Fatal("filling mutated the saved definition")
	}
}

// A saved project task has no steps to carry a context block, so its goal does.
func TestFillWorkflowSlotsCarriesUnreferencedValuesForTasks(t *testing.T) {
	w := SDKWorkflow{Kind: "task", Instruction: "Update the export.", TargetRoot: "/tmp",
		Inputs: []ContextSlot{{ID: "week", Label: "Week"}}}
	filled, _, err := FillWorkflowSlots(w, map[string]string{"week": "2026-09-07"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filled.Instruction, "2026-09-07") {
		t.Fatalf("a supplied value never reached the task: %q", filled.Instruction)
	}
}
