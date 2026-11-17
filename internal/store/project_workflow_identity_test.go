package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func workflowIdentityFields(t *testing.T, raw JSON) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSDKProjectWorkflowGrouping(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := t.Context()
	project := "sdk-groups-" + uniq()
	request := func(goal, workspace string) JSON {
		raw, _ := json.Marshal(map[string]any{"workflow": "node-workflow", "instruction": goal, "nodes": []any{map[string]any{"id": "write", "title": "Write", "instruction": goal}}, "workspace": workspace, "model": "fixture", "maxTokens": 123})
		return raw
	}
	// These records deliberately lack the new metadata, exercising the existing
	// SDK request fallback, including fresh generated folders on every execution.
	for i, goal := range []string{"Find suppliers", "Prepare interview", "Find suppliers"} {
		options, _ := json.Marshal(map[string]any{"runtime": "codex-sdk", "request": request(goal, "/managed/"+string(rune('a'+i)))})
		id := project + string(rune('a'+i))
		if err := st.CreateRun(ctx, Run{RunID: id, DAG: "node-workflow", Goal: goal, Project: project, State: "passed", Options: options}); err != nil {
			t.Fatal(err)
		}
		// Force a tie so latest membership has a deterministic run-id tiebreaker.
		if _, err := st.Pool.Exec(ctx, `UPDATE runs SET updated_at=$2 WHERE run_id=$1`, id, time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	detail, err := st.GetProjectDetail(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	if detail.WorkflowCount != 2 || len(detail.Workflows) != 2 {
		t.Fatalf("count/detail mismatch: %+v", detail)
	}
	for _, wf := range detail.Workflows {
		if wf.Key == "" || wf.Name == "" {
			t.Fatalf("missing identity: %+v", wf)
		}
		if wf.Name == "Find suppliers" && (wf.Runs != 2 || wf.LastRunID != project+"c") {
			t.Fatalf("wrong repeated group: %+v", wf)
		}
		if wf.Name == "Prepare interview" && wf.Runs != 1 {
			t.Fatalf("wrong distinct group: %+v", wf)
		}
	}
	// Map key order and model/budget defaults do not change organization.
	first, err := st.PrepareProjectWorkflow(ctx, request("Find suppliers", "/other"), "", "", "Find suppliers")
	if err != nil {
		t.Fatal(err)
	}
	for _, wf := range detail.Workflows {
		if wf.Name == "Find suppliers" && wf.Key != workflowIdentityFields(t, first)["key"] {
			t.Fatal("admission/fallback differ")
		}
	}
	a, _ := st.PrepareProjectWorkflow(ctx, JSON(`{"workflow":"checked-task","instruction":"Build","workspace":"/project/a"}`), "", "", "")
	b, _ := st.PrepareProjectWorkflow(ctx, JSON(`{"workflow":"checked-task","instruction":"Build","workspace":"/project/b"}`), "", "", "")
	if workflowIdentityFields(t, a)["key"] == workflowIdentityFields(t, b)["key"] {
		t.Fatal("in-place project targets collapsed")
	}
	// Name alone is never identity; actual instructions and correction are.
	corrected, _ := st.PrepareProjectWorkflow(ctx, JSON(`{"workflow":"node-workflow","instruction":"Find suppliers","nodes":[{"id":"write","title":"Write","instruction":"Find suppliers"}],"autoRepair":true}`), "", "Find suppliers", "")
	if workflowIdentityFields(t, corrected)["key"] == workflowIdentityFields(t, first)["key"] {
		t.Fatal("correction setting ignored")
	}
}

func TestSavedProjectWorkflowAssociation(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	ctx := t.Context()
	one, err := st.SaveSDKWorkflow(ctx, SDKWorkflow{Name: "First", Kind: "app-ideator", Instruction: "Original"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := st.SaveSDKWorkflow(ctx, SDKWorkflow{Name: "Second", Kind: "app-ideator", Instruction: "Original"})
	if err != nil {
		t.Fatal(err)
	}
	request := JSON(`{"workflow":"node-workflow","instruction":"Unsaved edit","nodes":[{"id":"write","title":"Write","instruction":"Do current work"}]}`)
	a, err := st.PrepareProjectWorkflow(ctx, request, one.ID, "Edited title", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.PrepareProjectWorkflow(ctx, request, two.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	first, second := workflowIdentityFields(t, a), workflowIdentityFields(t, b)
	if first["key"] == second["key"] || first["definitionKey"] != second["definitionKey"] || first["name"] != "Edited title" || second["name"] != "Second" {
		t.Fatalf("wrong source grouping: %s %s", a, b)
	}
	one.Name = "Renamed"
	one.Instruction = "New saved definition"
	one, err = st.SaveSDKWorkflow(ctx, one)
	if err != nil {
		t.Fatal(err)
	}
	edit, err := st.PrepareProjectWorkflow(ctx, JSON(`{"workflow":"app-ideator","domain":"Another unsaved edit"}`), one.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if workflowIdentityFields(t, edit)["key"] != first["key"] || workflowIdentityFields(t, edit)["definitionKey"] == first["definitionKey"] {
		t.Fatal("source revisions did not retain identity")
	}
	project := "saved-sdk-groups-" + uniq()
	for i, item := range []struct{ identity, request JSON }{{a, request}, {b, request}, {edit, JSON(`{"workflow":"app-ideator","domain":"Another unsaved edit"}`)}} {
		options, err := json.Marshal(map[string]any{"runtime": "codex-sdk", "request": item.request, "projectWorkflow": item.identity})
		if err != nil {
			t.Fatal(err)
		}
		id := project + string(rune('a'+i))
		if err := st.CreateRun(ctx, Run{RunID: id, DAG: "node-workflow", Goal: "Same display label", Project: project, State: "passed", Options: options}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Pool.Exec(ctx, `UPDATE runs SET updated_at=$2 WHERE run_id=$1`, id, time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	detail, err := st.GetProjectDetail(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	if detail.WorkflowCount != 2 || len(detail.Workflows) != 2 {
		t.Fatalf("saved workflows collapsed: %+v", detail)
	}
	for _, wf := range detail.Workflows {
		switch wf.Key {
		case first["key"]:
			if wf.Runs != 2 || wf.Name != "Renamed" || wf.LastRunID != project+"c" {
				t.Fatalf("edited source lost grouping/latest label: %+v", wf)
			}
		case second["key"]:
			if wf.Runs != 1 || wf.Name != "Second" || wf.LastRunID != project+"b" {
				t.Fatalf("other source lost grouping/link: %+v", wf)
			}
		default:
			t.Fatalf("unexpected group: %+v", wf)
		}
	}
	if err := st.DeleteSDKWorkflow(ctx, one.ID, one.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PrepareProjectWorkflow(ctx, request, one.ID, "", ""); err == nil {
		t.Fatal("deleted source admitted")
	}
	options, _ := json.Marshal(map[string]any{"runtime": "codex-sdk", "request": request, "projectWorkflow": a})
	frozen, err := st.ProjectWorkflowIdentity(ctx, options, "node-workflow", "ignored after admission", nil)
	if err != nil {
		t.Fatal(err)
	}
	if workflowIdentityFields(t, frozen)["name"] != "Edited title" || workflowIdentityFields(t, frozen)["key"] != first["key"] {
		t.Fatal("source deletion changed frozen identity")
	}
	if _, err := st.PrepareProjectWorkflow(ctx, request, "", strings.Repeat("é", 81), ""); err == nil {
		t.Fatal("accepted more than 160 bytes")
	}
}
